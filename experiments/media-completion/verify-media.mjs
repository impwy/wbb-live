// Two real Chrome publishers and two independent receiver pages. All media
// input is synthetic canvas/audio; no OS capture permission is requested.
import {chromium} from '../media-single-rendition/node_modules/playwright/index.mjs';
import {createHash, createHmac, randomUUID} from 'node:crypto';
import {readFileSync} from 'node:fs';
import {execFile} from 'node:child_process';
import {createServer} from 'node:http';
import {promisify} from 'node:util';

const execFileAsync=promisify(execFile);
const env=Object.fromEntries(readFileSync(new URL('./.env',import.meta.url),'utf8').trim().split('\n').map(line=>line.split('=',2)));
const key=Buffer.from(env.WBB_COMPLETION_KEY_HEX,'hex');
const anchor=JSON.parse(readFileSync(new URL('./anchor.json',import.meta.url),'utf8'));
const state={operations:[],workers:[],publishers:[],viewers:[],browsers:[],browserServers:[],armed:[],pendingPosts:[],controllers:[],retired:false};
const portOffset=Number(process.env.WBB_SPIKE_PORT_OFFSET||0);
if(![0,1000].includes(portOffset))throw new Error('unsupported isolated port offset');
const port=n=>n+portOffset;
const mediaNetwork=process.env.WBB_SPIKE_NETWORK||'wbb-media-completion-spike_spike';
const mediaService=process.env.WBB_SPIKE_MEDIA_SERVICE||'mediamtx';
const management=`http://127.0.0.1:${port(29698)}/v3/wbb`;
const api=`http://127.0.0.1:${port(29697)}/v3`;
const auth=`http://127.0.0.1:${port(29699)}`;
let epoch;

function sign(...parts){const h=createHmac('sha256',key);for(const p of parts)h.update(`${Buffer.byteLength(p)}:${p}`);return h.digest('hex')}
function assert(ok,message){if(!ok)throw new Error(message)}
async function finite(name,ms,fn){
  let timer;
  try{return await Promise.race([Promise.resolve().then(fn),new Promise((_,reject)=>{timer=setTimeout(()=>reject(new Error(`${name} timeout`)),ms)})])}
  finally{clearTimeout(timer)}
}
async function http(url,options={},ms=6000){return fetch(url,{...options,signal:options.signal??AbortSignal.timeout(ms)})}
function control(){return 'Basic '+Buffer.from('control:'+sign('control')).toString('base64')}
async function list(kind){
  const r=await http(`${api}/${kind}/list`,{headers:{Authorization:control()}});
  assert(r.status===200,`${kind} API ${r.status}`);
  const body=await r.json();assert(body.pageCount<=1,`${kind} pagination unhandled`);
  return body.items||[];
}
async function lifecycle(){
  const r=await http(`${management}/lifecycle`,{headers:{'X-WBB-Signature':sign('lifecycle',anchor.root,anchor.slot,'','')}});
  assert(r.status===200,`lifecycle ${r.status}`);
  const body=await r.json();assert(body.contract_version==='wbb-media-completion/1' && body.slot===anchor.slot && body.root===anchor.root,'lifecycle tuple');
  return body;
}
function managementHeaders(purpose,id){return {'X-WBB-Epoch':epoch,'X-WBB-Signature':sign(purpose,anchor.root,anchor.slot,epoch,id)}}
function barrierHeaders(purpose,stage,id){return {'X-WBB-Epoch':epoch,'X-WBB-Signature':sign(`barrier-${purpose}`,anchor.root,anchor.slot,epoch,stage,id)}}
async function barrier(stage,id,action){
  const r=await http(`${management}/barriers/${stage}/${id}${action?`/${action}`:''}`,{method:action?'POST':'GET',headers:barrierHeaders(action||'query',stage,id)});
  assert(r.status===200,`barrier ${stage} ${action||'query'} ${r.status}`);
  if(action==='arm')state.armed.push({stage,id});
  if(action==='release')state.armed=state.armed.filter(x=>x.stage!==stage || x.id!==id);
  return r.json();
}
async function waitBarrier(stage,id){
  return waitFor(`${stage} barrier arrival`,12000,async()=>(await barrier(stage,id)).arrived);
}
async function query(id){const r=await http(`${management}/operations/${id}?epoch=${epoch}`,{headers:managementHeaders('query',id)});assert(r.status===200,`query ${r.status}`);return r.json()}
async function close(id){const r=await http(`${management}/operations/${id}/close`,{method:'POST',headers:managementHeaders('close',id)});assert(r.status===202,`close ${r.status}`);return r.json()}
async function waitFor(name,ms,fn){
  const end=Date.now()+ms;let last;
  while(Date.now()<end){last=await fn();if(last)return last;await new Promise(resolve=>setTimeout(resolve,200))}
  throw new Error(`${name} timed out`);
}
async function allow(id,path){
  state.operations.push({id,path});
  const r=await http(`${auth}/fixture/allow`,{method:'POST',headers:{'Content-Type':'application/json','X-WBB-Signature':sign('fixture-allow',id,path)},body:JSON.stringify({operation:id,path})});
  assert(r.status===204,`allow ${r.status}`);
}
async function revoke(id,path){
  const r=await http(`${auth}/fixture/revoke`,{method:'POST',headers:{'Content-Type':'application/json','X-WBB-Signature':sign('fixture-revoke',id,path)},body:JSON.stringify({operation:id,path})});
  assert(r.status===204,`revoke ${r.status}`);
}
async function launchOwnedBrowser(label){
  const server=await chromium.launchServer({channel:'chrome',headless:true,args:['--autoplay-policy=no-user-gesture-required'],timeout:10000});
  state.browserServers.push({label,server});
  try{
    const browser=await chromium.connect(server.wsEndpoint(),{timeout:10000});
    state.browsers.push(browser);
    return browser;
  }catch(error){server.process().kill('SIGKILL');throw error}
}

