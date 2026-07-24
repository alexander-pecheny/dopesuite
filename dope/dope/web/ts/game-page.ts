// Game-page plumbing shared by od/si/host/viewer: the window globals contract
// (init payloads, menu chrome), route parsing, breadcrumbs, the menu jump/
// download mounts, the localStorage snapshot cache and the init/cache/fetch
// game-data loader.

// Page globals the bundle environment provides (menu.js's dopeMenu, the
// server-inlined __GAME_INIT__). Accessed via a structural cast so this module
// stays importable — and type-checkable — standalone.
export interface DopeMenuJump {
  label: string;
  href: string;
  title?: string;
  external?: boolean;
}

export interface DopeMenuExtra {
  label: string;
  href: string;
  title?: string;
  download?: boolean;
}

interface DopeMenuLike {
  setJump(jump: DopeMenuJump): void;
  setExtras?(items: DopeMenuExtra[]): void;
}

export interface GameInitLike {
  scheme?: unknown;
  state?: unknown;
  fest?: unknown;
  [key: string]: unknown;
}

interface PageGlobals {
  dopeMenu?: DopeMenuLike;
  __GAME_INIT__?: GameInitLike | null;
}

export interface GameBreadcrumbsOptions {
  festTitle?: string;
  gameTitle?: string;
  currentTitle?: string;
  festHref?: string;
  gameHref?: string;
}

export function renderGameBreadcrumbs(root: HTMLElement | null | undefined, options: GameBreadcrumbsOptions = {}): void {
  if (!root) return;
  const festTitle = String(options.festTitle || "Фест").trim() || "Фест";
  const gameTitle = String(options.gameTitle || "Игра").trim() || "Игра";
  const currentTitle = String(options.currentTitle || "").trim();
  root.replaceChildren();

  const festLink = document.createElement("a");
  festLink.className = "game-breadcrumbs-fest";
  festLink.href = options.festHref || "/";
  festLink.textContent = festTitle;
  root.appendChild(festLink);

  root.appendChild(breadcrumbSeparator());
  if (options.gameHref && currentTitle && currentTitle !== gameTitle) {
    const gameLink = document.createElement("a");
    gameLink.className = "game-breadcrumbs-game";
    gameLink.href = options.gameHref;
    gameLink.textContent = gameTitle;
    root.appendChild(gameLink);
    root.appendChild(breadcrumbSeparator());
    const current = document.createElement("span");
    current.className = "game-breadcrumbs-current";
    current.textContent = currentTitle;
    root.appendChild(current);
  } else {
    const game = document.createElement("span");
    game.className = "game-breadcrumbs-game";
    game.textContent = currentTitle || gameTitle;
    root.appendChild(game);
  }
}

function breadcrumbSeparator(): HTMLElement {
  const sep = document.createElement("span");
  sep.className = "game-breadcrumbs-sep";
  sep.textContent = "/";
  sep.setAttribute("aria-hidden", "true");
  return sep;
}

export function mountEditorLink(): {refresh(): void} {
  const set = () => (window as Window & PageGlobals).dopeMenu?.setJump({
    label: "Редактировать",
    href: editorHrefForCurrentLocation(),
    title: "Открыть в режиме редактирования",
  });
  set();
  return {refresh: set};
}

function editorHrefForCurrentLocation(): string {
  return "/host" + window.location.pathname + window.location.search;
}

// mountUnnumberedBanner shows a sticky notice when the fest has teams without
// numbers. Team number is the universal team identity, so the server blocks
// result editing until every team is numbered (409); this points the host at
// the numbers page. Idempotent — re-mounting is a no-op while the banner is up.
export function mountUnnumberedBanner(festID: string | number | null | undefined): HTMLElement | null {
  if (!festID || document.querySelector(".dope-unnumbered-banner")) return null;
  const bar = document.createElement("div");
  bar.className = "dope-unnumbered-banner";
  Object.assign(bar.style, {
    position: "sticky",
    top: "0",
    zIndex: "2147483600",
    background: "var(--amber-bg)",
    color: "var(--amber-text-strong)",
    font: "13px/1.4 system-ui, sans-serif",
    padding: "8px 12px",
    textAlign: "center",
    borderBottom: "1px solid var(--amber-border)",
  });
  bar.append("Командам не присвоены номера — редактирование результатов заблокировано. ");
  const link = document.createElement("a");
  link.href = `/host/fest/${festID}/numbers`;
  link.textContent = "Присвоить номера";
  Object.assign(link.style, {color: "inherit", fontWeight: "600", textDecoration: "underline"});
  bar.appendChild(link);
  document.body.prepend(bar);
  return bar;
}

