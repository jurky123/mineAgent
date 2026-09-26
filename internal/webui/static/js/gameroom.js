// gameroom.js — 棋类房间的公共部分（大厅 / 房间信息 / 着法 / SSE / 动作按钮）。
// 国际象棋与五子棋共用；各游戏只负责自己的棋盘渲染与走子交互。
import { shell, apiGet, apiPost } from './shell.js';
import { h, icon, toast, confirmDialog, copyText } from './ds.js';

export const SIDE_LABEL = { white: '白方', black: '黑方' };
export const REASON_LABEL = {
  checkmate: '将杀', stalemate: '逼和', resign: '认输', leave: '对手离开',
  five: '五连', full: '棋盘已满', timeout: '超时',
};

export function reasonLabel(r) { return REASON_LABEL[r] || r || ''; }

// ---------- API ----------
export async function createRoom(gameId) {
  const res = await apiPost('/api/games/' + gameId + '/rooms');
  return res.room;
}
export async function joinRoom(gameId, code) {
  const res = await apiPost('/api/games/' + gameId + '/rooms/join', { room: (code || '').trim().toUpperCase() });
  return res;
}
export async function leaveRoom(gameId) {
  await apiPost('/api/games/' + gameId + '/leave');
}
export async function resignRoom(gameId) {
  const res = await apiPost('/api/games/' + gameId + '/resign');
  return res.room || null;
}
export async function rematch(gameId) {
  await leaveRoom(gameId);
  return createRoom(gameId);
}
export async function loadRoom(gameId) {
  const { room } = await apiGet('/api/games/' + gameId + '/room');
  return room && room.game === gameId ? room : null;
}
export function shareLink(room) {
  return location.origin + '/games/' + (room ? room.game : '') + '?room=' + (room ? room.id : '');
}

// SSE：房间状态变化（EventSource 带不了 header，用 ?token=）
export function connectRoomEvents({ gameId, onRoom }) {
  const es = new EventSource('/api/games/events?token=' + encodeURIComponent(shell.token));
  let last = Date.now();
  es.addEventListener('room', (ev) => {
    last = Date.now();
    try {
      const data = JSON.parse(ev.data);
      if (!data || !data.room) return;
      if (gameId && data.room.game !== gameId) return;
      onRoom(data.room);
    } catch (e) { /* 忽略坏帧 */ }
  });
  es.onerror = () => {
    setTimeout(async () => {
      if (Date.now() - last < 5000) return;
      try {
        const room = await loadRoom(gameId);
        if (room) onRoom(room);
      } catch (e) { /* 未登录 */ }
    }, 5200);
  };
}

// ---------- UI 片段 ----------
export function playerBar(room, side) {
  const p = room.players && room.players[side];
  const you = room.you === side;
  const turn = room.turn === side && room.status === 'playing';
  const line = h('div', 'player-line' + (turn ? ' turn' : ''));
  line.appendChild(h('span', 'dot ' + side[0]));
  line.appendChild(h('span', 'player-name', p ? p.name + (you ? '（你）' : '') : '等待加入…'));
  if (turn && room.inCheck) line.appendChild(h('span', 'badge warn', '被将军'));
  else if (turn && you) line.appendChild(h('span', 'badge brand', '你的回合'));
  else if (turn && !you) line.appendChild(h('span', 'badge', '正在思考'));
  if (room.status === 'finished' && room.result === side) line.appendChild(h('span', 'badge brand', '胜'));
  return line;
}

