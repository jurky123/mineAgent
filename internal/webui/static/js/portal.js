// portal.js —— 首页：Agent 一级入口 + MC 状态 + 游戏 + 最新动态（Bento 布局）。
import { shell, boot, apiGet, fmtTime } from './shell.js';
import { h, icon, toast, openDialog, confirmDialog, gameIcon } from './ds.js';

const qs = new URLSearchParams(location.search);
const DEMO = qs.get('ui') === '1';
const FORCE_THEME = qs.get('theme');
if (FORCE_THEME === 'dark' || FORCE_THEME === 'light') localStorage.setItem('mineagent.theme', FORCE_THEME);

const home = () => document.getElementById('home');

function skeleton() {
  const wrap = h('div', 'home-top');
  const a = h('div', 'card hero-card');
  a.appendChild(h('div', 'skeleton', ' '));
  const b = h('div', 'card status-card');
  b.appendChild(h('div', 'skeleton', ' '));
  wrap.appendChild(a);
  wrap.appendChild(b);
  home().appendChild(wrap);
}

// ---------- Agent 主卡 ----------
function heroCard(app) {
  const c = app.card || {};
  const card = h('article', 'card hero-card');
  const head = h('div', 'hero-head');
  const ic = h('span', 'hero-icon');
  ic.appendChild(icon(app.icon || 'sparkle', 'lg'));
  head.appendChild(ic);
  const headText = h('div');
  headText.appendChild(h('div', 'hero-title', c.title || 'MineAgent'));
  headText.appendChild(h('div', 'hero-sub', c.subtitle || ''));
  head.appendChild(headText);
  card.appendChild(head);

  const last = h('div', 'hero-last');
  const lastTitle = (c.lastTitle || '').trim();
  if (lastTitle) {
    last.appendChild(h('div', 'list-sub', '继续上一次对话'));
    last.appendChild(h('div', 'hero-conv', '“' + lastTitle + '”'));
    last.appendChild(h('div', 'list-sub', (fmtTime(c.lastUpdated) || '刚刚') + (c.count ? ' · 共 ' + c.count + ' 个会话' : '')));
  } else if (c.count) {
    last.appendChild(h('div', 'list-sub', '继续上一次对话'));
    last.appendChild(h('div', 'hero-conv', '（未命名会话）'));
    last.appendChild(h('div', 'list-sub', (fmtTime(c.lastUpdated) || '刚刚') + ' · 共 ' + c.count + ' 个会话'));
  } else {
    last.appendChild(h('div', 'list-sub', '还没有对话'));
    last.appendChild(h('div', 'hero-conv', '问我任何事：写代码、查资料、控制服务器'));
  }
  card.appendChild(last);

  const actions = h('div', 'hero-actions');
  const newBtn = h('a', 'btn primary cta', '新对话');
  newBtn.href = c.newPath || '/agent?new=1';
  actions.appendChild(newBtn);
  const cont = h('a', 'btn', '继续');
  cont.href = c.continuePath || '/agent';
  actions.appendChild(cont);
  card.appendChild(actions);
  return card;
}

// ---------- MC 状态卡 ----------
function statusCard(app) {
  const c = app.card || {};
  const card = h('article', 'card status-card');
  const head = h('div', 'status-head');
  const tile = h('span', 'app-icon-tile');
  tile.appendChild(icon(app.icon || 'server', 'lg'));
  head.appendChild(tile);
  const dot = h('span', 'status-dot' + (app.error ? ' off' : ' on'));
  head.appendChild(dot);
  head.appendChild(h('span', null, 'Minecraft'));
  card.appendChild(head);

  if (app.error) {
    card.appendChild(h('div', 'status-empty', '服务器状态暂时拿不到'));
    return card;
  }
  if (!c.online) {
    card.appendChild(h('div', 'status-count', '离线'));
    card.appendChild(h('div', 'status-sub', '服务器现在没开'));
    return card;
  }
  const players = c.players || [];
  card.appendChild(h('div', 'status-count',
    players.length ? players.length + ' 人正在游戏' : '现在没人，服务器开着'));
  if (players.length) {
    const chips = h('div', 'player-chips');
    for (const name of players.slice(0, 8)) chips.appendChild(h('span', 'player-chip', name));
    if (players.length > 8) chips.appendChild(h('span', 'player-chip', '+' + (players.length - 8)));
    card.appendChild(chips);
  }
  const bits = [];
  if (c.tps) bits.push('TPS ' + Number(c.tps).toFixed(2));
  if (c.weather) bits.push(c.weather);
  if (c.period) bits.push(c.period);
  if (bits.length) card.appendChild(h('div', 'status-sub', bits.join(' · ')));

  if (c.details && c.details.length) {
    const details = h('div', 'status-details');
    details.hidden = true;
    for (const d of c.details) {
      const row = h('div', 'row');
      row.appendChild(h('span', null, d.label));
      row.appendChild(h('b', null, String(d.value)));
      details.appendChild(row);
    }
    card.appendChild(details);
    const toggle = h('button', 'btn ghost sm', '查看详情');
    toggle.onclick = () => {
      details.hidden = !details.hidden;
      toggle.textContent = details.hidden ? '查看详情' : '收起';
    };
    card.appendChild(toggle);
  }
  return card;
}

