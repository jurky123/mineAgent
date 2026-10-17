// 侧栏聊天列表：Draft 状态机 + 分组 + 搜索 + 重命名/删除
//
// 会话生命周期（和后端语义一一对应）：
//   S.conv === null  -> 新会话草稿：纯前端状态，不落库、侧栏不显示
//   S.conv === ""    -> 旧版默认会话（历史遗留，仍在侧栏显示）
//   S.conv === "id"  -> 已存在会话
// 草稿在发送第一条消息时由 /api/send 原子创建，返回值回填 S.conv。

import { $, esc, bus, toast, showError, askConfirm, askInput } from './ui.js';
import { S, setConv } from './state.js';
import { apiGet, apiPost } from './api.js';
import { clearMessages, loadHistory } from './chat.js';

let search = '', results = null, searchSequence = 0, searchTimer;
let showArchived = false;
async function searchMessages() {
 const sequence = ++searchSequence;
 if (!search) { results = null; renderList(); return; }
 try { const body = await apiGet('/api/conversations?q=' + encodeURIComponent(search)); if (sequence !== searchSequence) return; results = body.conversations || []; renderList(); }
 catch (error) { if (sequence === searchSequence) showError(error.message); }
}


export function initConversations() {
  $('newchat').onclick = newChat;
  const searchBox = $('searchbox');
  const searchInput = $('conv-search');
  const clearBtn = $('search-clear');
  const syncClear = () => { clearBtn.hidden = !searchInput.value; };
  searchBox.addEventListener('click', (e) => { if (!e.target.closest('.search-clear')) searchInput.focus(); });
  searchInput.addEventListener('input', () => {
    search = searchInput.value.trim().toLowerCase(); searchSequence++;
    syncClear();
    clearTimeout(searchTimer); searchTimer = setTimeout(searchMessages, 200);
    if (!search) { results = null; searchSequence++; renderList(); }
  });
  searchInput.addEventListener('keydown', (e) => { if (e.key === 'Escape') { searchInput.value = ''; search = ''; results = null; searchSequence++; syncClear(); renderList(); } });
  clearBtn.onclick = () => { searchInput.value = ''; search = ''; results = null; searchSequence++; syncClear(); renderList(); searchInput.focus(); };
  const archiveToggle = document.createElement('button'); archiveToggle.className = 'side-archive-toggle'; archiveToggle.textContent = '已归档';
  archiveToggle.onclick = () => { showArchived = !showArchived; archiveToggle.textContent = showArchived ? '返回聊天列表' : '已归档'; renderList(); }; $('conv-list').before(archiveToggle);
  bus.on('conversations-changed', () => { refresh().catch(() => {}); });
  bus.on('message-other', ({ conv }) => {
    if (!S.conversations.some((c) => c.conv === conv)) return;
    S.unread.add(conv);
    renderList();
  });
}

// 新聊天只切到草稿态：不调接口、不建库、不弹 toast（重复点击无副作用）。
export function newChat() {
  if (S.conv === null) { $('text').focus(); return; }
  setConv(null);
  S.unread.clear();
  clearMessages();
  renderList();
  bus.emit('sidebar-close');
  $('text').focus();
}

// refresh 只拉列表 + 修正本地指向（别的设备删过会话时回到草稿态）。
export async function refresh() {
  const body = await apiGet('/api/conversations');
  S.conversations = body.conversations || [];
  if (S.conv !== null && !S.conversations.some((c) => c.conv === S.conv)) {
    setConv(null);
    clearMessages();
  }
  if (search) await searchMessages(); else renderList();
}

function dayBucket(ms) {
  if (!ms) return 2;
  const d = new Date(ms);
  const now = new Date();
  const startOfToday = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
  if (d.getTime() >= startOfToday) return 0;
  if (d.getTime() >= startOfToday - 86400000) return 1;
  return 2;
}

