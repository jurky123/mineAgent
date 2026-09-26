// gomoku.js — 五子棋：大厅 → 房间（等待/对局/终局）；15 路棋盘 + 落子交互。
// 房间/大厅/SSE 等公共部分在 gameroom.js。
import { boot, apiPost } from './shell.js';
import { h, icon, toast, gameIcon, copyText } from './ds.js';
import * as room from './gameroom.js';

const qs = new URLSearchParams(location.search);
const DEMO = qs.get('ui') === '1';
const GAME = 'gomoku';

let state = { room: null };

const pointName = (i) => String.fromCharCode(97 + (i % 15)) + (Math.floor(i / 15) + 1);

function renderBoard() {
  const board = document.getElementById('board');
  board.innerHTML = '';
  const r = state.room;
  if (!r) return;
  const cells = r.cells || '';
  const size = r.size || 15;
  const win = new Set(r.winLine || []);
  for (let i = 0; i < size * size; i++) {
    const c = cells[i] || '.';
    const cell = h('button', 'gcell');
    cell.dataset.point = pointName(i);
    if (c === 'b' || c === 'w') cell.classList.add('has');
    if (r.lastMove === pointName(i)) cell.classList.add('last');
    if (win.has(i)) cell.classList.add('win');
    if (c === 'b') cell.appendChild(h('span', 'stone b'));
    else if (c === 'w') cell.appendChild(h('span', 'stone w'));
    cell.addEventListener('click', () => place(i));
    board.appendChild(cell);
  }
  board.style.setProperty('--gomoku-size', size);
}

async function place(i) {
  const r = state.room;
  if (!r || r.status !== 'playing' || r.turn !== r.you) return;
  const pt = pointName(i);
  if ((r.cells || '')[i] !== '.') { toast('这里已经有子了', { warn: true }); return; }
  try {
    const res = await apiPost('/api/games/' + GAME + '/move', { point: pt });
    apply(res.room);
  } catch (e) {
    toast(e.message, { warn: true });
  }
}

function renderStatus() {
  const el = document.getElementById('status');
  const r = state.room;
  el.innerHTML = '';
  if (!r) return;
  if (r.status === 'waiting') {
    el.className = 'status-bar';
    el.appendChild(h('span', null, '等待对手加入'));
    const share = h('button', 'btn sm');
    share.appendChild(icon('link'));
    share.appendChild(h('span', null, '复制邀请链接'));
    share.onclick = () => copyText(room.shareLink(r), '邀请链接已复制');
    el.appendChild(share);
    return;
  }
  if (r.status === 'playing') {
    const mine = r.turn === r.you;
    el.className = 'status-bar' + (mine ? ' active' : '');
    el.appendChild(h('span', null, mine ? '你的回合（' + room.SIDE_LABEL[r.you] + '）' : '等待对手落子'));
    return;
  }
  el.className = 'status-bar done';
  const why = room.reasonLabel(r.reason);
  if (r.result === 'draw') el.appendChild(h('span', null, '和棋（' + why + '）'));
  else {
    const winner = room.SIDE_LABEL[r.result] || r.result;
    el.appendChild(h('span', null, winner + '胜（' + why + '），' + (r.you === r.result ? '恭喜！' : '再接再厉')));
  }
}

function paint() {
  const r = state.room;
  const chip = document.getElementById('room-chip');
  const lobbyEl = document.getElementById('lobby');
  const roomEl = document.getElementById('room');
  if (r) {
    chip.hidden = false;
    chip.textContent = '房间 ' + r.id;
    chip.onclick = () => copyText(r.id, '房间码已复制');
    lobbyEl.hidden = true;
    roomEl.hidden = false;
    document.getElementById('top-player').replaceChildren(room.playerBar(r, r.you === 'white' ? 'black' : 'white'));
    document.getElementById('bottom-player').replaceChildren(room.playerBar(r, r.you === 'black' ? 'black' : 'white'));
    renderBoard();
    renderStatus();
    room.roomInfo(document.getElementById('room-info'), r);
    room.movesTable(document.getElementById('moves'), r.moves);
    room.actionButtons(document.getElementById('actions'), r, {
      onRoom: apply,
      onExit: () => { state.room = null; paint(); },
    });
  } else {
    chip.hidden = true;
    roomEl.hidden = true;
    lobbyEl.hidden = false;
    room.lobby(lobbyEl, { gameId: GAME, onRoom: apply, iconEl: gameIcon(GAME, 'lobby-piece') });
  }
}

function apply(r) {
  state.room = r;
  paint();
}

function demoRoom() {
  const cells = new Array(225).fill('.');
  cells[7 * 15 + 7] = 'b'; // h8
  cells[7 * 15 + 8] = 'w'; // i8
  cells[8 * 15 + 7] = 'b'; // h9
  return {
    id: 'GM5K2Q', game: 'gomoku', status: 'playing', result: '', reason: '',
    turn: 'black', size: 15, cells: cells.join(''), lastMove: 'h9', you: 'black',
    players: { black: { id: 1, name: 'jzk' }, white: { id: 2, name: '朋友' } },
    moves: ['h8', 'i8', 'h9'],
  };
}

(async () => {
  document.getElementById('room-chip').hidden = true;
  if (DEMO) { apply(demoRoom()); return; }
  const user = await boot({ active: '/games' });
  if (!user) return;
  const invite = (qs.get('room') || '').trim().toUpperCase();
  const current = await room.loadRoom(GAME);
  if (current) { apply(current); }
  else if (invite) {
    try {
      const res = await room.joinRoom(GAME, invite);
      if (res.redirect && res.redirect !== '/games/' + GAME) { location.href = res.redirect; return; }
      apply(res.room);
      history.replaceState(null, '', '/games/' + GAME);
    } catch (e) {
      toast('加入 ' + invite + ' 失败：' + e.message, { warn: true });
      paint();
    }
  } else {
    paint();
  }
  room.connectRoomEvents({ gameId: GAME, onRoom: apply });
})();