// ---------- 游戏卡 ----------
function gameCard(item) {
  const card = h('article', 'card game-card');
  const head = h('div', 'catalog-head');
  const ic = h('span', 'game-icon');
  ic.appendChild(gameIcon(item.id, 'game-icon-img'));
  head.appendChild(ic);
  const t = h('div');
  t.appendChild(h('div', 'game-title', item.name));
  const openRooms = item.openRooms || 0;
  t.appendChild(h('div', 'game-desc', openRooms ? openRooms + ' 个开放房间' : (item.desc || '')));
  head.appendChild(t);
  card.appendChild(head);
  const stats = [];
  if (item.wins) stats.push(item.wins + ' 胜');
  if (item.losses) stats.push(item.losses + ' 负');
  if (item.draws) stats.push(item.draws + ' 和');
  card.appendChild(h('div', 'game-stats', stats.length ? '最近战绩：' + stats.join(' · ') : '还没有对局记录'));
  const start = h('a', 'btn primary cta', openRooms ? '去加入' : '开始');
  start.href = item.path || '/games';
  card.appendChild(start);
  return card;
}

// ---------- 最新动态 ----------
function feedSection(app) {
  const c = app.card || {};
  const sec = h('section', 'section');
  const head = h('div', 'section-head');
  const st = h('h2', 'section-title');
  st.appendChild(icon(app.icon || 'megaphone', 'sm'));
  st.appendChild(h('span', null, c.title || '最新动态'));
  head.appendChild(st);
  head.appendChild(h('div', 'spacer'));
  if (c.canEdit) {
    const publish = h('button', 'btn sm', '发布动态');
    publish.onclick = () => publishDialog();
    head.appendChild(publish);
  }
  sec.appendChild(head);

  const list = h('div', 'card feed');
  const items = c.items || [];
  if (!items.length) {
    list.appendChild(h('div', 'empty', c.empty || '还没有动态'));
  } else {
    for (const it of items) {
      const row = h('div', 'feed-row');
      row.appendChild(h('span', 'feed-dot'));
      const main = h('div', 'feed-main');
      main.appendChild(h('div', 'feed-text', it.text));
      main.appendChild(h('div', 'feed-meta', (it.author ? it.author + ' · ' : '') + fmtTime(it.createdAt)));
      row.appendChild(main);
      if (c.canEdit) {
        const del = h('button', 'icon-btn sm feed-del');
        del.title = '删除';
        del.appendChild(icon('x', 'sm'));
        del.onclick = async () => {
          const ok = await confirmDialog({
            title: '删除这条动态？', body: '删除后无法恢复。', confirmText: '删除', danger: true,
          });
          if (!ok) return;
          const res = await fetch('/api/portal/announcements?id=' + it.id, {
            method: 'DELETE', headers: { Authorization: 'Bearer ' + shell.token },
          });
          if (!res.ok) { toast('删除失败', { warn: true }); return; }
          toast('已删除');
          refresh();
        };
        row.appendChild(del);
      }
      list.appendChild(row);
    }
  }
  sec.appendChild(list);
  return sec;
}

async function publishDialog() {
  const field = h('div', 'field');
  const ta = h('textarea', 'textarea');
  ta.placeholder = '写点什么…（最多 500 字）';
  ta.maxLength = 500;
  field.appendChild(ta);
  const err = h('div', 'field-err');
  field.appendChild(err);
  openDialog({
    title: '发布动态',
    body: field,
    actions: [
      { label: '取消' },
      {
        label: '发布', primary: true, keepOpen: true,
        onClick: async (close) => {
          const text = ta.value.trim();
          if (!text) { err.textContent = '内容不能为空'; ta.focus(); return false; }
          try {
            const res = await fetch('/api/portal/announcements', {
              method: 'POST',
              headers: { 'Content-Type': 'application/json', Authorization: 'Bearer ' + shell.token },
              body: JSON.stringify({ text }),
            });
            if (!res.ok) {
              const b = await res.json().catch(() => null);
              throw new Error((b && b.error) || 'HTTP ' + res.status);
            }
            toast('已发布');
            close();
            refresh();
          } catch (e) {
            err.textContent = e.message;
          }
        },
      },
    ],
  });
}

