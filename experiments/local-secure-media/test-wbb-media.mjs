import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';

const source = fs.readFileSync(new URL('../../wbb-frontend/web/wbb_media.js', import.meta.url), 'utf8');
const deferred = () => {
  let resolve,reject;
  const promise = new Promise((done,fail) => { resolve = done; reject = fail; });
  return { promise, resolve,reject };
};
const track = () => ({readyState: 'live', handlers: new Map(),
  stop() { this.readyState = 'ended'; },
  end() { this.readyState = 'ended'; this.handlers.get('ended')?.(); },
  addEventListener(name, handler) { this.handlers.set(name, handler); }});
const stream = tracks => ({getTracks: () => tracks,
  getVideoTracks: () => tracks.filter(t => t.kind === 'video'),
  getAudioTracks: () => tracks.filter(t => t.kind === 'audio')});

function mediaWith(displayPrompt, micPrompt, onMic = () => {}) {
  const documentListeners=new Map();
  const sandbox = {
    window: {},
    navigator: {mediaDevices: {
      getDisplayMedia: () => displayPrompt.promise,
      getUserMedia: () => { onMic(); return micPrompt.promise; },
    }},
    document: {visibilityState:'visible',
      addEventListener:(name,fn)=>documentListeners.set(name,fn),
      removeEventListener:name=>documentListeners.delete(name),
      createElement: () => ({srcObject: null, play: async () => {},
      setAttribute() {}, style: {}})},
    setTimeout, clearTimeout, Promise,
  };
  vm.runInNewContext(source, sandbox);
  const media=sandbox.window.WbbMedia;
  media.__emitVisibility=state=>{sandbox.document.visibilityState=state;documentListeners.get('visibilitychange')?.();};
  return media;
}

function cameraMedia(cameraPrompt) {
  const documentListeners=new Map(), windowListeners=new Map();
  let displayCalls=0, cameraCalls=0, constraints;
  const sandbox={
    window:{addEventListener:(name,fn)=>windowListeners.set(name,fn),
      removeEventListener:name=>windowListeners.delete(name)},
    document:{visibilityState:'visible',
      addEventListener:(name,fn)=>documentListeners.set(name,fn),
      removeEventListener:name=>documentListeners.delete(name),
      createElement:()=>({srcObject:null,play:async()=>{},setAttribute(){},style:{}})},
    navigator:{mediaDevices:{
      getDisplayMedia:()=>{displayCalls++;throw new Error('screen capture used');},
      getUserMedia:value=>{cameraCalls++;constraints=value;return cameraPrompt.promise;},
    }},
    setTimeout,clearTimeout,Promise,
    MediaStream:class {constructor(tracks){this.tracks=tracks;}getTracks(){return this.tracks;}},
    RTCPeerConnection:class {close(){this.closed=true;}},
    RTCRtpSender:{getCapabilities:()=>({codecs:[]})},
  };
  vm.runInNewContext(source,sandbox);
  return {media:sandbox.window.WbbMedia,document:sandbox.document,documentListeners,windowListeners,
    get displayCalls(){return displayCalls;},get cameraCalls(){return cameraCalls;},get constraints(){return constraints;}};
}

test('late display permission result is stopped after attempt cancellation', async () => {
  const display = deferred();
  const media = mediaWith(display, deferred());
  const key = media.newPublisher('screen', () => {});
  const pending = media.captureOffer(key).catch(e => e);
  await media.stopPublisher(key);
  const video = track(); video.kind = 'video';
  display.resolve(stream([video]));
  assert.match(String(await pending), /capture cancelled/);
  assert.equal(video.readyState, 'ended');
});

test('late microphone permission result and earlier display both stop', async () => {
  const display = deferred();
  const microphone = deferred();
  const micRequested = deferred();
  const media = mediaWith(display, microphone, () => micRequested.resolve());
  const key = media.newPublisher('screen', () => {});
  const pending = media.captureOffer(key).catch(e => e);
  const video = track(); video.kind = 'video';
  display.resolve(stream([video]));
  await micRequested.promise;
  const mic = track(); mic.kind = 'audio';
  await media.stopPublisher(key);
  microphone.resolve(stream([mic]));
  assert.match(String(await pending), /capture cancelled/);
  assert.equal(video.readyState, 'ended');
  assert.equal(mic.readyState, 'ended');
});

test('screen track ending during pending microphone permission stops immediately', async()=>{
  const display=deferred(), microphone=deferred(), micRequested=deferred();
  const media=mediaWith(display,microphone,()=>micRequested.resolve());
  let ended=0;
  const key=media.newPublisher('screen',()=>ended++);
  const pending=media.captureOffer(key).catch(e=>e);
  const video=track();video.kind='video';
  display.resolve(stream([video]));
  await micRequested.promise;
  video.end();
  assert.equal(ended,1);
  assert.equal(video.readyState,'ended');
  const mic=track();mic.kind='audio';
  microphone.resolve(stream([mic]));
  assert.match(String(await pending),/capture cancelled/);
  assert.equal(mic.readyState,'ended');
});

