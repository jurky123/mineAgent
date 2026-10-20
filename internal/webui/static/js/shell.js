// shell.js — 门户各页共用的顶栏 / 登录态 / 账户菜单 / 主题 / API 封装。
// 页面约定：<div id="shell-top"></div> + 自己的内容 + 调 shell.boot(active)。
// 未登录时渲染整页登录态（不是弹窗），登录后才进入门户。

import { passwordForm } from './passwords.js';
import { h, icon, toast, gameIcon, segThumb, reducedMotion } from './ds.js';

// Leaving a document or opting out of a cross-document animation can reject
// ready. Consume the transition promises without suppressing application errors.
for (const type of ['pageswap', 'pagereveal']) window.addEventListener(type, (event) => {
  event.viewTransition?.ready.catch(() => {});
  event.viewTransition?.finished.catch(() => {});
  if (type === 'pageswap' && event.viewTransition) {
    const next = new URL(event.activation?.entry?.url || location.href).pathname;
    if (next === '/ai' || next.startsWith('/ai/')) event.viewTransition.skipTransition();
  }
});

// 命令面板里的棋类图标（用真实棋子/石图）
function gameIconEl(id, cls) { return gameIcon(id, cls); }

const TOKEN_KEY = 'mineagent.token';
const NAME_KEY = 'mineagent.name';
const LAST_KEY = 'mineagent.lastName';
const SHELL_CACHE_KEY = 'mineagent.shell';   // 上次的 apps + user：新页面首帧就画完整顶栏，不等接口

export const shell = {
  token: localStorage.getItem(TOKEN_KEY) || '',
  user: null,
  apps: [],
};

// 预览模式（?ui=1）：走正式 Shell 渲染路径，只把数据源换成假的——
// 这样截图/录屏里顶部导航、账号菜单、空间尺寸都与线上一致。
export const PREVIEW_USER = { id: 1, name: 'jzk', admin: true };
export const PREVIEW_APPS = [
  { id: 'agent', name: 'MineAgent', nav: true, navLabel: 'Agent', path: '/agent?ui=1', icon: 'sparkle', homeRole: 'hero' },
  { id: 'games', name: '游戏', nav: true, navLabel: '游戏', path: '/games?ui=1', icon: 'gamepad', homeRole: 'content' },
];

// pageVersion 页面注入的版本号（静态资源带版本，避免缓存混用）。
export function pageVersion() {
  return (document.querySelector('meta[name=mineagent-version]') || {}).content || '';
}

// ---------- 主题（浅色 / 深色 / 跟随系统；和 Agent 页共用 mineagent.theme） ----------
const MODES = [
  { id: 'system', label: '跟随系统', icon: 'monitor' },
  { id: 'light', label: '浅色', icon: 'sun' },
  { id: 'dark', label: '深色', icon: 'moon' },
];
const mql = window.matchMedia('(prefers-color-scheme: dark)');

export function themeMode() { return localStorage.getItem('mineagent.theme') || 'system'; }

export function applyTheme() {
  const m = themeMode();
  const dark = m === 'dark' || (m === 'system' && mql.matches);
  document.documentElement.dataset.theme = dark ? 'dark' : 'light';
  document.documentElement.dataset.mode = m;
}

export function setTheme(mode) {
  localStorage.setItem('mineagent.theme', mode);
  // 整页 crossfade，避免几千个元素同时翻色（不支持的浏览器直接瞬时切换）。
  // html.vt-theme 让转场只做淡化、不套用跨页的上浮位移。
  if (document.startViewTransition && !reducedMotion()) {
    const root = document.documentElement;
    root.classList.add('vt-theme');
    const vt = document.startViewTransition(() => applyTheme());
    vt.ready.catch(() => {});
    vt.finished.then(() => root.classList.remove('vt-theme'), () => root.classList.remove('vt-theme'));
  } else {
    applyTheme();
  }
}

