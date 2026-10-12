// account.js — 设置页：账号 / 外观 / 安全 / 设备 / 游戏 / 关于。
import { shell, boot, apiGet, apiPost, logout, themeMode, setTheme, applyTheme } from './shell.js';
import { h, icon, toast, confirmDialog, openDialog, notifyEnabled, setNotifyEnabled, segThumb, reveal } from './ds.js';
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
    '人设': 'sparkle', '系统提示词': 'settings',
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

// ---------- 人设（模板 + 按服务切换）+ 系统提示词（管理员） ----------

let PROMPT_DATA = null;

async function renderPrompts() {
  let data;
  try {
    data = await apiGet('/api/prompts');
  } catch (e) {
    return; // 接口不可用时静默，不影响设置页其它部分
  }
  PROMPT_DATA = data;
  const rows = document.getElementById('personas-rows');
  rows.innerHTML = '';

  // 我的人设（网页）：空值 = 跟随服务默认
  rows.appendChild(personaSelectRow('我在网页端的人设', '只影响你自己的网页对话', false, data.myPersona || '', async (v) => {
    await apiPost('/api/prompts', { scope: 'user-persona', persona: v });
    toast('已切换');
  }));

  const adminSection = document.getElementById('prompts-admin-section');
  const manageBtn = document.getElementById('persona-manage');
  if (data.admin) {
    manageBtn.hidden = false;
    for (const svc of data.services || []) {
      rows.appendChild(personaSelectRow(svc.name, svc.desc || '', true, svc.persona || '', async (v) => {
        await apiPost('/api/prompts', { scope: 'service-persona', id: svc.id, persona: v });
        toast('已切换 ' + svc.name + ' 的人设');
      }));
    }
    adminSection.hidden = false;
    const box = document.getElementById('prompts-admin');
    box.innerHTML = '';
    for (const svc of data.services || []) box.appendChild(capabilityRow(svc));
  } else {
    manageBtn.hidden = true;
    adminSection.hidden = true;
  }
}

function personaSelectRow(title, sub, isService, value, onPick) {
  const row = h('div', 'list-row');
  const main = h('div', 'list-main');
  main.appendChild(h('div', 'list-title', title));
  if (sub) main.appendChild(h('div', 'list-sub', sub));
  row.appendChild(main);

  const sel = document.createElement('select');
  sel.className = 'input';
  sel.style.width = 'auto';
  sel.style.minWidth = '9rem';
  const none = document.createElement('option');
  none.value = '';
  none.textContent = isService ? '服务默认' : '跟随服务默认';
  sel.appendChild(none);
  for (const p of PROMPT_DATA.personas || []) {
    const o = document.createElement('option');
    o.value = p.id;
    o.textContent = p.name;
    sel.appendChild(o);
  }
  sel.value = value;
  sel.onchange = async () => {
    try {
      await onPick(sel.value);
    } catch (e) {
      toast(e.message || '保存失败', { warn: true });
      renderPrompts();
    }
  };
  row.appendChild(sel);
  return row;
}

function capabilityRow(svc) {
  const g = svc.global || {};
  const custom = !!(g.enabled && (g.text || '').trim());
  const row = h('div', 'list-row');
  const main = h('div', 'list-main');
  main.appendChild(h('div', 'list-title', svc.name));
  if (svc.desc) main.appendChild(h('div', 'list-sub', svc.desc));
  row.appendChild(main);
  row.appendChild(h('span', 'badge' + (custom ? ' brand' : ''), custom ? '已自定义' : '内置默认'));
  const btn = h('button', 'btn sm', '编辑');
  btn.onclick = () => openCapabilityEditor(svc);
  row.appendChild(btn);
  return row;
}

