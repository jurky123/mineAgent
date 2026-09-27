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

const SVG_NS = 'http://www.w3.org/2000/svg';

function svgEl(tag, attrs) {
  const el = document.createElementNS(SVG_NS, tag);
  for (const k in attrs) el.setAttribute(k, String(attrs[k]));
  return el;
}

function renderBoard() {
  const board = document.getElementById('board');
  board.innerHTML = '';
  const r = state.room;
  if (!r) return;
  const size = r.size || 15;
  const cells = r.cells || '';
  const win = new Set(r.winLine || []);
  const myTurn = r.status === 'playing' && r.turn === r.you;
  board.style.setProperty('--gomoku-size', size);

  // 网格用内联 SVG：viewBox 0..size，交叉点在 k+0.5；点位用百分比 (i+0.5)/size，两者严格一致。
  const grid = svgEl('svg', { class: 'gomoku-grid', viewBox: '0 0 ' + size + ' ' + size, preserveAspectRatio: 'none' });
  for (let i = 0; i < size; i++) {
    const p = i + 0.5;
    grid.appendChild(svgEl('line', { x1: p, y1: 0, x2: p, y2: size, 'vector-effect': 'non-scaling-stroke' }));
    grid.appendChild(svgEl('line', { x1: 0, y1: p, x2: size, y2: p, 'vector-effect': 'non-scaling-stroke' }));
  }
  if (size === 15) {
    for (const [rr, cc] of [[3, 3], [3, 11], [11, 3], [11, 11], [7, 7]]) {
      grid.appendChild(svgEl('circle', { cx: cc + 0.5, cy: rr + 0.5, r: 0.11, class: 'gstar' }));
    }
  }
  board.appendChild(grid);

  for (let i = 0; i < size * size; i++) {
    const row = Math.floor(i / size);
    const col = i % size;
    const pt = pointName(i);
    const btn = h('button', 'gpoint');
    btn.dataset.point = pt;
    btn.style.left = (((col + 0.5) / size) * 100) + '%';
    btn.style.top = (((row + 0.5) / size) * 100) + '%';
    const c = cells[i] || '.';
    if (c === 'b') btn.appendChild(h('span', 'stone b'));
    else if (c === 'w') btn.appendChild(h('span', 'stone w'));
    else if (myTurn) btn.appendChild(h('span', 'stone ghost ' + (r.you === 'black' ? 'b' : 'w')));
    if (r.lastMove === pt) btn.classList.add('last');
    if (win.has(i)) btn.classList.add('win');
    btn.addEventListener('click', () => place(i));
    board.appendChild(btn);
  }
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
