// Isolated 04-B1 evidence: two separate Chrome processes, synthetic canvas and
// mixed oscillators only. No capture permission or user device is requested.
import { chromium } from '../media-single-rendition/node_modules/playwright/index.mjs';
import crypto, { X509Certificate } from 'node:crypto';
import tls from 'node:tls';
import path from 'node:path';
import fs from 'node:fs';
import { execFile, spawn } from 'node:child_process';
import { promisify } from 'node:util';
import { fileURLToPath } from 'node:url';

const ORIGIN = 'https://localhost:19543';
const here = path.dirname(fileURLToPath(import.meta.url));
const execFileAsync = promisify(execFile);
const testEnvFile = process.env.WBB_TEST_ENV_FILE || path.join(here,'.env');
const testDatabase = process.env.WBB_CAPACITY_DB || 'wbb';
if(!/^wbb(?:_[a-z0-9_]+)?$/.test(testDatabase))throw new Error('invalid isolated database name');

function controlHeader() {
  const env = fs.readFileSync(testEnvFile,'utf8');
  const secret = env.match(/^WBB_MEDIA_SECRET=(.+)$/m)?.[1];
  if(!secret || secret.length<32)throw new Error('isolated test media secret missing');
  const password=crypto.createHmac('sha256',secret).update('control:api').digest('hex');
  return 'Authorization: Basic '+Buffer.from('control:'+password).toString('base64');
}

async function mediaList(kind) {
  if(!['paths','webrtcsessions','rtspsessions'].includes(kind))throw new Error('invalid MediaMTX list');
  const header=controlHeader();
  return await finite(`${kind} snapshot`,6000,()=>new Promise((resolve,reject)=>{
    const child=spawn('docker',['exec','-i','wbb-media-completion-integration-worker-1','sh','-c',`read -r auth; wget -q -O - --header "$auth" http://mediamtx:9997/v3/${kind}/list`]);
    let raw='',error='';child.stdout.on('data',chunk=>raw+=chunk);child.stderr.on('data',chunk=>error+=chunk);
    child.on('error',reject);
    child.on('close',code=>{if(code!==0)return reject(new Error(`${kind} snapshot failed: ${error.trim()}`));try{const value=JSON.parse(raw);if(value.pageCount>1)throw new Error('snapshot pagination requires a full listing');resolve(value.items||[])}catch(e){reject(e)}});
    child.stdin.end(header+'\n');
  }));
}

async function dbRun(sid) {
  if(!/^[0-9a-f-]{36}$/.test(sid))throw new Error('invalid session id');
  const sql=`SELECT state||','||COALESCE(pid::text,'')||','||fencing_token::text||','||round(extract(epoch from lease_until-now())*1000)::text FROM media_worker_runs WHERE broadcast_session_id='${sid}'`;
  const {stdout}=await execFileAsync('docker',['exec','wbb-media-completion-integration-postgres-1','psql','-U','wbb','-d',testDatabase,'-At','-c',sql],{timeout:5000});
  const [state,pid,fence,margin]=stdout.trim().split(',');return {state,pid:Number(pid),fence:Number(fence),leaseMarginMs:Number(margin)};
}

async function dedicatedSQL(sql) {
  await execFileAsync('docker',['exec','wbb-media-completion-integration-postgres-1','psql','-v','ON_ERROR_STOP=1','-U','wbb','-d',testDatabase,'-c',sql],{timeout:5000});
}

async function removeTransitionGuard() {
  await dedicatedSQL('DROP TRIGGER IF EXISTS wbb_test_reject_stopping ON broadcast_sessions; DROP FUNCTION IF EXISTS wbb_test_reject_stopping()');
}

async function removeRunExitGuard() {
  await dedicatedSQL('DROP TRIGGER IF EXISTS wbb_test_reject_run_exit ON media_worker_runs; DROP FUNCTION IF EXISTS wbb_test_reject_run_exit()');
}

async function dbCompletion(sid) {
  if(!/^[0-9a-f-]{36}$/.test(sid))throw new Error('invalid session id');
  const sql=`SELECT state||','||COALESCE(completion_evidence_kind,'')||','||COALESCE(completion_contract_version,'')||','||COALESCE(completion_root,'')||','||COALESCE(completion_slot,'')||','||COALESCE(completion_epoch,'')||','||COALESCE(completion_epoch_sequence::text,'')||','||COALESCE(completion_revision::text,'') FROM media_create_operations WHERE broadcast_session_id='${sid}'`;
  const {stdout}=await execFileAsync('docker',['exec','wbb-media-completion-integration-postgres-1','psql','-U','wbb','-d',testDatabase,'-At','-c',sql],{timeout:5000});
  const [state,evidence,contract,root,slot,epoch,sequence,revision]=stdout.trim().split(',');
  const env=fs.readFileSync(testEnvFile,'utf8');
  const expectedRoot=env.match(/^WBB_COMPLETION_ROOT=(.+)$/m)?.[1];
  const expectedSlot=env.match(/^WBB_COMPLETION_SLOT=(.+)$/m)?.[1];
  return state==='closed'&&evidence==='completed'&&contract==='wbb-media-completion/1'&&
    root===expectedRoot&&slot===expectedSlot&&/^[0-9a-f]{32}$/.test(epoch)&&Number(sequence)>0&&Number(revision)>=0;
}

