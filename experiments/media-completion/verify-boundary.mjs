import {createHash, createHmac, randomUUID} from 'node:crypto';
import {readFileSync} from 'node:fs';
import {allowOwned,cleanupBoundary} from './boundary-cleanup.mjs';

const env = Object.fromEntries(readFileSync(new URL('./.env', import.meta.url), 'utf8').trim().split('\n').map(line => line.split('=', 2)));
const key = Buffer.from(env.WBB_COMPLETION_KEY_HEX, 'hex');
const anchor = JSON.parse(readFileSync(new URL('./anchor.json', import.meta.url), 'utf8'));
const base = 'http://127.0.0.1:29698/v3/wbb';
const whipBase = 'http://127.0.0.1:29889';
const authBase = 'http://127.0.0.1:29699';
const apiBase = 'http://127.0.0.1:29697/v3';
const sdp = 'v=0\r\n';
const owned = new Set();
const registeredPaths = new Map();
const armed = new Map();
const pendingPosts = [];
const requestedPaths = new Set();

function sign(...parts) {
  const h = createHmac('sha256', key);
  for (const part of parts) h.update(`${Buffer.byteLength(part)}:${part}`);
  return h.digest('hex');
}
function assert(ok, label) { if (!ok) throw new Error(label); }
async function call(url, options = {}, ms = 6000) {
  return fetch(url, {...options, signal: options.signal ?? AbortSignal.timeout(ms)});
}
async function lifecycle() {
  const res = await call(`${base}/lifecycle`, {headers:{'X-WBB-Signature':sign('lifecycle',anchor.root,anchor.slot,'','')}});
  assert(res.status === 200, `lifecycle ${res.status}`);
  const body = await res.json();
  assert(body.contract_version === 'wbb-media-completion/1' && body.slot === anchor.slot && body.root === anchor.root && /^[a-f0-9]{32}$/.test(body.epoch), 'invalid lifecycle tuple');
  return body;
}
let epoch;
function opHeaders(purpose, id, value = epoch) {
  return {'X-WBB-Epoch':value, 'X-WBB-Signature':sign(purpose,anchor.root,anchor.slot,value,id)};
}
async function query(id) {
  const res = await call(`${base}/operations/${id}?epoch=${epoch}`, {headers:opHeaders('query',id)});
  assert(res.status === 200, `query ${res.status}`);
  return res.json();
}
async function close(id) {
  const res = await call(`${base}/operations/${id}/close`, {method:'POST',headers:opHeaders('close',id)});
  assert(res.status === 202, `close ${res.status}`);
  return res.json();
}
async function waitState(id, state) {
  const end = Date.now()+5000;
  while (Date.now()<end) {
    const q = await query(id);
    if (q.state===state) return q;
    await new Promise(resolve=>setTimeout(resolve,40));
  }
  throw new Error(`${id} never entered ${state}`);
}
function barrierHeaders(purpose, stage, id) {
  return {'X-WBB-Epoch':epoch,'X-WBB-Signature':sign(purpose,anchor.root,anchor.slot,epoch,stage,id)};
}
async function barrier(method, stage, id, action) {
  const path = `${base}/barriers/${stage}/${id}${action ? `/${action}` : ''}`;
  const purpose = `barrier-${action || 'query'}`;
  const res = await call(path,{method,headers:barrierHeaders(purpose,stage,id)});
  assert(res.status===200, `barrier ${action || 'query'} ${res.status}`);
  if (action==='arm') armed.set(`${stage}/${id}`,[stage,id]);
  if (action==='release') armed.delete(`${stage}/${id}`);
  return res.json();
}
async function waitArrived(stage,id) {
  const end=Date.now()+5000;
  while (Date.now()<end) {
    if ((await barrier('GET',stage,id)).arrived) return;
    await new Promise(resolve=>setTimeout(resolve,40));
  }
  throw new Error(`barrier ${stage} did not arrive`);
}
async function allow(id,path) {
  const res=await allowOwned(owned,registeredPaths,id,path,()=>call(`${authBase}/fixture/allow`,{method:'POST',headers:{'Content-Type':'application/json','X-WBB-Signature':sign('fixture-allow',id,path)},body:JSON.stringify({operation:id,path})}));
  assert(res.status===204, `fixture allow ${res.status}`);
}
async function revoke(id,path) {
  const res=await call(`${authBase}/fixture/revoke`,{method:'POST',headers:{'Content-Type':'application/json','X-WBB-Signature':sign('fixture-revoke',id,path)},body:JSON.stringify({operation:id,path})});
  assert(res.status===204,`fixture revoke ${res.status}`);
}
function control(){return `Basic ${Buffer.from(`control:${sign('control')}`).toString('base64')}`}
async function listAll(kind){
  const items=[];
  let pageCount;
  for(let page=0;page<(pageCount??1);page++){
    const res=await call(`${apiBase}/${kind}/list?page=${page}`,{headers:{Authorization:control()}});
    assert(res.status===200,`${kind} list ${res.status}`);
    const body=await res.json();
    assert(Number.isInteger(body.pageCount) && body.pageCount>=0 && body.pageCount<=1000,`${kind} invalid page count`);
    if(pageCount===undefined)pageCount=body.pageCount;
    else assert(pageCount===body.pageCount,`${kind} pagination changed`);
    items.push(...(body.items||[]));
  }
  return items;
}
async function assertOwnedResourcesAbsent(){
  const deadline=Date.now()+5000;
  let remaining;
  do{
    remaining=[];
    for(const kind of ['webrtcsessions','rtspsessions','paths']){
      const items=await listAll(kind);
      if(items.some(item=>requestedPaths.has(kind==='paths'?item.name:item.path)))remaining.push(kind);
    }
    if(!remaining.length)return;
    await new Promise(resolve=>setTimeout(resolve,100));
  }while(Date.now()<deadline);
  throw new Error(`owned resources remain: ${remaining.join(',')}`);
}
function publish(id,path,basicID=id,body=sdp,override={}) {
  requestedPaths.add(path);
  const signature=sign('publish',anchor.root,anchor.slot,epoch,id,path,createHash('sha256').update(body).digest('hex'));
  const request=call(`${whipBase}/${path}/whip`,{method:'POST',headers:{'Content-Type':'application/sdp','Authorization':`Basic ${Buffer.from(`create.${basicID}:${sign('create',basicID)}`).toString('base64')}`,'X-WBB-Operation':id,'X-WBB-Slot':anchor.slot,'X-WBB-Root':anchor.root,'X-WBB-Epoch':epoch,'X-WBB-Signature':signature,...override},body},15000);
  pendingPosts.push(request.then(()=>{},()=>{}));
  return request;
}
async function main() {
  let failure;
  try {
  epoch=(await lifecycle()).epoch;
  const probe=await call(`${authBase}/health`);
  assert(probe.status===204,'fixture health');
  const info=await call(`${apiBase}/info`,{headers:{Authorization:control()}});
  assert(info.status===200,`api info ${info.status}`);
  for (const [method,path] of [['PATCH','config/global/patch'],['PATCH','config/path-defaults/patch'],['POST','config/paths/add/new'],['PATCH','config/paths/patch/new'],['DELETE','config/paths/delete/new']]) {
    const res=await call(`${apiBase}/${path}`,{method,headers:{Authorization:control(),'Content-Type':'application/json'},body:method==='DELETE'?undefined:'{}'});
    assert(res.status===403,`config write ${method} ${path}: ${res.status}`);
  }
  console.log('api_read_only=5/5');

  const sid=randomUUID();const path=`wbb/${sid}/1/source`;
  const a=randomUUID(), b=randomUUID();
  await allow(a,path);
  let res=await publish(b,path,a);
  assert(res.status===401,`mixed A Basic/B HMAC ${res.status}`);
  assert((await query(b)).state==='never-registered','mixed tuple registered B');
  assert((await close(b)).state==='completed','pre-create B close');
  res=await publish(b,path,a);
  assert(res.status===401,`closed B mixed tuple ${res.status}`);
  console.log('mixed_credential_rejected=2/2');

  res=await publish(a,path);
  assert(res.status>=400 && res.status<500,`invalid SDP status ${res.status}`);
  assert((await query(a)).state==='active','4xx incorrectly completed operation');
  const changedBody=sdp+'o=changed 1 1 IN IP4 127.0.0.1\r\n';
  res=await publish(a,path,a,changedBody);
  assert(res.status===409,`different SDP reused operation ${res.status}`);
  const changedPath=`wbb/${randomUUID()}/1/source`;
  res=await publish(a,changedPath);
  assert(res.status===409,`different path reused operation ${res.status}`);
  for(const [label,override] of [
    ['slot',{'X-WBB-Slot':'other-slot'}],
    ['root',{'X-WBB-Root':'other-root'}],
    ['epoch',{'X-WBB-Epoch':'other-epoch'}],
    ['purpose',{'X-WBB-Signature':sign('close',anchor.root,anchor.slot,epoch,a)}],
  ]){
    res=await publish(a,path,a,sdp,override);
    assert(res.status===401,`${label} tuple accepted ${res.status}`);
  }
  console.log('tuple_conflict_and_wrong_auth=6/6');
  res=await publish(a,path);
  assert(res.status===409,`duplicate POST ${res.status}`);
  await close(a);await waitState(a,'completed');
  assert((await close(a)).state==='completed','duplicate close changed completed state');
  console.log('4xx_duplicate_close=pass');

  for (const stage of ['before_register','before_session_initialize','during_auth']) {
    const id=randomUUID();
    await allow(id,path);
    await barrier('POST',stage,id,'arm');
    const pending=publish(id,path);
    await waitArrived(stage,id);
    const closed=await close(id);
    assert(closed.state===(stage==='before_register'?'completed':'closing'),`${stage} early ${closed.state}`);
    await barrier('POST',stage,id,'release');
    const late=await pending;
    assert(late.status===409,`${stage} late POST ${late.status}`);
    await waitState(id,'completed');
    console.log(`${stage}=pass`);
  }
  } catch(error) {
    failure=error;
  } finally {
    try{
      await cleanupBoundary({
        owned,registeredPaths,armed,pendingPosts,close,
        release:(stage,id)=>barrier('POST',stage,id,'release'),
        waitCompleted:id=>waitState(id,'completed'),revoke,
        assertAbsent:assertOwnedResourcesAbsent,
      });
    }catch(error){failure=failure?new Error(`${failure.message}; ${error.message}`):error}
  }
  if(failure)throw failure;
  console.log('cleanup_verified=completed_and_owner_resources_absent');
}

main().catch(error=>{ console.error(`boundary_failure=${error.message}`);process.exitCode=1; });
