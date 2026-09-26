// ds.js — 交互原语：元素构造 / 图标 / Dialog / ConfirmDialog / Toast / 复制。
// 门户类页面统一用它，禁止再用 alert/prompt/confirm。

// 门户类页面统一用 ds 的 body 样式（页面无需自己加 class）
document.body.classList.add('ds');

export function h(tag, cls, text) {
  const el = document.createElement(tag);
  if (cls) el.className = cls;
  if (text != null) el.textContent = text;
  return el;
}

// ---------- 图标（24x24 线性图标，避免依赖字体/emoji） ----------
const ICON_PATHS = {
  home: "<path d=\"M15 21v-8a1 1 0 0 0-1-1h-4a1 1 0 0 0-1 1v8\" /><path d=\"M3 10a2 2 0 0 1 .709-1.528l7-6a2 2 0 0 1 2.582 0l7 6A2 2 0 0 1 21 10v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z\" />",
  sparkle: "<path d=\"M11.017 2.814a1 1 0 0 1 1.966 0l1.051 5.558a2 2 0 0 0 1.594 1.594l5.558 1.051a1 1 0 0 1 0 1.966l-5.558 1.051a2 2 0 0 0-1.594 1.594l-1.051 5.558a1 1 0 0 1-1.966 0l-1.051-5.558a2 2 0 0 0-1.594-1.594l-5.558-1.051a1 1 0 0 1 0-1.966l5.558-1.051a2 2 0 0 0 1.594-1.594z\" /><path d=\"M20 2v4\" /><path d=\"M22 4h-4\" /><circle cx=\"4\" cy=\"20\" r=\"2\" />",
  gamepad: "<line x1=\"6\" x2=\"10\" y1=\"11\" y2=\"11\" /><line x1=\"8\" x2=\"8\" y1=\"9\" y2=\"13\" /><line x1=\"15\" x2=\"15.01\" y1=\"12\" y2=\"12\" /><line x1=\"18\" x2=\"18.01\" y1=\"10\" y2=\"10\" /><path d=\"M17.32 5H6.68a4 4 0 0 0-3.978 3.59c-.006.052-.01.101-.017.152C2.604 9.416 2 14.456 2 16a3 3 0 0 0 3 3c1 0 1.5-.5 2-1l1.414-1.414A2 2 0 0 1 9.828 16h4.344a2 2 0 0 1 1.414.586L17 18c.5.5 1 1 2 1a3 3 0 0 0 3-3c0-1.545-.604-6.584-.685-7.258-.007-.05-.011-.1-.017-.151A4 4 0 0 0 17.32 5z\" />",
  pickaxe: "<path d=\"m14 13-8.381 8.38a1 1 0 0 1-3.001-3L11 9.999\" /><path d=\"M15.973 4.027A13 13 0 0 0 5.902 2.373c-1.398.342-1.092 2.158.277 2.601a19.9 19.9 0 0 1 5.822 3.024\" /><path d=\"M16.001 11.999a19.9 19.9 0 0 1 3.024 5.824c.444 1.369 2.26 1.676 2.603.278A13 13 0 0 0 20 8.069\" /><path d=\"M18.352 3.352a1.205 1.205 0 0 0-1.704 0l-5.296 5.296a1.205 1.205 0 0 0 0 1.704l2.296 2.296a1.205 1.205 0 0 0 1.704 0l5.296-5.296a1.205 1.205 0 0 0 0-1.704z\" />",
  megaphone: "<path d=\"M11 6a13 13 0 0 0 8.4-2.8A1 1 0 0 1 21 4v12a1 1 0 0 1-1.6.8A13 13 0 0 0 11 14H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2z\" /><path d=\"M6 14a12 12 0 0 0 2.4 7.2 2 2 0 0 0 3.2-2.4A8 8 0 0 1 10 14\" /><path d=\"M8 6v8\" />",
  user: "<path d=\"M19 21v-2a4 4 0 0 0-4-4H9a4 4 0 0 0-4 4v2\" /><circle cx=\"12\" cy=\"7\" r=\"4\" />",
  monitor: "<rect width=\"20\" height=\"14\" x=\"2\" y=\"3\" rx=\"2\" /><line x1=\"8\" x2=\"16\" y1=\"21\" y2=\"21\" /><line x1=\"12\" x2=\"12\" y1=\"17\" y2=\"21\" />",
  sun: "<circle cx=\"12\" cy=\"12\" r=\"4\" /><path d=\"M12 2v2\" /><path d=\"M12 20v2\" /><path d=\"m4.93 4.93 1.41 1.41\" /><path d=\"m17.66 17.66 1.41 1.41\" /><path d=\"M2 12h2\" /><path d=\"M20 12h2\" /><path d=\"m6.34 17.66-1.41 1.41\" /><path d=\"m19.07 4.93-1.41 1.41\" />",
  moon: "<path d=\"M20.985 12.486a9 9 0 1 1-9.473-9.472c.405-.022.617.46.402.803a6 6 0 0 0 8.268 8.268c.344-.215.825-.004.803.401\" />",
  check: "<path d=\"M20 6 9 17l-5-5\" />",
  plus: "<path d=\"M5 12h14\" /><path d=\"M12 5v14\" />",
  x: "<path d=\"M18 6 6 18\" /><path d=\"m6 6 12 12\" />",
  right: "<path d=\"m9 18 6-6-6-6\" />",
  left: "<path d=\"m15 18-6-6 6-6\" />",
  copy: "<rect width=\"14\" height=\"14\" x=\"8\" y=\"8\" rx=\"2\" ry=\"2\" /><path d=\"M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2\" />",
  clock: "<path d=\"M12 6v6l4 2\" /><circle cx=\"12\" cy=\"12\" r=\"10\" />",
  users: "<path d=\"M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2\" /><path d=\"M16 3.128a4 4 0 0 1 0 7.744\" /><path d=\"M22 21v-2a4 4 0 0 0-3-3.87\" /><circle cx=\"9\" cy=\"7\" r=\"4\" />",
  shield: "<path d=\"M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1z\" /><path d=\"m9 12 2 2 4-4\" />",
  devices: "<path d=\"M18 8V6a2 2 0 0 0-2-2H4a2 2 0 0 0-2 2v7a2 2 0 0 0 2 2h8\" /><path d=\"M10 19v-3.96 3.15\" /><path d=\"M7 19h5\" /><rect width=\"6\" height=\"10\" x=\"16\" y=\"12\" rx=\"2\" />",
  info: "<circle cx=\"12\" cy=\"12\" r=\"10\" /><path d=\"M12 16v-4\" /><path d=\"M12 8h.01\" />",
  settings: "<path d=\"M9.671 4.136a2.34 2.34 0 0 1 4.659 0 2.34 2.34 0 0 0 3.319 1.915 2.34 2.34 0 0 1 2.33 4.033 2.34 2.34 0 0 0 0 3.831 2.34 2.34 0 0 1-2.33 4.033 2.34 2.34 0 0 0-3.319 1.915 2.34 2.34 0 0 1-4.659 0 2.34 2.34 0 0 0-3.32-1.915 2.34 2.34 0 0 1-2.33-4.033 2.34 2.34 0 0 0 0-3.831A2.34 2.34 0 0 1 6.35 6.051a2.34 2.34 0 0 0 3.319-1.915\" /><circle cx=\"12\" cy=\"12\" r=\"3\" />",
  logout: "<path d=\"m16 17 5-5-5-5\" /><path d=\"M21 12H9\" /><path d=\"M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4\" />",
  trash: "<path d=\"M10 11v6\" /><path d=\"M14 11v6\" /><path d=\"M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6\" /><path d=\"M3 6h18\" /><path d=\"M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2\" />",
  link: "<path d=\"M9 17H7A5 5 0 0 1 7 7h2\" /><path d=\"M15 7h2a5 5 0 1 1 0 10h-2\" /><line x1=\"8\" x2=\"16\" y1=\"12\" y2=\"12\" />",
  external: "<path d=\"M15 3h6v6\" /><path d=\"M10 14 21 3\" /><path d=\"M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6\" />",
  restart: "<path d=\"M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8\" /><path d=\"M3 3v5h5\" />",
  flag: "<path d=\"M4 22V4a1 1 0 0 1 .4-.8A6 6 0 0 1 8 2c3 0 5 2 7.333 2q2 0 3.067-.8A1 1 0 0 1 20 4v10a1 1 0 0 1-.4.8A6 6 0 0 1 16 16c-3 0-5-2-8-2a6 6 0 0 0-4 1.528\" />",
  crown: "<path d=\"M11.562 3.266a.5.5 0 0 1 .876 0L15.39 8.87a1 1 0 0 0 1.516.294L21.183 5.5a.5.5 0 0 1 .798.519l-2.834 10.246a1 1 0 0 1-.956.734H5.81a1 1 0 0 1-.957-.734L2.02 6.02a.5.5 0 0 1 .798-.519l4.276 3.664a1 1 0 0 0 1.516-.294z\" /><path d=\"M5 21h14\" />",
  server: "<rect width=\"20\" height=\"8\" x=\"2\" y=\"2\" rx=\"2\" ry=\"2\" /><rect width=\"20\" height=\"8\" x=\"2\" y=\"14\" rx=\"2\" ry=\"2\" /><line x1=\"6\" x2=\"6.01\" y1=\"6\" y2=\"6\" /><line x1=\"6\" x2=\"6.01\" y1=\"18\" y2=\"18\" />",
  pencil: "<path d=\"M21.174 6.812a1 1 0 0 0-3.986-3.987L3.842 16.174a2 2 0 0 0-.5.83l-1.321 4.352a.5.5 0 0 0 .623.622l4.353-1.32a2 2 0 0 0 .83-.497z\" /><path d=\"m15 5 4 4\" />",
  search: "<path d=\"m21 21-4.34-4.34\" /><circle cx=\"11\" cy=\"11\" r=\"8\" />",
  refresh: "<path d=\"M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8\" /><path d=\"M21 3v5h-5\" /><path d=\"M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16\" /><path d=\"M8 16H3v5\" />",
  login: "<path d=\"m10 17 5-5-5-5\" /><path d=\"M15 12H3\" /><path d=\"M15 3h4a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2h-4\" />",
  skull: "<path d=\"m12.5 17-.5-1-.5 1h1z\" /><path d=\"M15 22a1 1 0 0 0 1-1v-1a2 2 0 0 0 1.56-3.25 8 8 0 1 0-11.12 0A2 2 0 0 0 8 20v1a1 1 0 0 0 1 1z\" /><circle cx=\"15\" cy=\"12\" r=\"1\" /><circle cx=\"9\" cy=\"12\" r=\"1\" />",
};

