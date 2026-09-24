// 侧栏聊天列表：Draft 状态机 + 分组 + 搜索 + 重命名/删除
//
// 会话生命周期（和后端语义一一对应）：
//   S.conv === null  -> 新会话草稿：纯前端状态，不落库、侧栏不显示
//   S.conv === ""    -> 旧版默认会话（历史遗留，仍在侧栏显示）
//   S.conv === "id"  -> 已存在会话
// 草稿在发送第一条消息时由 /api/send 原子创建，返回值回填 S.conv。

import { $, esc, bus, toast, showError, ICON } from './ui.js';
import { S, setConv } from './state.js';
import { apiGet, apiPost } from './api.js';
import { clearMessages, loadHistory } from './chat.js';

let search = '';

export function initConversations() {
  $('newchat').onclick = newChat;
  $('conv-search').addEventListener('input', () => { search = $('conv-search').value.trim().toLowerCase(); render(); });
  bus.on('conversations-changed', () => { refresh().catch(() => {}); });
  bus.on('message-other', ({ conv }) => {
    if (!S.conversations.some((c) => c.conv === conv)) return;
    S.unread.add(conv);
    render();
  });
}

// 新聊天只切到草稿态：不调接口、不建库、不弹 toast（重复点击无副作用）。
export function newChat() {
  if (S.conv === null) { $('text').focus(); return; }
  setConv(null);
  S.unread.clear();
  clearMessages();
  render();
  $('sidebar').classList.remove('open');
  $('backdrop').classList.remove('on');
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
  render();
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

function render() {
  const box = $('conv-list');
  box.innerHTML = '';
  const groups = ['今天', '昨天', '更早'];
  const shown = S.conversations.filter((c) => {
    if (!search) return true;
    return (c.title || '新聊天').toLowerCase().indexOf(search) >= 0;
  });
  if (!shown.length) {
    box.innerHTML = '<div class="side-empty">' + (search ? '没有匹配的聊天' : '还没有聊天记录') + '</div>';
  }
  let lastBucket = -1;
  shown.forEach((c) => {
    const bucket = dayBucket(c.updatedAt);
    if (bucket !== lastBucket) {
      lastBucket = bucket;
      const title = document.createElement('div');
      title.className = 'side-title';
      title.textContent = groups[bucket];
      box.appendChild(title);
    }
    const item = document.createElement('button');
    item.className = 'conv-item' + (S.conv !== null && c.conv === S.conv ? ' active' : '');
    const label = c.title || '新聊天';
    item.title = label;
    item.innerHTML = '<span class="conv-name">' + esc(label) + '</span>' +
      (S.unread.has(c.conv) && c.conv !== S.conv ? '<span class="conv-dot"></span>' : '') +
      '<span class="conv-acts">' +
      '<span class="conv-act" data-act="rename" title="重命名">' + ICON.edit + '</span>' +
      '<span class="conv-act" data-act="delete" title="删除">' + ICON.trash + '</span>' +
      '</span>';
    item.onclick = (e) => {
      const act = e.target.closest('[data-act]');
      if (act && act.dataset.act === 'rename') { e.stopPropagation(); rename(c); return; }
      if (act && act.dataset.act === 'delete') { e.stopPropagation(); del(c); return; }
      if (c.conv !== S.conv) switchTo(c.conv);
    };
    box.appendChild(item);
  });
  // 顶栏标题：草稿态显示"新聊天"
  const active = S.conversations.find((c) => c.conv === S.conv);
  $('header-title').textContent = S.conv === null ? '新聊天' : ((active && active.title) || 'MineAgent');
}

async function switchTo(conv) {
  setConv(conv);
  S.unread.delete(conv);
  render();
  $('sidebar').classList.remove('open');
  $('backdrop').classList.remove('on');
  await loadHistory();
  $('text').focus();
}

async function rename(c) {
  if (c.conv === null) return;
  const title = prompt('重命名聊天', c.title || '新聊天');
  if (title === null) return;
  try {
    await apiPost('/api/conversations/rename', { conv: c.conv, title: title.trim() });
    await refresh();
  } catch (e) { showError(e.message); }
}

async function del(c) {
  if (!confirm('删除这个聊天？记录和记忆都会被清除，不能恢复。')) return;
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
