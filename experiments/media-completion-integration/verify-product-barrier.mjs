// Product public HTTPS + real PostgreSQL + patched MediaMTX barrier. Run only
// with compose.barriers.yml and a separate B publisher already holding LIVE.
import {chromium} from '../media-single-rendition/node_modules/playwright/index.mjs';
import crypto,{createHmac,X509Certificate} from 'node:crypto';
import {readFileSync} from 'node:fs';
import tls from 'node:tls';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {dbCompletion,dbRun,ffmpegExists,mediaList} from './verify-browser.mjs';

const execFileAsync=promisify(execFile);
const origin='https://localhost:19543',management='http://127.0.0.1:29998/v3/wbb',proxy='http://127.0.0.1:29990';
const stage=process.argv[2];
const stages=new Set(['before_register','before_session_initialize','during_auth','before_add_publisher','during_rtp_callback']);
if(!stages.has(stage))throw new Error('unsupported product barrier stage');
const env=Object.fromEntries(readFileSync(new URL('./.env',import.meta.url),'utf8').trim().split('\n').map(line=>{const at=line.indexOf('=');return [line.slice(0,at),line.slice(at+1)]}));
const key=Buffer.from(env.WBB_COMPLETION_KEY_HEX,'hex'),root=env.WBB_COMPLETION_ROOT,slot=env.WBB_COMPLETION_SLOT;
if(key.length!==32||!root||!slot)throw new Error('missing private test configuration');
const pause=ms=>new Promise(resolve=>setTimeout(resolve,ms));
function sign(...parts){const h=createHmac('sha256',key);for(const part of parts)h.update(`${Buffer.byteLength(part)}:${part}`);return h.digest('hex')}
async function bounded(label,ms,fn){let timer;try{return await Promise.race([Promise.resolve().then(fn),new Promise((_,reject)=>timer=setTimeout(()=>reject(new Error(`${label} timeout`)),ms))])}finally{clearTimeout(timer)}}
async function until(label,ms,fn){const end=Date.now()+ms;while(Date.now()<end){const value=await fn();if(value)return value;await pause(150)}throw new Error(`${label} timeout`)}
async function leafPin(){return new Promise((resolve,reject)=>{const socket=tls.connect({host:'localhost',port:19543,servername:'localhost',rejectUnauthorized:false},()=>{try{const cert=new X509Certificate(socket.getPeerCertificate(true).raw);resolve(crypto.createHash('sha256').update(cert.publicKey.export({type:'spki',format:'der'})).digest('base64'))}catch(error){reject(error)}finally{socket.end()}});socket.setTimeout(3000,()=>{socket.destroy();reject(new Error('TLS pin timeout'))});socket.on('error',reject)})}
async function lifecycle(){const response=await fetch(`${management}/lifecycle`,{signal:AbortSignal.timeout(4000),headers:{'X-WBB-Signature':sign('lifecycle',root,slot,'','')}});if(response.status!==200)throw new Error(`lifecycle ${response.status}`);const value=await response.json();if(value.root!==root||value.slot!==slot||value.contract_version!=='wbb-media-completion/1'||!Number.isInteger(value.epoch_sequence))throw new Error('invalid lifecycle');return value}
async function barrier(epoch,id,action){const purpose=`barrier-${action||'query'}`;const response=await fetch(`${management}/barriers/${stage}/${id}${action?`/${action}`:''}`,{method:action?'POST':'GET',signal:AbortSignal.timeout(4000),headers:{'X-WBB-Epoch':epoch,'X-WBB-Signature':sign(purpose,root,slot,epoch,stage,id)}});if(response.status!==200)throw new Error(`barrier ${action||'query'} ${response.status}`);return response.json()}
async function productSQL(sql){const {stdout}=await execFileAsync('docker',['exec','wbb-media-completion-integration-postgres-1','psql','-v','ON_ERROR_STOP=1','-U','wbb','-d','wbb','-At','-c',sql],{timeout:5000});return stdout.trim()}
async function state(sid,id){if(!/^[0-9a-f-]{36}$/.test(sid)||!/^[0-9a-f-]{36}$/.test(id))throw new Error('unsafe ID');const raw=await productSQL(`SELECT s.state||','||o.state||','||COALESCE(o.completion_epoch,'')||','||COALESCE(o.completion_epoch_sequence::text,'') FROM broadcast_sessions s JOIN media_create_operations o ON o.broadcast_session_id=s.id WHERE s.id='${sid}' AND o.id='${id}'`);const [session,operation,epoch,sequence]=raw.split(',');return {session,operation,epoch,sequence:Number(sequence)}}
async function proxyCall(path,options={}){const response=await fetch(proxy+path,{...options,signal:AbortSignal.timeout(4000)});if(!response.ok)throw new Error(`proxy ${path} ${response.status}`);return response.status===204?null:response.json()}
async function setupPage(page){const email=`barrier-${crypto.randomUUID()}@example.invalid`,password=crypto.randomBytes(24).toString('hex');return page.evaluate(async({email,password})=>{const call=async(method,path,body,token,extra={})=>{const response=await fetch(path,{method,credentials:'include',signal:AbortSignal.timeout(7000),headers:{...(body?{'Content-Type':'application/json'}:{}),...(token?{Authorization:`Bearer ${token}`}:{}) ,...extra},body:body?JSON.stringify(body):undefined});const value=await response.json();return {status:response.status,value}};const registered=await call('POST','/api/v1/auth/register',{email,password,displayName:'Barrier Test'});if(registered.status!==201)throw new Error(`register ${registered.status}`);const login=await call('POST','/api/v1/auth/login',{email,password});if(login.status!==200)throw new Error(`login ${login.status}`);const access=login.value.accessToken;const created=await call('POST','/api/v1/broadcasts',{title:'Product barrier',description:'Isolated'},access);if(created.status!==201)throw new Error(`broadcast ${created.status}`);const bid=created.value.broadcast.id;const start=await call('POST',`/api/v1/broadcasts/${bid}/sessions`,{source:'screen'},access,{'Idempotency-Key':`barrier-${crypto.randomUUID()}`});if(start.status!==202)throw new Error(`prepare ${start.status}`);window.__barrier={access,bid,sid:start.value.session.id,grant:start.value.publishGrant};return {bid,sid:start.value.session.id,access}}, {email,password})}
async function prepareMedia(page){return page.evaluate(async()=>{const canvas=document.createElement('canvas');canvas.width=640;canvas.height=360;const ctx=canvas.getContext('2d');let frame=0;const paint=()=>{ctx.fillStyle=frame++%2?'#1462bd':'#e9af31';ctx.fillRect(0,0,640,360)};paint();const timer=setInterval(paint,33);const video=canvas.captureStream(30).getVideoTracks()[0];const audio=new AudioContext();await audio.resume();const dest=audio.createMediaStreamDestination();const osc=audio.createOscillator();osc.frequency.value=440;osc.connect(dest);osc.start();const sound=dest.stream.getAudioTracks()[0];const pc=new RTCPeerConnection();const sender=pc.addTrack(video);pc.addTrack(sound);const transceiver=pc.getTransceivers().find(x=>x.sender===sender);const codecs=RTCRtpSender.getCapabilities('video')?.codecs.filter(x=>x.mimeType.toLowerCase()==='video/h264')||[];if(!codecs.length)throw new Error('H264 unavailable');transceiver.setCodecPreferences(codecs);window.__barrier.media={pc,video,sound,audio,osc,timer};const offer=await pc.createOffer();await pc.setLocalDescription(offer);await Promise.race([new Promise(resolve=>{if(pc.iceGatheringState==='complete')resolve();else pc.addEventListener('icegatheringstatechange',()=>{if(pc.iceGatheringState==='complete')resolve()})}),new Promise(resolve=>setTimeout(resolve,2500))]);return pc.localDescription.sdp})}
async function main(){const browser=await chromium.launch({channel:'chrome',headless:true,args:[`--ignore-certificate-errors-spki-list=${await leafPin()}`,'--autoplay-policy=no-user-gesture-required'],timeout:10000});let page,setup,armed,operation,summary,failure,controlProbeMs=null,holdStartWallMs,holdEndWallMs;const cleanup=[];try{
  page=await browser.newPage();page.setDefaultTimeout(10000);await page.goto(origin,{timeout:10000});setup=await bounded('prepare',15000,()=>setupPage(page));const epoch=(await lifecycle()).epoch;
  if(stage!=='during_rtp_callback')await proxyCall('/target',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({sid:setup.sid,stage})});
  await bounded('synthetic offer',12000,()=>prepareMedia(page));
  await page.evaluate(()=>{const s=window.__barrier;s.pending=fetch(s.grant.whipUrl,{method:'POST',signal:AbortSignal.timeout(16000),headers:{'Content-Type':'application/sdp',Authorization:`Bearer ${s.grant.token}`},body:s.media.pc.localDescription.sdp}).then(async r=>({status:r.status,answer:await r.text(),location:r.headers.get('Location')})).catch(error=>({error:error.name}));return true});
  if(stage==='during_rtp_callback'||stage==='before_add_publisher'){
    const posted=await bounded('initial WHIP',18000,()=>page.evaluate(()=>window.__barrier.pending));if(posted.status!==201)throw new Error(`initial WHIP ${posted.status||posted.error}`);operation=posted.location?.split('/').at(-1);if(!/^[0-9a-f-]{36}$/.test(operation))throw new Error('invalid public resource ID');await page.evaluate(answer=>window.__barrier.media.pc.setRemoteDescription({type:'answer',sdp:answer}),posted.answer);
    if(stage==='during_rtp_callback'){
      await until('LIVE before RTP hold',20000,async()=>{const run=await dbRun(setup.sid);return run.state==='running'&&await ffmpegExists(run.pid)});
      await barrier(epoch,operation,'arm');armed={epoch,id:operation};
    }else{
      const marked=await proxyCall('/status');if(marked.armed?.id!==operation||marked.armed?.epoch!==epoch)throw new Error('proxy armed another operation');armed={epoch,id:operation};
    }
  }else{
    const marked=await until('proxy arm',10000,async()=>{const value=await proxyCall('/status');return value.armed?.stage===stage?value.armed:null});operation=marked.id;armed={epoch:marked.epoch,id:operation};if(epoch!==marked.epoch)throw new Error('epoch changed during publish');
  }
  try{await until('barrier arrival',12000,async()=>(await barrier(armed.epoch,operation)).arrived)}catch(error){
    const posted=await bounded('diagnostic WHIP',6000,()=>page.evaluate(()=>window.__barrier.pending)).catch(e=>({diagnostic:e.message}));
    throw new Error(`${error.message}; WHIP ${JSON.stringify({status:posted.status,error:posted.error,diagnostic:posted.diagnostic})}`);
  }
  holdStartWallMs=Date.now();
  if(stage==='before_session_initialize'){
    const started=Date.now();
    const other=await bounded('independent WebRTC management list',8000,()=>mediaList('webrtcsessions'));
    controlProbeMs=Date.now()-started;
    if(!other.some(x=>x.path?.startsWith('wbb/')&&!x.path.includes(setup.sid)))throw new Error('other LIVE session missing from management list');
  }
  const pinned=await state(setup.sid,operation);if(pinned.epoch!==armed.epoch||pinned.sequence<1)throw new Error('product operation tuple not committed');
  const stopped=await page.evaluate(async()=>{const s=window.__barrier;const response=await fetch(`/api/v1/broadcasts/${s.bid}/sessions/${s.sid}/stop`,{method:'POST',signal:AbortSignal.timeout(5000),headers:{Authorization:`Bearer ${s.access}`}});return response.status});if(stopped!==202)throw new Error(`stop ${stopped}`);
  await pause(6200);
  const held=await state(setup.sid,operation);
  if(stage==='before_register'){
    if(!['stopping','ended'].includes(held.session))throw new Error(`pre-registration close unsafe: ${JSON.stringify(held)}`);
  }else if(held.session!=='stopping'||held.operation==='closed')throw new Error(`held operation completed early: ${JSON.stringify(held)}`);
  holdEndWallMs=Date.now();
  await barrier(armed.epoch,operation,'release');armed=null;
  const posted=await bounded('released WHIP',18000,()=>page.evaluate(()=>window.__barrier.pending));
  if(!['during_rtp_callback','before_add_publisher'].includes(stage)&&(posted.status===201||posted.location))throw new Error(`late publication escaped stop: ${posted.status}`);
  await until('ENDED with pinned completion',45000,async()=>{const value=await state(setup.sid,operation);return value.session==='ended'&&value.operation==='closed'&&await dbCompletion(setup.sid)});
  const prefix=`wbb/${setup.sid}/1/`;const [paths,ingress,rtsp]=await Promise.all([mediaList('paths'),mediaList('webrtcsessions'),mediaList('rtspsessions')]);if(paths.some(x=>x.name?.startsWith(prefix)&&x.ready)||ingress.some(x=>x.path?.startsWith(prefix))||rtsp.some(x=>x.path?.startsWith(prefix)))throw new Error('owned media resource remains');
  summary={stage,holdStartWallMs,holdEndWallMs,heldMs:holdEndWallMs-holdStartWallMs,controlProbeMs,publicStop:stopped,lateWHIP:posted.status||posted.error,epochPinned:true,completed:true,ended:true,mediaAbsent:true,sessionId:setup.sid};
}catch(error){failure=error}finally{
  if(armed)await barrier(armed.epoch,armed.id,'release').catch(error=>cleanup.push(error));
  if(page&&setup)await page.evaluate(async()=>{const s=window.__barrier;const r=await fetch(`/api/v1/broadcasts/${s.bid}/sessions/${s.sid}/stop`,{method:'POST',signal:AbortSignal.timeout(5000),headers:{Authorization:`Bearer ${s.access}`}});if(![200,202,409].includes(r.status))throw new Error(`cleanup stop ${r.status}`)}).catch(error=>cleanup.push(error));
  if(page)await page.evaluate(async()=>{const m=window.__barrier?.media;if(m){clearInterval(m.timer);m.pc.close();m.video.stop();m.sound.stop();m.osc.stop();await m.audio.close()}}).catch(error=>cleanup.push(error));
  await proxyCall('/reset',{method:'POST'}).catch(error=>cleanup.push(error));
  if(page)await bounded('page close',10000,()=>page.close()).catch(error=>cleanup.push(error));
  await bounded('browser close',30000,()=>browser.close()).catch(error=>cleanup.push(error));
}
if(failure||cleanup.length)throw new AggregateError([...(failure?[failure]:[]),...cleanup],'product barrier or cleanup failed');console.log(JSON.stringify(summary))}
main().catch(error=>{console.error(error.errors?.map(x=>x.message).join('; ')||error.message);process.exitCode=1});
