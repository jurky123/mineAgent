// portal.js：门户首页 —— 欢迎语 + 应用卡片（公告栏 / Agent / MC 状态 / 占位）。
import { shell, boot, apiGet, apiPost, h } from './shell.js';

const qs = new URLSearchParams(location.search);
const DEMO = qs.get('ui') === '1';
const PAGE_THEME = qs.get('theme');
if (PAGE_THEME === 'dark' || PAGE_THEME === 'light') {
  localStorage.setItem('mineagent.theme', PAGE_THEME);
}

function fmtTime(ms) {
  if (!ms) return '';
  const d = new Date(ms);
  const now = new Date();
  const sameDay = d.toDateString() === now.toDateString();
  const pad = (n) => String(n).padStart(2, '0');
  if (sameDay) return '今天 ' + pad(d.getHours()) + ':' + pad(d.getMinutes());
  return (d.getMonth() + 1) + '月' + d.getDate() + '日 ' + pad(d.getHours()) + ':' + pad(d.getMinutes());
}

function renderStat(card) {
  const body = h('div', 'card-body');
  if (card.items && card.items.length) {
    const grid = h('div', 'stat-grid');
    for (const it of card.items) {
      const cell = h('div', 'stat-cell');
      cell.appendChild(h('div', 'stat-label', it.label));
      cell.appendChild(h('div', 'stat-value', String(it.value)));
      grid.appendChild(cell);
    }
    body.appendChild(grid);
  } else {
    const big = h('div', 'stat-big');
    big.appendChild(h('span', 'stat-num', String(card.value ?? '—')));
    if (card.hint) big.appendChild(h('span', 'stat-hint', card.hint));
    body.appendChild(big);
  }
  return body;
}

function renderList(card, app) {
  const body = h('div', 'card-body');
  const items = card.items || [];
  if (!items.length) {
    body.appendChild(h('div', 'empty', card.empty || '暂无内容'));
    return body;
  }
  const ul = h('ul', 'ann-list');
  for (const it of items) {
    const li = h('li', 'ann-item');
    const main = h('div', 'ann-main');
    main.appendChild(h('div', 'ann-text', it.text));
    main.appendChild(h('div', 'ann-meta', (it.author ? it.author + ' · ' : '') + fmtTime(it.createdAt)));
    li.appendChild(main);
    if (card.canEdit) {
      const del = h('button', 'icon-btn sm ann-del');
      del.textContent = '×';
      del.title = '删除公告';
      del.onclick = async () => {
        if (!confirm('删除这条公告？')) return;
        await fetch('/api/portal/announcements?id=' + it.id, {
          method: 'DELETE',
          headers: { Authorization: 'Bearer ' + shell.token },
        });
        refresh();
      };
      li.appendChild(del);
    }
    ul.appendChild(li);
  }
  body.appendChild(ul);
  if (card.canEdit) {
    const btn = h('button', 'btn ghost sm', '+ 发布公告');
    btn.onclick = async () => {
      const text = prompt('公告内容（最多 500 字）：');
      if (!text || !text.trim()) return;
      const res = await fetch('/api/portal/announcements', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Authorization: 'Bearer ' + shell.token },
        body: JSON.stringify({ text: text.trim() }),
      });
      if (!res.ok) {
        const b = await res.json().catch(() => null);
        alert((b && b.error) || '发布失败');
        return;
      }
      refresh();
    };
    body.appendChild(btn);
  }
  return body;
}

function renderLink(card, app) {
  const body = h('div', 'card-body');
  body.appendChild(h('div', 'link-desc', card.hint || app.desc || ''));
  const btn = h('span', 'link-go', '进入 →');
  body.appendChild(btn);
  return body;
}

function cardEl(app) {
  const card = app.card || {};
  const el = h('article', 'card' + (app.path ? ' clickable' : '') + (app.disabled ? ' disabled' : ''));
  const head = h('div', 'card-head');
  head.appendChild(h('h3', 'card-title', card.title || app.name));
  if (app.disabled) head.appendChild(h('span', 'badge', '开发中'));
  el.appendChild(head);
  if (app.disabled) {
    el.appendChild(h('div', 'card-body', '这个功能还在开发中，先占个位置。'));
    return el;
  }
  const type = card.type || (app.path ? 'link' : 'text');
  if (app.error) {
    el.appendChild(h('div', 'card-error', app.error));
  } else if (type === 'stat') {
    el.appendChild(renderStat(card));
  } else if (type === 'list') {
    el.appendChild(renderList(card, app));
  } else if (type === 'link') {
    el.appendChild(renderLink(card, app));
  } else {
    el.appendChild(h('div', 'card-body', card.text || app.desc || ''));
  }
  if (app.path) {
    el.addEventListener('click', (e) => {
      if (e.target.closest('button, a')) return;
      location.href = app.path;
    });
  }
  return el;
}

function demoData() {
  return {
    greeting: '下午好，jzk',
    apps: [
      { id: 'announcements', name: '公告栏', card: { type: 'list', title: '公告栏', canEdit: true, items: [
        { id: 2, text: '门户上线试运行：登录、公告、服务器状态已可用，游戏平台开发中。', author: 'jzk', createdAt: Date.now() - 3600e3 },
        { id: 1, text: '欢迎来到 Mine。', author: 'jzk', createdAt: Date.now() - 86400e3 },
      ] } },
      { id: 'agent', name: 'Agent 对话', path: '/agent', card: { type: 'stat', title: 'Agent 对话', value: 12, hint: '个会话' } },
      { id: 'minecraft', name: 'Minecraft 服务器', card: { type: 'stat', title: 'Minecraft 服务器', items: [
        { label: '在线', value: '2 / 20' }, { label: 'TPS', value: '19.98' },
        { label: '内存', value: '1234 / 4096 MB' }, { label: '版本', value: '1.21.8' },
      ], hint: '数据来自 Paper 服务器' } },
      { id: 'games', name: '小游戏', path: '/games', card: { type: 'link', title: '小游戏', hint: '2048 / 贪吃蛇 / 记忆翻牌' } },
    ],
    account: { id: 1, name: 'jzk', admin: true },
  };
}

function paint(data) {
  document.getElementById('greeting').textContent = data.greeting || '你好';
  const admin = data.account && data.account.admin;
  document.getElementById('hero-sub').textContent =
    (admin ? '管理员' : '欢迎回来') + ' · 一切从简，先上线再打磨';
  const host = document.getElementById('cards');
  host.innerHTML = '';
  for (const app of data.apps) host.appendChild(cardEl(app));
}

async function refresh() {
  if (DEMO) { paint(demoData()); return; }
  try {
    paint(await apiGet('/api/portal/home'));
  } catch (e) {
    if (e.unauthorized) { location.reload(); return; }
    document.getElementById('cards').innerHTML = '';
    document.getElementById('cards').appendChild(h('div', 'card', '加载失败：' + e.message));
  }
}

(async () => {
  document.getElementById('foot-ver').textContent = 'v' + (document.querySelector('meta[name=mineagent-version]')?.content || '');
  await boot({ active: '/', requireLogin: !DEMO });
  await refresh();
})();
