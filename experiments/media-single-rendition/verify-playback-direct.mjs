// Isolated direct Chrome: independent viewer and synthetic publisher tabs.
// Never targets the shared 8090 stack or captures physical devices.
import { chromium } from 'playwright';
import { readFileSync } from 'node:fs';
import { createHash, X509Certificate } from 'node:crypto';
import tls from 'node:tls';

const target = process.env.WBB_DEVICE_URL;
const caPath = process.env.WBB_DEVICE_CA;
const username = process.env.WBB_DEVICE_USER;
const password = process.env.WBB_DEVICE_PASSWORD;
if (target !== 'https://127.0.0.1:18443' || !caPath || !username || !password) {
  console.error('Use only the isolated loopback 18443 stack with CA and synthetic credentials');
  process.exit(2);
}
const ca = readFileSync(caPath);
const pin = await new Promise((resolve, reject) => {
  const socket = tls.connect({ host: '127.0.0.1', port: 18443, ca,
    rejectUnauthorized: true, timeout: 5000 }, () => {
    if (!socket.authorized) return reject(new Error('CA verification failed'));
    const cert = new X509Certificate(socket.getPeerCertificate().raw);
    resolve(createHash('sha256').update(cert.publicKey.export({ type: 'spki', format: 'der' })).digest('base64'));
    socket.end();
  });
  socket.on('timeout', () => socket.destroy(new Error('TLS timeout')));
  socket.on('error', reject);
});
const browser = await chromium.launch({ channel: 'chrome', headless: true,
  args: [`--ignore-certificate-errors-spki-list=${pin}`] });
