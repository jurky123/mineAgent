// Independent navigation rail checks with local static files and mock APIs only.
import assert from 'node:assert/strict';
import { readFile, mkdir } from 'node:fs/promises';
import { createServer } from 'node:http';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from 'playwright';
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const staticRoot = path.join(root, 'internal/webui/static');
let isAdmin = true, workspaceCalls = 0, failWorkspace=false, sends=0;
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
      return json({ models, skills: [{name:'农场助手',desc:'整理农场计划',prompt:'帮我规划农场',status:'available'},{name:'受限技能',status:'admin',reason:'管理员专属'}], capabilities:[{name:'current_time',kind:'查询',status:'available'},{name:'workspace_exec',kind:'文件与执行',status:'admin'}], model: selection, defaultModel: 'old', effort });
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
    if (url.pathname === '/api/workspace') { workspaceCalls++; if(!isAdmin)return json({error:'禁止读取'},403);if(failWorkspace)return json({error:'模拟故障'},503);const dir=url.searchParams.get('path')||'';if(dir.includes('..'))return json({error:'路径非法'},400);return json({path:dir,entries:dir?[]:[{name:'计划',dir:true},{name:'农场.md',size:2048,mtime:1700000000000},{name:'截图.png',size:4096,mtime:1700000000000},{name:'<img src=x onerror=alert(1)>.txt',size:14}]}); }
    if (url.pathname==='/api/send') { sends++;return json({}); }
    if (url.pathname.startsWith('/api/')) return json({ sessions: [], games: [], apps: [] });
    const pages = { '/agent/library':'chat/index.html','/agent/tools':'chat/index.html', '/agent': 'chat/index.html', '/account': 'account/index.html', '/': 'portal/index.html' };
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
 assert.equal(await page.locator('.rail-home').count(),0);assert.equal(await page.locator('#wsmodal').count(),0);
 await page.locator('#attach').click();assert.equal(await page.locator('#plusmenu [data-skill],#plusmenu .capability-list').count(),0);assert.equal(await page.locator('#plusmenu .po-item').count(),2);await page.keyboard.press('Escape');
 await page.locator('#collapse').click();assert(await page.locator('.navigation-rail').isVisible());
 await page.locator('#rail-conversations').click();assert(await page.locator('#conv-search').evaluate(e=>e===document.activeElement));
 await page.locator('#text').fill('未发送草稿');await page.locator('#rail-tools').click();await page.getByRole('button',{name:'取消',exact:true}).click();assert.equal(await page.locator('#text').inputValue(),'未发送草稿');assert.equal(new URL(page.url()).pathname,'/agent');await page.locator('#text').fill('');
 await page.locator('#rail-tools').click();await page.waitForURL('**/agent/tools');await page.getByRole('heading',{name:'插件与技能',exact:true}).waitFor();
 assert.equal(await page.locator('#rail-tools').getAttribute('aria-current'),'page');
 await page.getByLabel('搜索插件与工具').fill('农场');assert.equal(await page.locator('.capability-card').count(),1);
 await page.getByRole('link',{name:'在会话中使用',exact:true}).click();await page.locator('#text').waitFor({state:'visible'});await page.waitForFunction(()=>document.querySelector('#text').value==='帮我规划农场');assert.equal(sends,0,'skill executed without sending');
 await page.goto(base+'/agent/tools?tab=tools');await page.getByRole('heading',{name:'工具',exact:true}).waitFor();await page.locator('.capability-card').first().waitFor();assert.equal(await page.locator('.capability-card').count(),2);assert.equal(await page.locator('#plusmenu.on').count(),0);
 await page.locator('#rail-workspace').click();await page.waitForURL('**/agent/library');await page.locator('.resource-row').first().waitFor();assert(workspaceCalls>0);
 assert.equal(await page.locator('.resource-row').count(),4);assert.equal(await page.locator('.resource-row img').count(),0,'filename was interpreted as HTML');
 const file=page.getByRole('link',{name:'下载 农场.md',exact:true});assert((await file.getAttribute('href')).startsWith('/api/workspace/file?path='));assert(!(await file.getAttribute('href')).includes('token='));
 await page.getByLabel('搜索资料').fill('农场');assert.equal(await page.locator('.resource-row').count(),1);await page.getByLabel('搜索资料').fill('');
 await page.getByRole('button',{name:'网格',exact:true}).click();assert(await page.locator('.resource-grid').isVisible());
 await page.getByRole('button',{name:'打开文件夹 计划',exact:true}).click();await page.getByText('这个目录还是空的',{exact:true}).waitFor();assert(new URL(page.url()).searchParams.get('path')==='计划');
 await page.getByRole('button',{name:'资料空间',exact:true}).click();await page.locator('.resource-row').first().waitFor();
 failWorkspace=true;await page.getByRole('button',{name:'刷新',exact:true}).click();await page.getByRole('heading',{name:'暂时无法读取'}).waitFor();assert.equal(await page.locator('.resource-row').count(),0);failWorkspace=false;await page.getByRole('button',{name:'重试',exact:true}).click();await page.locator('.resource-row').first().waitFor();
 await page.setViewportSize({width:390,height:844});await page.locator('#section-menu').click();assert(await page.locator('#section-sidebar').isVisible());await page.getByRole('button',{name:'图像',exact:true}).click();assert.equal(await page.locator('main').evaluate(e=>e.inert),false);assert.equal(await page.locator('#sidebar').evaluate(e=>e.classList.contains('open')),false);
 assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
 const before=workspaceCalls;isAdmin=false;await page.goto(base+'/agent/library');await page.getByRole('heading',{name:'需要管理员权限'}).waitFor();assert.equal(workspaceCalls,before,'regular user workspace was requested');assert.equal(await page.locator('.resource-row').count(),0);
 await page.locator('#section-menu').click();await page.locator('#userbtn').click();const bounds=await page.locator('#accountmenu').boundingBox();assert(bounds.x>=0&&bounds.x+bounds.width<=390);await page.keyboard.press('Escape');assert(await page.locator('#sidebar').evaluate(e=>e.classList.contains('open')));await page.keyboard.press('Escape');assert(await page.locator('#section-menu').evaluate(e=>document.activeElement===e));
 await page.locator('#section-menu').click();await page.locator('#rail-usage').click();assert(await page.locator('#usagemodal').isVisible());await page.locator('#usage-close').click();
 assert.deepEqual(errors,[]);console.log('PASS: independent pages, active rail, real catalog, draft-only skill use, files/search/grid/folders/retry, safe filenames, admin gate and mobile focus.');
} finally {if(browser)await browser.close();server.closeAllConnections();await new Promise(r=>server.close(r));}
