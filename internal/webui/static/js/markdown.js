// Markdown 渲染 + 公式/代码高亮的渐进增强
// 渲染：标题/段落/列表（含有序、任务列表）/引用/表格/分隔线/代码块/图片/链接/删除线/行内样式 + LaTeX
// 增强（enhanceContent）：按需懒加载 KaTeX 与 highlight.js（自托管在 /vendor 下），渲染公式与代码高亮

import { esc } from './ui.js';

const ICON_COPY = '<svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style="vertical-align:-2px;margin-right:4px"><rect x="9" y="9" width="12" height="12" rx="2"/><path d="M5 15V5a2 2 0 0 1 2-2h10"/></svg>';
const ICON_IMG = '<svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style="vertical-align:-2px;margin-right:4px"><rect x="3" y="3" width="18" height="18" rx="2"/><circle cx="8.5" cy="8.5" r="1.5"/><path d="m21 15-5-5L5 21"/></svg>';

const blocks = []; // 占位块（代码块 / 公式），先抽出避免被 markdown 处理

function stash(html) {
  blocks.push(html);
  return '\u0000' + (blocks.length - 1) + '\u0000';
}

// ---------- 懒加载 vendor ----------
const loaded = {};
function loadCss(href) {
  if (loaded['css:' + href]) return;
  loaded['css:' + href] = true;
  const l = document.createElement('link');
  l.rel = 'stylesheet';
  l.href = href;
  document.head.appendChild(l);
}
function loadScript(src) {
  if (loaded['js:' + src]) return loaded['js:' + src];
  loaded['js:' + src] = new Promise((resolve, reject) => {
    const s = document.createElement('script');
    s.src = src;
    s.onload = resolve;
    s.onerror = () => reject(new Error('load ' + src));
    document.head.appendChild(s);
  });
  return loaded['js:' + src];
}
const vendorURL = (p) => new URL('../vendor/' + p, import.meta.url).href;

let hljsPromise = null;
function ensureHljs() {
  if (hljsPromise) return hljsPromise;
  hljsPromise = (async () => {
    loadCss(vendorURL('highlight/github.min.css'));
    loadCss(vendorURL('highlight/github-dark.min.css'));
    await loadScript(vendorURL('highlight/highlight.min.js'));
    console.debug('[mineagent] highlight.js ready', window.hljs && window.hljs.versionString);
    return window.hljs;
  })().catch((err) => { console.warn('[mineagent] highlight.js 加载失败，代码不高亮：', err); return null; });
  return hljsPromise;
}
let katexPromise = null;
function ensureKatex() {
  if (katexPromise) return katexPromise;
  katexPromise = (async () => {
    loadCss(vendorURL('katex/katex.min.css'));
    await loadScript(vendorURL('katex/katex.min.js'));
    console.debug('[mineagent] KaTeX ready', window.katex && window.katex.version);
    return window.katex;
  })().catch((err) => { console.warn('[mineagent] KaTeX 加载失败，公式按原文显示：', err); return null; });
  return katexPromise;
}

let h2cPromise = null;
function ensureHtml2Canvas() {
  if (h2cPromise) return h2cPromise;
  h2cPromise = loadScript(vendorURL('html2canvas/html2canvas.min.js'))
    .then(() => window.html2canvas)
    .catch((err) => { console.warn('[mineagent] html2canvas 加载失败，无法导出图片：', err); return null; });
  return h2cPromise;
}

// exportNodeImage 把某个元素（表格/公式）导出成 PNG 下载。
async function exportNodeImage(node, name) {
  const h2c = await ensureHtml2Canvas();
  if (!h2c) return;
  try { await document.fonts.ready; } catch (e) { /* 忽略 */ }
  const bg = getComputedStyle(document.body).backgroundColor || '#ffffff';
  const stage = document.createElement('div');
  stage.className = 'export-stage content';   // 带上 .content 才能命中表格/内容样式
  stage.style.background = bg;
  const clone = node.cloneNode(true);
  clone.querySelectorAll('.blocktools').forEach((n) => n.remove());
  stage.appendChild(clone);
  document.body.appendChild(stage);
  try {
    const canvas = await h2c(stage, { backgroundColor: bg, scale: 2, logging: false });
    await new Promise((resolve) => canvas.toBlob((blob) => {
      if (!blob) { resolve(); return; }
      const a = document.createElement('a');
      a.href = URL.createObjectURL(blob);
      a.download = name + '.png';
      document.body.appendChild(a);
      a.click();
      setTimeout(() => { URL.revokeObjectURL(a.href); a.remove(); }, 5000);
      resolve();
    }, 'image/png'));
  } catch (err) {
    console.warn('[mineagent] 导出图片失败：', err);
  } finally {
    stage.remove();
  }
}

