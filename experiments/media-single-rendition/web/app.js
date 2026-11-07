// WBB media spike UI (ticket 02).
//
// Lifecycle model: every publish attempt owns its resources in an attempt
// object {pc, stream, cleanups, resource, cancelled}. A stale attempt (user
// stopped, or a newer attempt started) may only clean up ITS OWN resources —
// never globals — and may never write UI state again. All awaits are followed
// by a staleness check, so late captures/POSTs/PATCHes cannot corrupt or kill
// a newer session. Fault injection happens OUTSIDE (network routes / init
// scripts in the automation); this file only observes.
//
// window.__spikeState / __playbackProbe / __spikeTracks drive automation.

'use strict';

const WHIP_URL = 'http://127.0.0.1:8889/spike/source/whip';
const HLS_URL = 'http://127.0.0.1:8888/spike/720p/index.m3u8';

const logEl = document.getElementById('log');
function log(msg, cls) {
  const t = new Date().toISOString().slice(11, 23);
  const div = document.createElement('div');
  div.textContent = `[${t}] ${msg}`;
  if (cls) div.className = cls;
  logEl.appendChild(div);
  logEl.scrollTop = logEl.scrollHeight;
  updateState({ lastEvent: msg });
}

const state = {
  lifecycle: 'idle',       // idle|starting|publishing|stopping
  iceState: '—',
  whipPost: null, patchStatus: null, deleteStatus: null, lateDeleteStatus: null,
  location: null, etag: null,
  publishedSize: null, outbound: null,
  // Observed (never hardcoded) at teardown time:
  tracksStopped: null, pcClosed: null, acClosed: null,
  lastEvent: '',
};
function updateState(patch) { Object.assign(state, patch); window.__spikeState = state; }

const els = {
  whip: document.getElementById('whip-state'),
  ice: document.getElementById('ice-state'),
  hls: document.getElementById('hls-state'),
  videoInfo: document.getElementById('video-info'),
  mixNote: document.getElementById('mix-note'),
  btnSynth: document.getElementById('btn-synth'),
  btnScreen: document.getElementById('btn-screen'),
  btnCamera: document.getElementById('btn-camera'),
  btnStop: document.getElementById('btn-stop'),
};
const preview = document.getElementById('preview');

function setWhip(t) { els.whip.textContent = t; }
function setIce(t) { els.ice.textContent = t; state.iceState = t; updateState({}); }

const sleep = ms => new Promise(r => setTimeout(r, ms));

// ---------------------------------------------------------------------------
// Attempt-scoped lifecycle
// ---------------------------------------------------------------------------

let current = null;   // the ONLY attempt allowed to write state / own devices

function stale(a) { return a === null || a !== current || a.cancelled; }

function observeTeardown(a) {
  // Evidence from real objects, not flags.
  updateState({
    tracksStopped: !a.stream || a.stream.getTracks().every(t => t.readyState === 'ended'),
    pcClosed: !a.pc || a.pc.connectionState === 'closed',
    acClosed: !a.ac || a.ac.state === 'closed',
  });
}

function stopLocal(a) {
  if (a.stream) a.stream.getTracks().forEach(t => { if (t.readyState !== 'ended') t.stop(); });
  if (a.pc) { try { a.pc.close(); } catch (_) {} }
  a.cleanups.forEach(fn => { try { fn(); } catch (_) {} });
  a.cleanups = [];
}

async function deleteResource(res, field) {
  try {
    const resp = await fetch(res.location, {
      method: 'DELETE',
      headers: res.etag ? { 'If-Match': res.etag } : {},
    });
    updateState({ [field]: resp.status });
    log(`WHIP DELETE(${field === 'deleteStatus' ? '정지' : '늦은 정리'}): HTTP ${resp.status}`, resp.ok ? 'ok' : 'warn');
  } catch (e) {
    updateState({ [field]: 'network-error' });
    log(`WHIP DELETE 오류: ${e}`, 'err');
  }
}