async function newPublisher(label,freq){
  const browser=await launchOwnedBrowser(`publisher ${label}`);
  const page=await browser.newPage();page.setDefaultTimeout(10000);state.publishers.push(page);
  await finite(`${label} navigation`,10000,()=>page.goto(`http://127.0.0.1:${port(29889)}/`));
  const offer=await finite(`${label} synthetic offer`,12000,()=>page.evaluate(async ({freq,label})=>{
    const canvas=document.createElement('canvas');canvas.width=640;canvas.height=360;
    const ctx=canvas.getContext('2d');let frame=0;
    const paint=()=>{ctx.fillStyle=frame++%2?'#0b60b2':'#e09a2c';ctx.fillRect(0,0,640,360);ctx.fillStyle='white';ctx.font='48px sans-serif';ctx.fillText(`${label} ${frame}`,20,60)};
    paint();const timer=setInterval(paint,33);
    const video=canvas.captureStream(30).getVideoTracks()[0];
    const audio=new AudioContext();await audio.resume();const dest=audio.createMediaStreamDestination();
    const osc=audio.createOscillator();const gain=audio.createGain();osc.frequency.value=freq;gain.gain.value=0.12;osc.connect(gain).connect(dest);osc.start();
    const sound=dest.stream.getAudioTracks()[0];
    const pc=new RTCPeerConnection();
    const videoSender=pc.addTrack(video);
    const audioSender=pc.addTransceiver('audio',{direction:'sendonly'}).sender;
    const transceiver=pc.getTransceivers().find(x=>x.sender===videoSender);
    const codecs=RTCRtpSender.getCapabilities('video')?.codecs.filter(c=>c.mimeType.toLowerCase()==='video/h264')||[];
    if(!codecs.length)throw new Error('Chrome H264 unavailable');
    transceiver.setCodecPreferences(codecs);
    window.__media={pc,video,sound,audio,osc,timer,audioSender};
    const local=await pc.createOffer();await pc.setLocalDescription(local);
    await Promise.race([new Promise(resolve=>{if(pc.iceGatheringState==='complete')resolve();else pc.addEventListener('icegatheringstatechange',()=>{if(pc.iceGatheringState==='complete')resolve()})}),new Promise(resolve=>setTimeout(resolve,2500))]);
    return pc.localDescription.sdp;
  },{freq,label}));
  return {browser,page,offer};
}
async function connectPublisher(owner,answer,label){
  await finite(`${label} answer`,7000,()=>owner.page.evaluate(async answer=>{
    await window.__media.pc.setRemoteDescription({type:'answer',sdp:answer});
  },answer));
  try{
    await waitFor(`${label} first video RTP`,5500,async()=>owner.page.evaluate(async()=>{
      const stats=await window.__media.pc.getStats();
      for(const row of stats.values())if(row.type==='outbound-rtp' && row.kind==='video' && row.packetsSent>3)return true;
      return false;
    }));
  }catch(error){
    const detail=await owner.page.evaluate(async()=>{
      const pc=window.__media.pc,stats=await pc.getStats();
      return {connection:pc.connectionState,ice:pc.iceConnectionState,directions:pc.getTransceivers().map(x=>x.currentDirection),outbound:[...stats.values()].filter(x=>x.type==='outbound-rtp').map(x=>({kind:x.kind,packets:x.packetsSent,frames:x.framesEncoded}))};
    });
    throw new Error(`${error.message}: ${JSON.stringify(detail)}`);
  }
  await finite(`${label} attach audio`,3000,()=>owner.page.evaluate(()=>window.__media.audioSender.replaceTrack(window.__media.sound)));
}

