const $ = (id) => document.getElementById(id);
const status = $('download-status');

async function load() {
  const key = location.hash.slice(1);
  $('download-form').hidden = true;
  $('download-key').value = '';
  $('file-checksum').hidden = true;
  $('file-name').textContent = '文件下载';
  $('file-size').textContent = '';
  status.textContent = '正在读取文件信息…';
  if (!/^[a-f0-9]{64}$/.test(key)) { status.textContent = '链接不完整，请使用发送者提供的完整链接。'; return; }
  try {
    const response = await fetch('/api/downloads/info', {
      method: 'POST', body: new URLSearchParams({ key }), credentials: 'omit', cache: 'no-store',
    });
    const file = await response.json();
    if (location.hash.slice(1) !== key) return;
    if (!response.ok) throw new Error(file.error || '文件暂不可用');
    $('file-name').textContent = file.name;
    $('file-size').textContent = (file.size / 1048576).toFixed(2) + ' MiB';
    $('file-hash').textContent = file.sha256;
    $('file-checksum').hidden = false;
    $('download-key').value = key;
    $('download-form').hidden = false;
    status.textContent = '文件已就绪。';
  } catch (error) { if (location.hash.slice(1) === key) status.textContent = error.message || '无法连接，请稍后重新打开链接。'; }
}
$('download-form').addEventListener('submit', () => {
  status.textContent = '已请求下载，请查看浏览器的下载列表。';
  // 原生附件响应保存到磁盘，不在浏览器内存里缓存整个文件。
});
window.addEventListener('hashchange', load);
load();