const ICON_NS = 'http://www.w3.org/2000/svg';

export function icon(name, cls) {
  const svg = document.createElementNS(ICON_NS, 'svg');
  svg.setAttribute('viewBox', '0 0 24 24');
  svg.setAttribute('class', 'i ' + (cls || ''));
  svg.setAttribute('fill', 'none');
  svg.setAttribute('stroke', 'currentColor');
  svg.setAttribute('stroke-width', '1.8');
  svg.setAttribute('stroke-linecap', 'round');
  svg.setAttribute('stroke-linejoin', 'round');
  // Lucide 图标是 <path>/<circle>/<rect> 组合，直接注入静态 innerHTML（本文件内置，无外部输入）
  svg.innerHTML = ICON_PATHS[name] || ICON_PATHS.info;
  return svg;
}

// 图标样式（避免每个页面都写一遍 .i）
if (!document.getElementById('ds-icon-style')) {
  const st = document.createElement('style');
  st.id = 'ds-icon-style';
  st.textContent = '.i{width:16px;height:16px;flex:none;display:inline-block;vertical-align:-2px}' +
    '.i.lg{width:19px;height:19px}.i.sm{width:13px;height:13px}';
  document.head.appendChild(st);
}

// ---------- Toast ----------
let toastWrap = null;
let toastTimers = new WeakMap();

