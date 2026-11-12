// Browser-facing readiness seam: separate viewer, no publisher or real devices.
import { chromium } from 'playwright';
import { readFileSync } from 'node:fs';

const index = readFileSync(new URL('./web/index.html', import.meta.url), 'utf8');
const app = readFileSync(new URL('./web/app.js', import.meta.url));
const hls = readFileSync(new URL('./web/vendor/hls.min.js', import.meta.url));
const browser = await chromium.launch({ channel: 'chrome', headless: true });
const page = await browser.newPage();
let phase = 'offline';
let part = 0;
let masterRequests = 0;
const results = [];
function check(name, ok) {
  console.log(`${ok ? 'PASS' : 'FAIL'} ${name}`);
  results.push(ok);
}
const cors = { 'access-control-allow-origin': 'http://127.0.0.1:8090' };
await page.route('**/*', async route => {
  const url = new URL(route.request().url());
  if (url.port === '8090') {
    if (url.pathname === '/') return route.fulfill({ status: 200, contentType: 'text/html', body: index });
    if (url.pathname === '/app.js') return route.fulfill({ status: 200, contentType: 'application/javascript', body: app });
    if (url.pathname === '/vendor/hls.min.js')
      return route.fulfill({ status: 200, contentType: 'application/javascript', body: hls });
  }
  if (url.port === '8888') {
    if (url.pathname.endsWith('/index.m3u8')) {
      masterRequests++;
      if (phase === 'slow') {
        await new Promise(resolve => setTimeout(resolve, 7000));
        return route.fulfill({ status: 200, headers: cors, contentType: 'application/vnd.apple.mpegurl',
          body: '#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1000000\nvideo1_stream.m3u8\n' }).catch(() => {});
      }
      if (phase === 'auth') return route.fulfill({ status: 401, headers: cors, body: '' });
      if (phase === 'network') return route.abort();
      if (phase === 'offline') return route.fulfill({ status: 404, headers: cors, body: '' });
      return route.fulfill({ status: 200, headers: cors, contentType: 'application/vnd.apple.mpegurl',
        body: '#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1000000\nvideo1_stream.m3u8?session=fixture\n' });
    }
    if (url.pathname.endsWith('/video1_stream.m3u8')) {
      if (phase !== 'ready') return route.fulfill({ status: 404, headers: cors, body: '' });
      part++;
      return route.fulfill({ status: 200, headers: cors, contentType: 'application/vnd.apple.mpegurl',
        body: `#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:${part}\n#EXT-X-PART-INF:PART-TARGET=0.2\n#EXT-X-SERVER-CONTROL:CAN-BLOCK-RELOAD=YES\n#EXT-X-PART:DURATION=0.2,URI="part${part}.mp4?session=fixture"\n` });
    }
    if (/\/part\d+\.mp4$/.test(url.pathname))
      return route.fulfill({ status: phase === 'ready' ? 200 : 404, headers: cors,
        contentType: 'video/mp4', body: Buffer.from([0, 0, 0, 1]) });
  }
  return route.fulfill({ status: 404, headers: cors, body: '' });
});
try {
  await page.goto('http://127.0.0.1:8090/', { waitUntil: 'load' });
  check('separate viewer starts with play and sound disabled',
    await page.locator('#btn-play').isDisabled() && await page.locator('#btn-sound').isDisabled());
  await page.waitForTimeout(2500);
  check('404 keeps playback waiting with visible status',
    await page.locator('#btn-play').isDisabled() &&
    (await page.locator('#play-readiness').textContent()).includes('대기'));
  phase = 'master-only';
  await page.waitForTimeout(2500);
  check('master 200 without media playlist remains disabled',
    await page.locator('#btn-play').isDisabled() && masterRequests >= 2);
  phase = 'ready';
  await page.waitForFunction(() => window.__spikeState?.playbackReady === true,
    null, { timeout: 10000 });
  check('media playlist and part enable play and sound',
    await page.locator('#btn-play').isEnabled() && await page.locator('#btn-sound').isEnabled());
  phase = 'offline';
  await page.waitForFunction(() => window.__spikeState?.playbackReady === false,
    null, { timeout: 10000 });
  check('broadcast end disables old readiness', await page.locator('#btn-play').isDisabled());
  phase = 'ready';
  await page.waitForFunction(() => window.__spikeState?.playbackReady === true,
    null, { timeout: 10000 });
  check('restart requires fresh media readiness', await page.locator('#btn-play').isEnabled());
  phase = 'auth';
  await page.waitForFunction(() => window.__spikeState?.playbackReadiness === 'auth',
    null, { timeout: 10000 });
  check('401 is distinct from 404 and disables playback', await page.locator('#btn-play').isDisabled() &&
    (await page.locator('#play-readiness').textContent()).includes('인증 필요'));
  phase = 'network';
  await page.waitForFunction(() => window.__spikeState?.playbackReadiness === 'network',
    null, { timeout: 10000 });
  check('network failure has a retry state', await page.locator('#btn-play').isDisabled());
  phase = 'slow';
  await page.waitForFunction(() => window.__spikeState?.playbackReadiness === 'slow',
    null, { timeout: 12000 });
  check('slow response times out without enabling playback', await page.locator('#btn-play').isDisabled());
  phase = 'ready';
  await page.waitForFunction(() => window.__spikeState?.playbackReady === true,
    null, { timeout: 10000 });
  await page.waitForTimeout(1500);
  check('late timed-out response cannot replace new readiness', await page.locator('#btn-play').isEnabled());
  await page.evaluate(() => window.dispatchEvent(new PageTransitionEvent('pagehide', { persisted: true })));
  phase = 'offline';
  await page.evaluate(() => window.dispatchEvent(new PageTransitionEvent('pageshow', { persisted: true })));
  let restored = false;
  try {
    await page.waitForFunction(() => window.__spikeState?.playbackReadiness === 'waiting',
      null, { timeout: 5000 });
    restored = true;
  } catch (_) { /* a missing BFCache resume is the red case */ }
  check('BFCache pageshow resumes readiness probing', restored && await page.locator('#btn-play').isDisabled());
} finally {
  await page.close();
  await browser.close();
}
if (results.some(x => !x)) process.exitCode = 1;
