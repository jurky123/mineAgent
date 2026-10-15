import { boot } from './shell.js';
import { h } from './ds.js';
import { loadMinecraftRelease, downloadCard } from './minecraft-release.js';

await boot({ active: '/minecraft', requireLogin: false, preview: new URLSearchParams(location.search).get('ui') === '1' });
const target = document.getElementById('minecraft-download');
async function load() {
  target.setAttribute('aria-busy', 'true');
  try { target.replaceChildren(downloadCard(await loadMinecraftRelease())); }
  catch (error) {
    const box = h('div', 'card download-card'); box.appendChild(h('p', 'field-err', error.message));
    const retry = h('button', 'btn', '重新加载发布信息'); retry.onclick = load; box.appendChild(retry); target.replaceChildren(box);
  } finally { target.setAttribute('aria-busy', 'false'); }
}
await load();
