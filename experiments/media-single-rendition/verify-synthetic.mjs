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
import { execSync } from 'node:child_process';

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
  args: ['--use-fake-ui-for-media-stream', '--use-fake-device-for-media-stream'],
});

try {
  // =========================================================================
  // Scenario 1: happy path (synthetic)
  // =========================================================================
  {
    const ctx = await browser.newContext();
    const page = await ctx.newPage();
    try {
      await page.goto(`${BASE}/?mode=synth&autostart=1`);
      await waitForState(page, s => s.lifecycle === 'publishing', 20000, 'publishing');
      await waitForState(page, s => ['connected', 'completed'].includes(s.iceState), 15000, 'ICE connected');

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
      if (p2.audioLevel === -1) {
        skip('S1 browser audio level', 'analyser/audio track unavailable');
      } else {
        check('S1 playback audio non-silent', p2.audioLevel > 0, `level=${p2.audioLevel}`);
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
      await ctx.route(WHIP_FILTER, async route => {
        if (route.request().method() === 'POST' && delayFirst) {
          delayFirst = false;
          const resp = await route.fetch();
          await sleep(4000); // the response arrives after the user stopped
          return route.fulfill({ response: resp });
        }
        return route.continue();
      });

      await page.goto(`${BASE}/`);
      await page.click('#btn-synth');
      await page.waitForFunction(() => window.__spikeState?.lastEvent?.includes('POST'), null, { timeout: 15000 });
      await page.click('#btn-stop'); // stop while POST is in flight
      await waitForState(page, s => s.lifecycle === 'idle', 10000, 'idle after stop-during-post');
      check('S3 stop during POST → idle, no publish', (await st(page)).whipPost === null);

      // New broadcast starts while the old response is still pending.
      await startSynthetic(page);
      const newLocation = (await st(page)).location;
      check('S3 new broadcast publishing', true);

      // The delayed 201 lands now: only its own session is deleted.
      await waitForState(page, s => s.lateDeleteStatus !== null, 10000, 'late DELETE');
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
      const orig = navigator.mediaDevices.getUserMedia.bind(navigator.mediaDevices);
      let first = true;
      navigator.mediaDevices.getUserMedia = async constraints => {
        if (first) { first = false; await new Promise(r => setTimeout(r, 4000)); }
        return orig(constraints);
      };
    });
    try {
      await page.goto(`${BASE}/`);
      await page.click('#btn-camera');   // fake device (launch flags)
      await sleep(300);
      await page.click('#btn-stop');     // stop before capture resolves
      await waitForState(page, s => s.lifecycle === 'idle', 10000, 'idle after stop-during-capture');
      const s = await st(page);
      check('S6 stop during capture → no publish', s.whipPost === null && s.lifecycle === 'idle');
      await sleep(4500); // let the late capture resolve and be discarded
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
} finally {
  await browser.close().catch(() => {});
}

const failed = results.filter(r => r.ok === false);
const skipped = results.filter(r => r.ok === null);
console.log(`\n${results.length - failed.length - skipped.length} 통과 / ${skipped.length} skip / ${failed.length} 실패 (총 ${results.length})`);
process.exit(failed.length ? 1 : 0);
