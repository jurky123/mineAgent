// 侧栏的历史会话：列表 / 新建 / 切换 / 重命名 / 删除（未读点）

import { $, esc, bus, toast, showError, ICON } from './ui.js';
import { S, setConv } from './state.js';
import { apiGet, apiPost } from './api.js';
import { clearMessages, loadHistory } from './chat.js';

export function initConversations() {
  $('newchat').onclick = async () => {
    try {
      const r = await apiPost('/api/conversations', {});
      await refresh();
      await switchTo(r.conv);
      toast('已新建会话', 'ok');
    } catch (e) { showError(e.message); }
  };
  bus.on('conversations-changed', () => { refresh().catch(() => {}); });
  bus.on('message-other', ({ conv }) => {
    if (!S.conversations.some((c) => c.conv === conv)) return;
    S.unread.add(conv);
    render();
  });
}

// refresh 只拉列表 + 修正本地指向（别的设备删过会话时回退到第一个），
// 真正的"切换"由调用方决定（避免启动时重复 loadHistory/SSE）。
export async function refresh() {
  const body = await apiGet('/api/conversations');
  S.conversations = body.conversations || [];
  let changed = false;
  if (!S.conversations.some((c) => c.conv === S.conv)) {
    setConv(S.conversations.length ? S.conversations[0].conv : '');
    changed = true;
  }
  render();
  return changed;
}

function render() {
  const box = $('conv-list');
  box.innerHTML = '';
  S.conversations.forEach((c) => {
    const item = document.createElement('button');
    item.className = 'conv-item' + (c.conv === S.conv ? ' active' : '');
    item.title = c.title || '新会话';
    const title = c.title || '新会话';
    item.innerHTML = '<span class="conv-name">' + esc(title) + '</span>' +
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
    // 当前会话标题同步到顶栏
    if (c.conv === S.conv) $('header-title').textContent = title;
  });
  if (!S.conversations.some((c) => c.conv === S.conv)) $('header-title').textContent = 'MineAgent';
}

async function switchTo(conv) {
  setConv(conv);
  S.unread.delete(conv);
  render();
  $('sidebar').classList.remove('open');
  $('backdrop').classList.remove('on');
  clearMessages();
  await loadHistory();
  $('text').focus();
}

async function rename(c) {
  const title = prompt('重命名会话', c.title || '新会话');
  if (title === null) return;
  try {
    await apiPost('/api/conversations/rename', { conv: c.conv, title: title.trim() });
    await refresh();
  } catch (e) { showError(e.message); }
}

async function del(c) {
  if (!confirm('删除这个会话？聊天记录和记忆都会被清除，不能恢复。')) return;
  try {
    await apiPost('/api/conversations/delete', { conv: c.conv });
    if (c.conv === S.conv) {
      const next = (S.conversations.find((x) => x.conv !== c.conv) || {}).conv || '';
      setConv(next);
      await refresh();
      await switchTo(next);
    } else {
      await refresh();
    }
    toast('已删除会话', 'ok');
  } catch (e) { showError(e.message); }
}