// A capture that resolved after its attempt went stale: every resource the
// attempt registered is released via stopLocal (tracks, sources, AC, RAF) —
// stopping only the returned stream would leak the originals.
function discardCapture(a) {
  stopLocal(a);
  log('시작 도중 정지/교체됨 — 해당 시도의 원본 자원(display/mic/AC/RAF) 모두 정리', 'warn');
}

// teardown: stop the CURRENT attempt. Local resources are released
// immediately; the network DELETE happens after. New starts stay blocked
// until local cleanup finished (lifecycle 'stopping' → 'idle').
async function teardown() {
  const a = current;
  if (!a) return;
  a.cancelled = true;      // freeze every later await of this attempt
  current = null;
  updateState({ lifecycle: 'stopping' });
  refreshButtons();
  setWhip('stopping');

  stopLocal(a);            // devices/PC/AudioContext first, never after network
  if (preview.srcObject === a.stream) preview.srcObject = null;
  setWhip('idle'); setIce('—');
  observeTeardown(a);

  if (a.resource) await deleteResource(a.resource, 'deleteStatus');

  updateState({ lifecycle: 'idle' });
  refreshButtons();
  log(`정리 완료(관측: tracks=${state.tracksStopped}, pcClosed=${state.pcClosed}, acClosed=${state.acClosed})`);
}

// A POST answered after its attempt went stale: the session exists
// server-side, so release it — but ONLY this attempt's session/resources.
async function lateRelease(a, locationHeader, etag) {
  stopLocal(a);
  log('늦은 201 도착 — 해당 세션만 즉시 DELETE', 'warn');
  if (locationHeader) {
    await deleteResource({ location: new URL(locationHeader, WHIP_URL).toString(), etag }, 'lateDeleteStatus');
  }
  observeTeardown(a);
}

