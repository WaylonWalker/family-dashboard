/*
 * Keep installed Family HQ sessions online-only. Dashboard HTML contains
 * private family data, so this worker deliberately does not cache requests.
 */
self.addEventListener('install', function (event) {
  event.waitUntil(self.skipWaiting());
});

self.addEventListener('activate', function (event) {
  event.waitUntil(self.clients.claim());
});
