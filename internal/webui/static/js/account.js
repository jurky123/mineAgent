// account.js：账号页 —— 资料 / 登录设备 / 退出。
import { shell, boot, logout, apiGet, apiPost, h } from './shell.js';

const qs = new URLSearchParams(location.search);
const DEMO = qs.get('ui') === '1';

function fmtTime(ms) {
  if (!ms) return '';
  const d = new Date(ms);
  const pad = (n) => String(n).padStart(2, '0');
  return d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate()) +
    ' ' + pad(d.getHours()) + ':' + pad(d.getMinutes());
}

function deviceName(ua) {
  ua = ua || '';
  let os = '未知设备';
  if (/iPhone|iPad|iPod/.test(ua)) os = 'iOS';
  else if (/Android/.test(ua)) os = 'Android';
  else if (/Macintosh/.test(ua)) os = 'macOS';
  else if (/Windows/.test(ua)) os = 'Windows';
  else if (/Linux/.test(ua)) os = 'Linux';
  let br = '浏览器';
  if (/Edg\//.test(ua)) br = 'Edge';
  else if (/Chrome\//.test(ua)) br = 'Chrome';
  else if (/Safari\//.test(ua)) br = 'Safari';
  else if (/Firefox\//.test(ua)) br = 'Firefox';
  else if (/curl/.test(ua)) br = 'curl';
  else if (/legacy-tokens/.test(ua)) br = '旧版登录（已迁移）';
  return os + ' · ' + br;
}

function renderProfile(u, sessCount) {
  const body = document.getElementById('profile-body');
  body.innerHTML = '';
  const grid = h('div', 'profile-grid');
  const add = (k, v) => {
    grid.appendChild(h('div', 'profile-k', k));
    grid.appendChild(h('div', 'profile-v', v));
  };
  add('名字', u.name);
  add('用户 ID', String(u.id));
  add('权限', u.admin ? '管理员' : '普通用户');
  add('登录设备', sessCount + ' 台');
  body.appendChild(grid);
  const out = h('button', 'btn ghost', '退出登录');
  out.onclick = () => logout();
  body.appendChild(out);
}

function renderSessions(list) {
  const host = document.getElementById('sessions');
  host.innerHTML = '';
  if (!list.length) { host.appendChild(h('div', 'empty', '没有登录记录')); return; }
  for (const s of list) {
    const row = h('div', 'device-row');
    const main = h('div', 'device-main');
    const title = h('div', 'device-title');
    title.appendChild(h('span', null, deviceName(s.userAgent)));
    if (s.current) title.appendChild(h('span', 'badge ok', '当前设备'));
    main.appendChild(title);
    main.appendChild(h('div', 'device-meta',
      (s.ip ? s.ip + ' · ' : '') + '最近 ' + fmtTime(s.lastSeenAt)));
    row.appendChild(main);
    if (!s.current) {
      const btn = h('button', 'btn ghost sm', '退出');
      btn.onclick = async () => {
        await apiPost('/api/account/sessions/revoke', { id: s.id });
        reload();
      };
      row.appendChild(btn);
    }
    host.appendChild(row);
  }
}

async function reload() {
  if (DEMO) {
    renderProfile({ id: 1, name: 'jzk', admin: true }, 2);
    renderSessions([
      { id: 9, userAgent: 'Mozilla/5.0 (Macintosh) Chrome/140', ip: '10.3.0.5', lastSeenAt: Date.now(), current: true },
      { id: 7, userAgent: 'Mozilla/5.0 (iPhone) Safari/604.1', ip: '10.3.0.5', lastSeenAt: Date.now() - 86400e3, current: false },
    ]);
    return;
  }
  const me = shell.user;
  const { sessions } = await apiGet('/api/account/sessions');
  renderProfile(me, sessions.length);
  renderSessions(sessions);
}

document.getElementById('revoke-others').onclick = async () => {
  if (!confirm('退出除当前设备外的所有登录？')) return;
  await apiPost('/api/account/sessions/revoke', { others: true });
  reload();
};

(async () => {
  await boot({ active: '/account', requireLogin: !DEMO });
  if (!DEMO && !shell.user) return;
  await reload();
})();