async function publish(label,frequency){
  const sid=randomUUID(),id=randomUUID(),path=`wbb/${sid}/1/source`;
  await allow(id,path);
  const owner=await newPublisher(label,frequency);
  const r=await sendOffer(id,path,owner.offer,16000);
  const answer=await r.text();assert(r.status===201,`${label} WHIP ${r.status}`);
  const location=r.headers.get('Location');assert(location?.startsWith(`/${path}/whip/`),`${label} unsafe Location`);
  await connectPublisher(owner,answer,label);
  await waitFor(`${label} outbound video`,12000,async()=>owner.page.evaluate(async()=>{
    const stats=await window.__media.pc.getStats();let video=null,codec=null;
    for(const row of stats.values())if(row.type==='outbound-rtp' && row.kind==='video')video=row;
    if(video?.codecId)codec=stats.get(video.codecId);
    return video?.packetsSent>20 && video?.framesEncoded>3 && codec?.mimeType?.toLowerCase()==='video/h264' ? {packets:video.packetsSent,frames:video.framesEncoded,codec:codec.mimeType}:null;
  }));
  await waitFor(`${label} two-track source path`,12000,async()=>{
    const source=(await list('paths')).find(x=>x.name===path && x.ready);
    const codecs=new Set(source?.tracks2?.map(track=>track.codec));
    return codecs.has('H264') && codecs.has('Opus');
  });
  return {label,frequency,sid,id,path,location,...owner};
}
function sendOffer(id,path,offer,ms=30000,signal){
  const basic='Basic '+Buffer.from(`create.${id}:${sign('create',id)}`).toString('base64');
  return http(`http://127.0.0.1:${port(29889)}/${path}/whip`,{method:'POST',headers:{'Content-Type':'application/sdp',Authorization:basic,'X-WBB-Operation':id,'X-WBB-Slot':anchor.slot,'X-WBB-Root':anchor.root,'X-WBB-Epoch':epoch,'X-WBB-Signature':sign('publish',anchor.root,anchor.slot,epoch,id,path,createHash('sha256').update(offer).digest('hex'))},body:offer,signal},ms);
}
function iceFragment(offer){
  const lines=offer.split('\r\n');
  const media=lines.find(line=>line.startsWith('m=video '));
  const mid=lines.find(line=>line.startsWith('a=mid:'));
  const ufrag=lines.find(line=>line.startsWith('a=ice-ufrag:'));
  const pwd=lines.find(line=>line.startsWith('a=ice-pwd:'));
  assert(media && mid && ufrag && pwd,'offer missing ICE fragment fields');
  return [media,mid,ufrag,pwd,''].join('\r\n');
}
async function closeWithoutResponse(id){
  let forward;
  const forwarded=new Promise(resolve=>{forward=resolve});
  const server=createServer(async(_req,res)=>{
    try{
      const upstream=await http(`${management}/operations/${id}/close`,{method:'POST',headers:managementHeaders('close',id)});
      forward(upstream.status);
    }catch(error){forward(error)}
    finally{res.destroy()}
  });
  try{
    await finite('loss relay listen',3000,()=>new Promise((resolve,reject)=>server.listen(0,'127.0.0.1',error=>error?reject(error):resolve())));
    const lost=http(`http://127.0.0.1:${server.address().port}/close`,{method:'POST'},5000).then(()=>false,()=>true);
    const status=await finite('forwarded close',6000,()=>forwarded);
    const responseLost=await finite('close response lost',6000,()=>lost);
    assert(status===202 && responseLost,'close relay did not prove upstream close and client response loss');
    return query(id);
  }finally{
    server.closeAllConnections();
    await finite('loss relay close',3000,()=>new Promise(resolve=>server.close(resolve)));
  }
}

async function docker(args,timeout=12000){
  try{return await execFileAsync('docker',args,{timeout,maxBuffer:256*1024})}
  catch{throw new Error('isolated Docker command failed')}
}
async function workerFor(publisher){
  const {sid,path,label}=publisher;
  const name=`wbb-07a1-ffmpeg-${sid.slice(0,8)}`;
  const user=`worker.${sid}.1.1`,password=sign('worker',sid,'1','1');
  const source=`rtsp://${user}:${password}@${mediaService}:8554/${path}`;
  const output=`rtsp://${user}:${password}@${mediaService}:8554/wbb/${sid}/1/720p`;
  state.workers.push(name);
  await docker(['run','-d','--name',name,'--label','wbb.spike=07a1','--network',mediaNetwork,'--entrypoint','ffmpeg','wbb-media-completion-spike:local','-nostdin','-hide_banner','-loglevel','error','-rtsp_transport','tcp','-i',source,'-map','0:v:0','-map','0:a:0','-c:v','libx264','-preset','veryfast','-tune','zerolatency','-profile:v','main','-pix_fmt','yuv420p','-r','30','-g','60','-keyint_min','60','-sc_threshold','0','-b:v','1500k','-maxrate','1700k','-bufsize','500k','-c:a','aac','-b:a','96k','-ar','48000','-ac','2','-f','rtsp','-rtsp_transport','tcp',output],15000);
  const outputPath=`wbb/${sid}/1/720p`;
  await waitFor(`${label} converted path`,20000,async()=>(await list('paths')).some(x=>x.name===outputPath && x.ready));
  const identity=await docker(['exec',name,'cat','/proc/1/stat']);
  const fields=identity.stdout.trim().split(' ');
  return {name,pid:fields[0],startTicks:fields[21],outputPath};
}