async function startPublish(makeStream, kindLabel) {
  if (state.lifecycle !== 'idle') {
    log(`무시: 현재 상태 ${state.lifecycle} — 시작/정리 완료 후 다시 시도하세요.`, 'warn');
    return;
  }
  const a = { kind: kindLabel, pc: null, stream: null, ac: null, cleanups: [], resource: null, cancelled: false };
  current = a;
  updateState({
    lifecycle: 'starting', whipPost: null, patchStatus: null, deleteStatus: null,
    lateDeleteStatus: null, location: null, etag: null, publishedSize: null, outbound: null,
    tracksStopped: null, pcClosed: null, acClosed: null,
  });
  refreshButtons();
  setWhip('starting');

  let stream;
  try {
    stream = await makeStream(a);
  } catch (err) {
    stopLocal(a);          // release whatever the attempt had acquired so far
    if (stale(a)) { log('늦은 캡처 실패 — 이미 정지된 시도, 자원만 정리', 'warn'); return; }
    current = null;
    observeTeardown(a);
    updateState({ lifecycle: 'idle' });
    setWhip('idle');
    refreshButtons();
    log(`캡처 실패(${err.name || err.message || err}) — 획득한 트랙/컨텍스트 정리 완료`, 'err');
    return;
  }
  if (stale(a)) return discardCapture(a);

  a.stream = stream;
  preview.srcObject = stream;

  const vTrack = stream.getVideoTracks()[0];
  if (vTrack) {
    const s = vTrack.getSettings ? vTrack.getSettings() : {};
    updateState({ publishedSize: { w: s.width || 0, h: s.height || 0 } });
    vTrack.addEventListener('ended', () => {
      log('video track ended (공유 중지/장치 해제) → 공통 정리 경로 진입');
      if (!stale(a)) teardown();
    });
  }

  a.pc = new RTCPeerConnection();
  a.pc.oniceconnectionstatechange = () => {
    if (stale(a)) return;
    setIce(a.pc.iceConnectionState);
    log(`ICE 상태: ${a.pc.iceConnectionState}`);
  };
  for (const track of stream.getTracks()) {
    const t = a.pc.addTransceiver(track, { direction: 'sendonly' });
    if (track.kind === 'video') {
      try {
        const params = t.sender.getParameters();
        params.degradationPreference = 'maintain-resolution';
        await t.sender.setParameters(params);
      } catch (e) {
        if (!stale(a)) log(`degradationPreference 설정 실패(무시): ${e.name}`, 'warn');
      }
    }
  }

  try {
    const offer = await a.pc.createOffer();
    await a.pc.setLocalDescription(offer);
    await waitIceGathering(a.pc, 2000);
    if (stale(a)) return;   // stop while gathering: teardown already owns cleanup

    log(`SDP 후보 수집 완료 (state=${a.pc.iceGatheringState}), WHIP POST 전송`);
    const resp = await fetch(WHIP_URL, {
      method: 'POST',
      headers: { 'Content-Type': 'application/sdp' },
      body: a.pc.localDescription.sdp,
    });
    const locationHeader = resp.headers.get('Location');
    const etag = resp.headers.get('ETag');

    if (stale(a)) {         // stop raced the response; lateRelease handles ONLY a
      await lateRelease(a, resp.status === 201 ? locationHeader : null, etag);
      return;
    }

    if (resp.status !== 201 || !locationHeader) {
      log(`WHIP POST 실패: HTTP ${resp.status} location=${locationHeader}`, 'err');
      updateState({ whipPost: resp.status });
      await teardown();
      return;
    }
    updateState({ whipPost: resp.status, location: new URL(locationHeader, WHIP_URL).toString(), etag });
    a.resource = { location: state.location, etag };
    log(`WHIP POST 201 · Location=${locationHeader} · ETag=${etag}`, 'ok');
    setWhip('publishing');

    const answerSdp = await resp.text();
    try {
      await a.pc.setRemoteDescription({ type: 'answer', sdp: answerSdp });
    } catch (e) {
      if (stale(a)) return;
      log(`SDP answer 적용 실패: ${e.name} — 세션 정리`, 'err');
      await teardown();
      return;
    }
    if (stale(a)) return;
    log('SDP answer 적용 완료');
  } catch (e) {
    if (stale(a)) return;
    log(`시작 실패: ${e.name || e} — 정리`, 'err');
    await teardown();
    return;
  }

  // PATCH probe (RFC 8839 trickle fragment round-trip).
  try {
    const frag = buildTrickleFragment(a.pc.localDescription.sdp);
    const patchResp = await fetch(a.resource.location, {
      method: 'PATCH',
      headers: {
        'Content-Type': 'application/trickle-ice-sdpfrag',
        ...(a.resource.etag ? { 'If-Match': a.resource.etag } : {}),
      },
      body: frag,
    });
    if (stale(a)) { log('늦은 PATCH 응답 무시(이미 정지됨)', 'warn'); return; }
    updateState({ patchStatus: patchResp.status });
    log(`WHIP PATCH probe: HTTP ${patchResp.status}`, patchResp.ok ? 'ok' : 'warn');
  } catch (e) {
    if (stale(a)) return;
    updateState({ patchStatus: 'network-error' });
    log(`WHIP PATCH probe 오류: ${e}`, 'warn');
  }
  if (stale(a)) return;     // a late stop must never resurrect 'publishing'

  updateState({ lifecycle: 'publishing' });
  refreshButtons();
  log('송출 중 — 변환(FFmpeg 720p)과 HLS 준비를 기다린 뒤 재생해 보세요.', 'ok');
}

// ---------------------------------------------------------------------------
// Capture sources
// ---------------------------------------------------------------------------

