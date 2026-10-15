// 入口：登录、侧栏、启动装配

import { boot as bootShell, shell } from './shell.js';
import { passkeyForm } from './passkeys.js';
import { $, toast } from './ui.js';
import { S, saveAuth, clearAuth } from './state.js';
import { api, logoutRequest, setUnauthorizedHandler } from './api.js';
import { initTheme, openAppearance } from './theme.js';
import { initWorkspace, openWorkspace } from './workspace.js';
import * as conversations from './conversations.js';
import * as chat from './chat.js';
import * as composer from './composer.js';
import { initPromptSettings, refreshPromptLabel } from './prompt-settings.js';

// ---------- 登录 / 登出 ----------
function showLogin() {
  const el = $('login');
  const card = el.querySelector('.login-card');
  card.replaceChildren(passkeyForm((data) => {
    S.token = data.token; S.me = data.name; S.admin = !!data.admin; saveAuth();
    bootShell({ active: '/agent', requireLogin: false }).catch(() => {});
    startChat().then(applyURLTarget).catch((error) => toast(error.message, 'err'));
  }));
  const home = document.createElement('a'); home.href = '/'; home.className = 'login-home'; home.textContent = '← 回到首页'; card.appendChild(home);
  el.style.display = 'flex'; el.classList.remove('leave');
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


function logout(silent) {
  clearAuth();
  S.options = null;
  document.querySelectorAll('.modal.on').forEach((el) => el.classList.remove('on'));
  chat.disconnect();
  chat.clearMessages();
  hideApp(() => showLogin());
}

async function startChat() {
  hideLogin(); showApp();
  $('myname').textContent = S.me;
  $('myavatar').textContent = (S.me[0] || '?').toUpperCase();
  $('myrole').textContent = S.admin ? '管理员' : '';
  $('myrole').className = 'badge' + (S.admin ? ' admin' : '');
  $('menu-ws').hidden = !S.admin;
  chat.clearMessages();
  await conversations.refresh();
  await chat.loadHistory();
  chat.connectSSE();
  composer.loadOptions().catch(() => {});
  refreshPromptLabel().catch(() => {});
  composer.renderPending();
  composer.autoGrow();
  $('sidebar').classList.remove('open');
  $('backdrop').classList.remove('on');
  if (!document.querySelector('.dialog-mask')) $('text').focus();
}

// ---------- 额度 ----------
async function openUsage() {
  $('usagemodal').classList.add('on');
  const body = $('usage-body');
  body.innerHTML = '<div class="usage-loading">加载中…</div>';
  try {
    const r = await api('/api/usage');
    const u = r.usage || {};
    const rows = [['滚动窗口（5 小时）', u.rolling], ['本周', u.weekly], ['本月', u.monthly]];
    body.innerHTML = rows.map(([label, win]) => {
      const pct = Math.max(0, Math.min(100, (win && win.percent) || 0));
      const cls = pct >= 90 ? ' hot' : pct >= 70 ? ' warm' : '';
      const reset = win && win.resetsAt ? '重置：' + new Date(win.resetsAt).toLocaleString() : '';
      return '<div class="usage-row"><div class="usage-top"><span>' + label + '</span><span class="pct">' + pct + '%</span></div>' +
        '<div class="usage-bar"><div class="usage-fill' + cls + '" style="width:' + pct + '%"></div></div>' +
        '<div class="usage-reset">' + reset + '</div></div>';
    }).join('') +
      '<div class="usage-note">' + (r.text || '') + '<br>百分比是网关（opencode）计费窗口内的已用比例，到点自动重置。</div>';
  } catch (e) {
    body.innerHTML = '<div class="usage-loading">' + e.message + '</div>';
  }
}

// ---------- 侧栏 ----------
function openSidebar() { $('sidebar').classList.add('open'); $('backdrop').classList.add('on'); }
function closeSidebar() { $('sidebar').classList.remove('open'); $('backdrop').classList.remove('on'); }

// 页面比服务端旧（开着标签页时后端更新过）→ 自动带新参数刷新一次，避免旧 JS 一直用下去
async function ensureFresh() {
  try {
    const cur = document.querySelector('meta[name="mineagent-version"]')?.content || '';
    console.info('[mineagent] web', cur);
    const res = await fetch('/api/version', { cache: 'no-store' });
    const { version } = await res.json();
    if (!version || !cur || cur === version) return;
    if (sessionStorage.getItem('mineagent.reloaded') === version) return;   // 防循环
    sessionStorage.setItem('mineagent.reloaded', version);
    const u = new URL(location.href);
    u.searchParams.set('_v', version);
    console.warn('[mineagent] 页面版本', cur, '比服务端', version, '旧，自动刷新');
    location.replace(u.toString());
  } catch (e) { /* 离线等场景忽略 */ }
}

// ---------- 启动 ----------
async function boot() {
  initTheme();
  initPromptSettings();
  ensureFresh();
  await bootShell({ active: '/agent', requireLogin: false, preview: new URLSearchParams(location.search).has('ui') });
  // 渲染调试模式：?ui=1 不连后端，直接用假数据渲染（给截图/视觉对比用）
  if (new URLSearchParams(location.search).has('ui')) {
    import('./debug.js').then((m) => m.startDebug());
    return;
  }
  initWorkspace();
  conversations.initConversations();
  setUnauthorizedHandler(() => logout(true));

  // 账户菜单：Workspace / 外观 / 退出登录
  const closeAccount = () => { $('accountmenu').classList.remove('on'); $('userbtn').setAttribute('aria-expanded', 'false'); };
  $('userbtn').onclick = (e) => { e.stopPropagation(); $('accountmenu').classList.toggle('on'); $('userbtn').setAttribute('aria-expanded', String($('accountmenu').classList.contains('on'))); };
  document.addEventListener('click', (e) => {
    if (!e.target.closest('#accountmenu') && !e.target.closest('#userbtn')) closeAccount();
  });
  document.addEventListener('keydown', (event) => { if (event.key === 'Escape' && $('accountmenu').classList.contains('on')) { closeAccount(); $('userbtn').focus(); } });
  $('menu-ws').onclick = () => { closeAccount(); openWorkspace(); };
  $('menu-theme').onclick = () => { closeAccount(); openAppearance(); };
  $('menu-usage').onclick = () => { closeAccount(); openUsage(); };
  $('usage-close').onclick = () => $('usagemodal').classList.remove('on');
  $('usagemodal').addEventListener('click', (e) => { if (e.target === $('usagemodal')) $('usagemodal').classList.remove('on'); });
  $('menu-logout').onclick = async () => { closeAccount(); try { await logoutRequest(); logout(false); bootShell({ active: '/agent', requireLogin: false }).catch(() => {}); } catch (error) { toast('退出失败，请恢复连接后重试', 'err'); } };
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

  if (shell.user) {
    api('/api/me').then((res) => {
      S.admin = !!res.admin; S.me = res.name;
      saveAuth();
      startChat().then(applyURLTarget);
    }).catch(() => logout(true));
  } else {
    logout(true);
  }
}

// URL 直达：/agent?new=1 开新对话；/agent?conv=<id> 打开指定会话（Portal 首页卡片用）。
async function applyURLTarget() {
  const q = new URLSearchParams(location.search);
  const wantConv = q.get('conv');
  const wantNew = q.has('new');
  const ask = q.get('q');           // 首页 Quick Composer 带过来的问题
  if (wantNew || ask) {
    conversations.newChat();
  } else if (wantConv && S.conversations.some((c) => c.conv === wantConv)) {
    await conversations.switchTo(wantConv);
  }
  if (ask) {
    // URL/example prompts remain drafts until the user explicitly sends them.
    const box = document.getElementById('text');
    box.value = ask;
    composer.autoGrow();
    composer.updateSendBtn();
    box.focus();
  }
  if (wantNew || wantConv || ask) {
    const clean = new URL(location.href); ['new', 'conv', 'q'].forEach((key) => clean.searchParams.delete(key)); history.replaceState(null, '', clean.pathname + clean.search + clean.hash);
  }
}

boot();
