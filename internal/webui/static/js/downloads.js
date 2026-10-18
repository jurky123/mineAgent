import { boot, api, apiGet, apiPost } from './shell.js';
import { h, toast, withPending, confirmDialog } from './ds.js';

const $ = (id) => document.getElementById(id);
let selected = null;
const size = (n) => (n / 1048576).toFixed(2) + ' MiB';

function showLink(path) {
  $('share-link').value = new URL(path, location.origin).href;
  $('new-link').hidden = false;
  $('share-link').focus();
  $('share-link').select();
}

async function refresh() {
  const data = await apiGet('/api/downloads');
  const list = $('files-list');
  list.replaceChildren();
  if (!data.files.length) list.appendChild(h('p', 'field-hint', '尚未上传文件。'));
  for (const file of data.files) {
    const card = h('article', 'card download-card');
    card.appendChild(h('h3', null, file.name));
    card.appendChild(h('p', null, `${size(file.size)} · ${file.enabled ? '链接可用' : '链接已停用'}`));
    card.appendChild(h('p', 'download-count', `下载请求 ${file.requests} 次 · 传输完成 ${file.completed} 次`));
    const actions = h('div', 'download-actions');
    const records = h('button', 'btn', '查看记录');
    records.onclick = () => loadEvents(file).catch(failed);
    const link = h('button', 'btn', '生成新链接');
    link.onclick = async () => {
      if (!await confirmDialog({ title: '生成新链接', body: '现有链接会立即失效，下载记录保留。', confirmText: '生成' })) return;
      await withPending(link, async () => {
        try { showLink((await apiPost('/api/downloads/link', { id: file.id })).link); await refresh(); } catch (e) { failed(e); }
      });
    };
    actions.append(records, link);
    if (file.enabled) {
      const revoke = h('button', 'btn', '停用链接');
      revoke.onclick = async () => {
        if (!await confirmDialog({ title: '停用下载链接', body: '后续下载将被拒绝；已经开始的传输和已下载文件不受影响。', confirmText: '停用', danger: true })) return;
        try { await apiPost('/api/downloads/revoke', { id: file.id }); $('new-link').hidden = true; $('share-link').value = ''; await refresh(); } catch (e) { failed(e); }
      };
      actions.appendChild(revoke);
    }
    card.appendChild(actions);
    list.appendChild(card);
  }
}

async function loadEvents(file) {
  const data = await apiGet('/api/downloads/events?id=' + encodeURIComponent(file.id));
  selected = file;
  $('events-title').textContent = file.name + ' · 下载记录';
  $('events-panel').hidden = false;
  $('events-empty').hidden = data.events.length !== 0;
  $('events-body').replaceChildren();
  const labels = { started: '传输中 / 未确认结束', completed: '服务器传输完成', interrupted: '传输中断' };
  for (const event of data.events) {
    const row = h('tr');
    for (const text of [new Date(event.startedAt).toLocaleString(), event.ip, labels[event.status] || event.status, String(event.bytesSent)]) row.appendChild(h('td', null, text));
    $('events-body').appendChild(row);
  }
}

function failed(error) { toast(error.message || '操作失败', { warn: true }); }
$('refresh-files').onclick = () => withPending($('refresh-files'), async () => {
  try { await refresh(); if (selected) await loadEvents(selected); } catch (e) { failed(e); }
});
$('copy-link').onclick = async () => {
  try { await navigator.clipboard.writeText($('share-link').value); toast('已复制'); }
  catch { $('share-link').focus(); $('share-link').select(); toast('请手动复制选中的链接'); }
};
$('upload-form').onsubmit = async (event) => {
  event.preventDefault();
  const file = $('upload-file').files[0];
  if (!file) return;
  if (file.size > 256 * 1048576 || !file.size) { toast('请选择非空、大小不超过 256 MiB 的文件', { warn: true }); return; }
  await withPending($('upload-button'), async () => {
    try {
      const data = await api('/api/downloads?name=' + encodeURIComponent(file.name), { method: 'POST', body: file, headers: { 'Content-Type': 'application/octet-stream' } });
      showLink(data.link); $('upload-file').value = ''; toast('上传完成'); await refresh();
    } catch (e) { failed(e); }
  });
};
boot({ active: 'downloads' }).then(async (user) => {
  if (!user?.admin) { $('permission').textContent = '上传文件和查看记录需要管理员权限。'; return; }
  $('permission').textContent = '';
  $('download-admin').hidden = false;
  await refresh();
}).catch(failed);
