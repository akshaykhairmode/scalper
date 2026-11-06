// Scalper Service Worker for PWA Support and Asset Caching
const CACHE_VERSION = 'scalper-v1';
const STATIC_CACHE = `${CACHE_VERSION}-static`;
const SHELL_CACHE = `${CACHE_VERSION}-shell`;

const CORE_SHELL = [
  '/',
  '/manifest.webmanifest',
  '/static/app.css',
  '/static/app.js',
  '/static/charts.js',
  '/static/klinecharts.min.js',
  '/static/patterns.js',
  '/static/icon.svg',
  '/static/icon-192.png',
  '/static/icon-512.png',
  '/static/apple-touch-icon.png',
];

// Install: pre-cache application shell assets
self.addEventListener('install', (event) => {
  event.waitUntil(
    caches.open(SHELL_CACHE).then(async (cache) => {
      // Use individual puts so a single asset failure doesn't abort install
      await Promise.all(
        CORE_SHELL.map(async (url) => {
          try {
            const response = await fetch(url, { cache: 'no-cache' });
            if (response.ok) {
              await cache.put(url, response);
            }
          } catch (err) {
            console.warn('[SW] Failed to pre-cache:', url, err);
          }
        })
      );
    }).then(() => self.skipWaiting())
  );
});

// Activate: purge older caches and claim clients
self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys().then((keys) => {
      return Promise.all(
        keys
          .filter((k) => k.startsWith('scalper-') && k !== STATIC_CACHE && k !== SHELL_CACHE)
          .map((k) => caches.delete(k))
      );
    }).then(() => self.clients.claim())
  );
});

// Fetch: route requests according to asset type
self.addEventListener('fetch', (event) => {
  const req = event.request;
  const url = new URL(req.url);

  // 1. Non-GET requests and WebSocket connections are strictly bypassed
  if (req.method !== 'GET' || url.protocol === 'ws:' || url.protocol === 'wss:') {
    return;
  }

  // 2. Market feed, broker APIs, authentication, health checks, and logs MUST NEVER be cached
  if (
    url.pathname.startsWith('/api/') ||
    url.pathname === '/ws' ||
    url.pathname === '/healthz' ||
    url.pathname === '/logs' ||
    url.pathname === '/login'
  ) {
    return; // Pass through straight to network
  }

  // 3. Navigation requests (HTML shell): Network-first with cache fallback
  if (req.mode === 'navigate') {
    event.respondWith(
      fetch(req)
        .then((networkResp) => {
          if (networkResp.ok) {
            const copy = networkResp.clone();
            caches.open(SHELL_CACHE).then((cache) => cache.put('/', copy));
          }
          return networkResp;
        })
        .catch(async () => {
          const cached = await caches.match('/');
          if (cached) return cached;
          return new Response('Offline - Scalper Terminal requires network connectivity.', {
            status: 503,
            headers: { 'Content-Type': 'text/plain; charset=utf-8' },
          });
        })
    );
    return;
  }

  // 4. Static assets (/static/*, /manifest.*, etc.): Cache-first with network background update
  if (url.pathname.startsWith('/static/') || url.pathname.includes('manifest')) {
    event.respondWith(
      caches.match(req).then((cachedResp) => {
        const fetchPromise = fetch(req)
          .then((networkResp) => {
            if (networkResp && networkResp.ok) {
              const copy = networkResp.clone();
              caches.open(STATIC_CACHE).then((cache) => cache.put(req, copy));
            }
            return networkResp;
          })
          .catch(() => cachedResp);

        return cachedResp || fetchPromise;
      })
    );
    return;
  }

  // Default: network fallback
  event.respondWith(
    caches.match(req).then((cached) => cached || fetch(req))
  );
});

// Listen for message events (e.g. SKIP_WAITING from app)
self.addEventListener('message', (event) => {
  if (event.data === 'SKIP_WAITING') {
    self.skipWaiting();
  }
});
