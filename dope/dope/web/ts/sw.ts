// dope's service worker: the pages a venue host saved for work without a
// network (offline.ts asks it to «prepare» them), and the scripts, styles and
// fonts those pages load. Nothing else is cached, and /api never is: a page
// keeps its own data and its unsent edits in localStorage.
//
// A saved page is network-first, with the saved copy as the fallback when the
// network fails or answers too slowly, and every online visit refreshes it. A
// versioned asset (?v=<hash>) never changes, so it is served from the cache.
// A page that was never saved goes to the network untouched.

const sw = self as unknown as ServiceWorkerGlobalScope;

const PAGES = "dope-pages-v1";
const ASSETS = "dope-assets-v1";
// A saved page waits this long for the network before it falls back to its copy.
const NAVIGATION_TIMEOUT_MS = 4000;
const HTTP_UNAVAILABLE = 503;

sw.addEventListener("install", (event) => {
  event.waitUntil(sw.skipWaiting());
});

sw.addEventListener("activate", (event) => {
  event.waitUntil(sw.clients.claim());
});

// assetURLs are the same-origin scripts, styles, icons and fonts a page or a
// stylesheet names.
function assetURLs(text: string, base: string): string[] {
  const out = new Set<string>();
  const patterns = [/(?:src|href)="([^"]+)"/g, /url\(\s*['"]?([^'")]+)['"]?\s*\)/g];
  for (const pattern of patterns) {
    for (const match of text.matchAll(pattern)) {
      const url = new URL(match[1], base);
      if (url.origin !== sw.location.origin) continue;
      if (url.pathname.startsWith("/static/") || url.pathname === "/manifest.webmanifest" || url.pathname === "/favicon.ico") {
        out.add(url.href);
      }
    }
  }
  return [...out];
}

// saveAsset caches one asset, and the fonts a stylesheet names.
async function saveAsset(cache: Cache, url: string): Promise<void> {
  const response = await fetch(url, {cache: "no-cache"});
  if (!response.ok) throw new Error(`${url}: ${response.status}`);
  await cache.put(url, response.clone());
  if ((response.headers.get("Content-Type") || "").includes("css")) {
    const css = await response.text();
    await Promise.all(assetURLs(css, url).map((font) => cache.match(font).then((hit) => hit ? undefined : saveAsset(cache, font))));
  }
}

// prepare saves each page and everything it loads, and answers which failed.
async function prepare(urls: string[]): Promise<string[]> {
  const pages = await caches.open(PAGES);
  const assets = await caches.open(ASSETS);
  const failed: string[] = [];
  for (const page of urls) {
    const url = new URL(page, sw.location.origin).href;
    try {
      const response = await fetch(url, {cache: "no-cache", credentials: "same-origin"});
      if (!response.ok || response.redirected) throw new Error(`${url}: ${response.status}`);
      await pages.put(url, response.clone());
      const html = await response.text();
      await Promise.all(assetURLs(html, url).map((asset) => saveAsset(assets, asset)));
    } catch (_) {
      failed.push(page);
    }
  }
  return failed;
}

sw.addEventListener("message", (event) => {
  const data = event.data as {type?: string; urls?: string[]} | null;
  if (data?.type !== "prepare" || !Array.isArray(data.urls)) return;
  const port = event.ports[0];
  event.waitUntil(prepare(data.urls).then((failed) => port?.postMessage({failed})));
});

// savedPage is the saved copy of a page: the same address, or the same path
// with another query, as when the venue page comes back with ?saved=1.
async function savedPage(request: Request): Promise<Response | undefined> {
  const pages = await caches.open(PAGES);
  return (await pages.match(request)) || (await pages.match(request, {ignoreSearch: true}));
}

async function networkFirst(request: Request, saved: Response): Promise<Response> {
  const pages = await caches.open(PAGES);
  const network = fetch(request).then(async (response) => {
    if (response.ok && !response.redirected) await pages.put(request, response.clone());
    return response;
  });
  network.catch(() => undefined);
  const late = new Promise<Response>((resolve) => setTimeout(() => resolve(saved), NAVIGATION_TIMEOUT_MS));
  try {
    return await Promise.race([network, late]);
  } catch (_) {
    return saved;
  }
}

// cacheFirst serves a versioned asset. A new version of a file a saved page
// loads replaces the old one, and the files no saved page loads stay out.
async function cacheFirst(request: Request): Promise<Response> {
  const assets = await caches.open(ASSETS);
  const hit = await assets.match(request);
  if (hit) return hit;
  try {
    const response = await fetch(request);
    const old = await assets.keys(request, {ignoreSearch: true});
    if (response.ok && old.length) {
      await Promise.all(old.map((key) => assets.delete(key)));
      await assets.put(request, response.clone());
    }
    return response;
  } catch (error) {
    // Offline, an older copy of the same file is better than none.
    const loose = await assets.match(request, {ignoreSearch: true});
    if (loose) return loose;
    throw error;
  }
}

async function networkThenCache(request: Request): Promise<Response> {
  const assets = await caches.open(ASSETS);
  try {
    const response = await fetch(request);
    if (response.ok && await assets.match(request)) await assets.put(request, response.clone());
    return response;
  } catch (_) {
    return (await assets.match(request, {ignoreSearch: true})) || new Response("", {status: HTTP_UNAVAILABLE});
  }
}

async function navigate(request: Request): Promise<Response> {
  const saved = await savedPage(request);
  return saved ? networkFirst(request, saved) : fetch(request);
}

sw.addEventListener("fetch", (event) => {
  const {request} = event;
  if (request.method !== "GET") return;
  const url = new URL(request.url);
  if (url.origin !== sw.location.origin || url.pathname.startsWith("/api/")) return;
  if (request.mode === "navigate") {
    event.respondWith(navigate(request));
    return;
  }
  if (url.pathname.startsWith("/static/") || url.pathname === "/manifest.webmanifest") {
    event.respondWith(url.searchParams.has("v") ? cacheFirst(request) : networkThenCache(request));
  }
});
