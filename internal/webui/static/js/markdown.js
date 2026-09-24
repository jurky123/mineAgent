// 轻量 Markdown 渲染：标题/列表/引用/表格/分隔线/段落 + 行内样式
// 先抽出围栏代码块，再按行做块级解析，最后行内处理（转义优先，安全）。

import { esc } from './ui.js';

export function renderContent(el, text) {
  const src = String(text || '');
  const blocks = [];
  const withPh = src.replace(/```(\w*)\n?([\s\S]*?)```/g, (m, lang, code) => {
    blocks.push('<pre><code>' + esc(code.replace(/\n$/, '')) + '</code></pre>');
    return '\u0000' + (blocks.length - 1) + '\u0000';
  });
  const lines = withPh.split('\n');
  const out = [];
  const inline = (s) => esc(s)
    .replace(/`([^`]+)`/g, '<code>$1</code>')
    .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
    .replace(/(^|[\s(（])\*([^*\s][^*]*)\*/g, '$1<em>$2</em>')
    .replace(/\[([^\]]+)\]\((https?:[^)\s]+)\)/g, '<a href="$2" target="_blank" rel="noopener">$1</a>')
    .replace(/(^|[^"'>\w])(https?:\/\/[^\s<]+)/g, '$1<a href="$2" target="_blank" rel="noopener">$2</a>');
  const cells = (line) => line.trim().replace(/^\||\|$/g, '').split('|').map((c) => c.trim());
  const isBlockStart = (t) => t === '' || /^\u0000\d+\u0000$/.test(t) || /^#{1,6}\s/.test(t) ||
    /^>\s?/.test(t) || /^[-*]\s+/.test(t) || /^\d+[.)]\s+/.test(t) || /^(-{3,}|\*{3,}|_{3,})$/.test(t);
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
      out.push('<ul>' + items.map((x) => '<li>' + inline(x) + '</li>').join('') + '</ul>');
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
  el.innerHTML = out.join('').replace(/\u0000(\d+)\u0000/g, (m, n) => blocks[+n]);
}
