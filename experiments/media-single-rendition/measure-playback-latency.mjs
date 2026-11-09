// Isolated synthetic-publisher baseline. Never run against the shared 8090 stack.
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
const page = await context.newPage();
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
// Test-only timestamp provider: encode Date.now() mod 2^32 into 32 large
// black/white cells on the synthetic canvas before canvas.captureStream sees it.
await page.addInitScript(() => {
  const original = CanvasRenderingContext2D.prototype.fillText;
  CanvasRenderingContext2D.prototype.fillText = function (text, ...args) {
    original.call(this, text, ...args);
    if (typeof text !== 'string' || !text.startsWith('WBB synthetic')) return;
    const stamp = Date.now() >>> 0;
    for (let bit = 0; bit < 32; bit++) {
      this.fillStyle = (stamp >>> (31 - bit)) & 1 ? '#fff' : '#000';
      this.fillRect(80 + bit * 35, 100, 35, 50);
    }
  };
});
try {
  const response = await page.goto(target + '/__media-check/', { waitUntil: 'load' });
  if (response?.status() !== 200) throw new Error('authenticated UI not ready');
  const publishAt = Date.now();
  await page.locator('#btn-synth').click();
  await page.waitForFunction(() => window.__spikeState?.whipPost === 201 &&
    window.__spikeState?.patchStatus === 204 && window.__spikeState?.iceState === 'connected',
  null, { timeout: 25000 });
  let hlsReady = false;
  for (let i = 0; i < 30; i++) {
    hlsReady = await page.evaluate(async () => {
      try {
        const response = await fetch('/__media-check/hls/spike/720p/index.m3u8', { cache: 'no-store' });
        return response.status === 200 && (await response.text()).includes('#EXTM3U');
      } catch (_) { return false; }
    });
    if (hlsReady) break;
    await sleep(1000);
  }
  if (!hlsReady) throw new Error('HLS master not ready in 30s');
  const masterReadyMs = Date.now() - publishAt;
  await page.waitForFunction(() => window.__spikeState?.playbackReady === true,
    null, { timeout: 30000 });
  const mediaReadyMs = Date.now() - publishAt;
  const playAt = Date.now();
  await page.locator('#btn-play').click();
  await page.waitForFunction(() => {
    const v = document.getElementById('playback');
    return v?.currentTime > 0 && v.getVideoPlaybackQuality().totalVideoFrames > 0;
  }, null, { timeout: 30000 });
  const startupMs = Date.now() - playAt;
  const samples = [];
  for (let i = 0; i < 5; i++) {
    samples.push(await page.evaluate(() => {
      const v = document.getElementById('playback');
      const d = hls?.levels?.[hls.currentLevel]?.details || hls?.levels?.[0]?.details;
      const numeric = x => Number.isFinite(x) ? Number(x.toFixed(3)) : null;
      let canvasToReadbackMs = null;
      if (v.videoWidth >= 1200 && v.videoHeight >= 150) {
        const canvas = document.createElement('canvas');
        canvas.width = v.videoWidth; canvas.height = v.videoHeight;
        const ctx = canvas.getContext('2d', { willReadFrequently: true });
        ctx.drawImage(v, 0, 0);
        let stamp = 0;
        for (let bit = 0; bit < 32; bit++) {
          const rgb = ctx.getImageData(97 + bit * 35, 125, 1, 1).data;
          stamp = (stamp * 2 + (rgb[0] + rgb[1] + rgb[2] > 384 ? 1 : 0)) >>> 0;
        }
        const delta = ((Date.now() >>> 0) - stamp + 2 ** 32) % (2 ** 32);
        if (delta > 0 && delta < 10000) canvasToReadbackMs = delta;
      }
      return {
        currentTime: numeric(v.currentTime), frames: v.getVideoPlaybackQuality().totalVideoFrames,
        canvasToReadbackMs,
        syncPositionGap: numeric(hls?.liveSyncPosition - v.currentTime),
        latency: numeric(hls?.latency), targetLatency: numeric(hls?.targetLatency),
        maxLatency: numeric(hls?.maxLatency), liveSyncPosition: numeric(hls?.liveSyncPosition),
        partHoldBack: numeric(d?.partHoldBack), holdBack: numeric(d?.holdBack),
        partTarget: numeric(d?.partTarget), targetduration: numeric(d?.targetduration),
        edge: numeric(d?.edge), age: numeric(d?.age),
      };
    }));
    if (i < 4) await sleep(2000);
  }
  const timestamps = samples.map(x => x.canvasToReadbackMs).filter(Number.isFinite);
  const validTimestamps = timestamps.length >= 4 &&
    Math.max(...timestamps) - Math.min(...timestamps) <= 3000;
  console.log(JSON.stringify({ test: 'isolated synthetic Chrome', masterReadyMs, mediaReadyMs,
    masterReadyMeaning: 'publish click to master playlist HTTP 200; media/part readiness not included',
    startupMs, timestampSamplesValid: validTimestamps, samples }));
  if (!samples[4].frames || samples[4].frames <= samples[0].frames || !validTimestamps) {
    console.error('playback progression or canvas-to-readback timestamp measurement unavailable');
    process.exitCode = 1;
  }
} finally {
  if (!page.isClosed()) {
    await page.locator('#btn-stop').click({ timeout: 5000 }).catch(() => {});
    await page.waitForTimeout(300).catch(() => {});
    await page.close();
  }
  await browser.close();
}