async function dbSessionOperation(sid) {
  if(!/^[0-9a-f-]{36}$/.test(sid))throw new Error('invalid session id');
  const sql=`SELECT s.state||','||o.state FROM broadcast_sessions s JOIN media_create_operations o ON o.broadcast_session_id=s.id WHERE s.id='${sid}'`;
  const {stdout}=await execFileAsync('docker',['exec','wbb-media-completion-integration-postgres-1','psql','-U','wbb','-d',testDatabase,'-At','-c',sql],{timeout:5000});
  const [session,operation]=stdout.trim().split(',');return {session,operation};
}

async function dbHasStoppingEvent(sid) {
  if(!/^[0-9a-f-]{36}$/.test(sid))throw new Error('invalid session id');
  const sql=`SELECT EXISTS(SELECT 1 FROM outbox_events WHERE aggregate_id='${sid}' AND type='broadcast.session.stopping')`;
  const {stdout}=await execFileAsync('docker',['exec','wbb-media-completion-integration-postgres-1','psql','-U','wbb','-d',testDatabase,'-At','-c',sql],{timeout:5000});
  return stdout.trim()==='t';
}

async function dbResourcePath(sid) {
  if(!/^[0-9a-f-]{36}$/.test(sid))throw new Error('invalid session id');
  const sql=`SELECT upstream_resource_path FROM media_create_operations WHERE broadcast_session_id='${sid}'`;
  const {stdout}=await execFileAsync('docker',['exec','wbb-media-completion-integration-postgres-1','psql','-U','wbb','-d',testDatabase,'-At','-c',sql],{timeout:5000});
  return stdout.trim();
}

async function setResourcePath(sid, resource) {
  if(!/^[0-9a-f-]{36}$/.test(sid)||!/^\/[a-zA-Z0-9/_-]+$/.test(resource))throw new Error('unsafe test resource');
  const sql=`UPDATE media_create_operations SET upstream_resource_path='${resource}' WHERE broadcast_session_id='${sid}'`;
  await execFileAsync('docker',['exec','wbb-media-completion-integration-postgres-1','psql','-U','wbb','-d',testDatabase,'-At','-c',sql],{timeout:5000});
}

async function ffmpegExists(pid) {
  if(!Number.isInteger(pid)||pid<1)throw new Error('invalid ffmpeg PID');
  const {stdout}=await execFileAsync('docker',['exec','wbb-media-completion-integration-worker-1','sh','-c',`if [ -r /proc/${pid}/comm ]; then cat /proc/${pid}/comm; fi`],{timeout:5000});
  return stdout.trim()==='ffmpeg';
}

async function finite(label, milliseconds, task) {
  let timer;
  try {
    return await Promise.race([
      Promise.resolve().then(task),
      new Promise((_, reject) => { timer = setTimeout(() => reject(new Error(`${label} exceeded ${milliseconds}ms`)), milliseconds); }),
    ]);
  } finally { clearTimeout(timer); }
}

async function closeOwnedBrowser(server,label,errors) {
  const child=server.process();
  try { await finite(`${label} graceful close`,5000,()=>server.close()); } catch {}
  if(child.exitCode===null && child.signalCode===null) {
    child.kill('SIGKILL');
    await finite(`${label} process exit`,5000,()=>new Promise(resolve=>{
      if(child.exitCode!==null||child.signalCode!==null)resolve();
      else child.once('exit',resolve);
    })).catch(error=>errors.push(new Error(`${label}: ${error.message}`)));
  }
  if(child.exitCode===null&&child.signalCode===null)errors.push(new Error(`${label} browser process remains`));
}

async function launchOwnedPair(browserType,args) {
  const servers=[];
  try {
    const publisherServer=await browserType.launchServer({channel:'chrome',headless:true,args,timeout:10000});
    servers.push(publisherServer);
    const viewerServer=await browserType.launchServer({channel:'chrome',headless:true,args,timeout:10000});
    servers.push(viewerServer);
    const publisherBrowser=await browserType.connect(publisherServer.wsEndpoint(),{timeout:10000});
    const viewerBrowser=await browserType.connect(viewerServer.wsEndpoint(),{timeout:10000});
    return {publisherServer,viewerServer,publisherBrowser,viewerBrowser};
  } catch(error) {
    const cleanup=[];
    for(const server of servers.reverse())await closeOwnedBrowser(server,'failed browser launch',cleanup);
    if(cleanup.length)throw new AggregateError([error,...cleanup],'Chrome launch and cleanup failed');
    throw error;
  }
}

function leafPin() {
  return new Promise((resolve, reject) => {
    const socket = tls.connect({host:'localhost',port:19543,servername:'localhost',rejectUnauthorized:false}, () => {
      try {
        const cert = new X509Certificate(socket.getPeerCertificate(true).raw);
        const spki = cert.publicKey.export({type:'spki',format:'der'});
        resolve(crypto.createHash('sha256').update(spki).digest('base64'));
      } catch(e) { reject(e); } finally { socket.end(); }
    });
    socket.setTimeout(3000,()=>{socket.destroy();reject(new Error('TLS leaf pin timeout'))});
    socket.on('error', reject);
  });
}

