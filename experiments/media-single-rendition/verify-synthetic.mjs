// Automated synthetic verification for the WBB media spike (ticket 02).
//
// Synthetic media ONLY (canvas + oscillator) — no real screen/mic/camera.
// Fault injection is EXTERNAL to the app under test:
//   - delayed WHIP POST / broken SDP answer  -> Playwright network routes
//   - delayed getUserMedia                   -> addInitScript promise wrapper
//   - video track 'ended'                    -> real event dispatch on the track
// Covers: WHIP POST/Location/ETag, ICE connected, PATCH 204, LL-HLS parts +
// progression + blocking reload (window-aware) + direct part GET, real
// playback evidence, server-side PCM non-silence, ffprobe JSON codec/
// dimension checks vs source, DELETE teardown with OBSERVED cleanup, MediaMTX
// webrtcsession disappearance, start-stop-start, and late-response races.
import { chromium } from 'playwright';
import { execSync, execFileSync } from 'node:child_process';

const sleep = ms => new Promise(r => setTimeout(r, ms));
const results = [];
function check(name, ok, detail = '') {
  results.push({ name, ok, detail });
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${name}${detail ? ' — ' + detail : ''}`);
}
function skip(name, detail = '') {
  results.push({ name, ok: null, detail });
  console.log(`SKIP  ${name}${detail ? ' — ' + detail : ''}`);
}

const BASE = 'http://127.0.0.1:8090';
const WHIP_FILTER = '**/spike/source/whip*';
const spikeDir = new URL('.', import.meta.url).pathname;

// --- transcoder evidence helpers -------------------------------------------
// The transcoder container restarts ffmpeg in a retry loop, so identity is
// (container ID + PID + /proc starttime): the ORIGINAL encoder must vanish;
// a NEW retry instance with a different starttime is allowed.
// NOTE: execFileSync with an argv array — never a host-shell string — so
// shell metacharacters like awk '{print $22}' reach the container intact.
function execIn(container, cmd) {
  return execFileSync('docker', ['compose', 'exec', '-T', container, 'sh', '-c', cmd], {
    cwd: spikeDir, encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'pipe'],
  }).trim();
}
function transcoderContainerId() {
  return execFileSync('docker', ['compose', 'ps', '-q', 'transcoder'], {
    cwd: spikeDir, encoding: 'utf8',
  }).trim();
}
// Returns { pid: null } when no ffmpeg is running (distinct from a query
// error) or { pid, starttime } with a validated positive numeric starttime,
// or { pid, error } when the identity could not be established.
function ffmpegIdentity() {
  let out;
  try {
    out = execIn('transcoder', 'pidof ffmpeg 2>/dev/null || true');
  } catch (e) {
    return { pid: null, error: `pidof failed: ${String(e).slice(0, 120)}` };
  }
  const pid = out.split(/\s+/)[0];
  if (!pid) return { pid: null };
  let raw;
  try {
    raw = execIn('transcoder', `cat /proc/${pid}/stat 2>/dev/null | awk '{print $22}'`);
  } catch (e) {
    return { pid, error: `stat failed: ${String(e).slice(0, 120)}` };
  }
  const starttime = Number(raw.trim());
  if (!Number.isFinite(starttime) || starttime <= 0) {
    return { pid, error: `bad starttime: ${JSON.stringify(raw)}` };
  }
  return { pid, starttime };
}

// --- MediaMTX API (v1.21.3: /v3/webrtcsessions/list, NOT /v3/whipsessions) --
async function api(path) {
  const r = await fetch(`http://127.0.0.1:9990${path}`);
  if (!r.ok) throw new Error(`api ${path} -> ${r.status}`);
  return r.json();
}
async function pathState(name) {
  const list = await api('/v3/paths/list');
  const it = list.items.find(i => i.name === name);
  return it ? { ready: it.ready, sourceType: it.source?.type ?? null } : null;
}
async function webrtcSessionCount() {
  const list = await api('/v3/webrtcsessions/list');
  return list.itemCount;
}

