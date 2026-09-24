// Markdown 渲染 + 公式/代码高亮的渐进增强
// 渲染：标题/段落/列表（含有序、任务列表）/引用/表格/分隔线/代码块/图片/链接/删除线/行内样式 + LaTeX
// 增强（enhanceContent）：按需懒加载 KaTeX 与 highlight.js（自托管在 /vendor 下），渲染公式与代码高亮

import { esc } from './ui.js';

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
    return window.hljs;
  })().catch(() => null);
  return hljsPromise;
}
let katexPromise = null;
function ensureKatex() {
  if (katexPromise) return katexPromise;
  katexPromise = (async () => {
    loadCss(vendorURL('katex/katex.min.css'));
    await loadScript(vendorURL('katex/katex.min.js'));
    return window.katex;
  })().catch(() => null);
  return katexPromise;
}

// ---------- 渲染 ----------
export function renderContent(el, text) {
  blocks.length = 0;
  const src = String(text || '');

  // 1) 围栏代码块：语言标签 + 复制按钮（ChatGPT 那种代码框）
  let withPh = src.replace(/```(\w*)\n?([\s\S]*?)```/g, (m, lang, code) => {
    const language = (lang || '').trim().toLowerCase();
    const label = language || 'text';
    return stash(
      '<div class="codeblock">' +
      '<div class="codehead"><span class="codelang">' + esc(label) + '</span>' +
      '<button class="codecopy" type="button">复制</button></div>' +
      '<pre><code class="language-' + esc(label) + '">' + esc(code.replace(/\n$/, '')) + '</code></pre>' +
      '</div>'
    );
  });

  // 2) 块级公式 $$...$$（可跨行）
  withPh = withPh.replace(/\$\$([\s\S]+?)\$\$/g, (m, tex) =>
    stash('<span class="math-display" data-tex="' + esc(tex.trim()) + '"></span>'));

  const lines = withPh.split('\n');
  const out = [];
  const inline = (s) => esc(s)
    // 行内公式 $...$（先抽走，避免 _ * 等被 markdown 处理）
    .replace(/\$([^$\n]{1,200}?)\$/g, (m, tex) =>
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
      out.push('<table><thead><tr>' + head.map((c) => '<th>' + inline(c) + '</th>').join('') + '</tr></thead><tbody>' +
        rows.map((r) => '<tr>' + r.map((c) => '<td>' + inline(c) + '</td>').join('') + '</tr>').join('') + '</tbody></table>');
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
