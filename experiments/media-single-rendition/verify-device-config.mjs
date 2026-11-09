// Browser seam: HTTPS endpoint configuration and WHIP resource URLs.
// All page assets are fulfilled locally; this does not test public TLS or ICE.
import { chromium } from 'playwright';
import { readFileSync } from 'node:fs';

const index = readFileSync(new URL('./web/index.html', import.meta.url), 'utf8');
const app = readFileSync(new URL('./web/app.js', import.meta.url), 'utf8');
const hls = readFileSync(new URL('./web/vendor/hls.min.js', import.meta.url));
const base = 'https://device-check.test/__media-check/';
const good = {
  whipPath: '/__media-check/whip/spike/source/whip',
  hlsPath: '/__media-check/hls/spike/720p/index.m3u8',
  whipBase: '/__media-check/whip/spike/source/whip',
};
const browser = await chromium.launch({ headless: true });
let passed = 0;
function check(label, condition) {
  console.log(`${condition ? 'PASS' : 'FAIL'} ${label}`);
  if (!condition) process.exitCode = 1;
  else passed++;
}
async function open(config) {
  const page = await browser.newPage();
  const errors = [];
  const requests = [];
  page.on('pageerror', e => errors.push(e.message));
  page.on('request', r => requests.push(r.url()));
  await page.route('**/*', async route => {
    const url = new URL(route.request().url());
    if (url.pathname === '/__media-check/' || url.pathname === '/__media-check/index.html') {
      const html = config === undefined ? index : index.replace(
        '<script src="app.js"></script>', '<script src="config.js"></script>\n  <script src="app.js"></script>');
      return route.fulfill({ status: 200, contentType: 'text/html', body: html });
    }
    if (url.pathname.endsWith('/config.js'))
      return route.fulfill({ status: 200, contentType: 'application/javascript',
        body: `window.__WBB_MEDIA_CONFIG = ${JSON.stringify(config)};` });
    if (url.pathname.endsWith('/app.js'))
      return route.fulfill({ status: 200, contentType: 'application/javascript', body: app });
    if (url.pathname.endsWith('/vendor/hls.min.js'))
      return route.fulfill({ status: 200, contentType: 'application/javascript', body: hls });
    if (url.pathname === '/__media-check/hls/spike/720p/index.m3u8')
      return route.fulfill({ status: 200, contentType: 'application/vnd.apple.mpegurl',
        body: '#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1000000\nvideo1_stream.m3u8?session=fixture\n' });
    if (url.pathname === '/__media-check/hls/spike/720p/video1_stream.m3u8')
      return route.fulfill({ status: 200, contentType: 'application/vnd.apple.mpegurl',
        body: '#EXTM3U\n#EXT-X-PART-INF:PART-TARGET=0.2\n#EXT-X-SERVER-CONTROL:CAN-BLOCK-RELOAD=YES\n' });
    return route.fulfill({ status: 404, body: '' });
  });
  await page.goto(base, { waitUntil: 'load' });
  return { page, errors, requests };
}
try {
  let seen = await open(undefined);
  check('HTTPS without runtime config fails closed', seen.errors.some(x => x.includes('missing on HTTPS page')));
  check('missing config makes no localhost request', seen.requests.every(x => !/localhost|127\.0\.0\.1/.test(x)));
  await seen.page.close();

  for (const [name, config] of [
    ['external WHIP URL', { ...good, whipPath: 'https://evil.test/whip' }],
    ['encoded path traversal', { ...good, hlsPath: '/__media-check/hls/%2e%2e/admin' }],
    ['wrong WHIP base', { ...good, whipBase: '/__media-check/whip' }],
  ]) {
    seen = await open(config);
    check(`${name} rejected in browser`, seen.errors.some(x => x.includes('__WBB_MEDIA_CONFIG invalid')));
    await seen.page.close();
  }

  seen = await open(good);
  check('valid HTTPS config initializes controls', seen.errors.length === 0 &&
    await seen.page.locator('#btn-synth').isEnabled());
  await seen.page.evaluate(() => manifestProbe());
  check('HTTPS manifest probe resolves relative variant and observes LL-HLS',
    (await seen.page.evaluate(() => window.__spikeState?.manifestLL)) === true &&
    seen.requests.some(x => x.includes('/__media-check/hls/spike/720p/video1_stream.m3u8?session=fixture')));
  const locations = await seen.page.evaluate(() => {
    const values = [
      '/__media-check/whip/spike/source/whip/session1',
      '/__media-check/whip/other/source/whip/session1',
      'https://evil.test/__media-check/whip/spike/source/whip/session1',
      '/__media-check/whip/spike/source/whip/%2e%2e/admin',
    ];
    return values.map(value => {
      try { return { ok: true, resolved: resolveWhipLocation(value) }; }
      catch (_) { return { ok: false }; }
    });
  });
  check('valid WHIP resource resolves at same origin', locations[0].ok &&
    locations[0].resolved === 'https://device-check.test/__media-check/whip/spike/source/whip/session1');
  check('other stream, external host, traversal are rejected', locations.slice(1).every(x => !x.ok));
  check('valid config makes no localhost request', seen.requests.every(x => !/localhost|127\.0\.0\.1/.test(x)));
  await seen.page.close();
} finally {
  await browser.close();
}
console.log(`${passed} browser checks passed`);