export function fmtTime(ms) {
  if (!ms) return '';
  const d = new Date(ms);
  const now = new Date();
  const pad = (n) => String(n).padStart(2, '0');
  const startOfToday = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
  if (d.getTime() >= startOfToday) return '今天 ' + pad(d.getHours()) + ':' + pad(d.getMinutes());
  if (d.getTime() >= startOfToday - 86400000) return '昨天 ' + pad(d.getHours()) + ':' + pad(d.getMinutes());
  return (d.getMonth() + 1) + '月' + d.getDate() + '日';
}

// ---------- API ----------
export async function api(path, opts) {
  opts = opts || {};
  opts.headers = Object.assign({}, opts.headers,
    shell.token ? { Authorization: 'Bearer ' + shell.token } : {});
  const res = await fetch(path, opts);
  let body = null;
  try { body = await res.json(); } catch (e) { /* 空响应 */ }
  if (res.status === 401) {
    clearAuth();
    throw Object.assign(new Error('未登录'), { unauthorized: true });
  }
  if (!res.ok) throw new Error((body && body.error) || ('HTTP ' + res.status));
  return body;
}

export const apiGet = (path) => api(path);
export const apiPost = (path, payload) => api(path, {
  method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload || {}),
});

function saveAuth(user, token) {
  shell.user = user;
  if (token) shell.token = token;
  if (shell.token) localStorage.setItem(TOKEN_KEY, shell.token);
  if (user) {
    localStorage.setItem(NAME_KEY, user.name);
    localStorage.setItem(LAST_KEY, user.name);
    localStorage.setItem('mineagent.admin', user.admin ? '1' : '0');
  }
}

export function clearAuth() {
  shell.user = null;
  shell.token = '';
  localStorage.removeItem(TOKEN_KEY);
  localStorage.removeItem(NAME_KEY);
  localStorage.removeItem('mineagent.admin');
  localStorage.removeItem(SHELL_CACHE_KEY);
}

export async function logout() {
  try { await apiPost('/api/auth/logout'); } catch (e) { toast('退出失败，请恢复连接后重试', { warn: true }); return; }
  clearAuth();
  location.href = '/';
}

// ---------- 整页登录态 ----------
function loginView() {
  return new Promise((resolve) => {
    const view = h('div', 'login-view');
    const box = h('div', 'login-box');
    box.appendChild(passwordForm((data) => {
      saveAuth({ id: data.id, name: data.name, admin: !!data.admin }, data.token);
      view.remove(); resolve(shell.user);
    }));
    view.appendChild(box); document.body.appendChild(view);
  });
}