function syntheticStream(a) {
  const canvas = document.createElement('canvas');
  canvas.width = 1280; canvas.height = 720;
  const ctx = canvas.getContext('2d');
  let t = 0, raf = 0;
  (function draw() {
    t += 1;
    ctx.fillStyle = '#101418'; ctx.fillRect(0, 0, 1280, 720);
    const x = (t * 4) % 1480 - 100;
    ctx.fillStyle = '#7C4DFF'; ctx.fillRect(x, 160, 120, 400);
    ctx.fillStyle = '#7ec699'; ctx.fillRect(1280 - x - 60, 200, 60, 320);
    ctx.fillStyle = '#e6e6e6'; ctx.font = '28px sans-serif';
    ctx.fillText(`WBB synthetic ${new Date().toISOString().slice(11, 19)}`, 40, 80);
    raf = requestAnimationFrame(draw);
  })();
  const video = canvas.captureStream(30);

  const ac = new AudioContext();
  const dest = ac.createMediaStreamDestination();
  const osc = ac.createOscillator(); osc.frequency.value = 440;
  const osc2 = ac.createOscillator(); osc2.frequency.value = 880;
  const gain = ac.createGain(); gain.gain.value = 0.12;
  osc.connect(gain); osc2.connect(gain); gain.connect(dest);
  osc.start(); osc2.start();

  a.ac = ac; // observed at teardown via ac.state
  a.cleanups.push(() => { cancelAnimationFrame(raf); osc.stop(); osc2.stop(); return ac.close(); });
  return new MediaStream([...video.getVideoTracks(), ...dest.stream.getAudioTracks()]);
}

async function pcScreenStream(a) {
  const display = await navigator.mediaDevices.getDisplayMedia({
    video: { frameRate: 30 },
    audio: {},
    selfBrowserSurface: 'exclude',
  });
  // Register EVERY acquired resource on the attempt immediately, so a stop
  // at any later point (mic wait, mix setup, stale) releases the originals.
  a.cleanups.push(() => display.getTracks().forEach(t => { if (t.readyState !== 'ended') t.stop(); }));
  if (stale(a)) {           // stopped while the display promise was pending:
    stopLocal(a);           // release display NOW, never prompt for the mic
    throw new Error('시작 도중 정지됨 — display 자원 정리 (마이크 요청 없음)');
  }

  // PC 요구사항: 마이크 음성이 필수다. 거부/실패 시 지금까지 취득한 자원을
  // 모두 정리하고 실패한다 (display가 남지 않는다).
  let mic;
  try {
    mic = await navigator.mediaDevices.getUserMedia({ audio: true });
  } catch (e) {
    stopLocal(a);
    throw new Error(`마이크 획득 실패(${e.name}) — 화면 공유 정리 후 중단 (마이크는 필수)`);
  }
  a.cleanups.push(() => mic.getTracks().forEach(t => { if (t.readyState !== 'ended') t.stop(); }));
  if (stale(a)) { stopLocal(a); throw new Error('시작 도중 정지됨 — 자원 정리 완료'); }

  const sharedAudio = display.getAudioTracks();
  els.mixNote.textContent = sharedAudio.length
    ? '탭/시스템 소리 + 마이크를 WebAudio로 합성해 송출합니다.'
    : '공유 오디오 트랙이 없습니다(오디오 포함 탭 재선택 권장). 마이크만 송출합니다.';
  log(`display tracks: video=${display.getVideoTracks().length} audio=${sharedAudio.length}, mic=1`);

  let ac;
  try {
    ac = new AudioContext();
    a.ac = ac;                                  // register BEFORE any throw
    a.cleanups.push(() => ac.close());          // can happen in mix setup
    const dest = ac.createMediaStreamDestination();
    if (sharedAudio.length) ac.createMediaStreamSource(new MediaStream(sharedAudio)).connect(dest);
    ac.createMediaStreamSource(mic).connect(dest);
    return new MediaStream([...display.getVideoTracks(), ...dest.stream.getAudioTracks()]);
  } catch (e) {
    stopLocal(a);          // AC/mix failure must not leak display/mic either
    throw new Error(`WebAudio 믹스 생성 실패(${e.name}) — 자원 정리 완료`);
  }
}

