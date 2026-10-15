// portal.js —— 首页：Agent 一级入口 + MC 状态 + 游戏 + 最新动态（Bento 布局）。
import { shell, boot, apiGet, fmtTime } from './shell.js';
import { h, icon, toast, openDialog, confirmDialog, gameIcon, reveal } from './ds.js';
import { loadMinecraftRelease, worldVisual, downloadCard } from './minecraft-release.js';
let releaseData = null;
let releaseError = '';


const qs = new URLSearchParams(location.search);
const DEMO = qs.get('ui') === '1';
const FORCE_THEME = qs.get('theme');
if (FORCE_THEME === 'dark' || FORCE_THEME === 'light') localStorage.setItem('mineagent.theme', FORCE_THEME);

const home = () => document.getElementById('home');

// 用名字稳定地取一个低饱和度底色（无外部头像依赖）
function tintOf(name) {
  let h = 0;
  for (const ch of name) h = (h * 31 + ch.codePointAt(0)) % 360;
  return 'hsl(' + h + ' 42% 88%)';
}

function skeleton() {
  const wrap = h('div', 'welcome-skeleton skeleton');
  wrap.setAttribute('role', 'status'); wrap.setAttribute('aria-label', '正在加载首页');
  home().replaceChildren(wrap);
}

function entryCard({ title, text, path, label, glyph }) {
  const card = h('article', 'entry-card');
  const mark = h('span', 'entry-icon'); mark.appendChild(icon(glyph, 'lg'));
  card.append(mark, h('h2', null, title), h('p', null, text));
  const link = h('a', 'btn', label); link.href = path; card.appendChild(link);
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
  if (players.length) {
    const faces = h('div', 'player-faces');
    for (const name of players.slice(0, 6)) {
      const a = h('span', 'face', (name[0] || '?').toUpperCase());
      a.title = name;
      a.style.setProperty('--tint', tintOf(name));
      faces.appendChild(a);
    }
    if (players.length > 6) faces.appendChild(h('span', 'face more', '+' + (players.length - 6)));
    card.appendChild(faces);
  }
  card.appendChild(h('div', 'status-count',
    players.length ? players.length + ' 人正在游戏' : '现在没人，服务器正在运行'));
  const bits = [];
  if (c.weather) bits.push(c.weather);
  if (c.period) bits.push(c.period);
  if (c.tps) bits.push('TPS ' + Number(c.tps).toFixed(2));
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
  if (openRooms) t.appendChild(h('div', 'game-desc', openRooms + ' 个开放房间'));
  head.appendChild(t);
  card.appendChild(head);
  const stats = [];
  if (item.wins) stats.push(item.wins + ' 胜');
  if (item.losses) stats.push(item.losses + ' 负');
  if (item.draws) stats.push(item.draws + ' 和');
  card.appendChild(h('div', 'game-stats', stats.length ? '最近战绩：' + stats.join(' · ') : '还没有对局记录'));
  // 首页只把 Agent 当一级操作；游戏入口降一级（进入 /games 后才是 primary）
  const start = h('a', 'btn cta', (openRooms ? '去加入' : '开始') + ' →');
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
      if (it.kind === 'game') {
        row.appendChild(h('span', 'feed-dot game'));
      } else {
        row.appendChild(h('span', 'feed-dot'));
      }
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
  const host = home();
  host.replaceChildren(); host.setAttribute('aria-busy', 'false');
  const apps = data.apps || [];
  const hero = apps.find((a) => a.id === 'agent');
  const mc = apps.find((a) => a.id === 'minecraft');
  const recent = hero?.card;
  const hasRecent = !!recent?.lastTitle || recent?.count > 0;
  const welcome = h('section', 'welcome-grid');
  const copy = h('div', 'welcome-copy');
  copy.appendChild(h('p', 'eyebrow', 'ZKUN / 门户'));
  copy.appendChild(h('p', 'welcome-greeting', data.greeting || '欢迎访问'));
  const heading = h('h1', null, 'zkun 门户');
  copy.append(heading, h('p', 'welcome-description', 'Agent 对话、在线游戏与 Minecraft 服务器入口。'));
  const actions = h('div', 'welcome-actions');
  const primary = h('a', 'btn primary cta', hasRecent ? '继续上次对话 →' : '打开 Agent →');
  primary.href = hasRecent ? recent.continuePath || '/agent' : '/agent';
  const explore = h('a', 'text-link', '游戏大厅'); explore.href = '/games';
  actions.append(primary, explore); copy.appendChild(actions);
  if (hasRecent) copy.appendChild(h('p', 'recent-context', '上次聊到：' + (recent.lastTitle || '未命名会话')));
  welcome.appendChild(copy);
  const status = mc?.error ? '状态暂不可用' : mc?.card?.online === true ? '服务器在线' : mc?.card?.online === false ? '离线' : '登录后查看实时状态';
  const world = h('a', 'world-link'); world.href = '#minecraft'; world.setAttribute('aria-label', '查看 Minecraft 加入方式');
  world.appendChild(worldVisual(releaseData, status)); welcome.appendChild(world); host.appendChild(welcome);
  const entries = h('section', 'entry-grid'); entries.setAttribute('aria-label', '快速入口');
  for (const entry of [
    { title: 'Agent', text: '对话、查询资料与使用工具。', path: '/agent', label: hasRecent ? '回到对话' : '开始对话', glyph: 'sparkle' },
    { title: '小游戏', text: '国际象棋与五子棋在线对局。', path: '/games', label: '进入游戏大厅', glyph: 'gamepad' },
    { title: 'Minecraft', text: '客户端准备、整合包下载与服务器信息。', path: '/minecraft', label: '下载与加入指南', glyph: 'pickaxe' },
  ]) entries.appendChild(entryCard(entry));
  host.appendChild(entries);
  const minecraft = downloadCard(releaseData, { compact: true, status }); minecraft.id = 'minecraft';
  if (releaseError) {
    const error = h('p', 'field-err', releaseError); error.setAttribute('role', 'status'); minecraft.appendChild(error);
    const retry = h('button', 'btn', '重新读取发布信息'); retry.onclick = refresh; minecraft.appendChild(retry);
  }
  if (mc) {
    const detail = h('details', 'home-server-details'); detail.appendChild(h('summary', null, '服务器实时详情'));
    detail.appendChild(statusCard(mc)); minecraft.appendChild(detail);
  }
  host.appendChild(minecraft);
  const games = apps.find((a) => a.id === 'games');
  if (games?.error) {
    const error = h('p', 'field-err', '游戏状态暂时加载失败，仍可从游戏大厅进入。'); host.appendChild(error);
  } else if (games?.card?.items?.length) {
    const section = h('section', 'section'); section.appendChild(h('h2', 'section-title', '游戏房间'));
    const grid = h('div', 'game-grid'); for (const item of games.card.items) grid.appendChild(gameCard(item));
    section.appendChild(grid); host.appendChild(section);
  }
  for (const app of apps.filter((a) => a.role === 'feed')) {
    if (app.card?.items?.length || app.card?.canEdit) host.appendChild(feedSection(app));
    else if (app.error) host.appendChild(h('p', 'field-err', '动态暂时无法加载，请稍后重试。'));
  }
  if (!paint.done) reveal(host.children); paint.done = true;
  if (location.hash === '#minecraft') requestAnimationFrame(() => minecraft.scrollIntoView());
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
        { id: 'chess', name: '国际象棋', desc: '经典双人对战', path: '/games/chess?ui=1&state=lobby', openRooms: 2, wins: 3, losses: 1, draws: 0 },
        { id: 'gomoku', name: '五子棋', desc: '15 路棋盘 · 先连五者胜', path: '/games/gomoku?ui=1&state=lobby', openRooms: 0, wins: 0, losses: 1, draws: 0 },
      ] } },
      { id: 'announcements', name: '最新动态', role: 'feed', card: {
        type: 'feed', title: '最新动态', canEdit: true, items: [
          { id: 0, kind: 'game', text: '五子棋：jzk 战胜了 Alex', author: '五连 · 9 手', createdAt: Date.now() - 1800e3 },
          { id: 2, text: '门户改版：首页变成个人 Hub，动态流取代了公告卡片。', author: 'jzk', createdAt: Date.now() - 3600e3 },
          { id: 0, kind: 'game', text: '国际象棋：朋友 战胜了 jzk', author: '将杀 · 4 手', createdAt: Date.now() - 7200e3 },
        ],
      } },
    ],
  };
}

async function refresh() {
  try { releaseData = await loadMinecraftRelease(); releaseError = ''; }
  catch (error) { releaseError = error.message; }
  if (DEMO) { paint(demoData()); return; }
  if (!shell.user) { paint({ greeting: '欢迎访问', apps: [] }); return; }
  try { paint(await apiGet('/api/portal/home')); }
  catch (error) {
    paint({ greeting: '欢迎回来', apps: [] });
    const box = h('div', 'home-error'); box.setAttribute('role', 'status');
    box.appendChild(h('p', 'field-err', '个人动态加载失败：' + error.message));
    const retry = h('button', 'btn', '重新加载'); retry.onclick = refresh; box.appendChild(retry); home().appendChild(box);
  }
}

skeleton();
await boot({ active: '/', requireLogin: false, preview: DEMO });
await refresh();
