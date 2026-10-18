import assert from 'node:assert/strict';
import { test } from 'node:test';
import worker from './cloudflare-download-worker.mjs';

const base = 'https://download.example.workers.dev';
const key = 'a'.repeat(64);
function request(path, options = {}) {
  return new Request(base + path, { method: 'POST', body: new URLSearchParams({ key }), ...options });
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
