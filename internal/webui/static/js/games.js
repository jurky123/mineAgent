// games.js — 游戏目录：只负责「发现游戏」，具体房间/对局在各自的游戏页里。
import { shell, boot, apiGet } from './shell.js';
import { h, icon, pieceImg } from './ds.js';

const qs = new URLSearchParams(location.search);
const DEMO = qs.get('ui') === '1';

// 每个游戏卡片的"额外状态"数据源（有就展示，没有就只显示简介）
async function chessStatus() {
  const [rooms, runs] = await Promise.all([
    apiGet('/api/games/chess/rooms').catch(() => ({ rooms: [] })),
    apiGet('/api/games/chess/runs?limit=50').catch(() => ({ runs: [] })),
  ]);
  let wins = 0, losses = 0, draws = 0;
  for (const r of runs.runs || []) {
    if (r.result === 'win') wins++;
    else if (r.result === 'lose') losses++;
    else draws++;
  }
  return { openRooms: (rooms.rooms || []).length, wins, losses, draws, total: (runs.runs || []).length };
}

function catalogCard(game, status) {
  const card = h('article', 'card catalog-card');
  const head = h('div', 'catalog-head');
  const ic = h('span', 'game-icon');
  ic.appendChild(pieceImg('N', 'game-icon-img'));
  head.appendChild(ic);
  const t = h('div');
  t.appendChild(h('div', 'catalog-title', game.name));
  t.appendChild(h('div', 'game-desc', game.desc || ''));
  head.appendChild(t);
  card.appendChild(head);

  const bits = [];
  if (status) {
    if (status.openRooms) bits.push(status.openRooms + ' 个开放房间');
    if (status.total) bits.push('最近 ' + status.wins + ' 胜 ' + status.losses + ' 负' + (status.draws ? ' ' + status.draws + ' 和' : ''));
  }
  card.appendChild(h('div', 'game-stats', bits.length ? bits.join(' · ') : '还没有对局记录'));

  const actions = h('div', 'catalog-actions');
  const start = h('a', 'btn primary cta', status && status.openRooms ? '去加入' : '开始');
  start.href = game.path;
  actions.appendChild(start);
  card.appendChild(actions);
  return card;
}

(async () => {
  if (DEMO) {
    shell.user = { id: 1, name: 'jzk', admin: true };
    document.getElementById('catalog').appendChild(
      catalogCard({ id: 'chess', name: '国际象棋', desc: '经典双人对战 · 在线房间', path: '/games/chess' },
        { openRooms: 2, wins: 3, losses: 1, draws: 0, total: 4 }));
    return;
  }
  await boot({ active: '/games' });
  if (!shell.user) return;
  const host = document.getElementById('catalog');
  try {
    const { games } = await apiGet('/api/games');
    const list = (games || []).filter((g) => g.enabled);
    if (!list.length) { host.appendChild(h('div', 'card', '暂时还没有可玩的游戏')); return; }
    for (const g of list) {
      let status = null;
      if (g.id === 'chess') status = await chessStatus();
      host.appendChild(catalogCard(g, status));
    }
  } catch (e) {
    host.appendChild(h('div', 'card', '加载失败：' + e.message));
  }
})();
