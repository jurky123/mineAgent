// Acceptance for the user-approved visual plan; fixture-only, never production state.
import assert from 'node:assert/strict';
import {readFile,writeFile,readdir} from 'node:fs/promises';
import {execFileSync} from 'node:child_process';
import path from 'node:path';
const cssNames=['tokens','ds','community','portal','games','layout','chat','composer','overlays','prompts','downloads','screens','body'];
const luminance=(hex)=>{const a=hex.match(/[\da-f]{2}/gi).map(c=>parseInt(c,16)/255).map(c=>c<=.04045?c/12.92:((c+.055)/1.055)**2.4);return .2126*a[0]+.7152*a[1]+.0722*a[2];};
const ratio=(a,b)=>{const l=[luminance(a),luminance(b)].sort((a,b)=>b-a);return (l[0]+.05)/(l[1]+.05);};
export async function checkInk(browser,base,repo,out){
 const checks=[], contrast=[],widths=[320,390,768,1280];
 for(const n of ['tokens','ds'])assert.equal(await readFile(path.join(repo,'internal/webui/static/css',n+'.css'),'utf8'),await readFile(path.join(repo,'aiplayer/static/css',n+'.css'),'utf8'),'AI mirror '+n);
 const original=execFileSync('git',['merge-base','HEAD','main'],{cwd:repo,encoding:'utf8'}).trim();
 async function htmlFiles(dir){let out=[];for(const ent of await readdir(dir,{withFileTypes:true})){const p=path.join(dir,ent.name);if(ent.isDirectory())out.push(...await htmlFiles(p));else if(ent.name.endsWith('.html'))out.push(p);}return out;}
 for(const f of await htmlFiles(path.join(repo,'internal/webui/static'))){
  const rel=path.relative(repo,f);let old;try{old=execFileSync('git',['show',original+':'+rel],{cwd:repo,encoding:'utf8',stdio:['ignore','pipe','ignore']});}catch{continue;}
  const next=await readFile(f,'utf8');
  for(const attr of ['id','aria-label','aria-labelledby','aria-describedby','aria-live','aria-hidden']){
   const pattern=new RegExp('\\b'+attr+'="([^"]*)"','g');
   const before=[...old.matchAll(pattern)].map(m=>m[1]);const after=[...next.matchAll(pattern)].map(m=>m[1]);
   for(const value of before){if(rel==='internal/webui/static/chat/index.html'&&attr==='aria-label'&&value==='首页')continue;if(rel==='internal/webui/static/chat/index.html'&&attr==='id'&&['wsmodal','ws-path','ws-refresh','ws-close','ws-list'].includes(value))continue;assert(after.includes(value),rel+' lost '+attr+'='+value);after.splice(after.indexOf(value),1);}
  }
 }
 const target='/games/chess?ui=1&state=playing';
 for(const width of widths){
  const c=await browser.newContext({viewport:{width,height:900},reducedMotion:'reduce',serviceWorkers:'block'});const p=await c.newPage();
  for(const route of ['/?ui=1','/games?ui=1',target,'/games/gomoku?ui=1&state=playing','/agent?ui=1','/minecraft','/account?ui=1']){
   await p.goto(base+route);if(route.startsWith('/agent'))await p.locator('#app').waitFor({state:'visible'});await p.waitForTimeout(120);
   assert(await p.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),route+' overflow at '+width);
  }
  checks.push({width,overflow:false});await c.close();
 }
 for(const theme of ['light','dark']){
  const ctx=await browser.newContext({viewport:{width:1280,height:900},reducedMotion:'reduce',colorScheme:theme,serviceWorkers:'block'});await ctx.addInitScript(t=>localStorage.setItem('mineagent.theme',t),theme);const page=await ctx.newPage();
  await page.goto(base+'/?ui=1');await page.locator('.hero-title').waitFor();
  const vars=await page.evaluate(()=>{const s=getComputedStyle(document.documentElement);return Object.fromEntries(['--page','--surface','--text','--text-secondary','--text-tertiary','--periwinkle','--tone-mc','--tone-sun','--tone-games','--on-accent'].map(n=>[n,s.getPropertyValue(n).trim()]));});
  for(const [fg,bg] of [['--text','--page'],['--text-secondary','--page'],['--text-tertiary','--page'],['--text','--surface'],['--on-accent','--tone-mc'],['--on-accent','--tone-sun'],['--on-accent','--tone-games']]){const value=ratio(vars[fg],vars[bg]);contrast.push({theme,fg,bg,ratio:+value.toFixed(2)});assert(value>=4.5,theme+' contrast '+fg+'/'+bg);}
  assert(ratio('#FFFFFF',vars['--periwinkle'])>=4.5,'solid blue/white contrast');
  assert.equal(await page.locator('.hero-lede,.launch-description').count(),0);
  assert(await page.locator('.hero-chip').evaluateAll(es=>es.every(e=>getComputedStyle(e).animationName==='none')),'reduced motion keeps floating chips');
  await page.keyboard.press('Tab');const focus=await page.evaluate(()=>{const s=getComputedStyle(document.activeElement);return {outline:s.outlineStyle,width:parseFloat(s.outlineWidth)};});assert(focus.outline!=='none'&&focus.width>=2,'keyboard focus missing');
  const homeStyles=await page.evaluate(()=>({dotGrid:getComputedStyle(document.body).backgroundImage,agent:getComputedStyle(document.querySelector('.launch-agent')).backgroundColor}));assert(homeStyles.dotGrid.includes('radial-gradient'),'paper dot grid absent');assert.equal(homeStyles.agent,'rgb(91, 99, 240)');
  await page.locator('.launch-example').first().focus();assert.equal(await page.locator('.launch-example').first().evaluate(e=>getComputedStyle(e).outlineColor),'rgb(255, 253, 247)','focus must remain visible on blue');
  await page.setViewportSize({width:390,height:844});assert.equal(await page.locator('.tab-pill').evaluate(e=>getComputedStyle(e).backgroundColor),'rgb(255, 211, 77)');assert.equal(await page.locator('.tabbar').evaluate(e=>getComputedStyle(e).left),'10px');
  await page.goto(base+'/agent?ui=1');await page.locator('#app').waitFor({state:'visible'});assert.equal(await page.locator('#newchat').evaluate(e=>getComputedStyle(e).backgroundColor),'rgb(91, 99, 240)','sidebar primary action lost to scoped navigation');await page.locator('#text').focus();assert.notEqual(await page.locator('.composer-box').evaluate(e=>getComputedStyle(e).boxShadow),'none');
  // Every selector is unique within its own file and media/support scope.
  const texture = await page.locator('main').evaluate(e => ({pattern:getComputedStyle(e).backgroundImage,size:getComputedStyle(e).backgroundSize}));
  assert(texture.pattern.includes('radial-gradient'), 'Agent paper dots absent');assert.equal(texture.size,'22px 22px');
  assert.equal(await page.locator('.composer-box').evaluate(e => getComputedStyle(e).backgroundImage),'none','paper pattern leaked into the input surface');
  await page.evaluate(async () => { const c=await import('/static/preview/js/composer.js');c.openModelPopover(false); });
  const selectedColors=await page.locator('#po-list .model.on').evaluate(e => {
   const hex=color=>'#'+color.match(/\d+/g).slice(0,3).map(n=>(+n).toString(16).padStart(2,'0')).join('');
   return {fg:hex(getComputedStyle(e.querySelector('.po-modeldetail')).color),bg:hex(getComputedStyle(e).backgroundColor)};
  });assert(ratio(selectedColors.fg,selectedColors.bg)>=4.5,'selected model metadata contrast');
  for(const route of ['/minecraft','/games/chess?ui=1','/agent?ui=1']){
   await page.goto(base+route);if(route.startsWith('/agent'))await page.locator('#app').waitFor({state:'visible'});
   const duplicates=await page.evaluate(names=>{
    const duplicates=[];
    for(const sheet of document.styleSheets){if(!sheet.href||!names.some(n=>sheet.href.endsWith('/'+n+'.css')))continue;const seen=new Set();
     const visit=(rules,context)=>{for(const r of rules){if(r.type===1){const key=context+'|'+r.selectorText;if(seen.has(key))duplicates.push(sheet.href.split('/').pop()+':'+key);seen.add(key);}else if(r.cssRules&&r.type!==7)visit(r.cssRules,context+'|'+(r.conditionText??r.name??r.cssText.split('{')[0]));}};visit(sheet.cssRules,'base');
    }return duplicates;
   },cssNames);assert.deepEqual(duplicates,[],'duplicate selector rules');
  }
  await ctx.close();
 }
 const summary={checks,contrast,domContracts:true,mirrors:true,reducedMotion:true,focus:true,uniqueSelectors:true};await writeFile(path.join(out,'acceptance.json'),JSON.stringify(summary,null,2));return summary;
}