const context = await browser.newContext({ httpCredentials: { username, password } });
const viewer = await context.newPage();
const publisher = await context.newPage();
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
const results = [];
function check(label, ok, detail = '') {
  console.log(`${ok ? 'PASS' : 'FAIL'} ${label}${detail ? ` — ${detail}` : ''}`);
  results.push(ok);
}
async function waitReady(ready, timeout = 30000) {
  await viewer.waitForFunction(expected => window.__spikeState?.playbackReady === expected,
    ready, { timeout });
}
async function waitPublishing() {
  await publisher.waitForFunction(() => window.__spikeState?.whipPost === 201 &&
    window.__spikeState?.patchStatus === 204 && window.__spikeState?.iceState === 'connected',
  null, { timeout: 25000 });
}
try {
  await viewer.goto(target + '/__media-check/', { waitUntil: 'load' });
  await waitReady(false);
  check('separate viewer before publish cannot play or unmute',
    await viewer.locator('#btn-play').isDisabled() && await viewer.locator('#btn-sound').isDisabled());
  await publisher.goto(target + '/__media-check/', { waitUntil: 'load' });
  const started = Date.now();
  await publisher.locator('#btn-synth').click();
  await waitPublishing();
  await waitReady(true);
  check('real media/part readiness enables separate viewer',
    await viewer.locator('#btn-play').isEnabled() && await viewer.locator('#btn-sound').isEnabled(),
    `${Date.now() - started}ms`);
  await viewer.locator('#btn-play').click();
  await viewer.waitForFunction(() => {
    const v = document.getElementById('playback');
    return v?.currentTime > 0 && v.getVideoPlaybackQuality().totalVideoFrames > 0;
  }, null, { timeout: 20000 });
  const first = await viewer.evaluate(() => window.__playbackProbe());
  await sleep(2000);
  const second = await viewer.evaluate(() => window.__playbackProbe());
  check('separate viewer plays video frames', second.currentTime > first.currentTime &&
    second.frames > first.frames);
  const metrics = await viewer.evaluate(() => ({
    latency: window.__spikeState?.playerLatency,
    startupMs: window.__spikeState?.playbackStartupMs,
    label: document.getElementById('latency-state').textContent,
  }));
  check('startup and player-edge latency are labelled separately from E2E',
    Number.isFinite(metrics.latency) && Number.isFinite(metrics.startupMs) &&
    metrics.label.includes('엣지') && metrics.label.includes('E2E 미측정'));
  let readinessFault = '';
  await viewer.route('**/__media-check/hls/spike/720p/index.m3u8*', async route => {
    if (route.request().resourceType() !== 'fetch') return route.continue();
    if (readinessFault === 'network') return route.abort();
    if (readinessFault === 'gone') return route.fulfill({ status: 404, body: '' });
    return route.continue();
  });
  const firstSource = await viewer.locator('#playback').evaluate(v => v.currentSrc);
  readinessFault = 'network';
  await viewer.waitForFunction(() => window.__spikeState?.playbackReadiness === 'network',
    null, { timeout: 10000 });
  readinessFault = 'gone';
  await viewer.waitForFunction(() => window.__spikeState?.playbackReadiness === 'waiting',
    null, { timeout: 10000 });
  const afterGone = await viewer.locator('#playback').evaluate(v => ({
    paused: v.paused, hasSource: v.hasAttribute('src'), readyState: v.readyState,
    mode: window.__spikeState?.playerMode,
  }));
  check('network then 404 removes existing player source',
    afterGone.paused && !afterGone.hasSource && afterGone.readyState === 0 && afterGone.mode === 'none',
    JSON.stringify(afterGone));
  readinessFault = '';
  await waitReady(true);
  await viewer.locator('#btn-play').click();
  await viewer.waitForFunction(() => document.getElementById('playback')?.currentTime > 0,
    null, { timeout: 20000 });
  const secondSource = await viewer.locator('#playback').evaluate(v => v.currentSrc);
  check('network then 404 retry creates a fresh player',
    Boolean(firstSource && secondSource && firstSource !== secondSource));
  await viewer.locator('#playback').evaluate(v => v.pause());
  await sleep(2000);
  check('readiness polling does not resume user pause',
    await viewer.locator('#playback').evaluate(v => v.paused));
  const beforeLive = await viewer.locator('#playback').evaluate(v => {
    v.currentTime = Math.max(0, v.currentTime - 3);
    return v.currentTime;
  });
  await viewer.locator('#btn-live').click();
  const afterLive = await viewer.locator('#playback').evaluate(v => ({ time: v.currentTime, paused: v.paused }));
  check('explicit live seek advances position without resuming pause',
    afterLive.time > beforeLive + 1 && afterLive.paused);
  await publisher.locator('#btn-stop').click();
  await publisher.waitForFunction(() => window.__spikeState?.lifecycle === 'idle', null, { timeout: 15000 });
  await waitReady(false, 20000);
  check('end invalidates viewer readiness and old playback',
    await viewer.locator('#btn-play').isDisabled() &&
    await viewer.locator('#playback').evaluate(v => v.paused));
  await publisher.locator('#btn-synth').click();
  await waitPublishing();
  await waitReady(true);
  check('restart re-enables play without auto-resuming',
    await viewer.locator('#btn-play').isEnabled() &&
    await viewer.locator('#playback').evaluate(v => v.paused));
  let failPlayerManifest = true;
  let failedManifestRequests = 0;
  await viewer.route('**/__media-check/hls/spike/720p/index.m3u8*', async route => {
    if (failPlayerManifest && route.request().resourceType() === 'xhr') {
      failedManifestRequests++;
      return route.abort();
    }
    return route.continue();
  });
  await viewer.locator('#btn-play').click();
  let fatal = false;
  try {
    await viewer.waitForFunction(() => document.getElementById('hls-state')?.textContent.includes('재생을 다시 누르세요'),
      null, { timeout: 25000 });
    fatal = true;
  } catch (_) { /* report as failure below */ }
  check('fatal HLS manifest error releases the old player', fatal && failedManifestRequests > 0 &&
    await viewer.locator('#playback').evaluate(v => v.paused && !v.hasAttribute('src')));
  failPlayerManifest = false;
  await viewer.locator('#btn-play').click();
  await viewer.waitForFunction(() => document.getElementById('playback')?.currentTime > 0,
    null, { timeout: 20000 });
  check('viewer can explicitly retry with a new player after fatal error', true);
} finally {
  if (!publisher.isClosed()) {
    await publisher.locator('#btn-stop').click({ timeout: 5000 }).catch(() => {});
    await publisher.waitForTimeout(300).catch(() => {});
  }
  await browser.close();
}
if (results.some(x => !x)) process.exitCode = 1;
