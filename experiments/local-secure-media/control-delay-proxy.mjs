// Test-only private MediaMTX API proxy. It delays a bounded number of kick
// responses to prove one STOPPING session cannot exhaust another's lease.
import http from 'node:http';

let remaining = Number(process.env.WBB_DELAY_KICKS || 0);
const delay = Number(process.env.WBB_DELAY_MS || 0);
if (!Number.isInteger(remaining) || remaining < 0 || remaining > 20 ||
    !Number.isInteger(delay) || delay < 0 || delay > 6000) {
  throw new Error('invalid isolated delay configuration');
}

http.createServer(async (request, response) => {
  try {
    if (request.method === 'POST' && /^\/v3\/(webrtcsessions|rtspsessions)\/kick\//.test(request.url) && remaining > 0) {
      remaining--;
      await new Promise(resolve => setTimeout(resolve, delay));
    }
    const upstream = await fetch(`http://mediamtx:9997${request.url}`, {
      method: request.method,
      headers: {Authorization: request.headers.authorization || ''},
      signal: AbortSignal.timeout(10000),
    });
    response.writeHead(upstream.status, {'Content-Type': upstream.headers.get('content-type') || 'application/json'});
    response.end(Buffer.from(await upstream.arrayBuffer()));
  } catch {
    response.writeHead(502);
    response.end();
  }
}).listen(9998, '0.0.0.0');