function openCapabilityEditor(svc) {
  const g = svc.global || {};
  let enabled = !!(g.enabled && (g.text || '').trim());

  const body = h('div');
  const seg = h('div', 'seg');
  seg.style.marginBottom = '10px';
  const bOff = h('button', 'seg-item', '用内置默认');
  const bOn = h('button', 'seg-item', '使用自定义');
  const ta = h('textarea', 'textarea');
  ta.value = g.text || '';
  ta.rows = 14;
  ta.placeholder = '留空并保存 = 恢复内置默认';
  const sync = () => {
    bOff.classList.toggle('on', !enabled);
    bOn.classList.toggle('on', enabled);
    ta.style.display = enabled ? '' : 'none';
  };
  bOff.onclick = () => { enabled = false; sync(); };
  bOn.onclick = () => { enabled = true; sync(); };
  seg.appendChild(bOff);
  seg.appendChild(bOn);
  body.appendChild(seg);

  if (svc.default) {
    const det = h('details');
    det.style.margin = '0 0 10px';
    det.appendChild(h('summary', 'list-sub', '查看内置系统提示词'));
    const pre = h('pre', 'list-sub', svc.default);
    pre.style.whiteSpace = 'pre-wrap';
    pre.style.maxHeight = '180px';
    pre.style.overflow = 'auto';
    det.appendChild(pre);
    body.appendChild(det);
  }
  body.appendChild(ta);
  sync();

  openDialog({
    title: '系统提示词 · ' + svc.name,
    width: 680,
    body,
    actions: [
      {
        label: '恢复默认', danger: true,
        onClick: async () => {
          await apiPost('/api/prompts', { scope: 'capability', id: svc.id, text: '' });
          toast('已恢复内置');
          renderPrompts();
        },
      },
      {
        label: '保存', primary: true,
        onClick: async () => {
          try {
            await apiPost('/api/prompts', { scope: 'capability', id: svc.id, enabled, text: ta.value });
            toast('已保存');
            renderPrompts();
          } catch (e) {
            toast(e.message || '保存失败', { warn: true });
            return false;
          }
        },
      },
    ],
  });
}

// 人设模板管理（管理员）
function openPersonaManager() {
  const body = h('div');
  const list = h('div', 'settings');
  const render = () => {
    list.innerHTML = '';
    for (const p of (PROMPT_DATA && PROMPT_DATA.personas) || []) {
      const row = h('div', 'list-row');
      const main = h('div', 'list-main');
      main.appendChild(h('div', 'list-title', p.name));
      if (p.desc) main.appendChild(h('div', 'list-sub', p.desc));
      row.appendChild(main);
      const edit = h('button', 'btn sm', '编辑');
      edit.onclick = () => openPersonaEdit(p, reloadPersonas);
      row.appendChild(edit);
      const del = h('button', 'btn sm' + (p.builtin ? '' : ' danger'), p.builtin ? '恢复默认' : '删除');
      del.onclick = async () => {
        const ok = await confirmDialog({
          title: p.builtin ? '恢复内置人设？' : '删除人设？',
          body: p.name, confirmText: p.builtin ? '恢复' : '删除', danger: true,
        });
        if (!ok) return;
        try {
          await apiPost('/api/prompts', { scope: 'persona-delete', id: p.id });
          toast('已处理');
          await reloadPersonas();
        } catch (e) {
          toast(e.message || '操作失败', { warn: true });
        }
      };
      row.appendChild(del);
      list.appendChild(row);
    }
  };
  const reloadPersonas = async () => {
    PROMPT_DATA = await apiGet('/api/prompts');
    render();
    renderPrompts();
  };
  render();
  body.appendChild(list);
  openDialog({
    title: '人设模板', width: 620, body,
    actions: [
      { label: '新建人设', onClick: () => { openPersonaEdit({ id: '', name: '', desc: '', text: '' }, reloadPersonas); return false; } },
      { label: '关闭', primary: true },
    ],
  });
}

function openPersonaEdit(p, onSaved) {
  const body = h('div');
  const name = h('input', 'input');
  name.type = 'text';
  name.value = p.name || '';
  name.placeholder = '名称，如：毒舌损友';
  name.maxLength = 40;
  const desc = h('input', 'input');
  desc.type = 'text';
  desc.value = p.desc || '';
  desc.placeholder = '一句话简介';
  desc.maxLength = 200;
  desc.style.marginTop = '8px';
  const ta = h('textarea', 'textarea');
  ta.value = p.text || '';
  ta.rows = 10;
  ta.placeholder = '人设正文：你是谁、什么性格、怎么说话（只写角色，能力与规则由系统提示词保证）';
  ta.style.marginTop = '8px';
  body.appendChild(name);
  body.appendChild(desc);
  body.appendChild(ta);

  openDialog({
    title: p.id ? '编辑人设' : '新建人设',
    width: 620,
    body,
    actions: [
      {
        label: '保存', primary: true,
        onClick: async () => {
          try {
            await apiPost('/api/prompts', {
              scope: 'persona-save',
              data: { id: p.id || '', name: name.value, desc: desc.value, text: ta.value },
            });
            toast('已保存');
            if (onSaved) onSaved();
            renderPrompts();
          } catch (e) {
            toast(e.message || '保存失败', { warn: true });
            return false;
          }
        },
      },
    ],
  });
}

(function bindPersonaManager() {
  const btn = document.getElementById('persona-manage');
  if (btn) btn.onclick = openPersonaManager;
})();
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
  await renderPrompts();
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
