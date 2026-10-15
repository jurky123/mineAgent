// account.js — 设置页：账号 / 外观 / 安全 / 设备 / 游戏 / 关于。
import { shell, boot, apiGet, apiPost, logout, themeMode, setTheme, applyTheme } from './shell.js';
import { h, icon, toast, confirmDialog, notifyEnabled, setNotifyEnabled, segThumb, reveal } from './ds.js';
import { sfxEnabled, setSfxEnabled } from './sfx.js';

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
  const av = document.getElementById('acc-avatar');
  av.textContent = (u.name[0] || '?').toUpperCase();
  av.style.viewTransitionName = 'user-avatar';
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
  const box = document.getElementById('acc-security'); box.replaceChildren();
  const row = h('div', 'list-row'); const main = h('div', 'list-main');
  main.append(h('div', 'list-title', 'Passkey 登录'), h('div', 'list-sub', '使用设备解锁或安全密钥验证身份；名字只用于显示。'));
  row.append(main, h('span', 'badge', '已启用')); box.appendChild(row);
  const note = h('p', 'list-sub', '恢复码只在激活或恢复时显示一次，请离线保管。Passkey 丢失后，退出并使用登录页的恢复入口；恢复成功会撤销旧凭据与所有旧登录。没有恢复码时需管理员核验归属。'); box.appendChild(note);
}

// segControl 分段开关：只建一次，之后只切 .on，选中背景由滑块滑过去（不再整段重建）
function segControl(seg, options, get, set) {
  seg.innerHTML = '';
  let syncThumb = () => {};
  const items = options.map(([val, label]) => {
    const b = h('button', 'seg-item', label);
    b.onclick = async () => { await set(val); sync(); };
    seg.appendChild(b);
    return [val, b];
  });
  const sync = () => {
    for (const [val, b] of items) b.classList.toggle('on', get() === val);
    syncThumb();
  };
  sync();
  syncThumb = segThumb(seg);
}

function renderThemeSeg() {
  segControl(document.getElementById('theme-seg'),
    [['system', '跟随系统'], ['light', '浅色'], ['dark', '深色']], themeMode, setTheme);
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
  const box = document.getElementById('acc-stats');
  box.innerHTML = '';
  if (!games || !games.length) return;
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

let firstLoad = true;   // 只有首次加载做淡入，之后刷新（如退出其他设备）直接替换
const kids = (...ids) => ids.flatMap((id) => [...document.getElementById(id).children]);

async function reload() {
  const me = shell.user;
  renderHeader(me);
  const [{ sessions }, stats] = await Promise.all([
    apiGet('/api/account/sessions'),
    apiGet('/api/account/stats').catch(() => null),   // 没有战绩就不显示
  ]);
  renderAccountRows(me, sessions.length);
  renderSessions(sessions);
  if (stats) renderStats(stats.games);
  if (firstLoad) reveal(kids('acc-rows', 'acc-sessions', 'acc-stats'));
  firstLoad = false;
}

// 桌面通知开关（需要浏览器授权；默认关）
(function bindNotifyToggle() {
  const box = document.getElementById('acc-notify');
  if (!box) return;
  const seg = h('div', 'seg');
  box.appendChild(seg);
  segControl(seg, [[true, '开'], [false, '关']], notifyEnabled, async (val) => {
    const ok = await setNotifyEnabled(val);
    if (val && !ok) toast('浏览器没有授权通知', { warn: true });
  });
})();

// 游戏音效开关（A/B 之外最实用的设置；默认开）
(function bindSfxToggle() {
  const box = document.getElementById('acc-sfx');
  if (!box) return;
  const seg = h('div', 'seg');
  box.appendChild(seg);
  segControl(seg, [[true, '开'], [false, '关']], sfxEnabled, setSfxEnabled);
})();

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
document.getElementById('history-row').onclick = () => { location.href = '/account/history'; };

(async () => {
  if (DEMO) {
    await boot({ active: '/account', preview: true });
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
