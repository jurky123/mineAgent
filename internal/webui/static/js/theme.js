// 主题：Light / Dark / System（Accent Color 按评审暂不做，交互色统一用蓝色 token）

import { $ } from './ui.js';

const MODES = ['light', 'dark', 'system'];
const mql = window.matchMedia('(prefers-color-scheme: dark)');

function mode() { return localStorage.getItem('mineagent.theme') || 'system'; }

export function applyTheme() {
  const m = mode();
  const dark = m === 'dark' || (m === 'system' && mql.matches);
  document.documentElement.dataset.theme = dark ? 'dark' : 'light';
  document.documentElement.dataset.mode = m;
}

export function initTheme() {
  applyTheme();
  mql.addEventListener('change', () => { if (mode() === 'system') applyTheme(); });
  bindAppearance();
}

export function openAppearance() {
  $('apmodal').classList.add('on');
  paint();
}

function bindAppearance() {
  const seg = $('theme-seg');
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
  $('ap-close').onclick = () => { $('apmodal').classList.remove('on'); };
  $('apmodal').addEventListener('click', (e) => { if (e.target === $('apmodal')) $('apmodal').classList.remove('on'); });
  paint();
}

function paint() {
  const idx = Math.max(0, MODES.indexOf(mode()));
  $('theme-thumb').style.transform = 'translateX(' + (idx * 100) + '%)';
  $('theme-seg').querySelectorAll('.seg-item').forEach((b, i) => b.classList.toggle('on', i === idx));
}
