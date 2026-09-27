// sfx.js — 极轻量音效（Web Audio 合成，零素材、零依赖）。
// 默认开启；账号页可关（localStorage: mineagent.sfx = 'off'）。
// 浏览器要求用户手势后才能出声：首次 pointerdown 时 resume AudioContext。

let ctx = null;
let enabled = localStorage.getItem('mineagent.sfx') !== 'off';

export function sfxEnabled() { return enabled; }
export function setSfxEnabled(on) {
  enabled = !!on;
  localStorage.setItem('mineagent.sfx', enabled ? 'on' : 'off');
  if (enabled) play('yourTurn');
}

function ac() {
  if (!ctx) {
    const AC = window.AudioContext || window.webkitAudioContext;
    if (!AC) return null;
    ctx = new AC();
  }
  if (ctx.state === 'suspended') ctx.resume().catch(() => {});
  return ctx;
}

if (typeof window !== 'undefined') {
  const unlock = () => { ac(); window.removeEventListener('pointerdown', unlock); };
  window.addEventListener('pointerdown', unlock, { once: true });
}

// 每个音效 = 一两个短音（osc + 指数衰减），很轻，不吵
const TONES = {
  move:     [{ f: 520, t: 0.05, g: 0.06 }],
  capture:  [{ f: 300, t: 0.09, g: 0.08 }, { f: 200, t: 0.1, g: 0.05, d: 0.04 }],
  yourTurn: [{ f: 740, t: 0.09, g: 0.07 }, { f: 990, t: 0.12, g: 0.06, d: 0.09 }],
  check:    [{ f: 880, t: 0.08, g: 0.08 }, { f: 660, t: 0.1, g: 0.06, d: 0.08 }],
  win:      [{ f: 660, t: 0.12, g: 0.08 }, { f: 880, t: 0.14, g: 0.08, d: 0.12 }, { f: 1180, t: 0.2, g: 0.07, d: 0.28 }],
  lose:     [{ f: 420, t: 0.16, g: 0.07 }, { f: 300, t: 0.22, g: 0.06, d: 0.14 }],
};

export function play(name) {
  if (!enabled) return;
  if (window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches) return;
  const c = ac();
  if (!c) return;
  const t0 = c.currentTime;
  for (const tone of TONES[name] || []) {
    const osc = c.createOscillator();
    const gain = c.createGain();
    osc.type = 'sine';
    osc.frequency.value = tone.f;
    const start = t0 + (tone.d || 0);
    gain.gain.setValueAtTime(tone.g, start);
    gain.gain.exponentialRampToValueAtTime(0.0001, start + tone.t);
    osc.connect(gain).connect(c.destination);
    osc.start(start);
    osc.stop(start + tone.t + 0.02);
  }
}