async function cameraStream(a) {
  const cam = await navigator.mediaDevices.getUserMedia({
    video: { facingMode: 'user', width: { ideal: 1280 }, height: { ideal: 720 } },
    audio: true,
  });
  a.cleanups.push(() => cam.getTracks().forEach(t => t.stop()));
  return cam;
}

// Automation observability hooks (read-only):
window.__spikeTracks = () => (current && current.stream ? current.stream.getTracks() : []);
window.__stopVideoTrack = () => { if (current && current.stream) current.stream.getVideoTracks()[0]?.stop(); };

// ---------------------------------------------------------------------------
// WHIP helpers
// ---------------------------------------------------------------------------

function waitIceGathering(c, timeoutMs) {
  return new Promise(resolve => {
    if (c.iceGatheringState === 'complete') return resolve();
    const timer = setTimeout(() => { c.removeEventListener('icegatheringstatechange', onChange); resolve(); }, timeoutMs);
    function onChange() {
      if (c.iceGatheringState === 'complete') {
        clearTimeout(timer);
        c.removeEventListener('icegatheringstatechange', onChange);
        resolve();
      }
    }
    c.addEventListener('icegatheringstatechange', onChange);
  });
}

function buildTrickleFragment(sdp) {
  const lines = sdp.split('\r\n');
  const ufrag = (lines.find(l => l.startsWith('a=ice-ufrag:')) || '').slice('a=ice-ufrag:'.length);
  const pwd = (lines.find(l => l.startsWith('a=ice-pwd:')) || '').slice('a=ice-pwd:'.length);
  const mid = (lines.find(l => l.startsWith('a=mid:')) || 'a=mid:0').slice('a=mid:'.length);
  const cand = lines.find(l => l.startsWith('a=candidate:'));
  return [
    `a=ice-ufrag:${ufrag}`,
    `a=ice-pwd:${pwd}`,
    'm=audio 9 UDP/TLS/RTP/SAVPF 0',
    `a=mid:${mid}`,
    cand || 'a=candidate:1 1 udp 2130706431 127.0.0.1 18389 typ host',
    '',
  ].join('\r\n');
}

// ---------------------------------------------------------------------------
// LL-HLS playback (hls.js local bundle) + evidence probes
// ---------------------------------------------------------------------------

let hls = null;
const playBtn = document.getElementById('btn-play');
const video = document.getElementById('playback');

async function attachPlayer() {
  if (video.canPlayType('application/vnd.apple.mpegurl')) {
    video.src = HLS_URL;
    els.hls.textContent = 'native HLS';
  } else if (window.Hls && window.Hls.isSupported()) {
    hls = new Hls({ lowLatencyMode: true, backBufferLength: 30 });
    hls.on(Hls.Events.MANIFEST_PARSED, () => {
      els.hls.textContent = `hls.js ${Hls.version} manifest OK`;
      log(`hls.js ${Hls.version}: manifest 파싱 성공`, 'ok');
    });
    hls.on(Hls.Events.ERROR, (_, data) => {
      if (data.fatal) { els.hls.textContent = `hls.js 오류: ${data.details}`; log(`hls.js 치명 오류: ${data.details}`, 'err'); }
    });
    hls.loadSource(HLS_URL);
    hls.attachMedia(video);
  } else {
    els.hls.textContent = 'HLS 재생 불가';
    log('hls.js 미지원 환경', 'err');
    return;
  }
  video.addEventListener('loadedmetadata', () => {
    els.videoInfo.textContent = `${video.videoWidth}x${video.videoHeight}`;
    log(`재생 스트림 해상도: ${video.videoWidth}x${video.videoHeight}`);
  });
}

playBtn.addEventListener('click', async () => {
  if (!hls && !video.src) await attachPlayer();
  try { await video.play(); log('재생 시작', 'ok'); } catch (e) { log(`재생 거부(자동재생 정책): ${e.name}`, 'warn'); }
});
document.getElementById('btn-sound').addEventListener('click', async () => {
  video.muted = false;
  try { await video.play(); log('소리 켜고 재생 시도', 'ok'); } catch (e) { log(`재생 거부: ${e.name}`, 'warn'); }
});

