// games.js：小游戏大厅（游戏列表 / 创建或加入国际象棋房间 / 最近战绩）。
import { shell, boot, apiGet, apiPost, h } from './shell.js';

const qs = new URLSearchParams(location.search);
const DEMO = qs.get('ui') === '1';

function fmtTime(ms) {
  if (!ms) return '';
  const d = new Date(ms);
  const pad = (n) => String(n).padStart(2, '0');
  return (d.getMonth() + 1) + '月' + d.getDate() + '日 ' + pad(d.getHours()) + ':' + pad(d.getMinutes());
}

function renderGames(list) {
  const host = document.getElementById('games');
  host.innerHTML = '';
  for (const g of list) {
    const el = h('article', 'card' + (g.enabled && g.path ? ' clickable' : ' disabled'));
    const head = h('div', 'card-head');
    head.appendChild(h('h3', 'card-title', (g.icon ? g.icon + ' ' : '') + g.name));
    if (!g.enabled) head.appendChild(h('span', 'badge', '开发中'));
    el.appendChild(head);
    el.appendChild(h('div', 'card-body', g.desc || ''));
    if (g.enabled && g.path) el.onclick = () => { location.href = g.path; };
    host.appendChild(el);
  }
}

async function renderRooms() {
  const host = document.getElementById('rooms');
  try {
    const { rooms } = await apiGet('/api/games/chess/rooms');
    host.innerHTML = '';
    if (!rooms.length) {
      host.appendChild(h('div', 'empty', '还没有等待中的房间，创建一间等人来吧'));
      return;
    }
    for (const r of rooms) {
      const row = h('div', 'room-row');
      const main = h('div', 'room-main');
      main.appendChild(h('div', 'room-name', r.host + ' 的房间'));
      main.appendChild(h('div', 'room-meta', '房间码 ' + r.id + ' · ' + fmtTime(r.createdAt)));
      row.appendChild(main);
      const btn = h('button', 'btn sm', '加入');
      btn.onclick = () => join(r.id);
      row.appendChild(btn);
      host.appendChild(row);
    }
  } catch (e) {
    host.innerHTML = '';
    host.appendChild(h('div', 'empty', '加载失败：' + e.message));
  }
}

async function renderRuns() {
  const host = document.getElementById('runs');
  if (DEMO) {
    host.innerHTML = '';
    host.appendChild(h('div', 'todo-line', '还没下过棋，去开一局吧'));
    return;
  }
  try {
    const { runs } = await apiGet('/api/games/chess/runs?limit=10');
    host.innerHTML = '';
    if (!runs || !runs.length) {
      host.appendChild(h('div', 'empty', '还没有对局记录，创建房间下一局吧'));
      return;
    }
    for (const r of runs) {
      let meta = {};
      try { meta = JSON.parse(r.metadata || '{}'); } catch (e) {}
      const row = h('div', 'run-row');
      const tag = h('span', 'result ' + r.result, r.result === 'win' ? '胜' : r.result === 'lose' ? '负' : '和');
      row.appendChild(tag);
      row.appendChild(h('span', 'run-meta',
        (meta.color === 'white' ? '执白' : '执黑') +
        (meta.opponent ? ' vs ' + meta.opponent : '') +
        ' · ' + (meta.moves || 0) + ' 回合'));
      const reason = { checkmate: '将杀', resign: '认输', stalemate: '逼和', leave: '离开' }[meta.reason] || meta.reason || '';
      row.appendChild(h('span', 'run-time', reason + ' · ' + fmtTime(r.createdAt)));
      host.appendChild(row);
    }
  } catch (e) {
    host.innerHTML = '';
    host.appendChild(h('div', 'empty', '加载失败：' + e.message));
  }
}

async function create() {
  try {
    await apiPost('/api/games/chess/rooms');
    location.href = '/games/chess';
  } catch (e) { alert('创建失败：' + e.message); }
}

async function join(code) {
  code = (code || '').trim().toUpperCase();
  if (!code) { alert('先填房间码'); return; }
  try {
    await apiPost('/api/games/chess/rooms/join', { room: code });
    location.href = '/games/chess';
  } catch (e) { alert('加入失败：' + e.message); }
}

(async () => {
  await boot({ active: '/games', requireLogin: !DEMO });
  if (DEMO) {
    renderGames([{ id: 'chess', name: '国际象棋', icon: '♞', desc: '在线房间对战 · 服务端裁判', enabled: true, path: '/games/chess' }]);
    document.getElementById('rooms').innerHTML = '';
    document.getElementById('rooms').appendChild(h('div', 'empty', '（演示）jzk 的房间 · 房间码 AB3K9Q'));
    renderRuns();
    return;
  }
  try {
    const { games } = await apiGet('/api/games');
    renderGames(games || []);
  } catch (e) {
    document.getElementById('games').appendChild(h('div', 'card', '加载失败：' + e.message));
  }
  await renderRooms();
  await renderRuns();
  document.getElementById('create').onclick = create;
  document.getElementById('refresh').onclick = renderRooms;
  document.getElementById('join').onclick = () => join(document.getElementById('code').value);
  document.getElementById('code').addEventListener('keydown', (e) => { if (e.key === 'Enter') join(e.target.value); });
})();