async function newViewer(publisher,viewerBrowser){
  const page=await viewerBrowser.newPage();page.setDefaultTimeout(10000);state.viewers.push(page);
  await finite(`${publisher.label} viewer navigation`,10000,()=>page.goto(`http://127.0.0.1:${port(29888)}/`));
  await finite(`${publisher.label} hls.js`,10000,()=>page.addScriptTag({path:new URL('../media-single-rendition/web/vendor/hls.min.js',import.meta.url).pathname}));
  await finite(`${publisher.label} viewer init`,10000,()=>page.evaluate(async({url})=>{
    if(!Hls.isSupported())throw new Error('MSE unavailable');
    const video=document.createElement('video');video.autoplay=true;video.playsInline=true;document.body.append(video);
    const audio=new AudioContext();await audio.resume();const source=audio.createMediaElementSource(video);const analyser=audio.createAnalyser();analyser.fftSize=4096;source.connect(analyser);analyser.connect(audio.destination);
    const hls=new Hls({enableWorker:false,lowLatencyMode:true});hls.attachMedia(video);hls.loadSource(url);window.__viewer={video,audio,analyser,hls,loadedBytes:0};
    hls.on(Hls.Events.FRAG_LOADED,(_,data)=>{window.__viewer.loadedBytes+=data.payload?.byteLength||0});
    await video.play().catch(()=>{});
  },{url:`http://127.0.0.1:${port(29888)}/${publisher.outputPath||`wbb/${publisher.sid}/1/720p`}/index.m3u8`}));
  await waitFor(`${publisher.label} decoded frames`,25000,async()=>{
    const frames=await page.evaluate(async()=>{await window.__viewer.video.play().catch(()=>{});return window.__viewer.video.getVideoPlaybackQuality()?.totalVideoFrames||0});return frames>4?frames:null;
  });
  await waitFor(`${publisher.label} decoded audio`,12000,async()=>{
    const sample=await audioSample(page,publisher.frequency);
    return sample.valid?sample:null;
  });
  return page;
}