// 房间信息卡：房间码（点击复制）、双方、邀请链接
export function roomInfo(el, room) {
  el.innerHTML = '';
  el.appendChild(h('div', 'side-head', '房间'));
  const code = h('button', 'room-code');
  code.appendChild(h('span', null, room.id));
  code.appendChild(icon('copy', 'sm'));
  code.title = '点击复制房间码';
  code.onclick = () => copyText(room.id, '房间码已复制');
  el.appendChild(code);
  const players = h('div', 'side-players');
  for (const side of ['white', 'black']) {
    const p = room.players && room.players[side];
    const line = h('div', 'side-player');
    line.appendChild(h('span', 'dot ' + side[0]));
    line.appendChild(h('span', null, (p ? p.name : '等待加入…') + (room.you === side ? '（你）' : '')));
    if (room.you === side) line.appendChild(h('span', 'badge brand', '你'));
    players.appendChild(line);
  }
  el.appendChild(players);
  const copy = h('button', 'btn sm ghost');
  copy.appendChild(icon('link'));
  copy.appendChild(h('span', null, '复制邀请链接'));
  copy.onclick = () => copyText(shareLink(room), '邀请链接已复制');
  el.appendChild(copy);
  el.appendChild(h('div', 'side-hint', '同一房间只允许两名玩家；刷新/断开重连不丢对局。'));
}

// 着法表（两列，当前着高亮）
export function movesTable(el, moves) {
  el.innerHTML = '';
  const list = moves || [];
  if (!list.length) { el.appendChild(h('div', 'empty', '暂无')); return; }
  const table = h('div', 'moves-grid');
  for (let i = 0; i < list.length; i += 2) {
    table.appendChild(h('span', 'mv-no', (i / 2 + 1) + '.'));
    table.appendChild(h('span', 'mv' + (i === list.length - 1 ? ' last' : ''), list[i]));
    table.appendChild(h('span', 'mv' + (i + 1 === list.length - 1 ? ' last' : ''), list[i + 1] || ''));
  }
  el.appendChild(table);
  const lastEl = table.querySelector('.mv.last');
  if (lastEl) lastEl.scrollIntoView({ block: 'nearest' });
}

// 动作按钮：认输 / 离开 / 再来一局（onRoom 回调更新局面，返回大厅用 onExit）
export function actionButtons(el, room, { onRoom, onExit }) {
  el.innerHTML = '';
  if (room.status === 'playing') {
    const resign = h('button', 'btn danger sm');
    resign.appendChild(icon('flag'));
    resign.appendChild(h('span', null, '认输'));
    resign.onclick = async () => {
      const ok = await confirmDialog({ title: '确定认输？', body: '本局将判对手胜。', confirmText: '认输', danger: true });
      if (!ok) return;
      const next = await resignRoom(room.game);
      if (next) onRoom(next); else onExit();
    };
    el.appendChild(resign);
  }
  if (room.status === 'finished') {
    const again = h('button', 'btn primary cta');
    again.appendChild(icon('restart'));
    again.appendChild(h('span', null, '再来一局'));
    again.onclick = async () => { onRoom(await rematch(room.game)); };
    el.appendChild(again);
  }
  if (room.status === 'waiting' || room.status === 'finished') {
    const leave = h('button', 'btn sm');
    leave.appendChild(icon('logout'));
    leave.appendChild(h('span', null, '离开房间'));
    leave.onclick = async () => {
      if (room.status === 'waiting') {
        await leaveRoom(room.game);
        onExit();
        return;
      }
      const ok = await confirmDialog({ title: '离开房间？', body: '对局中的离开会计为认输。', confirmText: '离开', danger: true });
      if (!ok) return;
      await leaveRoom(room.game);
      onExit();
    };
    el.appendChild(leave);
  }
}

