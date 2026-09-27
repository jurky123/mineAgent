// Mine 的 Service Worker：只为"可安装"（PWA）存在，不做离线缓存，避免静态资源过期问题。
const VER = '__VER__';
self.addEventListener('install', () => self.skipWaiting());
self.addEventListener('activate', (e) => e.waitUntil(self.clients.claim()));
self.addEventListener('fetch', () => { /* 纯网络 */ });
self.VER = VER;
