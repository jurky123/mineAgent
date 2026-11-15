// Independent, authenticated resource and capability pages. Read-only APIs only.
import { S } from './state.js';
import { api } from './api.js';
import { openViewer } from './viewer.js';
import { ICON, fmtSize } from './ui.js';

export const agentSection = ({'/agent/library':'library','/agent/tools':'tools'})[location.pathname] || '';
if (agentSection) { document.body.dataset.agentSection = agentSection; document.title = (agentSection === 'library' ? '资料空间' : '插件与工具') + ' · zkun'; }
let serial = 0, entries = [], skills = [], capabilities = [], directory = '', query = '', category = 'all';
let view = localStorage.getItem('mineagent.libraryView') === 'grid' ? 'grid' : 'list';
let sort = 'name', toolsTab = new URLSearchParams(location.search).get('tab') === 'tools' ? 'tools' : 'skills';
const $ = id => document.getElementById(id);
const el = (tag, cls, text) => { const n=document.createElement(tag); if(cls)n.className=cls; if(text!==undefined)n.textContent=text; return n; };
function button(text, action, cls='section-button') { const b=el('button',cls,text); b.type='button'; b.onclick=action; return b; }
function icon(html) { const n=el('span','resource-icon'); n.innerHTML=html; return n; }
const status = value => ({available:'可用',approval:'需审批',admin:'管理员专属',unavailable:'不可用'})[value] || '状态未知';
const fileKind = e => /\.(png|jpe?g|gif|webp|avif|bmp|svg)$/i.test(e.name) ? 'images' : /\.(md|txt|pdf|docx?|xlsx?|csv|json|ya?ml|html|pptx?)$/i.test(e.name) ? 'documents' : 'other';
const closeDrawer = () => { if ($('sidebar').classList.contains('open')) $('menu').click(); };
export function clearAgentSection() { serial++; entries=[];skills=[];capabilities=[]; $('section-content')?.replaceChildren(); $('section-sidebar')?.replaceChildren(); }

