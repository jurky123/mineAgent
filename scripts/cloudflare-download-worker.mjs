// Paste this complete module into the Cloudflare Worker editor, replacing Hello World.
// This URL is used only on the server side and is never included in the served page.
const ORIGIN = 'https://zkun.art';

const HEADERS = {
  'Cache-Control': 'no-store, private',
  'Referrer-Policy': 'no-referrer',
  'X-Content-Type-Options': 'nosniff',
  'X-Robots-Tag': 'noindex, nofollow, noarchive',
};

const PAGE = String.raw`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="robots" content="noindex,nofollow,noarchive">
<meta name="referrer" content="no-referrer"><title>文件下载</title>
<style>
*{box-sizing:border-box}body{margin:0;background:#f6f7f9;color:#15171a;font:16px/1.7 system-ui,sans-serif}
main{max-width:650px;margin:12vh auto;padding:24px}section{padding:28px;background:white;border:1px solid #e3e5e8;border-radius:16px}
h1{font-size:25px;overflow-wrap:anywhere}button{border:0;border-radius:10px;background:#2563eb;color:white;padding:12px 24px;font:inherit;cursor:pointer}
small{display:block;color:#5e6878;margin-top:24px}code{display:block;word-break:break-all;margin:12px 0}details{margin:20px 0}[hidden]{display:none}
@media(prefers-color-scheme:dark){body{background:#151619;color:#eee}section{background:#202125;border-color:#35363b}small{color:#b3b8c2}}
</style></head><body><main><section>
<h1 id="name">文件下载</h1><p id="status" role="status">正在读取文件信息…</p><p id="size"></p>
<details id="checksum" hidden><summary>SHA256 校验值</summary><code id="hash"></code></details>
<form id="form" action="/api/downloads/file" method="post" hidden>
<input id="key" type="hidden" name="key"><button type="submit">下载文件</button></form>
<small>下载请求会记录次数、时间和网络连接信息。请使用发送者提供的完整链接。</small>
<noscript>请启用 JavaScript 后重新打开原下载链接。</noscript>
</section></main><script>
const $=id=>document.getElementById(id);
async function load(){
  const key=location.hash.slice(1);
  $('form').hidden=true;$('key').value='';$('checksum').hidden=true;
  $('name').textContent='文件下载';$('size').textContent='';
  $('status').textContent='正在读取文件信息…';
  if(!/^[a-f0-9]{64}$/.test(key)){$('status').textContent='链接不完整，请使用发送者提供的完整链接。';return;}
  try{
    const response=await fetch('/api/downloads/info',{method:'POST',body:new URLSearchParams({key}),credentials:'omit',cache:'no-store'});
    const file=await response.json();
    if(location.hash.slice(1)!==key)return;
    if(!response.ok)throw new Error(file.error||'文件暂不可用');
    $('name').textContent=file.name;$('size').textContent=file.size+' 字节';
    $('hash').textContent=file.sha256;$('checksum').hidden=false;
    $('key').value=key;$('form').hidden=false;$('status').textContent='文件已就绪。';
  }catch(error){if(location.hash.slice(1)===key)$('status').textContent=error.message||'连接失败，请稍后重试。';}
}
$('form').addEventListener('submit',()=>{$('status').textContent='已请求下载，请查看浏览器的下载列表。';});
window.addEventListener('hashchange',load);load();
</script></body></html>`;

function error(status, message) {
  return new Response(JSON.stringify({ error: message }), {
    status, headers: { ...HEADERS, 'Content-Type': 'application/json; charset=utf-8' },
  });
}

async function relayDailyKey(request) {
  if (request.method !== 'POST') return error(405, '只支持 POST');
  if (request.headers.get('Origin')) return error(403, '此接口仅供通信程序使用');
  const authorization = request.headers.get('Authorization') || '';
  if (!/^Bearer [a-f0-9]{64}$/.test(authorization)) return error(403, '程序未授权或已停用');
  const reader = request.body?.getReader();
  if (!reader) return error(400, '请求格式无效');
  let text = '', count = 0;
  const decoder = new TextDecoder();
  try {
    while (true) {
      const { value, done } = await reader.read(); if (done) break;
      count += value.byteLength;
      if (count > 2048) { await reader.cancel(); return error(413, '请求过长'); }
      text += decoder.decode(value, { stream: true });
    }
    text += decoder.decode();
    const body = JSON.parse(text);
    if (Object.keys(body).length !== 1 || !/^[a-f0-9]{16}$/.test(body.group)) return error(400, '请求格式无效');
    const response = await fetch(ORIGIN + '/api/textcrypt/key', {
      method: 'POST', headers: { 'Content-Type': 'application/json', Authorization: authorization },
      body: JSON.stringify({ group: body.group }), redirect: 'manual', cache: 'no-store', signal: request.signal,
    });
    if (response.status !== 200) {
      await response.body?.cancel();
      if (response.status === 403) return error(403, '程序未授权或已停用');
      if (response.status === 410) return error(410, '当天密钥已被管理员清除');
      return error(502, '密钥服务暂不可用');
    }
    const data = await response.json();
    if (data.group !== body.group || !/^\d{4}-\d{2}-\d{2}$/.test(data.day) || !/^[A-Za-z0-9+/]{43}=$/.test(data.key) || !Number.isSafeInteger(data.serverTime) || !Number.isSafeInteger(data.expiresAt)) return error(502, '密钥服务返回无效数据');
    return new Response(JSON.stringify({ group: data.group, day: data.day, serverTime: data.serverTime, expiresAt: data.expiresAt, key: data.key }), { headers: { ...HEADERS, 'Content-Type': 'application/json; charset=utf-8' } });
  } catch { return error(502, '密钥服务连接失败'); }
  finally { reader.releaseLock(); }
}

