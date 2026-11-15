// Isolated screenshot matrix. No production config, account, model or Minecraft connection.
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { readFile, mkdir, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from 'playwright';
import {checkInk} from './paper-ink-checks.mjs';
const repo = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const root = path.join(repo, 'internal/webui/static');
const out = process.argv[2];
assert(out && path.isAbsolute(out), 'Provide an absolute artifact directory');
const pages = {'/':'portal/index.html','/agent':'chat/index.html','/games':'games/index.html','/games/chess':'games/chess/index.html','/games/gomoku':'games/gomoku/index.html','/minecraft':'minecraft/index.html','/account':'account/index.html','/account/history':'account/history/index.html','/downloads':'downloads/index.html','/screens':'display/index.html','/body':'body/index.html'};
const types = {'.html':'text/html','.js':'text/javascript','.mjs':'text/javascript','.css':'text/css','.png':'image/png','.svg':'image/svg+xml','.webp':'image/webp','.woff2':'font/woff2','.json':'application/json'};
const apps=[{id:'agent',name:'Agent',path:'/agent',icon:'sparkle',nav:true},{id:'games',name:'游戏',path:'/games',icon:'gamepad',nav:true},{id:'minecraft',name:'Minecraft',path:'/minecraft',icon:'pickaxe',nav:true},{id:'aiplayer',name:'AI 玩家',path:'/ai',enabled:true}];
const server=createServer(async(req,res)=>{
 try {
  const u=new URL(req.url,'http://localhost');
  const json=(d,code=200)=>{res.writeHead(code,{'Content-Type':'application/json'});res.end(JSON.stringify(d));};
  if(u.pathname==='/api/me'||u.pathname==='/api/auth/me')return json({error:'未登录'},401);
  if(u.pathname==='/api/portal/apps')return json({apps});
  if(u.pathname.startsWith('/api/portal/'))return json({apps:[{id:'minecraft',card:{online:false,count:0}}],announcements:[]});
  if(u.pathname==='/api/games')return json({games:[{id:'chess',name:'国际象棋',path:'/games/chess',enabled:true},{id:'gomoku',name:'五子棋',path:'/games/gomoku',enabled:true}]});
  if(u.pathname==='/api/events'){res.writeHead(200,{'Content-Type':'text/event-stream'});res.write(': fixture\n\n');req.on('close',()=>res.end());return;}
  if(u.pathname==='/api/options')return json({models:[],skills:[],defaultModel:'preview'});
  if(u.pathname==='/api/prompts')return json({personas:[],services:[],admin:false});
  if(u.pathname.startsWith('/api/'))return json({conversations:[],messages:[],sessions:[],rooms:[],runs:[],games:[],screens:[],files:[]});
  let rel=pages[u.pathname] || u.pathname.replace(/^\/static\/preview\//,'');
  if(u.pathname.endsWith('/minecraft/release.json'))return json({name:'Minecraft',status:'preparing',serverAddress:'mc.example.test',serverVersion:'待确认',release:null});
  if(!pages[u.pathname]&&!u.pathname.startsWith('/static/')){rel='error/index.html';res.statusCode=404;}
  const f=path.resolve(root,rel);if(!f.startsWith(root+path.sep)){res.writeHead(404);res.end();return;}
  let b=await readFile(f);if(f.endsWith('.html'))b=Buffer.from(b.toString().replaceAll('__VER__','preview'));
  res.setHeader('Content-Type',types[path.extname(f)]||'application/octet-stream');res.end(b);
 }catch{res.writeHead(404);res.end();}
});
await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
const base='http://127.0.0.1:'+server.address().port;
const routes=[['home','/?ui=1'],['guest','/'],['games','/games?ui=1'],['chess-lobby','/games/chess?ui=1&state=lobby'],['chess-playing','/games/chess?ui=1&state=playing'],['chess-check','/games/chess?ui=1&state=check'],['chess-finished','/games/chess?ui=1&state=finished-win'],['gomoku-playing','/games/gomoku?ui=1&state=playing'],['gomoku-finished','/games/gomoku?ui=1&state=finished-win'],['agent','/agent?ui=1'],['agent-prompts','/agent?ui=1&panel=prompts'],['agent-intel','/agent?ui=1&panel=intel'],['minecraft','/minecraft'],['account','/account?ui=1'],['history','/account/history?demo=gomoku'],['404','/does-not-exist']];
let browser;const result=[];
try {
 await mkdir(out,{recursive:true});browser=await chromium.launch({headless:true});
 for(const [width,theme] of [[1440,'light'],[1440,'dark'],[390,'light'],[320,'light']]) {
  const ctx=await browser.newContext({viewport:{width,height:900},reducedMotion:'reduce',colorScheme:theme,serviceWorkers:'block'});
  await ctx.addInitScript(t=>{localStorage.setItem('mineagent.theme',t);},theme);
  for(const [name,url] of routes){
   const page=await ctx.newPage(), errors=[];page.on('pageerror',e=>errors.push(e.message));
   await page.goto(base+url,{waitUntil:'domcontentloaded'});if(name.startsWith('agent'))await page.locator('#app').waitFor({state:'visible'});await page.waitForTimeout(350);
   const metrics=await page.evaluate(()=>({overflow:document.documentElement.scrollWidth>innerWidth,title:document.title,h1:document.querySelector('h1')?.textContent??'',theme:document.documentElement.dataset.theme}));
   await page.screenshot({path:path.join(out,`${name}-${width}-${theme}.png`),fullPage:true});
   result.push({name,width,theme,...metrics,errors});await page.close();
  }
  await ctx.close();
 }
 await writeFile(path.join(out,'matrix.json'),JSON.stringify(result,null,2));
 assert.deepEqual(result.filter(x=>x.overflow),[],'Screenshot matrix has horizontal overflow');
 assert.deepEqual(result.filter(x=>x.errors.length),[],'Screenshot matrix has browser errors');
 if(process.argv.includes('--check')) await checkInk(browser,base,repo,out);
 console.log(JSON.stringify({screenshots:result.length,overflow:result.filter(x=>x.overflow).map(x=>[x.name,x.width,x.theme]),pageErrors:result.filter(x=>x.errors.length).map(x=>[x.name,x.errors]),out}));
}finally{if(browser)await browser.close();server.closeAllConnections();await new Promise(resolve=>server.close(resolve));}
