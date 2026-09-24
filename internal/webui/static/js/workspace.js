// Workspace 浏览器（管理员）：目录导航 + 下载/预览

import { $, esc, fmtSize, hideAnimated, showError, ICON, extInfo } from './ui.js';
import { withTok } from './state.js';
import { api } from './api.js';
import { openViewer } from './viewer.js';

let wsPath = '';

export function openWorkspace() {
  $('wsmodal').classList.add('on');
  load();
}

export function initWorkspace() {
  $('ws-close').onclick = () => hideAnimated($('wsmodal'));
  $('ws-refresh').onclick = () => load();
  $('wsmodal').addEventListener('click', (e) => { if (e.target === $('wsmodal')) hideAnimated($('wsmodal')); });
}

async function load() {
  const box = $('ws-list');
  box.innerHTML = '<div class="ws-empty">加载中…</div>';
  try {
    const res = await api('/api/workspace?path=' + encodeURIComponent(wsPath));
    renderPath(res.path || '');
    box.innerHTML = '';
    const entries = res.entries || [];
    if (wsPath) {
      const up = document.createElement('button');
      up.className = 'ws-row';
      up.innerHTML = ICON.folder + '<span class="ws-name">..</span>';
      up.onclick = () => { wsPath = wsPath.split('/').slice(0, -1).join('/'); load(); };
      box.appendChild(up);
    }
    if (!entries.length) box.innerHTML = '<div class="ws-empty">这里是空的</div>';
    entries.forEach((en) => {
      const full = (res.path ? res.path + '/' : '') + en.name;
      const row = document.createElement('a');
      row.className = 'ws-row';
      if (en.dir) {
        row.innerHTML = ICON.folder + '<span class="ws-name">' + esc(en.name) + '</span>';
        row.onclick = (e) => { e.preventDefault(); wsPath = full; load(); };
      } else {
        const [label, cls] = extInfo(en.name);
        const url = withTok('/api/workspace/file?path=' + encodeURIComponent(full));
        row.href = url;
        row.setAttribute('download', en.name);
        row.innerHTML = '<span class="ext ' + cls + '" style="width:26px;height:26px;font-size:8px">' + label + '</span>' +
          '<span class="ws-name">' + esc(en.name) + '</span><span class="ws-size">' + fmtSize(en.size || 0) + '</span>';
        if (/\.(png|jpe?g|gif|webp|bmp|avif)$/i.test(en.name)) {
          row.onclick = (e) => { e.preventDefault(); openViewer([{ url: url, name: en.name }], 0); };
        }
      }
      box.appendChild(row);
    });
  } catch (e) {
    box.innerHTML = '<div class="ws-empty">' + esc(e.message) + '</div>';
  }
}

function renderPath(rel) {
  const parts = rel ? rel.split('/') : [];
  let html = '<a data-p="">workspace</a>';
  let acc = '';
  parts.forEach((seg) => { acc = acc ? acc + '/' + seg : seg; html += ' / <a data-p="' + esc(acc) + '">' + esc(seg) + '</a>'; });
  const el = $('ws-path');
  el.innerHTML = html;
  el.querySelectorAll('a').forEach((a) => { a.onclick = (e) => { e.preventDefault(); wsPath = a.dataset.p || ''; load(); }; });
}