// Playback evidence probe: currentTime, decoded frames, best-effort audio level.
window.__playbackProbe = async () => {
  const q = video.getVideoPlaybackQuality ? video.getVideoPlaybackQuality() : { totalVideoFrames: 0 };
  let audioLevel = -1;
  try {
    if (!window.__pbAnalyser) {
      window.__pbAC = new AudioContext();
      const stream = video.captureStream ? video.captureStream() : null;
      if (stream && stream.getAudioTracks().length) {
        const src = window.__pbAC.createMediaStreamSource(stream);
        window.__pbAnalyser = window.__pbAC.createAnalyser();
        src.connect(window.__pbAnalyser);
      }
    }
    if (window.__pbAnalyser) {
      const buf = new Uint8Array(window.__pbAnalyser.frequencyBinCount);
      window.__pbAnalyser.getByteFrequencyData(buf);
      audioLevel = buf.reduce((a, b) => a + b, 0);
    }
  } catch (_) { /* best effort */ }
  return { currentTime: video.currentTime, frames: q.totalVideoFrames, audioLevel };
};

// LL-HLS manifest evidence: master → media playlist, part markers.
async function manifestProbe() {
  try {
    const master = await (await fetch(HLS_URL, { cache: 'no-store' })).text();
    const variantUrl = new URL((master.match(/^([^#\s][^\s]*)$/m) || [])[1] || '', HLS_URL).toString();
    const media = await (await fetch(variantUrl, { cache: 'no-store' })).text();
    const isLL = media.includes('EXT-X-PART-INF') || media.includes('EXT-X-PART:');
    const hasServerControl = media.includes('CAN-BLOCK-RELOAD=YES');
    log(`manifest 확인: LL-HLS=${isLL}, CAN-BLOCK-RELOAD=${hasServerControl}`, isLL ? 'ok' : 'warn');
    updateState({ manifestLL: isLL });
  } catch (e) {
    log(`manifest 확인 실패(아직 준비 안 됨): ${e}`, 'warn');
  }
}

// ---------------------------------------------------------------------------
// Buttons, outbound stats, autostart
// ---------------------------------------------------------------------------

els.btnSynth.addEventListener('click', () => startPublish(a => syntheticStream(a), 'synthetic'));
els.btnScreen.addEventListener('click', () => startPublish(a => pcScreenStream(a), 'pc-screen'));
els.btnCamera.addEventListener('click', () => startPublish(a => cameraStream(a), 'camera'));
els.btnStop.addEventListener('click', () => teardown());

function refreshButtons() {
  const busy = state.lifecycle !== 'idle';
  els.btnSynth.disabled = busy; els.btnScreen.disabled = busy; els.btnCamera.disabled = busy;
  els.btnStop.disabled = state.lifecycle === 'idle';
}
refreshButtons();
setInterval(refreshButtons, 300);

// Publisher outbound stats for automation.
setInterval(async () => {
  if (current && !current.cancelled && state.lifecycle === 'publishing' && current.pc) {
    try {
      const stats = await current.pc.getStats();
      stats.forEach(r => {
        if (r.type === 'outbound-rtp' && r.kind === 'video' && typeof r.bytesSent === 'number') {
          updateState({ outbound: { bytes: r.bytesSent, frames: r.framesEncoded ?? r.framesSent ?? 0 } });
        }
      });
    } catch (_) {}
  }
}, 1000);

// Automation entry: ?mode=synth&autostart=1 (synthetic only; no devices).
const params = new URLSearchParams(location.search);
if (params.get('mode') === 'synth' && params.get('autostart') === '1') {
  log('autostart=1: 합성 모드 자동 시작');
  setTimeout(() => startPublish(a => syntheticStream(a), 'synthetic').then(() => setTimeout(manifestProbe, 6000)), 1000);
}