export function toast(msg, { warn = false, ms = 2600 } = {}) {
  if (!toastWrap) {
    toastWrap = h('div', 'toast-wrap');
    document.body.appendChild(toastWrap);
  }
  const el = h('div', 'toast' + (warn ? ' warn' : ''), msg);
  toastWrap.appendChild(el);
  clearTimeout(toastTimers.get(el));
  const timer = setTimeout(() => {
    el.style.opacity = '0';
    el.style.transition = 'opacity .2s';
    setTimeout(() => el.remove(), 220);
  }, ms);
  toastTimers.set(el, timer);
}

// ---------- Dialog ----------
// body: Node | string；actions: [{label, kind, primary, danger, onClick(close)}]
export function openDialog({ title, body, actions = [], width }) {
  const mask = h('div', 'dialog-mask');
  const box = h('div', 'dialog');
  if (width) box.style.width = 'min(94vw, ' + width + 'px)';
  const head = h('div', 'dialog-head');
  head.appendChild(h('h2', 'dialog-title', title || ''));
  const closeBtn = h('button', 'icon-btn sm');
  closeBtn.appendChild(icon('x'));
  closeBtn.onclick = () => close();
  head.appendChild(closeBtn);
  box.appendChild(head);
  const bodyEl = h('div', 'dialog-body');
  if (body instanceof Node) bodyEl.appendChild(body);
  else if (body) bodyEl.textContent = body;
  box.appendChild(bodyEl);
  const foot = h('div', 'dialog-actions');
  const close = () => mask.remove();
  for (const a of actions) {
    const btn = h('button', 'btn' + (a.primary ? ' primary cta' : '') + (a.danger ? ' danger' : ''), a.label);
    btn.disabled = !!a.disabled;
    btn.onclick = async () => {
      if (a.onClick) {
        const ret = await a.onClick(close);
        if (ret === false) return;
      }
      if (a.keepOpen !== true) close();
    };
    foot.appendChild(btn);
  }
  box.appendChild(foot);
  mask.appendChild(box);
  mask.addEventListener('click', (e) => { if (e.target === mask) close(); });
  document.addEventListener('keydown', function esc(e) {
    if (e.key === 'Escape') { close(); document.removeEventListener('keydown', esc); }
  });
  document.body.appendChild(mask);
  const firstInput = box.querySelector('input, textarea');
  if (firstInput) firstInput.focus();
  return { close, box, body: bodyEl };
}