async function main() {
  const pin = await leafPin();
  const args = [`--ignore-certificate-errors-spki-list=${pin}`, '--autoplay-policy=no-user-gesture-required'];
  const {publisherServer,viewerServer,publisherBrowser,viewerBrowser}=await launchOwnedPair(chromium,args);
  let publisher, viewer, setup, restorePath, summary, failure, managementPaused=false, mediamtxStopped=false, transitionGuardInstalled=false, runExitGuardInstalled=false;
  const cleanupErrors=[];
  try {
    publisher = await finite('publisher page',10000,()=>publisherBrowser.newPage());
    viewer = await finite('viewer page',10000,()=>viewerBrowser.newPage());
    publisher.setDefaultTimeout(10000);
    viewer.setDefaultTimeout(10000);
    await finite('publisher navigation',10000,()=>publisher.goto(ORIGIN+'/'));
    await finite('viewer navigation',10000,()=>viewer.goto(ORIGIN+'/'));
    const email = `media-${crypto.randomUUID()}@example.invalid`;
    const password = crypto.randomBytes(24).toString('hex');
    setup = await finite('account and prepare',15000,()=>publisher.evaluate(async ({email,password}) => {
      const call = async (method,payload,token,route,extra={}) => {
        const response=await fetch(route,{method,credentials:'include',signal:AbortSignal.timeout(7000),headers:{...(payload?{'Content-Type':'application/json'}:{}),...(token?{Authorization:`Bearer ${token}`}:{}) ,...extra},body:payload?JSON.stringify(payload):undefined});
        const raw=await response.text();let value={};try{value=JSON.parse(raw)}catch{}
        return {status:response.status,value};
      };
      const registered=await call('POST',{email,password,displayName:'Synthetic Publisher'},'', '/api/v1/auth/register');
      if(registered.status!==201)throw new Error(`register ${registered.status}`);
      const login=await call('POST',{email,password},'', '/api/v1/auth/login');
      if(login.status!==200)throw new Error(`login ${login.status}`);
      const access=login.value.accessToken;
      const broadcast=await call('POST',{title:'Synthetic media',description:'Local only'},access,'/api/v1/broadcasts');
      if(broadcast.status!==201)throw new Error(`broadcast ${broadcast.status}`);
      const bid=broadcast.value.broadcast.id;
      const started=await call('POST',{source:'screen'},access,`/api/v1/broadcasts/${bid}/sessions`,{'Idempotency-Key':`media-${crypto.randomUUID()}`});
      if(started.status!==202)throw new Error(`prepare ${started.status}`);
      window.__wbb={access,bid,sid:started.value.session.id,grant:started.value.publishGrant};
      return {status:started.status,bid,sid:started.value.session.id};
    },{email,password}));
    console.error('browser phase: prepared');

    const published = await finite('WHIP publication',18000,()=>publisher.evaluate(async () => {
      const canvas=document.createElement('canvas');canvas.width=640;canvas.height=360;
      const ctx=canvas.getContext('2d');let frame=0;
      const paint=()=>{ctx.fillStyle=frame++%2?'#1458bd':'#efae21';ctx.fillRect(0,0,640,360);ctx.fillStyle='white';ctx.font='48px sans-serif';ctx.fillText(String(frame),40,80)};
      paint();const timer=setInterval(paint,33);
      const video=canvas.captureStream(30).getVideoTracks()[0];
      const audio=new AudioContext();await audio.resume();const dest=audio.createMediaStreamDestination();
      const tones=[440,660].map((freq)=>{const osc=audio.createOscillator();const gain=audio.createGain();osc.frequency.value=freq;gain.gain.value=0.08;osc.connect(gain).connect(dest);osc.start();return osc});
      const sound=dest.stream.getAudioTracks()[0];
      const pc=new RTCPeerConnection();pc.addTrack(video);pc.addTrack(sound);
      window.__wbb.media={pc,video,sound,audio,tones,timer,resource:null,etag:null};
      const offer=await pc.createOffer();await pc.setLocalDescription(offer);
      await Promise.race([new Promise(resolve=>{if(pc.iceGatheringState==='complete')resolve();else pc.addEventListener('icegatheringstatechange',()=>{if(pc.iceGatheringState==='complete')resolve()})}),new Promise(resolve=>setTimeout(resolve,2500))]);
      const response=await fetch(window.__wbb.grant.whipUrl,{method:'POST',signal:AbortSignal.timeout(14000),headers:{'Content-Type':'application/sdp',Authorization:`Bearer ${window.__wbb.grant.token}`},body:pc.localDescription.sdp});
      const answer=await response.text();
      if(response.status!==201)throw new Error(`WHIP ${response.status}: ${answer.slice(0,160)}`);
      await pc.setRemoteDescription({type:'answer',sdp:answer});
      window.__wbb.media.resource=response.headers.get('Location');window.__wbb.media.etag=response.headers.get('ETag');
      delete window.__wbb.grant;
      return {status:response.status,resource:window.__wbb.media.resource,etag:window.__wbb.media.etag};
    }));
    if(!published.resource?.startsWith(`/api/v1/live-sessions/${setup.sid}/whip/`))throw new Error('unsafe WHIP Location');
    console.error('browser phase: published');

    let state='';
    for(let i=0;i<25;i++) {
      state=await finite('session state',5000,()=>publisher.evaluate(async()=>{const r=await fetch(`/api/v1/broadcasts/${window.__wbb.bid}/sessions/current`,{signal:AbortSignal.timeout(3000)});return (await r.json()).session?.state}));
      if(state==='live')break;
      console.error(`browser phase: waiting ${state} ${i+1}`);
      if(state==='stopping' || state==='ended')break;
      await new Promise(r=>setTimeout(r,2000));
    }
    if(state!=='live')throw new Error(`session did not become live: ${state}`);
    const liveRun=await dbRun(setup.sid);
    if(liveRun.state!=='running' || !(await ffmpegExists(liveRun.pid)))throw new Error(`LIVE FFmpeg identity missing: ${JSON.stringify(liveRun)}`);

    await finite('hls.js load',10000,()=>viewer.addScriptTag({path:path.join(here,'../media-single-rendition/web/vendor/hls.min.js')}));
    const watching=await finite('viewer playback setup',10000,()=>viewer.evaluate(async (bid)=>{
      const response=await fetch(`/api/v1/broadcasts/${bid}/playback`,{signal:AbortSignal.timeout(5000)});
      if(response.status!==200)throw new Error(`playback ${response.status}`);
      const info=await response.json();
      const video=document.createElement('video');video.autoplay=true;video.playsInline=true;document.body.append(video);
      const audio=new AudioContext();const source=audio.createMediaElementSource(video);const analyser=audio.createAnalyser();analyser.fftSize=4096;
      source.connect(analyser);analyser.connect(audio.destination);await audio.resume();
      const hls=new Hls({enableWorker:false,lowLatencyMode:true});hls.attachMedia(video);hls.loadSource(info.masterUrl);
      window.__wbbViewer={video,hls,audio,analyser,masterUrl:info.masterUrl};
      return {status:response.status,masterUrl:info.masterUrl};
    },setup.bid));
    let frames=0;
    for(let i=0;i<20;i++) {
      await finite('viewer play',3000,()=>viewer.evaluate(()=>window.__wbbViewer.video.play().catch(()=>{})));
      frames=await finite('decoded frame count',3000,()=>viewer.evaluate(()=>window.__wbbViewer.video.getVideoPlaybackQuality()?.totalVideoFrames||0));
      if(frames>3)break;
      await new Promise(r=>setTimeout(r,1000));
    }
    if(frames<=3)throw new Error(`viewer decoded only ${frames} frames`);

    let tones={a:-Infinity,b:-Infinity,noise:-Infinity};
    for(let i=0;i<12;i++) {
      const sample=await finite('audio FFT',3000,()=>viewer.evaluate(()=>{
        const {audio,analyser}=window.__wbbViewer;
        const bins=new Float32Array(analyser.frequencyBinCount);analyser.getFloatFrequencyData(bins);
        const peak=(low,high)=>{let value=-Infinity;for(let j=Math.floor(low*analyser.fftSize/audio.sampleRate);j<=Math.ceil(high*analyser.fftSize/audio.sampleRate);j++)value=Math.max(value,bins[j]);return value};
        return {a:peak(420,460),b:peak(640,680),noise:Math.max(peak(300,350),peak(750,800))};
      }));
      tones={a:Math.max(tones.a,sample.a),b:Math.max(tones.b,sample.b),noise:Math.max(tones.noise,sample.noise)};
      if(tones.a>-65 && tones.b>-65 && tones.a>tones.noise+6 && tones.b>tones.noise+6)break;
      await new Promise(r=>setTimeout(r,500));
    }
    if(!(tones.a>-65 && tones.b>-65 && tones.a>tones.noise+6 && tones.b>tones.noise+6))throw new Error(`two audio tones not decoded: ${JSON.stringify(tones)}`);
    console.error('browser phase: two audio tones decoded');

    const holdSeconds=Number(process.env.WBB_ISOLATED_HOLD_LIVE_SECONDS||0);
    let minLeaseMarginMs=null;
    let holdProgressFrames=0,holdProgressSeconds=0,holdAudioSamples=0,maxStallMs=0,postRestartFrames=null,sawStopping=false,stoppingEvent=false,holdSamples=[];
    if(holdSeconds>0) {
      if(!Number.isInteger(holdSeconds)||holdSeconds>120)throw new Error('invalid isolated hold duration');
      if(process.env.WBB_REQUIRE_CONTINUOUS_PLAYBACK==='1')await viewer.evaluate(()=>{
        const v=window.__wbbViewer;
        v.holdSamples=[];
        const sample=()=>{
          const bins=new Float32Array(v.analyser.frequencyBinCount);v.analyser.getFloatFrequencyData(bins);
          const peak=(lo,hi)=>{let value=-Infinity;for(let k=Math.floor(lo*v.analyser.fftSize/v.audio.sampleRate);k<=Math.ceil(hi*v.analyser.fftSize/v.audio.sampleRate);k++)value=Math.max(value,bins[k]);return value};
          v.holdSamples.push([Date.now(),v.video.getVideoPlaybackQuality()?.totalVideoFrames||0,v.video.currentTime,
            peak(420,460),peak(640,680),Math.max(peak(300,350),peak(750,800))]);
        };
        sample();v.holdTimer=setInterval(sample,250);
      });
      console.error(`browser phase: holding live ${holdSeconds}s session=${setup.sid}`);
      let lastFrames=frames,lastTime=await viewer.evaluate(()=>window.__wbbViewer.video.currentTime),lastProgressAt=Date.now();
      for(let i=0;i<holdSeconds;i++) {
        await new Promise(resolve=>setTimeout(resolve,1000));
        if(process.env.WBB_CHECK_CONTINUOUS_LEASE==='1') {
          const run=await dbRun(setup.sid);
          minLeaseMarginMs=Math.min(minLeaseMarginMs??Infinity,run.leaseMarginMs);
          if(run.state!=='running'||run.pid!==liveRun.pid||run.leaseMarginMs<=0)throw new Error(`lease interrupted during hold: ${JSON.stringify(run)}`);
        }
        if(process.env.WBB_EXPECT_CONTROL_OUTAGE==='1') {
          const observed=await finite('control outage state',5000,()=>publisher.evaluate(async()=>{const r=await fetch(`/api/v1/broadcasts/${window.__wbb.bid}/sessions/current`,{signal:AbortSignal.timeout(3000)});return (await r.json()).session?.state}));
          sawStopping ||= observed==='stopping';
        }
        if(process.env.WBB_REQUIRE_CONTINUOUS_PLAYBACK==='1') {
          const progress=await finite('viewer hold progress',3000,()=>viewer.evaluate(()=>({frames:window.__wbbViewer.video.getVideoPlaybackQuality()?.totalVideoFrames||0,time:window.__wbbViewer.video.currentTime})));
          if(progress.frames>lastFrames && progress.time>lastTime+0.01) {
            holdProgressFrames+=progress.frames-lastFrames;
            holdProgressSeconds+=progress.time-lastTime;
            lastProgressAt=Date.now();
          }
          maxStallMs=Math.max(maxStallMs,Date.now()-lastProgressAt);
          if(maxStallMs>12000)throw new Error(`viewer stalled during hold for ${maxStallMs}ms`);
          lastFrames=progress.frames;lastTime=progress.time;
          if(i%5===0) {
            const audioProgress=await finite('viewer hold audio',3000,()=>viewer.evaluate(async()=>{
              const {analyser,audio}=window.__wbbViewer;
              const peak=(bins,lo,hi)=>{let value=-Infinity;for(let k=Math.floor(lo*analyser.fftSize/audio.sampleRate);k<=Math.ceil(hi*analyser.fftSize/audio.sampleRate);k++)value=Math.max(value,bins[k]);return value};
              let a=-Infinity,b=-Infinity,noise=-Infinity;
              for(let sample=0;sample<5;sample++){
                const bins=new Float32Array(analyser.frequencyBinCount);analyser.getFloatFrequencyData(bins);
                a=Math.max(a,peak(bins,420,460));b=Math.max(b,peak(bins,640,680));
                noise=Math.max(noise,peak(bins,300,350),peak(bins,750,800));
                if(a>-65&&b>-65&&a>noise+6&&b>noise+6)break;
                await new Promise(resolve=>setTimeout(resolve,200));
              }
              return {a,b,noise};
            }));
            if(!(audioProgress.a>-65&&audioProgress.b>-65&&audioProgress.a>audioProgress.noise+6&&audioProgress.b>audioProgress.noise+6))throw new Error(`two audio tones stalled during hold: ${JSON.stringify(audioProgress)}`);
            holdAudioSamples++;
          }
        }
      }
      if(process.env.WBB_REQUIRE_CONTINUOUS_PLAYBACK==='1')holdSamples=await viewer.evaluate(()=>{
        const v=window.__wbbViewer;clearInterval(v.holdTimer);v.holdTimer=null;return v.holdSamples;
      });
      if(process.env.WBB_REQUIRE_CONTINUOUS_PLAYBACK==='1' && (holdProgressFrames<20 || holdProgressSeconds<2 || holdAudioSamples<Math.ceil(holdSeconds/5)))throw new Error(`viewer did not progress during hold: frames=${holdProgressFrames} seconds=${holdProgressSeconds} audioSamples=${holdAudioSamples}`);
      const resumed=await finite('post-hold session state',5000,()=>publisher.evaluate(async()=>{const r=await fetch(`/api/v1/broadcasts/${window.__wbb.bid}/sessions/current`,{signal:AbortSignal.timeout(3000)});return (await r.json()).session?.state}));
      if(process.env.WBB_EXPECT_CONTROL_OUTAGE==='1') {
        stoppingEvent=await dbHasStoppingEvent(setup.sid);
        if(!(sawStopping||stoppingEvent) || resumed!=='ended')throw new Error(`control outage did not recover through STOPPING: saw=${sawStopping} durable=${stoppingEvent} final=${resumed}`);
      } else if(process.env.WBB_EXPECT_MTX_RESTART==='1') {
        if(!['live','stopping','ended'].includes(resumed))throw new Error(`session after MediaMTX restart=${resumed}`);
      } else if(resumed!=='live')throw new Error(`session after hold=${resumed}`);
      if(process.env.WBB_EXPECT_WORKER_RESTART==='1' || process.env.WBB_EXPECT_API_RESTART==='1') {
        const recovered=await dbRun(setup.sid);
        if(recovered.state!=='running'||!(await ffmpegExists(recovered.pid)))throw new Error(`media run missing after restart: ${JSON.stringify(recovered)}`);
        if(process.env.WBB_EXPECT_WORKER_RESTART==='1' && recovered.fence<=liveRun.fence)throw new Error(`worker did not create a new fenced run: ${JSON.stringify(recovered)}`);
        if(process.env.WBB_EXPECT_API_RESTART==='1' && (recovered.fence!==liveRun.fence||recovered.pid!==liveRun.pid||recovered.leaseMarginMs<=0))throw new Error(`API restart disrupted worker: ${JSON.stringify(recovered)}`);
        const fresh=await finite('post-restart viewer page',10000,()=>viewerBrowser.newPage());
        try {
          await finite('post-restart viewer navigation',10000,()=>fresh.goto(ORIGIN+'/'));
          await finite('post-restart hls.js load',10000,()=>fresh.addScriptTag({path:path.join(here,'../media-single-rendition/web/vendor/hls.min.js')}));
          await finite('post-restart playback setup',10000,()=>fresh.evaluate(async bid=>{
            const r=await fetch(`/api/v1/broadcasts/${bid}/playback`,{signal:AbortSignal.timeout(5000)});
            if(r.status!==200)throw new Error(`post-restart playback ${r.status}`);
            const info=await r.json();const v=document.createElement('video');v.muted=true;v.autoplay=true;document.body.append(v);
            const h=new Hls({enableWorker:false,lowLatencyMode:true});h.attachMedia(v);h.loadSource(info.masterUrl);window.__postRestart={v,h};
          },setup.bid));
          for(let i=0;i<25;i++) {
            postRestartFrames=await finite('post-restart frames',3000,()=>fresh.evaluate(async()=>{await window.__postRestart.v.play().catch(()=>{});return window.__postRestart.v.getVideoPlaybackQuality()?.totalVideoFrames||0}));
            if(postRestartFrames>3)break;
            await new Promise(resolve=>setTimeout(resolve,1000));
          }
          if(postRestartFrames<=3)throw new Error(`post-restart viewer decoded ${postRestartFrames} frames`);
        } finally {
          await finite('post-restart viewer cleanup',5000,()=>fresh.close()).catch(()=>{});
        }
      }
      console.error(process.env.WBB_EXPECT_CONTROL_OUTAGE==='1'?'browser phase: control outage reached ENDED':'browser phase: still live after hold');
    }
    if(process.env.WBB_ISOLATED_INJECT_CLEANUP_ERROR==='1') {
      restorePath=await dbResourcePath(setup.sid);
      if(!restorePath.startsWith(`/wbb/${setup.sid}/1/source/whip/`))throw new Error('missing known WHIP resource');
      await setResourcePath(setup.sid,'/invalid');
      console.error('browser phase: injected one-session WHIP cleanup error');
    }

    const naturalExit=process.env.WBB_INJECT_FFMPEG_EXIT==='1';
    if(naturalExit) {
      const current=await dbRun(setup.sid);
      if(current.state!=='running'||current.pid!==liveRun.pid||current.fence!==liveRun.fence)throw new Error('FFmpeg run identity changed before natural exit injection');
      if(process.env.WBB_INJECT_TRANSITION_FAILURE==='1') {
        await dedicatedSQL(`CREATE FUNCTION wbb_test_reject_stopping() RETURNS trigger LANGUAGE plpgsql AS $guard$ BEGIN RAISE EXCEPTION 'isolated transition fault'; END $guard$;
          CREATE TRIGGER wbb_test_reject_stopping BEFORE UPDATE OF state ON broadcast_sessions FOR EACH ROW
          WHEN (NEW.id='${setup.sid}'::uuid AND NEW.state='stopping') EXECUTE FUNCTION wbb_test_reject_stopping()`);
        transitionGuardInstalled=true;
      }
      if(process.env.WBB_INJECT_EXIT_RECORD_FAILURE==='1') {
        await dedicatedSQL(`CREATE FUNCTION wbb_test_reject_run_exit() RETURNS trigger LANGUAGE plpgsql AS $guard$ BEGIN RAISE EXCEPTION 'isolated exit record fault'; END $guard$;
          CREATE TRIGGER wbb_test_reject_run_exit BEFORE UPDATE OF state ON media_worker_runs FOR EACH ROW
          WHEN (NEW.broadcast_session_id='${setup.sid}'::uuid AND NEW.state='exited') EXECUTE FUNCTION wbb_test_reject_run_exit()`);
        runExitGuardInstalled=true;
      }
      await execFileAsync('docker',['exec','wbb-media-completion-integration-worker-1','sh','-c',`kill -TERM ${current.pid}`],{timeout:5000});
      if(runExitGuardInstalled) {
        let held;
        for(let i=0;i<16;i++) {
          held={...await dbSessionOperation(setup.sid),run:(await dbRun(setup.sid)).state};
          if(held.session==='stopping'&&held.run==='running')break;
          await new Promise(resolve=>setTimeout(resolve,500));
        }
        if(held.session!=='stopping'||held.run!=='running')throw new Error(`failed exit recording was not retained: ${JSON.stringify(held)}`);
        await removeRunExitGuard();runExitGuardInstalled=false;
      }
      if(transitionGuardInstalled) {
        let recorded=false;
        for(let i=0;i<16;i++) {
          const run=await dbRun(setup.sid);
          if(run.state==='exited') {recorded=true;break}
          await new Promise(resolve=>setTimeout(resolve,500));
        }
        const held=await dbSessionOperation(setup.sid);
        if(!recorded||held.session!=='live')throw new Error(`exit was not durable before rejected transition: ${JSON.stringify(held)}`);
        await removeTransitionGuard();transitionGuardInstalled=false;
      }
      let stateAfterExit='';
      for(let i=0;i<12;i++) {
        stateAfterExit=await finite('natural exit state',5000,()=>publisher.evaluate(async()=>{const r=await fetch(`/api/v1/broadcasts/${window.__wbb.bid}/sessions/current`,{signal:AbortSignal.timeout(3000)});return (await r.json()).session?.state}));
        if(stateAfterExit==='stopping'||stateAfterExit==='ended')break;
        await new Promise(resolve=>setTimeout(resolve,500));
      }
      if(stateAfterExit!=='stopping'&&stateAfterExit!=='ended')throw new Error(`FFmpeg exit did not stop session: ${stateAfterExit}`);
    }
    const managementOutage=process.env.WBB_EXPECT_MANAGEMENT_OUTAGE==='1';
    const longManagementOutage=process.env.WBB_EXPECT_LONG_MANAGEMENT_OUTAGE==='1';
    const mediamtxDeath=process.env.WBB_EXPECT_MTX_DEATH==='1';
    let longOutageEvidence=null;
    if(managementOutage||longManagementOutage||mediamtxDeath) {
      if(mediamtxDeath)mediamtxStopped=true;else managementPaused=true;
      await execFileAsync('docker',[mediamtxDeath?'stop':'pause','wbb-media-completion-integration-mediamtx-1'],{timeout:20000});
    }
    if(longManagementOutage||mediamtxDeath) {
      await new Promise(resolve=>setTimeout(resolve,60000));
      const held=await dbSessionOperation(setup.sid);
      const run=await dbRun(setup.sid);
      const ffmpegAlive=await ffmpegExists(liveRun.pid);
      if(held.session!=='stopping'||held.operation==='closed'||run.state!=='exited'||ffmpegAlive)
        throw new Error(`long management outage did not stop safely: ${JSON.stringify({held,runState:run.state,ffmpegAlive})}`);
      longOutageEvidence={kind:mediamtxDeath?'process_stopped':'management_paused',held:held.session,operation:held.operation,run:run.state,ffmpegAlive};
      await execFileAsync('docker',[mediamtxDeath?'start':'unpause','wbb-media-completion-integration-mediamtx-1'],{timeout:10000});
      if(mediamtxDeath)mediamtxStopped=false;else managementPaused=false;
    }
    const redisOutage=process.env.WBB_EXPECT_REDIS_OUTAGE==='1';
    const controlOutage=process.env.WBB_EXPECT_CONTROL_OUTAGE==='1';
    const stopStartedAt=Date.now();
    const stopped=(controlOutage||naturalExit)?null:await finite('session stop',8000,()=>publisher.evaluate(async()=>{
      const {access,bid,sid}=window.__wbb;
      const response=await fetch(`/api/v1/broadcasts/${bid}/sessions/${sid}/stop`,{method:'POST',signal:AbortSignal.timeout(5000),headers:{Authorization:`Bearer ${access}`}});
      return response.status;
    }));
    if(!controlOutage && !naturalExit && !(process.env.WBB_EXPECT_MTX_RESTART==='1' && stopped===200) && !((longManagementOutage||mediamtxDeath)&&[200,202].includes(stopped)) && stopped!==(redisOutage?503:202))throw new Error(`stop ${stopped}`);
    console.error(`browser phase: stop request ${stopped}`);
    if(managementOutage) {
      await new Promise(resolve=>setTimeout(resolve,6500));
      const held=await dbSessionOperation(setup.sid);
      if(held.session!=='stopping'||held.operation==='closed')throw new Error(`management outage did not fail closed: ${JSON.stringify(held)}`);
      await execFileAsync('docker',['unpause','wbb-media-completion-integration-mediamtx-1'],{timeout:5000});
      managementPaused=false;
    }
    if(redisOutage || controlOutage || naturalExit) {
      await finite('publisher ingress disconnect',5000,()=>publisher.evaluate(()=>{const m=window.__wbb.media;if(!m)return;window.__wbb.media=null;clearInterval(m.timer);m.tones.forEach(x=>x.stop());m.video.stop();m.sound.stop();m.pc.close();m.audio.close()}));
    }
    const viewerGate=()=>finite('stopped viewer gate',8000,()=>viewer.evaluate(async ({bid,master})=>{
      const playback=await fetch(`/api/v1/broadcasts/${bid}/playback`,{signal:AbortSignal.timeout(5000)});
      const hls=await fetch(master+'?_HLS_msn=999',{signal:AbortSignal.timeout(5000)});
      return {playback:playback.status,hls:hls.status};
    },{bid:setup.bid,master:watching.masterUrl}));
    let after;
    if(!redisOutage && !controlOutage) {
      after=await viewerGate();
      if(after.playback!==409 || after.hls!==403)throw new Error(`stopped access ${JSON.stringify(after)}`);
    }
    if(restorePath) {
      await new Promise(resolve=>setTimeout(resolve,5000));
      const held=await finite('isolated STOPPING state',5000,()=>publisher.evaluate(async()=>{const r=await fetch(`/api/v1/broadcasts/${window.__wbb.bid}/sessions/current`,{signal:AbortSignal.timeout(3000)});return (await r.json()).session?.state}));
      if(held!=='stopping')throw new Error(`cleanup error did not retain STOPPING: ${held}`);
      await setResourcePath(setup.sid,restorePath);
      restorePath=null;
      console.error('browser phase: isolated cleanup error recovered');
    }
    let finalState='';
    for(let i=0;i<(naturalExit?22:55);i++) {
      finalState=await finite('ended state',5000,()=>publisher.evaluate(async()=>{const r=await fetch(`/api/v1/broadcasts/${window.__wbb.bid}/sessions/current`,{signal:AbortSignal.timeout(3000)});return (await r.json()).session?.state}));
      if(finalState==='ended')break;
      await new Promise(r=>setTimeout(r,1000));
    }
    if(finalState!=='ended')throw new Error(`session did not end: ${finalState}`);
    if(redisOutage || controlOutage || naturalExit) {
      after=await viewerGate();
      if(after.playback!==409 || after.hls!==403)throw new Error(`ended access ${JSON.stringify(after)}`);
    }
    const endedRun=await dbRun(setup.sid);
    const completionProved=await dbCompletion(setup.sid);
    if(!completionProved)throw new Error('ENDED lacks pinned completed operation evidence');
    const prefix=`wbb/${setup.sid}/1/`;
    const [paths,ingress,rtsp]=await Promise.all([mediaList('paths'),mediaList('webrtcsessions'),mediaList('rtspsessions')]);
    if(endedRun.state!=='exited' || await ffmpegExists(liveRun.pid) || paths.some(x=>x.name?.startsWith(prefix)&&x.ready) || ingress.some(x=>x.path?.startsWith(prefix)) || rtsp.some(x=>x.path?.startsWith(prefix))) {
      throw new Error(`media remained after ENDED: run=${JSON.stringify(endedRun)} paths=${paths.length} ingress=${ingress.length} rtsp=${rtsp.length}`);
    }
    summary={prepared:setup.status,whip:published.status,live:state,viewerFrames:frames,audioTonesDb:tones,minLeaseMarginMs,holdProgressFrames,holdProgressSeconds,holdAudioSamples,holdSamples,maxStallMs,postRestartFrames,sawStopping,stoppingEvent,stop:stopped,naturalExit,mtxRestart:process.env.WBB_EXPECT_MTX_RESTART==='1',longOutageEvidence,after,finalState,completionProved,stopToEndedMs:stopped===null?null:Date.now()-stopStartedAt,ffmpegExited:true,ingressAbsent:true,sessionId:setup.sid};
  } catch(error) {
    failure=error;
    if(process.env.WBB_REQUIRE_CONTINUOUS_PLAYBACK==='1'&&viewer) {
      const diagnostics=await finite('hold diagnostics',2000,()=>viewer.evaluate(()=>{
        const v=window.__wbbViewer;return v?{samples:v.holdSamples?.slice(-32),audioState:v.audio.state,videoTime:v.video.currentTime}:null;
      })).catch(()=>null);
      if(diagnostics)console.error(`hold diagnostic ${JSON.stringify(diagnostics)}`);
    }
  } finally {
    if(runExitGuardInstalled)await removeRunExitGuard().catch(error=>cleanupErrors.push(new Error(`exit guard cleanup: ${error.message}`)));
    if(transitionGuardInstalled)await removeTransitionGuard().catch(error=>cleanupErrors.push(new Error(`transition guard cleanup: ${error.message}`)));
    if(managementPaused)await execFileAsync('docker',['unpause','wbb-media-completion-integration-mediamtx-1'],{timeout:5000}).catch(error=>cleanupErrors.push(new Error(`management unpause: ${error.message}`)));
    if(mediamtxStopped)await execFileAsync('docker',['start','wbb-media-completion-integration-mediamtx-1'],{timeout:10000}).catch(error=>cleanupErrors.push(new Error(`MediaMTX restart: ${error.message}`)));
    if(restorePath && setup)await finite('restore isolated resource',6000,()=>setResourcePath(setup.sid,restorePath)).catch(error=>cleanupErrors.push(new Error(`restore resource: ${error.message}`)));
    if(setup && publisher)await finite('failure stop cleanup',7000,()=>publisher.evaluate(async()=>{const s=window.__wbb;if(!s)return;const r=await fetch(`/api/v1/broadcasts/${s.bid}/sessions/${s.sid}/stop`,{method:'POST',signal:AbortSignal.timeout(5000),headers:{Authorization:`Bearer ${s.access}`}});if(r.status!==200&&r.status!==202&&r.status!==409)throw new Error(`stop cleanup ${r.status}`)})).catch(error=>cleanupErrors.push(new Error(`stop cleanup: ${error.message}`)));
    if(publisher)await finite('publisher media cleanup',5000,()=>publisher.evaluate(()=>{const m=window.__wbb?.media;if(!m)return;clearInterval(m.timer);m.tones.forEach(x=>x.stop());m.video.stop();m.sound.stop();m.pc.close();m.audio.close()})).catch(error=>cleanupErrors.push(new Error(`publisher cleanup: ${error.message}`)));
    if(viewer)await finite('viewer media cleanup',5000,()=>viewer.evaluate(()=>{const v=window.__wbbViewer;if(v){clearInterval(v.holdTimer);v.hls.destroy();v.audio.close();v.video.remove()}})).catch(error=>cleanupErrors.push(new Error(`viewer cleanup: ${error.message}`)));
    if(viewer)await finite('viewer page close',10000,()=>viewer.close({runBeforeUnload:false})).catch(error=>cleanupErrors.push(new Error(`viewer page close: ${error.message}`)));
    if(publisher)await finite('publisher page close',10000,()=>publisher.close({runBeforeUnload:false})).catch(error=>cleanupErrors.push(new Error(`publisher page close: ${error.message}`)));
    await closeOwnedBrowser(viewerServer,'viewer',cleanupErrors);
    await closeOwnedBrowser(publisherServer,'publisher',cleanupErrors);
  }
  if(failure || cleanupErrors.length)throw new AggregateError([...(failure?[failure]:[]),...cleanupErrors], 'isolated browser flow or cleanup failed');
  console.log(JSON.stringify(summary));
}

export { mediaList, dbRun, dbCompletion, ffmpegExists, launchOwnedPair };

if(process.argv[1] && path.resolve(process.argv[1])===fileURLToPath(import.meta.url)) {
  main().then(()=>process.exit(0),error=>{console.error(error.errors?.map(e=>e.message).join('; ')||error.message);process.exit(1)});
}
