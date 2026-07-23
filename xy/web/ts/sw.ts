// sw.ts — xy's service worker: caches the app shell so the PWA loads and runs
// with no network. Data offline is handled in app code (sync.js + IndexedDB);
// the worker only deals with static assets and HTML navigations. It never caches
// /api responses — those carry encrypted, user- and session-specific data and
// are handled (with their own offline fallback) by the app's sync layer.
//
// Strategy:
//   - navigations (HTML pages): network-first, fall back to the cached page,
//     then to the cached board/home shell — so deep links work offline.
//   - versioned static (?v=<hash>): cache-first (content-addressed → immutable).
//   - other static (unversioned JS/CSS — e.g. board.js's bare module imports):
//     network-first, falling back to cache offline. These have no ?v= hash to
//     bust, so cache-first/stale-while-revalidate would serve a stale module for
//     a whole extra load after every deploy; network-first keeps them fresh
//     online while staying offline-capable.
//   - everything else (/api/…): straight to network, untouched.

// The webworker lib types `self` as a plain worker scope; alias it once as the
// service-worker scope so skipWaiting/clients and the SW event map are typed.
// (This stays a script file — no import/export — so the served /sw.js is a
// plain classic worker script.)
const sw = self as unknown as ServiceWorkerGlobalScope;

// v11: main's trello-import shell (v10) and the ESM-rewrite shell (also v10)
// diverged; the merge needs its own name so a deployed v10 cache gets cleared.
const CACHE = "xy-shell-v11";

// App shell precache: entry modules, styles, fonts, vendored crypto, icons, and
// the static page routes. Unversioned URLs; versioned requests are cached
// per-URL at runtime. Failures here don't abort install (allSettled).
const PRECACHE: string[] = [
  "/",
  "/login", "/register", "/profile", "/import",
  "/manifest.webmanifest",
  "/static/styles.css",
  "/static/dist/app.js", "/static/dist/crypto.js", "/static/dist/rank.js", "/static/dist/chgk.js",
  "/static/dist/diff.js", "/static/dist/board.js", "/static/dist/carddraft.js", "/static/dist/handoutsession.js", "/static/dist/boardmembers.js", "/static/dist/timer.js", "/static/dist/index.js", "/static/menu.js", "/static/dist/pwa.js",
  "/static/login.js", "/static/dist/profile.js", "/static/dist/import.js", "/static/dist/trellomodel.js",
  "/static/dist/store.js", "/static/dist/sync.js",
  "/static/ding.mp3",
  "/static/vendor/scrypt.js", "/static/vendor/_assert.js", "/static/vendor/_md.js",
  "/static/vendor/hmac.js", "/static/vendor/pbkdf2.js", "/static/vendor/sha256.js",
  "/static/vendor/utils.js", "/static/vendor/crypto.js",
  "/static/fonts/noto-sans-var.woff2", "/static/fonts/noto-sans-var-italic.woff2",
  "/static/fonts/jetbrains-mono-var.woff2",
  "/static/icon-192.png", "/static/icon-512.png", "/static/icon-maskable.png",
  "/static/apple-touch-icon.png", "/static/favicon.svg", "/favicon.ico",
];

sw.addEventListener("install", (event) => {
  event.waitUntil(
    (async () => {
      const cache = await caches.open(CACHE);
      await Promise.allSettled(PRECACHE.map((u) => cache.add(new Request(u, { cache: "reload" }))));
      await sw.skipWaiting();
    })()
  );
});

sw.addEventListener("activate", (event) => {
  event.waitUntil(
    (async () => {
      const names = await caches.keys();
      await Promise.all(names.filter((n) => n !== CACHE).map((n) => caches.delete(n)));
      await sw.clients.claim();
    })()
  );
});

function isStatic(url: URL): boolean {
  return url.pathname.startsWith("/static/") ||
    url.pathname === "/manifest.webmanifest";
}

async function networkFirstNavigation(request: Request): Promise<Response> {
  const cache = await caches.open(CACHE);
  try {
    const resp = await fetch(request);
    if (resp && resp.ok) cache.put(request, resp.clone());
    return resp;
  } catch (_) {
    const cached = await cache.match(request, { ignoreSearch: true });
    if (cached) return cached;
    // Deep-link fallback: every /board/{id} page is the same shell (board.js reads
    // the id from the URL), so any cached board page serves for a new board id.
    const url = new URL(request.url);
    if (url.pathname.startsWith("/board/")) {
      const keys = await cache.keys();
      const boardKey = keys.find((req) => new URL(req.url).pathname.startsWith("/board/"));
      if (boardKey) { const r = await cache.match(boardKey); if (r) return r; }
    }
    const home = await cache.match("/");
    if (home) return home;
    return new Response("Офлайн", { status: 503, headers: { "Content-Type": "text/plain; charset=utf-8" } });
  }
}

async function cacheFirst(request: Request): Promise<Response> {
  const cache = await caches.open(CACHE);
  const cached = await cache.match(request);
  if (cached) return cached;
  try {
    const resp = await fetch(request);
    if (resp && resp.ok) cache.put(request, resp.clone());
    return resp;
  } catch (err) {
    // Offline and this exact ?v=<hash> URL was never fetched online: fall back to
    // any cached copy of the same path. The precache stores assets unversioned
    // (the hashes aren't known at author time), so the versioned request would
    // otherwise miss and the app shell wouldn't load on the first offline visit.
    // This fallback only runs after the network fails, so online deploys still
    // fetch the fresh hashed asset rather than a stale precached one.
    const loose = await cache.match(request, { ignoreSearch: true });
    if (loose) return loose;
    throw err;
  }
}

// networkFirstStatic keeps unversioned modules fresh online (so a deploy lands on
// the next load, not the one after) while still serving the precached copy when
// the network is unavailable.
async function networkFirstStatic(request: Request): Promise<Response> {
  const cache = await caches.open(CACHE);
  try {
    const resp = await fetch(request);
    if (resp && resp.ok) cache.put(request, resp.clone());
    return resp;
  } catch (_) {
    const cached = await cache.match(request, { ignoreSearch: true });
    return cached || new Response("", { status: 504 });
  }
}

sw.addEventListener("fetch", (event) => {
  const { request } = event;
  if (request.method !== "GET") return;
  const url = new URL(request.url);
  if (url.origin !== sw.location.origin) return;

  if (request.mode === "navigate") {
    event.respondWith(networkFirstNavigation(request));
    return;
  }
  if (isStatic(url)) {
    if (url.searchParams.has("v")) event.respondWith(cacheFirst(request));
    else event.respondWith(networkFirstStatic(request));
    return;
  }
  // /api/* and anything else: let it hit the network (app handles offline).
});
