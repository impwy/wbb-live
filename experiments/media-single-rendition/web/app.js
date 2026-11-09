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

// Runtime media endpoint configuration (backward compatible).
// - No config (default): existing localhost URLs — all 83 CPU-spike checks
//   and the VAAPI spike keep working unchanged.
// - HTTPS mode (window.__WBB_MEDIA_CONFIG): same-origin RELATIVE paths only.
//   External origins, scheme-relative URLs (//host), path escapes (/../),
//   and localhost fallbacks are rejected at startup. No arbitrary upstream.
const RUNTIME_MEDIA_CONFIG = (() => {
  const raw = window.__WBB_MEDIA_CONFIG;
  if (!raw) {
    // HTTPS page without runtime config would silently fall back to
    // http://127.0.0.1:* (mixed content). Reject explicitly instead.
    if (window.location.protocol === 'https:') {
      throw new Error('__WBB_MEDIA_CONFIG missing on HTTPS page (would cause mixed-content localhost fallback)');
    }
    return null;
  }
  const fail = why => { throw new Error(`__WBB_MEDIA_CONFIG invalid: ${why}`); };
  const rel = p => {
    if (typeof p !== 'string' || !p.startsWith('/') || p.startsWith('//'))
      fail('not a same-origin relative path');
    if (/[?#\\\x00-\x1f]/.test(p) || /%(?:2f|5c)/i.test(p)) fail('unsafe path encoding');
    let decoded = p;
    for (let i = 0; i < 3; i++) {
      try { decoded = decodeURIComponent(decoded); } catch (_) { fail('invalid path encoding'); }
      if (decoded.split('/').some(segment => segment === '.' || segment === '..')) fail('path traversal');
    }
    try {
      const u = new URL(p, window.location.origin);
      if (u.origin !== window.location.origin || u.protocol !== window.location.protocol || u.pathname !== p)
        fail('path normalization changed endpoint');
      return u.pathname;
    } catch (_) { fail('unparseable path'); }
  };
  const cfg = {
    whipPath: rel(raw.whipPath),
    hlsPath: rel(raw.hlsPath),
    whipBase: rel(raw.whipBase),
  };
  if (cfg.whipBase !== cfg.whipPath ||
      cfg.whipPath !== '/__media-check/whip/spike/source/whip' ||
      cfg.hlsPath !== '/__media-check/hls/spike/720p/index.m3u8') fail('unexpected verification endpoint');
  if (/localhost|127\.0\.0\.1|\[::1\]/.test(cfg.whipPath + ' ' + cfg.hlsPath)) {
    fail('localhost in HTTPS mode paths');
  }
  return cfg;
})();

const WHIP_URL = RUNTIME_MEDIA_CONFIG
  ? RUNTIME_MEDIA_CONFIG.whipPath
  : 'http://127.0.0.1:8889/spike/source/whip';
const HLS_URL = RUNTIME_MEDIA_CONFIG
  ? RUNTIME_MEDIA_CONFIG.hlsPath
  : 'http://127.0.0.1:8888/spike/720p/index.m3u8';

// Absolute base for resolving relative URLs (a relative WHIP_URL would cause
// a TypeError if passed directly to new URL() as a base).
const WHIP_BASE_ABSOLUTE = new URL(WHIP_URL, window.location.origin).toString();
const HLS_BASE_ABSOLUTE = new URL(HLS_URL, window.location.origin).toString();

// Resolve a WHIP Location header. The proxy (Caddy) already restores the
// path prefix in the Location; the client VALIDATES that it is same-origin
// and within the allowed WHIP resource space, then uses it as-is.
// Never prepends whipBase (that would double the prefix).
function resolveWhipLocation(locationHeader) {
  if (typeof locationHeader !== 'string' || !locationHeader || /[?#\\\x00-\x1f]/.test(locationHeader)) {
    throw new Error('invalid WHIP Location');
  }
  // A URL constructor normalizes dot segments. Reject them in the original
  // header as well, including encoded forms, before accepting the resource.
  let decoded = locationHeader;
  for (let i = 0; i < 3; i++) {
    try { decoded = decodeURIComponent(decoded); } catch (_) { throw new Error('invalid WHIP Location encoding'); }
    if (decoded.split('/').some(segment => segment === '.' || segment === '..')) throw new Error('WHIP Location traversal');
  }
  if (/%(?:2f|5c)/i.test(locationHeader)) throw new Error('WHIP Location encoded separator');
  let resolved;
  try {
    resolved = new URL(locationHeader, WHIP_BASE_ABSOLUTE);
  } catch (_) {
    throw new Error('unparseable WHIP Location');
  }
  if (resolved.origin !== new URL(WHIP_BASE_ABSOLUTE).origin || resolved.username || resolved.password || resolved.hash) {
    throw new Error('WHIP Location cross-origin or malformed');
  }
  if (RUNTIME_MEDIA_CONFIG) {
    // Path is already normalized by the URL constructor (/../ and %2e%2e
    // are resolved); verify the normalized path stays inside the prefix.
    if (!resolved.pathname.startsWith(RUNTIME_MEDIA_CONFIG.whipBase + '/')) {
      throw new Error('WHIP Location outside allowed resource path');
    }
  }
  return resolved.toString();
}

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
    log('WHIP DELETE 네트워크 오류', 'err');
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
    await deleteResource({ location: resolveWhipLocation(locationHeader), etag }, 'lateDeleteStatus');
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
      log(`WHIP POST 실패: HTTP ${resp.status}`, 'err');
      updateState({ whipPost: resp.status });
      await teardown();
      return;
    }
    updateState({ whipPost: resp.status, location: resolveWhipLocation(locationHeader), etag });
    a.resource = { location: state.location, etag };
    log('WHIP POST 201 · resource 수신', 'ok');
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
  // Register a cleanup for EVERY resource right after it is acquired, so an
  // exception part-way through initialization cannot leak anything.
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
  a.cleanups.push(() => cancelAnimationFrame(raf));

  const video = canvas.captureStream(30);
  a.cleanups.push(() => video.getTracks().forEach(tr => { if (tr.readyState !== 'ended') tr.stop(); }));

  const ac = new AudioContext();
  a.ac = ac; // observed at teardown via ac.state
  a.cleanups.push(() => ac.close());

  try {
    const dest = ac.createMediaStreamDestination();
    // AC.close() does NOT end the destination's audio track — stop it
    // explicitly, registered immediately so later init failures can't leak it.
    a.cleanups.push(() => dest.stream.getTracks().forEach(tr => { if (tr.readyState !== 'ended') tr.stop(); }));
    const osc = ac.createOscillator(); osc.frequency.value = 440;
    const osc2 = ac.createOscillator(); osc2.frequency.value = 880;
    const gain = ac.createGain(); gain.gain.value = 0.12;
    osc.connect(gain); osc2.connect(gain); gain.connect(dest);
    osc.start(); osc2.start();
    a.cleanups.push(() => { osc.stop(); osc2.stop(); });
    return new MediaStream([...video.getVideoTracks(), ...dest.stream.getAudioTracks()]);
  } catch (e) {
    stopLocal(a);
    throw new Error(`합성 오디오 초기화 실패(${e.name}) — 자원 정리 완료`);
  }
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

  // P1: 화면 공유 중지(브라우저 UI '공유 중지')는 마이크 대기 중에도 즉시
  // 공통 종료 경로로 연결된다. 획득 직시 ended 리스너를 건다.
  const displayVideo = display.getVideoTracks()[0];
  displayVideo.addEventListener('ended', () => {
    log('display track ended (공유 중지) → 공통 정리 경로 진입');
    if (!stale(a)) teardown();
  });
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

  // 마이크를 기다리는 동안 공유가 중지됐거나 시도가 취소됐으면 여기서 끝낸다.
  if (stale(a)) { stopLocal(a); throw new Error('시작 도중 정지됨 — 자원 정리 완료'); }
  if (displayVideo.readyState === 'ended') {
    stopLocal(a);
    throw new Error('마이크 대기 중 화면 공유가 종료됨 — 자원 정리 후 중단');
  }

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
    // AC.close() does NOT end the destination's audio track — stop it
    // explicitly, registered immediately so later mix failures can't leak it.
    a.cleanups.push(() => dest.stream.getTracks().forEach(tr => { if (tr.readyState !== 'ended') tr.stop(); }));
    // 두 음원을 합산할 때 clipping을 피하기 위해 각 입력에 감쇠 gain을 건다
    // (두 입력이 동시에 최대 진폭이어도 합이 1.0을 넘지 않게 0.5씩).
    const sharedGain = ac.createGain(); sharedGain.gain.value = 0.5;
    const micGain = ac.createGain(); micGain.gain.value = 0.5;
    if (sharedAudio.length) ac.createMediaStreamSource(new MediaStream(sharedAudio)).connect(sharedGain);
    ac.createMediaStreamSource(mic).connect(micGain);
    sharedGain.connect(dest); micGain.connect(dest);
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

// Peak amplitude of the CURRENT attempt's outgoing (mixed) audio track —
// used to verify the PC mix attenuation. The observer AudioContext/analyser
// are owned by the attempt (closed in its cleanups) and rebuilt only when
// the audio track changes, so no context survives teardown and a new round
// never measures the previous attempt's track.
window.__outgoingAudioPeak = async () => {
  const a = current;
  if (!a || !a.stream) return -1;
  const at = a.stream.getAudioTracks()[0];
  if (!at || at.readyState === 'ended') return -1;
  try {
    if (!a.__ogAn || a.__ogTrack !== at || a.__ogAC.state === 'closed') {
      if (a.__ogAC) { try { a.__ogAC.close(); } catch (_) {} }
      a.__ogAC = new AudioContext();
      a.__ogAn = a.__ogAC.createAnalyser();
      a.__ogAC.createMediaStreamSource(new MediaStream([at])).connect(a.__ogAn);
      a.__ogTrack = at;
      a.cleanups.push(() => {
        if (a.__ogAC) { try { a.__ogAC.close(); } catch (_) {} }
        a.__ogAC = null; a.__ogAn = null; a.__ogTrack = null;
      });
    }
    const buf = new Float32Array(a.__ogAn.fftSize);
    a.__ogAn.getFloatTimeDomainData(buf);
    let peak = 0;
    for (const v of buf) peak = Math.max(peak, Math.abs(v));
    return peak;
  } catch (_) { return -1; }
};


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

function attachMse() {
  if (!(window.Hls && Hls.isSupported())) return false;
  hls = new Hls({ lowLatencyMode: true, backBufferLength: 30 });
  hls.on(Hls.Events.MANIFEST_PARSED, () => {
    els.hls.textContent = `hls.js ${Hls.version} manifest OK (MSE)`;
    log(`hls.js ${Hls.version}: manifest 파싱 성공 (MSE 경로)`, 'ok');
  });
  hls.on(Hls.Events.ERROR, (_, data) => {
    if (data.fatal) { els.hls.textContent = `hls.js 오류: ${data.details}`; log(`hls.js 치명 오류: ${data.details}`, 'err'); }
  });
  hls.loadSource(HLS_URL);
  hls.attachMedia(video);
  return true;
}

// MSE를 쓸 수 없는 환경에서만 native HLS를 시도한다(Chrome은 실제 미지원).
let playerMode = 'none';      // 'mse' | 'native' | 'none'
let fallbackAttempted = false;
function setPlayerMode(mode, fallback) {
  playerMode = mode;
  updateState({ playerMode: mode, fallbackAttempted: Boolean(fallback) });
}

async function attachPlayer() {
  if (attachMse()) { setPlayerMode('mse'); return; }
  if (video.canPlayType('application/vnd.apple.mpegurl')) {
    setPlayerMode('native');
    video.src = HLS_URL;
    els.hls.textContent = 'native HLS';
    log('MSE 미지원 — native HLS 경로 시도', 'warn');
    video.addEventListener('error', () => {
      const e = video.error ? `${video.error.code} ${video.error.message}` : 'unknown';
      log(`native HLS 요소 오류(지연 실패 포함): ${e}`, 'err');
      tryFallbackToMse(`element error ${e}`);
    });
    return;
  }
  setPlayerMode('none');
  els.hls.textContent = 'HLS 재생 불가';
  log('HLS 재생 불가: MSE도 native HLS도 사용할 수 없음', 'err');
}

// native 경로가 실제로 실패하면 MSE로 1회만 폴백한다. MSE조차 불가능하면
// 정확한 오류로 종료한다(자동재생 정책으로 오인하지 않음).
async function tryFallbackToMse(reason) {
  if (playerMode !== 'native' || fallbackAttempted) return;
  fallbackAttempted = true;
  log(`native HLS 실패(${reason}) — hls.js/MSE 폴백 시도`, 'warn');
  try { video.pause(); } catch (_) {}
  video.removeAttribute('src');
  video.load();
  if (attachMse()) {
    setPlayerMode('mse', true);
    try { await video.play(); log('폴백 재생 시작 (MSE)', 'ok'); }
    catch (e) { log(`폴백 재생 거부: ${e.name}`, 'warn'); }
  } else {
    setPlayerMode('none', true);
    els.hls.textContent = `재생 불가 (native 실패: ${reason})`;
    log(`native 실패(${reason}) + MSE 불가 — 재생 불가`, 'err');
  }
}

playBtn.addEventListener('click', async () => {
  if (!hls && !video.src) await attachPlayer();
  try {
    await video.play();
    log('재생 시작', 'ok');
  } catch (e) {
    log(`재생 실패: ${e.name}${e.message ? ' — ' + e.message : ''}`, 'warn');
    if (playerMode === 'native' && (e.name === 'NotSupportedError' || video.error)) {
      await tryFallbackToMse(e.name);
    }
  }
});
document.getElementById('btn-sound').addEventListener('click', async () => {
  video.muted = false;
  try { await video.play(); log('소리 켜고 재생 시도', 'ok'); } catch (e) { log(`재생 거부: ${e.name}`, 'warn'); }
});



// Playback evidence probe: currentTime, decoded frames, best-effort audio
// level. captureStream + analyser are created ONCE per player source and
// reused (a freshly created analyser reads zeros); rebuilt only when the
// element's source changes (e.g. native→MSE fallback) and disposed then.
window.__playbackProbe = async () => {
  const q = video.getVideoPlaybackQuality ? video.getVideoPlaybackQuality() : { totalVideoFrames: 0 };
  let audioLevel = -1;
  try {
    const srcRef = video.currentSrc || video.src || '';
    if (window.__pbAn && (window.__pbSrcRef !== srcRef || window.__pbAC.state === 'closed')) {
      window.__disposePlaybackObserver();
    }
    if (!window.__pbAn && video.captureStream) {
      const stream = video.captureStream();
      const at = stream && stream.getAudioTracks ? stream.getAudioTracks()[0] : null;
      if (at) {
        window.__pbStream = stream;
        window.__pbAC = new AudioContext();
        window.__pbAn = window.__pbAC.createAnalyser();
        window.__pbAC.createMediaStreamSource(new MediaStream([at])).connect(window.__pbAn);
        window.__pbSrcRef = srcRef;
      }
    }
    if (window.__pbAn) {
      const buf = new Uint8Array(window.__pbAn.frequencyBinCount);
      window.__pbAn.getByteFrequencyData(buf);
      audioLevel = buf.reduce((x, y) => x + y, 0);
    }
  } catch (_) { /* best effort */ }
  return { currentTime: video.currentTime, frames: q.totalVideoFrames, audioLevel };
};
window.__disposePlaybackObserver = () => {
  if (window.__pbStream) { try { window.__pbStream.getTracks().forEach(t => t.stop()); } catch (_) {} }
  if (window.__pbAC) { try { window.__pbAC.close(); } catch (_) {} }
  window.__pbStream = null; window.__pbAC = null; window.__pbAn = null; window.__pbTrack = null; window.__pbSrcRef = null;
};
window.addEventListener('pagehide', window.__disposePlaybackObserver);


// LL-HLS manifest evidence: master → media playlist, part markers.
async function manifestProbe() {
  try {
    const master = await (await fetch(HLS_URL, { cache: 'no-store' })).text();
    const variantUrl = new URL((master.match(/^([^#\s][^\s]*)$/m) || [])[1] || '', HLS_BASE_ABSOLUTE).toString();
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

// Publisher outbound stats + publisher AudioContext state for automation.
// The click gesture is what allows the AudioContext to actually run; the
// synthetic audio path is only verified as 'running'.
setInterval(async () => {
  if (current && !current.cancelled && state.lifecycle === 'publishing' && current.pc) {
    try {
      const stats = await current.pc.getStats();
      stats.forEach(r => {
        if (r.type === 'outbound-rtp' && r.kind === 'video' && typeof r.bytesSent === 'number') {
          updateState({ outbound: { bytes: r.bytesSent, frames: r.framesEncoded ?? r.framesSent ?? 0 } });
        }
      });
      updateState({ publisherAC: current.ac ? current.ac.state : null });
    } catch (_) {}
  }
}, 1000);

// Automation entry: ?mode=synth&autostart=1 (synthetic only; no devices).
const params = new URLSearchParams(location.search);
if (params.get('mode') === 'synth' && params.get('autostart') === '1') {
  log('autostart=1: 합성 모드 자동 시작');
  setTimeout(() => startPublish(a => syntheticStream(a), 'synthetic').then(() => setTimeout(manifestProbe, 6000)), 1000);
}
