// Public release data shared by the homepage and installation guide.
import { h, icon } from './ds.js';
import { pageVersion } from './shell.js';

export async function loadMinecraftRelease() {
  const response = await fetch('/static/' + pageVersion() + '/minecraft/release.json', { cache: 'no-store' });
  if (!response.ok) throw new Error('发布信息暂时无法读取，请稍后重试');
  return await response.json();
}

function safeURL(value) {
  if (!value) return null;
  try {
    const url = new URL(value, location.origin);
    decodeURIComponent(url.pathname);
    return url.protocol === 'https:' || (url.origin === location.origin && value.startsWith('/')) ? url : null;
  } catch { return null; }
}

export function downloadableRelease(data) {
  const r = data?.release;
  const url = safeURL(r?.url);
  return data?.status === 'published' && r?.verified === true && r.version && r.minecraft && r.loader && r.pcl && r.publishedAt && r.testedAt && r.serverCompatibility && ['mrpack', 'curseforge'].includes(r.format) &&
    r.sha256 && /^[a-f0-9]{64}$/i.test(r.sha256) && Number.isSafeInteger(r.bytes) && r.bytes > 0 &&
    url && /\.(mrpack|zip)$/i.test(url.pathname) && decodeURIComponent(url.pathname.split('/').pop()).includes(r.version) ? r : null;
}

export function worldVisual(data, status) {
  const wrap = h('div', 'world-visual');
  const cover = data?.cover;
  if (cover?.src?.startsWith('/static/') && cover.alt) {
    const img = h('img', 'world-cover');
    img.src = cover.src; img.alt = cover.alt; img.width = 960; img.height = 600;
    img.fetchPriority = 'high';
    img.onerror = () => { img.remove(); wrap.classList.add('world-noimage'); };
    wrap.appendChild(img);
  } else wrap.classList.add('world-noimage');
  const placeholder = h('div', 'world-placeholder');
  placeholder.appendChild(icon('pickaxe', 'lg'));
  placeholder.appendChild(h('span', 'world-placeholder-title', 'Minecraft 服务器'));
  placeholder.appendChild(h('span', 'world-placeholder-note', '服务器信息与加入指南'));
  wrap.appendChild(placeholder);
  const caption = h('div', 'world-caption');
  caption.appendChild(h('span', 'world-label', 'MINECRAFT'));
  caption.appendChild(h('strong', null, data?.name || 'Minecraft 服务器'));
  caption.appendChild(h('span', 'world-state', status || '查看服务器与加入方式'));
  wrap.appendChild(caption);
  return wrap;
}

export function copyButton(text, label = '复制服务器地址') {
  const box = h('div', 'copy-control');
  const button = h('button', 'btn', label);
  const feedback = h('span', 'copy-feedback'); feedback.setAttribute('role', 'status');
  button.onclick = async () => {
    try {
      await navigator.clipboard.writeText(text);
      button.textContent = '已复制'; feedback.textContent = '已复制到剪贴板';
      setTimeout(() => { button.textContent = label; }, 1800);
    } catch {
      feedback.textContent = '自动复制失败，请选中下方文字手动复制';
      if (!box.querySelector('input')) {
        const input = h('input', 'input'); input.value = text; input.readOnly = true;
        input.setAttribute('aria-label', '手动复制内容'); box.appendChild(input); input.focus(); input.select();
      }
    }
  };
  box.append(button, feedback);
  return box;
}

export function downloadCard(data, { compact = false, status = '' } = {}) {
  const section = h('section', 'minecraft-section');
  const intro = h('div', 'minecraft-intro');
  intro.appendChild(h('p', 'eyebrow', 'MINECRAFT / 服务器'));
  intro.appendChild(h('h2', null, '下载与加入'));
  intro.appendChild(h('p', null, '查看服务器信息，下载整合包并按指南配置客户端。'));
  if (!compact) intro.appendChild(worldVisual(data, status));
  section.appendChild(intro);
  const card = h('div', 'card download-card');
  const release = downloadableRelease(data);
  card.appendChild(h('span', 'badge', release ? '稳定版' : '发布准备中'));
  card.appendChild(h('h3', null, release ? '下载整合包' : '发布进度'));
  if (!release) { const checklist = h('ol', 'release-checklist'); for (const text of ['客户端版本确认：待验证', 'PCL 导入验证：待验证', '服务器连接验证：待验证']) checklist.appendChild(h('li', null, text)); card.appendChild(checklist); }
  card.appendChild(h('p', null, release ? '下载后导入电脑端 PCL，安装为独立实例。' : '兼容版本和 PCL 导入流程验证完成后开放下载。已有玩家可先查看加入指南。'));
  const facts = h('dl', 'release-facts');
  const add = (key, value) => { const row = h('div'); row.append(h('dt', null, key), h('dd', null, value)); facts.appendChild(row); };
  if (release) {
    add('整合包', release.version); add('Minecraft', release.minecraft); add('加载器', release.loader);
    add('文件大小', (release.bytes / 1048576).toFixed(1) + ' MB'); add('发布日期', release.publishedAt); add('验证 PCL', release.pcl); add('兼容服务端', release.serverCompatibility); add('验证日期', release.testedAt);
    add('更新要求', release.required ? '必须更新才能进服' : '可选更新');
    const a = h('a', 'btn primary cta', '下载整合包 · ' + release.version); a.href = safeURL(release.url).href;
    card.append(facts, a);
    const hash = h('details', 'release-hash'); hash.appendChild(h('summary', null, '文件校验 SHA256'));
    hash.appendChild(h('code', null, release.sha256)); card.appendChild(hash);
    if (release.changelog) card.appendChild(h('p', 'guide-note', release.changelog));
  } else {
    if (data?.serverVersion) add('服务端（状态查询）', data.serverVersion);
    add('客户端版本', '待验证'); add('PCL 导入验证', '尚未完成'); card.appendChild(facts);
    const button = h('button', 'btn primary cta', '整合包准备中'); button.disabled = true; card.appendChild(button);
  }
  const guide = h('a', 'btn', compact ? 'PCL 安装指南 →' : '查看安装步骤 ↓'); guide.href = compact ? '/minecraft' : '#installation';
  card.appendChild(guide);
  if (data?.serverAddress) {
    const label = h('div', 'server-address'); label.append(h('span', null, '服务器地址'), h('code', null, data.serverAddress));
    card.append(label, copyButton(data.serverAddress, '已有客户端？复制服务器地址'));
  } else card.appendChild(h('p', 'guide-note', '进服地址待管理员确认，请勿使用猜测地址。'));
  if (status === '离线') card.appendChild(h('p', 'guide-note', '服务器暂时离线，仍可安装；恢复后再加入。'));
  if (!compact) card.appendChild(copyButton(location.origin + '/minecraft', '复制指南链接'));
  section.appendChild(card);
  return section;
}
