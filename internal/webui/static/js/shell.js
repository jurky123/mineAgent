// shell.js：门户各页共用的顶栏 / 登录 / 账号菜单 / 主题。
// 页面结构：<div id="shell-top"></div> + 自己的内容 + 调 shell.boot(active)。

const TOKEN_KEY = 'mineagent.token';
const NAME_KEY = 'mineagent.name';

export const shell = {
  token: localStorage.getItem(TOKEN_KEY) || '',
  user: null,
  apps: [],
};

function h(tag, cls, text) {
  const el = document.createElement(tag);
  if (cls) el.className = cls;
  if (text != null) el.textContent = text;
  return el;
}

function svgIcon(name) {
  const paths = {
    sun: 'M12 4V2m0 20v-2m8-8h2M2 12h2m13.66-5.66 1.42-1.42M4.92 19.08l1.42-1.42m0-11.32L4.92 4.92m14.16 14.16-1.42-1.42M16 12a4 4 0 1 1-8 0 4 4 0 0 1 8 0Z',
    moon: 'M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8Z',
    user: 'M20 21a8 8 0 1 0-16 0m8-10a4 4 0 1 0 0-8 4 4 0 0 0 0 8Z',
    out: 'M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4m7 14 5-5-5-5m5 5H9',
    chat: 'M21 12a8 8 0 0 1-8 8H7l-4 3V12a8 8 0 0 1 8-8h2a8 8 0 0 1 8 8Z',
  };
  const NS = 'http://www.w3.org/2000/svg';
  const svg = document.createElementNS(NS, 'svg');
  svg.setAttribute('viewBox', '0 0 24 24');
  svg.setAttribute('class', 'i');
  const p = document.createElementNS(NS, 'path');
  p.setAttribute('d', paths[name] || paths.user);
  svg.appendChild(p);
  return svg;
}

// ---------- 主题（和聊天页共用 mineagent.theme 键） ----------
const MODES = ['light', 'dark', 'system'];
const mql = window.matchMedia('(prefers-color-scheme: dark)');

function themeMode() { return localStorage.getItem('mineagent.theme') || 'system'; }

export function applyTheme() {
  const m = themeMode();
  const dark = m === 'dark' || (m === 'system' && mql.matches);
  document.documentElement.dataset.theme = dark ? 'dark' : 'light';
  document.documentElement.dataset.mode = m;
}

function cycleTheme() {
  const i = MODES.indexOf(themeMode());
  localStorage.setItem('mineagent.theme', MODES[(i + 1) % MODES.length]);
  applyTheme();
}

// ---------- API ----------
export async function api(path, opts) {
  opts = opts || {};
  opts.headers = Object.assign({}, opts.headers,
    shell.token ? { Authorization: 'Bearer ' + shell.token } : {});
  const res = await fetch(path, opts);
  let body = null;
  try { body = await res.json(); } catch (e) { /* 空响应 */ }
  if (res.status === 401 && !opts.noAuthRedirect) {
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
    localStorage.setItem('mineagent.admin', user.admin ? '1' : '0');
  }
}

function clearAuth() {
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

// ---------- 登录弹层 ----------
let loginModal = null;

function showLogin() {
  return new Promise((resolve) => {
    if (!loginModal) {
      loginModal = h('div', 'login-mask');
      const box = h('div', 'login-box');
      box.appendChild(h('h2', null, '进入 Mine'));
      box.appendChild(h('p', 'login-hint', '输入一个名字（不需要密码，首次输入即注册）'));
      const input = h('input', 'login-input');
      input.id = 'login-name';
      input.type = 'text';
      input.maxLength = 24;
      input.placeholder = '你的名字';
      input.autocomplete = 'username';
      const err = h('div', 'login-err');
      const btn = h('button', 'btn primary login-btn', '进入');
      const submit = async () => {
        const name = input.value.trim();
        if (!name) { err.textContent = '先填个名字'; return; }
        btn.disabled = true; err.textContent = '';
        try {
          const res = await fetch('/api/auth/login', {
            method: 'POST', headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ name }),
          });
          const body = await res.json().catch(() => null);
          if (!res.ok) throw new Error((body && body.error) || ('HTTP ' + res.status));
          saveAuth({ id: body.id, name: body.name, admin: !!body.admin }, body.token);
          loginModal.remove(); loginModal = null;
          resolve(shell.user);
        } catch (e) {
          err.textContent = e.message;
        } finally {
          btn.disabled = false;
        }
      };
      btn.onclick = submit;
      input.addEventListener('keydown', (e) => { if (e.key === 'Enter') submit(); });
      box.appendChild(input); box.appendChild(err); box.appendChild(btn);
      loginModal.appendChild(box);
      document.body.appendChild(loginModal);
    }
    const first = loginModal.querySelector('input');
    if (first) first.focus();
  });
}