test('camera late permission and partial capture release every returned track',async()=>{
  for(const includeAudio of [true,false]) {
    const prompt=deferred(), fixture=cameraMedia(prompt);
    const key=fixture.media.newPublisher('camera',()=>{});
    const pending=fixture.media.captureOffer(key).catch(e=>e);
    if(includeAudio)await fixture.media.stopPublisher(key);
    const video=track();video.kind='video';
    const audio=track();audio.kind='audio';
    prompt.resolve(stream(includeAudio?[video,audio]:[video]));
    assert.match(String(await pending),includeAudio?/capture cancelled/:/camera video and microphone/);
    assert.equal(video.readyState,'ended');
    if(includeAudio)assert.equal(audio.readyState,'ended');
    assert.equal(fixture.cameraCalls,1);
    assert.equal(fixture.displayCalls,0);
    assert.equal(fixture.constraints.audio,true);
    assert.ok(fixture.constraints.video);
  }
});

test('camera permission rejection and missing H264 leave no live capture',async()=>{
  for(const reason of ['NotAllowedError','NotFoundError','NotReadableError']) {
    const denied=deferred(), deniedFixture=cameraMedia(denied);
    const deniedKey=deniedFixture.media.newPublisher('camera',()=>{});
    const rejected=deniedFixture.media.captureOffer(deniedKey).catch(e=>e);
    denied.reject(new Error(reason));
    assert.match(String(await rejected),new RegExp(reason));
  }
  const prompt=deferred(), fixture=cameraMedia(prompt);
  const key=fixture.media.newPublisher('camera',()=>{});
  const pending=fixture.media.captureOffer(key).catch(e=>e);
  const video=track();video.kind='video';const audio=track();audio.kind='audio';
  prompt.resolve(stream([video,audio]));
  assert.match(String(await pending),/H\.264 publishing is unavailable/);
  assert.equal(video.readyState,'ended');
  assert.equal(audio.readyState,'ended');
});

test('camera hidden/pagehide stop but screen visibility change does not',async()=>{
  const prompt=deferred(), fixture=cameraMedia(prompt);
  let ended=0;
  const key=fixture.media.newPublisher('camera',()=>ended++);
  const pending=fixture.media.captureOffer(key).catch(e=>e);
  fixture.documentListeners.get('visibilitychange')(); // visible permission prompt
  assert.equal(ended,0);
  fixture.document.visibilityState='hidden';
  fixture.documentListeners.get('visibilitychange')();
  assert.equal(ended,1);
  const video=track();video.kind='video';const audio=track();audio.kind='audio';
  prompt.resolve(stream([video,audio]));
  assert.match(String(await pending),/capture cancelled/);
  assert.equal(video.readyState,'ended');assert.equal(audio.readyState,'ended');
  const pagePrompt=deferred(), pageFixture=cameraMedia(pagePrompt);
  let pageEnded=0;
  const pageKey=pageFixture.media.newPublisher('camera',()=>pageEnded++);
  const pagePending=pageFixture.media.captureOffer(pageKey).catch(e=>e);
  pageFixture.windowListeners.get('pagehide')();
  assert.equal(pageEnded,1);
  const pageVideo=track();pageVideo.kind='video';const pageAudio=track();pageAudio.kind='audio';
  pagePrompt.resolve(stream([pageVideo,pageAudio]));
  assert.match(String(await pagePending),/capture cancelled/);
  assert.equal(pageVideo.readyState,'ended');assert.equal(pageAudio.readyState,'ended');
  const screenPrompt=deferred(), screenMic=deferred(), screen=mediaWith(screenPrompt,screenMic);
  let screenEnded=0;
  const screenKey=screen.newPublisher('screen',()=>screenEnded++);
  const screenPending=screen.captureOffer(screenKey).catch(e=>e);
  const screenVideo=track();screenVideo.kind='video';
  screenPrompt.resolve(stream([screenVideo]));
  await Promise.resolve();
  screen.__emitVisibility('hidden');
  assert.equal(screenEnded,0);
  assert.equal(screenVideo.readyState,'live');
  await screen.stopPublisher(screenKey);
  const mic=track();mic.kind='audio';screenMic.resolve(stream([mic]));
  await screenPending;
});

test('native HLS branch preserves user pause on live seek and reports playback error', async () => {
  const listeners=new Map();
  const video={paused:true,muted:true,currentTime:0,seekable:{length:1,end:()=>30},
    style:{},setAttribute(){},removeAttribute(name){if(name==='src')this.src=undefined;},
    load(){},canPlayType:type=>type==='application/vnd.apple.mpegurl'?'probably':'',
    addEventListener(name,handler){listeners.set(name,handler);},
    async play(){this.paused=false;},
    pause(){this.paused=true;listeners.get('pause')?.();},
  };
  const sandbox={window:{},document:{createElement:()=>video},setTimeout,clearTimeout,Promise};
  vm.runInNewContext(source,sandbox);
  const events=[];
  const media=sandbox.window.WbbMedia;
  const key=media.newViewer('/media/wbb/s1/1/720p/index.m3u8',event=>events.push(event));
  await media.playViewer(key,false);
  assert.equal(video.src,'/media/wbb/s1/1/720p/index.m3u8');
  video.pause();
  media.goLive(key);
  assert.equal(video.currentTime,30);
  assert.equal(video.paused,true);
  listeners.get('error')();
  assert.deepEqual(events,['playback-error']);
  media.stopViewer(key);
  assert.equal(video.src,undefined);
});
