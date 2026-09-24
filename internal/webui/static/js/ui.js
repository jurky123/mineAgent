// 通用 UI 工具：DOM、格式化、toast、图标、文件类型

export const $ = (id) => document.getElementById(id);

export const bus = {
  m: new Map(),
  on(evt, fn) {
    if (!this.m.has(evt)) this.m.set(evt, []);
    this.m.get(evt).push(fn);
  },
  emit(evt, data) {
    (this.m.get(evt) || []).forEach((fn) => {
      try { fn(data); } catch (e) { console.warn(e); }
    });
  }
};

export function esc(s) {
  return String(s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

export function fmtSize(n) {
  if (!n && n !== 0) return '';
  if (n < 1024) return n + ' B';
  if (n < 1048576) return (n / 1024).toFixed(1) + ' KB';
  return (n / 1048576).toFixed(1) + ' MB';
}

export function fmtTime(ms) {
  const d = new Date(ms || Date.now());
  return d.toTimeString().slice(0, 5);
}

export function toast(msg, type) {
  const box = $('toasts');
  const t = document.createElement('div');
  t.className = 'toast ' + (type || '');
  t.textContent = msg;
  t.onclick = () => { t.classList.add('out'); setTimeout(() => t.remove(), 200); };
  box.appendChild(t);
  while (box.children.length > 3) box.firstChild.remove();
  setTimeout(() => { t.classList.add('out'); setTimeout(() => t.remove(), 200); }, 3200);
}

export function showError(msg) { toast(msg, 'err'); }

export function hideAnimated(el, done) {
  if (!el.classList.contains('on')) { if (done) done(); return; }
  el.classList.add('closing');
  setTimeout(() => { el.classList.remove('on', 'closing'); if (done) done(); }, 140);
}

export const ICON = {
  copy: '<svg class="i"><use href="#i-copy"/></svg>',
  download: '<svg class="i"><use href="#i-download"/></svg>',
  expand: '<svg class="i"><use href="#i-expand"/></svg>',
  file: '<svg class="i lg"><use href="#i-file"/></svg>',
  folder: '<svg class="i lg"><use href="#i-folder"/></svg>',
  check: '<svg class="i"><use href="#i-check"/></svg>',
  x: '<svg class="i"><use href="#i-x"/></svg>',
  trash: '<svg class="i"><use href="#i-trash"/></svg>',
  edit: '<svg class="i"><use href="#i-edit"/></svg>'
};

// 按扩展名给出文件卡片角标（文字 + 配色 class）
export function extInfo(name) {
  const ext = (String(name || '').split('.').pop() || '').toLowerCase();
  const map = {
    pdf: ['PDF', 'pdf'], doc: ['DOC', 'doc'], docx: ['DOC', 'doc'], rtf: ['RTF', 'doc'],
    xls: ['XLS', 'xls'], xlsx: ['XLS', 'xls'], csv: ['CSV', 'xls'],
    ppt: ['PPT', 'ppt'], pptx: ['PPT', 'ppt'],
    txt: ['TXT', 'txt'], md: ['MD', 'txt'], log: ['LOG', 'txt'],
    json: ['JSON', 'code'], js: ['JS', 'code'], ts: ['TS', 'code'], py: ['PY', 'code'],
    java: ['JAVA', 'code'], go: ['GO', 'code'], sh: ['SH', 'code'], html: ['HTML', 'code'],
    css: ['CSS', 'code'], xml: ['XML', 'code'], yml: ['YML', 'code'], yaml: ['YML', 'code'],
    zip: ['ZIP', 'zip'], rar: ['RAR', 'zip'], '7z': ['7Z', 'zip'], tar: ['TAR', 'zip'], gz: ['GZ', 'zip'],
    mp3: ['MP3', 'audio'], wav: ['WAV', 'audio'], m4a: ['M4A', 'audio'], flac: ['FLAC', 'audio'],
    mp4: ['MP4', 'video'], mov: ['MOV', 'video'], mkv: ['MKV', 'video'], webm: ['WEBM', 'video']
  };
  if (map[ext]) return map[ext];
  return [ext ? ext.slice(0, 4).toUpperCase() : 'FILE', 'txt'];
}
