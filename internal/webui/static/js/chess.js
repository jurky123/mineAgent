// chess.js：国际象棋房间页（棋盘渲染 + 点击走子 + SSE 实时同步）。
import { shell, boot, apiGet, apiPost, h } from './shell.js';

const qs = new URLSearchParams(location.search);
const DEMO = qs.get('ui') === '1';

// 统一用实心字形，颜色交给 CSS（白子白填充+黑描边，黑子黑填充+浅描边）。
const GLYPH = {
  K: '♚', Q: '♛', R: '♜', B: '♝', N: '♞', P: '♟',
};

let state = { room: null, selected: null, lastSnapshotAt: 0 };

function squareName(sq) {
  return String.fromCharCode(97 + (sq % 8)) + String(1 + Math.floor(sq / 8));
}

function myColor() { return state.room ? state.room.you : ''; }

function legalFrom(sq) {
  const name = squareName(sq);
  return (state.room && state.room.legalMoves || []).filter((m) => m.slice(0, 2) === name);
}

function pieceClass(p) {
  return p === p.toUpperCase() ? 'pw' : 'pb';
}

function renderBoard() {
  const r = state.room;
  const board = document.getElementById('board');
  const files = document.getElementById('files');
  board.innerHTML = '';
  files.innerHTML = '';
  if (!r) {
    board.appendChild(h('div', 'empty board-empty', '还没有房间，先创建或加入一个'));
    return;
  }
  const flip = r.you === 'black';
  const pieces = r.pieces || '';
  const last = r.lastMove || '';
  const targets = state.selected != null ? legalFrom(state.selected).map((m) => m.slice(2, 4)) : [];
  const kingSq = (() => {
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
      const p = pieces[sq] && pieces[sq] !== '.' ? pieces[sq] : '';
      const sqName = squareName(sq);
      const cell = h('div', 'sq ' + ((rank + file) % 2 ? 'dark' : 'light'));
      cell.dataset.sq = sqName;
      if (last.length >= 4 && (sqName === last.slice(0, 2) || sqName === last.slice(2, 4))) cell.classList.add('last');
      if (targets.includes(sqName)) cell.classList.add('target');
      if (state.selected === sq) cell.classList.add('sel');
      if (kingSq === sq) cell.classList.add('check');
      if (p) {
        const span = h('span', 'piece ' + pieceClass(p), GLYPH[p.toUpperCase()] || '?');
        cell.appendChild(span);
      }
      cell.onclick = () => onSquare(sq);
      board.appendChild(cell);
    }
  }
  const letters = flip ? ['h', 'g', 'f', 'e', 'd', 'c', 'b', 'a'] : ['a', 'b', 'c', 'd', 'e', 'f', 'g', 'h'];
  for (const l of letters) files.appendChild(h('span', null, l));
}

function onSquare(sq) {
  const r = state.room;
  if (!r || r.status !== 'playing' || !r.legalMoves) return; // 不是你的回合
  const name = squareName(sq);
  if (state.selected != null) {
    const moves = legalFrom(state.selected);
    const hit = moves.find((m) => m.slice(2, 4) === name);
    if (hit) { sendMove(hit); return; }
  }
  const p = (r.pieces || '')[sq];
  const mine = r.you === 'white' ? (p && p === p.toUpperCase()) : (p && p !== p.toUpperCase() && p !== '.');
  state.selected = mine ? sq : null;
  renderBoard();
}

async function sendMove(move) {
  const from = move.slice(0, 2), to = move.slice(2, 4);
  const promo = move.length === 5 ? move[4] : '';
  state.selected = null;
  try {
    const res = await apiPost('/api/games/chess/move', { from, to, promotion: promo });
    apply(res.room);
  } catch (e) {
    toast(e.message);
  }
}