// 大厅：创建房间 / 输码加入 / 开放房间列表
export function lobby(el, { gameId, onRoom, iconEl }) {
  el.innerHTML = '';
  const hero = h('div', 'lobby-hero');
  const tile = h('span', 'lobby-icon');
  tile.appendChild(iconEl || icon('gamepad', 'lg'));
  hero.appendChild(tile);
  const txt = h('div');
  txt.appendChild(h('div', 'lobby-title', '在线对弈'));
  txt.appendChild(h('div', 'lobby-sub', '房间制双人对战 · 服务端裁判 · 刷新不丢对局'));
  hero.appendChild(txt);
  el.appendChild(hero);

  const grid = h('div', 'lobby-grid');
  const createCard = h('article', 'card lobby-card');
  createCard.appendChild(h('div', 'lobby-card-title', '创建一个新房间'));
  createCard.appendChild(h('div', 'lobby-card-sub', '创建后把房间码或邀请链接发给朋友'));
  const create = h('button', 'btn primary cta', '创建房间');
  create.onclick = async () => {
    try { onRoom(await createRoom(gameId)); } catch (e) { toast(e.message, { warn: true }); }
  };
  createCard.appendChild(create);
  grid.appendChild(createCard);

  const joinCard = h('article', 'card lobby-card');
  joinCard.appendChild(h('div', 'lobby-card-title', '加入朋友的房间'));
  joinCard.appendChild(h('div', 'lobby-card-sub', '输入 6 位房间码'));
  const row = h('div', 'join-row');
  const input = h('input', 'input code-input');
  input.maxLength = 6;
  input.placeholder = 'AB3K9Q';
  input.autocapitalize = 'characters';
  input.spellcheck = false;
  const join = h('button', 'btn cta', '加入');
  const doJoin = async () => {
    const code = input.value.trim().toUpperCase();
    if (!code) { toast('先填房间码', { warn: true }); return; }
    try {
      const res = await joinRoom(gameId, code);
      if (res.redirect && res.redirect !== '/games/' + gameId) { location.href = res.redirect; return; }
      onRoom(res.room);
    } catch (e) { toast(e.message, { warn: true }); }
  };
  join.onclick = doJoin;
  input.addEventListener('keydown', (e) => { if (e.key === 'Enter') doJoin(); });
  row.appendChild(input);
  row.appendChild(join);
  joinCard.appendChild(row);
  grid.appendChild(joinCard);
  el.appendChild(grid);

  const sec = h('section', 'section');
  const head = h('div', 'section-head');
  head.appendChild(h('h2', 'section-title', '开放房间'));
  head.appendChild(h('div', 'spacer'));
  const refresh = h('button', 'btn sm ghost', '刷新');
  head.appendChild(refresh);
  sec.appendChild(head);
  const list = h('div', 'card');
  sec.appendChild(list);
  el.appendChild(sec);

  const loadRooms = async () => {
    try {
      const { rooms } = await apiGet('/api/games/' + gameId + '/rooms');
      list.innerHTML = '';
      if (!rooms.length) { list.appendChild(h('div', 'empty', '还没有等待中的房间，创建一间等人来吧')); return; }
      for (const r of rooms) {
        const item = h('div', 'list-row');
        item.appendChild(h('span', 'avatar', (r.host[0] || '?')));
        const main = h('div', 'list-main');
        main.appendChild(h('div', 'list-title', r.host + ' 的房间'));
        main.appendChild(h('div', 'list-sub', '房间码 ' + r.id + ' · ' + fmtAgo(r.createdAt)));
        item.appendChild(main);
        const b = h('button', 'btn sm', '加入');
        b.onclick = async () => {
          try { onRoom((await joinRoom(gameId, r.id)).room); } catch (e) { toast(e.message, { warn: true }); }
        };
        item.appendChild(b);
        list.appendChild(item);
      }
    } catch (e) {
      list.innerHTML = '';
      list.appendChild(h('div', 'empty', '加载失败：' + e.message));
    }
  };
  refresh.onclick = loadRooms;
  loadRooms();
}

function fmtAgo(ms) {
  if (!ms) return '刚刚';
  const s = Math.max(0, Math.floor((Date.now() - ms) / 1000));
  if (s < 60) return '刚刚';
  if (s < 3600) return Math.floor(s / 60) + ' 分钟前';
  return Math.floor(s / 3600) + ' 小时前';
}