// --- HLS helpers -------------------------------------------------------------
async function mediaPlaylist() {
  const master = await (await fetch('http://127.0.0.1:8888/spike/720p/index.m3u8', { cache: 'no-store' })).text();
  const variantRel = (master.match(/^([^#\s][^\s]*)$/m) || [])[1];
  if (!variantRel) throw new Error('master playlist has no variant');
  const url = new URL(variantRel, 'http://127.0.0.1:8888/spike/720p/index.m3u8');
  const text = await (await fetch(url, { cache: 'no-store' })).text();
  return { url, text };
}
function parseMsn(text) {
  return Number((text.match(/#EXT-X-MEDIA-SEQUENCE:(\d+)/) || [])[1] || 0);
}
function segCount(text) {
  return (text.match(/#EXTINF/g) || []).length;
}
function partUris(text) {
  return [...text.matchAll(/^#EXT-X-PART:.*URI="([^"]+)"/gm)].map(m => m[1]);
}
// Does the playlist actually CONTAIN the requested (msn, part)?
//  - msn within the completed window [start, start+segCount)
//  - or msn == start+segCount (the in-progress segment): the PART entries
//    trailing after the last completed segment's URI count as its parts and
//    must exceed the requested part index. Zero wait time is fine when the
//    part already exists; the server holds the request otherwise.
function includesRequested(text, msn, part) {
  const start = parseMsn(text);
  const segs = segCount(text);
  if (msn >= start && msn < start + segs) return true;
  if (msn === start + segs) {
    const lines = text.split('\n');
    let lastUriIdx = -1;
    for (let i = lines.length - 1; i >= 0; i--) {
      if (lines[i] && !lines[i].startsWith('#')) { lastUriIdx = i; break; }
    }
    let trailingParts = 0;
    for (let i = lastUriIdx + 1; i < lines.length; i++) {
      if (lines[i].startsWith('#EXT-X-PART:')) trailingParts++;
    }
    return trailingParts > part;
  }
  return false;
}

// --- page state helpers ------------------------------------------------------
// Node-side polling: predicates stay plain Node functions (never serialized
// into the page), and failures keep their real cause (last state included).
async function waitForState(page, pred, timeout = 15000, label = 'state') {
  const deadline = Date.now() + timeout;
  let last;
  for (;;) {
    last = await st(page).catch(e => ({ fetchError: String(e) }));
    if (last && pred(last)) return;
    if (Date.now() > deadline) {
      throw new Error(`timeout waiting for ${label}; last=${JSON.stringify(last)}`);
    }
    await sleep(250);
  }
}
const st = page => page.evaluate(() => window.__spikeState);

async function startSynthetic(page) {
  await page.click('#btn-synth');
  await waitForState(page, s => s.lifecycle === 'publishing', 20000, 'publishing');
  await waitForState(page, s => ['connected', 'completed'].includes(s.iceState), 15000, 'ICE connected');
}
async function stopAndWait(page) {
  await page.click('#btn-stop');
  await waitForState(page, s => s.lifecycle === 'idle', 10000, 'idle after stop');
}

// ---------------------------------------------------------------------------
const browser = await chromium.launch({
  channel: 'chrome', // bundled Chromium lacks H.264; system Chrome has it
  args: ['--use-fake-ui-for-media-stream', '--use-fake-device-for-media-stream'],
});

// Codec preflight: the runner itself must be able to decode H.264/AAC.
{
  const p = await browser.newPage();
  const caps = await p.evaluate(() => ({
    h264: HTMLVideoElement.prototype.canPlayType.call(document.createElement('video'), 'video/mp4; codecs="avc1.42E01E"'),
    aac: HTMLVideoElement.prototype.canPlayType.call(document.createElement('video'), 'audio/mp4; codecs="mp4a.40.2"'),
  }));
  await p.close();
  check('codec preflight H.264', caps.h264 !== '', caps.h264 || 'empty');
  check('codec preflight AAC', caps.aac !== '', caps.aac || 'empty');
  if (!caps.h264 || !caps.aac) {
    console.error('시스템 Chrome(channel:chrome)이 H.264/AAC를 지원하지 않음 — 검증 중단');
    await browser.close().catch(() => {});
    process.exit(1);
  }
}

try {
  // =========================================================================
  // Scenario 1: happy path (synthetic)
  // =========================================================================
  {
    const ctx = await browser.newContext();
    const page = await ctx.newPage();
    try {
      // Real CLICK start (not autostart): the user gesture lets the
      // publisher's AudioContext run, which the synthetic audio verification
      // depends on.
      await page.goto(`${BASE}/`);
      await page.click('#btn-synth');
      await waitForState(page, s => s.lifecycle === 'publishing', 20000, 'publishing');
      await waitForState(page, s => ['connected', 'completed'].includes(s.iceState), 15000, 'ICE connected');
      // The stats interval samples the publisher AudioContext state once per
      // second — wait for the sample before asserting it.
      await waitForState(page, s => s.publisherAC !== undefined && s.publisherAC !== null, 5000, 'publisherAC sampled');
      check('S1 publisher AudioContext running', (await st(page)).publisherAC === 'running',
        `state=${(await st(page)).publisherAC}`);

      let s = await st(page);
      check('S1 WHIP POST 201', s.whipPost === 201, `status=${s.whipPost}`);
      check('S1 Location + ETag', Boolean(s.location && s.etag), s.location || 'missing');
      check('S1 ICE connected', ['connected', 'completed'].includes(s.iceState), s.iceState);

      // PATCH must be exactly 204.
      await waitForState(page, x => typeof x.patchStatus === 'number', 10000, 'PATCH');
      s = await st(page);
      check('S1 PATCH exactly 204', s.patchStatus === 204, `status=${s.patchStatus}`);

      // Publisher outbound traffic increasing (real encoder stats).
      await sleep(2500);
      const o1 = (await st(page)).outbound;
      await sleep(2500);
      const o2 = (await st(page)).outbound;
      check('S1 outbound bytes increasing', o1 && o2 && o2.bytes > o1.bytes, `${o1?.bytes} → ${o2?.bytes}`);
      check('S1 outbound frames increasing', o1 && o2 && o2.frames > o1.frames, `${o1?.frames} → ${o2?.frames}`);

      // Wait for the transcoder + HLS muxer.
      await sleep(12000);

      // LL-HLS: parts present, progressing, direct part GET, blocking reload.
      const m1 = await mediaPlaylist();
      check('S1 LL-HLS parts', m1.text.includes('EXT-X-PART-INF') && partUris(m1.text).length > 0);
      check('S1 CAN-BLOCK-RELOAD', m1.text.includes('CAN-BLOCK-RELOAD=YES'));

      // Direct part GET: fetch an actual part segment file.
      {
        const partRel = partUris(m1.text).at(-1);
        const partUrl = new URL(partRel, m1.url);
        const pr = await fetch(partUrl, { cache: 'no-store' });
        const buf = await pr.arrayBuffer();
        check('S1 direct part GET', pr.status === 200 && buf.byteLength > 1000,
          `HTTP ${pr.status}, ${buf.byteLength}B`);
      }

      await sleep(1600);
      const m2 = await mediaPlaylist();
      const progressed = parseMsn(m2.text) > parseMsn(m1.text) ||
        partUris(m2.text).at(-1) !== partUris(m1.text).at(-1);
      check('S1 parts progressing', progressed, `msn ${parseMsn(m1.text)}→${parseMsn(m2.text)}`);

      // Blocking reload: request the next segment/part and require the
      // RESPONSE to actually contain it (window coverage or trailing parts
      // of the in-progress segment). Fast answers are fine — the part may
      // already exist; the server holds the request until it does.
      {
        const baseline = await mediaPlaylist();
        const nextMsn = parseMsn(baseline.text) + segCount(baseline.text);
        const u = new URL(baseline.url);
        u.searchParams.set('_HLS_msn', String(nextMsn));
        u.searchParams.set('_HLS_part', '0');
        const ctl = new AbortController();
        const t = setTimeout(() => ctl.abort(), 8000);
        try {
          const r = await fetch(u, { signal: ctl.signal, cache: 'no-store' });
          clearTimeout(t);
          const text = await r.text();
          const included = includesRequested(text, nextMsn, 0);
          check('S1 blocking reload', r.status === 200 && included,
            `HTTP ${r.status}, window ${parseMsn(text)}..${parseMsn(text) + segCount(text) - 1}, msn ${nextMsn} included=${included}`);
        } catch (e) {
          clearTimeout(t);
          check('S1 blocking reload', false, String(e));
        }
      }

      // Real playback evidence (video starts muted to satisfy autoplay; the
      // 소리 켜기 button unmutes for humans).
      await page.click('#btn-play');
      try {
        await page.waitForFunction(() => {
          const v = document.getElementById('playback');
          return v && v.readyState >= 2 && !v.paused && v.currentTime > 0;
        }, null, { timeout: 12000 });
      } catch { /* probes below will fail with the diagnosis */ }
      const p1 = await page.evaluate(() => window.__playbackProbe());
      await sleep(2500);
      const p2 = await page.evaluate(() => window.__playbackProbe());
      check('S1 playback currentTime increasing', p2.currentTime > p1.currentTime && p2.currentTime > 0, `${p1.currentTime.toFixed(2)}→${p2.currentTime.toFixed(2)}`);
      check('S1 decoded frames increasing', p2.frames > p1.frames && p2.frames > 0, `${p1.frames}→${p2.frames}`);
      // Audio evidence needs an UNMUTED element: muted captureStream is silent.
      await page.click('#btn-sound');
      await sleep(1500);
      const pA = await page.evaluate(() => window.__playbackProbe());
      if (pA.audioLevel === -1) {
        skip('S1 browser audio level', 'analyser/audio track unavailable');
      } else {
        check('S1 playback audio non-silent', pA.audioLevel > 0, `level=${pA.audioLevel}`);
      }

      // Server-side audio non-silence: decode 3s of AAC to PCM and inspect.
      try {
        const pcm = execSync(
          `docker compose exec -T mediamtx sh -c ` +
          `"ffmpeg -v error -i http://127.0.0.1:8888/spike/720p/index.m3u8 -map 0:a:0 -t 3 -f s16le -"`,
          { cwd: spikeDir, maxBuffer: 64 * 1024 * 1024 },
        );
        let peak = 0;
        for (let i = 0; i + 1 < pcm.length; i += 2) {
          const v = Math.abs(pcm.readInt16LE(i));
          if (v > peak) peak = v;
        }
        check('S1 server-side PCM non-silent', pcm.length > 1000 && peak > 0, `${pcm.length}B, peak=${peak}`);
      } catch (e) {
        check('S1 server-side PCM non-silent', false, String(e).slice(0, 160));
      }

      // ffprobe JSON: positive dims, no upscale vs source, h264/aac, aspect kept.
      const probe = JSON.parse(execSync(
        `docker compose exec -T mediamtx ffprobe -v error -of json ` +
        `-show_streams http://127.0.0.1:8888/spike/720p/index.m3u8`,
        { cwd: spikeDir, encoding: 'utf8', maxBuffer: 10 * 1024 * 1024 },
      ));
      const v = probe.streams.find(x => x.codec_type === 'video');
      const a = probe.streams.find(x => x.codec_type === 'audio');
      check('S1 codec h264/aac', v?.codec_name === 'h264' && a?.codec_name === 'aac', `${v?.codec_name}/${a?.codec_name}`);
      const src = (await st(page)).publishedSize;
      check('S1 dims positive', (v?.width ?? 0) > 0 && (v?.height ?? 0) > 0, `${v?.width}x${v?.height}`);
      check('S1 no upscale vs source', v.width <= src.w && v.height <= src.h, `${v.width}x${v.height} <= ${src.w}x${src.h}`);
      const ar = Math.abs(v.width / v.height - src.w / src.h);
      check('S1 aspect preserved', ar < 0.05, `Δ=${ar.toFixed(3)}`);

      // Stop evidence — captured BEFORE stopping (a missing encoder/output up
      // front can never count as a pass):
      //   1) the output path is ready and parts are progressing,
      //   2) the CURRENT ffmpeg encoder identity (container+PID+starttime),
      //   3) the live variant (media playlist) URL for the post-stop watch.
      const outBefore = await pathState('spike/720p');
      check('S1 output ready before stop', outBefore?.ready === true, JSON.stringify(outBefore));
      const encoderBefore = ffmpegIdentity();
      check('S1 encoder running before stop',
        Boolean(encoderBefore?.pid) && Number.isFinite(encoderBefore.starttime) && !encoderBefore.error,
        encoderBefore.error ? encoderBefore.error : `pid=${encoderBefore.pid} starttime=${encoderBefore.starttime}`);
      const containerId = transcoderContainerId();
      const liveVariant = await mediaPlaylist();

      // Stop: DELETE + OBSERVED cleanup + server-side disappearance.
      // NOTE: local PC/tracks are released BEFORE the network DELETE (device
      // safety first). MediaMTX terminates the WHIP session when the peer
      // disconnects, so the DELETE legitimately returns 404 (already gone)
      // for sessions that had connected; 2xx happens when it never connected.
      // The hard invariants are: no orphan sessions and the source is gone.
      await stopAndWait(page);
      s = await st(page);
      const del = s.deleteStatus;
      check('S1 DELETE 2xx-or-already-gone(404)', (del >= 200 && del < 300) || del === 404, `status=${del}`);
      check('S1 tracks ended (observed)', s.tracksStopped === true);
      check('S1 PC closed (observed)', s.pcClosed === true);
      check('S1 AudioContext closed (observed)', s.acClosed === true);
      await sleep(500);
      const ps = await pathState('spike/source');
      check('S1 source gone server-side', ps && ps.ready === false && ps.sourceType === null, JSON.stringify(ps));
      check('S1 WebRTC sessions empty', (await webrtcSessionCount()) === 0, `count=${await webrtcSessionCount()}`);

      // Output path gone via the API (normal response; a missing static path
      // entry is also acceptable).
      await sleep(2500); // let the muxer wind down and the retry loop settle
      const outAfter = await pathState('spike/720p');
      check('S1 output gone server-side', outAfter === null || (outAfter.ready === false && outAfter.sourceType === null),
        JSON.stringify(outAfter));

      // The ORIGINAL encoder (same container, same PID+starttime) must be
      // gone; a retry-loop replacement with a new starttime is allowed. A
      // QUERY ERROR is not termination evidence.
      const sameContainer = transcoderContainerId() === containerId;
      const encoderAfter = ffmpegIdentity();
      let originalGone, goneDetail;
      if (encoderBefore?.error || !encoderBefore?.pid) {
        originalGone = false; goneDetail = 'before-identity unavailable';
      } else if (encoderAfter?.error) {
        originalGone = false; goneDetail = encoderAfter.error;
      } else if (!encoderAfter?.pid) {
        originalGone = true; goneDetail = 'no ffmpeg after stop';
      } else {
        originalGone = !(encoderAfter.pid === encoderBefore.pid && encoderAfter.starttime === encoderBefore.starttime);
        goneDetail = `before=${encoderBefore.pid}@${encoderBefore.starttime} after=${encoderAfter.pid}@${encoderAfter.starttime}`;
      }
      check('S1 original encoder terminated', sameContainer && originalGone, goneDetail);

      // 6-second watch on the recorded variant URL (longer than the 2s retry
      // loop). EVERY round re-verifies all three termination facts:
      //   (a) the original encoder identity is absent (query error = fail),
      //   (b) the API answers normally and source/output stay inactive,
      //   (c) the variant fetch (explicit timeout) is 404/410 — which also
      //       keeps being polled for the FULL window — or a FIXED 200 whose
      //       MEDIA-SEQUENCE/PART parse validity holds with no progression.
      // Network hangs, 5xx, parse failures or a stopped stack are not
      // termination evidence.
      {
        const t0 = Date.now();
        let ok = true, detail = '';
        let lastPart = null, lastMsn = null;
        let samples = 0;
        while (Date.now() - t0 < 6000) {
          try {
            const id = ffmpegIdentity();
            if (id?.error) { ok = false; detail = `identity query error: ${id.error}`; break; }
            if (id?.pid && encoderBefore?.pid && id.pid === encoderBefore.pid && id.starttime === encoderBefore.starttime) {
              ok = false; detail = `original encoder present again: ${id.pid}@${id.starttime}`; break;
            }
          } catch (e) { ok = false; detail = `identity exec error: ${String(e).slice(0, 120)}`; break; }

          try {
            const src = await pathState('spike/source');
            const out = await pathState('spike/720p');
            const inactive = p => p === null || (p.ready === false && p.sourceType === null);
            if (!inactive(src) || !inactive(out)) {
              ok = false; detail = `re-activated: src=${JSON.stringify(src)} out=${JSON.stringify(out)}`; break;
            }
          } catch (e) { ok = false; detail = `api error: ${String(e).slice(0, 120)}`; break; }

          try {
            const ctl = new AbortController();
            const to = setTimeout(() => ctl.abort(), 4000);
            const r = await fetch(liveVariant.url, { cache: 'no-store', signal: ctl.signal });
            samples++;
            if (r.status === 404 || r.status === 410) {
              clearTimeout(to);
              // Path removed — still keep polling the full window.
              lastMsn = null;
            } else if (r.status !== 200) {
              clearTimeout(to);
              ok = false; detail = `HTTP ${r.status}`; break;
            } else {
              // The abort timer covers the BODY too: a stalled body read is a
              // failure, not termination evidence.
              const text = await r.text();
              clearTimeout(to);
              if (!text.includes('#EXTM3U')) { ok = false; detail = 'no EXTM3U'; break; }
              const msnMatch = text.match(/#EXT-X-MEDIA-SEQUENCE:(\d+)/);
              if (!msnMatch) { ok = false; detail = 'no MEDIA-SEQUENCE (refusing 0 default)'; break; }
              const msn = Number(msnMatch[1]);
              // A playlist that advertises PART-INF but carries no actual part
              // URIs is corrupt — never a pass.
              const parts = partUris(text);
              if (text.includes('EXT-X-PART-INF') && parts.length === 0) {
                ok = false; detail = 'PART-INF without PART entries'; break;
              }
              if (parts.some(u => !u || !/\.m4s($|\?)/.test(u))) {
                ok = false; detail = 'invalid PART URI'; break;
              }
              const part = parts.at(-1) ?? null;
              if (lastMsn !== null && (msn > lastMsn || part !== lastPart)) {
                ok = false; detail = `progression after stop: msn ${lastMsn}→${msn}, part ${lastPart}→${part}`; break;
              }
              lastMsn = msn; lastPart = part;
            }
          } catch (e) { ok = false; detail = `fetch error/timeout: ${String(e).slice(0, 120)}`; break; }
          await sleep(2000);
        }
        check('S1 terminated for full 6s window', ok && samples >= 2, detail || `samples=${samples}`);
      }
    } finally {
      await ctx.close();
    }
  }

  // =========================================================================
  // Scenario 2: start → stop → start
  // =========================================================================
  {
    const ctx = await browser.newContext();
    const page = await ctx.newPage();
    try {
      await page.goto(`${BASE}/`);
      await startSynthetic(page);
      await stopAndWait(page);
      check('S2 first DELETE 2xx-or-404', ((await st(page)).deleteStatus === 200 || (await st(page)).deleteStatus === 404));
      await startSynthetic(page);
      check('S2 restart reaches publishing', true);
      await stopAndWait(page);
      check('S2 second DELETE 2xx-or-404', ((await st(page)).deleteStatus === 200 || (await st(page)).deleteStatus === 404));
      check('S2 WebRTC sessions empty', (await webrtcSessionCount()) === 0);
    } finally {
      await ctx.close();
    }
  }

  // =========================================================================
  // Scenario 3: stop during a DELAYED POST, then a NEW broadcast — the late
  // 201 may only delete its own session; the new broadcast must survive.
  // =========================================================================
  {
    const ctx = await browser.newContext();
    const page = await ctx.newPage();
    try {
      let delayFirst = true;
      let releaseFirst;   // signals Node when the first POST's upstream response is secured
      let signalStopped;  // releases the held response only after the stop completed
      const firstPostUpstream = new Promise(r => { releaseFirst = r; });
      const stopCompleted = new Promise(r => { signalStopped = r; });
      await ctx.route(WHIP_FILTER, async route => {
        if (route.request().method() === 'POST' && delayFirst) {
          delayFirst = false;
          const resp = await route.fetch(); // upstream 201 in hand
          releaseFirst(resp.status());      // NOW the test stops the publisher
          await stopCompleted;              // hold the response across the stop
          return route.fulfill({ response: resp });
        }
        return route.continue();
      });

      await page.goto(`${BASE}/`);
      await page.click('#btn-synth');
      const upstreamStatus = await firstPostUpstream; // deterministic barrier
      check('S3 first POST reached upstream', upstreamStatus === 201, `status=${upstreamStatus}`);
      await page.click('#btn-stop'); // stop while the response is held back
      await waitForState(page, s => s.lifecycle === 'idle', 10000, 'idle after stop-during-post');
      check('S3 stop during POST → idle, no publish', (await st(page)).whipPost === null);

      // New broadcast starts while the old response is STILL HELD. Only after
      // B is publishing (with its location captured) do we release the late
      // 201, so "late response arrives during a new broadcast" is guaranteed.
      await startSynthetic(page);
      const newLocation = (await st(page)).location;
      check('S3 new broadcast publishing', true);
      signalStopped();   // release the late 201 NOW

      // The delayed 201 lands while B is live: only its own session is deleted.
      await waitForState(page, s => s.lateDeleteStatus !== null, 15000, 'late DELETE');
      const s = await st(page);
      check('S3 late session DELETE 2xx', s.lateDeleteStatus >= 200 && s.lateDeleteStatus < 300, `status=${s.lateDeleteStatus}`);
      check('S3 new broadcast survives late response', s.lifecycle === 'publishing' && s.location === newLocation);
      const ps = await pathState('spike/source');
      check('S3 server still publishing new session', ps?.ready === true, JSON.stringify(ps));

      await stopAndWait(page);
      check('S3 final DELETE 2xx-or-404', ((await st(page)).deleteStatus === 200 || (await st(page)).deleteStatus === 404));
      check('S3 WebRTC sessions empty', (await webrtcSessionCount()) === 0);
    } finally {
      await ctx.close();
    }
  }

  // =========================================================================
  // Scenario 4: broken SDP answer → full cleanup with resource DELETE
  // =========================================================================
  {
    const ctx = await browser.newContext();
    const page = await ctx.newPage();
    try {
      await ctx.route(WHIP_FILTER, async route => {
        if (route.request().method() !== 'POST') return route.continue();
        const resp = await route.fetch();
        return route.fulfill({ response: resp, body: 'v=0\r\no=- broken 1 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\n' });
      });
      await page.goto(`${BASE}/`);
      await page.click('#btn-synth');
      await waitForState(page, s => s.lifecycle === 'idle', 20000, 'idle after broken answer');
      const s = await st(page);
      check('S4 broken answer → cleaned up', s.pcClosed === true && s.tracksStopped === true && s.acClosed === true);
      check('S4 session DELETEd after answer failure', s.deleteStatus >= 200 && s.deleteStatus < 300, `status=${s.deleteStatus}`);
      check('S4 no orphan sessions', (await webrtcSessionCount()) === 0);
    } finally {
      await ctx.close();
    }
  }

  // =========================================================================
  // Scenario 5: video track 'ended' (external event dispatch) → auto teardown
  // =========================================================================
  {
    const ctx = await browser.newContext();
    const page = await ctx.newPage();
    try {
      await page.goto(`${BASE}/`);
      await startSynthetic(page);
      await page.evaluate(() => {
        const t = window.__spikeTracks().find(x => x.kind === 'video');
        t.dispatchEvent(new Event('ended'));
      });
      await waitForState(page, s => s.lifecycle === 'idle', 10000, 'idle after ended');
      const s = await st(page);
      check('S5 ended → teardown', s.tracksStopped === true && s.pcClosed === true);
      check('S5 ended → DELETE 2xx-or-404', s.deleteStatus === 200 || s.deleteStatus === 404, `status=${s.deleteStatus}`);
      check('S5 WebRTC sessions empty', (await webrtcSessionCount()) === 0);
    } finally {
      await ctx.close();
    }
  }

  // =========================================================================
  // Scenario 6: stop during a DELAYED capture (getUserMedia promise wrapper);
  // the late capture must be discarded and the app must stay usable.
  // =========================================================================
  {
    const ctx = await browser.newContext();
    const page = await ctx.newPage();
    await ctx.addInitScript(() => {
      window.__camStreams = [];
      const orig = navigator.mediaDevices.getUserMedia.bind(navigator.mediaDevices);
      let first = true;
      navigator.mediaDevices.getUserMedia = async constraints => {
        if (first) {
          first = false;
          await new Promise(r => setTimeout(r, 4000)); // REALLY delay the capture
        }
        const s = await orig(constraints);
        window.__camStreams.push(s);
        return s;
      };
    });
    try {
      await page.goto(`${BASE}/`);
      await page.click('#btn-camera');   // fake device (launch flags)
      // Wait until the attempt is really 'starting' — a stop pressed while
      // still idle is a no-op and the late capture would publish.
      await page.waitForFunction(() => window.__spikeState?.lifecycle === 'starting', null, { timeout: 8000 });
      // The stop must land while the capture is still PENDING (nothing
      // acquired yet) — otherwise this is not a stop-during-capture test.
      const pending = await page.evaluate(() => (window.__camStreams || []).length);
      check('S6 stop pressed before capture acquired', pending === 0, `camStreams=${pending}`);
      await page.click('#btn-stop');     // stop while the capture promise is pending
      await waitForState(page, s => s.lifecycle === 'idle', 10000, 'idle after stop-during-capture');
      const s = await st(page);
      check('S6 stop during capture → no publish', s.whipPost === null && s.lifecycle === 'idle');
      // Wait for the late capture to actually resolve, then require its
      // tracks to exist and all be ended (empty is not a pass).
      await page.waitForFunction(() => (window.__camStreams || []).length > 0, null, { timeout: 10000 });
      await sleep(500);
      const cam = await page.evaluate(() => window.__camStreams.flatMap(s => s.getTracks().map(t => t.readyState)));
      check('S6 late capture tracks ended', cam.length > 0 && cam.every(r => r === 'ended'), JSON.stringify(cam));
      check('S6 late capture discarded, no session', (await webrtcSessionCount()) === 0);
      // State machine stays healthy.
      await startSynthetic(page);
      check('S6 app usable after race (synthetic OK)', true);
      await stopAndWait(page);
    } finally {
      await ctx.close();
    }
  }

  // =========================================================================
  // Scenario 7: PC mode — stop while the getDisplayMedia promise is pending.
  // The late display resolve must NOT prompt for the mic and must end the
  // display tracks (regression for the review finding).
  // =========================================================================
  {
    const ctx = await browser.newContext();
    const page = await ctx.newPage();
    await ctx.addInitScript(() => {
      window.__mdCalls = { gdm: 0, gum: 0 };
      window.__gdmStreams = [];
      const md = navigator.mediaDevices;
      md.getDisplayMedia = async () => {
        window.__mdCalls.gdm++;
        await new Promise(r => setTimeout(r, 4000)); // stop happens in between
        const c = document.createElement('canvas');
        c.width = 640; c.height = 360;
        const g = c.getContext('2d');
        const iv = setInterval(() => g.fillRect(0, 0, 640, 360), 100);
        const s = c.captureStream(15);
        s.getVideoTracks()[0].addEventListener('ended', () => clearInterval(iv));
        window.__gdmStreams.push(s);
        return s;
      };
      const origGum = md.getUserMedia.bind(md);
      md.getUserMedia = async c => { window.__mdCalls.gum++; return origGum(c); };
    });
    try {
      await page.goto(`${BASE}/`);
      await page.click('#btn-screen');
      await sleep(300);
      await page.click('#btn-stop');
      await waitForState(page, s => s.lifecycle === 'idle', 10000, 'idle after stop-during-display');
      await sleep(4500); // the late display resolve lands now
      const obs = await page.evaluate(() => ({
        micPrompts: window.__mdCalls.gum,
        displayTracks: window.__gdmStreams.flatMap(s => s.getTracks().map(t => t.readyState)),
      }));
      check('S7 no mic prompt after stop', obs.micPrompts === 0, `gum=${obs.micPrompts}`);
      check('S7 late display tracks ended', obs.displayTracks.length > 0 && obs.displayTracks.every(r => r === 'ended'),
        JSON.stringify(obs.displayTracks));
      check('S7 no publish', (await st(page)).whipPost === null);
    } finally {
      await ctx.close();
    }
  }

  // =========================================================================
  // Scenario 8: PC mode — WebAudio mix creation failure must release display,
  // mic and every created AudioContext (no leaks).
  // =========================================================================
  {
    const ctx = await browser.newContext();
    const page = await ctx.newPage();
    await ctx.addInitScript(() => {
      window.__mdCalls = { gum: 0 };
      window.__gdmStreams = [];
      window.__gumStreams = [];
      window.__acStats = { created: 0, closed: 0 };
      const md = navigator.mediaDevices;
      md.getDisplayMedia = async () => {
        const c = document.createElement('canvas');
        c.width = 640; c.height = 360;
        const g = c.getContext('2d');
        const iv = setInterval(() => g.fillRect(0, 0, 640, 360), 100);
        const s = c.captureStream(15);
        s.getVideoTracks()[0].addEventListener('ended', () => clearInterval(iv));
        window.__gdmStreams.push(s);
        return s;
      };
      const origGum = md.getUserMedia.bind(md);
      md.getUserMedia = async c => {
        window.__mdCalls.gum++;
        const s = await origGum(c);
        window.__gumStreams.push(s);
        return s;
      };
      const OrigAC = window.AudioContext;
      window.AudioContext = class extends OrigAC {
        constructor(...a) { super(...a); window.__acStats.created++; }
        close() { window.__acStats.closed++; return super.close(); }
      };
      OrigAC.prototype.createMediaStreamDestination = function () {
        throw new DOMException('injected mix failure', 'InvalidStateError');
      };
    });
    try {
      await page.goto(`${BASE}/`);
      await page.click('#btn-screen');
      await waitForState(page, s => s.lifecycle === 'idle', 15000, 'idle after mix failure');
      const obs = await page.evaluate(() => ({
        micPrompts: window.__mdCalls.gum,
        displayTracks: window.__gdmStreams.flatMap(s => s.getTracks().map(t => t.readyState)),
        micTracks: window.__gumStreams.flatMap(s => s.getTracks().map(t => t.readyState)),
        ac: window.__acStats,
      }));
      check('S8 mic was requested once', obs.micPrompts === 1, `gum=${obs.micPrompts}`);
      check('S8 display tracks ended', obs.displayTracks.every(r => r === 'ended'), JSON.stringify(obs.displayTracks));
      check('S8 mic tracks ended', obs.micTracks.every(r => r === 'ended'), JSON.stringify(obs.micTracks));
      check('S8 no AudioContext leak', obs.ac.created > 0 && obs.ac.closed === obs.ac.created, JSON.stringify(obs.ac));
      check('S8 no publish', (await st(page)).whipPost === null);
    } finally {
      await ctx.close();
    }
  }

  // =========================================================================
  // Scenario 9: PC mix attenuation — injected display carries a full-scale
  // tone and the (fake-device) mic is active too. The outgoing mix must be
  // audible but attenuated: two simultaneous peaks must not clip (<=0.95).
  // =========================================================================
  {
    const ctx = await browser.newContext();
    const page = await ctx.newPage();
    await ctx.addInitScript(() => {
      const md = navigator.mediaDevices;
      md.getDisplayMedia = async () => {
        const c = document.createElement('canvas'); c.width = 1280; c.height = 720;
        const g = c.getContext('2d');
        const iv = setInterval(() => g.fillRect(0, 0, 1280, 720), 100);
        const s = c.captureStream(30);
        s.getVideoTracks()[0].addEventListener('ended', () => clearInterval(iv));
        // loud shared audio: amplitude ~0.9
        const ac = new AudioContext();
        const dest = ac.createMediaStreamDestination();
        const osc = ac.createOscillator(); osc.frequency.value = 300;
        const gain = ac.createGain(); gain.gain.value = 0.9;
        osc.connect(gain); gain.connect(dest); osc.start();
        window.__keepAlive = () => ac.resume();
        return new MediaStream([...s.getVideoTracks(), ...dest.stream.getAudioTracks()]);
      };
    });
    try {
      await page.goto(`${BASE}/`);
      await page.click('#btn-screen');       // gesture → AudioContext allowed
      await waitForState(page, x => x.lifecycle === 'publishing', 25000, 'PC publishing');
      await waitForState(page, x => ['connected', 'completed'].includes(x.iceState), 15000, 'PC ICE');
      await sleep(2500);
      const peaks = [];
      for (let i = 0; i < 6; i++) {
        peaks.push(await page.evaluate(() => window.__outgoingAudioPeak()));
        await sleep(400);
      }
      const peak = Math.max(...peaks);
      check('S9 mix audible', peak > 0.05, `peak=${peak.toFixed(3)} samples=${JSON.stringify(peaks.map(p => +p.toFixed(3)))}`);
      check('S9 mix attenuated (no clip)', peak <= 0.95, `peak=${peak.toFixed(3)}`);
      await stopAndWait(page);
      check('S9 WebRTC sessions empty', (await webrtcSessionCount()) === 0);
    } finally {
      await ctx.close();
    }
  }

  // =========================================================================
  // Scenario 10: display sharing ends WHILE the mic permission is pending —
  // the ended handler must tear down immediately; the late mic resolution is
  // then discarded with all of its tracks stopped (P1 regression).
  // =========================================================================
  {
    const ctx = await browser.newContext();
    const page = await ctx.newPage();
    await ctx.addInitScript(() => {
      window.__gdmStreams = [];
      window.__gumStreams = [];
      const md = navigator.mediaDevices;
      md.getDisplayMedia = async () => {
        const c = document.createElement('canvas'); c.width = 1280; c.height = 720;
        const g = c.getContext('2d');
        const iv = setInterval(() => g.fillRect(0, 0, 1280, 720), 100);
        const s = c.captureStream(30);
        s.getVideoTracks()[0].addEventListener('ended', () => clearInterval(iv));
        window.__gdmStreams.push(s);
        return s;
      };
      const origGum = md.getUserMedia.bind(md);
      md.getUserMedia = async c => {
        await new Promise(r => setTimeout(r, 3000)); // ended fires during this wait
        const s = await origGum(c);
        window.__gumStreams.push(s);
        return s;
      };
    });
    try {
      await page.goto(`${BASE}/`);
      await page.click('#btn-screen');
      await page.waitForFunction(() => window.__spikeState?.lifecycle === 'starting', null, { timeout: 8000 });
      await sleep(700); // inside the mic wait now
      await page.evaluate(() => {
        const t = window.__gdmStreams[0].getVideoTracks()[0];
        t.dispatchEvent(new Event('ended'));   // user pressed 공유 중지
      });
      await waitForState(page, s => s.lifecycle === 'idle', 8000, 'idle after ended-during-mic-wait');
      check('S10 ended during mic wait → teardown', (await st(page)).whipPost === null);
      // Wait EXPLICITLY for the injected getUserMedia to complete, then
      // require non-empty tracks before asserting they all ended (an empty
      // array must never pass as "late mic discarded").
      await page.waitForFunction(() => (window.__gumStreams || []).length > 0, null, { timeout: 10000 });
      await sleep(500); // discard path settles
      const obs = await page.evaluate(() => ({
        display: window.__gdmStreams.flatMap(s => s.getTracks().map(t => t.readyState)),
        mic: window.__gumStreams.flatMap(s => s.getTracks().map(t => t.readyState)),
      }));
      check('S10 display tracks ended', obs.display.length > 0 && obs.display.every(r => r === 'ended'), JSON.stringify(obs.display));
      check('S10 late mic tracks ended', obs.mic.length > 0 && obs.mic.every(r => r === 'ended'), JSON.stringify(obs.mic));
      check('S10 WebRTC sessions empty', (await webrtcSessionCount()) === 0);
    } finally {
      await ctx.close();
    }
  }
  // =========================================================================
  // Scenario 11: player path failure injection (no Safari claims).
  //  a) native-only environment (MSE removed) + lying canPlayType: the play
  //     attempt fails and ends with the ACCURATE unrecoverable error.
  //  b) native selected first (injected) then NotSupportedError: exactly one
  //     fallback to MSE, which then really plays.
  // =========================================================================
  {
    // --- 11a: MSE unavailable → accurate terminal failure -------------------
    const ctxA = await browser.newContext();
    const pageA = await ctxA.newPage();
    try {
      await pageA.goto(`${BASE}/?mode=synth&autostart=1`);
      await waitForState(pageA, s => s.lifecycle === 'publishing', 20000, '11a publishing');
      await pageA.evaluate(() => {
        window.Hls = undefined; // MSE gone
        const orig = HTMLMediaElement.prototype.canPlayType;
        HTMLMediaElement.prototype.canPlayType = function (t) {
          return String(t).includes('mpegurl') ? 'probably' : orig.call(this, t);
        };
      });
      await pageA.click('#btn-play');
      await sleep(2500);
      const s = await st(pageA);
      const probe = await pageA.evaluate(() => window.__playbackProbe());
      check('S11a ends as none with accurate failure', s.playerMode === 'none' && s.fallbackAttempted === true,
        `mode=${s.playerMode} fallback=${s.fallbackAttempted} last=${s.lastEvent}`);
      check('S11a no fake playback', probe.currentTime === 0 && probe.frames === 0, JSON.stringify(probe));
      await stopAndWait(pageA);
    } finally {
      await ctxA.close();
    }

    // --- 11b: native fails first → exactly one MSE fallback, real playback --
    const ctxB = await browser.newContext();
    const pageB = await ctxB.newPage();
    try {
      await pageB.goto(`${BASE}/?mode=synth&autostart=1`);
      await waitForState(pageB, s => s.lifecycle === 'publishing', 20000, '11b publishing');
      await waitForState(pageB, s => ['connected', 'completed'].includes(s.iceState), 15000, '11b ICE');
      await pageB.evaluate(() => {
        // Force native selection first: the FIRST isSupported() call lies.
        const origCT = HTMLMediaElement.prototype.canPlayType;
        HTMLMediaElement.prototype.canPlayType = function (t) {
          return String(t).includes('mpegurl') ? 'probably' : origCT.call(this, t);
        };
        const origSup = window.Hls.isSupported.bind(window.Hls);
        let first = true;
        window.Hls.isSupported = () => { if (first) { first = false; return false; } return origSup(); };
      });
      await sleep(12000); // let HLS output be ready
      await pageB.click('#btn-play');
      await waitForState(pageB, s => s.playerMode === 'mse' && s.fallbackAttempted === true, 15000, '11b mse fallback');
      const p1 = await pageB.evaluate(() => window.__playbackProbe());
      await sleep(2500);
      const p2 = await pageB.evaluate(() => window.__playbackProbe());
      check('S11b fallback plays via MSE', p2.currentTime > p1.currentTime && p2.currentTime > 0,
        `${p1.currentTime.toFixed(2)}→${p2.currentTime.toFixed(2)}`);
      check('S11b decoded frames increase after fallback', p2.frames > p1.frames && p2.frames > 0, `${p1.frames}→${p2.frames}`);
      await stopAndWait(pageB);
    } finally {
      await ctxB.close();
    }
  }
  // =========================================================================
  // Scenario 12: destination audio-track leaks on LATER init failures.
  // AC.close() does not end a MediaStreamDestination track, so dest cleanup
  // must be registered the moment dest is created.
  //  a) synthetic: dest succeeds, createOscillator throws.
  //  b) PC mix: dest+gains succeed, createMediaStreamSource throws.
  // =========================================================================
  {
    async function ctxInit(ctx) {
      await ctx.addInitScript(() => {
        window.__destTracks = [];
        window.__gdmStreams = [];
        window.__gumStreams = [];
        window.__acStats = { created: 0, closed: 0 };
        const AC = window.AudioContext;
        window.AudioContext = class extends AC {
          constructor(...a) { super(...a); window.__acStats.created++; }
          close() { window.__acStats.closed++; return super.close(); }
        };
        const origDest = AC.prototype.createMediaStreamDestination;
        AC.prototype.createMediaStreamDestination = function (...a) {
          const d = origDest.apply(this, a);
          window.__destTracks.push(...d.stream.getTracks());
          return d;
        };
      });
    }

    // --- 12a: synthetic, oscillator creation throws after dest succeeded ---
    {
      const ctx = await browser.newContext();
      const page = await ctx.newPage();
      await ctxInit(ctx);
      await ctx.addInitScript(() => {
        const orig = AudioContext.prototype.createOscillator;
        let first = true;
        AudioContext.prototype.createOscillator = function (...a) {
          if (first) { first = false; throw new DOMException('injected osc failure', 'InvalidStateError'); }
          return orig.apply(this, a);
        };
      });
      try {
        await page.goto(`${BASE}/`);
        await page.click('#btn-synth');
        await waitForState(page, s => s.lifecycle === 'idle', 10000, 'idle after osc failure');
        const obs = await page.evaluate(() => ({
          dest: window.__destTracks.map(t => t.readyState),
          ac: window.__acStats,
        }));
        check('S12a dest tracks exist and ended', obs.dest.length > 0 && obs.dest.every(r => r === 'ended'), JSON.stringify(obs.dest));
        check('S12a AudioContext closed', obs.ac.created > 0 && obs.ac.closed === obs.ac.created, JSON.stringify(obs.ac));
        check('S12a no publish', (await st(page)).whipPost === null);
      } finally {
        await ctx.close();
      }
    }

    // --- 12b: PC mix, source connection throws after dest+gains succeeded --
    {
      const ctx = await browser.newContext();
      const page = await ctx.newPage();
      await ctxInit(ctx);
      await ctx.addInitScript(() => {
        const md = navigator.mediaDevices;
        md.getDisplayMedia = async () => {
          const c = document.createElement('canvas'); c.width = 640; c.height = 360;
          const g = c.getContext('2d');
          const iv = setInterval(() => g.fillRect(0, 0, 640, 360), 100);
          const s = c.captureStream(15);
          s.getVideoTracks()[0].addEventListener('ended', () => clearInterval(iv));
          window.__gdmStreams.push(s);
          return s;
        };
        const origGum = md.getUserMedia.bind(md);
        md.getUserMedia = async c => { const s = await origGum(c); window.__gumStreams.push(s); return s; };
        const orig = AudioContext.prototype.createMediaStreamSource;
        let first = true;
        AudioContext.prototype.createMediaStreamSource = function (...a) {
          if (first) { first = false; throw new DOMException('injected source failure', 'InvalidStateError'); }
          return orig.apply(this, a);
        };
      });
      try {
        await page.goto(`${BASE}/`);
        await page.click('#btn-screen');
        await waitForState(page, s => s.lifecycle === 'idle', 10000, 'idle after source failure');
        const obs = await page.evaluate(() => ({
          dest: window.__destTracks.map(t => t.readyState),
          display: window.__gdmStreams.flatMap(s => s.getTracks().map(t => t.readyState)),
          mic: window.__gumStreams.flatMap(s => s.getTracks().map(t => t.readyState)),
          ac: window.__acStats,
        }));
        check('S12b dest tracks exist and ended', obs.dest.length > 0 && obs.dest.every(r => r === 'ended'), JSON.stringify(obs.dest));
        check('S12b display tracks ended', obs.display.every(r => r === 'ended'), JSON.stringify(obs.display));
        check('S12b mic tracks ended', obs.mic.length > 0 && obs.mic.every(r => r === 'ended'), JSON.stringify(obs.mic));
        check('S12b AudioContext closed', obs.ac.created > 0 && obs.ac.closed === obs.ac.created, JSON.stringify(obs.ac));
        check('S12b no publish', (await st(page)).whipPost === null);
      } finally {
        await ctx.close();
      }
    }
  }
} finally {
  await browser.close().catch(() => {});
}

const failed = results.filter(r => r.ok === false);
const skipped = results.filter(r => r.ok === null);
console.log(`\n${results.length - failed.length - skipped.length} 통과 / ${skipped.length} skip / ${failed.length} 실패 (총 ${results.length})`);
process.exit(failed.length ? 1 : 0);
