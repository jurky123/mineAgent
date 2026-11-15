// Product navigation reuses the existing controls and authorization paths.
const byId = id => document.getElementById(id);
byId('rail-conversations').setAttribute('aria-current', 'page');
byId('rail-conversations').onclick = () => {
  document.body.classList.remove('collapsed');
  localStorage.setItem('mineagent.sidebar', '');
  byId('conv-search').focus();
};
const leaveDrawer = () => { if (byId('sidebar').classList.contains('open')) byId('menu').click(); };
byId('rail-workspace').hidden = true;
byId('rail-workspace').onclick = () => { if (!byId('menu-ws').hidden) { leaveDrawer(); byId('menu-ws').click(); } };
byId('rail-usage').onclick = () => { leaveDrawer(); byId('menu-usage').click(); };
byId('rail-tools').onclick = () => { leaveDrawer(); byId('attach').click(); };
