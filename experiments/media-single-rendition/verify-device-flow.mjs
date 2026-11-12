// Synthetic browser → CA-verified local Caddy relay → real MediaMTX.
// The relay keeps the browser fixture isolated; it does not prove browser
// certificate trust, physical devices, native HLS auth, or external ICE.
import { chromium } from 'playwright';
import { readFileSync } from 'node:fs';
import https from 'node:https';

const upstream = process.env.WBB_DEVICE_URL;
const caFile = process.env.WBB_DEVICE_CA;
const user = process.env.WBB_DEVICE_USER;
const password = process.env.WBB_DEVICE_PASSWORD;
if (!upstream || !caFile || !user || !password) {
  console.error('Set WBB_DEVICE_URL/CA/USER/PASSWORD for the isolated stack');
  process.exit(2);
}
const ca = readFileSync(caFile);
const index = readFileSync(new URL('./web/index.html', import.meta.url), 'utf8').replace(
  '<script src="app.js"></script>', '<script src="config.js"></script>\n  <script src="app.js"></script>');
const app = readFileSync(new URL('./web/app.js', import.meta.url));
const hls = readFileSync(new URL('./web/vendor/hls.min.js', import.meta.url));
const origin = new URL(upstream).origin;
const browserOrigin = 'https://device-check.test';
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
const basic = 'Basic ' + Buffer.from(`${user}:${password}`).toString('base64');
const results = [];
function check(label, ok) {
  console.log(`${ok ? 'PASS' : 'FAIL'} ${label}`);
  results.push(ok);
}
function requestCaddy(path, method = 'GET', headers = {}, body = null) {
  return new Promise((resolve, reject) => {
    const url = new URL(path, origin);
    const safeHeaders = { ...headers, authorization: basic };
    delete safeHeaders.host;
    if (['POST', 'PATCH', 'DELETE'].includes(method)) safeHeaders.origin = origin;
    if (body) safeHeaders['content-length'] = body.length;
    const req = https.request(url, { method, headers: safeHeaders, ca, rejectUnauthorized: true, timeout: 12000 }, res => {
      const chunks = [];
      res.on('data', chunk => chunks.push(chunk));
      res.on('end', () => resolve({ status: res.statusCode, headers: res.headers, body: Buffer.concat(chunks) }));
    });
    req.on('timeout', () => req.destroy(new Error('request timeout')));
    req.on('error', reject);
    if (body) req.write(body);
    req.end();
  });
}
let hlsCookie = '';
let hlsRedirectsSafe = true;
async function requestHls(path) {
  for (let i = 0; i < 4; i++) {
    const answer = await requestCaddy(path, 'GET', hlsCookie ? { cookie: hlsCookie } : {});
    const setCookie = answer.headers['set-cookie'];
    if (setCookie) hlsCookie = (Array.isArray(setCookie) ? setCookie[0] : setCookie).split(';')[0];
    if (answer.status !== 302 && answer.status !== 307) return answer;
    const location = new URL(answer.headers.location || '', origin);
    if (location.origin !== origin || !location.pathname.startsWith('/__media-check/hls/')) {
      hlsRedirectsSafe = false;
      return answer;
    }
    path = location.pathname + location.search;
  }
  throw new Error('HLS redirect loop');
}
const browser = await chromium.launch({ headless: true });
const page = await browser.newPage();
const relayMethods = [];
const errors = [];
page.on('pageerror', e => errors.push(e.message));
await page.route('**/*', async route => {
  const req = route.request();
  const url = new URL(req.url());
  try {
    if (url.pathname === '/__media-check/')
      return route.fulfill({ status: 200, contentType: 'text/html', body: index });
    if (url.pathname.endsWith('/config.js'))
      return route.fulfill({ status: 200, contentType: 'application/javascript', body:
        'window.__WBB_MEDIA_CONFIG={whipPath:"/__media-check/whip/spike/source/whip",' +
        'hlsPath:"/__media-check/hls/spike/720p/index.m3u8",' +
        'whipBase:"/__media-check/whip/spike/source/whip"};' });
    if (url.pathname.endsWith('/app.js'))
      return route.fulfill({ status: 200, contentType: 'application/javascript', body: app });
    if (url.pathname.endsWith('/vendor/hls.min.js'))
      return route.fulfill({ status: 200, contentType: 'application/javascript', body: hls });
    if (url.hostname !== 'device-check.test' || !url.pathname.startsWith('/__media-check/'))
      return route.abort();
    relayMethods.push(`${req.method()} ${url.pathname}`);
    const headers = {};
    for (const name of ['content-type', 'if-match']) {
      const value = req.headers()[name];
      if (value) headers[name] = value;
    }
    const answer = await requestCaddy(url.pathname + url.search, req.method(), headers, req.postDataBuffer());
    const responseHeaders = {};
    for (const name of ['content-type', 'location', 'etag', 'accept-patch']) {
      if (answer.headers[name]) responseHeaders[name] = answer.headers[name];
    }
    await route.fulfill({ status: answer.status, headers: responseHeaders, body: answer.body });
  } catch (_) {
    await route.abort();
  }
});
try {
  await page.goto(browserOrigin + '/__media-check/', { waitUntil: 'load' });
  await page.locator('#btn-synth').click();
  let posted = false;
  try {
    await page.waitForFunction(() => window.__spikeState?.whipPost === 201, { timeout: 30000 });
    posted = true;
  } catch (_) { /* report state below */ }
  const state = await page.evaluate(() => window.__spikeState || {});
  check('browser synthetic WHIP POST 201 through trusted Caddy relay', posted);
  check('Caddy Location remains within one stream', typeof state.location === 'string' &&
    state.location.startsWith(browserOrigin + '/__media-check/whip/spike/source/whip/'));
  if (posted) {
    try {
      await page.waitForFunction(() => window.__spikeState?.patchStatus === 204,
        { timeout: 10000 });
    } catch (_) { /* report below */ }
    const beforeStop = await page.evaluate(() => window.__spikeState || {});
    check('browser PATCH reached real WHIP resource', relayMethods.some(x => x.startsWith('PATCH /__media-check/whip/')) &&
      beforeStop.patchStatus === 204);
    const masterPath = '/__media-check/hls/spike/720p/index.m3u8';
    let master;
    let lastHls = '';
    for (let attempt = 0; attempt < 25; attempt++) {
      try {
        const response = await requestHls(masterPath);
        lastHls = `${response.status} location=${response.headers.location || ''} ${response.body.toString().slice(0, 80)}`;
        if (response.status === 200 && response.body.toString().includes('#EXTM3U')) {
          master = response.body.toString();
          break;
        }
      } catch (e) { lastHls = e.message; }
      await sleep(1000);
    }
    check('real HLS playlist through authenticated Caddy', Boolean(master));
    check('HLS redirect stays inside authenticated prefix', hlsRedirectsSafe);
    if (!master) console.log('HLS diagnostic:', lastHls);
    if (master) {
      const variantName = master.split('\n').map(x => x.trim()).find(x => x && !x.startsWith('#'));
      const variantUrl = variantName ? new URL(variantName, new URL(masterPath, origin)) : new URL(masterPath, origin);
      const variantPath = variantUrl.pathname + variantUrl.search;
      const mediaResponse = await requestHls(variantPath);
      const media = mediaResponse.body.toString();
      check('real LL-HLS media playlist', mediaResponse.status === 200 &&
        media.includes('#EXT-X-PART-INF') && media.includes('CAN-BLOCK-RELOAD=YES'));
      const partName = (media.match(/#EXT-X-PART:[^\n]*URI="([^"]+)"/) || [])[1];
      if (partName) {
        const partUrl = new URL(partName, new URL(variantPath, origin));
        const part = await requestHls(partUrl.pathname + partUrl.search);
        check('real HLS part through authenticated Caddy', part.status === 200 && part.body.length > 0);
      } else check('real HLS part through authenticated Caddy', false);
      const sequence = Number((media.match(/#EXT-X-MEDIA-SEQUENCE:(\d+)/) || [])[1]);
      if (Number.isFinite(sequence)) {
        const fullSegments = (media.match(/^#EXTINF:/gm) || []).length;
        const queryUrl = new URL(variantPath, origin);
        queryUrl.searchParams.set('_HLS_msn', String(sequence + fullSegments));
        const query = await requestHls(queryUrl.pathname + queryUrl.search);
        if (query.status !== 200) console.log('HLS query diagnostic:', query.status, query.body.toString().slice(0, 100));
        check('LL-HLS blocking reload query reaches MediaMTX', query.status === 200);
      } else check('LL-HLS blocking reload query reaches MediaMTX', false);
    }
    await page.locator('#btn-stop').click();
    await page.waitForFunction(() => window.__spikeState?.lifecycle === 'idle', { timeout: 15000 });
    const afterStop = await page.evaluate(() => window.__spikeState || {});
    check('browser DELETE reaches WHIP resource (2xx or already gone 404)',
      relayMethods.some(x => x.startsWith('DELETE /__media-check/whip/')) &&
      ((afterStop.deleteStatus >= 200 && afterStop.deleteStatus < 300) || afterStop.deleteStatus === 404));
    check('browser local capture and peer connection close', afterStop.tracksStopped === true &&
      afterStop.pcClosed === true && afterStop.acClosed === true);
  }
  check('no browser page error', errors.length === 0);
  check('no localhost browser request', relayMethods.every(x => !/localhost|127\.0\.0\.1/.test(x)));
  if (!posted) console.log('WHIP state:', JSON.stringify({ lifecycle: state.lifecycle, whipPost: state.whipPost, lastEvent: state.lastEvent }));
} finally {
  await page.close();
  await browser.close();
}
if (results.some(x => !x)) process.exitCode = 1;
