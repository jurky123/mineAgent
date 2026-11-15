// Independent navigation rail checks with local static files and mock APIs only.
import assert from 'node:assert/strict';
import { readFile, mkdir } from 'node:fs/promises';
import { createServer } from 'node:http';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from 'playwright';
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const staticRoot = path.join(root, 'internal/webui/static');
let isAdmin = true, workspaceCalls = 0;
let selection = '', refreshes = 0, failRefresh = false, effort = 'medium', failEffort = false, effortWrites = 0;
const models = [
  { id: 'old', label: 'Old', provider: 'default', providerLabel: 'Gateway', created: 100 },
  { id: 'new-free', label: 'New Free', provider: 'default', providerLabel: 'Gateway', created: 200, free: true, verified: true, vision: true, reasoning: true, tools: true },
  { id: 'blocked-free', label: 'Blocked', provider: 'default', providerLabel: 'Gateway', created: 250, free: true, unavailable: true, unavailableReason: '网关暂时无法路由' },
  { id: 'openrouter/needs-key', label: 'Needs Key', provider: 'openrouter', providerLabel: 'OpenRouter', free: true, needsKey: true },
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
        if (!models.some((m) => m.id === 'fresh')) models.push({ id: 'fresh', label: 'Fresh', provider: 'openrouter', providerLabel: 'OpenRouter', created: 300, free: true, vision: true, reasoning: true });
      }
      return json({ models, skills: [], model: selection, defaultModel: 'old', effort });
    }
    if (url.pathname === '/api/prefs') {
      let body = ''; for await (const chunk of req) body += chunk;
      const patch = JSON.parse(body);
      if ('model' in patch) selection = patch.model;
      if ('effort' in patch) {
        effortWrites++; await new Promise(resolve => setTimeout(resolve, 250));
        if (failEffort) return json({ error: '模拟偏好保存失败' }, 503);
        effort = patch.effort;
      }
      return json({ model: selection, effort });
    }
    if (url.pathname === '/api/me' || url.pathname === '/api/auth/me') return json({ name: 'Preview', id: 1, admin: isAdmin });
    if (url.pathname === '/api/version') return json({ version: 'preview' });
    if (url.pathname === '/api/conversations') return json({ conversations: [] });
    if (url.pathname === '/api/history') return json({ messages: [] });
    if (url.pathname === '/api/events') {
      res.writeHead(200, { 'Content-Type': 'text/event-stream' }); res.write(': preview\n\n');
      req.on('close', () => res.end()); return;
    }
    if (url.pathname === '/api/workspace') { workspaceCalls++; return json({path:'',entries:[]}); }
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
 browser=await chromium.launch({headless:true});
 const ctx=await browser.newContext({viewport:{width:1280,height:900},reducedMotion:'reduce'});
 await ctx.addInitScript(()=>{localStorage.setItem('mineagent.token','preview');localStorage.setItem('mineagent.name','Preview');});
 const page=await ctx.newPage(),errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.goto(base+'/agent');await page.locator('#text').waitFor({state:'visible'});
 assert.equal(await page.getByRole('navigation',{name:'工作区导航'}).count(),1);
 await page.locator('#collapse').click();assert(await page.locator('.navigation-rail').isVisible());assert.equal(await page.locator('.sidebar-panel').isVisible(),false);
 await page.locator('#rail-conversations').click();assert(await page.locator('#conv-search').evaluate(e=>e===document.activeElement));
 await page.locator('#rail-tools').click();assert(await page.locator('#plusmenu').isVisible());await page.keyboard.press('Escape');
 await page.locator('#rail-usage').click();assert(await page.locator('#usagemodal').isVisible());await page.locator('#usage-close').click();
 await page.locator('#userbtn').click();assert(await page.locator('#accountmenu').isVisible());await page.keyboard.press('Escape');assert(await page.locator('#userbtn').evaluate(e=>e===document.activeElement));
 assert.equal(await page.locator('#rail-workspace').isVisible(),true);await Promise.all([page.waitForResponse(r=>r.url().includes('/api/workspace')),page.locator('#rail-workspace').click()]);
 assert(workspaceCalls>0);
 isAdmin=false;await page.goto(base+'/agent');await page.locator('#text').waitFor({state:'visible'});assert.equal(await page.locator('#rail-workspace').isVisible(),false);
 await page.setViewportSize({width:390,height:844});await page.locator('#menu').click();assert(await page.locator('.navigation-rail').isVisible());
 await page.locator('#userbtn').click();const accountBounds=await page.locator('#accountmenu').boundingBox();assert(accountBounds.x>=0&&accountBounds.x+accountBounds.width<=390,'mobile account menu outside viewport');
 await mkdir('/home/ubuntu/.local/share/zkun-rail-20261115',{recursive:true});await page.screenshot({path:'/home/ubuntu/.local/share/zkun-rail-20261115/rail-mobile-account.png'});await page.keyboard.press('Escape');
 await page.locator('#rail-tools').click();assert.equal(await page.locator('#sidebar').evaluate(e=>e.classList.contains('open')),false);assert(await page.locator('#plusmenu').isVisible());assert.equal(await page.locator('main').evaluate(e=>e.inert),false);
 assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));assert.deepEqual(errors,[]);
 console.log('PASS: navigation rail, collapse/reopen, tools, quota, account Escape/focus, administrator gate and mobile drawer handoff.');
} finally {if(browser)await browser.close();server.closeAllConnections();await new Promise(r=>server.close(r));}
