// history.js — 对局历史：列表（和谁下、胜负）+ 逐步回放（原始着法重演）。
import { boot, apiGet, fmtTime } from './shell.js';
import { h, icon, gameIcon, reveal } from './ds.js';

const qs = new URLSearchParams(location.search);
const DEMO = ['gomoku', 'chess'].includes(qs.get('demo')) || qs.get('ui') === '1';
const GAME_NAME = { chess: '国际象棋', gomoku: '五子棋' };
const SIDE_NAME = { white: '白方', black: '黑方' };
const REASON = {
  checkmate: '将杀', stalemate: '逼和', resign: '认输', leave: '对手离开',
  five: '五连', full: '棋盘已满', timeout: '超时',
};
const START_PIECES = 'RNBQKBNRPPPPPPPP' + '.'.repeat(32) + 'pppppppprnbqkbnr';
const SVG_NS = 'http://www.w3.org/2000/svg';

let runs = [];
let cursor = 0;
let playback = null;
function stopPlayback() { clearInterval(playback); playback = null; const button = document.getElementById('play'); if (button) button.textContent = '播放'; }
let replay = null; // {run, steps:[], index}

function resultBadge(result) {
  const map = { win: ['胜', 'brand'], lose: ['负', 'warn'], draw: ['和', ''] };
  const [text, cls] = map[result] || [result, ''];
  return h('span', 'badge ' + cls, text);
}

// ---------- 列表 ----------
async function load(append) {
  if (DEMO) { runs = demoRuns(); renderList(runs, false); return; }
  const url = '/api/games/history?limit=20' + (append && cursor ? '&before=' + cursor : '');
  const { runs: list } = await apiGet(url);
  if (!append) runs = [];
  runs = runs.concat(list || []);
  if (runs.length) cursor = runs[runs.length - 1].id;
  renderList(runs, (list || []).length === 20);
}

function renderList(list, hasMore) {
  const box = document.getElementById('runs');
  box.innerHTML = '';
  const stats = { win: 0, lose: 0, draw: 0 };
  for (const r of runs) if (stats[r.result] !== undefined) stats[r.result]++;
  document.getElementById('summary').textContent =
    runs.length ? ((DEMO ? '演示战绩 · ' : '') + '胜 ' + stats.win + ' · 负 ' + stats.lose + ' · 和 ' + stats.draw) : '';
  if (!list.length) {
    box.appendChild(h('div', 'empty', '还没有对局记录，去游戏里下一局吧'));
    document.getElementById('more').hidden = true;
    return;
  }
  for (const r of list) {
    let meta = {};
    try { meta = JSON.parse(r.metadata || '{}'); } catch (e) { /* 旧记录 */ }
    const row = h('div', 'list-row');
    const ic = h('span', 'game-icon');
    ic.appendChild(gameIcon(r.gameId, 'game-icon-img'));
    row.appendChild(ic);
    const main = h('div', 'list-main');
    const title = h('div', 'list-title');
    title.appendChild(resultBadge(r.result));
    title.appendChild(h('span', null, ' ' + (GAME_NAME[r.gameId] || r.gameId) +
      (meta.opponent ? ' · vs ' + meta.opponent : '')));
    main.appendChild(title);
    const bits = [];
    if (meta.side) bits.push(SIDE_NAME[meta.side] || meta.side);
    if (meta.moves) bits.push(meta.moves + ' 手');
    if (meta.reason) bits.push(REASON[meta.reason] || meta.reason);
    bits.push(fmtTime(r.createdAt));
    main.appendChild(h('div', 'list-sub', bits.join(' · ')));
    row.appendChild(main);
    if (Array.isArray(meta.movelist) && meta.movelist.length) {
      const btn = h('button', 'btn sm ghost', '回放');
      btn.onclick = () => openReplay(r, meta);
      row.appendChild(btn);
    }
    box.appendChild(row);
  }
  document.getElementById('more').hidden = !hasMore;
  if (!renderList.done) reveal(box.children);   // 只在首次加载淡入；"加载更多"时不重播
  renderList.done = true;
}

// ---------- 回放 ----------
function sqName(sq) { return String.fromCharCode(97 + (sq % 8)) + (1 + Math.floor(sq / 8)); }
function sqIndex(name) { return (name.charCodeAt(1) - 49) * 8 + (name.charCodeAt(0) - 97); }

