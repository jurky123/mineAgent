// account.js — 设置页：账号 / 外观 / 安全 / 设备 / 游戏 / 关于。
import { shell, boot, apiGet, apiPost, logout, themeMode, setTheme, applyTheme } from './shell.js';
import { h, icon, toast, confirmDialog } from './ds.js';

const qs = new URLSearchParams(location.search);
const DEMO = qs.get('ui') === '1';

function fmtTime(ms) {
  if (!ms) return '';
  const d = new Date(ms);
  const pad = (n) => String(n).padStart(2, '0');
  return d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate()) + ' ' + pad(d.getHours()) + ':' + pad(d.getMinutes());
}

function deviceName(ua) {
  ua = ua || '';
  let os = '未知设备';
  if (/iPhone|iPad|iPod/.test(ua)) os = 'iPhone / iPad';
  else if (/Android/.test(ua)) os = 'Android';
  else if (/Macintosh/.test(ua)) os = 'Mac';
  else if (/Windows/.test(ua)) os = 'Windows';
  else if (/Linux/.test(ua)) os = 'Linux';
  let br = '浏览器';
  if (/Edg\//.test(ua)) br = 'Edge';
  else if (/Chrome\//.test(ua)) br = 'Chrome';
  else if (/Safari\//.test(ua)) br = 'Safari';
  else if (/Firefox\//.test(ua)) br = 'Firefox';
  else if (/curl/.test(ua)) br = 'curl';
  else if (/legacy-tokens/.test(ua)) br = '旧版登录（已迁移）';
  return br + ' · ' + os;
}

// 给静态分区标题补图标（账号/外观/安全/设备/游戏/关于）
function decorateSections() {
  const map = {
    '账号': 'user', '外观': 'sun', '安全': 'shield', '设备': 'devices',
    '游戏': 'gamepad', '关于': 'info',
  };
  document.querySelectorAll('.section-title').forEach((el) => {
    const name = map[el.textContent.trim()];
    if (!name) return;
    el.innerHTML = '';
    el.appendChild(icon(name, 'sm'));
    el.appendChild(h('span', null, Object.keys(map).find((k) => map[k] === name)));
  });
}

function renderHeader(u) {
  document.getElementById('acc-avatar').textContent = (u.name[0] || '?').toUpperCase();
  document.getElementById('acc-name').textContent = u.name;
  const meta = document.getElementById('acc-meta');
  meta.innerHTML = '';
  meta.appendChild(h('span', 'badge' + (u.admin ? ' brand' : ''), u.admin ? '管理员' : '普通用户'));
  meta.appendChild(h('span', null, 'ID #' + u.id));
  const about = document.getElementById('acc-version');
  const ver = (document.querySelector('meta[name=mineagent-version]') || {}).content || '';
  about.textContent = ver ? 'v' + ver : '';
}

function renderAccountRows(u, sessCount) {
  const box = document.getElementById('acc-rows');
  box.innerHTML = '';
  const row = (label, value, sub) => {
    const r = h('div', 'list-row');
    const main = h('div', 'list-main');
    main.appendChild(h('div', 'list-title', label));
    if (sub) main.appendChild(h('div', 'list-sub', sub));
    r.appendChild(main);
    r.appendChild(h('div', 'setting-value', value));
    box.appendChild(r);
  };
  row('用户名', u.name, '登录名，不可修改');
  row('用户 ID', '#' + u.id);
  row('登录设备', sessCount + ' 台');
}

function renderSecurity() {
  const box = document.getElementById('acc-security');
  box.innerHTML = '';
  const r1 = h('div', 'list-row');
  const m1 = h('div', 'list-main');
  m1.appendChild(h('div', 'list-title', '免密码登录'));
  m1.appendChild(h('div', 'list-sub', '输入名字即可进入；知道名字就能登录，请勿把管理员名字告诉别人'));
  r1.appendChild(m1);
  r1.appendChild(h('span', 'badge', '已启用'));
  box.appendChild(r1);
  const r2 = h('div', 'list-row');
  const m2 = h('div', 'list-main');
  m2.appendChild(h('div', 'list-title', 'PIN 码'));
  m2.appendChild(h('div', 'list-sub', '给名字加一道门（开发中，暂未开放）'));
  r2.appendChild(m2);
  r2.appendChild(h('span', 'badge', '尚未设置'));
  box.appendChild(r2);
}

function renderThemeSeg() {
  const seg = document.getElementById('theme-seg');
  const paint = () => {
    seg.innerHTML = '';
    for (const [id, label] of [['system', '跟随系统'], ['light', '浅色'], ['dark', '深色']]) {
      const b = h('button', 'seg-item' + (themeMode() === id ? ' on' : ''), label);
      b.onclick = () => { setTheme(id); paint(); };
      seg.appendChild(b);
    }
  };
  paint();
}

function renderSessions(list) {
  const box = document.getElementById('acc-sessions');
  box.innerHTML = '';
  if (!list.length) { box.appendChild(h('div', 'empty', '没有登录记录')); return; }
  for (const s of list) {
    const row = h('div', 'list-row');
    const main = h('div', 'list-main');
    const title = h('div', 'list-title');
    title.appendChild(h('span', null, deviceName(s.userAgent)));
    if (s.current) {
      const b = h('span', 'badge brand', '当前设备');
      b.style.marginLeft = '8px';
      title.appendChild(b);
    }
    main.appendChild(title);
    main.appendChild(h('div', 'list-sub', (s.ip ? s.ip + ' · ' : '') + '最近 ' + fmtTime(s.lastSeenAt)));
    row.appendChild(main);
    if (!s.current) {
      const btn = h('button', 'btn sm ghost', '退出');
      btn.onclick = async () => {
        const ok = await confirmDialog({ title: '退出这台设备？', body: deviceName(s.userAgent), confirmText: '退出', danger: true });
        if (!ok) return;
        await apiPost('/api/account/sessions/revoke', { id: s.id });
        toast('已退出');
        reload();
      };
      row.appendChild(btn);
    }
    box.appendChild(row);
  }
}

function renderStats(games) {
  if (!games || !games.length) return;
  document.getElementById('stats-section').hidden = false;
  const box = document.getElementById('acc-stats');
  box.innerHTML = '';
  const names = { chess: '国际象棋' };
  for (const g of games) {
    const rate = g.total ? Math.round((g.wins / g.total) * 100) : 0;
    const row = h('div', 'list-row');
    const main = h('div', 'list-main');
    main.appendChild(h('div', 'list-title', names[g.gameId] || g.gameId));
    main.appendChild(h('div', 'list-sub', '共 ' + g.total + ' 局 · ' + g.wins + ' 胜 ' + g.losses + ' 负 ' + g.draws + ' 和'));
    row.appendChild(main);
    const v = h('div', 'setting-value');
    v.appendChild(h('b', null, g.total ? rate + '%' : '—'));
    v.appendChild(h('span', 'list-sub', ' 胜率'));
    row.appendChild(v);
    box.appendChild(row);
  }
}

async function reload() {
  const me = shell.user;
  renderHeader(me);
  const { sessions } = await apiGet('/api/account/sessions');
  renderAccountRows(me, sessions.length);
  renderSessions(sessions);
  try {
    const { games } = await apiGet('/api/account/stats');
    renderStats(games);
  } catch (e) { /* 没有战绩就不显示 */ }
}

document.getElementById('revoke-others').onclick = async () => {
  const ok = await confirmDialog({
    title: '退出其他设备？', body: '除当前设备外，其它设备都需要重新登录。', confirmText: '退出', danger: true,
  });
  if (!ok) return;
  await apiPost('/api/account/sessions/revoke', { others: true });
  toast('已退出其他设备');
  reload();
};
document.getElementById('logout-row').onclick = () => logout();

(async () => {
  if (DEMO) {
    shell.user = { id: 1, name: 'jzk', admin: true };
    renderHeader(shell.user);
    renderAccountRows(shell.user, 2);
    renderSecurity();
    renderThemeSeg();
    renderSessions([
      { id: 9, userAgent: 'Mozilla/5.0 (Macintosh) Chrome/140', ip: '10.3.0.5', lastSeenAt: Date.now(), current: true },
      { id: 7, userAgent: 'Mozilla/5.0 (iPhone) Safari/604.1', ip: '10.3.0.5', lastSeenAt: Date.now() - 2 * 86400e3, current: false },
    ]);
    renderStats([{ gameId: 'chess', total: 5, wins: 3, losses: 1, draws: 1 }]);
    document.getElementById('acc-version').textContent = 'vdev';
    return;
  }
  await boot({ active: '/account' });
  if (!shell.user) return;
  decorateSections();
  renderSecurity();
  renderThemeSeg();
  await reload();
})();
