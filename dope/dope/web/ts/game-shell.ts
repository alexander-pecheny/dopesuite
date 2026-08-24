// The game shell: what every game page mounts before it draws its own thing.
// One call takes the page's identity (app, route, the init payload's common
// flags) and gives back the handles the four pages used to build by hand:
// the ☰ menu's jump links and downloads, the unnumbered-teams banner, the
// status dot and viewer counter, the client recorder, the header trail and
// document title, and host presence — whose cursors are declared as a list of
// element kinds, the same data-* keys the sheet cursor addresses cells by.
// `host` is never passed: it is the route's.

import {createLiveEvents, createScopedWriter, createSyncIndicator, createHostPresence, gameEventsURL, installClientRecorder, scheduleStaticReload} from "./state-sync.js";
import type {ClientRecorder, HostPresence, LiveEvents, PatchPath, ScopedWriter, SyncIndicator} from "./state-sync.js";
import {createStatusReporter, createViewerCounter} from "./widgets.js";
import {createGameDataLoader, fetchGameData, mountEditorLink, mountGameDownloads, mountUnnumberedBanner, mountViewerLink, renderGameBreadcrumbs} from "./game-page.js";
import type {GameDataSnapshot, GameRoute} from "./game-page.js";
import {cssEscape} from "./cells.js";

// One kind of element a host's cursor can rest on: the selector that finds it
// (a comma list is fine; the first kind whose selector matches wins) and the
// data-* keys that identify one such element on every host's screen.
export interface CursorKind {
  selector: string;
  keys: string[];
}

export interface GameChrome {
  festTitle: string;
  gameTitle: string;
  // The game's own href when the current page is a section below it (ЭК's
  // Площадки, Статистика…), and that section's title; both empty at the game.
  gameHref?: string;
  currentTitle?: string;
}

export interface GameShellSpec {
  app: "od" | "ksi" | "ek" | "brain" | "multi" | "troika";
  root: HTMLElement;
  statusNode?: HTMLElement | null;
  breadcrumbsNode?: HTMLElement | null;
  festID?: string;
  gameID?: string;
  viewer: boolean;
  apiBase?: string;
  // The init payload's common flags, read before a loader consumes it.
  init?: Record<string, unknown> | null;
  // An embedded view (ЭК's ?embed=1) mounts no menu items and no presence.
  embedded?: boolean;
  // Whether the ☰ menu offers the game's XLSX/archive downloads (the server
  // exports ОД, КСИ and ЭК; not брейн). Default true.
  downloads?: boolean;
  chrome: () => GameChrome;
  cursorKinds: Record<string, CursorKind>;
  // The element the cursor rests on when nothing is focused (the sheet's
  // active cell), so a host who clicked away still shows where they are.
  activeCursorElement?: () => Element | null;
  recorderState?: () => unknown;
}

export interface GameShell {
  readonly viewer: boolean;
  readonly canEdit: boolean;
  readonly staticMode: boolean;
  // The numeric game id the server scopes events by (the URL may carry a slug).
  readonly scopeGameID: string;
  readonly indicator: SyncIndicator;
  readonly viewerCounter: {setCount(count: unknown): void};
  readonly recorder: ClientRecorder | null;
  // Repaint the header trail and the document title from spec.chrome().
  renderChrome(): void;
  // Re-point the menu's jump link after an in-page navigation.
  refreshLinks(): void;
  presence: {connect(): void; refresh(): void; publish(): void; fromElement(element: Element | EventTarget | null): void};
}