async function viewerProgress(page){return page.evaluate(()=>({frames:window.__viewer.video.getVideoPlaybackQuality()?.totalVideoFrames||0,time:window.__viewer.video.currentTime}))}
async function audioSample(page,freq){
  return page.evaluate(freq=>{
    const {audio,analyser}=window.__viewer;const bins=new Float32Array(analyser.frequencyBinCount);analyser.getFloatFrequencyData(bins);
    const peak=(low,high)=>{let result=-Infinity;for(let j=Math.floor(low*analyser.fftSize/audio.sampleRate);j<=Math.ceil(high*analyser.fftSize/audio.sampleRate);j++)result=Math.max(result,bins[j]);return result};
    const tone=peak(freq-18,freq+18),noise=Math.max(peak(300,340),peak(780,820));
    return {tone,noise,valid:tone>-65 && tone>noise+6};
  },freq);
}
async function bSample(page,b,useAPI=true){
  const [viewer,audio]=await Promise.all([viewerProgress(page),audioSample(page,b.frequency)]);
  assert(audio.valid,'B decoded audio missing');
  if(!useAPI){
    // An auth or WebRTC event-loop barrier can block the matching API list.
    // Keep observing the already connected publisher and HLS receiver.
    const [outbound,loadedBytes]=await Promise.all([
      b.page.evaluate(async()=>{
        const stats=await window.__media.pc.getStats();let video=0,audio=0;
        for(const row of stats.values())if(row.type==='outbound-rtp'){
          if(row.kind==='video')video+=row.bytesSent||0;
          if(row.kind==='audio')audio+=row.bytesSent||0;
        }
        return {video,audio};
      }),
      page.evaluate(()=>window.__viewer.loadedBytes),
    ]);
    return {...viewer,sourceBytes:outbound.video,outputBytes:outbound.audio,ingressBytes:loadedBytes};
  }
  const [paths,sessions]=await Promise.all([list('paths'),list('webrtcsessions')]);
  const source=paths.find(x=>x.name===b.path),output=paths.find(x=>x.name===b.outputPath),ingress=sessions.find(x=>x.path===b.path);
  assert(source?.ready && output?.ready && ingress,'B media path or ingress lost');
  return {...viewer,sourceBytes:source.inboundBytes,outputBytes:output.inboundBytes,ingressBytes:ingress.inboundBytes};
}
function lifecycleIdentity(x){return [x.contract_version,x.root,x.slot,x.epoch,x.epoch_sequence].join('|')}
async function workerState(name){
  const r=await docker(['inspect','--format','{{.State.Running}} {{.State.Pid}} {{.State.ExitCode}}',name]);
  const [running,pid,exitCode]=r.stdout.trim().split(' ');
  return {running:running==='true',pid:Number(pid),exitCode:Number(exitCode)};
}
async function absentFor(publisher){
  const [paths,webrtc,rtsp]=await Promise.all([list('paths'),list('webrtcsessions'),list('rtspsessions')]);
  const prefix=`wbb/${publisher.sid}/1/`;
  return !paths.some(x=>x.name.startsWith(prefix) && x.ready) &&
    !webrtc.some(x=>x.path.startsWith(prefix)) && !rtsp.some(x=>x.path.startsWith(prefix));
}
async function continuity(page,b,closed,useAPI=true){
  const samples=[await bSample(page,b,useAPI)];
  let lastProgressAt=Date.now(),progressIntervals=0,ingressIntervals=0;
  for(let i=0;i<8;i++){
    await new Promise(resolve=>setTimeout(resolve,850));
    const now=await bSample(page,b,useAPI),last=samples.at(-1);
    assert(now.frames>=last.frames && now.time>=last.time,'B playback regressed');
    assert(now.sourceBytes>=last.sourceBytes && now.outputBytes>=last.outputBytes && now.ingressBytes>=last.ingressBytes,'B media counters regressed');
    if(now.frames>last.frames && now.time>last.time){lastProgressAt=Date.now();progressIntervals++}
    assert(Date.now()-lastProgressAt<2600,'B playback stalled beyond 2.6 seconds');
    if(now.ingressBytes>last.ingressBytes && now.sourceBytes>last.sourceBytes && now.outputBytes>last.outputBytes)ingressIntervals++;
    samples.push(now);
  }
  assert(progressIntervals>=5 && ingressIntervals>=5,'B media was not continuous across intervals');
  assert(closed(), 'A completion was not observed during B continuity window');
  return {samples:samples.length,progressIntervals,ingressIntervals,framesAdded:samples.at(-1).frames-samples[0].frames,timeAdded:samples.at(-1).time-samples[0].time};
}
async function cleanup(){
  const errors=[];
  async function attempt(label,fn){try{await fn()}catch(error){errors.push(`${label}: ${error.message}`)}}
  for(const {id} of state.operations)await attempt('close operation',async()=>{
    if(state.retired)assert((await query(id)).state==='completed','retired epoch operation not completed');
    else await close(id);
  });
  for(const controller of state.controllers)controller.abort();
  for(const {stage,id} of [...state.armed])await attempt('release barrier',()=>barrier(stage,id,'release'));
  for(const pending of state.pendingPosts)await attempt('pending POST settlement',()=>finite('pending POST settlement',12000,()=>pending));
  for(const page of state.publishers)await attempt('publisher cleanup',()=>finite('publisher cleanup',4000,()=>page.evaluate(async()=>{
    const m=window.__media;if(!m)return;clearInterval(m.timer);m.video.stop();m.sound.stop();m.osc.stop();m.pc.close();await m.audio.close();
  })));
  for(const page of state.viewers)await attempt('viewer cleanup',()=>finite('viewer cleanup',4000,()=>page.evaluate(async()=>{
    const v=window.__viewer;if(v){v.hls.destroy();await v.audio.close();v.video.remove()}
  })));
  for(const page of [...state.publishers,...state.viewers])await attempt('page close',()=>finite('page close',5000,()=>page.close({runBeforeUnload:false})));
  for(const {id} of state.operations)await attempt('operation completion',()=>waitFor('operation completed',12000,async()=>(await query(id)).state==='completed'));
  for(const name of state.workers)await attempt('worker removal',async()=>{
    const existing=await docker(['ps','-a','--filter',`name=^/${name}$`,'--format','{{.Names}}']);
    if(existing.stdout.trim()===name)await docker(['rm','-f',name],8000);
    const remaining=await docker(['ps','-a','--filter',`name=^/${name}$`,'--format','{{.Names}}']);
    assert(remaining.stdout.trim()==='', 'owned worker container remains');
  });
  for(const {id,path} of state.operations)await attempt('revoke fixture',()=>revoke(id,path));
  for(const {label,server} of state.browserServers)await attempt(`${label} browser process exit`,async()=>{
    const child=server.process();
    try{await finite('browser graceful close',5000,()=>server.close())}catch{}
    if(child.exitCode===null && child.signalCode===null){
      child.kill('SIGKILL');
      await finite('browser process kill',5000,()=>new Promise(resolve=>{
        if(child.exitCode!==null || child.signalCode!==null)resolve();
        else child.once('exit',resolve);
      }));
    }
    assert(child.exitCode!==null || child.signalCode!==null,'owned browser process remains');
  });
  for(const {id,path} of state.operations)await attempt('owned resource absence',async()=>{
    const sid=path.split('/')[1];
    assert(await absentFor({sid}),'owned media resource remains');
    assert((await query(id)).state==='completed','owned operation is not completed');
  });
  if(errors.length)throw new Error(`cleanup failed: ${errors.join('; ')}`);
}