async function keyFrom(request) {
  if (!(request.headers.get('content-type') || '').startsWith('application/x-www-form-urlencoded')) return '';
  if (Number(request.headers.get('content-length')) > 4096) return '';
  const reader = request.body?.getReader();
  if (!reader) return '';
  const decoder = new TextDecoder();
  let text = '', size = 0;
  try {
    while (true) {
      const { value, done } = await reader.read();
      if (done) break;
      size += value.byteLength;
      if (size > 4096) { await reader.cancel(); return ''; }
      text += decoder.decode(value, { stream: true });
    }
    text += decoder.decode();
  } finally { reader.releaseLock(); }
  const key = new URLSearchParams(text).get('key') || '';
  return /^[a-f0-9]{64}$/.test(key) ? key : '';
}

export default {
  async fetch(request) {
    const url = new URL(request.url);
    if (url.protocol !== 'https:') return error(426, '请使用 HTTPS');
    if (url.search) return error(400, '链接格式无效');
    if (url.pathname === '/api/textcrypt/key') return relayDailyKey(request);
    if (url.pathname === '/' || url.pathname === '/download') {
      if (!['GET', 'HEAD'].includes(request.method)) return error(405, '只支持 GET / HEAD');
      return new Response(request.method === 'HEAD' ? null : PAGE, { headers: {
        ...HEADERS, 'Content-Type': 'text/html; charset=utf-8',
        'Content-Security-Policy': "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'",
      } });
    }
    if (url.pathname === '/robots.txt') return new Response('User-agent: *\nDisallow: /\n', { headers: { ...HEADERS, 'Content-Type': 'text/plain' } });
    // Never relay arbitrary paths, management APIs, cookies, Authorization or browser headers.
    if (!['/api/downloads/info', '/api/downloads/file'].includes(url.pathname)) return error(404, '页面不存在');
    if (request.method !== 'POST') return error(405, '请从下载页面点击下载');
    const origin = request.headers.get('Origin');
    if ((origin && origin !== 'null' && origin !== url.origin) || request.headers.get('Sec-Fetch-Site') === 'cross-site') return error(403, '不允许跨站请求');
    let key;
    try { key = await keyFrom(request); } catch { return error(400, '请求无效'); }
    if (!key) return error(404, '链接无效或已停用');
    try {
      const upstream = await fetch(ORIGIN + url.pathname, {
        method: 'POST', body: new URLSearchParams({ key }),
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        redirect: 'manual', cache: 'no-store', signal: request.signal,
      });
      if (upstream.status !== 200) {
        await upstream.body?.cancel();
        return upstream.status === 404 ? error(404, '链接无效、已停用或文件不可用') : error(502, '下载服务暂不可用，请稍后重试');
      }
      if (url.pathname.endsWith('/info')) {
        const data = await upstream.json();
        if (typeof data.name !== 'string' || !Number.isSafeInteger(data.size) || data.size <= 0 || !/^[a-f0-9]{64}$/.test(data.sha256)) return error(502, '文件信息暂不可用');
        return new Response(JSON.stringify({ name: data.name, size: data.size, sha256: data.sha256 }), { headers: { ...HEADERS, 'Content-Type': 'application/json; charset=utf-8' } });
      }
      if (!(upstream.headers.get('content-type') || '').startsWith('application/octet-stream')) {
        await upstream.body?.cancel(); return error(502, '文件暂不可用');
      }
      const headers = new Headers({ ...HEADERS, 'Content-Type': 'application/octet-stream' });
      for (const name of ['Content-Disposition', 'Content-Length']) {
        const value = upstream.headers.get(name); if (value) headers.set(name, value);
      }
      // Stream directly: do not buffer/cache the tool or emit origin response headers/redirects.
      return new Response(upstream.body, { headers });
    } catch { return error(502, '下载服务连接失败，请稍后重试'); }
  },
};
