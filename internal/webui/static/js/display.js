// display.js — 显示屏管理：屏幕列表 + 导航白名单。
// 数据走门户 /api/display/*（转发到 MineDisplay 本地控制 API，令牌由服务端读取）。
import { boot, apiGet, apiPost } from './shell.js';
import { h, toast, withPending, confirmDialog } from './ds.js';

const $ = (id) => document.getElementById(id);
let refreshing = false;

boot({ active: 'screens' }).then(() => {
  refresh();
  setInterval(refresh, 5000);
});

async function refresh() {
  if (refreshing) return;
  refreshing = true;
  try {
    const st = await apiGet('/api/display/status');
    render(st);
  } catch (e) {
    const meta = $('screens-meta');
    meta.textContent = '控制 API 不可用';
    meta.classList.add('warn');
  } finally {
    refreshing = false;
  }
}

function render(st) {
  const meta = $('screens-meta');
  meta.classList.remove('warn');
  meta.textContent = `代理端口 ${st.proxyPort} · 浏览器客户端 ${st.clients}`;

  const list = $('screens-list');
  list.innerHTML = '';
  if (!st.screens || !st.screens.length) {
    list.appendChild(h('div', 'screens-empty', '还没有浏览器屏，下面输入 ID 和网址创建一个。'));
  }
  for (const s of st.screens || []) list.appendChild(screenRow(s));

  $('wl-enabled').checked = !!st.whitelistEnabled;
  const chips = $('wl-hosts');
  chips.innerHTML = '';
  if (!st.hosts || !st.hosts.length) {
    chips.appendChild(h('span', 'screens-empty', '（没有加白名单）'));
  }
  for (const host of st.hosts || []) chips.appendChild(hostChip(host));
}

function screenRow(s) {
  const row = h('div', 'screens-row');
  const main = h('div', 'screens-row-main');
  main.appendChild(h('div', 'screens-row-title', `${s.id} · ${s.host || s.url}`));
  const bits = ['r' + s.revision];
  if (s.placed) {
    bits.push(`已摆放 ${fmt(s.width)}×${fmt(s.height)} ${s.facing}`);
  } else {
    bits.push('未摆放（游戏里 browser place）');
  }
  main.appendChild(h('div', 'screens-row-sub', bits.join(' · ')));
  row.appendChild(main);

  const close = h('button', 'btn', '关闭');
  close.onclick = async () => {
    const ok = await confirmDialog({
      title: '关闭屏幕',
      body: `确定关闭「${s.id}」？世界里的屏会消失。`,
      confirmText: '关闭',
      danger: true,
    });
    if (!ok) return;
    await withPending(close, async () => {
      try {
        await apiPost('/api/display/close', { id: s.id });
        toast('已关闭 ' + s.id);
        await refresh();
      } catch (e) {
        toast(e.message || '操作失败', { warn: true });
      }
    });
  };
  row.appendChild(close);
  return row;
}

function hostChip(host) {
  const chip = h('button', 'screens-chip', host + ' ×');
  chip.title = '点击移除';
  chip.onclick = async () => {
    try {
      await apiPost('/api/display/whitelist', { action: 'remove', host });
      toast('已移除 ' + host);
      await refresh();
    } catch (e) {
      toast(e.message || '操作失败', { warn: true });
    }
  };
  return chip;
}

$('open-btn').onclick = async () => {
  const id = $('open-id').value.trim();
  const url = $('open-url').value.trim();
  if (!id || !url) {
    toast('先填屏幕 ID 和网址', { warn: true });
    return;
  }
  await withPending($('open-btn'), async () => {
    try {
      await apiPost('/api/display/open', { id, url });
      toast('已打开/更新 ' + id);
      await refresh();
    } catch (e) {
      toast(e.message || '操作失败', { warn: true });
    }
  });
};

$('wl-add').onclick = async () => {
  const host = $('wl-host').value.trim();
  if (!host) {
    toast('先填一个 host', { warn: true });
    return;
  }
  try {
    await apiPost('/api/display/whitelist', { action: 'add', host });
    $('wl-host').value = '';
    toast('已加入 ' + host);
    await refresh();
  } catch (e) {
    toast(e.message || '操作失败', { warn: true });
  }
};

$('wl-presets').onclick = async () => {
  await withPending($('wl-presets'), async () => {
    try {
      const r = await apiPost('/api/display/whitelist', { action: 'presets' });
      toast(`已加入常用站 ${r.added || 0} 个`);
      await refresh();
    } catch (e) {
      toast(e.message || '操作失败', { warn: true });
    }
  });
};

$('wl-enabled').onchange = async () => {
  const enabled = $('wl-enabled').checked;
  try {
    await apiPost('/api/display/whitelist', { action: 'enabled', enabled });
    toast(enabled ? '白名单已启用' : '白名单已关闭');
  } catch (e) {
    toast(e.message || '操作失败', { warn: true });
    $('wl-enabled').checked = !enabled;
  }
};

function fmt(v) {
  return Number.isInteger(v) ? String(v) : String(Math.round(v * 10) / 10);
}