async function main(lostClose=false,activeStage=null){
  const initialLifecycle=await lifecycle();epoch=initialLifecycle.epoch;
  let a,b,workerA,workerB,mainError,summary;
  try{
    a=await publish('A',440);console.error('A source ready');
    b=await publish('B',660);console.error('B source ready');
    workerA=await workerFor(a);a.outputPath=workerA.outputPath;
    workerB=await workerFor(b);b.outputPath=workerB.outputPath;
    const viewerBrowser=await launchOwnedBrowser('viewer');
    const viewerA=await newViewer(a,viewerBrowser);
    const viewerB=await newViewer(b,viewerBrowser);
    let pendingPatch,patchState='not-sent';
    if(activeStage){
      await barrier(activeStage,a.id,'arm');
      if(activeStage==='during_ice_patch'){
        pendingPatch=http(`http://127.0.0.1:${port(29889)}${a.location}`,{method:'PATCH',headers:{'Content-Type':'application/trickle-ice-sdpfrag'},body:iceFragment(a.offer)},15000)
          .then(res=>({res}),error=>({error}));
        pendingPatch.then(result=>{patchState=result.error?'transport-error':`HTTP-${result.res.status}`});
        state.pendingPosts.push(pendingPatch);
      }
      try{await waitBarrier(activeStage,a.id)}catch(error){throw new Error(`${error.message}; patch=${patchState}`)}
    }
    let aCompleted=false;
    const [closingResult,monitorResult]=await Promise.allSettled([
      (async()=>{
        const closedA=lostClose?await closeWithoutResponse(a.id):await close(a.id);
        assert(activeStage?closedA.state==='closing':['closing','completed'].includes(closedA.state),`A close ${closedA.state}`);
        if(activeStage){
          assert((await query(a.id)).state==='closing',`${activeStage} completed while held`);
          await new Promise(resolve=>setTimeout(resolve,1600));
          await barrier(activeStage,a.id,'release');
          if(pendingPatch){const result=await pendingPatch;if(result.res)await result.res.text()}
        }
        await waitFor('A completed',12000,async()=>{const q=await query(a.id);return q.state==='completed'?q:null});
        aCompleted=true;
      })(),
      continuity(viewerB,b,()=>aCompleted),
    ]);
    if(closingResult.status==='rejected')throw closingResult.reason;
    if(monitorResult.status==='rejected')throw monitorResult.reason;
    const progress=monitorResult.value;
    await waitFor('A media absent',10000,()=>absentFor(a));
    const aExited=await waitFor('A FFmpeg exited',10000,async()=>{
      const result=await workerState(workerA.name);return !result.running && result.pid===0?result:null;
    });
    const currentB=await docker(['exec',workerB.name,'cat','/proc/1/stat']);
    const fields=currentB.stdout.trim().split(' ');
    assert(fields[0]===workerB.pid && fields[21]===workerB.startTicks,'B FFmpeg identity changed');
    assert((await list('paths')).some(x=>x.name===workerB.outputPath && x.ready),'B output lost');
    assert((await list('webrtcsessions')).some(x=>x.path===b.path),'B ingress lost');
    const afterLifecycle=await lifecycle();
    assert(lifecycleIdentity(initialLifecycle)===lifecycleIdentity(afterLifecycle),'B lifecycle tuple or epoch changed');
    assert((await query(b.id)).state==='active','B operation no longer active');
    summary={a:'completed',aFFmpegExited:true,aExitCode:aExited.exitCode,b:'active',bContinuity:progress,bFFmpegStable:true,bLifecycleStable:true,aMediaAbsent:true,tonesHz:[440,660],closeResponseLost:lostClose,activeBarrier:activeStage};
    await close(b.id);
    await waitFor('B completed',10000,async()=>{const q=await query(b.id);return q.state==='completed'?q:null});
  }catch(error){mainError=error}
  try{await cleanup()}catch(error){mainError=mainError?new Error(`${mainError.message}; ${error.message}`):error}
  if(mainError)throw mainError;
  console.log(JSON.stringify({...summary,cleanup:'verified'}));
}

