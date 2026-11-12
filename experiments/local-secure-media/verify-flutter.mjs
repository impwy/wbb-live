// 04-B2: actual Flutter widgets in two independent Chrome processes.
// Capture is synthetic only through addInitScript; the product has no test UI.
import { chromium } from '../media-single-rendition/node_modules/playwright/index.mjs';
import tls from 'node:tls';
import crypto, { X509Certificate } from 'node:crypto';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { mediaList, dbRun, ffmpegExists } from './verify-browser.mjs';

const origin = 'https://localhost:19443';
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
const execFileAsync=promisify(execFile);
async function dbSessionSource(sid) {
  if(!/^[0-9a-f-]{36}$/.test(sid))throw new Error('invalid session id');
  const {stdout}=await execFileAsync('docker',['exec','wbb-local-secure-media-postgres-1',
    'psql','-U','wbb','-d','wbb','-At','-c',`SELECT source FROM broadcast_sessions WHERE id='${sid}'`],{timeout:5000});
  return stdout.trim();
}
const finite = async (label, ms, work) => {
  let timer;
  try { return await Promise.race([work(), new Promise((_, reject) => {
    timer = setTimeout(() => reject(new Error(`${label} exceeded ${ms}ms`)), ms);
  })]); } finally { clearTimeout(timer); }
};

async function pin() {
  return new Promise((resolve, reject) => {
    const socket = tls.connect({host:'localhost',port:19443,servername:'localhost',rejectUnauthorized:false}, () => {
      try {
        const cert = new X509Certificate(socket.getPeerCertificate(true).raw);
        resolve(crypto.createHash('sha256').update(cert.publicKey.export({type:'spki',format:'der'})).digest('base64'));
      } catch (e) { reject(e); } finally { socket.end(); }
    });
    socket.setTimeout(3000, () => { socket.destroy(); reject(new Error('TLS pin timeout')); });
    socket.on('error', reject);
  });
}

function syntheticCapture({camera}) {
  const NativeAC = window.AudioContext;
  const NativePC = window.RTCPeerConnection;
  const actualDisplay = navigator.mediaDevices.getDisplayMedia?.bind(navigator.mediaDevices);
  const actualMic = navigator.mediaDevices.getUserMedia?.bind(navigator.mediaDevices);
  const spy = window.__flutterMediaTest = { displayCalls: 0, micCalls: 0, cameraCalls: 0,
    displayAt: 0, cameraAt: 0, prepareAt: 0, preparedSource: '',
    streams: [], sourceContexts: [], productContexts: [], pcs: [], timers: [] };
  const realFetch = window.fetch.bind(window);
  let oldGrant=null;
  spy.probeOldGrant=async()=>{
    if(!oldGrant)return null;
    const response=await realFetch(oldGrant.whipUrl,{method:'POST',credentials:'include',
      signal:AbortSignal.timeout(5000),headers:{'Content-Type':'application/sdp',Authorization:`Bearer ${oldGrant.token}`},
      body:'v=0'});
    oldGrant=null;
    return response.status;
  };
  window.fetch = (...args) => {
    const url = String(args[0]?.url || args[0]);
    if (url.endsWith('/sessions') && (args[1]?.method || args[0]?.method) === 'POST') {
      spy.prepareAt = performance.now();
      // Inspect only an already-materialized body. A Request/stream must not
      // be consumed or delay the product fetch; the DB checks source later.
      try {
        const body=args[1]?.body;
        const json=typeof body==='string'?body:
          ArrayBuffer.isView(body)?new TextDecoder().decode(body):
          body instanceof ArrayBuffer?new TextDecoder().decode(body):'';
        if(json)spy.preparedSource=JSON.parse(json).source||'';
      } catch(_) {}
    }
    const response=realFetch(...args);
    if(url.endsWith('/sessions') && (args[1]?.method || args[0]?.method)==='POST') {
      response.then(r=>r.clone().json().then(value=>{oldGrant=value.publishGrant||null;}).catch(()=>{})).catch(()=>{});
    }
    return response;
  };
  window.AudioContext = class extends NativeAC {
    constructor(...args) { super(...args); spy.productContexts.push(this); }
  };
  window.RTCPeerConnection = class extends NativePC {
    constructor(...args) { super(...args); spy.pcs.push(this); }
  };
  const tone = frequency => {
    const ac = new NativeAC(); spy.sourceContexts.push(ac);
    const dest = ac.createMediaStreamDestination();
    const oscillator = ac.createOscillator(); oscillator.frequency.value = frequency;
    const gain = ac.createGain(); gain.gain.value = 0.12;
    oscillator.connect(gain).connect(dest); oscillator.start();
    ac.resume();
    return dest.stream.getAudioTracks()[0];
  };
  const videoTrack=()=>{
    const canvas = document.createElement('canvas'); canvas.width=640;canvas.height=360;
    const ctx=canvas.getContext('2d'); let n=0;
    const paint=()=>{ctx.fillStyle=n++%2?'#1754b5':'#e9a22f';ctx.fillRect(0,0,640,360);};
    paint();spy.timers.push(setInterval(paint,33));
    return canvas.captureStream(30).getVideoTracks()[0];
  };
  navigator.mediaDevices.getDisplayMedia = () => {
    spy.displayCalls++; spy.displayAt = performance.now();
    const result = new MediaStream([videoTrack(), tone(440)]);
    spy.streams.push(result);
    return Promise.resolve(result);
  };
  navigator.mediaDevices.getUserMedia = constraints => {
    if (constraints.video) {
      spy.cameraCalls++;spy.cameraAt=performance.now();
      const result=new MediaStream([videoTrack(),tone(660)]);
      spy.streams.push(result);return Promise.resolve(result);
    }
    spy.micCalls++;
    const result = new MediaStream([tone(660)]);spy.streams.push(result);
    return Promise.resolve(result);
  };
  spy.cleanup = async () => {
    spy.timers.forEach(clearInterval);
    spy.streams.forEach(s => s.getTracks().forEach(t => t.stop()));
    await Promise.allSettled(spy.sourceContexts.map(ac => ac.close()));
    navigator.mediaDevices.getDisplayMedia = actualDisplay;
    navigator.mediaDevices.getUserMedia = actualMic;
  };
}

