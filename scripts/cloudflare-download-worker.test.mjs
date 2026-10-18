import assert from 'node:assert/strict';
import { test } from 'node:test';
import module from './cloudflare-download-worker.mjs';
import { createHmac, createHash } from 'node:crypto';

const base = 'https://download.example.workers.dev';
const key = 'a'.repeat(64);
const env = { DOWNLOAD_PROXY_SECRET: 'ab'.repeat(32) };
const worker = { fetch: (request, bindings = env) => module.fetch(request, bindings) };
function request(path, options = {}) {
  const headers = new Headers({ 'Content-Type': 'application/x-www-form-urlencoded', 'CF-Connecting-IP': '198.51.100.8' });
  for (const [key, value] of new Headers(options.headers)) headers.set(key, value);
  return new Request(base + path, { method: 'POST', body: new URLSearchParams({ key }), ...options, headers });
}

test('standalone page contains no origin address, site branding or external assets', async () => {
  const response = await worker.fetch(new Request(base + '/download'));
  const html = await response.text();
  assert.equal(response.status, 200);
  assert.doesNotMatch(html, /zkun|https?:\/\//i);
  assert.match(html, /action="\/api\/downloads\/file"/);
  assert.match(response.headers.get('content-security-policy'), /connect-src 'self'/);
  const head = await worker.fetch(new Request(base + '/download', { method: 'HEAD' }));
  assert.equal(await head.text(), '');
});

test('reject unsupported paths, methods, cross-site requests, malformed keys and oversized bodies', async () => {
  for (const [req, expected] of [
    [request('/api/downloads'), 404],
    [request('/api/workspace/file'), 404],
    [request('/https://another.example'), 404],
    [new Request(base + '/api/downloads/file'), 405],
    [request('/api/downloads/file?key=' + key), 400],
    [request('/api/downloads/file', { headers: { Origin: 'https://another.example' } }), 403],
    [request('/api/downloads/file', { headers: { 'Sec-Fetch-Site': 'cross-site' } }), 403],
    [request('/api/downloads/file', { body: new URLSearchParams({ key: 'invalid' }) }), 404],
    [request('/api/downloads/file', { body: new URLSearchParams({ key, filler: 'x'.repeat(4200) }) }), 404],
    [new Request('http://download.example.workers.dev/download'), 426],
  ]) assert.equal((await worker.fetch(req)).status, expected);
});

test('relay only explicit key, strip browser identity headers, and return only metadata fields', async (t) => {
  t.mock.method(globalThis, 'fetch', async (url, options) => {
    assert.equal(url, 'https://zkun.art/api/downloads/info');
    assert.equal(options.redirect, 'manual');
    assert.equal(options.cache, 'no-store');
    const headers = new Headers(options.headers);
    for (const name of ['Cookie', 'Authorization', 'Origin', 'Referer', 'Sec-Fetch-Site', 'X-Forwarded-For']) assert.equal(headers.has(name), false);
    assert.equal(options.body.toString(), 'key=' + key);
    return Response.json({ name: 'tool.bin', size: 12, sha256: 'b'.repeat(64), extra: 'private-origin-details' });
  });
  const response = await worker.fetch(request('/api/downloads/info', { headers: {
    'Content-Type': 'application/x-www-form-urlencoded', Origin: base, Cookie: 'fixture-cookie', Authorization: 'fixture-authorization', 'Sec-Fetch-Site': 'same-origin',
  } }));
  assert.equal(response.status, 200);
  const data = await response.json();
  assert.deepEqual(Object.keys(data).sort(), ['name', 'sha256', 'size']);
});

test('native no-referrer forms work and file stream/filename are preserved without origin headers', async (t) => {
  t.mock.method(globalThis, 'fetch', async () => new Response('tool contents', { headers: {
    'Content-Type': 'application/octet-stream', 'Content-Disposition': 'attachment; filename="tool.bin"',
    'Content-Length': '13', 'Set-Cookie': 'origin-cookie', 'X-Origin-URL': 'https://zkun.art',
  } }));
  const response = await worker.fetch(request('/api/downloads/file', { headers: { 'Content-Type': 'application/x-www-form-urlencoded', Origin: 'null' } }));
  assert.equal(response.status, 200);
  assert.equal(await response.text(), 'tool contents');
  assert.equal(response.headers.get('Content-Disposition'), 'attachment; filename="tool.bin"');
  assert.equal(response.headers.get('Cache-Control'), 'no-store, private');
  assert.equal(response.headers.has('Set-Cookie'), false);
  assert.equal(response.headers.has('X-Origin-URL'), false);
});

test('never forward origin redirects or raw error pages', async (t) => {
  for (const status of [301, 302, 404, 500, 503]) {
    t.mock.method(globalThis, 'fetch', async () => new Response('origin error https://zkun.art', { status, headers: { Location: 'https://zkun.art/' } }));
    const response = await worker.fetch(request('/api/downloads/file'));
    assert.equal(response.status, status === 404 ? 404 : 502);
    assert.equal(response.headers.has('Location'), false);
    assert.doesNotMatch(await response.text(), /zkun/i);
    t.mock.restoreAll();
  }
});

test('sign the Cloudflare visitor address, bound to method/path/token, and ignore forged identity headers', async (t) => {
  const secret = 'ab'.repeat(32), ip = '2001:db8::8';
  t.mock.method(globalThis, 'fetch', async (url, options) => {
    const headers = new Headers(options.headers);
    assert.equal(headers.get('X-Download-Client-IP'), ip);
    const stamp = headers.get('X-Download-Timestamp');
    assert.ok(Math.abs(Number(stamp) - Date.now() / 1000) < 5);
    const tokenHash = createHash('sha256').update(key).digest('hex');
    const expected = createHmac('sha256', Buffer.from(secret, 'hex')).update(['v1', stamp, 'POST', '/api/downloads/file', ip, tokenHash].join('\n')).digest('hex');
    assert.equal(headers.get('X-Download-Signature'), expected);
    assert.equal(headers.has('X-Forwarded-For'), false);
    assert.equal(headers.has('CF-Connecting-IP'), false);
    assert.ok(!JSON.stringify(options.headers).includes(secret));
    return new Response('tool', { headers: { 'Content-Type': 'application/octet-stream' } });
  });
  const response = await worker.fetch(request('/api/downloads/file', { headers: {
    'Content-Type': 'application/x-www-form-urlencoded', 'CF-Connecting-IP': ip,
    'X-Forwarded-For': '198.51.100.99', 'X-Download-Client-IP': '198.51.100.99', 'X-Download-Signature': 'forged',
  } }), { DOWNLOAD_PROXY_SECRET: secret });
  assert.equal(response.status, 200);
  assert.equal(await response.text(), 'tool');
  assert.equal(response.headers.has('X-Download-Signature'), false);
});

test('a configured signer never substitutes browser forwarding headers for a missing Cloudflare IP', async (t) => {
  let called = false;
  t.mock.method(globalThis, 'fetch', async () => { called = true; return new Response('unexpected'); });
  const req = request('/api/downloads/file', { headers: {
    'Content-Type': 'application/x-www-form-urlencoded', 'X-Forwarded-For': '198.51.100.99',
  } });
  req.headers.delete('CF-Connecting-IP');
  const response = await worker.fetch(req, { DOWNLOAD_PROXY_SECRET: 'ab'.repeat(32) });
  assert.equal(response.status, 502);
  assert.equal(called, false);
});

test('missing or malformed signing configuration is explicit and never silently records an origin IP', async (t) => {
  let calls = 0;
  t.mock.method(globalThis, 'fetch', async () => { calls++; return new Response('unexpected'); });
  for (const bindings of [{}, { DOWNLOAD_PROXY_SECRET: 'invalid' }]) {
    const response = await worker.fetch(request('/api/downloads/file'), bindings);
    assert.equal(response.status, 503);
    assert.match((await response.json()).error, /配置未完成/);
  }
  assert.equal(calls, 0);
});

test('public status reports only the deployed version and configuration state, without keys or origin', async () => {
  for (const [bindings, signedIP] of [[env, true], [{}, false]]) {
    const response = await worker.fetch(new Request(base + '/api/downloads/status'), bindings);
    assert.equal(response.status, 200);
    assert.deepEqual(await response.json(), { version: 'ip-signature-20261018-v3', signedIP });
    assert.match(response.headers.get('Cache-Control'), /no-store/);
  }
});

test('daily key relay forwards only program credential and batch, never messages or admin paths', async (t) => {
  const group='0123456789abcdef', token='a'.repeat(64), key=Buffer.alloc(32,7).toString('base64');
  let calls=0;
  t.mock.method(globalThis,'fetch',async(url,options)=>{
    calls++;
    assert.equal(url,'https://zkun.art/api/textcrypt/key');
    assert.deepEqual(JSON.parse(options.body),{group});
    const headers=new Headers(options.headers);
    assert.equal(headers.get('Authorization'),'Bearer '+token);
    assert.equal(headers.has('Cookie'),false);
    assert.equal(options.redirect,'manual');
    return Response.json({group,day:'2026-10-18',serverTime:1792300000000,expiresAt:1792339200000,key,origin:'https://zkun.art'});
  });
  const make=(body)=>new Request(base+'/api/textcrypt/key',{method:'POST',headers:{Authorization:'Bearer '+token,'Content-Type':'application/json',Cookie:'not-forwarded'},body:JSON.stringify(body)});
  const response=await worker.fetch(make({group}));
  assert.equal(response.status,200);
  assert.match(response.headers.get('Cache-Control'),/no-store/);
  const data=await response.json();assert.equal(data.key,key);assert.equal(data.origin,undefined);
  assert.equal((await worker.fetch(make({group,plaintext:'must-not-forward'}))).status,400);
  assert.equal(calls,1);
  assert.equal((await worker.fetch(new Request(base+'/api/textcrypt/key'))).status,405);
  assert.equal((await worker.fetch(new Request(base+'/api/textcrypt/admin'))).status,404);
});
