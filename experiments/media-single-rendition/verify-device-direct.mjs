// Direct system Chrome → isolated Caddy HTTPS → real MediaMTX/FFmpeg.
// A CA-verified leaf SPKI is pinned only for this launched Chrome process.
// Synthetic canvas/audio only; physical devices and public internet remain open.
import { chromium } from 'playwright';
import { readFileSync } from 'node:fs';
import { createHash, X509Certificate } from 'node:crypto';
import tls from 'node:tls';

const target = process.env.WBB_DEVICE_URL;
const caPath = process.env.WBB_DEVICE_CA;
const username = process.env.WBB_DEVICE_USER;
const password = process.env.WBB_DEVICE_PASSWORD;
if (!target || !caPath || !username || !password) {
  console.error('Set WBB_DEVICE_URL/CA/USER/PASSWORD for the isolated stack');
  process.exit(2);
}
const url = new URL(target);
const ca = readFileSync(caPath);
const pin = await new Promise((resolve, reject) => {
  const socket = tls.connect({ host: url.hostname, port: Number(url.port || 443), ca,
    rejectUnauthorized: true, timeout: 5000 }, () => {
    if (!socket.authorized) return reject(new Error('Caddy certificate not CA-verified'));
    const cert = new X509Certificate(socket.getPeerCertificate().raw);
    const spki = cert.publicKey.export({ type: 'spki', format: 'der' });
    resolve(createHash('sha256').update(spki).digest('base64'));
    socket.end();
  });
  socket.on('timeout', () => socket.destroy(new Error('TLS timeout')));
  socket.on('error', reject);
});
const browser = await chromium.launch({ channel: 'chrome', headless: true,
  args: [`--ignore-certificate-errors-spki-list=${pin}`] });