async function barrierMain(stage,cancelPost=false){
  assert(['before_register','before_session_initialize','during_auth','before_add_publisher','before_completion_response'].includes(stage),'unsupported barrier stage');
  const initialLifecycle=await lifecycle();epoch=initialLifecycle.epoch;
  let b,workerB,mainError,summary;
  try{
    b=await publish('B',660);workerB=await workerFor(b);b.outputPath=workerB.outputPath;
    const viewerBrowser=await launchOwnedBrowser('barrier viewer');
    const viewerB=await newViewer(b,viewerBrowser);
    const a={sid:randomUUID(),id:randomUUID(),label:'A',frequency:440};
    a.path=`wbb/${a.sid}/1/source`;
    await allow(a.id,a.path);
    const owner=await newPublisher('A',440);
    await barrier(stage,a.id,'arm');
    const controller=cancelPost?new AbortController():null;
    if(controller)state.controllers.push(controller);
    const pending=sendOffer(a.id,a.path,owner.offer,30000,controller?.signal).then(res=>({res}),error=>({error}));
    state.pendingPosts.push(pending);
    if(stage==='before_add_publisher'){
      const result=await pending;
      if(result.error)throw result.error;
      assert(result.res.status===201,`A pre-barrier WHIP ${result.res.status}`);
      const answer=await result.res.text();
      await connectPublisher(owner,answer,'A before AddPublisher');
    }
    await waitBarrier(stage,a.id);
    let aCompleted=false;
    const [race,monitor]=await Promise.allSettled([
      (async()=>{
        if(cancelPost){
          controller.abort();
          const result=await pending;
          assert(result.error,'cancelled WHIP POST unexpectedly delivered a response');
        }
        const first=await close(a.id);
        assert(first.state===(stage==='before_register'?'completed':'closing'),`${stage} early close ${first.state}`);
        if(stage!=='before_register')assert((await query(a.id)).state==='closing',`${stage} completed while held`);
        await new Promise(resolve=>setTimeout(resolve,1600));
        await barrier(stage,a.id,'release');
        const result=await pending;
        if(result.error && !cancelPost)throw result.error;
        if(!cancelPost && stage!=='before_add_publisher'){
          assert(result.res.status===409,`${stage} late WHIP ${result.res.status}`);
          await result.res.text();
        }
        await waitFor(`${stage} A completed`,12000,async()=>(await query(a.id)).state==='completed');
        aCompleted=true;
      })(),
      continuity(viewerB,b,()=>aCompleted,false),
    ]);
    if(race.status==='rejected')throw race.reason;
    if(monitor.status==='rejected')throw monitor.reason;
    assert(await absentFor(a),`${stage} A resource remains`);
    const currentB=await docker(['exec',workerB.name,'cat','/proc/1/stat']);
    const fields=currentB.stdout.trim().split(' ');
    assert(fields[0]===workerB.pid && fields[21]===workerB.startTicks,`${stage} B FFmpeg identity changed`);
    assert(lifecycleIdentity(initialLifecycle)===lifecycleIdentity(await lifecycle()),`${stage} lifecycle changed`);
    assert((await query(b.id)).state==='active',`${stage} B operation lost`);
    summary={barrierStage:stage,postResponseLost:cancelPost,a:'completed',aMediaAbsent:true,b:'active',bContinuity:monitor.value,bFFmpegStable:true,bLifecycleStable:true};
  }catch(error){mainError=error}
  try{await cleanup()}catch(error){mainError=mainError?new Error(`${mainError.message}; ${error.message}`):error}
  if(mainError)throw mainError;
  console.log(JSON.stringify({...summary,cleanup:'verified'}));
}

