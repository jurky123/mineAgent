// chess.js — 国际象棋：大厅 → 房间（等待/对局/终局）；棋盘渲染 + 走子交互。
// 房间/大厅/SSE 等公共部分在 gameroom.js。
import { boot, apiGet, apiPost } from './shell.js';
import { h, icon, toast, gameIcon, copyText } from './ds.js';
import * as room from './gameroom.js';

const qs = new URLSearchParams(location.search);
const DEMO = qs.get('ui') === '1' || qs.get('ui') === 'motion';
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
        const img = h('img', 'pc ' + (p === p.toUpperCase() ? 'pc-w' : 'pc-b'));
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
  if (state.moving) return;   // 上一次还没回，别重复落子
  state.moving = true;
  setBoardPending(true);
  state.selected = null;
  try {
    const res = await apiPost('/api/games/' + GAME + '/move', {
      from: move.slice(0, 2), to: move.slice(2, 4), promotion: move[4] || '',
    });
    apply(res.room);
  } catch (e) {
    toast(e.message, { warn: true });
    renderBoard();
  } finally {
    state.moving = false;
    setBoardPending(false);
  }
}

// setBoardPending 在棋盘角落显示"提交中"小 spinner（走子没回来之前）
function setBoardPending(on) {
  const col = document.querySelector('.board-col');
  if (col) col.classList.toggle('board-pending', !!on);
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
  el.appendChild(h('span', null, '本局结束'));   // 结果细节交给棋盘上的浮层
}

let demoDanmakuShown = '';

// 预览模式下把最新一条解说用弹幕演示出来（只飘一次）
function demoDanmaku(r) {
  if (!DEMO || !r || !r.comments) return;
  const keys = Object.keys(r.comments).map(Number).sort((a, b) => a - b);
  if (!keys.length) return;
  const lastKey = keys[keys.length - 1];
  const key = (r.id || '') + ':' + lastKey;
  if (demoDanmakuShown === key) return;
  demoDanmakuShown = key;
  setTimeout(() => room.danmaku(r.comments[lastKey]), 400);
}

function paint() {
  const r = state.room;
  demoDanmaku(r);
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
    room.lobby(lobbyEl, {
      gameId: GAME, onRoom: apply, iconEl: gameIcon(GAME, 'lobby-piece'),
      demoRooms: DEMO ? () => [{ id: 'AB3K9Q', host: '朋友的房间', createdAt: Date.now() - 132000 }] : undefined,
      demoCreate: DEMO ? demoWaiting : undefined,
      demoJoin: DEMO ? demoAfterE4E5 : undefined,
    });
  }
}

function apply(r) {
  state.room = r;
  state.selected = null;
  paint();
}

// ---------- UI 预览模式（?ui=1&state=… / ?ui=motion）----------
// 状态化假数据，供截图/录屏评审用；?ui=motion 会按时间线自动演示一遍。
const UI_STATE = (qs.get('state') || 'lobby').toLowerCase();
const UI_MOTION = qs.get('ui') === 'motion';
const START_PIECES = 'RNBQKBNRPPPPPPPP' + '.'.repeat(32) + 'pppppppprnbqkbnr';
const OPENING_LEGAL = ['b1a3','b1c3','g1f3','g1h3','a2a3','b2b3','c2c3','d2d3','e2e3','f2f3','g2g3','h2h3','a2a4','b2b4','c2c4','d2d4','e2e4','f2f4','g2g4','h2h4'];

// demoUci 在假数据里重演 UCI（与 history.js 的 applyUci 同逻辑）
function demoUci(pieces, uci) {
  const arr = pieces.split('');
  const from = (uci.charCodeAt(1) - 49) * 8 + (uci.charCodeAt(0) - 97);
  const to = (uci.charCodeAt(3) - 49) * 8 + (uci.charCodeAt(2) - 97);
  const p = arr[from];
  if (!p || p === '.') return pieces;
  arr[from] = '.';
  let piece = p;
  if (uci.length === 5) piece = p === p.toUpperCase() ? uci[4].toUpperCase() : uci[4];
  arr[to] = piece;
  if ((p === 'K' || p === 'k') && Math.abs((to % 8) - (from % 8)) === 2) {
    const rank = Math.floor(from / 8);
    if (to % 8 === 6) { arr[rank * 8 + 5] = arr[rank * 8 + 7]; arr[rank * 8 + 7] = '.'; }
    else { arr[rank * 8 + 3] = arr[rank * 8 + 0]; arr[rank * 8 + 0] = '.'; }
  }
  return arr.join('');
}
function demoPieces(moves) {
  let p = START_PIECES;
  for (const m of moves) p = demoUci(p, m);
  return p;
}