export function mountViewerLink(): {refresh(): void} {
  const set = () => (window as Window & PageGlobals).dopeMenu?.setJump({
    label: "Страница зрителя",
    href: viewerHrefForCurrentLocation(),
    title: "Открыть зрительскую страницу",
    external: true,
  });
  set();
  return {refresh: set};
}

function viewerHrefForCurrentLocation(): string {
  const path = window.location.pathname.replace(/^\/host(?=\/|$)/, "");
  return (path || "/") + window.location.search;
}

// mountGameDownloads registers the game's archive download links in the ☰ menu:
// "Скачать XLSX" for everyone, and "Скачать .json.gz" (current state + edit
// history) for hosts only. apiBase is the game's /api/fest/.../games/... base.
export function mountGameDownloads(opts: {apiBase?: string; canEdit?: boolean} | null | undefined): void {
  const apiBase = opts && opts.apiBase;
  const menu = (window as Window & PageGlobals).dopeMenu;
  if (!apiBase || !menu?.setExtras) return;
  const items: DopeMenuExtra[] = [{
    label: "Скачать XLSX",
    href: `${apiBase}/export.xlsx`,
    title: "Скачать таблицу игры в формате XLSX",
    download: true,
  }];
  if (opts.canEdit) {
    items.push({
      label: "Скачать .json.gz",
      href: `${apiBase}/export.json.gz`,
      title: "Скачать текущее состояние игры и историю правок",
      download: true,
    });
  }
  menu.setExtras(items);
}

export interface GameRoute {
  viewer?: boolean;
  festID?: string;
  gameID?: string;
  apiBase?: string;
}

export function parseGameRoute(pathname: string = window.location.pathname): GameRoute {
  const host = pathname.match(/^\/host\/fest\/([^/]+)\/game\/([^/]+)/);
  if (host) {
    return {
      viewer: false,
      festID: host[1],
      gameID: host[2],
      apiBase: `/api/fest/${host[1]}/games/${host[2]}`,
    };
  }
  const pub = pathname.match(/^\/fest\/([^/]+)\/game\/([^/]+)/);
  if (pub) {
    return {
      viewer: true,
      festID: pub[1],
      gameID: pub[2],
      apiBase: `/api/fest/${pub[1]}/games/${pub[2]}`,
    };
  }
  return {};
}

export interface LocalCache {
  key: string;
  read(): unknown;
  write(value: unknown): void;
}

// createLocalCache wraps one localStorage slot in the read/write-with-try/catch
// discipline every page used to copy verbatim: reads degrade to null and writes
// are silently dropped when storage is unavailable (private mode, quota, disabled
// cookies). The page owns the key string and what JSON shape it stores.
export function createLocalCache(key: string): LocalCache {
  return {
    key,
    read() {
      try {
        const raw = window.localStorage.getItem(key);
        return raw ? JSON.parse(raw) : null;
      } catch (_err) {
        return null;
      }
    },
    write(value) {
      if (value == null) return;
      try {
        window.localStorage.setItem(key, JSON.stringify(value));
      } catch (_err) {
        // ignore (quota / disabled / private mode)
      }
    },
  };
}

// notifyEmbeddedResize posts the current document height to the parent frame so
// an embedding host can size its iframe to the content. A no-op outside an
// embed (no parent, or not the ?embed=1 view).
export function notifyEmbeddedResize(embedded: boolean): void {
  if (!embedded || window.parent === window) return;
  window.requestAnimationFrame(() => {
    window.parent.postMessage({
      type: "dope:resize",
      height: Math.max(document.documentElement.scrollHeight, document.body.scrollHeight),
    }, window.location.origin);
  });
}