async function activeCapacityMain(){
  assert(portOffset===1000,'active capacity requires the separate loopback stack');
  const initial=await lifecycle();epoch=initial.epoch;
  let a,b,workerA,workerB,summary,mainError;
  try{
    assert((await list('paths')).length===0 && (await list('webrtcsessions')).length===0,'capacity stack has other active media');
    a=await publish('A',440);b=await publish('B',660);
    workerA=await workerFor(a);a.outputPath=workerA.outputPath;
    workerB=await workerFor(b);b.outputPath=workerB.outputPath;
    const viewerBrowser=await launchOwnedBrowser('capacity viewer');
    await newViewer(a,viewerBrowser);
    const viewerB=await newViewer(b,viewerBrowser);
    assert((await query(a.id)).state==='active' && (await query(b.id)).state==='active','real A/B not active at capacity');

    const third=randomUUID();
    const full=await http(`${management}/operations/${third}/close`,{method:'POST',headers:managementHeaders('close',third)});
    const fullBody=await full.json();
    assert(full.status===503 && fullBody.state==='capacity_unknown','third close falsely completed at capacity');
    assert((await query(a.id)).state==='active' && (await query(b.id)).state==='active','third close changed A/B');

    await barrier('during_rtp_callback',a.id,'arm');
    await waitBarrier('during_rtp_callback',a.id);
    let aCompleted=false;
    const [stop,monitor]=await Promise.allSettled([
      (async()=>{
        const closed=await close(a.id);
        assert(closed.state==='closing','held A did not remain closing');
        assert((await query(a.id)).state==='closing' && (await query(b.id)).state==='active','held A/B query changed');
        await new Promise(resolve=>setTimeout(resolve,1200));
        assert((await query(a.id)).state==='closing','held A completed before release');
        await barrier('during_rtp_callback',a.id,'release');
        await waitFor('held A completed',12000,async()=>(await query(a.id)).state==='completed');
        aCompleted=true;
      })(),
      continuity(viewerB,b,()=>aCompleted),
    ]);
    if(stop.status==='rejected')throw stop.reason;
    if(monitor.status==='rejected')throw monitor.reason;
    await waitFor('A media absent',10000,()=>absentFor(a));
    await waitFor('A FFmpeg exited',10000,async()=>{const x=await workerState(workerA.name);return !x.running && x.pid===0});
    const currentB=await docker(['exec',workerB.name,'cat','/proc/1/stat']);
    const fields=currentB.stdout.trim().split(' ');
    assert(fields[0]===workerB.pid && fields[21]===workerB.startTicks,'B FFmpeg identity changed');
    assert((await query(b.id)).state==='active','B operation lost');
    assert(lifecycleIdentity(initial)===lifecycleIdentity(await lifecycle()),'capacity epoch changed');
    summary={capacity:2,realOperations:['active','active'],thirdClose:'capacity_unknown/503',heldA:'closing_then_completed',b:'active',bContinuity:monitor.value,bFFmpegStable:true,bLifecycleStable:true,aMediaAbsent:true};
  }catch(error){mainError=error}
  try{await cleanup()}catch(error){mainError=mainError?new Error(`${mainError.message}; ${error.message}`):error}
  if(mainError)throw mainError;
  console.log(JSON.stringify({...summary,cleanup:'verified'}));
}

async function killRestartMain(){
  const initial=await lifecycle();epoch=initial.epoch;
  let a,b,workerA,workerB,summary,mainError;
  try{
    assert((await list('paths')).length===0 && (await list('webrtcsessions')).length===0,'dedicated stack has other active media');
    a=await publish('A',440);b=await publish('B',660);
    workerA=await workerFor(a);a.outputPath=workerA.outputPath;
    workerB=await workerFor(b);b.outputPath=workerB.outputPath;
    const viewerBrowser=await launchOwnedBrowser('restart viewer');
    await newViewer(a,viewerBrowser);await newViewer(b,viewerBrowser);
    await docker(['kill','--signal','KILL','wbb-media-completion-spike-mediamtx-1']);
    await docker(['compose','-p','wbb-media-completion-spike','--env-file','.env','-f','compose.yml','up','-d','mediamtx'],20000);
    const next=await waitFor('new MediaMTX epoch',15000,async()=>{
      try{const current=await lifecycle();return current.epoch!==initial.epoch?current:null}catch{return null}
    });
    assert(next.epoch_sequence===initial.epoch_sequence+1,'restart did not append exactly one epoch');
    state.retired=true;
    assert((await query(a.id)).state==='completed' && (await query(b.id)).state==='completed','killed media epoch was not retired');
    for(const worker of [workerA,workerB])await waitFor('old FFmpeg exit',12000,async()=>{
      const s=await workerState(worker.name);return !s.running && s.pid===0?s:null;
    });
    assert(await absentFor(a) && await absentFor(b),'media from killed epoch remained after restart');
    summary={oldEpochRetired:true,epochSequence:[initial.epoch_sequence,next.epoch_sequence],bothOldOperations:'completed',bothFFmpegExited:true,oldMediaAbsent:true,sharedRestartBContinuity:'not-claimed'};
  }catch(error){mainError=error}
  try{await cleanup()}catch(error){mainError=mainError?new Error(`${mainError.message}; ${error.message}`):error}
  if(mainError)throw mainError;
  console.log(JSON.stringify({...summary,cleanup:'verified'}));
}

// BrowserServer websocket listeners can outlive their browser process. All
// owned child processes and media resources are checked above before exit.
const mode=process.argv[2];
const activeStages=['during_rtp_callback','during_pli','during_ice_patch'];
const run=mode===undefined?main():mode==='lost_close'?main(true):mode==='cancel_post'?barrierMain('before_completion_response',true):mode==='kill_restart'?killRestartMain():mode==='active_capacity'?activeCapacityMain():activeStages.includes(mode)?main(false,mode):barrierMain(mode);
run.then(
  ()=>process.exit(0),
  error=>{console.error(`media_failure=${error.message}`);process.exit(1)},
);
