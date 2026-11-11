// Public HTTPS + real patched MediaMTX: the test-only proxy consumes one WHIP
// 201 and drops it before the product API receives a byte of the response.
import {chromium} from '../media-single-rendition/node_modules/playwright/index.mjs';
import crypto,{X509Certificate} from 'node:crypto';
import tls from 'node:tls';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {dbCompletion,mediaList} from './verify-browser.mjs';

const execFileAsync=promisify(execFile);
const origin='https://localhost:19543';
const wait=ms=>new Promise(resolve=>setTimeout(resolve,ms));
async function pin(){
  return new Promise((resolve,reject)=>{
    const socket=tls.connect({host:'localhost',port:19543,servername:'localhost',rejectUnauthorized:false},()=>{
      try{const cert=new X509Certificate(socket.getPeerCertificate(true).raw);
        resolve(crypto.createHash('sha256').update(cert.publicKey.export({type:'spki',format:'der'})).digest('base64'));
      }catch(error){reject(error)}finally{socket.end()}
    });
    socket.setTimeout(3000,()=>{socket.destroy();reject(new Error('TLS pin timeout'))});
    socket.on('error',reject);
  });
}
async function operation(sid){
  if(!/^[0-9a-f-]{36}$/.test(sid))throw new Error('invalid session id');
  const sql=`SELECT state||','||(upstream_resource_path IS NULL)::text||','||(completion_epoch IS NOT NULL)::text FROM media_create_operations WHERE broadcast_session_id='${sid}'`;
  const {stdout}=await execFileAsync('docker',['exec','wbb-media-completion-integration-postgres-1','psql','-U','wbb','-d','wbb','-At','-c',sql],{timeout:5000});
  return stdout.trim();
}
async function main(){
  const invalidSDP=process.argv.includes('--upstream-4xx');
  const browser=await chromium.launch({channel:'chrome',headless:true,args:[`--ignore-certificate-errors-spki-list=${await pin()}`],timeout:10000});
  let page,sid,summary,failure;const cleanupErrors=[];
  try{
    page=await browser.newPage();page.setDefaultTimeout(10000);
    await page.goto(origin,{timeout:10000});
    const email=`lost-${crypto.randomUUID()}@example.invalid`;
    const password=crypto.randomBytes(24).toString('hex');
    const prepared=await page.evaluate(async({email,password})=>{
      const call=async(method,payload,token,path,extra={})=>{
        const response=await fetch(path,{method,credentials:'include',signal:AbortSignal.timeout(7000),
          headers:{...(payload?{'Content-Type':'application/json'}:{}),...(token?{Authorization:`Bearer ${token}`}:{}) ,...extra},
          body:payload?JSON.stringify(payload):undefined});
        return {status:response.status,body:await response.json()};
      };
      const registered=await call('POST',{email,password,displayName:'Synthetic Lost Response'},'', '/api/v1/auth/register');
      if(registered.status!==201)throw new Error(`register ${registered.status}`);
      const login=await call('POST',{email,password},'', '/api/v1/auth/login');
      if(login.status!==200)throw new Error(`login ${login.status}`);
      const access=login.body.accessToken;
      const broadcast=await call('POST',{title:'Lost Response',description:'Isolated only'},access,'/api/v1/broadcasts');
      if(broadcast.status!==201)throw new Error(`broadcast ${broadcast.status}`);
      const bid=broadcast.body.broadcast.id;
      const start=await call('POST',{source:'screen'},access,`/api/v1/broadcasts/${bid}/sessions`,{'Idempotency-Key':`lost-${crypto.randomUUID()}`});
      if(start.status!==202)throw new Error(`prepare ${start.status}`);
      window.__lost={access,bid,sid:start.body.session.id,grant:start.body.publishGrant};
      return {sid:start.body.session.id};
    },{email,password});
    sid=prepared.sid;
    const post=await page.evaluate(async invalidSDP=>{
      const canvas=document.createElement('canvas');canvas.width=640;canvas.height=360;
      const context=canvas.getContext('2d');context.fillStyle='#547ab3';context.fillRect(0,0,640,360);
      const video=canvas.captureStream(30).getVideoTracks()[0];
      const audio=new AudioContext();await audio.resume();const destination=audio.createMediaStreamDestination();
      const tone=audio.createOscillator();tone.frequency.value=440;tone.connect(destination);tone.start();
      const sound=destination.stream.getAudioTracks()[0];const pc=new RTCPeerConnection();
      pc.addTrack(video);pc.addTrack(sound);window.__lost.media={pc,video,sound,audio,tone};
      const offer=await pc.createOffer();await pc.setLocalDescription(offer);
      await Promise.race([new Promise(resolve=>{if(pc.iceGatheringState==='complete')resolve();else pc.addEventListener('icegatheringstatechange',()=>{if(pc.iceGatheringState==='complete')resolve()})}),new Promise(resolve=>setTimeout(resolve,2500))]);
      const response=await fetch(window.__lost.grant.whipUrl,{method:'POST',signal:AbortSignal.timeout(14000),
        headers:{'Content-Type':'application/sdp',Authorization:`Bearer ${window.__lost.grant.token}`},body:invalidSDP?'v=0\r\n':pc.localDescription.sdp});
      return {status:response.status,location:response.headers.get('Location')};
    },invalidSDP);
    if(post.status!==(invalidSDP?502:504) || post.location)throw new Error(`unexpected failed WHIP result: ${post.status}`);
    const before=await operation(sid);
    if(before!=='cleanup_pending,true,true')throw new Error(`unknown resource was not retained: ${before}`);
    const stop=await page.evaluate(async()=>{
      const s=window.__lost;const response=await fetch(`/api/v1/broadcasts/${s.bid}/sessions/${s.sid}/stop`,
        {method:'POST',signal:AbortSignal.timeout(5000),headers:{Authorization:`Bearer ${s.access}`}});
      return response.status;
    });
    if(stop!==202)throw new Error(`stop ${stop}`);
    let ended=false;
    for(let i=0;i<35;i++){
      ended=await page.evaluate(async()=>{const s=window.__lost;const r=await fetch(`/api/v1/broadcasts/${s.bid}/sessions/current`,{signal:AbortSignal.timeout(5000)});return (await r.json()).session?.state==='ended'});
      if(ended)break;await wait(1000);
    }
    if(!ended || !(await dbCompletion(sid)))throw new Error('unknown Location did not close with pinned completed proof');
    const prefix=`wbb/${sid}/1/`;
    const [paths,ingress,rtsp]=await Promise.all([mediaList('paths'),mediaList('webrtcsessions'),mediaList('rtspsessions')]);
    if(paths.some(x=>x.name?.startsWith(prefix)&&x.ready)||ingress.some(x=>x.path?.startsWith(prefix))||rtsp.some(x=>x.path?.startsWith(prefix)))throw new Error('media remained after lost response');
    if(!invalidSDP){
      const {stdout,stderr}=await execFileAsync('docker',['logs','wbb-media-completion-integration-whip-proxy-1'],{timeout:5000});
      if(!(stdout+stderr).includes('dropped_first_whip_201'))throw new Error('proxy did not observe a real upstream 201');
    }
    summary={upstream201Dropped:invalidSDP?null:true,upstream4xx:invalidSDP,publicStatus:post.status,unknownLocation:true,stop,ended,completionProved:true,mediaAbsent:true,sessionId:sid};
  }catch(error){failure=error}finally{
    if(page&&sid)await page.evaluate(async()=>{const s=window.__lost;if(!s)return;await fetch(`/api/v1/broadcasts/${s.bid}/sessions/${s.sid}/stop`,{method:'POST',signal:AbortSignal.timeout(5000),headers:{Authorization:`Bearer ${s.access}`}})}).catch(error=>cleanupErrors.push(error));
    if(page)await page.evaluate(async()=>{const m=window.__lost?.media;if(m){m.pc.close();m.video.stop();m.sound.stop();m.tone.stop();await m.audio.close()}}).catch(error=>cleanupErrors.push(error));
    await browser.close().catch(error=>cleanupErrors.push(error));
  }
  if(failure||cleanupErrors.length)throw new AggregateError([...(failure?[failure]:[]),...cleanupErrors],'lost response or cleanup failed');
  console.log(JSON.stringify(summary));
}
main().catch(error=>{console.error(error.errors?.map(x=>x.message).join('; ')||error.message);process.exitCode=1});