export interface GameDataSnapshot {
  scheme: unknown;
  state: unknown;
  fest: unknown;
}

// fetchGameData loads the three cold payloads a game page needs — scheme, state,
// and (when the route is fest-scoped) the fest view — in parallel, throwing the
// server's error text on any non-OK response. The fest fetch is skipped when the
// route carries neither an apiBase nor a festID.
export async function fetchGameData(route: GameRoute): Promise<GameDataSnapshot> {
  const festURL = route.apiBase || (route.festID ? `/api/fest/${route.festID}` : "");
  const [schemeResp, stateResp, festResp] = await Promise.all([
    fetch(`${route.apiBase}/scheme`),
    fetch(`${route.apiBase}/state`),
    festURL ? fetch(festURL) : Promise.resolve(null),
  ]);
  if (!schemeResp.ok) throw new Error(await schemeResp.text());
  if (!stateResp.ok) throw new Error(await stateResp.text());
  if (festResp && !festResp.ok) throw new Error(await festResp.text());
  return {
    scheme: await schemeResp.json(),
    state: await stateResp.json(),
    fest: festResp ? await festResp.json() : null,
  };
}

export type GameDataSource = "init" | "cache" | "fetch";

export interface AdoptedGameSnapshot extends GameDataSnapshot {
  init?: GameInitLike;
}

export interface GameDataLoaderOptions {
  route: GameRoute;
  cachePrefix: string;
  adopt: (snapshot: AdoptedGameSnapshot, source: GameDataSource) => void;
  revalidate?: () => unknown;
}

export interface GameDataLoader {
  load(): Promise<void>;
  cache: LocalCache;
  writeSnapshot(snap: GameDataSnapshot | null | undefined): void;
}

// createGameDataLoader centralizes the stale-while-revalidate hydration flow the
// OD and KSI pages share: render instantly from the server-inlined __GAME_INIT__
// payload, else from the localStorage snapshot, else from a cold parallel fetch;
// and in the first two cases kick a background revalidation. The page supplies
// `adopt(snapshot, source)` — which assigns its own scheme/state/fest closures and
// renders — and an optional `revalidate()`. `source` is "init" | "cache" | "fetch";
// on "init" the snapshot also carries the raw `init` payload for seq/epoch/banner
// wiring that only the inlined path has. Returns {load, cache} where `load()`
// mirrors the old loadAll(): it resolves synchronously off init/cache (revalidation
// runs detached) and awaits the network only on a cold start.
export function createGameDataLoader({route, cachePrefix, adopt, revalidate}: GameDataLoaderOptions): GameDataLoader {
  const cache = createLocalCache(`${cachePrefix}:game:${route.festID || ""}:${route.gameID || ""}`);
  function writeSnapshot(snap: GameDataSnapshot | null | undefined): void {
    if (snap && snap.scheme && snap.state) cache.write({scheme: snap.scheme, state: snap.state, fest: snap.fest ?? null});
  }
  function kickRevalidate(): void {
    if (revalidate) Promise.resolve().then(revalidate).catch((error: unknown) => console.error(error));
  }
  async function load(): Promise<void> {
    const init = (window as Window & PageGlobals).__GAME_INIT__;
    if (init && init.scheme && init.state) {
      (window as Window & PageGlobals).__GAME_INIT__ = null;
      const snap = {scheme: init.scheme, state: init.state, fest: init.fest || null};
      adopt({...snap, init}, "init");
      writeSnapshot(snap);
      kickRevalidate();
      return;
    }
    const cached = cache.read() as {scheme?: unknown; state?: unknown; fest?: unknown} | null;
    if (cached && cached.scheme && cached.state) {
      adopt({scheme: cached.scheme, state: cached.state, fest: cached.fest || null}, "cache");
      kickRevalidate();
      return;
    }
    const fresh = await fetchGameData(route);
    adopt(fresh, "fetch");
    writeSnapshot(fresh);
  }
  return {load, cache, writeSnapshot};
}