export function mountGamePage(spec: GameShellSpec): GameShell {
  const viewer = spec.viewer;
  const canEdit = Boolean(spec.init?.canEdit);
  const staticMode = Boolean(spec.init?.static);
  const scopeGameID = spec.init?.gameID != null ? String(spec.init.gameID) : spec.gameID || "";
  const embedded = Boolean(spec.embedded);
  document.body.classList.toggle("viewer-readonly", viewer);

  let jumpLink: {refresh(): void} | null = null;
  if (!embedded) {
    if (viewer) {
      if (canEdit) jumpLink = mountEditorLink();
    } else {
      jumpLink = mountViewerLink();
      if (spec.init?.teamsUnnumbered) mountUnnumberedBanner(spec.festID);
    }
    if (spec.apiBase && spec.downloads !== false) mountGameDownloads({apiBase: spec.apiBase, canEdit});
  }

  const indicator = createSyncIndicator(createStatusReporter(spec.statusNode));
  const viewerCounter = createViewerCounter(spec.statusNode);
  const recorder = installClientRecorder({
    scope: `${spec.app}:${spec.festID || ""}:${spec.gameID || ""}`,
    getState: spec.recorderState,
    // Editors always get the download button; spectators only when they add
    // ?log to the URL, so the diagnostic UI stays off the public view.
    showButton: !viewer || /[?&]log\b/.test(location.search),
  });

  function renderChrome(): void {
    const chrome = spec.chrome();
    const gameTitle = String(chrome.gameTitle || "").trim() || "Игра";
    const festTitle = String(chrome.festTitle || "").trim();
    const currentTitle = String(chrome.currentTitle || "").trim();
    document.title = festTitle ? `${currentTitle || gameTitle} · ${festTitle}` : currentTitle || gameTitle;
    if (!spec.breadcrumbsNode || !spec.festID) return;
    renderGameBreadcrumbs(spec.breadcrumbsNode, {
      host: !viewer,
      festHref: viewer ? `/fest/${spec.festID}` : `/host/fest/${spec.festID}`,
      festTitle: festTitle || "Фест",
      gameHref: chrome.gameHref || "",
      gameTitle,
      currentTitle,
    });
  }

  // ---- presence: the cursor as a kind plus its keys -------------------------

  const kinds = Object.entries(spec.cursorKinds);
  const dataAttr = (key: string) => "data-" + key.replace(/[A-Z]/g, (c) => "-" + c.toLowerCase());

  function cursorFromElement(element: Element | EventTarget | null): Record<string, unknown> | null {
    if (!(element instanceof Element)) return null;
    for (const [kind, {selector, keys}] of kinds) {
      const target = element.closest<HTMLElement>(selector);
      if (!target || !spec.root.contains(target)) continue;
      const cursor: Record<string, unknown> = {app: spec.app, kind, gameID: spec.gameID};
      for (const key of keys) cursor[key] = target.dataset[key] ?? "";
      return cursor;
    }
    return null;
  }

  function findTarget(raw: unknown): Element | null {
    const cursor = raw as Record<string, unknown> | null;
    if (!cursor || cursor.app !== spec.app || String(cursor.gameID) !== String(spec.gameID)) return null;
    const kind = spec.cursorKinds[String(cursor.kind)];
    if (!kind) return null;
    const attrs = kind.keys.map((key) => `[${dataAttr(key)}="${cssEscape(String(cursor[key] ?? ""))}"]`).join("");
    const selector = kind.selector.split(",").map((part) => part.trim() + attrs).join(",");
    return spec.root.querySelector(selector);
  }

  let presence: HostPresence | null = null;
  function connectPresence(): void {
    if (presence || viewer || embedded || !spec.festID) return;
    presence = createHostPresence({
      root: spec.root,
      eventsURL: `/host-events?fest_id=${encodeURIComponent(spec.festID)}`,
      presenceURL: `/api/fest/${spec.festID}/presence`,
      cursorFromElement,
      getCursor: () => cursorFromElement(document.activeElement) || cursorFromElement(spec.activeCursorElement?.() || null),
      findTarget,
    });
    presence.connect();
  }

  return {
    viewer,
    canEdit,
    staticMode,
    scopeGameID,
    indicator,
    viewerCounter,
    recorder,
    renderChrome,
    refreshLinks: () => jumpLink?.refresh(),
    presence: {
      connect: connectPresence,
      refresh: () => presence?.refresh(),
      publish: () => presence?.publishCurrent(),
      fromElement: (element) => presence?.publishFromElement(element),
    },
  };
}

// ---- the game document ----

// A page whose whole Protocol document is one state blob (ОД, КСИ) lives
// through one lifecycle: the snapshot (init payload, then the cache, then the
// API) is adopted; the live events and the scoped writer are built on the
// game-state scope; a remote state arrives overlaid with this page's un-acked
// edits; a revalidation re-fetches and re-adopts only what changed. The pages
// used to spell this out each (byte for byte); mountGameDocument is that
// composition over the ADR-0015 primitives, and the page keeps two callbacks:
// adopt a fresh document, apply a remote state.
export interface GameDocumentSpec {
  route: GameRoute;
  cachePrefix: string;
  shell: GameShell;
  // A fresh document: assign scheme/state/fest, derive, render.
  adopt: (snapshot: GameDataSnapshot) => void;
  // A remote state, already overlaid with the page's un-acked edits.
  apply: (state: unknown) => void;
  // What the page holds now — the revalidation's baseline, the live base.
  current: () => GameDataSnapshot;
  // Test seam: the stream constructor the engine opens (an EventSource).
  newEventSource?: (url: string) => EventSource;
}