// ---------- 顶栏 ----------
function paintTop(active) {
  const host = document.getElementById('shell-top');
  if (!host) return;
  host.innerHTML = '';
  const bar = h('header', 'topbar');

  const brand = h('a', 'brand');
  brand.href = '/';
  const mark = h('img', 'brand-logo');
  mark.src = '/static/' + pageVersion() + '/img/logo.png';
  mark.alt = '';
  brand.appendChild(mark);
  brand.appendChild(h('span', null, 'zkun'));
  bar.appendChild(brand);

  const nav = h('nav', 'nav');
  const mk = (label, path, iconName) => {
    const on = path.split('?')[0] === active || (path === '/games' && active.startsWith('/games/'));

    const a = h('a', 'nav-item' + (on ? ' on' : ''));
    a.href = path;
    if (on) a.setAttribute('aria-current', 'page');
    if (on) a.appendChild(h('span', 'nav-pill'));
    a.appendChild(icon(iconName));
    a.appendChild(h('span', null, label));
    nav.appendChild(a);
  };
  mk('首页', '/', 'home');
  for (const app of shell.apps) {
    if (app.enabled && (!app.adminOnly || shell.user?.admin) && app.nav && app.path && ['agent', 'games'].includes(app.id)) mk(app.navLabel || app.name, app.path, app.icon || 'info');
  }
  mk('Minecraft', '/minecraft', 'pickaxe');
  bar.appendChild(nav);
  bar.appendChild(h('div', 'spacer'));

  // 主题选择器（明确列出三个选项，不再循环切换）
  const themeWrap = h('div', 'pop-wrap theme-wrap');
  const themeBtn = h('button', 'icon-btn');
  themeBtn.title = '外观'; themeBtn.setAttribute('aria-label', '外观'); themeBtn.setAttribute('aria-expanded', 'false');
  themeBtn.appendChild(icon('sun'));
  const themePop = h('div', 'pop');
  themePop.appendChild(h('div', 'pop-head', '外观'));
  const paintTheme = () => {
    themePop.querySelectorAll('.pop-item').forEach((el) => {
      el.classList.toggle('on', el.dataset.mode === themeMode());
    });
  };
  for (const m of MODES) {
    const item = h('button', 'pop-item');
    item.dataset.mode = m.id;
    item.appendChild(icon(m.icon));
    item.appendChild(h('span', null, m.label));
    item.appendChild(icon('check', 'sm check'));
    item.onclick = () => { setTheme(m.id); paintTheme(); };
    themePop.appendChild(item);
  }
  paintTheme();
  themeBtn.onclick = (e) => { e.stopPropagation(); closePops(themePop); togglePop(themePop); };
  themePop.onclick = (e) => e.stopPropagation();
  themeWrap.appendChild(themeBtn);
  themeWrap.appendChild(themePop);
  bar.appendChild(themeWrap);

  // 账户菜单
  const accWrap = h('div', 'pop-wrap');
  const accBtn = h('button', 'icon-btn account-btn');
  accBtn.title = shell.user ? shell.user.name : '登录'; accBtn.setAttribute('aria-label', shell.user ? shell.user.name + '的账户菜单' : '登录菜单'); accBtn.setAttribute('aria-expanded', 'false');
  if (shell.user) {
    const av = h('span', 'avatar', (shell.user.name[0] || '?'));
    // Portal → Account 的头像共享元素。账号页自己的大头像已经叫 user-avatar，
    // 名字重复会让浏览器放弃整个转场（连主题切换的淡化也没了），所以在账号页顶栏头像不命名。
    if (!active.startsWith('/account')) av.style.viewTransitionName = 'user-avatar';
    accBtn.appendChild(av);
  }
  else accBtn.appendChild(icon('user'));
  const accPop = h('div', 'pop');
  if (shell.user) {
    const head = h('div', 'pop-head');
    head.appendChild(h('div', 'pop-name', shell.user.name));
    head.appendChild(h('div', 'pop-title', 'ID ' + shell.user.id + (shell.user.admin ? ' · 管理员' : '')));
    accPop.appendChild(head);
    const item = (label, ic, fn, cls) => {
      const b = h('button', 'pop-item' + (cls ? ' ' + cls : ''));
      b.appendChild(icon(ic));
      b.appendChild(h('span', null, label));
      b.onclick = fn;
      accPop.appendChild(b);
    };
    item('显示屏工具', 'monitor', () => { location.href = '/screens'; });
    item('我的账号', 'user', () => { location.href = '/account'; });
    // 手机上没有主题按钮：把外观放进账号菜单（三个明确选项）
    const themeRow = h('div', 'menu-theme');
    themeRow.appendChild(h('div', 'menu-theme-title', '外观'));
    const seg = h('div', 'seg');
    let syncThumb = () => {};
    const paintSeg = () => {
      seg.querySelectorAll('.seg-item').forEach((el) => el.classList.toggle('on', el.dataset.mode === themeMode()));
      syncThumb();
    };
    for (const m of MODES) {
      const b = h('button', 'seg-item', m.label);
      b.dataset.mode = m.id;
      b.onclick = () => { setTheme(m.id); paintSeg(); };
      seg.appendChild(b);
    }
    paintSeg();
    syncThumb = segThumb(seg);
    themeRow.appendChild(seg);
    accPop.appendChild(themeRow);
    item('退出登录', 'logout', () => logout(), 'danger-item');
  } else {
    const b = h('button', 'pop-item');
    b.appendChild(icon('user'));
    b.appendChild(h('span', null, '登录'));
    b.onclick = () => { location.href = '/account'; };
    accPop.appendChild(b);
  }
  accBtn.onclick = (e) => { e.stopPropagation(); closePops(accPop); togglePop(accPop); };
  accPop.onclick = (e) => e.stopPropagation();
  accWrap.appendChild(accBtn);
  accWrap.appendChild(accPop);
  bar.appendChild(accWrap);

  host.appendChild(bar);
  paintTabbar(active);
  if (!paintTop.bound) { document.addEventListener('click', () => closePops());
    document.addEventListener('keydown', (event) => { if (event.key === 'Escape') { const open = document.querySelector('.pop.on'); if (open) { const trigger = open.parentElement.querySelector('button'); closePops(); trigger?.focus(); } } });
    paintTop.bound = true; }
}

