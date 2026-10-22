// Real browser + disposable authenticated server; no model provider or production profile.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { createInterface } from 'node:readline';
import { chromium } from 'playwright';
const server=spawn('go',['run','./scripts/passkey-fixture'],{stdio:['pipe','pipe','inherit']});
let browser;
try {
 const fixture=await new Promise((resolve,reject)=>{const lines=createInterface({input:server.stdout});const timer=setTimeout(()=>reject(new Error('fixture startup timeout')),90000);lines.once('line',line=>{clearTimeout(timer);resolve(JSON.parse(line));});server.once('exit',()=>{clearTimeout(timer);reject(new Error('fixture exited'));});});
 browser=await chromium.launch({headless:true});const page=await browser.newPage({viewport:{width:390,height:844}});
 const errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.goto(fixture.origin+'/account');
 await page.getByLabel('账号名称',{exact:true}).fill('owner');await page.getByRole('button',{name:'继续',exact:true}).click();
 await page.getByLabel('创建密码',{exact:true}).fill('conversation test phrase!');await page.getByLabel('再次输入密码',{exact:true}).fill('conversation test phrase!');await page.getByRole('button',{name:'设置密码并进入'}).click();await page.locator('#acc-name').filter({hasText:'owner'}).waitFor();
 await page.goto(fixture.origin+'/agent');await page.locator('#text').waitFor({state:'visible'});
 async function send(text){await page.locator('#text').fill(text);await page.locator('#text').dispatchEvent('input');await page.locator('#send').click();await page.waitForFunction(()=>!document.querySelector('#send').disabled || document.querySelector('#text').value==='');await page.waitForFunction(()=>document.querySelectorAll('.msg').length>0);}
 await send('/new');await page.getByText('已开启空白对话，旧对话历史已保存。',{exact:false}).waitFor();
 const first=await page.evaluate(()=>localStorage.getItem('mineagent.conv'));assert.ok(first);
 await send('/effort low');await page.locator('#intellabel').filter({hasText:'低'}).waitFor();
 await send('/help');await page.locator('.content').filter({hasText:'/fork ——'}).waitFor();
 await send('/fork');await page.getByText('已复制当前已保存的历史并进入独立分支',{exact:false}).waitFor();
 const fork=await page.evaluate(()=>localStorage.getItem('mineagent.conv'));assert.notEqual(first,fork);
 await page.locator('#intellabel').filter({hasText:'低'}).waitFor();await page.locator('#intelbtn').click();await page.getByRole('radio',{name:'高',exact:true}).click();await page.locator('#intellabel').filter({hasText:'高'}).waitFor();await page.keyboard.press('Escape');
 await page.reload();await page.locator('#text').waitFor({state:'visible'});assert.equal(await page.evaluate(()=>localStorage.getItem('mineagent.conv')),fork);await page.locator('#intellabel').filter({hasText:'高'}).waitFor();
 await send('/resume '+first);await page.getByText('已恢复对话：'+first,{exact:false}).waitFor();assert.equal(await page.evaluate(()=>localStorage.getItem('mineagent.conv')),first);await page.locator('#intellabel').filter({hasText:'低'}).waitFor();
 await send('/effort');await page.locator('.content').filter({hasText:'当前会话思考强度：低'}).waitFor();
 await send('/effort default');await page.locator('#intellabel').filter({hasText:'默认'}).waitFor();
 await send('/resume default');await page.getByText('已恢复对话：default',{exact:false}).waitFor();assert.equal(await page.evaluate(()=>localStorage.getItem('mineagent.conv')),'');
 assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));assert.deepEqual(errors,[]);
 console.log('PASS: authenticated browser /new /help /fork /resume /effort, inherited effort and slider sync, default restore, reload persistence and mobile layout; no Agent backend.');
} finally {
 await browser?.close();server.stdin.end();await new Promise(resolve=>{if(server.exitCode!==null)resolve();else{server.once('exit',resolve);setTimeout(()=>server.kill(),3000).unref();}});
}
