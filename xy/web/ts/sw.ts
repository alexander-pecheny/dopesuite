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

// App shell precache: entry modules, styles, fonts, vendored crypto, icons, and
// the static page routes. Both constants are baked in by the build (webbuild's
// xySWBuild): the manifest is derived from the module graph + asset dirs and the
// cache name from their content hash, so neither can drift from what ships.
// Unversioned URLs; versioned requests are cached per-URL at runtime. Failures
// here don't abort install (allSettled).
declare const __PRECACHE__: string[];
declare const __SHELL_VERSION__: string;
const CACHE = __SHELL_VERSION__;
const PRECACHE: string[] = __PRECACHE__;

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

// ---- named downloads (see namedurl.ts) ----
// Generated files the page hands over so the browser sees them at /dl/<name>
// rather than at a blob: UUID. In memory only: this is decrypted plaintext, and
// it must never reach Cache Storage. The map is dropped whenever the worker is
// recycled, which is fine — the viewer has already loaded what it fetched.
const downloads = new Map<string, { blob: Blob; filename: string }>();

sw.addEventListener("message", (event) => {
  const d = event.data as { type?: string; path?: string; filename?: string; blob?: Blob } | null;
  const reply = (ok: boolean): void => { event.ports[0]?.postMessage(ok); };
  if (!d || !d.path) return;
  if (d.type === "xy-dl-put" && d.blob) {
    downloads.set(d.path, { blob: d.blob, filename: d.filename || "download" });
    reply(true);
  } else if (d.type === "xy-dl-del") {
    downloads.delete(d.path);
    reply(true);
  }
});

// rfc6266 spells a download name so a non-ASCII one survives: a quoted-string
// filename is latin-1, so Cyrillic sent that way arrives as mojibake. Mirrors
// contentDisposition in internal/server/export.go.
function rfc6266(disposition: string, filename: string): string {
  const ascii = filename.replace(/[^\x20-\x7e]/g, "_").replace(/["\\]/g, "");
  return `${disposition}; filename="${ascii}"; filename*=UTF-8''${encodeURIComponent(filename)}`;
}

function serveDownload(path: string): Response {
  const hit = downloads.get(path);
  if (!hit) return new Response("", { status: 404 });
  return new Response(hit.blob, {
    headers: {
      "Content-Type": hit.blob.type || "application/octet-stream",
      // inline, not attachment: the iframe must render it. Both PDF viewers read
      // the name to suggest on Save from here either way.
      "Content-Disposition": rfc6266("inline", hit.filename),
      "Cache-Control": "no-store",
    },
  });
}

sw.addEventListener("fetch", (event) => {
  const { request } = event;
  if (request.method !== "GET") return;
  const url = new URL(request.url);
  if (url.origin !== sw.location.origin) return;

  // Before the navigate branch: the PDF preview iframe is a navigation.
  if (url.pathname.startsWith("/dl/")) {
    event.respondWith(serveDownload(url.pathname));
    return;
  }
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
