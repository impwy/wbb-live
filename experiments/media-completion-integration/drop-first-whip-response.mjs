// Isolated fault proxy: consume one real MediaMTX 201 answer but drop it before
// the product API receives even the first response byte. Never use in prod.
import http from 'node:http';

let dropped=false;
const server=http.createServer((request,response)=>{
  const upstream=http.request({hostname:'mediamtx',port:8889,method:request.method,
    path:request.url,headers:request.headers},reply=>{
    if(request.method==='POST'&&reply.statusCode===201&&!dropped){
      dropped=true;
      reply.resume();
      reply.on('end',()=>{console.error('dropped_first_whip_201');response.destroy()});
      return;
    }
    response.writeHead(reply.statusCode,reply.headers);
    reply.pipe(response);
  });
  upstream.setTimeout(10000,()=>upstream.destroy(new Error('upstream timeout')));
  upstream.on('error',()=>response.destroy());
  request.on('aborted',()=>upstream.destroy());
  request.pipe(upstream);
});
server.headersTimeout=5000;
server.requestTimeout=15000;
server.listen(8889,'0.0.0.0');
