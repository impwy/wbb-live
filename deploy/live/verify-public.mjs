// Public end-to-end check of a deployed WBB stack, run from another network.
// Publisher: Chrome with a synthetic camera+microphone (not a physical device)
// signs in with the evaluation login, creates a broadcast and publishes over
// WHIP. Viewer: a separate anonymous Chrome plays the LL-HLS stream. The
// script then stops the broadcast and checks that playback is closed.
// Usage: node verify-public.mjs [https://live.wombatburrow.com] [holdSeconds]
// Prints no tokens, SDP or credentials.
import { chromium } from '../../experiments/media-single-rendition/node_modules/playwright/index.mjs';

const base = (process.argv[2] || 'https://live.wombatburrow.com').replace(/\/$/, '');
const holdSeconds = Number(process.argv[3] || 45);
const log = (step, data = {}) => console.log(JSON.stringify({ t: new Date().toISOString(), step, ...data }));
const sleep = ms => new Promise(r => setTimeout(r, ms));

const flags = ['--use-fake-device-for-media-stream', '--use-fake-ui-for-media-stream', '--autoplay-policy=no-user-gesture-required'];
const pubBrowser = await chromium.launch({ args: flags });
// Playwright's Chromium lacks H.264/AAC playback; the viewer uses Google Chrome.
const viewBrowser = await chromium.launch({ args: flags, channel: 'chrome' });
let exitCode = 1;
try {
  const pub = await (await pubBrowser.newContext()).newPage();
  await pub.goto(base + '/manifest.json', { waitUntil: 'domcontentloaded' }); // not the app: it would rotate the session

  const started = await pub.evaluate(async () => {
    const api = (path, opts = {}) => fetch('/api/v1' + path, { credentials: 'include', ...opts });
    const login = await api('/auth/evaluation-login', { method: 'POST' });
    if (login.status !== 200) return { error: 'evaluation-login ' + login.status };
    const token = (await login.json()).accessToken;
    const auth = { Authorization: 'Bearer ' + token };
    const created = await api('/broadcasts', { method: 'POST', headers: { ...auth, 'Content-Type': 'application/json' },
      body: JSON.stringify({ title: '공개 자동 점검', description: 'verify-public.mjs 합성 카메라 점검' }) });
    if (created.status !== 201) return { error: 'create ' + created.status };
    const broadcast = (await created.json()).broadcast;
    const ice = await (await api('/media/ice-servers', { headers: auth })).json();
    const prep = await api(`/broadcasts/${broadcast.id}/sessions`, { method: 'POST',
      headers: { ...auth, 'Content-Type': 'application/json', 'Idempotency-Key': crypto.randomUUID() },
      body: JSON.stringify({ source: 'camera' }) });
    if (prep.status !== 202) return { error: 'prepare ' + prep.status };
    const prepared = await prep.json();
    const stream = await navigator.mediaDevices.getUserMedia({ video: { width: 1280, height: 720 }, audio: true });
    const pc = new RTCPeerConnection({ iceServers: ice.iceServers });
    window.__pc = pc;
    const h264 = RTCRtpSender.getCapabilities('video').codecs.filter(c => c.mimeType.toLowerCase() === 'video/h264');
    pc.addTransceiver(stream.getVideoTracks()[0], { direction: 'sendonly' }).setCodecPreferences(h264);
    pc.addTransceiver(stream.getAudioTracks()[0], { direction: 'sendonly' });
    await pc.setLocalDescription(await pc.createOffer());
    await new Promise(r => { const t = setTimeout(r, 5000); pc.onicegatheringstatechange = () => { if (pc.iceGatheringState === 'complete') { clearTimeout(t); r(); } }; });
    const whip = await fetch(prepared.publishGrant.whipUrl, { method: 'POST',
      headers: { Authorization: 'Bearer ' + prepared.publishGrant.token, 'Content-Type': 'application/sdp' }, body: pc.localDescription.sdp });
    if (whip.status !== 201) return { error: 'whip ' + whip.status };
    const location = whip.headers.get('location');
    const etag = whip.headers.get('etag');
    await pc.setRemoteDescription({ type: 'answer', sdp: await whip.text() });
    window.__ctl = { token, broadcastId: broadcast.id, sessionId: prepared.session.id, location, etag, version: broadcast.version };
    return { broadcastId: broadcast.id, sessionId: prepared.session.id, iceServers: ice.iceServers.length };
  });
  if (started.error) throw new Error(started.error);
  log('published', started);

  const state = () => pub.evaluate(async id => {
    const r = await fetch(`/api/v1/broadcasts/${id}/sessions/current`);
    const s = (await r.json()).session;
    const stats = await window.__pc.getStats();
    let pair = null, sent = 0;
    stats.forEach(v => {
      if (v.type === 'candidate-pair' && v.nominated && v.state === 'succeeded') {
        const local = stats.get(v.localCandidateId);
        pair = local ? `${local.candidateType}/${local.protocol}` : 'unknown';
      }
      if (v.type === 'outbound-rtp' && v.kind === 'video') sent = v.framesEncoded;
    });
    return { state: s?.state, endReason: s?.endReason, ice: window.__pc.iceConnectionState, pair, framesEncoded: sent };
  }, started.broadcastId);

  let live = false;
  for (let i = 0; i < 40 && !live; i++) {
    const s = await state();
    if (s.state === 'live') { live = true; log('live', s); }
    else await sleep(1000);
  }
  if (!live) throw new Error('session never became live: ' + JSON.stringify(await state()));

  const view = await (await viewBrowser.newContext()).newPage();
  await view.goto(base + '/manifest.json', { waitUntil: 'domcontentloaded' });
  await view.addScriptTag({ url: base + '/vendor/hls.min.js' });
  const playback = await view.evaluate(async id => {
    for (let i = 0; i < 20; i++) {
      const r = await fetch(`/api/v1/broadcasts/${id}/playback`);
      if (r.status === 200) {
        const p = await r.json();
        const video = document.createElement('video');
        video.muted = true; video.playsInline = true; document.body.appendChild(video);
        const hls = new Hls({ lowLatencyMode: true });
        hls.loadSource(p.masterUrl); hls.attachMedia(video);
        window.__video = video;
        video.play().catch(() => {});
        return { status: 200 };
      }
      await new Promise(res => setTimeout(res, 1000));
    }
    return { status: 'not-ready' };
  }, started.broadcastId);
  log('viewer-started', playback);

  let lastTime = 0, stalls = 0;
  for (let i = 0; i < holdSeconds; i += 5) {
    await sleep(5000);
    const s = await state();
    const v = await view.evaluate(() => ({ time: window.__video?.currentTime || 0, width: window.__video?.videoWidth || 0, height: window.__video?.videoHeight || 0 }));
    if (v.time <= lastTime) stalls++;
    lastTime = v.time;
    log('hold', { ...s, viewerTime: Number(v.time.toFixed(1)), viewerSize: `${v.width}x${v.height}` });
    if (s.state !== 'live') throw new Error('session left live during hold');
  }
  if (lastTime <= 0) throw new Error('viewer never played');

  const stop = await pub.evaluate(async () => {
    const c = window.__ctl;
    const r = await fetch(c.location, { method: 'DELETE', headers: { Authorization: 'Bearer ' + c.token, ...(c.etag ? { 'If-Match': c.etag } : {}) } });
    window.__pc.close();
    return r.status;
  });
  log('stop-requested', { whipDelete: stop });
  let ended = null;
  for (let i = 0; i < 30 && !ended; i++) {
    const s = await state().catch(() => null);
    if (s?.state === 'ended') ended = s; else await sleep(1000);
  }
  if (!ended) throw new Error('session did not end');
  const after = await view.evaluate(async id => (await fetch(`/api/v1/broadcasts/${id}/playback`)).status, started.broadcastId);
  log('ended', { endReason: ended.endReason, playbackAfterEnd: after });

  // Replay (optional feature): the server registers the recording of the
  // ended session within its scan interval; fetch the first bytes by id.
  const replay = await view.evaluate(async id => {
    for (let i = 0; i < 40; i++) {
      const list = await (await fetch(`/api/v1/broadcasts/${id}/recordings`)).json();
      if (list.items?.length) {
        const r = list.items[0];
        const media = await fetch(`/api/v1/recordings/${r.id}/media`, { headers: { Range: 'bytes=0-1023' } });
        return { recordings: list.items.length, durationSeconds: r.durationSeconds, mediaStatus: media.status, contentType: media.headers.get('content-type') };
      }
      await new Promise(res => setTimeout(res, 2000));
    }
    return { recordings: 0 };
  }, started.broadcastId);
  log('replay', replay);

  // KEEP=1 leaves the check broadcast for manual inspection.
  const cleanup = process.env.KEEP === '1' ? 'kept' : await pub.evaluate(async () => {
    const c = window.__ctl;
    const b = await (await fetch(`/api/v1/broadcasts/${c.broadcastId}`)).json();
    return (await fetch(`/api/v1/broadcasts/${c.broadcastId}?version=${b.broadcast.version}`, { method: 'DELETE', headers: { Authorization: 'Bearer ' + c.token } })).status;
  });
  log('cleanup', { deleteBroadcast: cleanup, viewerStalls: stalls });
  exitCode = after === 200 ? 1 : 0;
} catch (error) {
  log('failed', { error: String(error.message || error) });
} finally {
  await pubBrowser.close();
  await viewBrowser.close();
  process.exit(exitCode);
}
