/* Cache versioned public assets locally for kiosk displays; never cache private HTML or API data. */
(function () {
  'use strict';
  var CACHE_NAME = 'family-hq-static-v12';
  var CACHE_PREFIX = 'family-hq-static-';
  var ASSETS = [
    '/style.css?v=43',
    '/theme.js?v=2',
    '/app.js?v=43',
    '/icons/family-hq.svg',
    '/icons/family-hq-192.png',
    '/icons/family-hq-512.png',
    '/icons/family-hq-maskable-512.png',
    '/favicon.ico'
  ];

  self.addEventListener('install', function (event) {
    event.waitUntil(caches.open(CACHE_NAME).then(function (cache) {
      return Promise.all(ASSETS.map(function (asset) {
        return fetch(asset, { cache: 'reload' }).then(function (response) {
          if (response.ok) return cache.put(asset, response);
        }).catch(function () { /* Keep installation usable if one optional asset is unavailable. */ });
      }));
    }).then(function () { return self.skipWaiting(); }));
  });

  self.addEventListener('activate', function (event) {
    event.waitUntil(caches.keys().then(function (keys) {
      return Promise.all(keys.map(function (key) {
        if (key.indexOf(CACHE_PREFIX) === 0 && key !== CACHE_NAME) return caches.delete(key);
      }));
    }).then(function () { return self.clients.claim(); }));
  });

  self.addEventListener('fetch', function (event) {
    var request = event.request;
    var url = new URL(request.url);
    if (request.method !== 'GET' || url.origin !== self.location.origin || ASSETS.indexOf(url.pathname + url.search) === -1) return;
    event.respondWith(caches.open(CACHE_NAME).then(function (cache) {
      return cache.match(request).then(function (cached) {
        return cached || fetch(request).then(function (response) {
          if (response.ok) cache.put(request, response.clone());
          return response;
        });
      });
    }));
  });
})();
