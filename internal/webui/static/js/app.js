// 入口：登录、侧栏、启动装配

import { $, toast } from './ui.js';
import { S, saveAuth, clearAuth } from './state.js';
import { api, logoutRequest, setUnauthorizedHandler } from './api.js';
import { initTheme, openAppearance } from './theme.js';
import { initWorkspace, openWorkspace } from './workspace.js';
import { initConversations, refresh as refreshConversations } from './conversations.js';
import * as chat from './chat.js';
import * as composer from './composer.js';

// ---------- 登录 / 登出 ----------
function showLogin() {
  const el = $('login');
  // 上次的名字/浏览器自动填充会让人误以为"空着也能进"（实际提交的是旧名字），统一清掉
  const nameInput = $('name');
  if (nameInput) nameInput.value = '';
  el.style.display = 'flex';
  el.classList.remove('leave');
  el.classList.add('enter');
  setTimeout(() => el.classList.remove('enter'), 300);
  setTimeout(() => nameInput && nameInput.focus(), 60);
}
function hideLogin() {
  const el = $('login');
  if (el.style.display === 'none') return;
  el.classList.add('leave');
  setTimeout(() => { el.style.display = 'none'; el.classList.remove('leave'); }, 170);
}
function showApp() {
  const el = $('app');
  el.style.display = 'block';
  el.classList.remove('leave');
  el.classList.add('enter');
  setTimeout(() => el.classList.remove('enter'), 310);
}
function hideApp(done) {
  const el = $('app');
  if (el.style.display === 'none') { if (done) done(); return; }
  el.classList.add('leave');
  setTimeout(() => { el.style.display = 'none'; el.classList.remove('leave'); if (done) done(); }, 170);
}

async function doLogin(name) {
  $('enter').disabled = true;
  $('loginerr').textContent = '';
  try {
    const res = await fetch('/api/login', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: name })
    });
    const body = await res.json().catch(() => null);
    if (!res.ok) throw new Error((body && body.error) || ('HTTP ' + res.status));
    S.token = body.token; S.me = body.name; S.admin = !!body.admin;
    saveAuth();
    startChat();
  } catch (e) {
    $('loginerr').textContent = e.message;
    toast(e.message, 'err');
  } finally {
    $('enter').disabled = false;
  }
}

function logout(silent) {
  clearAuth();
  chat.disconnect();
  chat.clearMessages();
  hideApp(() => showLogin());
  if (!silent) $('loginerr').textContent = '';
}

async function startChat() {
  hideLogin(); showApp();
  $('myname').textContent = S.me;
  $('myavatar').textContent = (S.me[0] || '?').toUpperCase();
  $('myrole').textContent = S.admin ? '管理员' : '';
  $('myrole').className = 'badge' + (S.admin ? ' admin' : '');
  $('menu-ws').hidden = !S.admin;
  chat.clearMessages();
  await refreshConversations();
  await chat.loadHistory();
  chat.connectSSE();
  composer.loadOptions().catch(() => {});
  composer.renderPending();
  composer.autoGrow();
  $('sidebar').classList.remove('open');
  $('backdrop').classList.remove('on');
  $('text').focus();
}

// ---------- 侧栏 ----------
function openSidebar() { $('sidebar').classList.add('open'); $('backdrop').classList.add('on'); }
function closeSidebar() { $('sidebar').classList.remove('open'); $('backdrop').classList.remove('on'); }

// ---------- 启动 ----------
function boot() {
  initTheme();
  // 渲染调试模式：?ui=1 不连后端，直接用假数据渲染（给截图/视觉对比用）
  if (new URLSearchParams(location.search).has('ui')) {
    import('./debug.js').then((m) => m.startDebug());
    return;
  }
  initWorkspace();
  initConversations();
  setUnauthorizedHandler(() => logout(true));

  $('enter').onclick = () => doLogin($('name').value.trim());
  $('name').addEventListener('keydown', (e) => { if (e.key === 'Enter') $('enter').click(); });

  // 账户菜单：Workspace / 外观 / 退出登录
  const closeAccount = () => $('accountmenu').classList.remove('on');
  $('userbtn').onclick = (e) => { e.stopPropagation(); $('accountmenu').classList.toggle('on'); };
  document.addEventListener('click', (e) => {
    if (!e.target.closest('#accountmenu') && !e.target.closest('#userbtn')) closeAccount();
  });
  $('menu-ws').onclick = () => { closeAccount(); openWorkspace(); };
  $('menu-theme').onclick = () => { closeAccount(); openAppearance(); };
  $('menu-logout').onclick = () => { closeAccount(); logoutRequest(); logout(false); };
  $('menu').onclick = () => $('sidebar').classList.contains('open') ? closeSidebar() : openSidebar();
  $('backdrop').onclick = closeSidebar;
  $('collapse').onclick = () => {
    const collapsed = document.body.classList.toggle('collapsed');
    localStorage.setItem('mineagent.sidebar', collapsed ? 'collapsed' : '');
  };
  if (localStorage.getItem('mineagent.sidebar') === 'collapsed') document.body.classList.add('collapsed');

  $('loadolder').onclick = chat.loadOlder;
  $('list').addEventListener('scroll', chat.updateToBottom);
  $('tobottom').onclick = chat.toBottom;

  if (S.token && S.me) {
    api('/api/me').then((res) => {
      S.admin = !!res.admin; S.me = res.name;
      saveAuth();
      startChat();
    }).catch(() => logout(true));
  } else {
    logout(true);
  }
}

boot();
