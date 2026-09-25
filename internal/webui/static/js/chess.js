// chess.js — 国际象棋：大厅（没房间）→ 房间（等待 / 对局 / 终局），
// 服务端权威 + SSE 同步。棋盘用 SVG 棋子（/static/<ver>/img/pieces/*.svg）。
import { shell, boot, apiGet, apiPost } from './shell.js';
import { h, icon, toast, confirmDialog, copyText, pieceImg } from './ds.js';

const qs = new URLSearchParams(location.search);
const DEMO = qs.get('ui') === '1';
const VER = (document.querySelector('meta[name=mineagent-version]') || {}).content || '';
const PIECE_SRC = (p) => '/static/' + VER + '/img/pieces/' + (p === p.toUpperCase() ? 'w' : 'b') + p.toUpperCase() + '.svg';

let room = null;
let selected = null;

function squareName(sq) {
  return String.fromCharCode(97 + (sq % 8)) + String(1 + Math.floor(sq / 8));
}
function legalFrom(sq) {
  const name = squareName(sq);
  return ((room && room.legalMoves) || []).filter((m) => m.slice(0, 2) === name);
}

// ---------- 棋盘 ----------
function renderBoard() {
  const board = document.getElementById('board');
  board.innerHTML = '';
  if (!room) return;
  const flip = room.you === 'black';
  const pieces = room.pieces || '';
  const last = room.lastMove || '';
  const targets = selected != null ? legalFrom(selected).map((m) => m.slice(2, 4)) : [];
  const checkSq = (() => {
    if (!room.inCheck) return null;
    const want = room.turn === 'white' ? 'K' : 'k';
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
      if (selected === sq) cell.classList.add('sel');
      if (checkSq === sq) cell.classList.add('check');
      // 边线坐标：左列显示横排号，底行显示纵线字母
      if (col === 0) cell.appendChild(h('span', 'coord rank', String(rank + 1)));
      if (row === 7) cell.appendChild(h('span', 'coord file', name[0]));
      if (p) {
        const img = h('img', 'pc');
        img.src = PIECE_SRC(p);
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
  if (!room || room.status !== 'playing' || !room.legalMoves) return;
  const name = squareName(sq);
  if (selected != null) {
    const hit = legalFrom(selected).find((m) => m.slice(2, 4) === name);
    if (hit) { sendMove(hit); return; }
  }
  const p = (room.pieces || '')[sq];
  const mine = room.you === 'white' ? (p && p === p.toUpperCase()) : (p && p !== p.toUpperCase() && p !== '.');
  selected = mine ? sq : null;
  renderBoard();
}

async function sendMove(move) {
  selected = null;
  try {
    const res = await apiPost('/api/games/chess/move', { from: move.slice(0, 2), to: move.slice(2, 4), promotion: move[4] || '' });
    apply(res.room);
  } catch (e) {
    toast(e.message, { warn: true });
    renderBoard();
  }
}

// ---------- 对局信息 ----------
function playerLine(color) {
  const p = room.players && room.players[color];
  const you = room.you === color;
  const turn = room.turn === color && room.status === 'playing';
  const line = h('div', 'player-line' + (turn ? ' turn' : ''));
  line.appendChild(h('span', 'dot ' + color[0]));
  line.appendChild(h('span', 'player-name', p ? p.name + (you ? '（你）' : '') : '等待加入…'));
  if (turn && room.inCheck) line.appendChild(h('span', 'badge warn', '被将军'));
  else if (turn && room.you) line.appendChild(h('span', 'badge brand', '你的回合'));
  else if (turn && !room.you) line.appendChild(h('span', 'badge', '正在思考'));
  if (room.status === 'finished' && room.result === color) line.appendChild(h('span', 'badge brand', '胜'));
  return line;
}

function renderInfo() {
  const top = document.getElementById('top-player');
  const bottom = document.getElementById('bottom-player');
  const status = document.getElementById('status');
  const actions = document.getElementById('actions');
  top.innerHTML = ''; bottom.innerHTML = ''; status.innerHTML = ''; actions.innerHTML = '';

  const flip = room.you === 'black';
  top.appendChild(playerLine(flip ? 'white' : 'black'));
  bottom.appendChild(playerLine(flip ? 'black' : 'white'));

  const mine = room.status === 'playing' && room.turn === room.you;
  if (room.status === 'waiting') {
    status.appendChild(h('span', null, '等待对手加入'));
    const share = h('button', 'btn sm');
    share.appendChild(icon('link'));
    share.appendChild(h('span', null, '复制邀请链接'));
    share.onclick = () => copyText(shareLink(), '邀请链接已复制');
    status.appendChild(share);
  } else if (room.status === 'playing') {
    status.className = 'status-bar' + (mine ? ' active' : '');
    status.appendChild(h('span', null, mine ? '你的回合' : '等待对手走棋'));
    if (room.inCheck) status.appendChild(h('span', 'badge warn', '被将军'));
  } else {
    status.className = 'status-bar done';
    const reason = { checkmate: '将杀', stalemate: '逼和', resign: '认输', leave: '对手离开' }[room.reason] || room.reason || '';
    if (room.result === 'draw') status.appendChild(h('span', null, '和棋（' + reason + '）'));
    else {
      const win = room.you === room.result;
      status.appendChild(h('span', null, (room.result === 'white' ? '白方' : '黑方') + '胜（' + reason + '）' + (win ? '，恭喜！' : '')));
    }
  }

  if (room.status === 'playing') {
    const resign = h('button', 'btn danger sm');
    resign.appendChild(icon('flag'));
    resign.appendChild(h('span', null, '认输'));
    resign.onclick = async () => {
      const ok = await confirmDialog({ title: '确定认输？', body: '本局将判对手胜。', confirmText: '认输', danger: true });
      if (!ok) return;
      const res = await apiPost('/api/games/chess/resign');
      if (res.room) apply(res.room);
    };
    actions.appendChild(resign);
  }
  if (room.status === 'waiting' || room.status === 'finished') {
    if (room.status === 'finished') {
      const again = h('button', 'btn primary cta');
      again.appendChild(icon('restart'));
      again.appendChild(h('span', null, '再来一局'));
      again.onclick = async () => {
        await apiPost('/api/games/chess/leave');
        const res = await apiPost('/api/games/chess/rooms');
        apply(res.room);
      };
      actions.appendChild(again);
    }
    const leave = h('button', 'btn sm');
    leave.appendChild(icon('logout'));
    leave.appendChild(h('span', null, '离开房间'));
    leave.onclick = () => leaveRoom();
    actions.appendChild(leave);
  }
}

function shareLink() {
  return location.origin + '/games/chess?room=' + (room ? room.id : '');
}

function renderRoomInfo() {
  const box = document.getElementById('room-info');
  box.innerHTML = '';
  if (!room) return;
  const head = h('div', 'side-head', '房间');
  box.appendChild(head);
  const codeRow = h('div', 'code-row');
  const chip = h('button', 'room-code');
  chip.appendChild(h('span', null, room.id));
  chip.appendChild(icon('copy', 'sm'));
  chip.title = '点击复制房间码';
  chip.onclick = () => copyText(room.id, '房间码已复制');
  codeRow.appendChild(chip);
  box.appendChild(codeRow);
  const players = h('div', 'side-players');
  for (const color of ['white', 'black']) {
    const p = room.players && room.players[color];
    const line = h('div', 'side-player');
    line.appendChild(h('span', 'dot ' + color[0]));
    line.appendChild(h('span', null, p ? p.name + (room.you === color ? '（你）' : '') : '等待加入…'));
    if (room.you === color) line.appendChild(h('span', 'badge brand', '你'));
    players.appendChild(line);
  }
  box.appendChild(players);
  const copy = h('button', 'btn sm ghost');
  copy.appendChild(icon('link'));
  copy.appendChild(h('span', null, '复制邀请链接'));
  copy.onclick = () => copyText(shareLink(), '邀请链接已复制');
  box.appendChild(copy);
  box.appendChild(h('div', 'side-hint', '同一房间只允许两名玩家；刷新/断开重连不丢对局。'));
}

function renderMoves() {
  const box = document.getElementById('moves');
  box.innerHTML = '';
  const list = (room && room.moves) || [];
  if (!list.length) { box.appendChild(h('div', 'empty', '暂无')); return; }
  const table = h('div', 'moves-grid');
  for (let i = 0; i < list.length; i += 2) {
    const no = i / 2 + 1;
    table.appendChild(h('span', 'mv-no', no + '.'));
    table.appendChild(h('span', 'mv' + (i === list.length - 1 ? ' last' : ''), list[i]));
    table.appendChild(h('span', 'mv' + (i + 1 === list.length - 1 ? ' last' : ''), list[i + 1] || ''));
  }
  box.appendChild(table);
  const last = table.querySelector('.mv.last');
  if (last) last.scrollIntoView({ block: 'nearest' });
}

function paint() {
  const chip = document.getElementById('room-chip');
  if (room) {
    chip.hidden = false;
    chip.textContent = '房间 ' + room.id;
    chip.onclick = () => copyText(room.id, '房间码已复制');
    document.getElementById('lobby').hidden = true;
    document.getElementById('room').hidden = false;
    renderBoard();
    renderInfo();
    renderRoomInfo();
    renderMoves();
  } else {
    chip.hidden = true;
    document.getElementById('room').hidden = true;
    document.getElementById('lobby').hidden = false;
    renderLobby();
  }
}

function apply(r) {
  room = r;
  selected = null;
  paint();
}

// ---------- 大厅 ----------
function renderLobby() {
  if (DEMO) return;
  loadRooms();
}

async function loadRooms() {
  const box = document.getElementById('rooms');
  try {
    const { rooms } = await apiGet('/api/games/chess/rooms');
    box.innerHTML = '';
    if (!rooms.length) { box.appendChild(h('div', 'empty', '还没有等待中的房间，创建一间等人来吧')); return; }
    for (const r of rooms) {
      const row = h('div', 'list-row');
      const avatar = h('span', 'avatar', (r.host[0] || '?'));
      row.appendChild(avatar);
      const main = h('div', 'list-main');
      main.appendChild(h('div', 'list-title', r.host + ' 的房间'));
      main.appendChild(h('div', 'list-sub', '房间码 ' + r.id + ' · ' + fmtAgo(r.createdAt)));
      row.appendChild(main);
      const join = h('button', 'btn sm', '加入');
      join.onclick = async () => {
        try {
          const res = await apiPost('/api/games/chess/rooms/join', { room: r.id });
          apply(res.room);
        } catch (e) { toast(e.message, { warn: true }); }
      };
      row.appendChild(join);
      box.appendChild(row);
    }
  } catch (e) {
    box.innerHTML = '';
    box.appendChild(h('div', 'empty', '加载失败：' + e.message));
  }
}

function fmtAgo(ms) {
  if (!ms) return '刚刚';
  const s = Math.max(0, Math.floor((Date.now() - ms) / 1000));
  if (s < 60) return '刚刚';
  if (s < 3600) return Math.floor(s / 60) + ' 分钟前';
  return Math.floor(s / 3600) + ' 小时前';
}

async function createRoom() {
  try {
    const res = await apiPost('/api/games/chess/rooms');
    apply(res.room);
  } catch (e) { toast(e.message, { warn: true }); }
}

async function joinRoom(code) {
  code = (code || '').trim().toUpperCase();
  if (!code) { toast('先填房间码', { warn: true }); return; }
  try {
    const res = await apiPost('/api/games/chess/rooms/join', { room: code });
    apply(res.room);
  } catch (e) { toast(e.message, { warn: true }); }
}

async function leaveRoom() {
  const ok = await confirmDialog({ title: '离开房间？', body: '对局中的离开会计为认输。', confirmText: '离开', danger: true });
  if (!ok) return;
  await apiPost('/api/games/chess/leave');
  room = null; selected = null;
  paint();
}

// ---------- SSE ----------
function connectSSE() {
  if (DEMO) return;
  const es = new EventSource('/api/games/chess/events?token=' + encodeURIComponent(shell.token));
  es.addEventListener('room', (ev) => {
    try {
      const data = JSON.parse(ev.data);
      if (data && data.room) apply(data.room);
    } catch (e) { /* 忽略坏帧 */ }
  });
  es.onerror = () => {
    setTimeout(async () => {
      try {
        const { room: r } = await apiGet('/api/games/chess/room');
        if (r) apply(r);
      } catch (e) { /* 未登录 */ }
    }, 5000);
  };
}

// ---------- demo ----------
function demoRoom() {
  const rows = ['rnbqkbnr', 'pppppppp', '........', '........', '....P...', '........', 'PPPP.PPP', 'RNBQKBNR'];
  return {
    id: 'AB3K9Q', status: 'playing', result: '', reason: '', turn: 'white', inCheck: false,
    pieces: rows.join(''), lastMove: 'e2e4', you: 'white',
    players: { white: { id: 1, name: 'jzk' }, black: { id: 2, name: '朋友' } },
    legalMoves: ['g1f3', 'f1c4', 'd2d4', 'e4e5', 'b1c3'],
    moves: ['e2e4'],
  };
}

(async () => {
  document.getElementById('room-chip').hidden = true;
  const lobbyPiece = document.getElementById('lobby-piece');
  if (lobbyPiece) lobbyPiece.src = pieceImg('N').src;
  if (DEMO) {
    shell.user = { id: 1, name: 'jzk', admin: true };
    apply(demoRoom());
    return;
  }
  await boot({ active: '/games', requireLogin: true });
  if (!shell.user) return;

  document.getElementById('create').onclick = createRoom;
  document.getElementById('refresh').onclick = loadRooms;
  document.getElementById('join').onclick = () => joinRoom(document.getElementById('code').value);
  document.getElementById('code').addEventListener('keydown', (e) => { if (e.key === 'Enter') joinRoom(e.target.value); });

  const invite = (qs.get('room') || '').trim().toUpperCase();
  const { room: current } = await apiGet('/api/games/chess/room');
  if (current) { apply(current); }
  else if (invite) {
    try {
      const res = await apiPost('/api/games/chess/rooms/join', { room: invite });
      apply(res.room);
      history.replaceState(null, '', '/games/chess');
    } catch (e) {
      toast('加入 ' + invite + ' 失败：' + e.message, { warn: true });
      paint();
    }
  } else {
    paint();
  }
  connectSSE();
})();
