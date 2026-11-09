import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';

const source = fs.readFileSync(new URL('../../wbb-frontend/web/wbb_media.js', import.meta.url), 'utf8');
const deferred = () => {
  let resolve;
  const promise = new Promise(done => { resolve = done; });
  return { promise, resolve };
};
const track = () => ({readyState: 'live', stop() { this.readyState = 'ended'; },
  addEventListener() {}});
const stream = tracks => ({getTracks: () => tracks,
  getVideoTracks: () => tracks.filter(t => t.kind === 'video'),
  getAudioTracks: () => tracks.filter(t => t.kind === 'audio')});

function mediaWith(displayPrompt, micPrompt, onMic = () => {}) {
  const sandbox = {
    window: {},
    navigator: {mediaDevices: {
      getDisplayMedia: () => displayPrompt.promise,
      getUserMedia: () => { onMic(); return micPrompt.promise; },
    }},
    document: {createElement: () => ({srcObject: null, play: async () => {},
      setAttribute() {}, style: {}})},
    setTimeout, clearTimeout, Promise,
  };
  vm.runInNewContext(source, sandbox);
  return sandbox.window.WbbMedia;
}

test('late display permission result is stopped after attempt cancellation', async () => {
  const display = deferred();
  const media = mediaWith(display, deferred());
  const key = media.newPublisher(() => {});
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
  const key = media.newPublisher(() => {});
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
