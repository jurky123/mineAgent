// portal.js —— 首页：Agent 一级入口 + MC 状态 + 游戏 + 最新动态（Bento 布局）。
import { shell, boot, apiGet, fmtTime } from './shell.js';
import { h, icon, toast, openDialog, confirmDialog, gameIcon } from './ds.js';
import { loadMinecraftRelease, copyButton } from './minecraft-release.js';
let releaseData = null;
let releaseError = '';


const qs = new URLSearchParams(location.search);
const DEMO = qs.get('ui') === '1';
const FORCE_THEME = qs.get('theme');
if (FORCE_THEME === 'dark' || FORCE_THEME === 'light') localStorage.setItem('mineagent.theme', FORCE_THEME);

const home = () => document.getElementById('home');

function skeleton() {
 const wrap = h('div', 'welcome-skeleton skeleton'); wrap.setAttribute('role','status'); wrap.setAttribute('aria-label','正在加载首页'); home().replaceChildren(wrap);
}

// ---------- 最新动态 ----------
function feedSection(app) {
  const c = app.card || {};
  const sec = h('section', 'section');
  const head = h('div', 'section-head');
  const st = h('h2', 'section-title');
  st.appendChild(icon(app.icon || 'megaphone', 'sm'));
  st.appendChild(h('span', null, c.title || '站点更新'));
  head.appendChild(st);
  head.appendChild(h('div', 'spacer'));
  if (c.canEdit) {
    const publish = h('button', 'btn sm', '发布动态');
    publish.disabled = DEMO; if (DEMO) publish.title = '演示模式不发布更新'; publish.onclick = () => publishDialog();
    head.appendChild(publish);
  }
  sec.appendChild(head);

  const list = h('div', 'card feed');
  const items = (c.items || []).slice(0, 3);
  if (!items.length) {
    list.appendChild(h('div', 'empty', c.empty || '还没有动态'));
  } else {
    for (let it of items) {
      if (/名字即账号|输入名字即可|知道名字就能登录/.test(it.text || '')) it = { ...it, text: '【历史公告 · 已失效】' + it.text + '\n当前请使用密码或 Passkey 登录。' };
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
        del.title = '删除'; del.disabled = DEMO;
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
  const recent = apps.find(a => a.id === 'agent')?.card;
  const hasRecent = !!recent?.lastTitle || recent?.count > 0;
  const head = h('header', 'portal-heading');
  head.append(h('h1', null, 'zkun'), h('p', null, 'Agent、在线游戏与 Minecraft。'));
  host.appendChild(head);
  const entries = h('section', 'portal-launchpad entry-grid'); entries.setAttribute('aria-label', '快速入口');
  const agent = h('article', 'launch-agent');
  const agentHead = h('div', 'launch-heading'); agentHead.append(icon('sparkle', 'lg'), h('h2', null, 'Agent'));
  agent.append(agentHead, h('p', 'launch-description', '对话、处理文件与使用工具。'));
  const examples = h('div', 'launch-examples');
  for (const [label, query] of [['查询服务器状态', '看看服务器现在的运行状态'], ['整理一份文档', '帮我整理一份文档，先确认需要哪些材料']]) {
    const a = h('a', 'launch-example', label); a.href = '/agent?new=1&q=' + encodeURIComponent(query); a.appendChild(icon('right', 'sm')); examples.appendChild(a);
  }
  const open = h('a', 'btn primary cta', hasRecent ? '继续对话' : '打开 Agent'); open.href = hasRecent ? recent.continuePath || '/agent' : '/agent';
  agent.append(examples, open); entries.appendChild(agent);
  const games = h('article', 'launch-secondary launch-games');
  const gameHead = h('div', 'launch-heading'); gameHead.append(icon('gamepad', 'lg'), h('h2', null, '在线游戏'));
  games.append(gameHead, h('p', null, '国际象棋与五子棋，创建房间即可开始。'));
  const gameLinks = h('div', 'launch-game-links');
  for (const [id, name] of [['chess', '国际象棋'], ['gomoku', '五子棋']]) { const a = h('a', 'launch-game', name); a.href = '/games/' + id; a.prepend(gameIcon(id)); gameLinks.appendChild(a); }
  games.appendChild(gameLinks); entries.appendChild(games);
  const mc = apps.find(a => a.id === 'minecraft');
  const minecraft = h('article', 'launch-secondary launch-minecraft'); minecraft.id = 'minecraft';
  const mcHead = h('div', 'launch-heading'); mcHead.append(icon('pickaxe', 'lg'), h('h2', null, 'Minecraft'));
  const state = data.loading ? '正在读取服务器状态…' : mc?.error ? '状态暂不可用' : mc?.card?.online === true ? '服务器在线' + (Number.isFinite(mc.card.count) ? ' · ' + mc.card.count + ' 人' : '') : mc?.card?.online === false ? '服务器离线' : '状态暂不可用';
  const status = h('p', 'launch-status', state); status.setAttribute('role', 'status');
  minecraft.append(mcHead, status);
  if (releaseData?.serverAddress) {
    const address = h('div', 'portal-address'); address.append(h('code', null, releaseData.serverAddress), copyButton(releaseData.serverAddress, '复制地址')); minecraft.appendChild(address);
  }
  const guide = h('a', 'text-link', '客户端与加入指南'); guide.href = '/minecraft'; guide.appendChild(icon('right', 'sm')); minecraft.appendChild(guide);
  entries.appendChild(minecraft); host.appendChild(entries);
  if (hasRecent) {
    const section = h('section', 'portal-recent'); section.appendChild(h('h2', null, '最近对话'));
    const link = h('a', 'recent-row'); link.href = recent.continuePath || '/agent';
    const title = h('div'); title.append(h('strong', null, recent.lastTitle || '未命名会话'));
    if (recent.lastSummary) title.appendChild(h('p', 'recent-summary', recent.lastSummary));
    link.append(title, h('span', 'recent-date', fmtTime(recent.lastUpdated)), icon('right')); section.appendChild(link); host.appendChild(section);
  }
  for (const app of apps.filter(a => a.role === 'feed')) {
    if (app.card?.items?.length || app.card?.canEdit) host.appendChild(feedSection(app));
    else if (app.error) host.appendChild(h('p', 'field-err', '更新暂时无法加载，请稍后重试。'));
  }
  if (releaseError) { const note = h('p', 'field-err', releaseError); const retry = h('button', 'btn ghost', '重试'); retry.onclick = refresh; host.append(note, retry); }
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
  const pageTask = DEMO ? Promise.resolve(demoData()) : apiGet(shell.user ? '/api/portal/home' : '/api/portal/public');
  const updateTask = shell.user || DEMO ? Promise.resolve(null) : apiGet('/api/portal/announcements?limit=3');
  const remaining = Promise.allSettled([pageTask, updateTask]);
  try { releaseData = await loadMinecraftRelease(); releaseError = ''; }
  catch (error) { releaseError = error.message; }
  if (!DEMO) paint({apps: [], loading: true});
  const [page, updates] = await remaining;
  const data = page.status === 'fulfilled' ? page.value : { apps: [{id: 'minecraft', error: true}] };
  if (updates.status === 'fulfilled' && updates.value?.announcements?.length) data.apps.push({id: 'announcements', role: 'feed', card: {title: '站点更新', items: updates.value.announcements}});
  paint(data);
  if (page.status === 'rejected') {
    const box = h('div', 'home-error'); box.setAttribute('role', 'status'); const retry = h('button', 'btn', '重新加载'); retry.onclick = refresh;
    box.append(h('p', 'field-err', '状态加载失败，请重试。'), retry); home().appendChild(box);
  }
}

skeleton();
await boot({ active: '/', requireLogin: false, preview: DEMO });
await refresh();
