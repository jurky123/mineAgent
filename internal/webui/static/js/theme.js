// 主题：Light / Dark / System + 强调色

import { $ } from './ui.js';

const MODES = ['light', 'dark', 'system'];
const ACCENTS = [
  { id: 'default', label: '默认' },
  { id: '#2563eb', label: '蓝' },
  { id: '#7c3aed', label: '紫' },
  { id: '#059669', label: '绿' },
  { id: '#db2777', label: '粉' }
];
const mql = window.matchMedia('(prefers-color-scheme: dark)');

function mode() { return localStorage.getItem('mineagent.theme') || 'system'; }
function accent() { return localStorage.getItem('mineagent.accent') || ''; }

export function applyTheme() {
  const m = mode();
  const dark = m === 'dark' || (m === 'system' && mql.matches);
  document.documentElement.dataset.theme = dark ? 'dark' : 'light';
  document.documentElement.dataset.mode = m;
}

export function applyAccent() {
  const a = accent();
  if (a) {
    document.documentElement.style.setProperty('--accent', a);
    document.documentElement.style.setProperty('--accent-contrast', luminance(a) > 0.6 ? '#0d0d0d' : '#ffffff');
  } else {
    document.documentElement.style.removeProperty('--accent');
    document.documentElement.style.removeProperty('--accent-contrast');
  }
}

function luminance(hex) {
  const h = hex.replace('#', '');
  const r = parseInt(h.slice(0, 2), 16) / 255;
  const g = parseInt(h.slice(2, 4), 16) / 255;
  const b = parseInt(h.slice(4, 6), 16) / 255;
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

export function initTheme() {
  applyTheme();
  applyAccent();
  mql.addEventListener('change', () => { if (mode() === 'system') applyTheme(); });
  bindAppearance();
}

function bindAppearance() {
  const seg = $('theme-seg');
  const paint = () => {
    const idx = Math.max(0, MODES.indexOf(mode()));
    $('theme-thumb').style.transform = 'translateX(' + (idx * 100) + '%)';
    seg.querySelectorAll('.seg-item').forEach((b, i) => b.classList.toggle('on', i === idx));
    // 强调色选中态
    $('accent-swatches').querySelectorAll('.swatch').forEach((s) => {
      const a = accent();
      s.classList.toggle('on', (s.dataset.accent || '') === a);
    });
  };
  // 主题分段（可点可拖）
  let dragging = false;
  const frac = (x) => {
    const r = seg.getBoundingClientRect();
    const pad = 3, w = (r.width - pad * 2) / MODES.length;
    return Math.max(0, Math.min(MODES.length - 1, (x - r.left - pad) / w - 0.5));
  };
  const preview = (f, commit) => {
    $('theme-thumb').style.transform = 'translateX(' + (f * 100) + '%)';
    const near = Math.round(f);
    seg.querySelectorAll('.seg-item').forEach((b, i) => b.classList.toggle('on', i === near));
    if (commit) {
      localStorage.setItem('mineagent.theme', MODES[near]);
      applyTheme();
    }
  };
  seg.addEventListener('pointerdown', (e) => {
    dragging = true; seg.classList.add('dragging');
    try { seg.setPointerCapture(e.pointerId); } catch (err) {}
    preview(frac(e.clientX), false);
  });
  seg.addEventListener('pointermove', (e) => { if (dragging) preview(frac(e.clientX), false); });
  const stop = (e) => {
    if (!dragging) return;
    dragging = false; seg.classList.remove('dragging');
    preview(frac(e.clientX), true);
  };
  seg.addEventListener('pointerup', stop);
  seg.addEventListener('pointercancel', stop);
  // 强调色
  const box = $('accent-swatches');
  ACCENTS.forEach((a) => {
    const b = document.createElement('button');
    b.className = 'swatch' + (a.id === 'default' ? ' default' : '');
    b.title = a.label;
    b.dataset.accent = a.id === 'default' ? '' : a.id;
    if (a.id !== 'default') b.style.background = a.id;
    b.onclick = () => {
      if (a.id === 'default') localStorage.removeItem('mineagent.accent');
      else localStorage.setItem('mineagent.accent', a.id);
      applyAccent(); paint();
    };
    box.appendChild(b);
  });
  $('nav-theme').onclick = () => { $('apmodal').classList.add('on'); paint(); };
  $('ap-close').onclick = () => { $('apmodal').classList.remove('on'); };
  $('apmodal').addEventListener('click', (e) => { if (e.target === $('apmodal')) $('apmodal').classList.remove('on'); });
  paint();
}