// 手机底部 tabbar：拇指可达；≤640px 显示（顶栏导航同时隐藏）
function paintTabbar(active) {
  const old = document.getElementById('tabbar');
  if (old) old.remove();
  const items = [
    { label: '首页', path: '/', icon: 'home' },
    { label: 'Agent', path: '/agent', icon: 'sparkle' },
    { label: '游戏', path: '/games', icon: 'gamepad' },
    { label: 'Minecraft', path: '/minecraft', icon: 'pickaxe' },
    { label: '我的', path: '/account', icon: 'user' },
  ];
  const bar = h('nav', 'tabbar');
  bar.id = 'tabbar';
  for (const it of items) {
    const on = it.path === active || (it.path !== '/' && active.startsWith(it.path));
    const a = h('a', 'tab' + (on ? ' on' : ''));
    if (on) a.setAttribute('aria-current', 'page');
    a.href = it.path;
    const ic = h('span', 'tab-ic');
    if (on) ic.appendChild(h('span', 'tab-pill'));
    ic.appendChild(icon(it.icon));
    a.appendChild(ic);
    a.appendChild(h('span', null, it.label));
    bar.appendChild(a);
  }
  document.body.appendChild(bar);
}

function closePops(except) {
  document.querySelectorAll('.pop.on').forEach((el) => { if (el !== except) hidePop(el); });
}

// popover 退出动画（打开 120ms / 关闭 90ms，别"啪"地消失）
function hidePop(el) {
  if (!el || !el.classList.contains('on') || el.classList.contains('closing')) return;
  el.classList.remove('on');
  el.parentElement.querySelector('button')?.setAttribute('aria-expanded', 'false');
  el.classList.add('closing');
  setTimeout(() => el.classList.remove('closing'), 130);
}

function togglePop(el) {
  if (el.classList.contains('on')) hidePop(el);
  else { el.classList.remove('closing'); el.classList.add('on'); el.parentElement.querySelector('button')?.setAttribute('aria-expanded', 'true'); }
}

// ---------- 命令面板（Ctrl/Cmd+K）----------
function openPalette() {
  const actions = [
    { icon: 'sparkle', label: '新建 Agent 对话', hint: 'Agent', run: () => { location.href = '/agent?new=1'; } },
    { el: () => gameIconEl('chess', 'palette-icon'), label: '打开国际象棋', hint: '游戏', run: () => { location.href = '/games/chess'; } },
    { el: () => gameIconEl('gomoku', 'palette-icon'), label: '打开五子棋', hint: '游戏', run: () => { location.href = '/games/gomoku'; } },
    { icon: 'megaphone', label: '查看最新动态', hint: '门户', run: () => { location.href = '/'; } },
    { icon: 'user', label: '我的账号', hint: '设置', run: () => { location.href = '/account'; } },
    { icon: 'clock', label: '对局历史与回放', hint: '设置', run: () => { location.href = '/account/history'; } },
    { icon: 'sun', label: '切换主题', hint: '外观', run: () => { setTheme(themeMode() === 'dark' ? 'light' : 'dark'); } },
  ];
  const box = h('div', 'palette');
  const input = h('input', 'palette-input');
  input.placeholder = '搜索或执行操作…';
  const list = h('div', 'palette-list');
  box.appendChild(input);
  box.appendChild(list);
  const mask = h('div', 'dialog-mask palette-mask');
  mask.appendChild(box);
  document.body.appendChild(mask);

  let filtered = actions.slice();
  let cursor = 0;
  let closed = false;
  const close = () => {
    if (closed) return;
    closed = true;
    mask.classList.add('closing');
    setTimeout(() => mask.remove(), 170);
  };
  mask.closePalette = close;
  const paint = () => {
    list.innerHTML = '';
    filtered.forEach((a, i) => {
      const row = h('button', 'palette-item' + (i === cursor ? ' on' : ''));
      row.appendChild(a.el ? a.el() : icon(a.icon));
      row.appendChild(h('span', 'palette-label', a.label));
      row.appendChild(h('span', 'palette-hint', a.hint || ''));
      row.onclick = () => { close(); a.run(); };
      list.appendChild(row);
    });
    if (!filtered.length) list.appendChild(h('div', 'palette-empty', '没有匹配的操作'));
  };
  const filter = () => {
    const q = input.value.trim().toLowerCase();
    filtered = actions.filter((a) => !q || a.label.toLowerCase().includes(q) || (a.hint || '').toLowerCase().includes(q));
    cursor = 0;
    paint();
  };
  input.addEventListener('input', filter);
  input.addEventListener('keydown', (e) => {
    if (e.key === 'ArrowDown') { cursor = Math.min(cursor + 1, filtered.length - 1); paint(); e.preventDefault(); }
    else if (e.key === 'ArrowUp') { cursor = Math.max(cursor - 1, 0); paint(); e.preventDefault(); }
    else if (e.key === 'Enter' && filtered[cursor]) { const a = filtered[cursor]; close(); a.run(); }
    else if (e.key === 'Escape') close();
  });
  mask.addEventListener('click', (e) => { if (e.target === mask) close(); });
  paint();
  input.focus();
}

