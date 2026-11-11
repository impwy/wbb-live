// Test-only proxy: arm one exact operation after the product commits it and
// before its WHIP POST reaches MediaMTX. The public product request is intact.
import http from 'node:http';
import {createHmac} from 'node:crypto';

const key=Buffer.from(process.env.WBB_COMPLETION_KEY_HEX||'', 'hex');
const root=process.env.WBB_COMPLETION_ROOT,slot=process.env.WBB_COMPLETION_SLOT;
if(key.length!==32||!root||!slot)throw new Error('invalid isolated barrier proxy configuration');
const stages=new Set(['before_register','before_session_initialize','during_auth','before_add_publisher']);
const uuid=/^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
let target=null,armed=null;
function sign(...parts){const h=createHmac('sha256',key);for(const part of parts)h.update(`${Buffer.byteLength(part)}:${part}`);return h.digest('hex')}
const control=http.createServer(async(req,res)=>{
  if(req.method==='GET'&&req.url==='/status'){
    res.setHeader('Content-Type','application/json');res.end(JSON.stringify({armed}));return;
  }
  if(req.method==='POST'&&req.url==='/reset'){
    target=null;armed=null;res.writeHead(204).end();return;
  }
  if(req.method!=='POST'||req.url!=='/target'||target){res.writeHead(409).end();return}
  try{
    let raw='';for await(const chunk of req){raw+=chunk;if(raw.length>1024)throw new Error('control body too long')}
    const value=JSON.parse(raw);
    if(!uuid.test(value.sid)||!stages.has(value.stage))throw new Error('invalid target');
    target={sid:value.sid,stage:value.stage};res.writeHead(204).end();
  }catch{res.writeHead(400).end()}
});
control.headersTimeout=5000;control.requestTimeout=5000;
control.listen(8890,'0.0.0.0');

const media=http.createServer(async(req,res)=>{
  const match=req.url?.match(/^\/wbb\/([0-9a-f-]{36})\/1\/source\/whip$/);
  if(req.method==='POST'&&target&&match?.[1]===target.sid&&!armed){
    const id=req.headers['x-wbb-operation'],epoch=req.headers['x-wbb-epoch'];
    if(typeof id!=='string'||!uuid.test(id)||typeof epoch!=='string'||!/^[0-9a-f]{32}$/.test(epoch)){
      res.writeHead(502).end();return;
    }
    const stage=target.stage;
    try{
      const response=await fetch(`http://mediamtx:9998/v3/wbb/barriers/${stage}/${id}/arm`,{
        method:'POST',signal:AbortSignal.timeout(4000),headers:{'X-WBB-Epoch':epoch,
          'X-WBB-Signature':sign('barrier-arm',root,slot,epoch,stage,id)}});
      if(response.status!==200)throw new Error('barrier arm rejected');
      armed={stage,id,epoch};
    }catch{res.writeHead(502).end();return}
  }
  const upstream=http.request({hostname:'mediamtx',port:8889,method:req.method,path:req.url,headers:req.headers},reply=>{
    res.writeHead(reply.statusCode,reply.headers);reply.pipe(res);
  });
  upstream.setTimeout(20000,()=>upstream.destroy(new Error('upstream timeout')));
  upstream.on('error',()=>res.destroy());
  req.on('aborted',()=>upstream.destroy());
  req.pipe(upstream);
});
media.headersTimeout=5000;media.requestTimeout=20000;
media.listen(8889,'0.0.0.0');
