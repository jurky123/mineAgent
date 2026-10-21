// 消息区：渲染 / 历史分页 / SSE / 空状态与 composer 停靠

import { $, esc, fmtSize, fmtTime, bus, ICON, extInfo, showError } from './ui.js';
import { S, withTok, convParam } from './state.js';
import { api } from './api.js';
import { renderContent, enhanceContent } from './markdown.js';
import { openViewer } from './viewer.js';

let msgIds = new Set();
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

// ---------- 渲染 ----------
export function renderMsg(m, prepend) {
  if (msgIds.has(m.id)) return;
  msgIds.add(m.id);
  // 注意：服务端给 agent 消息的 role 是 "agent"（不是 assistant），
  // 这里统一按"非 user 即助手"处理，否则会走纯文本分支（线上踩过）。
  const isAgent = m.role !== 'user';
  const row = document.createElement('div');
  row.className = 'msg ' + (isAgent ? 'assistant' : 'user');
  row.dataset.raw = m.text || ''; row.dataset.messageId = m.id;
  let html = '<div class="body">';
  if (isAgent) {
    html += '<div class="content"></div>' +
      (m.files && m.files.length ? '<div class="files">' + m.files.map(fileBubble).join('') + '</div>' : '') +
      '<div class="actions"><span class="time">' + fmtTime(m.at) + '</span>' +
      '<button class="act copy">' + ICON.copy + '复制 Markdown</button><button class="act copy-text">复制文本</button></div>';
  } else {
    html += '<div class="content"></div>' +
      (m.files && m.files.length ? '<div class="files">' + m.files.map(fileBubble).join('') + '</div>' : '') +
      '<div class="actions"><button class="act copy">' + ICON.copy + '复制</button></div>';
  }
  html += '</div>';
  row.innerHTML = html;
  const content = row.querySelector('.content');
  if (isAgent) {
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
  if (plainCopy) plainCopy.onclick = () => navigator.clipboard.writeText(content.innerText).then(() => { plainCopy.textContent = '已复制'; setTimeout(() => { plainCopy.textContent = '复制文本'; }, 1200); }).catch(() => showError('复制失败，请手动选择文本'));
  oldestId = oldestId ? Math.min(oldestId, m.id) : m.id;
  const near = scrollNearBottom();
  if (prepend) $('listInner').insertBefore(row, $('loadolder').nextSibling);
  else $('listInner').appendChild(row);
  if (!prepend && near) toBottom(); else {
    if (!prepend && isAgent) { $('tobottom').classList.add('has-new'); $('tobottom').setAttribute('aria-label', '有新回复，回到底部'); if (!$('tobottom').querySelector('span')) { const label = document.createElement('span'); label.textContent = '有新回复'; $('tobottom').appendChild(label); } }
    updateToBottom();
  }
  layout();
}

export function clearMessages() {
  $('listInner').querySelectorAll('.msg,.typing,.run-events').forEach((n) => n.remove());
  msgIds.clear(); lastId = 0; oldestId = 0; hasMore = false;
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
    return;
  }
  if (text) typingLabel = text;
  if (!row) {
    row = document.createElement('div');
    row.className = 'typing';
    row.innerHTML = '<span class="task-state"></span><span class="typing-text"></span>';
    row.setAttribute('role', 'status');
    box.appendChild(row);
    typingStart = startedAt || Date.now();
  }
  const paint = () => {
    const el = row.querySelector('.typing-text');
    if (!el) return;
    const secs = Math.floor((Date.now() - typingStart) / 1000);
    const state = /审批|授权/.test(typingLabel) ? '等待审批' : /队|等待处理|已提交/.test(typingLabel) ? '排队中' : /停止|取消/.test(typingLabel) ? '正在停止' : /工具|搜索|执行|读取|查询/.test(typingLabel) ? '使用工具' : '处理中';
    row.querySelector('.task-state').textContent = state;
    el.textContent = typingLabel + (secs >= 3 ? ' · ' + secs + ' 秒' : '');
  };
  paint();
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

function renderRunEvents(events, append = false) {
  if (!events.length) return;
  let details = $('listInner').querySelector('.run-events');
  if (!details) { details = document.createElement('details'); details.className = 'run-events'; details.innerHTML = '<summary>任务步骤</summary><ol class="run-event-list"></ol>';  $('listInner').appendChild(details); }
  const list = details.querySelector('.run-event-list'); if (!append) list.replaceChildren();
  for (const event of events) {
    let row = Array.from(list.children).find((el) => el.dataset.callId === event.id);
    if (!row) { row = document.createElement('li'); row.dataset.callId = event.id; list.appendChild(row); }
    const labels = { started: '请求调用', returned: '已返回（结果以回复为准）', failed: '调用失败', cancelled: '已停止或未完成', unconfirmed: '未收到完成事件', approval: '等待管理员审批' };
    row.textContent = (event.at ? new Date(event.at).toLocaleTimeString() + ' · ' : '') + event.name + ' · ' + (labels[event.status] || event.status) + (event.durationMs ? ' · ' + (event.durationMs / 1000).toFixed(1) + 's' : '');
  }
}
