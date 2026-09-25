// shell.js — 门户各页共用的顶栏 / 登录态 / 账户菜单 / 主题 / API 封装。
// 页面约定：<div id="shell-top"></div> + 自己的内容 + 调 shell.boot(active)。
// 未登录时渲染整页登录态（不是弹窗），登录后才进入门户。

import { h, icon, toast } from './ds.js';

const TOKEN_KEY = 'mineagent.token';
const NAME_KEY = 'mineagent.name';
const LAST_KEY = 'mineagent.lastName';

export const shell = {
  token: localStorage.getItem(TOKEN_KEY) || '',
  user: null,
  apps: [],
};

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
  applyTheme();
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
}

export async function logout() {
  try { await apiPost('/api/auth/logout'); } catch (e) { /* 忽略 */ }
  clearAuth();
  location.href = '/';
}

// ---------- 整页登录态 ----------
async function doLogin(name) {
  const res = await fetch('/api/auth/login', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name }),
  });
  const body = await res.json().catch(() => null);
  if (!res.ok) throw new Error((body && body.error) || ('HTTP ' + res.status));
  saveAuth({ id: body.id, name: body.name, admin: !!body.admin }, body.token);
  return shell.user;
}

function loginView() {
  return new Promise((resolve) => {
    const view = h('div', 'login-view');
    const box = h('div', 'login-box');
    box.appendChild(h('div', 'login-mark'));
    box.appendChild(h('h1', 'login-title', 'Mine'));

    const lastName = (localStorage.getItem(LAST_KEY) || '').trim();
    const err = h('div', 'field-err');

    const submit = async (name) => {
      err.textContent = '';
      if (!name) { err.textContent = '先填个名字'; return; }
      try {
        const user = await doLogin(name);
        view.remove();
        resolve(user);
      } catch (e) {
        err.textContent = e.message;
      }
    };

    const render = (showLast) => {
      box.innerHTML = '';
      box.appendChild(h('div', 'login-mark'));
      box.appendChild(h('h1', 'login-title', 'Mine'));
      if (showLast && lastName) {
        box.appendChild(h('p', 'login-sub', '欢迎回来'));
        const cont = h('button', 'btn primary cta login-go', '以 ' + lastName + ' 继续');
        cont.onclick = () => submit(lastName);
        box.appendChild(cont);
        const other = h('button', 'btn ghost sm login-other', '使用其他名字');
        other.onclick = () => render(false);
        box.appendChild(other);
      } else {
        const frag = paintForm('', submit);
        if (lastName) {
          const back = h('button', 'btn ghost sm login-other', '← 用上次的名字');
          back.onclick = () => render(true);
          frag.appendChild(back);
        }
        box.appendChild(frag);
      }
      box.appendChild(err);
      box.appendChild(h('p', 'login-hint', '暂时不需要密码 · 输入名字即可进入'));
      const input = box.querySelector('input');
      if (input) input.focus();
    };
    render(true);
    view.appendChild(box);
    document.body.appendChild(view);
  });
}

function paintForm(value, submit) {
  const frag = document.createDocumentFragment();
  frag.appendChild(h('p', 'login-sub', '欢迎来到我们的空间'));
  const field = h('div', 'field');
  const input = h('input', 'input login-input');
  input.id = 'login-name';
  input.maxLength = 24;
  input.placeholder = '你的名字';
  input.value = value || '';
  input.autocomplete = 'off';
  input.spellcheck = false;
  field.appendChild(input);
  frag.appendChild(field);
  const go = h('button', 'btn primary cta login-go', '继续');
  const run = () => submit(input.value.trim());
  go.onclick = run;
  input.addEventListener('keydown', (e) => { if (e.key === 'Enter') run(); });
  frag.appendChild(go);
  return frag;
}

// ---------- 顶栏 ----------
function paintTop(active) {
  const host = document.getElementById('shell-top');
  if (!host) return;
  host.innerHTML = '';
  const bar = h('header', 'topbar');

  const brand = h('a', 'brand');
  brand.href = '/';
  brand.appendChild(h('span', 'brand-dot'));
  brand.appendChild(h('span', null, 'Mine'));
  bar.appendChild(brand);

  const nav = h('nav', 'nav');
  const mk = (label, path) => {
    const a = h('a', 'nav-item' + (path === active ? ' on' : ''), label);
    a.href = path;
    nav.appendChild(a);
  };
  mk('首页', '/');
  for (const app of shell.apps) {
    if (app.nav && app.path) mk(app.navLabel || app.name, app.path);
  }
  bar.appendChild(nav);
  bar.appendChild(h('div', 'spacer'));

  // 主题选择器（明确列出三个选项，不再循环切换）
  const themeWrap = h('div', 'pop-wrap');
  const themeBtn = h('button', 'icon-btn');
  themeBtn.title = '外观';
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
  themeBtn.onclick = (e) => { e.stopPropagation(); closePops(themePop); themePop.classList.toggle('on'); };
  themePop.onclick = (e) => e.stopPropagation();
  themeWrap.appendChild(themeBtn);
  themeWrap.appendChild(themePop);
  bar.appendChild(themeWrap);

  // 账户菜单
  const accWrap = h('div', 'pop-wrap');
  const accBtn = h('button', 'icon-btn');
  accBtn.title = shell.user ? shell.user.name : '登录';
  if (shell.user) accBtn.appendChild(h('span', 'avatar', (shell.user.name[0] || '?')));
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
    item('我的账号', 'user', () => { location.href = '/account'; });
    item('退出登录', 'logout', () => logout(), 'danger-item');
  } else {
    const b = h('button', 'pop-item');
    b.appendChild(icon('user'));
    b.appendChild(h('span', null, '登录'));
    b.onclick = () => location.reload();
    accPop.appendChild(b);
  }
  accBtn.onclick = (e) => { e.stopPropagation(); closePops(accPop); accPop.classList.toggle('on'); };
  accPop.onclick = (e) => e.stopPropagation();
  accWrap.appendChild(accBtn);
  accWrap.appendChild(accPop);
  bar.appendChild(accWrap);

  host.appendChild(bar);
  document.addEventListener('click', () => closePops());
}

function closePops(except) {
  document.querySelectorAll('.pop.on').forEach((el) => { if (el !== except) el.classList.remove('on'); });
}

// ---------- boot ----------
// active: 顶部导航高亮；requireLogin=false 时未登录也继续（?ui=1 演示模式）。
export async function boot({ active = '', requireLogin = true } = {}) {
  applyTheme();
  mql.addEventListener('change', () => { if (themeMode() === 'system') applyTheme(); });
  document.body.classList.add('ds');

  const loadApps = async () => {
    try { shell.apps = (await apiGet('/api/portal/apps')).apps || []; } catch (e) { /* 忽略 */ }
  };
  await loadApps();

  try {
    const me = await apiGet('/api/auth/me');
    saveAuth(me);
  } catch (e) {
    shell.user = null;
  }
  paintTop(active);

  if (!shell.user && requireLogin) {
    await loginView();
    await loadApps();
    paintTop(active);
  }
  return shell.user;
}
