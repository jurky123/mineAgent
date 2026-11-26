// 消息区：渲染 / 历史分页 / SSE / 空状态与 composer 停靠

import { $, esc, fmtSize, fmtTime, bus, ICON, extInfo, showError } from './ui.js';
import { S, withTok, convParam } from './state.js';
import { api } from './api.js';
import { renderContent, enhanceContent } from './markdown.js';
import { openViewer } from './viewer.js';

let msgIds = new Set();
let msgRecords = [];
let runEventMap = new Map();
let lastId = 0, oldestId = 0, hasMore = false;
let es = null;
let docked = null;
// typingDocked：移动端点了输入框后，即使还没发消息也把 composer 停在底部
let typingDocked = false;

// ---------- 附件 ----------
function fileBubble(f) {
  const name = f.name || '文件';
  if (f.image) {
    const src = withTok(f.url);
    return '<div class="imgbox"><img src="' + esc(src) + '" alt="' + esc(name) + '" loading="lazy" data-zoom="' + esc(src) + '" data-name="' + esc(name) + '">' +
      '<div class="imgtools">' +
      '<button class="itool" data-preview title="查看">' + ICON.expand + '</button>' +
      '<a class="itool" href="' + esc(src) + '" download="' + esc(name) + '" title="下载">' + ICON.download + '</a>' +
      '</div></div>';
  }
  const [label, cls] = extInfo(name);
  return '<a class="filecard" href="' + esc(withTok(f.url)) + '" download="' + esc(name) + '" title="' + esc(name) + '">' +
    '<span class="ext ' + cls + '">' + label + '</span>' +
    '<span class="fmeta"><span class="fname">' + esc(name) + '</span>' +
    (f.size ? '<span class="fsize">' + fmtSize(f.size) + '</span>' : '') + '</span>' +
    '<span class="fdl">' + ICON.download + '</span></a>';
}