export interface GameDocument {
  // The SSE scope key of the whole document.
  readonly scope: string;
  // Adopt the first snapshot, connect the events and the host presence.
  load(): Promise<void>;
  // One patch of the document, coalesced and retried by the writer.
  save(path: PatchPath, value: unknown): void;
  // A remote state with the page's pending edits overlaid.
  overlay(state: unknown): unknown;
  // Re-send the un-acked edits of a previous load; true when there were any.
  recover(): boolean;
  // Whether an edit at (or above) path is still un-acked.
  isPending(path: PatchPath): boolean;
  sync(): {live: LiveEvents; writer: ScopedWriter};
}

export function mountGameDocument(spec: GameDocumentSpec): GameDocument {
  const {route, shell} = spec;
  const scope = `game-state:${shell.scopeGameID}`;
  let stateSeq = 0; // the scope seq the page's state is at; seeded by the init payload, moved by the engine
  let epoch = ""; // the server epoch at page render; the engine's baseline
  let live: LiveEvents | null = null;
  let writer: ScopedWriter | null = null;

  const loader = createGameDataLoader({
    route,
    cachePrefix: spec.cachePrefix,
    adopt: (snapshot) => {
      if (snapshot.init) {
        if (snapshot.init.seq != null) stateSeq = Number(snapshot.init.seq) || 0;
        if (snapshot.init.epoch != null) epoch = String(snapshot.init.epoch);
      }
      spec.adopt(snapshot);
    },
    revalidate: async () => {
      const before = JSON.stringify(spec.current());
      const fresh = await fetchGameData(route);
      loader.writeSnapshot(fresh);
      if (JSON.stringify({scheme: fresh.scheme, state: fresh.state, fest: fresh.fest ?? null}) === before) return;
      spec.adopt(fresh);
    },
  });

  function sync(): {live: LiveEvents; writer: ScopedWriter} {
    if (live && writer) return {live, writer};
    writer = createScopedWriter({
      readonly: shell.viewer,
      urlOf: () => `${route.apiBase}/state`,
      adopt: (_scope, response) => spec.apply(overlay(response)),
      indicator: shell.indicator,
      recorder: () => shell.recorder,
      onRejected: (info) => shell.recorder?.event("write-rejected", info),
    });
    live = createLiveEvents({
      eventsURL: () => gameEventsURL(route.festID!, route.gameID),
      gameID: shell.scopeGameID,
      epoch,
      scopes: [{
        prefix: scope,
        base: () => ({data: spec.current().state, seq: stateSeq}),
        adopt: (_scope, view) => {
          stateSeq = view.seq;
          spec.apply(overlay(view.data));
        },
        stateURL: () => `${route.apiBase}/state`,
      }],
      indicator: shell.indicator,
      onViewers: (count) => shell.viewerCounter.setCount(count),
      onLockdown: scheduleStaticReload,
      staticMode: () => shell.staticMode,
      recorder: () => shell.recorder,
      newEventSource: spec.newEventSource,
    });
    return {live, writer};
  }

  function overlay(state: unknown): unknown {
    return writer ? writer.overlay(scope, state) : state;
  }

  function recover(): boolean {
    return sync().writer.recover(scope) > 0;
  }

  async function load(): Promise<void> {
    await loader.load();
    shell.indicator.touch();
    sync().live.connect();
    // Un-acked edits a previous load persisted (a refresh mid-sync): show them
    // overlaid, with their pending spinner, and let the writer re-send them.
    if (recover()) spec.apply(overlay(spec.current().state));
    shell.presence.connect();
  }

  return {
    scope,
    load,
    save: (path, value) => sync().writer.patch(scope, path, value),
    overlay,
    recover,
    isPending: (path) => Boolean(writer?.isPending(scope, path)),
    sync,
  };
}
