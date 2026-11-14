// Only this explicit list of public resources may enter the cache.
const CACHE = 'rhythm-public-v1';
const PUBLIC_ASSETS = ['/offline.html', '/app-icon.svg'];
self.addEventListener('install', event => {
  event.waitUntil(caches.open(CACHE).then(cache => cache.addAll(PUBLIC_ASSETS)));
});
self.addEventListener('activate', event => {
  event.waitUntil(caches.keys().then(keys => Promise.all(keys.filter(key => key.startsWith('rhythm-public-') && key !== CACHE).map(key => caches.delete(key)))));
});
self.addEventListener('fetch', event => {
  const url = new URL(event.request.url);
  if (event.request.method !== 'GET' || url.origin !== self.location.origin) return;
  // API, JSON and live documents are never cached, even on successful requests.
  if (PUBLIC_ASSETS.includes(url.pathname) && !url.search) {
    event.respondWith(caches.match(event.request).then(cached => cached || fetch(event.request)));
  } else if (event.request.mode === 'navigate' && url.pathname === '/') {
    event.respondWith(fetch(event.request).catch(() => caches.match('/offline.html')));
  }
});
