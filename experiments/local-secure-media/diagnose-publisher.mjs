// Isolated diagnostic for the real product WbbMedia bridge. This bypasses only
// Flutter form entry; it is not evidence that the user flow works.
import { chromium } from '../media-single-rendition/node_modules/playwright/index.mjs';
import crypto, { X509Certificate } from 'node:crypto';
import tls from 'node:tls';
import { mediaList } from './verify-browser.mjs';

const origin = 'https://localhost:19443';
const finite = async (label, ms, work) => {
  let timer;
  try { return await Promise.race([work(), new Promise((_, reject) => {
    timer = setTimeout(() => reject(new Error(`${label} timeout`)), ms);
  })]); } finally { clearTimeout(timer); }
};

async function pin() {
  return new Promise((resolve, reject) => {
    const socket = tls.connect({host:'localhost',port:19443,servername:'localhost',rejectUnauthorized:false}, () => {
      try {
        const cert = new X509Certificate(socket.getPeerCertificate(true).raw);
        resolve(crypto.createHash('sha256').update(cert.publicKey.export({type:'spki',format:'der'})).digest('base64'));
      } catch (error) { reject(error); } finally { socket.end(); }
    });
    socket.setTimeout(3000, () => { socket.destroy(); reject(new Error('pin timeout')); });
    socket.on('error', reject);
  });
}

async function syntheticCapture(delayAudio) {
  const originalPc = window.RTCPeerConnection;
  window.__diagnostic = { pcs: [], sourceContexts: [], timers: [], tracks: [], audioSender: null, audioTrack: null };
  window.RTCPeerConnection = class extends originalPc {
    constructor(...args) { super(...args); window.__diagnostic.pcs.push(this); }
    addTransceiver(track, options) {
      if (delayAudio && track?.kind === 'audio') {
        const transceiver = super.addTransceiver('audio', options);
        window.__diagnostic.audioSender = transceiver.sender;
        window.__diagnostic.audioTrack = track;
        return transceiver;
      }
      return super.addTransceiver(track, options);
    }
  };
  const tone = frequency => {
    const audio = new AudioContext();
    window.__diagnostic.sourceContexts.push(audio);
    const destination = audio.createMediaStreamDestination();
    const oscillator = audio.createOscillator();
    oscillator.frequency.value = frequency;
    oscillator.connect(destination); oscillator.start();
    audio.resume();
    return destination.stream.getAudioTracks()[0];
  };
  navigator.mediaDevices.getDisplayMedia = () => {
    const canvas = document.createElement('canvas'); canvas.width=640; canvas.height=360;
    const context = canvas.getContext('2d'); let frame=0;
    const paint = () => { context.fillStyle=frame++%2?'#1355b2':'#e5a325'; context.fillRect(0,0,640,360); };
    paint(); window.__diagnostic.timers.push(setInterval(paint,33));
    const tracks = [canvas.captureStream(30).getVideoTracks()[0], tone(440)];
    window.__diagnostic.tracks.push(...tracks);
    return Promise.resolve(new MediaStream(tracks));
  };
  navigator.mediaDevices.getUserMedia = () => {
    const track=tone(660);window.__diagnostic.tracks.push(track);
    return Promise.resolve(new MediaStream([track]));
  };
}