async function enableSemantics(page) {
  await page.locator('flt-semantics-placeholder').waitFor({timeout:10000});
  await page.locator('flt-semantics-placeholder').evaluate(e => e.click());
}

async function main() {
  const camera=process.argv.includes('--camera');
  const hideCamera=process.argv.includes('--camera-hide');
  if(hideCamera&&!camera)throw new Error('camera-hide requires camera');
  const leaf = await pin();
  const args = [`--ignore-certificate-errors-spki-list=${leaf}`, '--autoplay-policy=no-user-gesture-required'];
  const publisherBrowser = await chromium.launch({channel:'chrome',headless:true,args,timeout:10000});
  const viewerBrowser = await chromium.launch({channel:'chrome',headless:true,args,timeout:10000});
  const email = `f${crypto.randomBytes(5).toString('hex')}@e.test`;
  const password = `WbbTest-${crypto.randomBytes(8).toString('hex')}`;
  const title = `Flutter ${crypto.randomUUID().slice(0,8)}`;
  let owner, viewer, bid, sid, passed=false, liveRun;
  try {
    owner = await publisherBrowser.newPage(); viewer = await viewerBrowser.newPage();
    if(camera) {
      await owner.setViewportSize({width:390,height:844});
      await viewer.setViewportSize({width:390,height:844});
    }
    owner.setDefaultTimeout(10000); viewer.setDefaultTimeout(10000);
    await owner.addInitScript(syntheticCapture,{camera});
    await finite('owner navigation',15000,()=>owner.goto(origin));
    await enableSemantics(owner);
    await owner.getByRole('button',{name:'로그인',exact:true}).click();
    await owner.getByRole('button',{name:'계정이 없나요? 가입하기'}).click();
    const fields = [['이메일',email],[/비밀번호/,password],[/표시 이름/,'Flutter Publisher']];
    const input = name => owner.getByRole('textbox',{name});
    for (let round=0; round<2; round++) {
      for (const [name,value] of fields) {
        const field=input(name);
        if (await field.inputValue() === value) continue;
        await field.click();
        const focused=await field.evaluate(e=>document.activeElement===e);
        if (!focused) throw new Error('Flutter editor did not focus the selected field');
        // Flutter Web installs the editing client after DOM focus. A focused
        // input alone can still be reset by that deferred installation.
        await owner.waitForTimeout(300);
        await field.fill(value);
        await owner.waitForTimeout(150);
      }
      const matches=await Promise.all(fields.map(async ([name,value])=>await input(name).inputValue()===value));
      if (matches.every(Boolean)) break;
      if (round===1) throw new Error(`Flutter form input mismatch ${matches.join(',')}`);
    }
    await owner.getByRole('button',{name:'가입하고 로그인'}).click();
    // The submitted widget values are checked by reaching the authenticated
    // screen; no direct registration endpoint is used here.
    await owner.getByRole('button',{name:'내 방송'}).waitFor();
    await owner.getByRole('button',{name:'내 방송'}).click();
    await owner.getByRole('button',{name:'방송 등록'}).click();
    const titleField=owner.getByRole('textbox',{name:/제목/});
    await titleField.click();
    if (!(await titleField.evaluate(e=>document.activeElement===e))) throw new Error('broadcast title not focused');
    await owner.waitForTimeout(300);
    await titleField.fill(title);
    await owner.waitForTimeout(150);
    if (await titleField.inputValue() !== title) throw new Error('broadcast title input mismatch');
    await owner.getByRole('textbox',{name:/설명/}).click();
    if (await titleField.inputValue() !== title) throw new Error('broadcast title changed on blur');
    await owner.getByRole('button',{name:'등록',exact:true}).click();
    await owner.getByRole('button',{name:new RegExp(title)}).click();
    bid = await owner.evaluate(async title => {
      const r=await fetch('/api/v1/broadcasts?limit=20',{signal:AbortSignal.timeout(5000)});
      return (await r.json()).items.find(b=>b.title===title)?.id;
    }, title);
    if (!bid) throw new Error('created broadcast not found by public list');
    await owner.getByRole('button',{name:camera?'카메라·마이크 시작':'화면 공유 시작'}).click();
    try {
      await owner.getByText('송출: 송출 중').waitFor({timeout:camera?10000:25000});
    } catch(error) {
      console.error('publish state',await owner.getByText(/송출:|실패|권한/).allTextContents().catch(()=>[]));
      console.error('capture calls',await owner.evaluate(()=>({display:window.__flutterMediaTest?.displayCalls,
        mic:window.__flutterMediaTest?.micCalls,camera:window.__flutterMediaTest?.cameraCalls,
        preparedSource:window.__flutterMediaTest?.preparedSource})).catch(()=>({})));
      if(camera) console.error('direct camera capture',await owner.evaluate(async()=>{
        const key=window.WbbMedia.newPublisher('camera',()=>{});
        try{const value=JSON.parse(await window.WbbMedia.captureOffer(key));return {offerLength:value.sdp.length};}
        catch(e){return {name:e?.name,message:e?.message};}
        finally{await window.WbbMedia.stopPublisher(key);}
      }).catch(e=>({message:e.message})));
      throw error;
    }
    sid = await owner.evaluate(async bid => {
      const r=await fetch(`/api/v1/broadcasts/${bid}/sessions/current`,{signal:AbortSignal.timeout(5000)});
      return (await r.json()).session?.id;
    },bid);
    const videoNegotiation = await finite('H264 outbound RTP',10000,()=>owner.evaluate(async()=>{
      const pc=window.__flutterMediaTest.pcs[0];
      const section=sdp=>sdp?.split(/\r?\nm=video /)[1]?.split(/\r?\nm=/)[0]||'';
      const describe=sdp=>{const part=section(sdp);return {m:part.split(/\r?\n/,1)[0],direction:part.match(/a=(sendrecv|sendonly|recvonly|inactive)/)?.[1]||'',h264:part.includes('H264/90000')}};
      let video={framesEncoded:0,packetsSent:0,bytesSent:0,codec:''};
      let audio={packetsSent:0,codec:''};
      for(let i=0;i<32;i++) {
        const stats=await pc.getStats();
        stats.forEach(s=>{
          if(s.type==='outbound-rtp'&&s.kind==='video')video={framesEncoded:s.framesEncoded||0,packetsSent:s.packetsSent||0,bytesSent:s.bytesSent||0,codec:stats.get(s.codecId)?.mimeType||''};
          if(s.type==='outbound-rtp'&&s.kind==='audio')audio={packetsSent:s.packetsSent||0,codec:stats.get(s.codecId)?.mimeType||''};
        });
        if(video.framesEncoded>0 && video.packetsSent>0 && audio.packetsSent>0)break;
        await new Promise(r=>setTimeout(r,250));
      }
      return {offer:describe(pc.localDescription?.sdp),answer:describe(pc.remoteDescription?.sdp),
        tracks:pc.getSenders().map(s=>({kind:s.track?.kind,state:s.track?.readyState})),video,audio};
    }));
    if(!videoNegotiation.offer.h264 || !videoNegotiation.answer.h264 ||
      videoNegotiation.video.codec!=='video/H264' || videoNegotiation.video.framesEncoded<1 ||
      videoNegotiation.video.packetsSent<1 || videoNegotiation.audio.codec!=='audio/opus' ||
      videoNegotiation.audio.packetsSent<1) throw new Error(`outbound media missing ${JSON.stringify(videoNegotiation)}`);
    const capture = await owner.evaluate(() => {
      const s=window.__flutterMediaTest;
      return {display:s.displayCalls,mic:s.micCalls,camera:s.cameraCalls,
        captureBeforePrepare:(s.cameraAt||s.displayAt)>0&&s.prepareAt>(s.cameraAt||s.displayAt),
        preparedSource:s.preparedSource,
        pc:s.pcs.length,ac:s.productContexts.length};
    });
    if ((camera?(capture.camera!==1||capture.display!==0||capture.mic!==0||capture.ac!==0):
      (capture.display!==1||capture.mic!==1||capture.camera!==0||capture.ac<1)) ||
      !capture.captureBeforePrepare || capture.pc!==1) {
      throw new Error(`capture order/resources ${JSON.stringify(capture)}`);
    }
    await owner.getByText('상태: 방송 중').waitFor({timeout:35000});
    liveRun=await dbRun(sid);
    if (liveRun.state!=='running' || !(await ffmpegExists(liveRun.pid))) throw new Error('LIVE FFmpeg missing');
    if (!sid) throw new Error('LIVE session id missing');
    const storedSource=await dbSessionSource(sid);
    if(storedSource!==(camera?'camera':'screen'))throw new Error(`stored source ${storedSource}`);
    const livePaths=await mediaList('paths');
    const sourcePath=livePaths.find(x=>x.name===`wbb/${sid}/1/source`);
    const sourceTracks=sourcePath?.tracks||[];
    if(!sourcePath?.ready || !sourceTracks.includes('H264') || !sourceTracks.includes('Opus')) {
      throw new Error(`MediaMTX source lacks H264/Opus: ${JSON.stringify(sourceTracks)}`);
    }
    await viewer.goto(origin); await enableSemantics(viewer);
    await viewer.getByRole('button',{name:new RegExp(title)}).click();
    await viewer.getByRole('button',{name:'재생',exact:true}).waitFor({state:'visible'});
    await finite('viewer ready',25000,async()=>{
      while (!(await viewer.getByRole('button',{name:'재생',exact:true}).isEnabled())) await sleep(500);
    });
    await viewer.getByRole('button',{name:'소리 켜기'}).click();
    await finite('decoded frames',25000,async()=>{
      while (await viewer.locator('video').last().evaluate(v=>v.getVideoPlaybackQuality()?.totalVideoFrames||0) < 5) await sleep(500);
    });
    const frames = await viewer.locator('video').last().evaluate(v=>v.getVideoPlaybackQuality()?.totalVideoFrames||0);
    const tones = await viewer.locator('video').last().evaluate(async video => {
      const audio=new AudioContext();const source=audio.createMediaElementSource(video);
      const analyser=audio.createAnalyser();analyser.fftSize=4096;source.connect(analyser);analyser.connect(audio.destination);
      await audio.resume();
      const peak=(bins,lo,hi)=>{let v=-Infinity;for(let i=Math.floor(lo*analyser.fftSize/audio.sampleRate);i<=Math.ceil(hi*analyser.fftSize/audio.sampleRate);i++)v=Math.max(v,bins[i]);return v};
      let a=-Infinity,b=-Infinity,noise=-Infinity;
      for(let n=0;n<14;n++){const bins=new Float32Array(analyser.frequencyBinCount);analyser.getFloatFrequencyData(bins);
        a=Math.max(a,peak(bins,420,460));b=Math.max(b,peak(bins,640,680));noise=Math.max(noise,peak(bins,300,350),peak(bins,750,800));
        if(a>-65&&b>-65&&a>noise+6&&b>noise+6)break;await new Promise(r=>setTimeout(r,400));}
      await audio.close();return {a,b,noise};
    });
    if (camera ? !(tones.b>-65&&tones.b>tones.noise+6&&tones.a<tones.b-10) :
      !(tones.a>-65&&tones.b>-65&&tones.a>tones.noise+6&&tones.b>tones.noise+6)) {
      throw new Error(`expected audio missing: ${JSON.stringify(tones)}`);
    }
    await viewer.locator('video').last().evaluate(v=>v.pause());
    await viewer.getByRole('button',{name:'라이브로 이동'}).click();
    const pausedSeekTime=await viewer.locator('video').last().evaluate(v=>v.currentTime);
    // A periodic detail refresh occurs during this hold. Neither it nor the
    // explicit live seek is allowed to resume a user-paused player.
    await sleep(5500);
    const afterPaused=await viewer.locator('video').last().evaluate(v=>({paused:v.paused,time:v.currentTime}));
    if(!afterPaused.paused || afterPaused.time>pausedSeekTime+0.1) throw new Error(`user pause lost ${JSON.stringify(afterPaused)}`);
    await viewer.getByRole('button',{name:'소리 켜기'}).click();
    await finite('resume after pause',5000,async()=>{
      while(await viewer.locator('video').last().evaluate(v=>v.paused)) await sleep(100);
    });
    const masterUrl=await viewer.evaluate(async bid=>{
      const r=await fetch(`/api/v1/broadcasts/${bid}/playback`,{signal:AbortSignal.timeout(5000)});
      if(!r.ok)throw new Error(`playback access ${r.status}`);
      return (await r.json()).masterUrl;
    },bid);
    let hiddenRelease=null;
    if(hideCamera) {
      hiddenRelease=await owner.evaluate(()=>{
        Object.defineProperty(document,'visibilityState',{configurable:true,get:()=> 'hidden'});
        document.dispatchEvent(new Event('visibilitychange'));
        const s=window.__flutterMediaTest;
        return {tracks:s.streams.flatMap(x=>x.getTracks()).every(t=>t.readyState==='ended'),
          pc:s.pcs.every(x=>x.connectionState==='closed')};
      });
      if(!hiddenRelease.tracks||!hiddenRelease.pc)throw new Error(`hidden did not release local media ${JSON.stringify(hiddenRelease)}`);
    } else {
      await owner.getByRole('button',{name:'방송 중지'}).click();
    }
    await finite('stop accepted',8000,async()=>{
      while(true) {
        const state=await owner.evaluate(async bid=>{
          const r=await fetch(`/api/v1/broadcasts/${bid}/sessions/current`,{signal:AbortSignal.timeout(3000)});
          return (await r.json()).session?.state;
        },bid);
        if(state==='stopping'||state==='ended')break;
        await sleep(200);
      }
    });
    const afterStop=await viewer.evaluate(async ({bid,masterUrl})=>{
      const [access,hls]=await Promise.all([
        fetch(`/api/v1/broadcasts/${bid}/playback`,{signal:AbortSignal.timeout(5000)}),
        fetch(`${masterUrl}?_HLS_msn=999`,{signal:AbortSignal.timeout(5000)}),
      ]);
      return {access:access.status,hls:hls.status};
    },{bid,masterUrl});
    if(afterStop.access!==409 || afterStop.hls!==403) throw new Error(`stop gate ${JSON.stringify(afterStop)}`);
    const oldGrantStatus=await owner.evaluate(()=>window.__flutterMediaTest.probeOldGrant());
    if(oldGrantStatus!==401 && oldGrantStatus!==409)throw new Error(`old publish grant accepted or unobserved: ${oldGrantStatus}`);
    await finite('ended state',30000,async()=>{
      while (true) {
        const state=await owner.evaluate(async bid=>{
          const r=await fetch(`/api/v1/broadcasts/${bid}/sessions/current`,{signal:AbortSignal.timeout(5000)});
          return (await r.json()).session?.state;
        },bid);
        if (state==='ended') break;
        await sleep(500);
      }
    });
    if(hideCamera) {
      await owner.evaluate(()=>{
        delete document.visibilityState;
        document.dispatchEvent(new Event('visibilitychange'));
      });
    }
    await owner.getByText('상태: 종료',{exact:true}).waitFor({timeout:10000});
    if(hideCamera) {
      const noRestart=await owner.evaluate(()=>window.__flutterMediaTest.cameraCalls);
      if(noRestart!==1)throw new Error(`camera restarted after visibility return: ${noRestart}`);
    }
    const endedRun=await dbRun(sid);
    const prefix=`wbb/${sid}/1/`;
    const [paths,ingress,rtsp]=await Promise.all([mediaList('paths'),mediaList('webrtcsessions'),mediaList('rtspsessions')]);
    if(endedRun.state!=='exited'||await ffmpegExists(liveRun.pid)||
      paths.some(x=>x.name?.startsWith(prefix)&&x.ready)||
      ingress.some(x=>x.path?.startsWith(prefix))||rtsp.some(x=>x.path?.startsWith(prefix))) {
      throw new Error('media resources remained after ENDED');
    }
    const released = await owner.evaluate(() => {
      const s=window.__flutterMediaTest;
      return {tracks:s.streams.flatMap(x=>x.getTracks()).every(t=>t.readyState==='ended'),
        pc:s.pcs.every(x=>x.connectionState==='closed'),
        audio:s.productContexts.every(x=>x.state==='closed')};
    });
    if (!released.tracks || !released.pc || !released.audio) throw new Error(`publisher resource leak ${JSON.stringify(released)}`);
    await viewer.getByRole('button',{name:'상태 새로고침'}).click();
    await viewer.getByRole('button',{name:'재생',exact:true}).waitFor({state:'visible'});
    if (await viewer.getByRole('button',{name:'재생',exact:true}).isEnabled()) throw new Error('ended playback remains enabled');
    console.log(JSON.stringify({source:storedSource,signupLogin:true,uiPublish:true,captureBeforePrepare:true,
      outboundVideo:videoNegotiation.video,outboundAudio:videoNegotiation.audio,sourceTracks,
      anonymousFrames:frames,audioTonesDb:tones,pausedPreserved:afterPaused.paused,
      afterStop,oldGrantStatus,ended:true,released,hiddenRelease,autoRestart:hideCamera?false:null,sessionId:sid}));
    passed=true;
  } finally {
    if (!sid && bid && owner) sid=await finite('failure session lookup',5000,()=>owner.evaluate(async bid=>{
      try {const r=await fetch(`/api/v1/broadcasts/${bid}/sessions/current`,{signal:AbortSignal.timeout(3000)});
        return (await r.json()).session?.id||null;}catch{return null;}
    },bid)).catch(()=>null);
    if (!passed && bid && sid && owner) await finite('failure stop',6000,()=>owner.evaluate(async ({bid,sid,email,password})=>{
      try {const login=await fetch('/api/v1/auth/login',{method:'POST',headers:{'Content-Type':'application/json'},
        body:JSON.stringify({email,password}),signal:AbortSignal.timeout(3000)});
        if(!login.ok)return;const token=(await login.json()).accessToken;
        await fetch(`/api/v1/broadcasts/${bid}/sessions/${sid}/stop`,{method:'POST',
          headers:{Authorization:`Bearer ${token}`},signal:AbortSignal.timeout(3000)});
      } catch {}}, {bid,sid,email,password})).catch(()=>{});
    if (owner) await finite('synthetic source cleanup',4000,()=>owner.evaluate(()=>window.__flutterMediaTest?.cleanup())).catch(()=>{});
    await finite('viewer browser close',5000,()=>viewerBrowser.close()).catch(()=>{});
    await finite('publisher browser close',5000,()=>publisherBrowser.close()).catch(()=>{});
  }
}

main().then(()=>process.exit(0),error=>{console.error(error.message);process.exit(1)});
