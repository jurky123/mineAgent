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
  const ready = !S.sending && !S.waiting && ($('text').value.trim() || S.pending.some((p) => p.done)) && !S.pending.some((p) => !p.done && !p.err);
  $('send').classList.toggle('ready', !!ready);
  $('send').disabled = !ready;
  $('text').placeholder = S.waiting ? '回答结束后可发送，草稿会保留' : '给 MineAgent 发消息';
}

// ---------- 发送 ----------
export async function sendMsg() {
  if (S.sending || S.waiting) { toast('回答结束后可发送，当前草稿会保留'); return; }
  const text = $('text').value.trim();
  const files = S.pending.filter((p) => p.done).map((p) => p.file);
  if (!text && !files.length) return;
  if (S.pending.some((p) => !p.done && !p.err)) { showError('还有文件在上传，稍等'); return; }
  const originConv = S.conv;
  const sentPending = S.pending;
  S.sending = true;
  $('text').value = ''; S.pending = []; renderPending(); autoGrow();
  $('send').disabled = true;
  try {
    const payload = { text: text, files: files };
    if (originConv !== null) payload.conv = originConv; // null = 草稿，由服务端新建
    const wasDraft = originConv === null;
    const res = await apiPost('/api/send', payload);
    if (wasDraft && S.conv === originConv && res && res.conv) {
      // 第一条消息诞生了新会话：回填并补拉历史（SSE 那条消息可能早于本响应到达）
      setConv(res.conv);
      await refreshConversations();
      await syncAfter();
    }
    sentPending.forEach((p) => p.url && URL.revokeObjectURL(p.url));
    if (res?.running && S.conv === res.conv) setTyping(true, res.running.text, res.running.startedAt);
    await syncAfter();
    if (!wasDraft) bus.emit('conversations-changed');
  } catch (e) {
    // An uncertain HTTP result is never automatically retried. Preserve the snapshot separately.
    const recovery = document.createElement('div'); recovery.className = 'send-recovery'; recovery.setAttribute('role', 'alert');
    recovery.append(document.createTextNode('发送未确认：' + e.message + '。请先查看聊天记录，避免重复执行操作。'));
    const restore = document.createElement('button'); restore.className = 'btn'; restore.textContent = '恢复发送内容到草稿';
    restore.onclick = () => {
      $('text').value = text + ($('text').value ? '\n\n' + $('text').value : '');
      S.pending = sentPending.concat(S.pending); renderPending(); autoGrow(); recovery.remove(); $('text').focus();
    }; recovery.appendChild(restore); $('composer').prepend(recovery);
  } finally {
    S.sending = false; updateSendBtn();
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
  placePopover(box);
  box.classList.add('on');
}

function applyCmd(x) {
  $('text').value = x.c + (x.arg ? ' ' : '');
  $('cmdpalette').classList.remove('on');
  autoGrow(); $('text').focus();
  $('text').setSelectionRange($('text').value.length, $('text').value.length);
}

// ---------- 弹层 ----------
// placePopover 空会话（composer 居中）时向下展开，其余情况向上（默认）。
export function placePopover(box) {
  const centered = $('composer').parentElement === $('emptySlot');
  const anchor = box.offsetParent || $('composer');
  const rect = anchor.getBoundingClientRect();
  const above = rect.top - 18;
  const below = window.innerHeight - rect.bottom - 18;
  const useBelow = centered && below >= above;
  box.classList.toggle('below', useBelow);
  if (box.id === 'popover') box.style.maxHeight = Math.max(160, Math.min(560, useBelow ? below : above)) + 'px';
}

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

// modelLabel 给 Intelligence 面板里的 Model 行显示（默认提供商显示原名，额外提供商显示 label）
function modelLabel() {
  const id = (S.options && S.options.model) || '';
  if (!id) return (S.options && S.options.defaultModel) || '';
  const hit = ((S.options && S.options.models) || []).find((m) => m.id === id);
  return hit ? hit.label : id;
}

function renderPlusMenu() {
  const box = $('plusmenu');
  const skills = (S.options && S.options.skills) || [];
  box.innerHTML =
    '<button class="po-item" id="pm-file"><svg class="i"><use href="#i-file"/></svg>上传文件</button>' +
    '<button class="po-item" id="pm-image"><svg class="i"><use href="#i-image"/></svg>上传图片</button>' +
    (skills.length
      ? '<div class="po-sep"></div><div class="po-title">技能</div>' +
        skills.map((s, i) => '<button class="po-item" data-skill="' + i + '" title="' + esc(s.reason || '') + '"><svg class="i"><use href="#i-spark"/></svg><span>' + esc(s.name) +
          '</span><span class="po-desc" style="margin-left:auto">' + esc((s.status === 'admin' || s.status === 'unavailable') ? s.reason : (s.status === 'approval' ? '需审批 · ' : '') + (s.desc || '')) + '</span></button>').join('')
      : '');
  $('pm-file').onclick = () => { closePopovers(); $('file').click(); };
  $('pm-image').onclick = () => { closePopovers(); $('file').click(); };
  const capabilities = S.options?.capabilities || [];
  if (capabilities.length) { const details = document.createElement('details'); details.className = 'capability-list'; const summary = document.createElement('summary'); summary.textContent = '当前工具与权限'; details.appendChild(summary);
    for (const entry of capabilities) { const row = document.createElement('p'); const labels = { available: '可用', approval: '需审批', admin: '管理员专属', unavailable: '当前不可用' }; row.textContent = entry.name + ' · ' + entry.kind + ' · ' + (labels[entry.status] || '当前不可用'); details.appendChild(row); } box.appendChild(details); }
  box.querySelectorAll('[data-skill]').forEach((b) => {
    b.onclick = () => {
      const s = skills[+b.dataset.skill];
      if (s.status === 'admin' || s.status === 'unavailable') { toast(s.reason || '当前不可用'); return; }
      closePopovers();
      $('text').value = s.prompt || s.name;
      autoGrow(); $('text').focus();
      $('text').setSelectionRange($('text').value.length, $('text').value.length);
      updateSendBtn();
    };
  });
}

let modelFreeOnly = localStorage.getItem('mineagent.freeonly') === '1';
let modelShowUnavailable = false;
let modelSort = 'newest';

export function openModelPopover(back, query = '') {
  const box = $('popover');
  const cur = S.options ? S.options.model || '' : '';
  const defaultModel = (S.options && S.options.defaultModel) || '';
  const all = (S.options && S.options.models) || [];
  box.innerHTML = (back ? '<button class="po-item po-back" id="po-back"><svg class="i"><use href="#i-chevron"/></svg>返回</button>' : '') +
    '<div class="po-modelhead"><span class="po-title">选择模型</span><button class="po-refresh" id="po-refresh">刷新列表</button></div>' +
    '<div class="po-toolrow"><input class="po-filter" id="po-filter" aria-label="搜索模型" placeholder="搜索名称或模型 ID…"></div>' +
    '<div class="po-toolrow po-modelcontrols">' +
    '<button class="po-chip' + (modelFreeOnly ? ' on' : '') + '" id="po-freeonly" aria-pressed="' + modelFreeOnly + '">只看免费</button>' +
    '<button class="po-chip' + (modelShowUnavailable ? ' on' : '') + '" id="po-showall" aria-pressed="' + modelShowUnavailable + '">显示不可用</button>' +
    '<select class="po-sort" id="po-sort" aria-label="模型排序"><option value="newest">最新优先</option><option value="free">免费优先</option><option value="name">名称排序</option></select></div>' +
    '<div class="po-modelstatus" id="po-modelstatus" aria-live="polite"></div>' +
    '<div class="po-scroll" id="po-list"></div>';
  if (back) $('po-back').onclick = (e) => { e.stopPropagation(); openIntelPopover(); };
  const list = $('po-list');
  const usable = (m) => !m.needsKey && !m.unavailable;
  const badge = (m) => {
    let out = m.free ? '<span class="po-badge free">免费</span>' : '';
    if (m.needsKey) out += '<span class="po-badge dim">未配置密钥</span>';
    else if (m.unavailable) out += '<span class="po-badge dim">不可用</span>';
    else if (m.verified) out += '<span class="po-badge dim">已验证</span>';
    return out;
  };
  const mk = (m) => {
    const b = document.createElement('button');
    b.className = 'po-item model' + ((cur || defaultModel) === m.id ? ' on' : '') + (!usable(m) ? ' off' : '');
    b.disabled = !usable(m);
    const released = new Date((m.created || 0) * 1000);
    const date = m.created && Number.isFinite(released.getTime()) ? released.toISOString().slice(0, 10) : '';
    const detail = [m.id, date ? '发布 ' + date : '', m.contextLength ? Math.round(m.contextLength / 1024) + 'K 上下文' : '',
      m.unavailableReason || '', m.stale ? '缓存目录' : ''].filter(Boolean).join(' · ');
    b.innerHTML = '<span class="po-modelbody"><span class="po-modelname">' + esc(m.label) +
      (m.provider === 'default' && m.id === defaultModel ? ' <span class="po-desc">默认</span>' : '') + badge(m) +
      '</span><span class="po-modeldetail">' + esc(detail) + '</span></span><span class="check">' + ICON.check + '</span>';
    b.title = detail;
    b.onclick = (e) => { e.stopPropagation(); setPrefs({ model: m.id }); };
    list.appendChild(b);
  };
  const build = (f) => {
    list.innerHTML = '';
    const q = (f || '').trim().toLowerCase();
    const matches = all.filter((m) => (!q || m.label.toLowerCase().includes(q) || m.id.toLowerCase().includes(q)) &&
      (!modelFreeOnly || m.free) && (modelShowUnavailable || usable(m)));
    matches.sort((a, b) => {
      if (usable(a) !== usable(b)) return usable(a) ? -1 : 1;
      if (modelSort === 'free' && a.free !== b.free) return a.free ? -1 : 1;
      if (modelSort !== 'name' && (a.created || 0) !== (b.created || 0)) return (b.created || 0) - (a.created || 0);
      return a.label.localeCompare(b.label, undefined, { numeric: true, sensitivity: 'base' });
    });
    const freeCount = all.filter((m) => usable(m) && m.free).length;
    const unknownDates = matches.some((m) => !m.created);
    $('po-modelstatus').textContent = matches.length + ' 个模型 · ' + freeCount + ' 个免费可选' +
      (modelSort === 'newest' && unknownDates ? ' · 无发布日期的按名称排序' : '') +
      (all.some((m) => m.stale) ? ' · 刷新失败，显示缓存' : '') + ' · 实际可用性与额度以提供商为准';
    const def = all.find((m) => m.provider === 'default' && m.id === defaultModel);
    if (!q && !modelFreeOnly && (!def || usable(def) || modelShowUnavailable)) {
      const b = document.createElement('button');
      b.className = 'po-item model' + (cur === '' ? ' on' : '');
      b.innerHTML = '<span class="po-modelname">跟随默认 · ' + esc(defaultModel) + (def ? badge(def) : '') + '</span><span class="check">' + ICON.check + '</span>';
      b.disabled = !!def && !usable(def);
      b.onclick = (e) => { e.stopPropagation(); setPrefs({ model: '' }); };
      list.appendChild(b);
    }
    // Group by provider after sorting; each provider appears once.
    const groups = new Map();
    for (const m of matches) {
      if (!groups.has(m.provider)) groups.set(m.provider, []);
      groups.get(m.provider).push(m);
    }
    for (const models of groups.values()) {
      const t = document.createElement('div');
      t.className = 'po-title po-provider';
      t.textContent = models[0].providerLabel || models[0].provider;
      list.appendChild(t);
      models.forEach(mk);
    }
    if (!matches.length) {
      const empty = document.createElement('div'); empty.className = 'po-modelempty';
      empty.textContent = modelFreeOnly ? '暂无匹配的免费模型。免费额度与可用性以提供商为准。' : '暂无匹配的可选模型。可刷新列表或显示不可用模型。';
      list.appendChild(empty);
    }
  };
  $('po-sort').value = modelSort;
  $('po-filter').value = query;
  build(query);
  $('po-filter').oninput = () => build($('po-filter').value);
  $('po-sort').onchange = () => { modelSort = $('po-sort').value; build($('po-filter').value); };
  $('po-freeonly').onclick = (e) => {
    e.stopPropagation(); modelFreeOnly = !modelFreeOnly;
    localStorage.setItem('mineagent.freeonly', modelFreeOnly ? '1' : '0');
    $('po-freeonly').classList.toggle('on', modelFreeOnly);
    $('po-freeonly').setAttribute('aria-pressed', String(modelFreeOnly));
    build($('po-filter').value);
  };
  $('po-showall').onclick = (e) => {
    e.stopPropagation(); modelShowUnavailable = !modelShowUnavailable;
    $('po-showall').classList.toggle('on', modelShowUnavailable);
    $('po-showall').setAttribute('aria-pressed', String(modelShowUnavailable));
    build($('po-filter').value);
  };
  $('po-refresh').onclick = async (e) => {
    e.stopPropagation();
    const button = e.currentTarget;
    button.disabled = true; button.textContent = '刷新中…';
    try {
      await loadOptions(true);
      if ($('po-refresh') === button && box.classList.contains('on')) {
        openModelPopover(back, $('po-filter').value);
        $('po-refresh').focus();
      }
    } catch (err) { showError(err.message); }
    finally { button.disabled = false; button.textContent = '刷新列表'; }
  };
  closePopovers('popover');
  placePopover(box);
  box.classList.add('on');
}

export function openIntelPopover() {
  const box = $('popover');
  const idx = intelIndex(S.options && S.options.effort);
  const modelName = modelLabel();
  const curOpt = (S.options && (S.options.models || []).find((m) => m.id === S.options.model)) || null;
  const modelBad = !!(curOpt && curOpt.unavailable);
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
  placePopover(box);
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
  if (S.token !== 'debug') S.options = await api('/api/options' + (force ? '?refresh=1' : ''));
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
  renderPlusMenu(); closePopovers('plusmenu'); placePopover(box); box.classList.add('on');
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
  if (!S.me) return;
  const files = e.clipboardData && e.clipboardData.files;
  if (files && files.length) { e.preventDefault(); addFiles(files); }
});
let dragDepth = 0;
window.addEventListener('dragenter', (e) => {
  e.preventDefault();
  if (!S.me) return;
  if (++dragDepth === 1) $('drop').classList.add('on');
});
window.addEventListener('dragover', (e) => e.preventDefault());
window.addEventListener('dragleave', () => { if (--dragDepth <= 0) { dragDepth = 0; $('drop').classList.remove('on'); } });
window.addEventListener('drop', (e) => {
  e.preventDefault(); dragDepth = 0; $('drop').classList.remove('on');
  if (e.dataTransfer && e.dataTransfer.files.length) addFiles(e.dataTransfer.files);
});

bus.on('running-changed', updateSendBtn);
