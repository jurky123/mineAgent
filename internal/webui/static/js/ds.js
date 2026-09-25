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
  sun: 'M12 4V2m0 20v-2m8-8h2M2 12h2m13.66-5.66 1.42-1.42M4.92 19.08l1.42-1.42m0-11.32L4.92 4.92m14.16 14.16-1.42-1.42M16 12a4 4 0 1 1-8 0 4 4 0 0 1 8 0Z',
  moon: 'M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8Z',
  monitor: 'M3 5h18v11H3zM8 21h8m-4-5v5',
  user: 'M20 21a8 8 0 1 0-16 0m8-10a4 4 0 1 0 0-8 4 4 0 0 0 0 8Z',
  logout: 'M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4m7 14 5-5-5-5m5 5H9',
  check: 'm5 13 4 4L19 7',
  plus: 'M12 5v14M5 12h14',
  x: 'M18 6 6 18M6 6l12 12',
  right: 'm9 6 6 6-6 6',
  left: 'm15 6-6 6 6 6',
  copy: 'M8 8h11v11H8zM5 15H4V4h11v1',
  clock: 'M12 8v4l3 2m6-2a9 9 0 1 1-18 0 9 9 0 0 1 18 0Z',
  users: 'M17 21v-2a4 4 0 0 0-3-3.87M9 21v-2a4 4 0 0 1 3-3.87M13 7a4 4 0 1 1-8 0 4 4 0 0 1 8 0Zm8 3a3 3 0 1 1-6 0 3 3 0 0 1 6 0Z',
  shield: 'M12 3l7 3v6c0 4.5-3 7.5-7 9-4-1.5-7-4.5-7-9V6z',
  devices: 'M4 5h11v9H4zM2 19h15m4-9h2v10h-5V10h3z',
  info: 'M12 11v5m0-8h.01M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0Z',
  sparkle: 'M12 3l1.9 5.1L19 10l-5.1 1.9L12 17l-1.9-5.1L5 10l5.1-1.9zM19 15l.9 2.1L22 18l-2.1.9L19 21l-.9-2.1L16 18l2.1-.9z',
  pickaxe: 'M14 4l6 6M3 21l9-9m-4 4-5-5 3-3 5 5m2-7 4 4 3-3-4-4z',
  gamepad: 'M7 12h2m-1-1v2m6 0h.01M17 12h.01M6 7h12a4 4 0 0 1 4 4v2a4 4 0 0 1-7.3 2.3L14 14h-4l-.7 1.3A4 4 0 0 1 2 13v-2a4 4 0 0 1 4-4Z',
  megaphone: 'M3 11v2a1 1 0 0 0 1 1h2l9 5V5L6 10H4a1 1 0 0 0-1 1Zm15-3a6 6 0 0 1 0 8',
  chess: 'M9 21h6m-5 0v-4m4 4v-4M8 15h8l-1-3h-6zM10 12V9m4 3V9M8 9h8l-1-2H9zM11 7V4h2v3',
  settings: 'M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6Zm7.4-3a7.4 7.4 0 0 0-.1-1.2l2-1.6-2-3.4-2.4 1a7.5 7.5 0 0 0-2-1.2L14.5 3h-4l-.4 2.6a7.5 7.5 0 0 0-2 1.2l-2.4-1-2 3.4 2 1.6a7.4 7.4 0 0 0 0 2.4l-2 1.6 2 3.4 2.4-1a7.5 7.5 0 0 0 2 1.2l.4 2.6h4l.4-2.6a7.5 7.5 0 0 0 2-1.2l2.4 1 2-3.4-2-1.6c.06-.39.1-.79.1-1.2Z',
  home: 'M3 10.5 12 3l9 7.5M5.5 9.5V20h13V9.5',
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
  const p = document.createElementNS(ICON_NS, 'path');
  p.setAttribute('d', ICON_PATHS[name] || ICON_PATHS.info);
  svg.appendChild(p);
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