export async function mountAgentSection() {
  if (!agentSection) return;
  $('section-content').hidden=false; $('section-sidebar').hidden=false;
  directory=new URLSearchParams(location.search).get('path') || '';
  await load();
}
async function load() {
  const ticket=++serial, token=S.token;
  entries=[];skills=[];capabilities=[];
  renderShell();
  $('section-results').replaceChildren(el('p','section-empty','正在读取…'));
  if (agentSection==='library' && !S.admin) {
    $('section-count').textContent='管理员可用';
    $('section-results').replaceChildren(el('h2',null,'需要管理员权限'),el('p','section-note','资料空间目前使用管理员工作区，当前账号无法读取其中的文件。'));
    return;
  }
  try {
    let data;
    if (S.token==='debug') {
      data=agentSection==='library' ? {path:directory,entries:directory?[]:[{name:'项目资料',dir:true},{name:'农场规划.md',size:2048,mtime:1700000000000},{name:'小木屋.png',size:85420,mtime:1700000000000},{name:'服务说明.pdf',size:14520,mtime:1700000000000}]} : {skills:S.options?.skills||[],capabilities:[{name:'current_time',kind:'查询',status:'available'},{name:'workspace_exec',kind:'文件与执行',status:'admin'}]};
    } else data=await api(agentSection==='library'?'/api/workspace?path='+encodeURIComponent(directory):'/api/options');
    if(ticket!==serial || token!==S.token)return;
    if(agentSection==='library') { if(!Array.isArray(data.entries))throw new Error('资料响应格式无效');entries=data.entries;directory=data.path||''; }
    else { skills=Array.isArray(data.skills)?data.skills:[];capabilities=Array.isArray(data.capabilities)?data.capabilities:[]; }
    renderShell();renderResults();
  } catch(error) {
    if(ticket!==serial || token!==S.token)return;
    $('section-results').replaceChildren(el('h2',null,'暂时无法读取'),el('p','section-note',error.message),button('重试',load));
  }
}
function renderShell() {
  const side=$('section-sidebar');side.replaceChildren();
  side.append(el('h2','section-sidebar-title',agentSection==='library'?'资料空间':'自定义'));
  const nav=el('nav','section-navigation');nav.setAttribute('aria-label',agentSection==='library'?'资料分类':'能力分类');
  const choices=agentSection==='library'?[['all','全部'],['documents','文档'],['images','图像'],['other','其他文件']]:[['skills','插件与技能'],['tools','工具']];
  for(const [key,label] of choices) {
    const active=agentSection==='library'?category===key:toolsTab===key;
    const b=button(label,()=>{if(agentSection==='library')category=key;else { toolsTab=key;const url=new URL(location.href);if(key==='tools')url.searchParams.set('tab','tools');else url.searchParams.delete('tab');history.replaceState(null,'',url); }closeDrawer();renderShell();renderResults();},'section-nav-item');
    b.setAttribute('aria-pressed',String(active));nav.append(b);
  }
  side.append(nav);
  const recent=agentSection==='library'?entries.filter(e=>!e.dir).sort((a,b)=>(b.mtime||0)-(a.mtime||0)).slice(0,6):skills.slice(0,8);
  if(recent.length) {
    side.append(el('p','section-sidebar-label',agentSection==='library'?'当前目录 · 最近修改':'已配置'));
    for(const item of recent) {
      const b=button(item.name,()=>{query=item.name;if(agentSection==='tools')toolsTab='skills';else category='all';closeDrawer();renderShell();renderResults();},'section-recent');b.title=item.name;side.append(b);
    }
  }
  const root=$('section-content');root.replaceChildren();
  const board=el('div','section-board');root.append(board);
  const head=el('header','section-page-head');
  const menu=button('',()=>$('menu').click(),'section-icon-button section-menu');menu.innerHTML='<svg class="i"><use href="#i-menu"/></svg>';menu.id='section-menu';menu.setAttribute('aria-label','打开页面导航');
  const collapse=button('',()=>$('collapse').click(),'section-icon-button section-collapse');collapse.innerHTML='<svg class="i"><use href="#i-panel"/></svg>';collapse.setAttribute('aria-label','收起页面导航');
  head.append(menu,collapse,el('h1',null,agentSection==='library'?choices.find(([key])=>key===category)[1]:toolsTab==='skills'?'插件与技能':'工具'));
  const search=el('input','section-search');search.type='search';search.placeholder=agentSection==='library'?'搜索当前目录':'搜索名称或说明';search.setAttribute('aria-label',agentSection==='library'?'搜索资料':'搜索插件与工具');search.value=query;
  search.oninput=()=>{query=search.value;renderResults();};
  const home=el('a','section-button section-home-link','回门户');home.href='/';home.setAttribute('aria-label','回门户');
  head.append(search,button('刷新',load),home);board.append(head);
  const controls=el('div','section-controls');
  if(agentSection==='library') {
    const path=el('nav','section-breadcrumbs');path.setAttribute('aria-label','目录路径');
    path.append(button('资料空间',()=>openDirectory('')));
    let current='';for(const name of directory.split('/').filter(Boolean)) {current=current?current+'/'+name:name;const destination=current;path.append(el('span',null,'/'),button(name,()=>openDirectory(destination)));}
    controls.append(path);
    const order=el('select','section-order');order.setAttribute('aria-label','资料排序');
    for(const [value,text] of [['name','名称排序'],['modified','最近修改']]) {const option=el('option',null,text);option.value=value;order.append(option);}order.value=sort;order.onchange=()=>{sort=order.value;renderResults();};controls.append(order);
    for(const [mode,label] of [['list','列表'],['grid','网格']]) {const b=button(label,()=>{view=mode;localStorage.setItem('mineagent.libraryView',mode);renderShell();renderResults();});b.setAttribute('aria-pressed',String(view===mode));controls.append(b);}
  } else controls.append(el('span','section-note','来自当前账号已配置的技能与工具'));
  const count=el('span','section-count');count.id='section-count';count.setAttribute('role','status');controls.append(count);board.append(controls);
  const results=el('div','section-results');results.id='section-results';board.append(results);
}
function openDirectory(path) {
  directory=path;query='';const url=new URL(location.href);if(path)url.searchParams.set('path',path);else url.searchParams.delete('path');history.replaceState(null,'',url);load();
}
function renderResults() {
  const root=$('section-results');if(!root)return;root.replaceChildren();
  if(agentSection==='library' && !S.admin) {root.append(el('h2',null,'需要管理员权限'));return;}
  const q=query.trim().toLocaleLowerCase();
  if(agentSection==='library') {
    const list=entries.filter(e=>(!q||e.name.toLocaleLowerCase().includes(q))&&(category==='all'||e.dir||fileKind(e)===category)).sort((a,b)=>Number(!!b.dir)-Number(!!a.dir)||(sort==='modified'?(b.mtime||0)-(a.mtime||0):0)||a.name.localeCompare(b.name));
    $('section-count').textContent=list.length+' 项'+(entries.length===500?' · 当前目录最多展示500项':'');
    root.className='section-results '+(view==='grid'?'resource-grid':'resource-table');
    if(!list.length){root.append(el('p','section-empty',entries.length?'没有匹配的资料':'这个目录还是空的'));return;}
    if(view==='list') {const labels=el('div','resource-table-head');labels.append(el('span',null,'名称'),el('span',null,'大小'),el('span',null,'修改时间'));root.append(labels);}
    for(const entry of list) {
      const path=directory?directory+'/'+entry.name:entry.name;
      const row=el(entry.dir||S.token==='debug'?'button':'a','resource-row');
      if(entry.dir){row.type='button';row.onclick=()=>openDirectory(path);row.setAttribute('aria-label','打开文件夹 '+entry.name);}
      else if(S.token==='debug'){row.type='button';row.onclick=()=>{row.title='演示文件，不连接真实下载';};}
      else {row.href='/api/workspace/file?path='+encodeURIComponent(path);row.download=entry.name;const image=/\.(png|jpe?g|gif|webp|avif|bmp)$/i.test(entry.name);row.setAttribute('aria-label',(image?'预览 ':'下载 ')+entry.name);if(image)row.onclick=event=>{event.preventDefault();openViewer([{url:row.href,name:entry.name}],0);};}
      const glyph=entry.dir?icon(ICON.folder):el('span','resource-icon file-type',entry.name.split('.').pop().slice(0,5).toUpperCase());
      const name=el('span','resource-name',entry.name);name.title=entry.name;
      row.append(glyph,name,el('span','resource-size',entry.dir?'文件夹':fmtSize(entry.size||0)),el('time','resource-date',entry.mtime?new Date(entry.mtime).toLocaleString('zh-CN',{dateStyle:'medium'}):'—'));root.append(row);
    }
  } else {
    const items=(toolsTab==='skills'?skills:capabilities).filter(item=>!q||[item.name,item.desc,item.kind,item.reason].filter(Boolean).join(' ').toLocaleLowerCase().includes(q));
    $('section-count').textContent=items.length+' 项';root.className='section-results capability-grid';
    if(!items.length){root.append(el('p','section-empty',q?'没有匹配的条目':toolsTab==='skills'?'当前没有配置插件或技能':'当前没有可展示的工具'));return;}
    for(const item of items) {
      const card=el('article','capability-card');const body=el('div','capability-body');const head=el('div','capability-title');head.append(el('h2',null,item.name));
      const state=item.status|| (toolsTab==='skills'?'available':'unknown');const badge=el('span','capability-status',status(state));badge.dataset.state=state;head.append(badge);body.append(head,el('p',null,item.reason||item.desc||item.kind||'已配置技能'));
      card.append(icon(toolsTab==='skills'?'<svg class="i"><use href="#i-spark"/></svg>':ICON.file),body);
      if(toolsTab==='skills' && state!=='admin' && state!=='unavailable') {const use=el('a','section-button','在会话中使用');use.href='/agent?new=1&q='+encodeURIComponent(item.prompt||item.name);card.append(use);}
      root.append(card);
    }
  }
}
