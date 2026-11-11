// Third public publisher while two real Chrome/FFmpeg/HLS publishers occupy
// the disposable product MediaMTX history. Its unknown operation must stay
// unresolved; the wrapper drops only its disposable DB after both finish.
import {chromium} from '../media-single-rendition/node_modules/playwright/index.mjs';
import crypto,{X509Certificate,createHmac} from 'node:crypto';
import {readFileSync} from 'node:fs';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import tls from 'node:tls';

const env=Object.fromEntries(readFileSync(process.env.WBB_TEST_ENV_FILE,'utf8').trim().split('\n').map(line=>{const at=line.indexOf('=');return [line.slice(0,at),line.slice(at+1)]}));
const key=Buffer.from(env.WBB_COMPLETION_KEY_HEX,'hex');
const exec=promisify(execFile);
function sign(...parts){const h=createHmac('sha256',key);for(const part of parts)h.update(`${Buffer.byteLength(part)}:${part}`);return h.digest('hex')}
function pin(){return new Promise((resolve,reject)=>{const socket=tls.connect({host:'localhost',port:19543,servername:'localhost',rejectUnauthorized:false},()=>{try{const cert=new X509Certificate(socket.getPeerCertificate(true).raw);resolve(crypto.createHash('sha256').update(cert.publicKey.export({type:'spki',format:'der'})).digest('base64'))}catch(error){reject(error)}finally{socket.end()}});socket.setTimeout(3000,()=>{socket.destroy();reject(new Error('TLS pin timeout'))});socket.on('error',reject)})}
async function sql(statement){const {stdout}=await exec('docker',['exec','wbb-media-completion-integration-postgres-1','psql','-v','ON_ERROR_STOP=1','-U','wbb','-d',env.WBB_CAPACITY_DB,'-At','-c',statement],{timeout:5000});return stdout.trim()}
async function managementOperation(id,epoch,action){const url=`http://127.0.0.1:29998/v3/wbb/operations/${id}${action==='close'?'/close':`?epoch=${epoch}`}`;const response=await fetch(url,{method:action==='close'?'POST':'GET',signal:AbortSignal.timeout(4000),headers:{'X-WBB-Epoch':epoch,'X-WBB-Signature':sign(action,env.WBB_COMPLETION_ROOT,env.WBB_COMPLETION_SLOT,epoch,id)}});return {status:response.status,value:await response.json()}}
async function main(){if(key.length!==32)throw new Error('invalid test key');const browser=await chromium.launch({channel:'chrome',headless:true,args:[`--ignore-certificate-errors-spki-list=${await pin()}`],timeout:10000});let page,sid,bid,access,summary;const cleanup=[];try{
  page=await browser.newPage();await page.goto('https://localhost:19543',{timeout:10000});
  const email=`capacity-${crypto.randomUUID()}@example.invalid`,password=crypto.randomBytes(24).toString('hex');
  ({sid,bid,access}=await page.evaluate(async({email,password})=>{
    const call=async(method,path,body,token,headers={})=>{const r=await fetch(path,{method,credentials:'include',signal:AbortSignal.timeout(7000),headers:{...(body?{'Content-Type':'application/json'}:{}),...(token?{Authorization:`Bearer ${token}`}:{}) ,...headers},body:body?JSON.stringify(body):undefined});return {status:r.status,value:await r.json()}};
    let r=await call('POST','/api/v1/auth/register',{email,password,displayName:'Capacity Test'});if(r.status!==201)throw new Error(`register ${r.status}`);
    r=await call('POST','/api/v1/auth/login',{email,password});if(r.status!==200)throw new Error(`login ${r.status}`);const access=r.value.accessToken;
    r=await call('POST','/api/v1/broadcasts',{title:'Capacity isolation',description:'Disposable'},access);if(r.status!==201)throw new Error(`broadcast ${r.status}`);const bid=r.value.broadcast.id;
    r=await call('POST',`/api/v1/broadcasts/${bid}/sessions`,{source:'screen'},access,{'Idempotency-Key':`capacity-${crypto.randomUUID()}`});if(r.status!==202)throw new Error(`prepare ${r.status}`);
    window.__capacity={access,bid,sid:r.value.session.id,grant:r.value.publishGrant};return {access,bid,sid:r.value.session.id};
  },{email,password}));
  const live=await sql("SELECT o.id::text||','||o.completion_epoch FROM media_create_operations o JOIN broadcast_sessions s ON s.id=o.broadcast_session_id WHERE s.state='live'");
  const liveRows=live.split('\n');if(liveRows.length!==2)throw new Error('two LIVE operations missing');
  for(const row of liveRows){const [id,epoch]=row.split(',');const checked=await managementOperation(id,epoch,'query');if(checked.status!==200||checked.value.state!=='active')throw new Error(`LIVE registry operation ${checked.status}/${checked.value.state}`)}
  const posted=await page.evaluate(async()=>{const s=window.__capacity;const pc=new RTCPeerConnection();pc.addTransceiver('audio',{direction:'sendonly'});try{const offer=await pc.createOffer();await pc.setLocalDescription(offer);const response=await fetch(s.grant.whipUrl,{method:'POST',signal:AbortSignal.timeout(12000),headers:{'Content-Type':'application/sdp',Authorization:`Bearer ${s.grant.token}`},body:pc.localDescription.sdp});return response.status}finally{pc.close()}});
  if(posted!==502)throw new Error(`saturated public WHIP ${posted}`);
  const row=await sql(`SELECT id::text||','||state||','||completion_epoch FROM media_create_operations WHERE broadcast_session_id='${sid}'`);
  const [operation,state,epoch]=row.split(',');if(!/^[0-9a-f-]{36}$/.test(operation)||!epoch||state==='closed')throw new Error('capacity operation was not durably pinned');
  const full=await managementOperation(operation,epoch,'close');
  if(full.status!==503||full.value.state!=='capacity_unknown')throw new Error(`capacity close ${full.status}/${full.value.state}`);
  const stopped=await page.evaluate(async()=>{const s=window.__capacity;const r=await fetch(`/api/v1/broadcasts/${s.bid}/sessions/${s.sid}/stop`,{method:'POST',signal:AbortSignal.timeout(5000),headers:{Authorization:`Bearer ${s.access}`}});return r.status});
  if(stopped!==202)throw new Error(`capacity stop ${stopped}`);
  await new Promise(resolve=>setTimeout(resolve,6200));
  const final=await sql(`SELECT s.state||','||o.state||','||COALESCE(o.completion_evidence_kind,'') FROM broadcast_sessions s JOIN media_create_operations o ON o.broadcast_session_id=s.id WHERE s.id='${sid}'`);
  if(final.split(',')[0]!=='stopping'||final.split(',')[1]==='closed'||final.split(',')[2])throw new Error(`capacity falsely ended: ${final}`);
  summary={publicWHIP:posted,liveRegistry:['active','active'],unknownClose:full.value.state,unknownCloseStatus:full.status,stop:stopped,afterHold:'stopping/nonclosed',sessionId:sid};
}finally{
  if(page&&sid)await page.evaluate(async()=>{const s=window.__capacity;const r=await fetch(`/api/v1/broadcasts/${s.bid}/sessions/${s.sid}/stop`,{method:'POST',signal:AbortSignal.timeout(5000),headers:{Authorization:`Bearer ${s.access}`}});if(![200,202].includes(r.status))throw new Error(`cleanup stop ${r.status}`)}).catch(error=>cleanup.push(error));
  if(page)await page.close().catch(error=>cleanup.push(error));
  await browser.close().catch(error=>cleanup.push(error));
  if(cleanup.length)throw new AggregateError(cleanup,'capacity cleanup failed');
}
console.log(JSON.stringify(summary))}
main().catch(error=>{console.error(error.message);process.exitCode=1});