// 按 UCI 走一步，返回新的 64 字符局面（象棋；不含吃过路兵）
function applyUci(pieces, uci) {
  const arr = pieces.split('');
  const from = sqIndex(uci.slice(0, 2));
  const to = sqIndex(uci.slice(2, 4));
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

function chessSteps(movelist) {
  const steps = [{ pieces: START_PIECES, last: '' }];
  let pieces = START_PIECES;
  for (const uci of movelist) {
    pieces = applyUci(pieces, uci);
    steps.push({ pieces, last: uci });
  }
  return steps;
}

function gomokuSteps(movelist) {
  const cells = new Array(225).fill('.');
  const steps = [{ cells: cells.join(''), last: '' }];
  let turn = 'b';
  for (const pt of movelist) {
    // 五子棋坐标：列 a-o，行 1-15
    const col = pt.toLowerCase().charCodeAt(0) - 97;
    const row = parseInt(pt.slice(1), 10) - 1;
    if (row < 0 || row > 14 || col < 0 || col > 14) continue;
    cells[row * 15 + col] = turn;
    steps.push({ cells: cells.join(''), last: pt.toLowerCase() });
    turn = turn === 'b' ? 'w' : 'b';
  }
  return steps;
}

function openReplay(run, meta) {
  stopPlayback();
  const game = run.gameId;
  const steps = game === 'chess' ? chessSteps(meta.movelist || []) : gomokuSteps(meta.movelist || []);
  replay = { run, meta, steps, index: steps.length - 1, game };
  document.getElementById('list-view').hidden = true;
  document.getElementById('replay-view').hidden = false;
  const side = SIDE_NAME[meta.side] || meta.side || '';
  document.getElementById('replay-title').textContent =
    (GAME_NAME[game] || game) + ' · ' + (meta.opponent ? 'vs ' + meta.opponent : '') +
    ' · ' + (resultText(run.result)) + (side ? '（' + (DEMO ? '演示玩家 A 执' : '你执') + side + '）' : '');
  const top = document.getElementById('replay-top');
  const bottom = document.getElementById('replay-bottom');
  const opponent = meta.opponent || '对手';
  const bottomSide = game === 'gomoku' ? 'black' : 'white';
  top.textContent = meta.side === bottomSide ? opponent : (DEMO ? '演示玩家 A' : '你');
  bottom.textContent = meta.side === bottomSide ? (DEMO ? '演示玩家 A' : '你') : opponent;
  const slider = document.getElementById('slider');
  slider.max = String(steps.length - 1);
  renderReplay();
  document.getElementById('back').focus();
}

function resultText(result) {
  return { win: '胜', lose: '负', draw: '和' }[result] || result || '';
}

function renderReplay() {
  if (!replay) return;
  const { steps, index, game } = replay;
  const step = steps[index];
  document.getElementById('step').textContent = index + ' / ' + (steps.length - 1);
  document.getElementById('slider').value = String(index);
  const board = document.getElementById('replay-board');
  board.innerHTML = '';
  if (game === 'chess') {
    const grid = h('div', 'board replay-board-chess');
    const pieces = step.pieces;
    for (let row = 0; row < 8; row++) {
      for (let col = 0; col < 8; col++) {
        const rank = 7 - row;
        const file = col;
        const sq = rank * 8 + file;
        const name = sqName(sq);
        const p = pieces[sq] !== '.' ? pieces[sq] : '';
        const cell = h('div', 'sq ' + ((rank + file) % 2 ? 'dark' : 'light'));
        if (step.last && (name === step.last.slice(0, 2) || name === step.last.slice(2, 4))) cell.classList.add('last');
        if (col === 0) cell.appendChild(h('span', 'coord rank', String(rank + 1)));
        if (row === 7) cell.appendChild(h('span', 'coord file', name[0]));
        if (p) {
          const img = h('img', 'pc ' + (p === p.toUpperCase() ? 'pc-w' : 'pc-b'));
          const ver = (document.querySelector('meta[name=mineagent-version]') || {}).content || '';
          img.src = '/static/' + ver + '/img/pieces/' + (p === p.toUpperCase() ? 'w' : 'b') + p.toUpperCase() + '.svg';
          cell.appendChild(img);
        }
        grid.appendChild(cell);
      }
    }
    board.appendChild(grid);
  } else {
    const grid = h('div', 'gomoku-board replay-board-gomoku');
    grid.style.setProperty('--gomoku-size', 15);
    const svg = document.createElementNS(SVG_NS, 'svg');
    svg.setAttribute('class', 'gomoku-grid');
    svg.setAttribute('viewBox', '0 0 15 15');
    svg.setAttribute('preserveAspectRatio', 'none');
    for (let i = 0; i < 15; i++) {
      const p = i + 0.5;
      for (const [x1, y1, x2, y2] of [[p, 0, p, 15], [0, p, 15, p]]) {
        const l = document.createElementNS(SVG_NS, 'line');
        l.setAttribute('x1', x1); l.setAttribute('y1', y1); l.setAttribute('x2', x2); l.setAttribute('y2', y2);
        l.setAttribute('vector-effect', 'non-scaling-stroke');
        svg.appendChild(l);
      }
    }
    grid.appendChild(svg);
    const cells = step.cells;
    for (let i = 0; i < 225; i++) {
      const c = cells[i];
      if (c === '.') continue;
      const dot = h('span', 'stone ' + c + (step.last === ptName(i) ? ' replay-last' : ''));
      dot.style.left = (((i % 15) + 0.5) / 15 * 100) + '%';
      dot.style.top = ((Math.floor(i / 15) + 0.5) / 15 * 100) + '%';
      dot.classList.add('replay-stone');
      grid.appendChild(dot);
    }
    if (index === steps.length - 1 && replay.meta.reason === 'five') {
      const win = winningLine(cells);
      if (win) {
        const line = document.createElementNS(SVG_NS, 'line');
        line.setAttribute('x1', win[0] % 15 + .5); line.setAttribute('y1', Math.floor(win[0] / 15) + .5);
        line.setAttribute('x2', win[1] % 15 + .5); line.setAttribute('y2', Math.floor(win[1] / 15) + .5);
        line.setAttribute('class', 'replay-winning-line'); svg.appendChild(line);
      }
    }
    const frame = h('div', 'gomoku-frame');
    const cols = h('div', 'gomoku-cols'); const rows = h('div', 'gomoku-rows');
    for (let i = 0; i < 15; i++) { cols.appendChild(h('span', null, String.fromCharCode(65 + i))); rows.appendChild(h('span', null, String(i + 1))); }
    frame.append(cols, rows, grid); board.appendChild(frame);
  }
  const moves = document.getElementById('replay-moves');
  moves.innerHTML = '';
  const display = replay.meta.display?.length ? replay.meta.display : replay.meta.movelist || [];
  if (!display.length) { moves.appendChild(h('div', 'empty', '暂无')); return; }
  const table = h('div', 'moves-grid');
  for (let i = 0; i < display.length; i += 2) {
    table.appendChild(h('span', 'mv-no', (i / 2 + 1) + '.'));
    for (let j = i; j < i + 2; j++) {
      if (!display[j]) { table.appendChild(h('span')); continue; }
      const button = h('button', 'mv' + (j === index - 1 ? ' last' : ''), display[j]);
      button.setAttribute('aria-label', '跳到第 ' + (j + 1) + ' 手：' + display[j]);
      if (j === index - 1) button.setAttribute('aria-current', 'step');
      button.onclick = () => { stopPlayback(); replay.index = Math.min(j + 1, steps.length - 1); renderReplay(); };
      table.appendChild(button);
    }
  }
  moves.appendChild(table);
  const current = table.querySelector('[aria-current]');
  if (current) {
    const row = current.offsetTop - moves.offsetTop;
    if (row < moves.scrollTop || row + current.offsetHeight > moves.scrollTop + moves.clientHeight) moves.scrollTop = Math.max(0, row - moves.clientHeight / 2);
  }
  document.getElementById('first').disabled = document.getElementById('prev').disabled = index === 0;
  document.getElementById('last').disabled = document.getElementById('next').disabled = index === steps.length - 1;

}

function winningLine(cells) {
  for (let i = 0; i < 225; i++) {
    if (cells[i] === '.') continue;
    const row = Math.floor(i / 15), col = i % 15;
    for (const [dr, dc] of [[0, 1], [1, 0], [1, 1], [1, -1]]) {
      const r = row + dr * 4, c = col + dc * 4;
      if (r < 0 || r >= 15 || c < 0 || c >= 15) continue;
      if ([1, 2, 3, 4].every((n) => cells[(row + dr * n) * 15 + col + dc * n] === cells[i])) return [i, r * 15 + c];
    }
  }
  return null;
}

function ptName(i) { return String.fromCharCode(97 + (i % 15)) + (Math.floor(i / 15) + 1); }

function stepBy(delta) {
  stopPlayback();
  if (!replay) return;
  replay.index = Math.max(0, Math.min(replay.steps.length - 1, replay.index + delta));
  renderReplay();
}

function showLoadError(error) {
  const box = document.getElementById('runs');
  const row = h('div', 'empty'); row.setAttribute('role', 'status'); row.appendChild(h('p', null, '历史记录加载失败：' + error.message));
  const retry = h('button', 'btn', '重试'); retry.onclick = () => load(false).catch(showLoadError); row.appendChild(retry); box.replaceChildren(row);
}
document.addEventListener('keydown', (event) => {
  if (!replay || event.defaultPrevented || event.altKey || event.ctrlKey || event.metaKey || event.shiftKey || event.target.closest('input, textarea, select, [contenteditable="true"]')) return;
  if (event.key === 'ArrowLeft' || event.key === 'ArrowRight') { event.preventDefault(); stepBy(event.key === 'ArrowLeft' ? -1 : 1); }
  else if (event.key === 'Home' || event.key === 'End') { event.preventDefault(); replay.index = event.key === 'Home' ? 0 : replay.steps.length - 1; renderReplay(); }
});
// ---------- demo ----------
function demoRuns() {
  return [
    { id: 3, gameId: 'gomoku', result: 'win', metadata: JSON.stringify({ opponent: '演示玩家 B', side: 'black', moves: 9, reason: 'five', movelist: ['h8', 'i9', 'h9', 'i8', 'h10', 'i7', 'h11', 'i6', 'h12'], display: ['h8', 'i9', 'h9', 'i8', 'h10', 'i7', 'h11', 'i6', 'h12'] }), createdAt: Date.now() - 3600e3 },
    { id: 2, gameId: 'chess', result: 'lose', metadata: JSON.stringify({ opponent: '演示玩家 B', side: 'white', moves: 4, reason: 'checkmate', movelist: ['f2f3', 'e7e5', 'g2g4', 'd8h4'], display: ['f3', 'e5', 'g4', 'Qh4#'] }), createdAt: Date.now() - 86400e3 },
  ];
}

(async () => {
  document.getElementById('back').onclick = () => {
    stopPlayback();
    replay = null;
    document.getElementById('replay-view').hidden = true;
    document.getElementById('list-view').hidden = false;
  };
  document.getElementById('more').onclick = () => load(true).catch(showLoadError);
  document.getElementById('first').onclick = () => { stopPlayback(); replay.index = 0; renderReplay(); };
  document.getElementById('prev').onclick = () => stepBy(-1);
  document.getElementById('next').onclick = () => stepBy(1);
  document.getElementById('last').onclick = () => { stopPlayback(); replay.index = replay.steps.length - 1; renderReplay(); };
  document.getElementById('slider').oninput = (e) => { stopPlayback(); replay.index = Number(e.target.value); renderReplay(); };

  const controls = document.querySelector('.replay-controls');
  const play = h('button', 'btn sm', '播放'); play.id = 'play';
  const speed = h('select', 'input'); speed.setAttribute('aria-label', '回放速度');
  for (const [value, label] of [['1500', '慢速'], ['800', '正常'], ['300', '快速']]) { const option = h('option', null, label); option.value = value; speed.appendChild(option); } speed.value = '800';
  play.onclick = () => {
    if (playback) { stopPlayback(); return; } if (!replay) return;
    if (replay.index === replay.steps.length - 1) replay.index = 0;
    renderReplay(); play.textContent = '暂停';
    playback = setInterval(() => { if (!replay || document.hidden) { stopPlayback(); return; } replay.index = Math.min(replay.index + 1, replay.steps.length - 1); renderReplay(); if (replay.index === replay.steps.length - 1) stopPlayback(); }, Number(speed.value));
  }; speed.onchange = stopPlayback; controls.append(play, speed);
  const hint = h('p', 'field-hint', '← / → 逐步查看，Home / End 跳到首末；末手有标记，终局连线表示五连。'); controls.after(hint);
  document.addEventListener('visibilitychange', () => { if (document.hidden) stopPlayback(); });

  if (DEMO) {
    await boot({ active: '/account', requireLogin: false });
    const banner = h('aside', 'demo-banner'); banner.setAttribute('role', 'status');
    banner.appendChild(h('strong', null, '演示回放，不影响真实战绩'));
    const exit = h('a', 'btn', '退出演示'); exit.href = '/games/gomoku'; banner.appendChild(exit);
    document.querySelector('main').prepend(banner);
    const badge = h('span', 'badge', '演示数据 · 非真实对局'); document.querySelector('.crumb').appendChild(badge);
    runs = demoRuns();
    renderList(runs, false);
    return;
  }
  const user = await boot({ active: '/account' });
  if (!user) return;
  await load(false).catch(showLoadError);
})();
