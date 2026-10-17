// Model selector regression checks with local static files and mock APIs only.
import assert from 'node:assert/strict';
import { readFile, mkdir } from 'node:fs/promises';
import { createServer } from 'node:http';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from 'playwright';
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const staticRoot = path.join(root, 'internal/webui/static');
let selection = '', refreshes = 0, failRefresh = false;
const models = [
  { id: 'old', label: 'Old', provider: 'default', providerLabel: 'Gateway', created: 100 },
  { id: 'new-free', label: 'New Free', provider: 'default', providerLabel: 'Gateway', created: 200, free: true, verified: true },
  { id: 'blocked-free', label: 'Blocked', provider: 'default', providerLabel: 'Gateway', created: 250, free: true, unavailable: true, unavailableReason: '网关暂时无法路由' },
  { id: 'extra/needs-key', label: 'Needs Key', provider: 'extra', providerLabel: 'Extra', free: true, needsKey: true },
  { id: 'contributor', label: 'Contributor', provider: 'default', providerLabel: 'Gateway', free: false },
];
const types = { '.html': 'text/html', '.js': 'text/javascript', '.mjs': 'text/javascript', '.css': 'text/css', '.svg': 'image/svg+xml', '.png': 'image/png', '.woff2': 'font/woff2' };
const server = createServer(async (req, res) => {
  try {
    const url = new URL(req.url, 'http://localhost');
    const json = (value, code = 200) => { res.writeHead(code, { 'Content-Type': 'application/json' }); res.end(JSON.stringify(value)); };
    if (url.pathname === '/api/options') {
      if (url.searchParams.get('refresh') === '1') {
        refreshes++;
        if (failRefresh) return json({ error: '模拟刷新失败' }, 503);
        if (!models.some((m) => m.id === 'fresh')) models.push({ id: 'fresh', label: 'Fresh', provider: 'default', providerLabel: 'Gateway', created: 300, free: true });
      }
      return json({ models, skills: [], model: selection, defaultModel: 'old', effort: 'medium' });
    }
    if (url.pathname === '/api/prefs') {
      let body = ''; for await (const chunk of req) body += chunk;
      selection = JSON.parse(body).model; return json({ model: selection, effort: 'medium' });
    }
    if (url.pathname === '/api/me' || url.pathname === '/api/auth/me') return json({ name: 'Preview', id: 1, admin: true });
    if (url.pathname === '/api/version') return json({ version: 'preview' });
    if (url.pathname === '/api/conversations') return json({ conversations: [] });
    if (url.pathname === '/api/history') return json({ messages: [] });
    if (url.pathname === '/api/events') {
      res.writeHead(200, { 'Content-Type': 'text/event-stream' }); res.write(': preview\n\n');
      req.on('close', () => res.end()); return;
    }
    if (url.pathname.startsWith('/api/')) return json({ sessions: [], games: [], apps: [] });
    const pages = { '/agent': 'chat/index.html', '/account': 'account/index.html', '/': 'portal/index.html' };
    const rel = pages[url.pathname] || url.pathname.replace(/^\/static\/preview\//, '');
    const file = path.resolve(staticRoot, rel);
    if (!file.startsWith(staticRoot + path.sep)) { res.writeHead(404); res.end(); return; }
    let content = await readFile(file);
    if (file.endsWith('.html')) content = Buffer.from(content.toString().replaceAll('__VER__', 'preview'));
    res.writeHead(200, { 'Content-Type': types[path.extname(file)] || 'application/octet-stream' }); res.end(content);
  } catch { res.writeHead(404); res.end(); }
});
await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
const base = 'http://127.0.0.1:' + server.address().port;
let browser;
try {
  browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ viewport: { width: 1440, height: 960 } });
  await context.addInitScript(() => {
    localStorage.setItem('mineagent.token', 'preview'); localStorage.setItem('mineagent.name', 'Preview');
  });
  const page = await context.newPage();
  const errors = []; page.on('pageerror', (e) => errors.push(e.message));
  await page.goto(base + '/agent');
  await page.evaluate(async () => {
    const c = await import('/static/preview/js/composer.js'); await c.loadOptions(); c.openModelPopover(true);
  });
  const rows = page.locator('#po-list .po-modelbody .po-modelname');
  assert.match(await rows.first().textContent(), /New Free/);
  assert.equal(await rows.count(), 3, 'known unavailable models should be hidden');
  await page.locator('#po-freeonly').click();
  assert.equal(await rows.count(), 1);
  assert.match(await rows.first().textContent(), /免费/);
  await page.locator('#po-showall').click();
  assert.equal(await page.locator('#po-list button:disabled').count(), 2);
  await page.locator('#po-filter').fill('needs-key');
  assert.equal(await rows.count(), 1);
  await page.locator('#po-filter').fill('');
  await page.locator('#po-showall').click();
  failRefresh = true;
  await page.locator('#po-refresh').click();
  await page.locator('#po-refresh:not(:disabled)').waitFor();
  assert.equal(await rows.count(), 1, 'refresh failure erased options');
  failRefresh = false;
  await page.locator('#po-refresh').click();
  await page.getByRole('button', { name: /Fresh/ }).waitFor();
  assert.match(await rows.first().textContent(), /Fresh/);
  assert.equal(refreshes, 2);
  await page.getByRole('button', { name: /Fresh/ }).click();
  await page.waitForFunction(() => document.querySelector('.po-item.model.on')?.textContent.includes('Fresh'));
  assert.equal(selection, 'fresh');
  await page.locator('#po-freeonly').click();
  await page.locator('#po-sort').selectOption('name');
  assert.match(await rows.first().textContent(), /Contributor/);
  await page.locator('#po-sort').selectOption('newest');
  await mkdir('/tmp/mineagent-models-ui', { recursive: true });
  await page.screenshot({ path: '/tmp/mineagent-models-ui/desktop.png' });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(base + '/agent');
  await page.evaluate(async () => {
    const c = await import('/static/preview/js/composer.js'); await c.loadOptions(); c.openModelPopover(true);
  });
  await page.locator('#po-filter').waitFor();
  await page.locator('#po-freeonly').click();
  await page.locator('#po-freeonly').click();
  assert.ok(await page.locator('#popover').evaluate((el) => el.getBoundingClientRect().right <= innerWidth));
  await page.screenshot({ path: '/tmp/mineagent-models-ui/mobile.png' });
  await page.goto(base + '/agent?ui=1&panel=models');
  await page.locator('#po-list .po-modelbody').first().waitFor();
  await page.locator('#po-refresh').click();
  await page.locator('#po-refresh:not(:disabled)').waitFor();
  assert.equal(refreshes, 2, 'preview contacted live options API');
  assert.deepEqual(errors, []);
  console.log('Model UI checks passed (sorting, free/availability filters, refresh, failure, selection, mobile, preview).');
} finally {
  if (browser) await browser.close();
  server.closeAllConnections(); await new Promise((resolve) => server.close(resolve));
}
