// Acceptance against an isolated real server; no production accounts/model calls.
import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {createInterface} from 'node:readline';
import {mkdir} from 'node:fs/promises';
import {chromium} from 'playwright';
const binary=process.argv[2] || '/tmp/zkun-ui-fixture';assert(binary.startsWith('/tmp/'));
const server=spawn(binary,[],{stdio:['pipe','pipe','inherit']});let browser;
const out='/tmp/zkun-delivery-ui';await mkdir(out,{recursive:true});
try{
 const cfg=await new Promise((resolve,reject)=>{createInterface({input:server.stdout}).once('line',line=>resolve(JSON.parse(line)));server.once('exit',()=>reject(new Error('fixture exited')));});
 browser=await chromium.launch({headless:true});const errors=[];
 const context=await browser.newContext({viewport:{width:390,height:844},serviceWorkers:'block'});const page=await context.newPage();page.on('pageerror',e=>errors.push(e.message));
 async function noOverflow(p){assert(await p.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'page overflows');}
 for(const width of [320,390,768,1280]){
  await page.setViewportSize({width,height:900});
  for(const route of ['/','/games','/games/chess','/minecraft','/games/gomoku?ui=1&state=playing']){
   await page.goto(cfg.origin+route);await page.locator('.topbar').waitFor();await page.waitForTimeout(100);await noOverflow(page);
   assert.equal(await page.locator('.login-view').count(),0,'public discovery blocked by login');
   if(width===390||width===1280)await page.screenshot({path:out+'/final-'+width+'-'+route.replace(/[^a-z0-9]+/gi,'-')+'.png',fullPage:true});
  }
 }
 await page.goto(cfg.origin+'/games/chess');await page.getByRole('button',{name:'创建房间',exact:true}).click();await page.getByLabel('账号名称',{exact:true}).fill('owner');await page.getByRole('button',{name:'继续',exact:true}).click();
 await page.getByLabel('创建密码',{exact:true}).fill('portal acceptance phrase!');await page.getByLabel('再次输入密码',{exact:true}).fill('portal acceptance phrase!');await page.getByRole('button',{name:'设置密码并进入'}).click();
 await page.locator('.room-code').waitFor();const roomCode=(await page.locator('.room-code span').first().textContent()).trim();assert.match(roomCode,/^[A-Z0-9]{6}$/);
 const guest=await browser.newContext({viewport:{width:390,height:844},serviceWorkers:'block'});const peer=await guest.newPage();peer.on('pageerror',e=>errors.push(e.message));
 await peer.goto(cfg.origin+'/games/chess?room='+roomCode);assert.equal(await peer.getByLabel('房间码',{exact:true}).inputValue(),roomCode);await peer.getByRole('button',{name:'加入',exact:true}).click();
 await peer.getByLabel('账号名称',{exact:true}).fill('reader');await peer.getByRole('button',{name:'继续',exact:true}).click();await peer.getByRole('heading',{name:'创建账号',exact:true}).waitFor();
 assert(!await peer.locator('.login-box').innerText().then(s=>s.includes('原有对话和战绩')),'new account got legacy-account copy');
 await peer.getByLabel('创建密码',{exact:true}).fill('reader acceptance phrase!');await peer.getByLabel('再次输入密码',{exact:true}).fill('reader acceptance phrase!');await peer.getByRole('button',{name:'设置密码并进入'}).click();
 await peer.locator('#actions').getByRole('button',{name:'认输',exact:true}).waitFor();assert.equal((await peer.locator('.room-code span').first().textContent()).trim(),roomCode);
 await peer.locator('.game-more summary').click();await peer.keyboard.press('Escape');assert(!await peer.locator('.game-more').evaluate(e=>e.open));await noOverflow(peer);
 await page.goto(cfg.origin+'/agent');await page.locator('#text').waitFor({state:'visible'});
 await page.locator('#header-prompts').click();await page.getByLabel('回复长度',{exact:true}).selectOption('brief');await page.getByLabel('默认语言',{exact:true}).selectOption('en');await page.getByRole('button',{name:'应用偏好',exact:true}).click();await page.getByText('更改从下一条消息开始生效',{exact:true}).waitFor();
 await page.locator('.dialog-head button').click();await page.reload();await page.locator('#header-prompts').click();assert.equal(await page.getByLabel('回复长度',{exact:true}).inputValue(),'brief');assert.equal(await page.getByLabel('默认语言',{exact:true}).inputValue(),'en');await page.locator('.dialog-head button').click();
 await page.route('**/api/options*',route=>route.fulfill({json:{models:[{id:'fixture-new',label:'Fixture New',provider:'openrouter',providerLabel:'OpenRouter',free:true,created:300,vision:true,tools:true},{id:'fixture-old',label:'Fixture Old',provider:'default',providerLabel:'Default',created:100}],model:'',defaultModel:'fixture-old',effort:'high',skills:[]}}));
 await page.reload();await page.locator('#model-label').filter({hasText:'fixture-old'}).waitFor();await page.locator('#intelbtn').click();assert.equal(await page.getByRole('radio',{name:'高',exact:true}).getAttribute('aria-checked'),'true');
 let effort='', preferenceWrites=0;await page.route('**/api/prefs',async route=>{preferenceWrites++;effort=route.request().postDataJSON().effort;await route.fulfill({json:{model:'',effort}});});await page.getByRole('radio',{name:'低',exact:true}).click();await page.waitForFunction(()=>document.querySelector('#intellabel').textContent.includes('低'));assert.equal(effort,'low');await page.keyboard.press('Escape');
 await page.locator('#modelbtn').click();assert.match(await page.locator('#po-list .po-modelbody').first().innerText(),/Fixture New/);await page.getByLabel('模型能力筛选').selectOption('vision');assert.equal(await page.locator('#po-list .po-modelbody').count(),1);await page.keyboard.press('Escape');
 await page.locator('#text').fill('/new');await page.locator('#text').dispatchEvent('input');await page.locator('#send').click();await page.getByText('已开启空白对话，旧对话历史已保存。',{exact:false}).waitFor();const source=await page.evaluate(()=>localStorage.getItem('mineagent.conv'));
 await page.locator('#text').fill('保留这条未发送的草稿');await page.locator('#forkchat').click();await page.getByText('来源会话：'+source,{exact:false}).waitFor();assert.equal(await page.locator('#text').inputValue(),'保留这条未发送的草稿');assert.notEqual(await page.evaluate(()=>localStorage.getItem('mineagent.conv')),source);
 let uploads=0;await page.route('**/api/upload?*',route=>{uploads++;return uploads===1?route.fulfill({status:503,json:{error:'模拟网络失败'}}):route.fulfill({json:{name:'notes.txt',path:'fixture/notes.txt',size:5}});});
 await page.locator('#file').setInputFiles({name:'notes.txt',mimeType:'text/plain',buffer:Buffer.from('hello')});await page.getByRole('button',{name:'重新上传 notes.txt'}).waitFor();assert.equal(await page.locator('#pending .chip').count(),1);assert.equal(await page.locator('#text').inputValue(),'保留这条未发送的草稿');await page.getByRole('button',{name:'重新上传 notes.txt'}).click();await page.locator('#pending .chip.done').waitFor();assert.equal(uploads,2);await page.getByRole('button',{name:'移除附件 notes.txt'}).click();assert.equal(await page.locator('#pending .chip').count(),0);
 await page.screenshot({path:out+'/final-agent-desktop.png'});await page.setViewportSize({width:390,height:844});await noOverflow(page);await page.screenshot({path:out+'/final-agent-mobile.png'});
 const security=await page.evaluate(async()=>await(await fetch('/api/account/security',{headers:{Authorization:'Bearer '+localStorage.getItem('mineagent.token')}})).json());assert.equal(security.password,true);assert.equal(security.passkeys,0);
 const tokenBeforePreview=await page.evaluate(()=>localStorage.getItem('mineagent.token'));const writesBeforePreview=preferenceWrites;
 await page.goto(cfg.origin+'/agent?ui=1&panel=intel&effort=high');await page.getByRole('radio',{name:'低',exact:true}).click();await page.waitForFunction(()=>document.querySelector('#intellabel').textContent.includes('低'));
 assert.equal(preferenceWrites,writesBeforePreview,'preview wrote real preferences');assert.equal(await page.evaluate(()=>localStorage.getItem('mineagent.token')),tokenBeforePreview,'preview cleared real login');
 await page.goto(cfg.origin+'/games/chess?ui=1&state=playing');await page.locator('#actions').getByRole('button',{name:'认输',exact:true}).waitFor();assert.equal(await page.locator('#actions').getByRole('button',{name:'认输',exact:true}).isDisabled(),true,'preview can mutate a real game');
 await page.goto(cfg.origin+'/does-not-exist');assert.equal(await page.title(),'页面不存在 · zkun');await page.getByRole('link',{name:'返回首页',exact:true}).waitFor();
 assert.deepEqual(errors,[]);await guest.close();await context.close();
 console.log('PASS: public discovery and 320–1280px layout; real create/join through login with invite preserved; private preference persistence; exact reasoning values; cross-provider sorting/capabilities; draft-preserving fork; attachment retry; real account security; branded 404.');
}finally{await browser?.close();server.stdin.end();}
