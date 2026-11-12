// Isolated HTTP capacity and real MediaMTX process-replacement check.
// Uses a fresh named volume and loopback port; never touches the media stack.
import {createHmac,randomUUID} from 'node:crypto';
import {execFile} from 'node:child_process';
import {mkdtempSync,readFileSync,rmSync,writeFileSync} from 'node:fs';
import {fileURLToPath} from 'node:url';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {promisify} from 'node:util';

const execFileAsync=promisify(execFile);
const envFile=fileURLToPath(new URL('./.env',import.meta.url));
const anchorFile=fileURLToPath(new URL('./anchor.json',import.meta.url));
const configFile=fileURLToPath(new URL('./mediamtx.yml',import.meta.url));
const key=Buffer.from(Object.fromEntries(readFileSync(envFile,'utf8').trim().split('\n').map(line=>line.split('=',2))).WBB_COMPLETION_KEY_HEX,'hex');
const anchor=JSON.parse(readFileSync(anchorFile,'utf8'));
const suffix=randomUUID().slice(0,8),volume=`wbb-07a1-capacity-${suffix}`;
const scratch=mkdtempSync(join(tmpdir(),'wbb-07a1-config-'));
const names=new Set();
const base='http://127.0.0.1:29696/v3/wbb';
function assert(ok,message){if(!ok)throw new Error(message)}
function sign(...parts){const h=createHmac('sha256',key);for(const p of parts)h.update(`${Buffer.byteLength(p)}:${p}`);return h.digest('hex')}
async function docker(args,ms=15000){
  try{return await execFileAsync('docker',args,{timeout:ms,maxBuffer:128*1024})}
  catch{throw new Error('owned Docker command failed')}
}
async function expectDockerFailure(args){
  try{await execFileAsync('docker',args,{timeout:12000,maxBuffer:128*1024})}
  catch(error){if(typeof error.code==='number' && error.code!==0)return;throw new Error('owned Docker failure did not finish')}
  throw new Error('invalid startup unexpectedly accepted');
}
function mounts(config=configFile){return ['-v',`${anchorFile}:/spike/anchor.json:ro`,'-v',`${config}:/spike/mediamtx.yml:ro`,'-v',`${volume}:/spike/state`]}
function vars(){return ['--env-file',envFile,'-e','WBB_COMPLETION_ANCHOR=/spike/anchor.json','-e','WBB_COMPLETION_DIR=/spike/state','-e','WBB_COMPLETION_ADDRESS=0.0.0.0:9998','-e','WBB_COMPLETION_BARRIERS=1','-e','WBB_COMPLETION_TEST_CAPACITY=2']}
async function lifecycle(){
  const r=await fetch(`${base}/lifecycle`,{headers:{'X-WBB-Signature':sign('lifecycle',anchor.root,anchor.slot,'','')},signal:AbortSignal.timeout(2000)});
  assert(r.status===200,`lifecycle ${r.status}`);
  const body=await r.json();assert(body.contract_version==='wbb-media-completion/1' && body.root===anchor.root && body.slot===anchor.slot,'lifecycle tuple');return body;
}
async function ready(){
  const deadline=Date.now()+10000;
  while(Date.now()<deadline){try{return await lifecycle()}catch{await new Promise(resolve=>setTimeout(resolve,150))}}
  throw new Error('owned MediaMTX readiness timed out');
}
async function request(id,epoch,action){
  const purpose=action==='close'?'close':'query';
  const url=`${base}/operations/${id}${action==='close'?'/close':`?epoch=${epoch}`}`;
  const r=await fetch(url,{method:action==='close'?'POST':'GET',headers:{'X-WBB-Epoch':epoch,'X-WBB-Signature':sign(purpose,anchor.root,anchor.slot,epoch,id)},signal:AbortSignal.timeout(3000)});
  return {status:r.status,body:await r.json()};
}
async function start(name,publish=true){
  names.add(name);
  await docker(['run','-d','--name',name,'--label','wbb.spike=07a1','--network','bridge',...(publish?['-p','127.0.0.1:29696:9998']:[]),...vars(),...mounts(),'wbb-media-completion-spike:local','/spike/mediamtx.yml']);
  return publish?ready():null;
}
async function killAndRemove(name){await docker(['kill','--signal','KILL',name]);await docker(['rm',name]);names.delete(name)}
async function cleanup(){
  const errors=[];
  for(const name of names){
    try{
      const r=await docker(['ps','-a','--filter',`name=^/${name}$`,'--format','{{.Names}}']);
      if(r.stdout.trim()===name)await docker(['rm','-f',name]);
    }catch(error){errors.push(`${name}: ${error.message}`)}
  }
  try{await docker(['volume','rm',volume])}catch(error){errors.push(`volume: ${error.message}`)}
  try{rmSync(scratch,{recursive:true,force:true})}catch(error){errors.push(`temporary config: ${error.message}`)}
  if(errors.length)throw new Error(`cleanup failed: ${errors.join('; ')}`);
}
async function main(){
  let failure,summary;
  try{
    await docker(['volume','create','--label','wbb.spike=07a1',volume]);
    for(const [label,setting] of [
      ['debug','MTX_LOGLEVEL=debug'],
      ['packets','MTX_DUMPPACKETS=true'],
      ['hook','MTX_RUNONCONNECT=/bin/true'],
      ['path_hook','MTX_PATHDEFAULTS_RUNONREAD=/bin/true'],
    ]){
      const name=`wbb-07a1-reject-${label}-${suffix}`;names.add(name);
      await expectDockerFailure(['run','--name',name,'--network','none',...vars(),'-e',setting,...mounts(),'wbb-media-completion-spike:local','/spike/mediamtx.yml']);
      const logs=await docker(['logs',name]);
      assert((logs.stdout+logs.stderr).includes('completion configuration'),`${label} rejection did not reach completion validator`);
      await docker(['rm',name]);names.delete(name);
    }
    const original=readFileSync(configFile,'utf8');
    const pathSection=original.indexOf('paths:\n');
    assert(pathSection>0,'base config paths section missing');
    // Use an exact path: upstream rejects alwaysAvailable on regexp paths
    // before the completion-specific validator can examine it.
    const simple=original.slice(0,pathSection)+'paths:\n  fixture:\n    source: publisher\n';
    for(const [label,needle,replacement] of [
      ['always_default','pathDefaults:\n  source: publisher','pathDefaults:\n  source: publisher\n  alwaysAvailable: true\n  alwaysAvailableTracks: [{codec: H264}]'],
      ['forward_default','pathDefaults:\n  source: publisher','pathDefaults:\n  source: publisher\n  forward:\n    - dest: rtsp://127.0.0.1:8554/other'],
      ['always_path','  fixture:\n    source: publisher','  fixture:\n    source: publisher\n    alwaysAvailable: true\n    alwaysAvailableTracks: [{codec: H264}]'],
      ['forward_path','  fixture:\n    source: publisher','  fixture:\n    source: publisher\n    forward:\n      - dest: rtsp://127.0.0.1:8554/other'],
    ]){
      assert(simple.includes(needle),`${label} config fixture changed`);
      const config=join(scratch,`${label}.yml`);
      writeFileSync(config,simple.replace(needle,replacement),{mode:0o600});
      const name=`wbb-07a1-reject-${label}-${suffix}`;names.add(name);
      await expectDockerFailure(['run','--name',name,'--network','none',...vars(),...mounts(config),'wbb-media-completion-spike:local','/spike/mediamtx.yml']);
      const logs=await docker(['logs',name]);
      assert((logs.stdout+logs.stderr).includes('completion configuration: unsupported path'),`${label} did not fail completion path validation`);
      await docker(['rm',name]);names.delete(name);
    }
    const unboot=`wbb-07a1-unboot-${suffix}`;names.add(unboot);
    await expectDockerFailure(['run','--name',unboot,'--network','none',...vars(),...mounts(),'wbb-media-completion-spike:local','/spike/mediamtx.yml']);
    const emptyLogs=await docker(['logs',unboot]);
    assert((emptyLogs.stdout+emptyLogs.stderr).includes('completion history unavailable'),'empty volume rejection was not caused by missing history');
    await docker(['rm',unboot]);names.delete(unboot);
    const bootstrap=`wbb-07a1-bootstrap-${suffix}`;names.add(bootstrap);
    await docker(['run','--name',bootstrap,'--network','none',...vars(),...mounts(),'--entrypoint','/wbb-bootstrap','wbb-media-completion-spike:local']);
    await docker(['rm',bootstrap]);names.delete(bootstrap);
    const a=await start(`wbb-07a1-capacity-a-${suffix}`);
    const competitor=`wbb-07a1-competitor-${suffix}`;names.add(competitor);
    await expectDockerFailure(['run','--name',competitor,'--network','none',...vars(),...mounts(),'wbb-media-completion-spike:local','/spike/mediamtx.yml']);
    const lockLogs=await docker(['logs',competitor]);
    assert((lockLogs.stdout+lockLogs.stderr).includes('completion history unavailable'),'concurrent refusal was not a registry refusal');
    await docker(['rm',competitor]);names.delete(competitor);
    const ids=[randomUUID(),randomUUID(),randomUUID()];
    for(const id of ids.slice(0,2)){
      const x=await request(id,a.epoch,'close');assert(x.status===202 && x.body.state==='completed','capacity fixture close');
    }
    const full=await request(ids[2],a.epoch,'close');
    assert(full.status===503 && full.body.state==='capacity_unknown','full registry falsely completed unknown operation');
    for(const id of ids.slice(0,2))assert((await request(id,a.epoch,'query')).body.state==='completed','existing tombstone lost');
    await killAndRemove(`wbb-07a1-capacity-a-${suffix}`);
    const b=await start(`wbb-07a1-capacity-b-${suffix}`);
    assert(b.epoch!==a.epoch && b.epoch_sequence===a.epoch_sequence+1,'B epoch continuity');
    await killAndRemove(`wbb-07a1-capacity-b-${suffix}`);
    const c=await start(`wbb-07a1-capacity-c-${suffix}`);
    assert(c.epoch!==b.epoch && c.epoch!==a.epoch && c.epoch_sequence===b.epoch_sequence+1,'C epoch continuity');
    assert((await request(ids[0],a.epoch,'query')).body.state==='completed','A not retired in C');
    assert((await request(ids[1],b.epoch,'query')).body.state==='completed','unobserved B not retired in C');
    assert((await request(ids[0],'unknown-epoch','query')).body.state==='unknown_epoch','unknown epoch accepted');
    await killAndRemove(`wbb-07a1-capacity-c-${suffix}`);
    // Preserve valid JSON and a 32-character hex ID while changing one byte.
    const tamper=`wbb-07a1-tamper-${suffix}`;names.add(tamper);
    await docker(['run','--rm','--name',tamper,'--network','none','-v',`${volume}:/spike/state`,'--entrypoint','/bin/sh','wbb-media-completion-spike:local','-c',`awk '{i=index($0,"\\"id\\":\\""); if(i==0)exit 2; p=i+6; c=substr($0,p,1); printf "%s", substr($0,1,p-1) (c=="0"?"1":"0") substr($0,p+1)}' /spike/state/history.json > /spike/state/tampered && mv /spike/state/tampered /spike/state/history.json`]);names.delete(tamper);
    const damaged=`wbb-07a1-damaged-${suffix}`;names.add(damaged);
    await expectDockerFailure(['run','--name',damaged,'--network','none',...vars(),...mounts(),'wbb-media-completion-spike:local','/spike/mediamtx.yml']);
    const damagedLogs=await docker(['logs',damaged]);
    assert((damagedLogs.stdout+damagedLogs.stderr).includes('history checksum mismatch'),'well-formed modified epoch did not fail checksum validation');
    await docker(['rm',damaged]);names.delete(damaged);
    summary={unsupportedConfigRejected:['debug','packets','hook','path_hook','always_default','forward_default','always_path','forward_path'],emptyVolumeRejected:true,concurrentLockRejected:true,capacity:2,saturatedClose:'capacity_unknown',existingTombstones:'completed',epochSequence:[a.epoch_sequence,b.epoch_sequence,c.epoch_sequence],oldEpochs:'completed',unknownEpoch:'unknown_epoch',checksumTamperRejected:true};
  }catch(error){failure=error}
  try{await cleanup()}catch(error){failure=failure?new Error(`${failure.message}; ${error.message}`):error}
  if(failure)throw failure;
  console.log(JSON.stringify({...summary,cleanup:'verified'}));
}
main().then(()=>process.exit(0),error=>{console.error(`capacity_epoch_failure=${error.message}`);process.exit(1)});