export function confirmDialog({ title, body, confirmText = '确定', danger = false }) {
  return new Promise((resolve) => {
    let done = false;
    const finish = (v) => { if (!done) { done = true; resolve(v); } };
    const d = openDialog({
      title,
      body,
      actions: [
        { label: '取消', onClick: () => finish(false) },
        {
          label: confirmText, primary: !danger, danger,
          onClick: () => { finish(true); },
        },
      ],
    });
    // 点遮罩/× 关闭也算取消
    const obs = new MutationObserver(() => {
      if (!document.body.contains(d.box)) { finish(false); obs.disconnect(); }
    });
    obs.observe(document.body, { childList: true, subtree: true });
  });
}

// ---------- 棋子图 ----------
// base 形如 'N'（马）；在浅色背景上用黑子、深色背景上用白子，保证对比度。
export function pieceSrc(base) {
  const ver = (document.querySelector('meta[name=mineagent-version]') || {}).content || '';
  const dark = document.documentElement.dataset.theme === 'dark';
  return '/static/' + ver + '/img/pieces/' + (dark ? 'w' : 'b') + base + '.svg';
}

// imgAsset 站点自带的图片资源（带版本号）
export function imgAsset(name, cls) {
  const ver = (document.querySelector('meta[name=mineagent-version]') || {}).content || '';
  const img = h('img', cls || '');
  img.src = '/static/' + ver + '/img/' + name;
  img.alt = '';
  return img;
}

// 游戏图标（棋类用棋子图，其它用站点图）
export function gameIcon(gameId, cls) {
  if (gameId === 'chess') return pieceImg('N', cls);
  return imgAsset(gameId + '.svg', cls);
}

export function pieceImg(base, cls) {
  const img = h('img', cls || '');
  img.src = pieceSrc(base);
  img.alt = '';
  return img;
}

// ---------- 复制 ----------
export async function copyText(text, okMsg = '已复制') {
  try {
    await navigator.clipboard.writeText(text);
    toast(okMsg);
    return true;
  } catch (e) {
    // 非安全上下文（http 访问）没有 clipboard API：用临时输入框兜底
    const ta = h('textarea');
    ta.value = text;
    ta.style.position = 'fixed';
    ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.select();
    let ok = false;
    try { ok = document.execCommand('copy'); } catch (err) { ok = false; }
    ta.remove();
    toast(ok ? okMsg : text);
    return ok;
  }
}
