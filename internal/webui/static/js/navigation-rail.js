// Product navigation reuses the existing controls and authorization paths.
import { S } from './state.js';
import { askConfirm } from './ui.js';
const byId = id => document.getElementById(id);
for (const id of ['rail-conversations', 'rail-workspace', 'rail-tools']) {
  const link = byId(id);
  if (id !== 'rail-conversations') link.onclick = async event => {
    if (location.pathname !== '/agent' || (!byId('text').value.trim() && !S.pending.length)) return;
    event.preventDefault();
    if (await askConfirm({title:'离开当前会话？',text:'当前还有未发送的文字或附件。离开会丢失这些内容。',okLabel:'离开页面',danger:false})) location.assign(link.href);
  };
  if (link.pathname === location.pathname) link.setAttribute('aria-current', 'page');
}
byId('rail-conversations').onclick = event => { if (location.pathname === '/agent') { event.preventDefault(); document.body.classList.remove('collapsed'); localStorage.setItem('mineagent.sidebar',''); byId('conv-search').focus(); } };
const leaveDrawer = () => { if (byId('sidebar').classList.contains('open')) byId('menu').click(); };
byId('rail-usage').onclick = () => { leaveDrawer(); byId('menu-usage').click(); };
