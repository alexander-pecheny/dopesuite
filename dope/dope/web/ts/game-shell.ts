// The game shell: what every game page mounts before it draws its own thing.
// One call takes the page's identity (app, route, the init payload's common
// flags) and gives back the handles the four pages used to build by hand:
// the ☰ menu's jump links and downloads, the unnumbered-teams banner, the
// status dot and viewer counter, the client recorder, the header trail and
// document title, and host presence — whose cursors are declared as a list of
// element kinds, the same data-* keys the sheet cursor addresses cells by.
// `host` is never passed: it is the route's.

import {createSyncIndicator, createHostPresence, installClientRecorder} from "./state-sync.js";
import type {ClientRecorder, HostPresence, SyncIndicator} from "./state-sync.js";
import {createStatusReporter, createViewerCounter} from "./widgets.js";
import {mountEditorLink, mountGameDownloads, mountUnnumberedBanner, mountViewerLink, renderGameBreadcrumbs} from "./game-page.js";
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
  app: "od" | "ksi" | "ek" | "brain";
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
