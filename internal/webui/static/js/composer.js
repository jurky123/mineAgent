// Composer：输入 / 发送 / 附件上传压缩 / 斜杠命令 / ＋菜单 / 模型与思考强度

import { $, esc, fmtSize, bus, ICON, showError, toast } from './ui.js';
import { S, setConv } from './state.js';
import { api, apiPost } from './api.js';
import { setTyping, syncAfter, dockAtBottom } from './chat.js';
import { refresh as refreshConversations } from './conversations.js';
import { openViewer } from './viewer.js';

// ---------- 输入 ----------
export function autoGrow() {
  const t = $('text');
  t.style.height = 'auto';
  t.style.height = Math.min(t.scrollHeight, 200) + 'px';
  updateSendBtn();
}

export function updateSendBtn() {
  const ready = ($('text').value.trim() || S.pending.some((p) => p.done)) && !S.pending.some((p) => !p.done && !p.err);
  $('send').classList.toggle('ready', !!ready);
  $('send').disabled = !ready;
}

// ---------- 发送 ----------
export async function sendMsg() {
  const text = $('text').value.trim();
  const files = S.pending.filter((p) => p.done).map((p) => p.file);
  if (!text && !files.length) return;
  if (S.pending.some((p) => !p.done && !p.err)) { showError('还有文件在上传，稍等'); return; }
  $('send').disabled = true;
  try {
    const payload = { text: text, files: files };
    if (S.conv !== null) payload.conv = S.conv; // null = 草稿，由服务端新建
    const wasDraft = S.conv === null;
    const res = await apiPost('/api/send', payload);
    if (wasDraft && res && res.conv) {
      // 第一条消息诞生了新会话：回填并补拉历史（SSE 那条消息可能早于本响应到达）
      setConv(res.conv);
      await refreshConversations();
      await syncAfter();
    }
    $('text').value = ''; autoGrow();
    S.pending.forEach((p) => p.url && URL.revokeObjectURL(p.url));
    S.pending = []; renderPending();
    setTyping(true, '正在思考…');
    setTimeout(() => { if (S.waiting) setTyping(false); }, 180000);
    if (!wasDraft) bus.emit('conversations-changed');
  } catch (e) {
    showError(e.message);
  } finally {
    updateSendBtn();
  }
}

// ---------- 待发附件 ----------
export function renderPending() {
  const box = $('pending');
  box.innerHTML = '';
  S.pending.forEach((p) => {
    const chip = document.createElement('div');
    chip.className = 'chip' + (p.done ? ' done' : '');
    if (p.isImage) {
      chip.innerHTML = '<img src="' + p.url + '" alt="">';
      chip.querySelector('img').onclick = () => {
        const imgs = S.pending.filter((q) => q.isImage && q.url);
        openViewer(imgs.map((q) => ({ url: q.url, name: q.name })), imgs.indexOf(p));
      };
    } else {
      chip.innerHTML = '<div class="doc">' + ICON.file + '<span class="n">' + esc(p.name) + '</span></div>';
    }
    if (!p.done) {
      const bar = document.createElement('div');
      bar.className = 'bar';
      bar.style.width = Math.round((p.progress || 0) * 100) + '%';
      chip.appendChild(bar);
    }
    if (p.note && p.done) {
      const note = document.createElement('div');
      note.className = 'note';
      note.textContent = p.note;
      chip.appendChild(note);
    }
    const x = document.createElement('button');
    x.className = 'x';
    x.innerHTML = ICON.x;
    x.onclick = () => {
      if (p.url) URL.revokeObjectURL(p.url);
      S.pending.splice(S.pending.indexOf(p), 1);
      renderPending(); updateSendBtn();
    };
    chip.appendChild(x);
    p.el = chip;
    box.appendChild(chip);
  });
  updateSendBtn();
}