function hasMarkdownSyntax(text) {
  const s = String(text || '');
  if (!s.trim()) return false;
  return /(^|\n)\s{0,3}(#{1,6}\s|[-*+]\s|\d+\.\s|>\s|```|~~~|\|.*\|)|(\*\*|__|~~|`[^`]+`|\[[^\]]+\]\([^)]+\)|\$\$|\$[^\s$][^$\n]*\$)/.test(s);
}

// ---------- 渲染 ----------
export function renderMsg(m, prepend) {
  if (msgIds.has(m.id)) return;
  msgIds.add(m.id);
  // 注意：服务端给 agent 消息的 role 是 "agent"（不是 assistant），
  // 这里统一按"非 user 即助手"处理，否则会走纯文本分支（线上踩过）。
  const isAgent = m.role !== 'user';
  const rawText = (m.text || '').trim();
  const files = m.files || [];
  const images = files.filter((f) => f.image);
  const nonImages = files.filter((f) => !f.image);
  const isMd = hasMarkdownSyntax(rawText);
  const row = document.createElement('div');
  row.className = 'msg ' + (isAgent ? 'assistant' : 'user');
  row.dataset.raw = m.text || ''; row.dataset.messageId = m.id; row.dataset.at = m.at || 0;
  msgRecords.push({ id: m.id, role: isAgent ? 'assistant' : 'user', at: m.at || 0, el: row });
  msgRecords.sort((a, b) => (a.at - b.at) || (a.id - b.id));
  let acts = '';
  if (isAgent) {
    acts += '<span class="time">' + fmtTime(m.at) + '</span>';
    if (rawText && isMd) {
      acts += '<button class="act copy">' + ICON.copy + '复制 Markdown</button><button class="act copy-text">复制文本</button>';
    } else if (rawText) {
      acts += '<button class="act copy-text">' + ICON.copy + '复制文本</button>';
    } else if (images.length) {
      acts += '<button class="act act-view-img">' + ICON.expand + '查看大图</button><button class="act act-dl-file">' + ICON.download + '下载图片</button>';
    } else if (nonImages.length) {
      acts += '<button class="act act-dl-file">' + ICON.download + '下载附件</button>';
    }
    acts += '<button class="act fork-here" title="从这条回复创建新分支">' + ICON.fork + '创建分支</button>';
  } else {
    if (rawText) acts += '<button class="act copy">' + ICON.copy + '复制</button>';
    else if (images.length) acts += '<button class="act act-view-img">' + ICON.expand + '查看大图</button>';
    else if (nonImages.length) acts += '<button class="act act-dl-file">' + ICON.download + '下载附件</button>';
  }
  const html = '<div class="body"><div class="content"></div>' +
    (files.length ? '<div class="files">' + files.map(fileBubble).join('') + '</div>' : '') +
    (acts ? '<div class="actions">' + acts + '</div>' : '') + '</div>';
  row.innerHTML = html;
  const content = row.querySelector('.content');
  if (!rawText) {
    content.hidden = true;
  } else if (isAgent) {
    renderContent(content, m.text || '');
    enhanceContent(content);   // 懒加载高亮/KaTeX（没有代码/公式就不加载）
  } else {
    content.textContent = m.text || '';
  }
  const copy = row.querySelector('.copy');
  if (copy) copy.onclick = () => {
    navigator.clipboard.writeText(row.dataset.raw).then(() => {
      copy.innerHTML = ICON.check + '已复制'; setTimeout(() => { copy.innerHTML = ICON.copy + (isAgent ? '复制 Markdown' : '复制'); }, 1200);
    }).catch(() => showError('复制失败，请手动选择文本'));
  };
  const plainCopy = row.querySelector('.copy-text');
  if (plainCopy) plainCopy.onclick = () => {
    const label = isMd ? '复制文本' : (ICON.copy + '复制文本');
    navigator.clipboard.writeText(content.innerText || row.dataset.raw).then(() => {
      plainCopy.innerHTML = (isMd ? '' : ICON.check) + '已复制';
      setTimeout(() => { plainCopy.innerHTML = label; }, 1200);
    }).catch(() => showError('复制失败，请手动选择文本'));
  };
  const viewImg = row.querySelector('.act-view-img');
  if (viewImg) viewImg.onclick = () => {
    openViewer(images.map((f) => ({ url: withTok(f.url), name: f.name || '' })), 0);
  };
  const dlFile = row.querySelector('.act-dl-file');
  if (dlFile) dlFile.onclick = () => {
    const target = images[0] || nonImages[0];
    if (!target) return;
    const a = document.createElement('a');
    a.href = withTok(target.url); a.download = target.name || '文件';
    document.body.appendChild(a); a.click(); a.remove();
  };
  const forkBtn = row.querySelector('.fork-here');
  if (forkBtn) forkBtn.onclick = () => {
    bus.emit('fork-from-message', { conv: S.conv, messageId: m.id });
  };
  oldestId = oldestId ? Math.min(oldestId, m.id) : m.id;
  const near = scrollNearBottom();
  if (prepend) $('listInner').insertBefore(row, $('loadolder').nextSibling);
  else {
    const liveAnchor = $('listInner').querySelector('.run-events-live, .typing');
    if (liveAnchor) $('listInner').insertBefore(row, liveAnchor);
    else $('listInner').appendChild(row);
  }
  if (isAgent && runEventMap.size) syncTurnRunEvents();
  if (!prepend && near) toBottom(); else {
    if (!prepend && isAgent) { $('tobottom').classList.add('has-new'); $('tobottom').setAttribute('aria-label', '有新回复，回到底部'); if (!$('tobottom').querySelector('span')) { const label = document.createElement('span'); label.textContent = '有新回复'; $('tobottom').appendChild(label); } }
    updateToBottom();
  }
  layout();
}

export function clearMessages() {
  $('listInner').querySelectorAll('.msg,.typing,.run-events').forEach((n) => n.remove());
  msgIds.clear(); msgRecords = []; runEventMap.clear(); lastId = 0; oldestId = 0; hasMore = false;
  updateLoadOlder(); setTyping(false); layout(true);
}

let typingTimer = null, typingStart = 0, typingLabel = '正在思考…';

// setTyping(true, '正在搜索资料…', startedAt)：动态状态行，带已用秒数；
// startedAt 由服务端给出（恢复状态时保持真实已用时间）。
export function setTyping(on, text, startedAt) {
  S.waiting = on;
  const stop = $('stop'); if (stop) stop.hidden = !on;
  bus.emit('running-changed');
  const box = $('listInner');
  let row = box.querySelector('.typing');
  if (!on) {
    if (typingTimer) { clearInterval(typingTimer); typingTimer = null; }
    if (row) row.remove();
    syncTurnRunEvents();
    return;
  }
  if (text) typingLabel = text;
  if (!row) {
    row = document.createElement('div');
    row.className = 'typing typing-active';
    row.innerHTML = '<span class="step-pulse-dot" aria-hidden="true"></span><span class="task-state"></span><span class="typing-text"></span>';
    row.setAttribute('role', 'status');
    box.appendChild(row);
    typingStart = startedAt || Date.now();
  }
  const paint = () => {
    const el = row.querySelector('.typing-text');
    if (!el) return;
    const secs = Math.floor((Date.now() - typingStart) / 1000);
    const state = /审批|授权/.test(typingLabel) ? '等待审批' : /队|等待处理|已提交/.test(typingLabel) ? '排队中' : /停止|取消/.test(typingLabel) ? '正在停止' : /工具|搜索|执行|读取|查询/.test(typingLabel) ? '使用工具' : '思考中';
    row.querySelector('.task-state').textContent = state;
    el.textContent = typingLabel + (secs >= 3 ? ' · ' + secs + ' 秒' : '');
    const liveSummary = box.querySelector('.run-events-live > summary .step-live-label');
    if (liveSummary) liveSummary.textContent = typingLabel + (secs >= 3 ? ' · ' + secs + 's' : '');
  };
  paint();
  syncTurnRunEvents();
  if (!typingTimer) typingTimer = setInterval(paint, 1000);
  if (scrollNearBottom()) toBottom();
}

// ---------- 滚动 ----------
export function scrollNearBottom() {
  const el = $('list');
  return el.scrollHeight - el.scrollTop - el.clientHeight < 140;
}
export function toBottom() { $('tobottom').classList.remove('has-new'); $('tobottom').querySelector('span')?.remove(); $('tobottom').setAttribute('aria-label', '回到底部'); const el = $('list'); el.scrollTop = el.scrollHeight; updateToBottom(); }
export function updateToBottom() {
  const el = $('list');
  $('tobottom').classList.toggle('on', el.scrollHeight - el.scrollTop - el.clientHeight > 300);
}

// ---------- 空状态 / composer 停靠 ----------
export function hasMessages() { return $('listInner').querySelectorAll('.msg').length > 0; }

export function layout(force) {
  const has = hasMessages();
  if (force) typingDocked = false;   // 清空/切会话时重置回中央
  const wantBottom = has || typingDocked;
  if (docked !== wantBottom || force) { dockComposer(wantBottom, !force); docked = wantBottom; }
  $('empty').style.display = has ? 'none' : '';
}

// dockAtBottom 供移动端"聚焦输入框"时调用：把 composer 从空状态中央移到底部。
// 移动端不做位移动画——键盘弹出会同时改变视口，动画中途的 transform 容易把输入框带出屏幕。
export function dockAtBottom() {
  typingDocked = true;
  dockComposer(true, false);
  docked = true;
}

function dockComposer(atBottom, animate) {
  const c = $('composer');
  const target = atBottom ? $('bottomSlot') : $('emptySlot');
  // 清掉上一次可能残留的动画状态（键盘弹出/resize 打断时最容易出问题）
  if (c._dockTimer) { clearTimeout(c._dockTimer); c._dockTimer = null; }
  if (c.parentElement === target) {
    c.style.transition = '';
    c.style.transform = '';
    return;
  }
  const from = animate ? c.getBoundingClientRect() : null;
  target.appendChild(c);
  if (!animate) {
    c.style.transition = '';
    c.style.transform = '';
    return;
  }
  const to = c.getBoundingClientRect();
  const dx = from.left - to.left, dy = from.top - to.top;
  if (!dx && !dy) return;
  c.style.transition = 'none';
  c.style.transform = 'translate(' + dx + 'px,' + dy + 'px)';
  requestAnimationFrame(() => {
    c.style.transition = 'transform .3s cubic-bezier(.2,.8,.2,1)';
    c.style.transform = '';
    c._dockTimer = setTimeout(() => {
      c.style.transition = '';
      c.style.transform = '';
      c._dockTimer = null;
    }, 360);
  });
}

// ---------- 历史 ----------
export async function loadHistory(around) {
  clearMessages();
  if (S.conv === null) return; // 草稿：没有历史
  try {
    const body = await api('/api/history?conv=' + encodeURIComponent(convParam() || '') + (around ? '&around=' + encodeURIComponent(around) : ''));
    for (const m of body.messages || []) { lastId = Math.max(lastId, m.id); renderMsg(m); }
    hasMore = !!body.hasMore;
    updateLoadOlder();
    layout(true);
    if (around) requestAnimationFrame(() => { const hit = document.querySelector('[data-message-id="' + around + '"]'); if (hit) { hit.classList.add('search-hit'); hit.scrollIntoView({ block: 'center' }); } });
    if (!around && (body.messages || []).length) toBottom();
    // 切走再回来/刷新页面：恢复"正在处理"状态行（服务端持久化了当前进度）
    renderRunEvents(body.runEvents || []);
    if (body.running && body.running.text) setTyping(true, body.running.text, body.running.startedAt);
  } catch (e) { showError(e.message); }
}

export async function syncAfter() {
  if (S.conv === null) return;
  try {
    const body = await api('/api/history?after=' + lastId + '&conv=' + encodeURIComponent(convParam() || ''));
    for (const m of body.messages || []) { lastId = Math.max(lastId, m.id); renderMsg(m); }
    renderRunEvents(body.runEvents || []);
    setTyping(!!body.running, body.running?.text, body.running?.startedAt);
  } catch (e) { /* SSE reconnect will retry; do not resubmit user actions */ }
}

export function updateLoadOlder() { $('loadolder').hidden = !hasMore; }

export async function loadOlder() {
  const btn = $('loadolder');
  if (S.conv === null || !oldestId || btn.disabled) return;
  btn.disabled = true; btn.textContent = '加载中…';
  try {
    const body = await api('/api/history?before=' + oldestId + '&conv=' + encodeURIComponent(convParam() || ''));
    const list = body.messages || [];
    const el = $('list');
    const prevH = el.scrollHeight, prevTop = el.scrollTop;
    list.forEach((m) => renderMsg(m, true));
    hasMore = !!body.hasMore;
    el.scrollTop = el.scrollHeight - prevH + prevTop;
  } catch (e) { showError(e.message); }
  btn.disabled = false; btn.textContent = '载入更早消息';
  updateLoadOlder();
}

// ---------- SSE ----------
export function disconnect() {
  if (es) { es.close(); es = null; }
}

export function connectSSE() {
  if (es) es.close();
  es = new EventSource('/api/events?token=' + encodeURIComponent(S.token));
  es.addEventListener('message', (ev) => {
    let data = null;
    try { data = JSON.parse(ev.data); } catch (e) { return; }
    if (data.type === 'message' && data.message) {
      const evConv = data.conv || '';
      // 草稿态（S.conv=null）：不匹配任何会话，交给发送流程回填
      if (S.conv === null || evConv !== S.conv) {
        if (data.message.role === 'agent') bus.emit('message-other', { conv: evConv });
        return;
      }
      lastId = Math.max(lastId, data.message.id);
      renderMsg(data.message);

    } else if (data.type === 'cleared') {
      if (S.conv !== null && data.conv === S.conv) clearMessages();
    } else if (data.type === 'progress') {
      if (S.conv !== null && (data.conv || '') === S.conv) setTyping(true, data.text || '正在思考…');
    } else if (data.type === 'run_done') {
      bus.emit('conversations-changed');
      if (S.conv !== null && (data.conv || '') === S.conv) { setTyping(false); syncAfter(); }
    } else if (data.type === 'run_event') {
      if (S.conv !== null && (data.conv || '') === S.conv) renderRunEvents([data.event], true);
    } else if (data.type === 'conversations') {
      bus.emit('conversations-changed');
    }
  });
  es.onopen = () => { $('connbar').hidden = true; syncAfter(); };
  es.onerror = () => { $('connbar').hidden = false; };
}

// 列表内的图片点击（挂一次）
$('list').addEventListener('click', (e) => {
  const hit = e.target.closest('img[data-zoom],[data-preview]');
  if (!hit) return;
  const row = e.target.closest('.msg');
  if (!row) return;
  const imgs = Array.from(row.querySelectorAll('img[data-zoom]'));
  if (!imgs.length) return;
  const target = hit.tagName === 'IMG' ? hit : hit.closest('.imgbox').querySelector('img[data-zoom]');
  openViewer(imgs.map((n) => ({ url: n.dataset.zoom, name: n.dataset.name || '' })), Math.max(0, imgs.indexOf(target)));
});

function formatRunEvent(event) {
  const labels = { started: '正在调用', returned: '已返回（结果以回复为准）', failed: '调用失败', cancelled: '已停止或未完成', unconfirmed: '未收到完成事件', approval: '等待管理员审批' };
  return (event.at ? new Date(event.at).toLocaleTimeString() + ' · ' : '') + event.name + ' · ' + (labels[event.status] || event.status) + (event.durationMs ? ' · ' + (event.durationMs / 1000).toFixed(1) + 's' : '');
}

function fillRunEventsEl(details, events, isLive) {
  const open = details.open;
  details.className = 'run-events' + (isLive ? ' run-events-live' : '');
  const activeStep = events.findLast ? events.findLast((e) => e.status === 'started' || e.status === 'approval') : [...events].reverse().find((e) => e.status === 'started' || e.status === 'approval');
  const latest = activeStep || events[events.length - 1];
  const summary = details.querySelector('summary') || document.createElement('summary');
  if (isLive) {
    const liveText = activeStep ? (activeStep.name + ' · ' + (activeStep.status === 'approval' ? '等待审批' : '执行中…')) : (typingLabel || (latest ? latest.name : '正在处理…'));
    summary.innerHTML = '<span class="step-pulse-dot" aria-hidden="true"></span><span>任务步骤（' + events.length + '）</span><span class="step-live-label">' + esc(liveText) + '</span>';
  } else {
    summary.innerHTML = '<span>任务步骤（' + events.length + ' 步）</span>';
  }
  if (!summary.parentNode) details.appendChild(summary);
  let list = details.querySelector('.run-event-list');
  if (!list) { list = document.createElement('ol'); list.className = 'run-event-list'; details.appendChild(list); }
  list.replaceChildren();
  for (const event of events) {
    const row = document.createElement('li');
    row.dataset.callId = event.id;
    if (event.status === 'started' || event.status === 'approval') row.className = 'step-running';
    row.textContent = formatRunEvent(event);
    list.appendChild(row);
  }
  details.open = open;
}

function syncTurnRunEvents() {
  const box = $('listInner');
  const events = Array.from(runEventMap.values()).sort((a, b) => (a.at || 0) - (b.at || 0));
  const assistants = msgRecords.filter((m) => m.role === 'assistant');
  const buckets = new Map();
  const liveEvents = [];

  for (const ev of events) {
    const evAt = ev.at || 0;
    let target = null;
    for (const a of assistants) {
      if (a.at && evAt && evAt <= a.at + 2000) { target = a; break; }
    }
    if (!target && !S.waiting && assistants.length && (!evAt || evAt <= (assistants[assistants.length - 1].at || 0) + 15000)) {
      target = assistants[assistants.length - 1];
    }
    if (target) {
      if (!buckets.has(target.id)) buckets.set(target.id, []);
      buckets.get(target.id).push(ev);
    } else {
      liveEvents.push(ev);
    }
  }

  for (const a of assistants) {
    const body = a.el.querySelector('.body');
    if (!body) continue;
    const evs = buckets.get(a.id);
    let details = body.querySelector(':scope > .run-events');
    if (!evs || !evs.length) {
      if (details) details.remove();
      continue;
    }
    if (!details) {
      details = document.createElement('details');
      details.className = 'run-events';
      body.insertBefore(details, body.firstChild);
    }
    fillRunEventsEl(details, evs, false);
  }

  let liveDetails = box.querySelector(':scope > .run-events');
  if (liveEvents.length) {
    if (!liveDetails) {
      liveDetails = document.createElement('details');
      const typing = box.querySelector('.typing');
      if (typing) box.insertBefore(liveDetails, typing);
      else box.appendChild(liveDetails);
    }
    fillRunEventsEl(liveDetails, liveEvents, S.waiting || liveEvents.some((e) => e.status === 'started' || e.status === 'approval'));
  } else if (liveDetails) {
    liveDetails.remove();
  }
}

function renderRunEvents(events, append = false) {
  if (!append) runEventMap.clear();
  for (const event of events || []) {
    if (!event || !event.id) continue;
    runEventMap.set(event.id, event);
  }
  syncTurnRunEvents();
}