const DEMO_PLAYERS = { white: { id: 1, name: 'jzk' }, black: { id: 2, name: '朋友' } };
function demoRoom(over) {
  return Object.assign({
    id: 'AB3K9Q', game: 'chess', status: 'playing', result: '', reason: '',
    turn: 'white', inCheck: false, pieces: START_PIECES, lastMove: '',
    you: 'white', players: DEMO_PLAYERS, legalMoves: OPENING_LEGAL, moves: [], first: 'white',
  }, over || {});
}
function demoWaiting() {
  return demoRoom({ status: 'waiting', players: { white: { id: 1, name: 'jzk' }, black: null }, moves: [] });
}
function demoAfterE4() {
  return demoRoom({ turn: 'black', pieces: demoPieces(['e2e4']), lastMove: 'e2e4', moves: ['e4'],
    legalMoves: ['e7e5','e7e6','c7c5','g8f6','b8c6','d7d5'] });
}
function demoAfterE4E5() {
  return demoRoom({ turn: 'white', pieces: demoPieces(['e2e4','e7e5']), lastMove: 'e7e5', moves: ['e4','e5'],
    legalMoves: ['g1f3','f1c4','d2d4','b1c3'],
    comments: { 1: '抢下中心，最经典的起手。', 2: '对称回应，双方进入正常开局。' } });
}
function demoAfterNf3() {
  return demoRoom({ turn: 'black', pieces: demoPieces(['e2e4','e7e5','g1f3']), lastMove: 'g1f3',
    moves: ['e4','e5','Nf3'], legalMoves: ['b8c6','d7d6','g8f6'] });
}
function demoCheck() {
  const rows = ['rnb1kbnr', 'pppp1ppp', '........', '....p...', '.......q', '.....P..', 'PPPPP.PP', 'RNBQKBNR'];
  return demoRoom({ pieces: rows.join(''), inCheck: true, turn: 'white', lastMove: 'd8h4',
    moves: ['f3','e5','g4','Qh4+'], legalMoves: ['g2g3', 'e1f2', 'g2g4'] });
}
function demoUndoRequest() {
  return Object.assign(demoAfterE4E5(), { undoReq: { by: 'black', at: Date.now() } });
}
function demoSwapRequest() {
  return Object.assign(demoAfterE4E5(), { swapReq: { by: 'black', at: Date.now() } });
}
function demoDrawRequest() {
  return Object.assign(demoAfterE4E5(), { drawReq: { by: 'black', at: Date.now() } });
}
function demoDraw() {
  return demoRoom({
    status: 'finished', result: 'draw', reason: 'agreement',
    pieces: demoPieces(['e2e4','e7e5','g1f3','b8c6','f1b5','a7a6']),
    lastMove: 'a7a6', moves: ['e4','e5','Nf3','Nc6','Bb5','a6'],
  });
}
function demoFinished(youWin) {
  const rows = ['......k.', '.....ppp', '........', '........', '........', '........', '........', 'R.....K.'];
  return demoRoom({
    status: 'finished', result: youWin ? 'white' : 'black', reason: 'checkmate',
    pieces: youWin ? rows.join('') : demoPieces(['f2f3','e7e5','g2g4','d8h4']),
    lastMove: youWin ? 'a1a8' : 'd8h4',
    moves: youWin ? ['Ra8#'] : ['f3','e5','g4','Qh4#'],
    legalMoves: undefined,
  });
}
const DEMO_STATES = {
  lobby: () => null,
  waiting: demoWaiting,
  playing: () => demoAfterE4E5(),
  check: demoCheck,
  'finished-win': () => demoFinished(true),
  'finished-lose': () => demoFinished(false),
  'undo-request': demoUndoRequest,
  'swap-request': demoSwapRequest,
  'draw-request': demoDrawRequest,
  'finished-draw': demoDraw,
};

// motion：按时间线自动演示一遍（给录屏用，12 秒左右）
function runMotion() {
  const timeline = [
    [0, demoWaiting()],
    [1400, demoRoom({ turn: 'white', pieces: START_PIECES, moves: [] })],
    [3000, demoAfterE4()],
    [4200, demoAfterE4E5()],
    [5800, demoAfterNf3()],
    [7200, demoUndoRequest()],
    [9400, demoFinished(true)],
  ];
  for (const [at, r] of timeline) {
    setTimeout(() => {
      document.querySelectorAll('.dialog-mask').forEach((el) => el.remove());
      apply(r);
    }, at);
  }
}

(async () => {
  document.getElementById('room-chip').hidden = true;
  if (DEMO) {
    await boot({ active: '/games', preview: true });   // 预览也要有顶栏（同一套 Shell）
    if (UI_MOTION) { apply(null); runMotion(); return; }
    const build = DEMO_STATES[UI_STATE] || DEMO_STATES.playing;
    apply(build());
    return;
  }
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
  room.connectRoomEvents({
    gameId: GAME,
    onRoom: apply,
    onComment: (ply, text) => {
      if (state.room) {
        state.room.comments = Object.assign({}, state.room.comments, { [ply]: text });
      }
      room.danmaku(text);
    },
  });
  // 解说异步返回：合并到当前房间再重绘（请求期间可能已经换过 snapshot 对象）
  document.addEventListener('mine:comment', (e) => {
    const d = e.detail || {};
    if (state.room && d.ply) {
      state.room.comments = Object.assign({}, state.room.comments, { [d.ply]: d.text });
    }
    if (!d.silent) room.danmaku(d.text);   // 本地拿到的解说也飘一条
  });
})();