// ---------- 顶栏 ----------
function renderTop(active) {
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
  for (const app of shell.apps) {
    if (!app.enabled || !app.path) continue;
    const a = h('a', 'nav-item' + (app.path === active ? ' on' : ''), app.name);
    a.href = app.path;
    nav.appendChild(a);
  }
  bar.appendChild(nav);
  bar.appendChild(h('div', 'spacer'));

  const themeBtn = h('button', 'icon-btn');
  themeBtn.title = '切换主题';
  themeBtn.appendChild(svgIcon(themeMode() === 'dark' ? 'moon' : 'sun'));
  themeBtn.onclick = () => { cycleTheme(); renderTop(active); };
  bar.appendChild(themeBtn);

  const accBtn = h('button', 'icon-btn account-btn');
  accBtn.title = shell.user ? shell.user.name : '登录';
  if (shell.user) accBtn.appendChild(h('span', 'avatar', shell.user.name.slice(0, 1)));
  else accBtn.appendChild(svgIcon('user'));
  bar.appendChild(accBtn);

  const menu = h('div', 'account-menu');
  if (shell.user) {
    const head = h('div', 'account-head');
    head.appendChild(h('div', 'account-name', shell.user.name));
    head.appendChild(h('div', 'account-id', 'ID ' + shell.user.id + (shell.user.admin ? ' · 管理员' : '')));
    menu.appendChild(head);
    const mk = (label, icon, fn) => {
      const b = h('button', 'menu-item');
      b.appendChild(svgIcon(icon));
      b.appendChild(h('span', null, label));
      b.onclick = fn;
      menu.appendChild(b);
    };
    mk('我的账号', 'user', () => { location.href = '/account'; });
    mk('Agent 对话', 'chat', () => { location.href = '/agent'; });
    mk('退出登录', 'out', () => logout());
  } else {
    const b = h('button', 'menu-item');
    b.appendChild(svgIcon('user'));
    b.appendChild(h('span', null, '登录'));
    b.onclick = () => showLogin();
    menu.appendChild(b);
  }
  accBtn.onclick = (e) => { e.stopPropagation(); menu.classList.toggle('on'); };
  menu.onclick = (e) => e.stopPropagation();
  document.addEventListener('click', () => menu.classList.remove('on'));

  const holder = h('div', 'account-wrap');
  holder.appendChild(accBtn);
  holder.appendChild(menu);
  bar.appendChild(holder);
  host.appendChild(bar);
}

// ---------- boot ----------
// active: 当前导航高亮；requireLogin=false 时未登录也渲染（如 ?ui=1 演示）。
export async function boot({ active = '', requireLogin = true } = {}) {
  applyTheme();
  mql.addEventListener('change', () => { if (themeMode() === 'system') applyTheme(); });

  try { shell.apps = (await apiGet('/api/portal/apps')).apps || []; } catch (e) { /* 顶栏无导航 */ }

  try {
    const me = await apiGet('/api/auth/me');
    saveAuth(me);
  } catch (e) {
    shell.user = null;
  }
  renderTop(active);
  if (!shell.user && requireLogin) {
    const user = await showLogin();
    try { shell.apps = (await apiGet('/api/portal/apps')).apps || []; } catch (e) {}
    renderTop(active);
    return user;
  }
  if (shell.user) renderTop(active);
  return shell.user;
}

export { h, svgIcon, showLogin };