// ---------- 渲染 ----------
function paint(data) {
  document.getElementById('greeting').textContent = data.greeting || '你好';
  const acc = data.account || {};
  document.getElementById('greeting-sub').textContent = '今天想做点什么？';
  const host = home();
  host.innerHTML = '';

  const apps = data.apps || [];
  const hero = apps.find((a) => a.role === 'hero');
  const status = apps.find((a) => a.role === 'status');
  const content = apps.filter((a) => a.role === 'content' || a.role === 'app');
  const feed = apps.filter((a) => a.role === 'feed');

  if (hero || status) {
    const top = h('div', 'home-top');
    if (hero) top.appendChild(heroCard(hero));
    if (status) top.appendChild(statusCard(status));
    host.appendChild(top);
  }
  const games = [];
  for (const app of content) {
    for (const item of ((app.card && app.card.items) || [])) games.push(item);
  }
  if (games.length) {
    const sec = h('section', 'section');
    const head = h('div', 'section-head');
    const st = h('h2', 'section-title');
    st.appendChild(icon('gamepad', 'sm'));
    st.appendChild(h('span', null, '游戏'));
    head.appendChild(st);
    host.appendChild(sec);
    sec.appendChild(head);
    const grid = h('div', 'game-grid');
    for (const item of games) grid.appendChild(gameCard(item));
    sec.appendChild(grid);
  }
  for (const app of feed) host.appendChild(feedSection(app));
  if (!hero && !status && !content.length && !feed.length) {
    host.appendChild(h('div', 'card', '门户暂无内容'));
  }
}

function demoData() {
  return {
    greeting: '下午好，jzk',
    account: { id: 1, name: 'jzk', admin: true },
    apps: [
      { id: 'agent', name: 'MineAgent', path: '/agent', role: 'hero', card: {
        type: 'hero', title: 'MineAgent', subtitle: '写代码、查资料、收发文件、控制服务器',
        lastTitle: '帮我 review 一下 chess 的房间逻辑', lastUpdated: Date.now() - 7200e3, count: 12,
        newPath: '/agent?new=1', continuePath: '/agent',
      } },
      { id: 'minecraft', name: 'Minecraft', role: 'status', card: {
        type: 'status', online: true, count: 3, max: 20, players: ['Steve', 'Alex', 'jzk'],
        tps: 19.98, weather: '晴天', period: '白天',
        details: [{ label: '内存', value: '1234 / 4096 MB' }, { label: '版本', value: '26.2' }, { label: 'TPS 5m/15m', value: '19.99 / 20.00' }],
      } },
      { id: 'games', name: '游戏', path: '/games', role: 'content', card: { type: 'games', items: [
        { id: 'chess', name: '国际象棋', desc: '经典双人对战', path: '/games/chess', openRooms: 2, wins: 3, losses: 1, draws: 0 },
        { id: 'gomoku', name: '五子棋', desc: '15 路棋盘 · 先连五者胜', path: '/games/gomoku', openRooms: 0, wins: 0, losses: 1, draws: 0 },
      ] } },
      { id: 'announcements', name: '最新动态', role: 'feed', card: {
        type: 'feed', title: '最新动态', canEdit: true, items: [
          { id: 2, text: '门户改版：首页变成个人 Hub，动态流取代了公告卡片。', author: 'jzk', createdAt: Date.now() - 3600e3 },
          { id: 1, text: '国际象棋在线房间已开放，和朋友开一局吧。', author: 'jzk', createdAt: Date.now() - 86400e3 },
        ],
      } },
    ],
  };
}

async function refresh() {
  if (DEMO) { paint(demoData()); return; }
  try {
    paint(await apiGet('/api/portal/home'));
  } catch (e) {
    if (e.unauthorized) { location.reload(); return; }
    home().innerHTML = '';
    home().appendChild(h('div', 'card', '加载失败：' + e.message));
  }
}

(async () => {
  if (!DEMO) skeleton();
  await boot({ active: '/', requireLogin: !DEMO });
  if (!DEMO && !shell.user) return;
  await refresh();
})();