async function compressImage(file) {
  const type = (file.type || '').toLowerCase();
  if (!/^image\/(png|jpe?g|webp|bmp|avif)$/.test(type)) return null;
  if (file.size < 400 * 1024) return null;
  let bitmap;
  try { bitmap = await createImageBitmap(file); } catch (e) { return null; }
  const maxDim = 1600;
  const scale = Math.min(1, maxDim / Math.max(bitmap.width, bitmap.height));
  const w = Math.max(1, Math.round(bitmap.width * scale));
  const h = Math.max(1, Math.round(bitmap.height * scale));
  const canvas = document.createElement('canvas');
  canvas.width = w; canvas.height = h;
  canvas.getContext('2d').drawImage(bitmap, 0, 0, w, h);
  if (bitmap.close) try { bitmap.close(); } catch (e) {}
  const blob = await new Promise((res) => { try { canvas.toBlob(res, 'image/jpeg', 0.85); } catch (e) { res(null); } });
  if (!blob || blob.size >= file.size) return null;
  const base = (file.name || 'image').replace(/\.[^.]+$/, '');
  return new File([blob], base + '.jpg', { type: 'image/jpeg' });
}

function uploadOne(file, onProgress) {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open('POST', '/api/upload?name=' + encodeURIComponent(file.name));
    xhr.setRequestHeader('Authorization', 'Bearer ' + S.token);
    xhr.upload.onprogress = (e) => { if (e.lengthComputable) onProgress(e.loaded / e.total); };
    xhr.onload = () => {
      let body = null;
      try { body = JSON.parse(xhr.responseText); } catch (e) {}
      if (xhr.status >= 200 && xhr.status < 300) resolve(body);
      else if (xhr.status === 401) reject(new Error('登录已失效'));
      else reject(new Error((body && body.error) || ('HTTP ' + xhr.status)));
    };
    xhr.onerror = () => reject(new Error('网络错误'));
    xhr.send(file);
  });
}