export function renderList() {
  const box = $('conv-list');
  box.innerHTML = '';
  const groups = ['今天', '昨天', '更早'];
  const shown = (search && results !== null ? results : S.conversations).filter((c) => search ? true : !!c.archived === showArchived);
  if (!shown.length) {
    box.innerHTML = '<div class="side-empty">' + (search ? '没有匹配的聊天' : '还没有聊天记录') + '</div>';
  }
  let lastBucket = -1;
  shown.forEach((c) => {
    const bucket = c.pinned ? -2 : dayBucket(c.updatedAt);
    if (bucket !== lastBucket) {
      lastBucket = bucket;
      const title = document.createElement('div');
      title.className = 'side-title';
      title.textContent = bucket === -2 ? '置顶' : groups[bucket];
      box.appendChild(title);
    }
    const item = document.createElement('div');
    item.className = 'conv-item' + (S.conv !== null && c.conv === S.conv ? ' active' : '');
    const label = c.title || '新聊天';
    const open = document.createElement('button'); open.className = 'conv-open'; open.title = label;
    open.innerHTML = '<span class="conv-name">' + esc(label) + '</span>' + (c.snippet ? '<span class="conv-snippet">' + esc(c.snippet) + '</span>' : '');
    open.onclick = () => switchTo(c.conv, c.matchId);
    item.appendChild(open);
    if (S.unread.has(c.conv) && c.conv !== S.conv) { const dot = document.createElement('span'); dot.className = 'conv-dot'; item.appendChild(dot); }
    const menu = document.createElement('details'); menu.className = 'conv-menu';
    const trigger = document.createElement('summary'); trigger.textContent = '⋯'; trigger.setAttribute('aria-label', '会话操作：' + label); menu.appendChild(trigger);
    const actions = document.createElement('div'); actions.className = 'conv-menu-actions';
    for (const [label, run] of [
      ['重命名', () => rename(c)],
      [c.pinned ? '取消置顶' : '置顶', () => settings(c, { pinned: !c.pinned })],
      [c.archived ? '取消归档' : '归档', () => settings(c, { archived: !c.archived })],
      ['删除', () => del(c)],
    ]) { const button = document.createElement('button'); button.textContent = label; button.onclick = () => { menu.open = false; run(); }; actions.appendChild(button); }
    menu.addEventListener('keydown', (event) => { if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); menu.open = false; trigger.focus(); } });
    menu.appendChild(actions); item.appendChild(menu);
    box.appendChild(item);
  });
  // 顶栏标题：草稿态显示"新聊天"
  const active = S.conversations.find((c) => c.conv === S.conv);
  $('header-title').textContent = S.conv === null ? '新聊天' : ((active && active.title) || 'MineAgent');
}

export async function switchTo(conv, around) {
  setConv(conv);
  S.unread.delete(conv);
  renderList();
  bus.emit('sidebar-close');
  await loadHistory(around);
  $('text').focus();
}

async function rename(c) {
  if (c.conv === null) return;
  const title = await askInput({ title: '重命名聊天', text: '', value: c.title || '', okLabel: '保存' });
  if (title === null || title === (c.title || '')) return;
  try {
    await apiPost('/api/conversations/rename', { conv: c.conv, title: title });
    await refresh();
  } catch (e) { showError(e.message); }
}

async function del(c) {
  const ok = await askConfirm({
    title: '删除这个聊天？',
    text: '聊天记录和记忆都会被清除，不能恢复。',
    okLabel: '删除'
  });
  if (!ok) return;
  try {
    await apiPost('/api/conversations/delete', { conv: c.conv });
    if (c.conv === S.conv) {
      setConv(null);
      clearMessages();
    }
    await refresh();
    toast('已删除聊天', 'ok');
  } catch (e) { showError(e.message); }
}

async function settings(c, patch) {
 try { await apiPost('/api/conversations/settings', { conv: c.conv, pinned: !!c.pinned, archived: !!c.archived, ...patch }); await refresh(); }
 catch (error) { showError(error.message); }
}
