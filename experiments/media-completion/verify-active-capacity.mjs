// A separate capacity=2 MediaMTX epoch with two real WHIP/RTSP/HLS flows.
import {createHmac,randomUUID} from 'node:crypto';
import {execFile} from 'node:child_process';
import {readFileSync} from 'node:fs';
import {fileURLToPath} from 'node:url';
import {promisify} from 'node:util';

const execFileAsync=promisify(execFile);
const envFile=fileURLToPath(new URL('./.env',import.meta.url));
const anchorFile=fileURLToPath(new URL('./anchor.json',import.meta.url));
const configFile=fileURLToPath(new URL('./mediamtx.yml',import.meta.url));
const keyHex=Object.fromEntries(readFileSync(envFile,'utf8').trim().split('\n').map(x=>x.split('=',2))).WBB_COMPLETION_KEY_HEX;
const key=Buffer.from(keyHex,'hex');
const anchor=JSON.parse(readFileSync(anchorFile,'utf8'));
const suffix=randomUUID().slice(0,8);
const network=`wbb-07a1-active-${suffix}`;
const volume=`wbb-07a1-active-state-${suffix}`;
const authName=`wbb-07a1-active-auth-${suffix}`;
const mediaName=`wbb-07a1-active-media-${suffix}`;
const image='wbb-media-completion-spike:local';
const owned=new Set();

function assert(value,message){if(!value)throw new Error(message)}
function sign(...parts){const h=createHmac('sha256',key);for(const p of parts)h.update(`${Buffer.byteLength(p)}:${p}`);return h.digest('hex')}
async function docker(args,timeout=15000){
  try{return await execFileAsync('docker',args,{timeout,maxBuffer:128*1024,env:{...process.env,WBB_FIXTURE_KEY_HEX:keyHex}})}
  catch{throw new Error('isolated Docker command failed')}
}
async function waitReady(){
  const deadline=Date.now()+12000;
  while(Date.now()<deadline){
    try{
      const [a,m]=await Promise.all([
        fetch('http://127.0.0.1:30699/health',{signal:AbortSignal.timeout(1000)}),
        fetch('http://127.0.0.1:30698/v3/wbb/lifecycle',{headers:{'X-WBB-Signature':sign('lifecycle',anchor.root,anchor.slot,'','')},signal:AbortSignal.timeout(1000)}),
      ]);
      if(a.status===204 && m.status===200){const body=await m.json();assert(body.contract_version==='wbb-media-completion/1' && body.epoch_sequence===1,'fresh capacity epoch');return}
    }catch{}
    await new Promise(resolve=>setTimeout(resolve,150));
  }
  throw new Error('separate capacity stack not ready');
}
async function cleanup(){
  const failures=[];
  const attempt=async(label,fn)=>{try{await fn()}catch(error){failures.push(`${label}: ${error.message}`)}};
  await attempt('remove owned network containers',async()=>{
    const r=await docker(['ps','-a','--filter',`network=${network}`,'--format','{{.Names}}']);
    for(const name of r.stdout.trim().split('\n').filter(Boolean))await docker(['rm','-f',name]);
    const after=await docker(['ps','-a','--filter',`network=${network}`,'--format','{{.Names}}']);
    assert(after.stdout.trim()==='','owned network container remains');
  });
  if(owned.has(volume))await attempt('remove owned volume',()=>docker(['volume','rm',volume]));
  if(owned.has(network))await attempt('remove owned network',()=>docker(['network','rm',network]));
  if(failures.length)throw new Error(`capacity cleanup failed: ${failures.join('; ')}`);
}
async function main(){
  let failure,result;
  try{
    await docker(['network','create','--label','wbb.spike=07a1',network]);owned.add(network);
    await docker(['volume','create','--label','wbb.spike=07a1',volume]);owned.add(volume);
    await docker(['run','--rm','--network','none','--env-file',envFile,'-e','WBB_COMPLETION_ANCHOR=/spike/anchor.json','-e','WBB_COMPLETION_DIR=/spike/state','-v',`${anchorFile}:/spike/anchor.json:ro`,'-v',`${volume}:/spike/state`,'--entrypoint','/wbb-bootstrap',image]);
    await docker(['run','-d','--name',authName,'--network',network,'--network-alias','auth','-p','127.0.0.1:30699:8081','--env-file',envFile,'-e','WBB_FIXTURE_KEY_HEX','-e','WBB_FIXTURE_ADDRESS=:8081','--entrypoint','/wbb-auth-fixture',image]);
    await docker(['run','-d','--name',mediaName,'--network',network,'--network-alias','mediamtx',
      '-p','127.0.0.1:30697:9997','-p','127.0.0.1:30698:9998','-p','127.0.0.1:30889:8889','-p','127.0.0.1:30888:8888','-p','127.0.0.1:30554:8554','-p','127.0.0.1:30791:30791/udp',
      '--env-file',envFile,'-e','WBB_COMPLETION_ANCHOR=/spike/anchor.json','-e','WBB_COMPLETION_DIR=/spike/state','-e','WBB_COMPLETION_ADDRESS=0.0.0.0:9998','-e','WBB_COMPLETION_BARRIERS=1','-e','WBB_COMPLETION_TEST_CAPACITY=2','-e','MTX_WEBRTCLOCALUDPADDRESS=:30791',
      '-v',`${anchorFile}:/spike/anchor.json:ro`,'-v',`${configFile}:/spike/mediamtx.yml:ro`,'-v',`${volume}:/spike/state`,image,'/spike/mediamtx.yml']);
    await waitReady();
    const child=await execFileAsync(process.execPath,['verify-media.mjs','active_capacity'],{cwd:fileURLToPath(new URL('.',import.meta.url)),timeout:120000,maxBuffer:128*1024,env:{...process.env,WBB_SPIKE_PORT_OFFSET:'1000',WBB_SPIKE_NETWORK:network,WBB_SPIKE_MEDIA_SERVICE:'mediamtx'}});
    result=JSON.parse(child.stdout.trim().split('\n').at(-1));
    assert(result.capacity===2 && result.thirdClose==='capacity_unknown/503' && result.heldA==='closing_then_completed' && result.b==='active' && result.bContinuity.progressIntervals>=5 && result.bContinuity.ingressIntervals>=5 && result.cleanup==='verified','media capacity result incomplete');
  }catch(error){failure=error}
  try{await cleanup()}catch(error){failure=failure?new Error(`${failure.message}; ${error.message}`):error}
  if(failure)throw failure;
  console.log(JSON.stringify({...result,separateStackCleanup:'verified'}));
}
main().then(()=>process.exit(0),error=>{console.error(`active_capacity_failure=${error.message}`);process.exit(1)});