// ---------- 渲染 ----------
export function renderContent(el, text) {
  blocks.length = 0;
  const src = String(text || '');

  // 1) 围栏代码块：语言标签 + 复制按钮（ChatGPT 那种代码框）
  let withPh = src.replace(/```(\w*)\n?([\s\S]*?)```/g, (m, lang, code) => {
    const language = (lang || '').trim().toLowerCase();
    const label = language || 'text';
    // 复制按钮/语言标签浮动在代码块内部右上角（hover 出现，触屏常显）
    return stash(
      '<div class="codeblock">' +
      '<pre><code class="language-' + esc(label) + '">' + esc(code.replace(/\n$/, '')) + '</code></pre>' +
      '<div class="codetools"><span class="codelang">' + esc(label) + '</span>' +
      '<button class="codecopy" type="button">' + ICON_COPY + '复制</button></div>' +
      '</div>'
    );
  });

  // 2) 块级公式：$$...$$（可跨行）与 \[...\]
  const mathBlock = (tex) =>
    stash('<div class="blockwrap" data-kind="math">' +
      '<div class="blocktools"><button class="blockimg" type="button">' + ICON_IMG + '导出图片</button></div>' +
      '<span class="math-display" data-tex="' + esc(tex.trim()) + '"></span></div>');
  withPh = withPh.replace(/\$\$([\s\S]+?)\$\$/g, (m, tex) => mathBlock(tex));
  withPh = withPh.replace(/\\\[([\s\S]+?)\\\]/g, (m, tex) => mathBlock(tex));

  const lines = withPh.split('\n');
  const out = [];
  const inline = (s) => esc(s)
    // 行内公式：$...$ 与 \(...\)（先抽走，避免 _ * 等被 markdown 处理）
    .replace(/\$([^$\n]{1,200}?)\$/g, (m, tex) =>
      stash('<span class="math-inline" data-tex="' + esc(tex) + '"></span>'))
    .replace(/\\\(([^\n]{1,200}?)\\\)/g, (m, tex) =>
      stash('<span class="math-inline" data-tex="' + esc(tex) + '"></span>'))
    .replace(/`([^`]+)`/g, '<code>$1</code>')
    .replace(/!\[([^\]]*)\]\((https?:[^)\s]+)\)/g, '<img class="mdimg" src="$2" alt="$1" loading="lazy">')
    .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
    .replace(/(^|[\s(（])\*([^*\s][^*]*)\*/g, '$1<em>$2</em>')
    .replace(/~~([^~]+)~~/g, '<del>$1</del>')
    .replace(/\[([^\]]+)\]\((https?:[^)\s]+)\)/g, '<a href="$2" target="_blank" rel="noopener">$1</a>')
    .replace(/(^|[^"'>\w])(https?:\/\/[^\s<]+)/g, '$1<a href="$2" target="_blank" rel="noopener">$2</a>');
  const cells = (line) => line.trim().replace(/^\||\|$/g, '').split('|').map((c) => c.trim());
  const isBlockStart = (t) => t === '' || /^\u0000\d+\u0000$/.test(t) || /^#{1,6}\s/.test(t) ||
    /^>\s?/.test(t) || /^[-*]\s+/.test(t) || /^\d+[.)]\s+/.test(t) || /^(-{3,}|\*{3,}|_{3,})$/.test(t);
  const taskItem = (x) => {
    const m = x.match(/^\[([ xX])\]\s*(.*)$/);
    if (!m) return '<li>' + inline(x) + '</li>';
    const done = m[1].toLowerCase() === 'x';
    return '<li class="task' + (done ? ' done' : '') + '"><span class="taskbox">' + (done ? '✓' : '') + '</span>' + inline(m[2]) + '</li>';
  };
  let i = 0;
  while (i < lines.length) {
    const t = lines[i].trim();
    if (t === '') { i++; continue; }
    if (/^\u0000\d+\u0000$/.test(t)) { out.push('<div>' + t + '</div>'); i++; continue; }
    const h = t.match(/^(#{1,6})\s+(.*)$/);
    if (h) { const lv = Math.min(h[1].length + 1, 6); out.push('<h' + lv + '>' + inline(h[2]) + '</h' + lv + '>'); i++; continue; }
    if (/^(-{3,}|\*{3,}|_{3,})$/.test(t)) { out.push('<hr>'); i++; continue; }
    if (/^>\s?/.test(t)) {
      const buf = [];
      while (i < lines.length && /^>\s?/.test(lines[i].trim())) { buf.push(lines[i].trim().replace(/^>\s?/, '')); i++; }
      out.push('<blockquote>' + buf.map((x) => '<div>' + inline(x) + '</div>').join('') + '</blockquote>');
      continue;
    }
    if (t.includes('|') && i + 1 < lines.length && /^\s*\|?[\s:|-]*-[\s:|-]*\|?[\s:|-]*$/.test(lines[i + 1].trim()) && lines[i + 1].includes('-')) {
      const head = cells(t); i += 2;
      const rows = [];
      while (i < lines.length && lines[i].includes('|') && lines[i].trim() !== '') { rows.push(cells(lines[i])); i++; }
      out.push('<div class="blockwrap" data-kind="table">' +
        '<div class="blocktools"><button class="blockimg" type="button">' + ICON_IMG + '导出图片</button></div>' +
        '<div class="tablewrap"><table><thead><tr>' + head.map((c) => '<th>' + inline(c) + '</th>').join('') + '</tr></thead><tbody>' +
        rows.map((r) => '<tr>' + r.map((c) => '<td>' + inline(c) + '</td>').join('') + '</tr>').join('') + '</tbody></table></div></div>');
      continue;
    }
    if (/^[-*]\s+/.test(t)) {
      const items = [];
      while (i < lines.length && /^\s*[-*]\s+/.test(lines[i])) { items.push(lines[i].trim().replace(/^\s*[-*]\s+/, '')); i++; }
      out.push('<ul>' + items.map(taskItem).join('') + '</ul>');
      continue;
    }
    if (/^\d+[.)]\s+/.test(t)) {
      const items = [];
      while (i < lines.length && /^\s*\d+[.)]\s+/.test(lines[i])) { items.push(lines[i].trim().replace(/^\s*\d+[.)]\s+/, '')); i++; }
      out.push('<ol>' + items.map((x) => '<li>' + inline(x) + '</li>').join('') + '</ol>');
      continue;
    }
    const buf = [t];
    i++;
    while (i < lines.length && !isBlockStart(lines[i].trim())) { buf.push(lines[i].trim()); i++; }
    out.push('<p>' + buf.map((x) => inline(x)).join('<br>') + '</p>');
  }
  el.innerHTML = out.join('').replace(/\u0000(\d+)\u0000/g, (m, n) => (blocks[+n] !== undefined ? blocks[+n] : m));
}

// ---------- 渐进增强：代码高亮 + 公式渲染 + 复制按钮 ----------
export async function enhanceContent(el) {
  const codes = el.querySelectorAll('pre code[class]');
  if (codes.length) {
    const hljs = await ensureHljs();
    if (hljs) codes.forEach((c) => { try { hljs.highlightElement(c); } catch (e) { /* 忽略未知语言 */ } });
  }
  const maths = el.querySelectorAll('.math-inline, .math-display');
  if (maths.length) {
    const katex = await ensureKatex();
    maths.forEach((n) => {
      const tex = n.dataset.tex || '';
      if (!katex) { n.textContent = tex; return; }
      try {
        katex.render(tex, n, { displayMode: n.classList.contains('math-display'), throwOnError: false });
      } catch (e) { n.textContent = tex; }
    });
  }
  // 表格 / 块级公式：导出图片
  el.querySelectorAll('.blockwrap').forEach((wrap) => {
    const btn = wrap.querySelector('.blockimg');
    if (!btn || btn._bound) return;
    btn._bound = true;
    btn.onclick = () => {
      const kind = wrap.dataset.kind === 'table' ? 'table' : 'formula';
      const target = wrap.querySelector('.tablewrap') || wrap.querySelector('.math-display') || wrap;
      const stamp = new Date().toISOString().slice(0, 19).replace(/[:T]/g, '-');
      exportNodeImage(target, kind + '-' + stamp);
    };
  });
  el.querySelectorAll('.codeblock').forEach((box) => {
    const btn = box.querySelector('.codecopy');
    if (!btn || btn._bound) return;
    btn._bound = true;
    btn.onclick = () => {
      const code = box.querySelector('code');
      navigator.clipboard.writeText(code ? code.textContent : '').then(() => {
        btn.textContent = '已复制'; setTimeout(() => { btn.textContent = '复制'; }, 1200);
      });
    };
  });
}
