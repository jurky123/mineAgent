import { chromium } from 'playwright';
import http from 'node:http';
import { readFile } from 'node:fs/promises';
import { mkdir } from 'node:fs/promises';

const [video, prefix, ...marks] = process.argv.slice(2);
await mkdir('/tmp/opencode/frames', { recursive: true });
const data = await readFile(video);
const srv = http.createServer((req, res) => {
  if (req.url.includes('.webm')) {
    res.writeHead(200, { 'Content-Type': 'video/webm', 'Content-Length': data.length });
    res.end(data);
    return;
  }
  res.writeHead(200, { 'Content-Type': 'text/html' });
  res.end('<body style="margin:0;background:#000"><video id="v" src="/v.webm" style="width:1440px" muted autoplay></video></body>');
});
await new Promise((r) => srv.listen(18899, r));
const b = await chromium.launch({ args: ['--autoplay-policy=no-user-gesture-required'] });
const p = await b.newPage({ viewport: { width: 1440, height: 900 } });
await p.goto('http://127.0.0.1:18899/');
await p.waitForFunction(() => document.getElementById('v').duration > 0, null, { timeout: 15000 });
const dur = await p.evaluate(() => document.getElementById('v').duration);
console.log('duration', dur.toFixed(1), 's');
await p.evaluate(() => document.getElementById('v').play());
let last = 0;
for (const m of marks.map(Number).sort((a, b2) => a - b2)) {
  await p.waitForTimeout(Math.max(0, (m - last) * 1000));
  last = m;
  const out = `/tmp/opencode/frames/${prefix}-${m}.png`;
  await p.screenshot({ path: out });
  const t = await p.evaluate(() => document.getElementById('v').currentTime);
  console.log('frame', out, 'at', t.toFixed(1));
}
await b.close();
srv.close();