const context = await browser.newContext({ httpCredentials: { username, password } });
const page = await context.newPage();
const mediaResponses = [];
const mediaRequests = [];
const failedRequests = [];
const errors = [];
const results = [];
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
function check(label, ok, detail = '') {
  console.log(`${ok ? 'PASS' : 'FAIL'} ${label}${detail ? ' — ' + detail : ''}`);
  results.push(ok);
}
page.on('pageerror', e => errors.push(e.message));
page.on('requestfailed', r => failedRequests.push({ path: new URL(r.url()).pathname, reason: r.failure()?.errorText }));
page.on('request', async r => {
  const u = new URL(r.url());
  if (u.pathname.startsWith('/__media-check/hls/')) {
    const item = { path: u.pathname, queryKeys: [...u.searchParams.keys()], hasCookie: false };
    mediaRequests.push(item);
    const headers = await r.allHeaders();
    item.hasCookie = Boolean(headers.cookie);
    item.validCookie = /(?:^|;\s*)cookieCheck=1(?:;|$)/.test(headers.cookie || '');
  }
});
page.on('response', r => {
  const u = new URL(r.url());
  if (u.pathname.startsWith('/__media-check/hls/')) {
    const headers = r.headers();
    mediaResponses.push({ status: r.status(), path: u.pathname, queryKeys: [...u.searchParams.keys()],
      redirect: headers.location || '', setCookie: Boolean(headers['set-cookie']), server: headers.server || '' });
  }
});
try {
  const caps = await page.evaluate(() => ({
    h264: document.createElement('video').canPlayType('video/mp4; codecs="avc1.42E01E"'),
    aac: document.createElement('video').canPlayType('audio/mp4; codecs="mp4a.40.2"'),
  }));
  check('system Chrome H264/AAC preflight', Boolean(caps.h264 && caps.aac));
  if (!caps.h264 || !caps.aac) throw new Error('system Chrome lacks media codecs');
  const navigation = await page.goto(target + '/__media-check/', { waitUntil: 'load', timeout: 15000 });
  check('direct Caddy HTTPS UI with Basic Auth', navigation?.status() === 200 &&
    await page.locator('#btn-synth').isEnabled());
  await page.locator('#btn-synth').click();
  await page.waitForFunction(() => window.__spikeState?.whipPost === 201 &&
    window.__spikeState?.patchStatus === 204 && window.__spikeState?.iceState === 'connected',
  null, { timeout: 25000 });
  check('direct browser WHIP POST/PATCH/ICE', true);
  let hlsReady = false;
  for (let attempt = 0; attempt < 30; attempt++) {
    hlsReady = await page.evaluate(async () => {
      try {
        const response = await fetch('/__media-check/hls/spike/720p/index.m3u8', { cache: 'no-store' });
        return response.status === 200 && (await response.text()).includes('#EXTM3U');
      } catch (_) { return false; }
    });
    if (hlsReady) break;
    await sleep(1000);
  }
  check('direct browser HLS playlist ready', hlsReady);
  await page.evaluate(() => manifestProbe());
  const manifest = await page.evaluate(() => ({ ll: window.__spikeState?.manifestLL,
    lastEvent: window.__spikeState?.lastEvent }));
  check('direct HTTPS manifestProbe observes LL-HLS', manifest.ll === true &&
    manifest.lastEvent?.includes('CAN-BLOCK-RELOAD=true'));
  const probeFetch = await page.evaluate(async () => {
    const response = await fetch('/__media-check/hls/spike/720p/index.m3u8', { cache: 'no-store' });
    const text = await response.text();
    return { status: response.status, path: new URL(response.url).pathname, isM3u8: text.includes('#EXTM3U') };
  });
  if (!probeFetch.isM3u8) console.log('HLS fetch diagnostic:', probeFetch);
  await page.locator('#btn-play').click();
  try {
    await page.waitForFunction(() => {
      const v = document.getElementById('playback');
      return v?.readyState >= 2 && !v.paused && v.currentTime > 0;
    }, null, { timeout: 30000 });
  } catch (_) { /* probes below provide the result */ }
  const first = await page.evaluate(() => window.__playbackProbe());
  await page.waitForTimeout(2500);
  const second = await page.evaluate(() => window.__playbackProbe());
  check('direct MSE playback time increases', second.currentTime > first.currentTime && second.currentTime > 0,
    `${first.currentTime.toFixed(2)}→${second.currentTime.toFixed(2)}`);
  check('direct MSE decoded frames increase', second.frames > first.frames && second.frames > 0,
    `${first.frames}→${second.frames}`);
  await page.locator('#btn-sound').click();
  await page.waitForTimeout(1500);
  const audio = await page.evaluate(() => window.__playbackProbe());
  check('direct browser audio non-silent', audio.audioLevel > 0, `level=${audio.audioLevel}`);
  const cookies = await context.cookies(target + '/__media-check/hls/spike/720p/index.m3u8');
  check('HLS cookie remains on authenticated path', cookies.some(c => c.name === 'cookieCheck' &&
    c.path.startsWith('/__media-check/hls/')));
  check('HLS redirect remains on authenticated path', mediaResponses.some(r => r.status === 302) &&
    mediaResponses.filter(r => r.status === 302).every(r => r.redirect.startsWith('/__media-check/hls/')));
  check('HLS media assets fetched directly', mediaResponses.some(r => r.status === 200 && r.path.endsWith('.m3u8')) &&
    mediaResponses.some(r => r.status === 200 && r.path.endsWith('.mp4')));
  await page.locator('#btn-stop').click();
  await page.waitForFunction(() => window.__spikeState?.lifecycle === 'idle', null, { timeout: 15000 });
  const stopped = await page.evaluate(() => window.__spikeState);
  check('direct browser stop closes tracks/PC/audio', stopped.tracksStopped === true &&
    stopped.pcClosed === true && stopped.acClosed === true &&
    ((stopped.deleteStatus >= 200 && stopped.deleteStatus < 300) || stopped.deleteStatus === 404));
  check('no browser page errors', errors.length === 0, `count=${errors.length}`);
  check('no failed direct media request except deliberate abort',
    failedRequests.filter(x => x.path.startsWith('/__media-check/') && x.reason !== 'net::ERR_ABORTED').length === 0,
    `count=${failedRequests.length}`);
  if (results.some(x => !x)) {
    const hls = await page.locator('#hls-state').textContent();
    console.log('diagnostic:', JSON.stringify({ hls,
      mediaResponses: mediaResponses.map(x => ({ status: x.status, path: x.path, redirect: x.redirect, setCookie: x.setCookie })),
      mediaRequests,
      probeFetch,
      cookiePaths: cookies.map(x => ({ name: x.name, path: x.path })),
      requestFailures: failedRequests.map(x => x.reason), pageErrors: errors.length }));
  }
} finally {
  await page.close();
  await browser.close();
}
if (results.some(x => !x)) process.exitCode = 1;
