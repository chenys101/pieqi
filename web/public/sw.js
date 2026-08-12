// Pieqi service worker
//
// 缓存策略（核心目标：前端一更新，设备端自动拉到新版本，不再"时灵时不灵"）：
//  - 导航请求 / index.html：network-first —— 每次拉最新 HTML，从而引用最新带 hash 的 bundle；
//  - /assets/*（文件名带内容 hash，不可变）：cache-first —— 离线可加载；
//  - API / WS 一律不缓存。
// 版本号随每次前端发布递增，activate 时清掉旧版本缓存，强制刷新。
const VERSION = 'v2';
const SHELL_CACHE = `pieqi-shell-${VERSION}`;
const ASSET_CACHE = `pieqi-assets-${VERSION}`;

self.addEventListener('install', () => {
  // 新 SW 立即接管，不等旧页面关掉
  self.skipWaiting();
});

self.addEventListener('activate', (e) => {
  e.waitUntil(
    caches.keys().then((keys) =>
      Promise.all(keys.filter((k) => !k.includes(VERSION)).map((k) => caches.delete(k)))
    )
  );
  self.clients.claim();
});

self.addEventListener('fetch', (e) => {
  const req = e.request;
  const url = new URL(req.url);

  // 非 GET 或 API / WS：不拦截
  if (
    req.method !== 'GET' ||
    url.pathname.startsWith('/api/') ||
    url.pathname.startsWith('/internal') ||
    url.pathname === '/ws'
  ) {
    return;
  }

  // 带 hash 的构建产物：内容不可变，cache-first（命中即用，miss 则拉取并缓存）
  if (url.pathname.startsWith('/assets/')) {
    e.respondWith(
      caches.match(req).then((cached) => {
        if (cached) return cached;
        return fetch(req).then((res) => {
          const copy = res.clone();
          caches.open(ASSET_CACHE).then((c) => c.put(req, copy)).catch(() => {});
          return res;
        });
      })
    );
    return;
  }

  // 导航 / index.html / 其余：network-first（更新优先，离线时回退缓存）
  e.respondWith(
    fetch(req)
      .then((res) => {
        if (res && res.ok) {
          const copy = res.clone();
          caches.open(SHELL_CACHE).then((c) => c.put(req, copy)).catch(() => {});
        }
        return res;
      })
      .catch(() =>
        caches.match(req).then((cached) => cached || caches.match('/'))
      )
  );
});
