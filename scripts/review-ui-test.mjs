// Round-two regressions: deterministic Markdown, draft races, real/demo identity, search and permissions.
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { createServer } from 'node:http';
import path from 'node:path';
import { chromium } from 'playwright';
const root = path.resolve('internal/webui/static');
const types = { '.html': 'text/html', '.js': 'text/javascript', '.mjs': 'text/javascript', '.css': 'text/css', '.svg': 'image/svg+xml', '.png': 'image/png', '.woff2': 'font/woff2' };
let sending = 0, running = false, failSend = false, queriedAround = false, writeCalls = 0;
const clients = new Set();
const messages = [{ id: 1, role: 'user', text: '这是网站体验测试。安排 UNO 活动', at: Date.now() }, { id: 2, role: 'agent', text: '已整理活动时间表。', at: Date.now() }];
const conversation = { conv: 'abcdef', title: '活动安排', updatedAt: Date.now(), pinned: false, archived: false };
const server = createServer(async (req, res) => {
 try {
  const url = new URL(req.url, 'http://localhost');
  const json = (data, status = 200) => { res.writeHead(status, { 'Content-Type': 'application/json' }); res.end(JSON.stringify(data)); };
  if (url.pathname === '/api/version') return json({ version: 'review' });
  if (['/api/me','/api/auth/me'].includes(url.pathname)) return json({ id: 9, name: '星期八', admin: false });
  if (url.pathname === '/api/portal/apps') return json({ apps: [{ id: 'agent', name: 'Agent', path: '/agent', nav: true }, { id: 'games', name: '游戏', path: '/games', nav: true }] });
  if (url.pathname === '/api/conversations') return json({ conversations: [{ ...conversation, ...(url.searchParams.has('q') ? { snippet: '安排 UNO 活动', matchId: 1 } : {}) }] });
  if (url.pathname === '/api/conversations/settings') { let body='';for await (const part of req) body+=part;Object.assign(conversation, JSON.parse(body));return json({ ok:true }); }
  if (url.pathname === '/api/history') { if (url.searchParams.get('around') === '1') queriedAround = true; return json({ messages, running: running ? { text: '正在处理…', startedAt: Date.now() } : null, runEvents: [{ id: 'clock', name: 'current_time', status: 'returned', at: Date.now(), durationMs: 8 }] }); }
  if (url.pathname === '/api/send') { sending++; await new Promise((r) => setTimeout(r, 300)); if (failSend) return json({ error: '模拟连接失败' }, 503); running = true; return json({ conv: 'abcdef' }); }
  if (url.pathname === '/api/stop') { running = false; for (const client of clients) client.write('data: '+JSON.stringify({ type: 'run_done', conv: 'abcdef' })+'\n\n'); return json({ ok: true }); }
  if (url.pathname === '/api/options') return json({ models: [], skills: [{ name: '写代码跑一下', status: 'admin', reason: '需要管理员权限，可联系管理员', prompt: '写代码' }], capabilities: [{ name: 'workspace_exec', kind: '文件与执行', status: 'admin' }, { name: 'current_time', kind: '查询', status: 'available' }] });
  if (url.pathname === '/api/events') { res.writeHead(200, { 'Content-Type': 'text/event-stream' });res.write(': connected\n\n');clients.add(res);req.on('close',()=>clients.delete(res));return; }
  if (url.pathname === '/api/display/status') return json({ clients: 1, screens: [{ id:'demo',url:'https://example.com',revision:2 }], hosts:['example.com'], whitelistEnabled:false });
  if (url.pathname.startsWith('/api/display/')) { writeCalls++; return json({ error:'forbidden' },403); }
  if (url.pathname.startsWith('/api/')) return json({});
  const pages = { '/agent':'chat/index.html', '/account/history':'account/history/index.html', '/screens':'display/index.html', '/account':'account/index.html' };
  const file = path.resolve(root, pages[url.pathname] || url.pathname.replace(/^\/static\/review\//,''));
  if (!file.startsWith(root + path.sep)) {res.writeHead(404);res.end();return;}
  let bytes = await readFile(file);if (file.endsWith('.html')) bytes=Buffer.from(bytes.toString().replaceAll('__VER__','review'));
  res.writeHead(200, { 'Content-Type': types[path.extname(file)] || 'application/octet-stream' });res.end(bytes);
 } catch (error) { res.writeHead(500);res.end(error.message); }
});
await new Promise((resolve)=>server.listen(0,'127.0.0.1',resolve));
const base='http://127.0.0.1:'+server.address().port;
let browser;
try {
 browser=await chromium.launch({headless:true});
 const context=await browser.newContext({viewport:{width:1280,height:900},reducedMotion:'reduce'});
 await context.addInitScript(()=>{localStorage.setItem('mineagent.token','fixture');localStorage.setItem('mineagent.name','星期八');localStorage.setItem('mineagent.conv','abcdef');});
 const page=await context.newPage();const errors=[];page.on('pageerror',(error)=>errors.push(error.message));
 await page.goto(base+'/agent');await page.locator('.msg.assistant').waitFor();
 // The next draft is typed while the first request is pending.
 await page.locator('#text').fill('本次发送');await page.locator('#send').click();
 assert.equal(await page.locator('#text').inputValue(),'');
 await page.locator('#text').fill('下一条草稿');
 await page.locator('#stop').waitFor();assert.equal(await page.locator('#text').inputValue(),'下一条草稿');assert.equal(sending,1);
 assert.equal(await page.locator('#send').isDisabled(),true);
 await page.locator('#stop').click();await page.locator('#stop').waitFor({state:'hidden'});assert.equal(await page.locator('#text').inputValue(),'下一条草稿');
 // A failed acknowledgement retains a separate recoverable snapshot and never resubmits.
 failSend=true;await page.locator('#text').fill('未确认发送内容');await page.locator('#send').click();await page.locator('#text').fill('更新的草稿');
 await page.getByRole('button',{name:'恢复发送内容到草稿'}).waitFor();assert.equal(await page.locator('#text').inputValue(),'更新的草稿');
 await page.getByRole('button',{name:'恢复发送内容到草稿'}).click();assert.equal(await page.locator('#text').inputValue(),'未确认发送内容\n\n更新的草稿');assert.equal(sending,2);
 await page.locator('#conv-search').fill('UNO');await page.locator('.conv-snippet').filter({hasText:'UNO'}).waitFor();await page.locator('.conv-open').click();
 await page.locator('.search-hit').waitFor();assert.equal(queriedAround,true);
 await page.locator('#attach').click();await page.getByRole('button',{name:/写代码跑一下/}).click();assert.notEqual(await page.locator('#text').inputValue(),'写代码');
 // Fixed rendering specimens do not rely on model output.
 const sample='3. 第一项\n   - 子项 A\n     1. 第三层\n4. 第二项\n   - 子项 B\n5. 第三项\n\n| 活动 | 开始 | 结束 | 人数 | 地点 | 备注 |\n| --- | --- | --- | --- | --- | --- |\n| 矿洞寻宝 | 周六20:00 | 21:30 | 12 | 主世界 | 自备装备 |\n\n```json\n{"long":"'+ 'x'.repeat(300) +'"}\n```\n\n<img src=x onerror=alert(1)>\n\n[unsafe](javascript:alert(1))\n\n公式 $x_1$\n\n$$x^2+y^2$$\n\n- [x] 已完成';
 await page.evaluate(async(sample)=>{const {renderContent,enhanceContent}=await import('/static/review/js/markdown.js');const box=document.createElement('div');box.id='specimen';box.className='msg assistant';box.innerHTML='<div class="body"><div class="content"></div></div>';document.querySelector('#listInner').append(box);renderContent(box.querySelector('.content'),sample);Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:async(text)=>{window.copiedFixture=text;}}});await enhanceContent(box.querySelector('.content'));},sample);
 assert.equal(await page.locator('#specimen .content > ol').getAttribute('start'),'3');assert.equal(await page.locator('#specimen .content > ol > li').count(),3);
 assert.equal(await page.locator('#specimen ol > li > ul > li > ol').count(),1);assert.equal(await page.locator('#specimen [onerror],#specimen a[href^="javascript:"]').count(),0);
 assert.equal(await page.locator('#specimen .math-inline').count(),1);assert.equal(await page.locator('#specimen .math-display').count(),1);
 assert.equal(await page.locator('#specimen .task.done').count(),1);
 const code=await page.locator('#specimen pre code').textContent();assert.equal(code,'{"long":"'+'x'.repeat(300)+'"}\n');
 await page.locator('#specimen .codecopy').click(); assert.equal(await page.evaluate(()=>window.copiedFixture),code);
 await page.setViewportSize({width:500,height:757});
 assert.ok(await page.locator('#specimen .tablewrap').evaluate((el)=>el.scrollWidth>el.clientWidth));
 assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
 await page.locator('#menu').click();await page.keyboard.press('Escape');assert.equal(await page.locator('#sidebar').evaluate((el)=>el.classList.contains('open')),false);assert.equal(await page.locator('main').evaluate((el)=>el.inert),false);assert.equal(await page.evaluate(()=>document.activeElement.id),'menu');
 await page.goto(base+'/account/history?demo=gomoku');await page.getByText('演示回放，不影响真实战绩').waitFor();
 assert.equal(await page.evaluate(()=>localStorage.getItem('mineagent.name')),'星期八');await page.getByRole('button',{name:'星期八的账户菜单',exact:true}).waitFor();
 assert.equal(await page.locator('.topbar a[href*="ui=1"]').count(),0);
 await page.getByRole('button',{name:'回放',exact:true}).first().click();await page.getByRole('button',{name:'播放',exact:true}).click();await page.getByRole('button',{name:'暂停',exact:true}).waitFor();await page.getByRole('button',{name:'暂停',exact:true}).click();
 await page.goto(base+'/screens');await page.getByText('只读访问。修改屏幕内容或设置需要管理员权限。').waitFor();
 assert.equal(await page.locator('#open-btn').isVisible(),false);assert.equal(await page.locator('#wl-enabled').isVisible(),false);assert.equal(await page.getByRole('button',{name:'关闭',exact:true}).count(),0);assert.equal(writeCalls,0);
 assert.deepEqual(errors,[]);
 console.log('PASS: deterministic Markdown nesting/start/math/XSS/code, scrollable table, draft race and failure recovery, no duplicate sends, stop, content search and positioning, drawer Escape/focus, demo identity/navigation/playback, read-only screen permissions.');
} finally {if(browser)await browser.close();server.closeAllConnections();await new Promise((resolve)=>server.close(resolve));}
