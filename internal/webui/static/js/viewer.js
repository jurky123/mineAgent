// 图片查看器：缩放/拖动/多图切换/下载

import { $ } from './ui.js';

const viewer = { list: [], index: 0, scale: 1, x: 0, y: 0, drag: false, sx: 0, sy: 0, ox: 0, oy: 0 };
const V = () => $('viewer');

export function openViewer(list, index) {
  if (!list || !list.length) return;
  viewer.list = list;
  viewer.index = Math.max(0, Math.min(index || 0, list.length - 1));
  V().classList.add('on');
  document.body.style.overflow = 'hidden';
  showViewerImage();
}

export function closeViewer() {
  if (!V().classList.contains('on')) return;
  V().classList.add('closing');
  setTimeout(() => { V().classList.remove('on', 'closing'); viewer.list = []; }, 140);
  document.body.style.overflow = '';
}

function showViewerImage() {
  const it = viewer.list[viewer.index] || {};
  const img = $('vimg');
  img.src = it.url || ''; img.alt = it.name || '';
  document.querySelector('.vname').textContent = it.name || '';
  document.querySelector('.vcount').textContent = viewer.list.length > 1 ? (viewer.index + 1) + ' / ' + viewer.list.length : '';
  const many = viewer.list.length > 1;
  $('vprev').style.visibility = many ? 'visible' : 'hidden';
  $('vnext').style.visibility = many ? 'visible' : 'hidden';
  const dl = document.querySelector('.vdl');
  dl.href = it.url || '';
  if (it.name) dl.setAttribute('download', it.name); else dl.removeAttribute('download');
  resetZoom();
}

function applyZoom() {
  $('vimg').style.transform = 'translate(' + viewer.x + 'px,' + viewer.y + 'px) scale(' + viewer.scale + ')';
  document.querySelector('.vzoom').textContent = Math.round(viewer.scale * 100) + '%';
}

function resetZoom() { viewer.scale = 1; viewer.x = 0; viewer.y = 0; applyZoom(); }

function zoomBy(f, cx, cy) {
  const ns = Math.min(8, Math.max(0.1, viewer.scale * f));
  const r = $('vstage').getBoundingClientRect();
  const px = (cx === undefined ? r.width / 2 : cx - r.left) - r.width / 2;
  const py = (cy === undefined ? r.height / 2 : cy - r.top) - r.height / 2;
  const k = ns / viewer.scale;
  viewer.x = px - (px - viewer.x) * k;
  viewer.y = py - (py - viewer.y) * k;
  viewer.scale = ns;
  applyZoom();
}

function stepViewer(d) {
  if (viewer.list.length < 2) return;
  viewer.index = (viewer.index + d + viewer.list.length) % viewer.list.length;
  showViewerImage();
}

(function bind() {
  const stage = $('vstage');
  stage.addEventListener('wheel', (e) => { e.preventDefault(); zoomBy(e.deltaY < 0 ? 1.15 : 1 / 1.15, e.clientX, e.clientY); }, { passive: false });
  stage.addEventListener('dblclick', (e) => { e.preventDefault(); viewer.scale > 1.01 ? resetZoom() : zoomBy(2, e.clientX, e.clientY); });
  stage.addEventListener('pointerdown', (e) => {
    if (e.target.closest('.vnav')) return;
    viewer.drag = true; viewer.sx = e.clientX; viewer.sy = e.clientY;
    viewer.ox = viewer.x; viewer.oy = viewer.y;
    $('vimg').classList.add('grabbing');
    try { stage.setPointerCapture(e.pointerId); } catch (err) {}
  });
  stage.addEventListener('pointermove', (e) => {
    if (!viewer.drag) return;
    viewer.x = viewer.ox + (e.clientX - viewer.sx);
    viewer.y = viewer.oy + (e.clientY - viewer.sy);
    applyZoom();
  });
  const end = (e) => { viewer.drag = false; $('vimg').classList.remove('grabbing'); try { stage.releasePointerCapture(e.pointerId); } catch (err) {} };
  stage.addEventListener('pointerup', end);
  stage.addEventListener('pointercancel', end);
  let pinch = 0;
  const dist = (t) => Math.hypot(t[0].clientX - t[1].clientX, t[0].clientY - t[1].clientY);
  stage.addEventListener('touchstart', (e) => { if (e.touches.length === 2) pinch = dist(e.touches); }, { passive: true });
  stage.addEventListener('touchmove', (e) => {
    if (e.touches.length === 2 && pinch) {
      e.preventDefault();
      const d = dist(e.touches);
      zoomBy(d / pinch, (e.touches[0].clientX + e.touches[1].clientX) / 2, (e.touches[0].clientY + e.touches[1].clientY) / 2);
      pinch = d;
    }
  }, { passive: false });
  stage.addEventListener('touchend', () => { pinch = 0; });
  document.querySelector('.vbar').addEventListener('click', (e) => {
    const b = e.target.closest('[data-act]');
    if (!b) return;
    const act = b.dataset.act;
    if (act === 'zoomin') zoomBy(1.25);
    else if (act === 'zoomout') zoomBy(1 / 1.25);
    else if (act === 'reset') resetZoom();
    else if (act === 'close') closeViewer();
  });
  $('vprev').onclick = () => stepViewer(-1);
  $('vnext').onclick = () => stepViewer(1);
  V().addEventListener('click', (e) => { if (e.target === V() || e.target === stage) closeViewer(); });
  document.addEventListener('keydown', (e) => {
    if (!V().classList.contains('on')) return;
    if (e.key === 'Escape') closeViewer();
    else if (e.key === 'ArrowLeft') stepViewer(-1);
    else if (e.key === 'ArrowRight') stepViewer(1);
    else if (e.key === '+' || e.key === '=') zoomBy(1.25);
    else if (e.key === '-') zoomBy(1 / 1.25);
    else if (e.key === '0') resetZoom();
  });
})();
