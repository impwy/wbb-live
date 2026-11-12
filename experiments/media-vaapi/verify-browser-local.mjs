// Phase B: browser synthetic WHIP over TCP ICE (through SSH local forwards)
// into the REMOTE VAAPI spike. Runs on the developer machine against:
//   http://127.0.0.1:8090  (existing CPU-experiment web helper, unchanged)
//   127.0.0.1:8889 -> Labtop:18889 (WHIP HTTP)   [ssh -L]
//   127.0.0.1:8888 -> Labtop:18888 (LL-HLS)      [ssh -L]
//   127.0.0.1:18390 -> Labtop:18390 (ICE/TCP)    [ssh -L, same port number]
// The spike's MediaMTX has UDP ICE disabled, so a connected session PROVES
// TCP ICE; additionally the selected candidate pair protocol is asserted.
// Termination evidence is produced server-side via verify-termination.py.
import { chromium } from 'playwright';
import { execFileSync } from 'node:child_process';

const sleep = ms => new Promise(r => setTimeout(r, ms));
const results = [];
const remoteDir = process.env.WBB_VAAPI_REMOTE_DIR || '/tmp/wbb-media-vaapi';
if (!/^\/tmp\/wbb-media-vaapi(?:-[a-z0-9-]+)?$/.test(remoteDir)) throw new Error('invalid WBB_VAAPI_REMOTE_DIR');
const hlsPort = Number(process.env.WBB_VAAPI_HLS_PORT || 8888);
const whipPort = Number(process.env.WBB_VAAPI_WHIP_PORT || 8889);
for (const port of [hlsPort, whipPort]) if (!Number.isInteger(port) || port < 1 || port > 65535) throw new Error('invalid forward port');
function check(name, ok, detail = '') {
  results.push({ name, ok, detail });
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${name}${detail ? ' — ' + detail : ''}`);
}
const remote = (cmd, log) => {
  try {
    return execFileSync('ssh', ['-o', 'BatchMode=yes', '-o', 'ConnectTimeout=8', 'Labtop', `cd ${remoteDir} && ${cmd}`], { encoding: 'utf8', timeout: 45000, maxBuffer: 10 * 1024 * 1024 });
  } catch (e) {
    if (log) console.log(`  [remote:${log}] ${String(e).slice(0, 200)}`);
    throw e;
  }
};

let browser;
try {
  browser = await chromium.launch({ channel: 'chrome' });
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  // The reused web helper uses fixed 8888/8889 URLs. Rewrite only those
  // browser requests when another local experiment already owns the ports.
  await page.route(/^http:\/\/127\.0\.0\.1:888[89]\//, async route => {
    const url = new URL(route.request().url());
    url.port = String(url.port === '8888' ? hlsPort : whipPort);
    if (route.request().url().startsWith('http://127.0.0.1:8888/')) {
      try {
        const response = await route.fetch({ url: url.toString(), timeout: 15000 });
        return await route.fulfill({ response });
      } catch {
        // Blocking LL-HLS reloads may still be in flight when the browser
        // closes after the verified stop. The request is then already gone.
        try { await route.abort(); } catch { /* context closed */ }
        return;
      }
    }
    return route.continue({ url: url.toString() });
  });
  page.on('pageerror', e => console.log('  [pageerror]', String(e).slice(0, 160)));

  // External observation of the selected ICE candidate pair (no app changes).
  await ctx.addInitScript(() => {
    const Orig = window.RTCPeerConnection;
    window.__pcs = [];
    window.RTCPeerConnection = function (...args) {
      const pc = new Orig(...args);
      window.__pcs.push(pc);
      return pc;
    };
    window.RTCPeerConnection.prototype = Orig.prototype;
    window.__icePair = async () => {
      for (const pc of window.__pcs) {
        const stats = await pc.getStats();
        let sel = null;
        const byId = {};
        stats.forEach(r => { byId[r.id] = r; });
        stats.forEach(r => {
          if (r.type === 'candidate-pair' && (r.selected === true || (r.nominated === true && r.state === 'succeeded'))) sel = r;
        });
        if (sel) {
          const lc = byId[sel.localCandidateId], rc = byId[sel.remoteCandidateId];
          return { state: sel.state, local: lc ? `${lc.protocol}/${lc.candidateType}` : null, remote: rc ? `${rc.protocol}/${rc.candidateType}` : null };
        }
      }
      return null;
    };
  });

  await page.goto('http://127.0.0.1:8090/');
  await page.click('#btn-synth'); // real click: publisher AudioContext must run
  await page.waitForFunction(() => window.__spikeState?.lifecycle === 'publishing', null, { timeout: 25000 });
  await page.waitForFunction(() => ['connected', 'completed'].includes(window.__spikeState?.iceState), null, { timeout: 20000 });
  check('B1 WHIP publishing over tunnel', true);
  check('B1 ICE connected', true, `state=${await page.evaluate(() => window.__spikeState.iceState)}`);

  // ICE must be TCP (UDP is disabled on this spike's MediaMTX).
  let pair = null;
  for (let i = 0; i < 10 && !pair; i++) {
    pair = await page.evaluate(() => window.__icePair());
    if (!pair) await sleep(500);
  }
  check('B2 selected pair is TCP', pair !== null && /tcp/.test(pair.local || '') && /tcp/.test(pair.remote || ''),
    JSON.stringify(pair));
  // The stats interval samples publisherAC once per second — wait for it.
  await page.waitForFunction(
    () => window.__spikeState?.publisherAC !== undefined && window.__spikeState?.publisherAC !== null,
    null, { timeout: 8000 },
  ).catch(() => {});
  check('B2 publisher AC running', (await page.evaluate(() => window.__spikeState.publisherAC)) === 'running',
    `state=${await page.evaluate(() => window.__spikeState.publisherAC)}`);

  // Server-side VAAPI output through the forwarded HLS port.
  await sleep(12000);
  const master = await (await fetch(`http://127.0.0.1:${hlsPort}/spike/720p/index.m3u8`, { cache: 'no-store', signal: AbortSignal.timeout(10000) })).text();
  check('B3 master playlist via tunnel', master.includes('#EXTM3U'));
  const variantRel = (master.match(/^([^#\s][^\s]*)$/m) || [])[1];
  const media = await (await fetch(new URL(variantRel, `http://127.0.0.1:${hlsPort}/spike/720p/index.m3u8`), { cache: 'no-store', signal: AbortSignal.timeout(10000) })).text();
  check('B3 LL-HLS parts', media.includes('EXT-X-PART-INF') && /EXT-X-PART:/.test(media));

  // Real playback (MSE path) through the tunnel.
  await page.click('#btn-play');
  await page.waitForFunction(() => {
    const v = document.getElementById('playback');
    return v && v.readyState >= 2 && !v.paused && v.currentTime > 0;
  }, null, { timeout: 15000 }).catch(() => {});
  const p1 = await page.evaluate(() => window.__playbackProbe());
  await sleep(2500);
  const p2 = await page.evaluate(() => window.__playbackProbe());
  check('B4 playback time increasing', p2.currentTime > p1.currentTime && p2.currentTime > 0,
    `${p1.currentTime.toFixed(2)}→${p2.currentTime.toFixed(2)}`);
  check('B4 decoded frames increasing', p2.frames > p1.frames && p2.frames > 0, `${p1.frames}→${p2.frames}`);
  await page.click('#btn-sound');
  await sleep(1200);
  const pA = await page.evaluate(() => window.__playbackProbe());
  if (pA.audioLevel === -1) {
    // The browser analyser can be unavailable. Observe decoded PCM from this
    // live server output instead; an unavailable substitute is a failure.
    const pcm = execFileSync('ssh', ['-o', 'BatchMode=yes', '-o', 'ConnectTimeout=8', 'Labtop',
      `cd ${remoteDir} && timeout -k 2 25 docker compose exec -T mediamtx timeout 20 ffmpeg -v error -i http://127.0.0.1:8888/spike/720p/index.m3u8 -map 0:a:0 -t 3 -f s16le -`],
      { timeout: 30000, maxBuffer: 2 * 1024 * 1024 });
    let peak = 0;
    for (let i = 0; i + 1 < pcm.length; i += 2) peak = Math.max(peak, Math.abs(pcm.readInt16LE(i)));
    check('B4 server PCM audio non-silent', pcm.length > 1000 && peak > 0, `bytes=${pcm.length} peak=${peak}`);
  } else check('B4 browser audio non-silent', pA.audioLevel > 0, `level=${pA.audioLevel}`);

  // Server-side ffprobe of the VAAPI output while the browser publishes.
  const probe = remote('timeout -k 2 35 docker compose exec -T mediamtx timeout 30 ffprobe -v error -of json -show_streams http://127.0.0.1:8888/spike/720p/index.m3u8', 'ffprobe');
  const streams = JSON.parse(probe).streams;
  const v = streams.find(s => s.codec_type === 'video');
  const a = streams.find(s => s.codec_type === 'audio');
  check('B5 remote VAAPI codec h264/aac', v?.codec_name === 'h264' && a?.codec_name === 'aac', `${v?.codec_name}/${a?.codec_name}`);
  check('B5 remote dims <=1280x720', (v?.width ?? 0) > 0 && v.width <= 1280 && v.height <= 720, `${v?.width}x${v?.height}`);

  // Termination evidence (server-side) around the browser stop.
  remote('python3 ./verify-termination.py before > logs/term-browser-before.log 2>&1', 'before');
  console.log('  [remote] before OK');
  await page.click('#btn-stop');
  await page.waitForFunction(() => window.__spikeState?.lifecycle === 'idle', null, { timeout: 12000 });
  const del = await page.evaluate(() => window.__spikeState.deleteStatus);
  check('B6 browser DELETE 2xx-or-404', (del >= 200 && del < 300) || del === 404, `status=${del}`);
  remote('python3 ./verify-termination.py after > logs/term-browser-after.log 2>&1', 'after');
  const afterLog = remote('tail -2 logs/term-browser-after.log');
  check('B6 server termination evidence', /TERMINATION_OK/.test(afterLog), afterLog.trim().split('\n').pop());
} catch (e) {
  check('B verification execution', false, String(e).slice(0, 240));
} finally {
  await browser?.close().catch(() => {});
}

const failed = results.filter(r => !r.ok);
console.log(`\n${results.length - failed.length}/${results.length} 통과`);
process.exit(failed.length ? 1 : 0);