if (typeof window !== 'undefined') {
  window.addEventListener('keydown', (e) => {
    if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
      e.preventDefault();
      const open = document.querySelector('.palette-mask:not(.closing)');
      if (open) open.closePalette();
      else openPalette();
    }
  });
}

// ---------- boot ----------
// active: 顶部导航高亮；requireLogin=false 时未登录也继续（?ui=1 演示模式）。
export async function boot({ active = '', requireLogin = true, preview = false } = {}) {
  applyTheme();
  // PWA：注册 Service Worker（仅"可安装"用，不做离线缓存）
  if ('serviceWorker' in navigator && location.protocol === 'https:') {
    navigator.serviceWorker.register('/sw.js').catch(() => {});
  }
  mql.addEventListener('change', () => { if (themeMode() === 'system') applyTheme(); });
  if (!document.body.classList.contains('agent-page')) document.body.classList.add('ds');

  if (preview) {
    // 预览：不请求后端，直接用假账号/假应用，但走同一套顶栏渲染
    shell.apps = PREVIEW_APPS;
    shell.user = PREVIEW_USER;   // 只放内存：预览不能写 localStorage（否则会顶掉真实账号的 lastName/admin）
    paintTop(active);
    return shell.user;
  }

  // 有登录态时先按上次的缓存把顶栏画完整（首帧就有导航与头像，跨页转场不"跳"），接口回来有变化再重画
  let painted = '';
  const snapshot = () => JSON.stringify({ apps: shell.apps,
    user: shell.user && { id: shell.user.id, name: shell.user.name, admin: !!shell.user.admin } });
  if (shell.token) {
    try {
      const c = JSON.parse(localStorage.getItem(SHELL_CACHE_KEY) || 'null');
      if (c && c.user) {
        shell.apps = c.apps || [];
        shell.user = c.user;
        paintTop(active);
        painted = snapshot();
      }
    } catch (e) { /* 缓存坏了就当没有 */ }
  }

  const loadApps = async () => {
    try { shell.apps = (await apiGet('/api/portal/apps')).apps || []; } catch (e) { /* 忽略 */ }
  };
  const loadMe = async () => {
    try {
      const me = await apiGet('/api/auth/me');
      saveAuth(me);
    } catch (e) {
      shell.user = null;
    }
  };
  await Promise.all([loadApps(), loadMe()]);
  if (shell.user) localStorage.setItem(SHELL_CACHE_KEY, snapshot());
  if (snapshot() !== painted) paintTop(active);

  if (!shell.user && requireLogin) {
    await loginView();
    await loadApps();
    paintTop(active);
  }
  return shell.user;
}
