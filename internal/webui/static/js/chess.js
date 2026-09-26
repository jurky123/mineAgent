// chess.js — 国际象棋：大厅 → 房间（等待/对局/终局）；棋盘渲染 + 走子交互。
// 房间/大厅/SSE 等公共部分在 gameroom.js。
import { boot, apiGet, apiPost } from './shell.js';
import { h, icon, toast, gameIcon, copyText } from './ds.js';
import * as room from './gameroom.js';

const qs = new URLSearchParams(location.search);
const DEMO = qs.get('ui') === '1';
const GAME = 'chess';

let state = { room: null, selected: null };

const squareName = (sq) => String.fromCharCode(97 + (sq % 8)) + String(1 + Math.floor(sq / 8));

// 棋盘上的棋子必须用真实颜色（白色用白子、黑色用黑子），不能用主题感知的图标图。
function pieceSrc(p) {
  const ver = (document.querySelector('meta[name=mineagent-version]') || {}).content || '';
  return '/static/' + ver + '/img/pieces/' + (p === p.toUpperCase() ? 'w' : 'b') + p.toUpperCase() + '.svg';
}
function legalFrom(sq) {
  const name = squareName(sq);
  return ((state.room && state.room.legalMoves) || []).filter((m) => m.slice(0, 2) === name);
}

// ---------- 棋盘 ----------
function renderBoard() {
  const board = document.getElementById('board');
  board.innerHTML = '';
  const r = state.room;
  if (!r) return;
  const flip = r.you === 'black';
  const pieces = r.pieces || '';
  const last = r.lastMove || '';
  const targets = state.selected != null ? legalFrom(state.selected).map((m) => m.slice(2, 4)) : [];
  const checkSq = (() => {
    if (!r.inCheck) return null;
    const want = r.turn === 'white' ? 'K' : 'k';
    const i = pieces.indexOf(want);
    return i >= 0 ? i : null;
  })();

  for (let row = 0; row < 8; row++) {
    for (let col = 0; col < 8; col++) {
      const rank = flip ? row : 7 - row;
      const file = flip ? 7 - col : col;
      const sq = rank * 8 + file;
      const name = squareName(sq);
      const p = pieces[sq] && pieces[sq] !== '.' ? pieces[sq] : '';
      const cell = h('div', 'sq ' + ((rank + file) % 2 ? 'dark' : 'light'));
      cell.dataset.sq = name;
      if (last.length >= 4 && (name === last.slice(0, 2) || name === last.slice(2, 4))) cell.classList.add('last');
      if (targets.includes(name)) cell.classList.add('target');
      if (state.selected === sq) cell.classList.add('sel');
      if (checkSq === sq) cell.classList.add('check');
      if (col === 0) cell.appendChild(h('span', 'coord rank', String(rank + 1)));
      if (row === 7) cell.appendChild(h('span', 'coord file', name[0]));
      if (p) {
        const img = h('img', 'pc');
        img.src = pieceSrc(p);
        img.alt = p;
        img.draggable = false;
        cell.appendChild(img);
      }
      cell.onclick = () => onSquare(sq);
      board.appendChild(cell);
    }
  }
}

function onSquare(sq) {
  const r = state.room;
  if (!r || r.status !== 'playing' || !r.legalMoves) return;
  const name = squareName(sq);
  if (state.selected != null) {
    const hit = legalFrom(state.selected).find((m) => m.slice(2, 4) === name);
    if (hit) { sendMove(hit); return; }
  }
  const p = (r.pieces || '')[sq];
  const mine = r.you === 'white' ? (p && p === p.toUpperCase()) : (p && p !== p.toUpperCase() && p !== '.');
  state.selected = mine ? sq : null;
  renderBoard();
}

async function sendMove(move) {
  state.selected = null;
  try {
    const res = await apiPost('/api/games/' + GAME + '/move', {
      from: move.slice(0, 2), to: move.slice(2, 4), promotion: move[4] || '',
    });
    apply(res.room);
  } catch (e) {
    toast(e.message, { warn: true });
    renderBoard();
  }
}

// ---------- 状态栏 ----------
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
    el.appendChild(h('span', null, mine ? '你的回合' : '等待对手走棋'));
    if (r.inCheck) el.appendChild(h('span', 'badge warn', '被将军'));
    return;
  }
  el.className = 'status-bar done';
  const why = room.reasonLabel(r.reason);
  if (r.result === 'draw') el.appendChild(h('span', null, '和棋（' + why + '）'));
  else {
    const winner = r.result === 'white' ? '白方' : '黑方';
    el.appendChild(h('span', null, winner + '胜（' + why + '）' + (r.you === r.result ? '，恭喜！' : '')));
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
    document.getElementById('top-player').replaceChildren(room.playerBar(r, r.you === 'black' ? 'white' : 'black'));
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
  state.selected = null;
  paint();
}

// ---------- demo ----------
function demoRoom() {
  const rows = ['rnbqkbnr', 'pppppppp', '........', '........', '....P...', '........', 'PPPP.PPP', 'RNBQKBNR'];
  return {
    id: 'AB3K9Q', game: 'chess', status: 'playing', result: '', reason: '', turn: 'white', inCheck: false,
    pieces: rows.join(''), lastMove: 'e2e4', you: 'white',
    players: { white: { id: 1, name: 'jzk' }, black: { id: 2, name: '朋友' } },
    legalMoves: ['g1f3', 'f1c4', 'd2d4', 'e4e5', 'b1c3'],
    moves: ['e2e4'],
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
