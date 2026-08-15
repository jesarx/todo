var CACHE = "todo-v1";

self.addEventListener("install", function (e) {
  e.waitUntil(self.skipWaiting());
});

self.addEventListener("activate", function (e) {
  e.waitUntil(caches.keys().then(function (keys) {
    return Promise.all(keys.filter(function (k) { return k !== CACHE; })
      .map(function (k) { return caches.delete(k); }));
  }).then(function () { return self.clients.claim(); }));
});

// al llegar al login (cierre de sesión o sesión caducada) se tira el caché:
// nadie debería poder leer las tareas sin conexión después de salir. La cola
// de cambios sin subir vive en localStorage y no se toca.
self.addEventListener("message", function (e) {
  if (e.data === "clear") e.waitUntil(caches.delete(CACHE));
});

self.addEventListener("fetch", function (e) {
  var req = e.request;
  if (req.method !== "GET") return;
  var url = new URL(req.url);
  if (url.origin !== location.origin) return;

  // estáticos: primero el caché. Las URLs van versionadas (?v=hash), así que
  // un deploy nuevo produce URLs nuevas y nunca se sirve css/js viejo.
  if (url.pathname.startsWith("/static/") || url.pathname === "/favicon.svg" ||
      url.pathname === "/manifest.webmanifest") {
    e.respondWith(caches.open(CACHE).then(function (c) {
      return c.match(req).then(function (hit) {
        return hit || fetch(req).then(function (res) {
          if (res.ok) c.put(req, res.clone());
          return res;
        });
      });
    }));
    return;
  }

  // páginas: primero la red y se guarda copia; sin conexión, la última copia
  // vista de esa misma página (o la portada). Así la app abre sin señal.
  if (req.mode === "navigate") {
    e.respondWith(fetch(req).then(function (res) {
      // res.redirected = la sesión caducó y esto es el login disfrazado de
      // otra página: guardarlo dejaría la app mostrando el login sin conexión
      if (res.ok && !res.redirected && url.pathname !== "/login") {
        var copy = res.clone();
        caches.open(CACHE).then(function (c) { c.put(req, copy); });
      }
      return res;
    }).catch(function () {
      return caches.match(req).then(function (hit) {
        return hit || caches.match("/");
      });
    }));
  }
});