let toastTimer = null;
function toast(msg) {
  let el = document.getElementById('gtoast');
  if (!el) {
    el = h('div', 'gtoast');
    el.id = 'gtoast';
    document.body.appendChild(el);
  }
  el.textContent = msg;
  el.classList.add('on');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => el.classList.remove('on'), 2600);
}

function playerLine(color) {
  const r = state.room;
  const p = r.players && r.players[color];
  const you = r.you === color;
  const turn = r.turn === color && r.status === 'playing';
  const name = p ? p.name : '等待加入';
  const line = h('div', 'player-line' + (turn ? ' turn' : ''));
  line.appendChild(h('span', 'dot' + (color === 'white' ? ' w' : ' b')));
  line.appendChild(h('span', 'player-name', name + (you ? '（你）' : '')));
  if (turn) line.appendChild(h('span', 'badge ok', '行棋中'));
  if (r.status === 'finished' && r.result === color) line.appendChild(h('span', 'badge', '胜'));
  return line;
}

function renderPanels() {
  const r = state.room;
  const top = document.getElementById('top-player');
  const bottom = document.getElementById('bottom-player');
  const status = document.getElementById('status');
  top.innerHTML = '';
  bottom.innerHTML = '';
  if (!r) {
    top.appendChild(h('div', 'player-line', '——'));
    bottom.appendChild(h('div', 'player-line', '——'));
    status.textContent = '还没有房间';
    status.className = 'status-bar';
    return;
  }
  const flip = r.you === 'black';
  const topColor = flip ? 'white' : 'black';
  const bottomColor = flip ? 'black' : 'white';
  top.appendChild(playerLine(topColor));
  bottom.appendChild(playerLine(bottomColor));

  let text = '';
  let cls = 'status-bar';
  if (r.status === 'waiting') {
    text = r.you
      ? '等待对手加入…把房间码 ' + r.id + ' 发给朋友'
      : '你是观战/等待状态';
  } else if (r.status === 'playing') {
    const mine = r.you && r.turn === r.you;
    text = mine ? '轮到你走' : '等对手走棋…';
    if (r.inCheck) text += '（被将军！）';
    cls += mine ? ' active' : '';
  } else {
    const reason = { checkmate: '将杀', stalemate: '逼和', resign: '认输', leave: '对手离开' }[r.reason] || r.reason;
    if (r.result === 'draw') text = '和棋（' + reason + '）';
    else {
      const winner = r.result === 'white' ? '白方' : '黑方';
      const youWin = r.you === r.result;
      text = winner + '胜（' + reason + '）' + (youWin ? '，恭喜！' : '');
    }
    cls += ' done';
  }
  status.textContent = text;
  status.className = cls;

  const info = document.getElementById('room-info');
  info.innerHTML = '';
  const link = location.origin + '/games/chess?room=' + r.id;
  info.appendChild(h('div', 'room-code', '房间码 ' + r.id));
  const copyBtn = h('button', 'btn ghost sm', '复制邀请链接');
  copyBtn.onclick = () => {
    navigator.clipboard.writeText(link).then(() => toast('已复制邀请链接'), () => toast(link));
  };
  info.appendChild(copyBtn);
  info.appendChild(h('div', 'room-hint', '同一房间只允许两名玩家；刷新/断开重连不丢对局。'));

  const moves = document.getElementById('moves');
  moves.innerHTML = '';
  const list = r.moves || [];
  if (!list.length) {
    moves.appendChild(h('span', 'empty', '暂无'));
  } else {
    list.forEach((m, i) => {
      const no = Math.floor(i / 2) + 1;
      const tag = h('span', 'mv' + (i === list.length - 1 ? ' last' : ''));
      tag.textContent = (i % 2 === 0 ? no + '. ' : '') + m;
      moves.appendChild(tag);
    });
    moves.scrollTop = moves.scrollHeight;
  }

  document.getElementById('resign').hidden = r.status !== 'playing';
  document.getElementById('leave').hidden = !(r.status === 'waiting' || r.status === 'finished');
  document.getElementById('again').hidden = r.status !== 'finished';
}

