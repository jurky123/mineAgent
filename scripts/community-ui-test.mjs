// Site experience checks against a temporary static server with mock APIs.
import assert from 'node:assert/strict';
import { readFile, mkdir } from 'node:fs/promises';
import { createServer } from 'node:http';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from 'playwright';
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const staticRoot = path.join(root, 'internal/webui/static');
let loggedIn = false, failRelease = false, sends = 0;
const types = { '.html': 'text/html', '.js': 'text/javascript', '.mjs': 'text/javascript', '.css': 'text/css', '.svg': 'image/svg+xml', '.png': 'image/png', '.woff2': 'font/woff2' };
const server = createServer(async (req, res) => {
  try {
    const url = new URL(req.url, 'http://localhost');
    const json = (value, code = 200) => { res.writeHead(code, { 'Content-Type': 'application/json' }); res.end(JSON.stringify(value)); };
    if (url.pathname === '/api/me' || url.pathname === '/api/auth/me') return json(loggedIn ? { name: 'Friend', id: 1, admin: true } : { error: '未登录' }, loggedIn ? 200 : 401);
    if (url.pathname === '/api/portal/apps') return json({ apps: [{ id: 'agent', name: 'Agent', path: '/agent', nav: true, icon: 'sparkle' }, { id: 'games', name: '游戏', path: '/games', nav: true, icon: 'gamepad' }] });
    if (url.pathname === '/api/portal/home') return json({ greeting: '晚上好，Friend', apps: [{ id: 'agent', role: 'hero', card: { count: 1, lastTitle: '上次的真实测试对话', continuePath: '/agent?conv=test' } }] });
    if (url.pathname === '/api/version') return json({ version: 'preview' });
    if (url.pathname === '/api/conversations') return json({ conversations: [] });
    if (url.pathname === '/api/history') return json({ messages: [] });
    if (url.pathname === '/api/options') return json({ models: [], skills: [], defaultModel: 'demo' });
    if (url.pathname === '/api/prompts') return json({ personas: [] });
    if (url.pathname === '/api/send') { sends++; return json({}); }
    if (url.pathname.endsWith('/minecraft/release.json')) {
      if (failRelease) return json({ error: 'unavailable' }, 503);
      return json({ name: 'Minecraft 服务器', status: 'preparing', release: null });
    }
    if (url.pathname === '/api/events') {
      res.writeHead(200, { 'Content-Type': 'text/event-stream' }); res.write(': preview\n\n');
      req.on('close', () => res.end()); return;
    }
    if (url.pathname.startsWith('/api/')) return json({ sessions: [], games: [], apps: [] });
    const pages = { '/agent': 'chat/index.html', '/account': 'account/index.html', '/account/history': 'account/history/index.html', '/minecraft': 'minecraft/index.html', '/': 'portal/index.html' };
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
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 }, reducedMotion: 'reduce' });
  const page = await context.newPage();
  const errors = []; page.on('pageerror', (error) => errors.push(error.message));
  await mkdir('/tmp/zkun-community-ui', { recursive: true });
  const checkWidth = async () => assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'horizontal overflow');
  await page.goto(base);
  await page.getByRole('heading', { name: /zkun 门户/ }).waitFor();
  assert.equal(await page.locator('.login-view').count(), 0, 'public home forced login');
  assert.equal(await page.getByRole('button', { name: '整合包准备中' }).isDisabled(), true);
  assert.equal(await page.locator('a[download]').count(), 0);
  assert.equal(await page.getByText('服务器信息与加入指南').count(), 1);
  await checkWidth(); await page.screenshot({ path: '/tmp/zkun-community-ui/home-desktop.png', fullPage: true });
  await page.getByRole('link', { name: '下载与加入指南', exact: true }).click();
  await page.getByRole('heading', { name: 'Minecraft', exact:true }).waitFor();
  assert.equal(await page.locator('.install-steps > li').count(), 4);
  await page.getByText('文件损坏 / SHA256 不匹配', { exact: true }).click();
  assert.equal(await page.locator('.guide-faq details[open]').count(), 1);
  const valid = await page.evaluate(async () => {
    const { downloadableRelease } = await import('/static/preview/js/minecraft-release.js');
    const data = { status: 'published', release: { verified: true, version: '1.0', minecraft: 'fixture', loader: 'fixture', pcl: 'fixture', format: 'mrpack', serverCompatibility: 'fixture', testedAt: '2026-10-15', publishedAt: '2026-10-15', bytes: 10, sha256: 'a'.repeat(64), url: '/downloads/example-1.0.mrpack' } };
    return [!!downloadableRelease(data), !!downloadableRelease({ ...data, release: { ...data.release, sha256: '' } }), !!downloadableRelease({ ...data, release: { ...data.release, url: 'javascript:alert(1)' } })];
  });
  assert.deepEqual(valid, [true, false, false]);
  await page.screenshot({ path: '/tmp/zkun-community-ui/minecraft-desktop.png', fullPage: true });
  await page.evaluate(async () => {
    const { copyButton } = await import('/static/preview/js/minecraft-release.js');
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: async () => { throw new Error('denied'); } } });
    document.getElementById('minecraft-download').appendChild(copyButton('fixture-address', '测试复制'));
  });
  await page.getByRole('button', { name: '测试复制', exact: true }).click();
  assert.equal(await page.getByLabel('手动复制内容').inputValue(), 'fixture-address');
  await page.goto(base + '/account');
  await page.getByRole('button', { name: '使用 Passkey 登录', exact: true }).waitFor();
  assert.equal(await page.locator('#login-name').count(), 1, 'account name step missing');
  assert.equal(await page.getByRole('button', { name: '继续', exact: true }).count(), 1);
  await page.screenshot({ path: '/tmp/zkun-community-ui/passkey-login.png' });
  await page.goto(base + '/minecraft');
  failRelease = true; await page.reload();
  await page.getByRole('button', { name: '重新加载发布信息' }).waitFor();
  failRelease = false; await page.getByRole('button', { name: '重新加载发布信息' }).click();
  await page.getByRole('button', { name: '整合包准备中' }).waitFor();
  loggedIn = true;
  await page.addInitScript(() => { localStorage.removeItem('mineagent.token'); localStorage.removeItem('mineagent.name'); });
  await page.goto(base);
  await page.getByRole('link', { name: '继续上次对话 →' }).waitFor();
  const accountButton = page.getByRole('button', { name: 'Friend的账户菜单', exact: true });
  await accountButton.click(); await page.keyboard.press('Escape');
  assert.equal(await accountButton.getAttribute('aria-expanded'), 'false');
  assert.equal(await accountButton.evaluate((el) => document.activeElement === el), true);
  await page.goto(base + '/agent?new=1&q=' + encodeURIComponent('先作为草稿保留'));
  await page.waitForFunction(() => document.getElementById('text').value === '先作为草稿保留');
  assert.equal(sends, 0, 'URL prompt auto-sent');
  assert.equal(await page.locator('.topbar .brand').textContent(), 'zkun');
  assert.equal(await page.locator('.login-view').count(), 0);
  await page.screenshot({ path: '/tmp/zkun-community-ui/agent-desktop.png' });
  await page.goto(base + '/account/history?ui=1');
  await page.getByRole('button', { name: '回放', exact: true }).first().click();
  assert.equal(await page.locator('.gomoku-cols span').count(), 15);
  assert.equal(await page.locator('.gomoku-rows span').count(), 15);
  assert.equal(await page.locator('.replay-winning-line').count(), 1);
  await page.getByRole('button', { name: /跳到第 3 手/ }).click();
  assert.equal(await page.locator('#step').textContent(), '3 / 9');
  await page.keyboard.press('ArrowRight');
  assert.equal(await page.locator('#step').textContent(), '4 / 9');
  await page.locator('#slider').focus(); await page.keyboard.press('ArrowRight');
  assert.equal(await page.locator('#step').textContent(), '5 / 9', 'slider arrow was handled twice');
  await page.locator('#last').click();
  await page.screenshot({ path: '/tmp/zkun-community-ui/replay-desktop.png' });
  for (const width of [390, 768, 1280]) {
    await page.setViewportSize({ width, height: 900 });
    for (const url of ['/', '/minecraft', '/account/history?ui=1']) {
      await page.goto(base + url); await page.locator('.topbar').waitFor();
      if (url === '/') await page.getByRole('heading', { name: /zkun 门户/ }).waitFor();
      if (url.includes('history')) { await page.getByRole('button', { name: '回放', exact: true }).first().click(); }
      await checkWidth();
      if (width === 390) await page.screenshot({ path: '/tmp/zkun-community-ui/' + (url.includes('history') ? 'replay' : url.includes('minecraft') ? 'minecraft' : 'home') + '-mobile.png', fullPage: true });
    }
  }
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.goto(base + '/?ui=1&theme=dark'); await page.getByRole('heading', { name: /zkun 门户/ }).waitFor();
  await page.screenshot({ path: '/tmp/zkun-community-ui/home-dark.png', fullPage: true });
  assert.deepEqual(errors, []);
  console.log('PASS: public home/guide, honest release states, retries, shared identity, prompt drafts, replay coordinates/jump/keyboard/winning line, responsive widths and dark/reduced-motion rendering.');
} finally {
  if (browser) await browser.close(); server.closeAllConnections(); await new Promise((resolve) => server.close(resolve));
}