async function main() {
  const leaf=await pin();
  const browser=await chromium.launch({channel:'chrome',headless:true,args:[`--ignore-certificate-errors-spki-list=${leaf}`,'--autoplay-policy=no-user-gesture-required'],timeout:10000});
  let page, session, token, broadcastId, key, resource;
  try {
    page=await browser.newPage();
    await page.addInitScript(syntheticCapture, process.argv.includes('--delay-audio'));
    await finite('navigate',12000,()=>page.goto(origin));
    const email=`diag-${crypto.randomUUID()}@example.invalid`;
    const password=crypto.randomBytes(24).toString('hex');
    const setup=await finite('API setup',15000,()=>page.evaluate(async ({email,password}) => {
      const call=async (path,body,access,extra={}) => {
        const response=await fetch(path,{method:'POST',credentials:'include',signal:AbortSignal.timeout(7000),headers:{'Content-Type':'application/json',...(access?{Authorization:`Bearer ${access}`}:{}) ,...extra},body:JSON.stringify(body)});
        const value=await response.json();
        if(!response.ok)throw new Error(`${path} ${response.status}`);
        return value;
      };
      await call('/api/v1/auth/register',{email,password,displayName:'Diagnostic'});
      const login=await call('/api/v1/auth/login',{email,password});
      const created=await call('/api/v1/broadcasts',{title:'Diagnostic capture',description:'Local diagnostic'},login.accessToken);
      const bid=created.broadcast.id;
      const prepared=await call(`/api/v1/broadcasts/${bid}/sessions`,{source:'screen'},login.accessToken,{'Idempotency-Key':`diagnostic-${crypto.randomUUID()}`});
      return {access:login.accessToken,bid,sid:prepared.session.id,grant:prepared.publishGrant};
    },{email,password}));
    token=setup.access;broadcastId=setup.bid;session=setup.sid;
    const result=await finite('product capture/WHIP',22000,()=>page.evaluate(async grant => {
      const key=window.WbbMedia.newPublisher(()=>{});
      window.__diagnostic.key=key;
      const offered=JSON.parse(await window.WbbMedia.captureOffer(key));
      const pc=window.__diagnostic.pcs[0];
      const start=performance.now();
      const events=[];
      for(const name of ['connectionstatechange','iceconnectionstatechange'])pc.addEventListener(name,()=>events.push({at:Math.round(performance.now()-start),name,connection:pc.connectionState,ice:pc.iceConnectionState}));
      const response=await fetch(grant.whipUrl,{method:'POST',signal:AbortSignal.timeout(14000),headers:{'Content-Type':'application/sdp',Authorization:`Bearer ${grant.token}`},body:offered.sdp});
      const answer=await response.text();
      if(response.status!==201)throw new Error(`WHIP ${response.status}`);
      await window.WbbMedia.acceptAnswer(key,answer);
      const section=sdp=>sdp?.split(/\r?\nm=video /)[1]?.split(/\r?\nm=/)[0]||'';
      const summary=sdp=>{const part=section(sdp);return {m:part.split(/\r?\n/,1)[0],direction:part.match(/a=(sendrecv|sendonly|recvonly|inactive)/)?.[1]||'',h264:part.includes('H264/90000')}};
      const samples=[];
      let audioReleased=false;
      for(let i=0;i<24;i++){
        await new Promise(resolve=>setTimeout(resolve,250));
        const stats=await pc.getStats();
        const sample={at:Math.round(performance.now()-start),ice:pc.iceConnectionState,connection:pc.connectionState};
        stats.forEach(s=>{
          if(s.type==='outbound-rtp'){
            const codec=stats.get(s.codecId);
            sample[s.kind]={framesEncoded:s.framesEncoded||0,packetsSent:s.packetsSent||0,bytesSent:s.bytesSent||0,codec:codec?.mimeType};
          }
        });
        if(!audioReleased && window.__diagnostic.audioSender && sample.video?.packetsSent>0) {
          await window.__diagnostic.audioSender.replaceTrack(window.__diagnostic.audioTrack);
          audioReleased=true;
        }
        samples.push(sample);
      }
      return {key,location:response.headers.get('Location'),etag:response.headers.get('ETag'),offer:summary(pc.localDescription?.sdp),answer:summary(pc.remoteDescription?.sdp),events,samples,audioReleased};
    },setup.grant));
    key=result.key;resource=result.location;
    const paths=await mediaList('paths');
    console.log(JSON.stringify({session,audioReleased:result.audioReleased,offer:result.offer,answer:result.answer,events:result.events,samples:result.samples,source:paths.filter(x=>x.name?.includes(session)).map(x=>({name:x.name,ready:x.ready,tracks:x.tracks}))}));
  } finally {
    if(page && key)await finite('local capture close',5000,()=>page.evaluate(key=>window.WbbMedia.stopPublisher(key),key)).catch(()=>{});
    if(page && resource && token)await finite('WHIP delete',5000,()=>page.evaluate(async ({resource,token})=>fetch(resource,{method:'DELETE',signal:AbortSignal.timeout(3000),headers:{Authorization:`Bearer ${token}`}}).then(r=>r.status),{resource,token})).catch(()=>{});
    if(page && session && broadcastId && token)await finite('session stop',6000,()=>page.evaluate(async ({session,broadcastId,token})=>fetch(`/api/v1/broadcasts/${broadcastId}/sessions/${session}/stop`,{method:'POST',signal:AbortSignal.timeout(4000),headers:{Authorization:`Bearer ${token}`}}).then(r=>r.status),{session,broadcastId,token})).catch(()=>{});
    if(page)await finite('source cleanup',4000,()=>page.evaluate(async()=>{const d=window.__diagnostic;d.timers.forEach(clearInterval);d.tracks.forEach(t=>t.stop());await Promise.allSettled(d.sourceContexts.map(c=>c.close()));})).catch(()=>{});
    await finite('browser close',5000,()=>browser.close()).catch(()=>{});
  }
}

main().catch(error=>{console.error(error);process.exitCode=1});