function apply(room) {
  state.room = room;
  state.lastSnapshotAt = Date.now();
  if (state.selected != null) state.selected = null;
  renderBoard();
  renderPanels();
}

// ---------- 无房间时的创建/加入面板 ----------
function renderNoRoom() {
  const status = document.getElementById('status');
  status.innerHTML = '';
  status.appendChild(h('span', null, '还没有房间：'));
  const create = h('button', 'btn primary sm', '创建房间（执白）');
  create.onclick = async () => {
    const res = await apiPost('/api/games/chess/rooms');
    apply(res.room);
  };
  status.appendChild(create);
  const input = h('input', 'login-input inline');
  input.id = 'joincode';
  input.maxLength = 6;
  input.placeholder = '输入房间码加入';
  const join = h('button', 'btn sm', '加入');
  const doJoin = async () => {
    try {
      const res = await apiPost('/api/games/chess/rooms/join', { room: input.value.trim().toUpperCase() });
      apply(res.room);
    } catch (e) { toast(e.message); }
  };
  join.onclick = doJoin;
  input.addEventListener('keydown', (e) => { if (e.key === 'Enter') doJoin(); });
  status.appendChild(input);
  status.appendChild(join);
}

// URL 里带 ?room=XXX 时提示加入（方便分享链接）
function pendingInvite() {
  const code = (qs.get('room') || '').trim().toUpperCase();
  return code && /^[A-Z0-9]{4,8}$/.test(code) ? code : '';
}

async function loadRoom() {
  if (DEMO) {
    apply(demoRoom());
    return;
  }
  const invite = pendingInvite();
  const { room } = await apiGet('/api/games/chess/room');
  if (room) { apply(room); return; }
  if (invite) {
    try {
      const res = await apiPost('/api/games/chess/rooms/join', { room: invite });
      apply(res.room);
      return;
    } catch (e) {
      toast('加入 ' + invite + ' 失败：' + e.message);
    }
  }
  if (!state.room) renderNoRoom();
}

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
    // 断开自动重连由 EventSource 负责；若 5 秒无消息再补拉一次状态。
    setTimeout(async () => {
      if (Date.now() - state.lastSnapshotAt > 5000) {
        try {
          const { room } = await apiGet('/api/games/chess/room');
          if (room) apply(room);
        } catch (e) { /* 未登录等 */ }
      }
    }, 5200);
  };
}

function demoRoom() {
  const rows = [
    'rnbqkbnr',
    'pppppppp',
    '........',
    '........',
    '....P...',
    '........',
    'PPPP.PPP',
    'RNBQKBNR',
  ];
  return {
    id: 'AB3K9Q', status: 'playing', result: '', reason: '', turn: 'white', inCheck: false,
    pieces: rows.join(''), lastMove: 'e2e4', you: 'white',
    players: { white: { id: 1, name: 'jzk' }, black: { id: 2, name: '朋友' } },
    legalMoves: ['e1e2', 'g1f3', 'f1c4', 'd2d4', 'e4e5'],
    moves: ['e2e4'],
  };
}

document.getElementById('resign').onclick = async () => {
  if (!confirm('确定认输？')) return;
  try {
    const res = await apiPost('/api/games/chess/resign');
    if (res.room) apply(res.room); else location.href = '/games';
  } catch (e) { toast(e.message); }
};
document.getElementById('leave').onclick = async () => {
  await apiPost('/api/games/chess/leave');
  state.room = null;
  state.selected = null;
  location.href = '/games';
};
document.getElementById('again').onclick = async () => {
  await apiPost('/api/games/chess/leave');
  const res = await apiPost('/api/games/chess/rooms');
  apply(res.room);
};

(async () => {
  await boot({ active: '/games', requireLogin: !DEMO });
  if (!DEMO && !shell.user) return;
  renderBoard();
  renderPanels();
  await loadRoom();
  connectSSE();
})();