export async function addFiles(fileList) {
  const jobs = [];
  for (const f of Array.from(fileList)) {
    if (f.size > 20 * 1024 * 1024) { showError('「' + f.name + '」超过 20MB 上限'); continue; }
    jobs.push({ original: f, isImage: /^image\//.test(f.type || '') });
  }
  const prepared = await Promise.all(jobs.map(async (j) => {
    const smaller = await compressImage(j.original);
    return { file: smaller || j.original, isImage: j.isImage, saved: smaller ? j.original.size - smaller.size : 0 };
  }));
  for (const p of prepared) {
    const item = {
      file: p.file, name: p.file.name, isImage: p.isImage || /^image\//.test(p.file.type || ''),
      progress: 0, done: false, note: p.saved > 0 ? '已压缩 ' + fmtSize(p.saved) : '',
      url: p.isImage ? URL.createObjectURL(p.file) : ''
    };
    S.pending.push(item); renderPending();
    uploadOne(p.file, (v) => {
      item.progress = v;
      const b = item.el && item.el.querySelector('.bar');
      if (b) b.style.width = Math.round(v * 100) + '%';
    }).then((ref) => { item.file = ref; item.done = true; renderPending(); })
      .catch((e) => { item.err = true; showError('上传失败：' + e.message); S.pending.splice(S.pending.indexOf(item), 1); renderPending(); });
  }
  $('text').focus();
}

// ---------- 斜杠命令面板 ----------
const CMDS = [
  { c: '/help', d: '显示帮助' },
  { c: '/status', d: '服务 / 连接状态' },
  { c: '/memory', d: '记忆概况（clear 清空，summary 看摘要）' },
  { c: '/bind', d: '绑定 MC 身份', arg: '<MC名>' },
  { c: '/unbind', d: '解绑 MC 身份' },
  { c: '/myid', d: '看自己的身份' }
];
let cmdList = [], cmdIndex = 0;

function syncPalette() {
  const v = $('text').value;
  if (!v.startsWith('/') || v.includes(' ') || v.includes('\n') || v.length > 16) { $('cmdpalette').classList.remove('on'); cmdList = []; return; }
  cmdList = CMDS.filter((x) => x.c.startsWith(v.toLowerCase()) && x.c !== v);
  if (!cmdList.length) { $('cmdpalette').classList.remove('on'); cmdList = []; return; }
  cmdIndex = Math.min(cmdIndex, cmdList.length - 1);
  const box = $('cmdpalette');
  box.innerHTML = '';
  cmdList.forEach((x, i) => {
    const b = document.createElement('button');
    b.className = 'cmd-item' + (i === cmdIndex ? ' on' : '');
    b.innerHTML = '<span class="c">' + esc(x.c + (x.arg ? ' ' + x.arg : '')) + '</span><span class="d">' + esc(x.d) + '</span>';
    b.onmousedown = (e) => { e.preventDefault(); applyCmd(x); };
    box.appendChild(b);
  });
  closePopovers('cmdpalette');
  box.classList.add('on');
}

function applyCmd(x) {
  $('text').value = x.c + (x.arg ? ' ' : '');
  $('cmdpalette').classList.remove('on');
  autoGrow(); $('text').focus();
  $('text').setSelectionRange($('text').value.length, $('text').value.length);
}

// ---------- 弹层 ----------
export function closePopovers(except) {
  ['plusmenu', 'popover', 'cmdpalette'].forEach((id) => { if (id !== except) $(id).classList.remove('on'); });
}
document.addEventListener('click', (e) => {
  if (e.target.closest('.popover') || e.target.closest('.tool-btn')) return;
  closePopovers();
}, true);   // 捕获阶段：处理器重建 DOM 前就判断好，避免"点了却被自己关掉"

// ---------- Intelligence（思考强度 + 模型 合一） ----------
// UI 四档映射到后端 reasoning_effort：Instant='' / Medium='low' / High='medium' / Extra High='high'。
const INTEL = [
  { v: '', name: 'Instant' },
  { v: 'low', name: 'Medium' },
  { v: 'medium', name: 'High' },
  { v: 'high', name: 'Extra High' }
];
function intelIndex(effort) {
  const i = INTEL.findIndex((x) => x.v === (effort || ''));
  return i < 0 ? 0 : i;
}
let intelPaint = null;

export function renderToolbar() {
  if (!S.options) return;
  $('intellabel').textContent = INTEL[intelIndex(S.options.effort)].name;
}

function renderPlusMenu() {
  const box = $('plusmenu');
  const skills = (S.options && S.options.skills) || [];
  box.innerHTML =
    '<button class="po-item" id="pm-file"><svg class="i"><use href="#i-file"/></svg>上传文件</button>' +
    '<button class="po-item" id="pm-image"><svg class="i"><use href="#i-image"/></svg>上传图片</button>' +
    (skills.length
      ? '<div class="po-sep"></div><div class="po-title">技能</div>' +
        skills.map((s, i) => '<button class="po-item" data-skill="' + i + '"><svg class="i"><use href="#i-spark"/></svg><span>' + esc(s.name) +
          '</span><span class="po-desc" style="margin-left:auto">' + esc(s.desc || '') + '</span></button>').join('')
      : '');
  $('pm-file').onclick = () => { closePopovers(); $('file').click(); };
  $('pm-image').onclick = () => { closePopovers(); $('file').click(); };
  box.querySelectorAll('[data-skill]').forEach((b) => {
    b.onclick = () => {
      const s = skills[+b.dataset.skill];
      closePopovers();
      $('text').value = s.prompt || s.name;
      autoGrow(); $('text').focus();
      $('text').setSelectionRange($('text').value.length, $('text').value.length);
      updateSendBtn();
    };
  });
}

export function openModelPopover(back) {
  const box = $('popover');
  const cur = S.options ? S.options.model || '' : '';
  const bad = new Set((S.options && S.options.unavailable) || []);
  box.innerHTML = (back ? '<button class="po-item po-back" id="po-back"><svg class="i"><use href="#i-chevron"/></svg>返回</button>' : '') +
    '<div class="po-title">模型</div>' +
    ((cur && bad.has(cur)) ? '<div class="po-warn">⚠ 当前模型网关不可用，换一个吧</div>' : '') +
    '<input class="po-filter" id="po-filter" placeholder="筛选模型…"><div class="po-scroll" id="po-list"></div>' +
    (bad.size ? '<div class="po-desc" style="padding:6px 12px 2px">灰掉的模型网关当前不可用（503）</div>' : '');
  if (back) $('po-back').onclick = (e) => { e.stopPropagation(); openIntelPopover(); };
  const list = $('po-list');
  const mk = (value, label, hint) => {
    const off = value !== '' && bad.has(value);
    const b = document.createElement('button');
    b.className = 'po-item' + (cur === value ? ' on' : '') + (off ? ' off' : '');
    b.innerHTML = '<span>' + esc(label) + (hint ? ' <span class="po-desc">' + esc(hint) + '</span>' : '') +
      (off ? ' <span class="po-desc">不可用</span>' : '') + '</span><span class="check">' + ICON.check + '</span>';
    if (off) { b.disabled = true; } else { b.onclick = (e) => { e.stopPropagation(); setPrefs({ model: value }); }; }
    list.appendChild(b);
  };
  const build = (f) => {
    list.innerHTML = '';
    const q = (f || '').trim().toLowerCase();
    if (!q) mk('', '默认 · ' + ((S.options && S.options.defaultModel) || ''), '');
    ((S.options && S.options.models) || []).forEach((m) => {
      if (q && m.toLowerCase().indexOf(q) < 0) return;
      mk(m, m, m === (S.options && S.options.defaultModel) ? '默认' : '');
    });
    if (!list.children.length) list.innerHTML = '<div class="po-desc" style="padding:8px 10px">没有匹配的模型</div>';
  };
  build('');
  $('po-filter').oninput = () => build($('po-filter').value);
  closePopovers('popover');
  box.classList.add('on');
}

export function openIntelPopover() {
  const box = $('popover');
  const idx = intelIndex(S.options && S.options.effort);
  const modelName = (S.options && (S.options.model || S.options.defaultModel)) || '';
  const modelBad = !!(S.options && S.options.model && (S.options.unavailable || []).indexOf(S.options.model) >= 0);
  box.innerHTML = '<div class="intel-head">' + INTEL[idx].name + '<svg class="i"><use href="#i-chevron"/></svg></div>' +
    '<div class="reason init" id="reason">' +
    '<div class="reason-track" id="reason-track">' +
    '<div class="reason-rail"><div class="reason-fill" id="reason-fill">' +
    '<div class="fill-aurora"></div></div>' +
    '<div class="reason-sparkles" id="reason-sparkles"></div></div>' +
    '<div class="reason-dots" id="reason-dots"></div>' +
    '<div class="reason-thumb" id="reason-thumb"></div></div>' +
    '<div class="reason-labels" id="reason-labels">' + INTEL.map((x, i) =>
      '<span class="' + (i === idx ? 'on' : '') + '" data-i="' + i + '">' + x.name + '</span>').join('') + '</div>' +
    '</div>' +
    '<div class="intel-sep"></div>' +
    '<button class="intel-model" id="po-model"><span>Model</span><span class="val"' + (modelBad ? ' style="color:var(--danger)"' : '') + '>' +
    esc(modelName) + (modelBad ? '（不可用）' : '') + '</span><svg class="i sm"><use href="#i-chevron"/></svg></button>';
  const track = $('reason-track');
  closePopovers('popover');
  box.classList.add('on');   // 先可见，量宽度才算得准
  const PAD = 12, N = INTEL.length;
  let rect = null;
  const trackRect = () => rect || (rect = track.getBoundingClientRect());
  const xAt = (f) => PAD + (trackRect().width - PAD * 2) * (f / (N - 1));   // f 允许小数
  const fracFromX = (clientX) => {
    const r = trackRect();
    const rel = Math.min(Math.max(clientX - r.left, PAD), r.width - PAD);
    return Math.max(0, Math.min(N - 1, (rel - PAD) / ((r.width - PAD * 2) / (N - 1))));
  };

  // paint 接受小数档位：拖动时跟手连续移动，点击/松手传整数吸附
  const paint = (f) => {
    const x = xAt(f);
    $('reason-thumb').style.transform = 'translate3d(' + x + 'px, -50%, 0)';
    $('reason-fill').style.width = Math.max(0, x - PAD) + 'px';
    const near = Math.round(f);
    $('reason-labels').querySelectorAll('span').forEach((n, k) => n.classList.toggle('on', k === near));
    const head = document.querySelector('.intel-head');
    if (head) head.firstChild.textContent = INTEL[near].name;
    // 刻度点：只建一次，之后只更新位置/选中态（拖动不再重建 DOM）
    const dots = $('reason-dots');
    if (dots) {
      if (dots.children.length !== N) {
        dots.innerHTML = INTEL.map(() => '<div class="reason-dot"></div>').join('');
      }
      Array.prototype.forEach.call(dots.children, (d, k) => {
        const dx = xAt(k);
        d.style.left = dx + 'px';
        d.classList.toggle('on', dx <= x + 0.5);
      });
    }
    $('reason').classList.toggle('max', near === N - 1);
    intelPaint = (i) => paint(i);   // 供 setPrefs 原地重绘
  };

  // 立即播放吸附动画（乐观更新），网络请求在后台跑，失败回滚
  function applyEffort(i) {
    const v = INTEL[i].v;
    const cur = (S.options && S.options.effort) || '';
    paint(i);
    if (v === cur) return;
    const prev = intelIndex(cur);
    setPrefs({ effort: v }).then((ok) => { if (!ok) paint(prev); });
  }

  renderSparkles();   // 常驻渲染，靠 .max 透明度淡入
  // 白色粒子：确定性伪随机，尺寸/位置/漂移/时长各不同
  function renderSparkles() {
    const box = $('reason-sparkles');
    if (!box || box.children.length) return;
    let seed = 7;
    const rnd = () => { seed = (seed * 1103515245 + 12345) & 0x7fffffff; return seed / 0x7fffffff; };
    let html = '';
    for (let k = 0; k < 18; k++) {
      const size = 1.5 + rnd() * 3.5;
      html += '<span class="spark" style="' +
        'left:' + (4 + rnd() * 92).toFixed(1) + '%;' +
        'top:' + (18 + rnd() * 64).toFixed(1) + '%;' +
        'width:' + size.toFixed(1) + 'px;height:' + size.toFixed(1) + 'px;' +
        '--dx:' + ((rnd() - 0.5) * 14).toFixed(1) + 'px;' +
        '--dy:' + ((rnd() - 0.5) * 10).toFixed(1) + 'px;' +
        '--dur:' + (1.8 + rnd() * 1.8).toFixed(2) + 's;' +
        '--delay:' + (-rnd() * 2).toFixed(2) + 's;' +
        'opacity:' + (0.4 + rnd() * 0.5).toFixed(2) + ';"></span>';
    }
    box.innerHTML = html;
  }

  paint(idx);
  requestAnimationFrame(() => $('reason').classList.remove('init'));

  let dragging = false, rafPending = false, lastX = 0;
  track.addEventListener('pointerdown', (e) => {
    dragging = true;
    rect = track.getBoundingClientRect();   // 缓存几何，拖动期间避免反复 layout
    $('reason').classList.add('dragging');
    try { track.setPointerCapture(e.pointerId); } catch (err) {}
    paint(fracFromX(e.clientX));
  });
  track.addEventListener('pointermove', (e) => {
    if (!dragging) return;
    lastX = e.clientX;
    if (rafPending) return;
    rafPending = true;
    requestAnimationFrame(() => {
      rafPending = false;
      if (dragging) paint(fracFromX(lastX));   // 跟手（小数位置）
    });
  });
  const stop = (e) => {
    if (!dragging) return;
    dragging = false;
    $('reason').classList.remove('dragging');
    applyEffort(Math.round(fracFromX(e.clientX)));   // 立刻吸附，不等网络
  };
  track.addEventListener('pointerup', stop);
  track.addEventListener('pointercancel', stop);
  $('reason-labels').querySelectorAll('span').forEach((n) => {
    n.onclick = () => applyEffort(+n.dataset.i);
  });
  $('po-model').onclick = (e) => { e.stopPropagation(); openModelPopover(true); };
}

async function setPrefs(patch) {
  try {
    const r = await apiPost('/api/prefs', patch);
    S.options.model = r.model; S.options.effort = r.effort;
    renderToolbar();
    if ('model' in patch) {
      if ($('popover').classList.contains('on')) openModelPopover(true);
      toast('模型：' + (r.model || '默认'), 'ok');
    } else if (intelPaint && $('reason-track')) {
      intelPaint(intelIndex(r.effort));   // 原地重绘：平滑吸附，不重建 DOM
    }
    return true;
  } catch (e) { showError(e.message); return false; }
}

export async function loadOptions(force) {
  if (S.options && !force) { renderToolbar(); return S.options; }
  S.options = await api('/api/options');
  renderToolbar();
  return S.options;
}

// ---------- 事件绑定 ----------
const isMobile = () => window.matchMedia('(max-width: 899px)').matches;

// 移动端：点输入框就把 composer 停到底部（键盘弹出时被浏览器自动上推即可，
// 不再自己改高度——iOS 下改高度反而会把整个应用推出屏幕）。
$('text').addEventListener('focus', () => {
  if (isMobile()) dockAtBottom();
});

$('text').addEventListener('input', () => { autoGrow(); syncPalette(); });
$('text').addEventListener('keydown', (e) => {
  if ($('cmdpalette').classList.contains('on') && cmdList.length) {
    if (e.key === 'ArrowDown') { e.preventDefault(); cmdIndex = (cmdIndex + 1) % cmdList.length; syncPalette(); return; }
    if (e.key === 'ArrowUp') { e.preventDefault(); cmdIndex = (cmdIndex - 1 + cmdList.length) % cmdList.length; syncPalette(); return; }
    if (e.key === 'Tab' || (e.key === 'Enter' && !e.isComposing)) { e.preventDefault(); applyCmd(cmdList[cmdIndex]); return; }
    if (e.key === 'Escape') { e.preventDefault(); $('cmdpalette').classList.remove('on'); return; }
  }
  if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) { e.preventDefault(); sendMsg(); }
});
$('send').onclick = sendMsg;
$('attach').onclick = (e) => {
  e.stopPropagation();
  const box = $('plusmenu');
  if (box.classList.contains('on')) { box.classList.remove('on'); return; }
  renderPlusMenu(); closePopovers('plusmenu'); box.classList.add('on');
};
$('intelbtn').onclick = (e) => {
  e.stopPropagation();
  const box = $('popover');
  if (box.classList.contains('on')) { box.classList.remove('on'); return; }  // 再点一次关闭
  openIntelPopover();
};
$('file').onchange = () => { addFiles($('file').files); $('file').value = ''; };
document.querySelectorAll('.suggests button').forEach((b) => {
  b.onclick = () => { $('text').value = b.dataset.q; autoGrow(); $('text').focus(); };
});
document.addEventListener('paste', (e) => {
  if (!S.token) return;
  const files = e.clipboardData && e.clipboardData.files;
  if (files && files.length) { e.preventDefault(); addFiles(files); }
});
let dragDepth = 0;
window.addEventListener('dragenter', (e) => {
  e.preventDefault();
  if (!S.token) return;
  if (++dragDepth === 1) $('drop').classList.add('on');
});
window.addEventListener('dragover', (e) => e.preventDefault());
window.addEventListener('dragleave', () => { if (--dragDepth <= 0) { dragDepth = 0; $('drop').classList.remove('on'); } });
window.addEventListener('drop', (e) => {
  e.preventDefault(); dragDepth = 0; $('drop').classList.remove('on');
  if (e.dataTransfer && e.dataTransfer.files.length) addFiles(e.dataTransfer.files);
});
