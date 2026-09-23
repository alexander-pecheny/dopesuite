// url-state.ts — where a game page keeps its place in the URL. The hash names
// the tab; the query string carries what the viewer chose to look at (the
// Division, ADR-0020). One module owns both, so no page hand-rolls hash parsing
// and, more to the point, so writing one of them can never drop the other: a
// tab switch used to replace the whole URL with `#tab` and take the query with
// it.
//
// Every write is a replaceState: neither a tab nor a Division is a place worth
// a back-button stop, and the browser fires no event for it, so a page's own
// listeners stay quiet when it moves the URL itself.

export interface TabLike {
  key: string;
}

export interface HashTabOptions<T extends TabLike> {
  // canonical migrates a legacy hash onto the tab that answers to it
  // (game-tabs.ts canonicalKey). Without it the hash is read as written.
  canonical?: (tabs: T[], key: string) => string;
}

// rawHash is the hash as the address bar spells it, undecoded — a tab key is
// ASCII, and every page has always compared it this way.
function rawHash(): string {
  return (window.location.hash || "").replace(/^#/, "");
}

// tabFromHash is the tab the hash names, or null when it names none of them —
// which is also how a host-only tab stays out of a viewer's reach: it is not in
// the list, so the hash cannot select it.
export function tabFromHash<T extends TabLike>(tabs: T[], options: HashTabOptions<T> = {}): string | null {
  const key = options.canonical ? options.canonical(tabs, rawHash()) : rawHash();
  return tabs.some((tab) => tab.key === key) ? key : null;
}

// setHashTab puts the tab in the hash and leaves the query string alone.
export function setHashTab(key: string): void {
  if (rawHash() === key) return;
  writeLocation(window.location.pathname + window.location.search + "#" + key);
}

// param is one query parameter, decoded; "" when the URL does not carry it.
export function param(name: string): string {
  return new URL(window.location.href).searchParams.get(name) ?? "";
}

// setParam writes one query parameter, percent-encoding the value and keeping
// the hash; an empty value removes the parameter rather than leaving it blank.
export function setParam(name: string, value: string): void {
  const url = new URL(window.location.href);
  if (value) url.searchParams.set(name, value);
  else url.searchParams.delete(name);
  writeLocation(url.pathname + url.search + url.hash);
}

// onNavigate fires when the browser moves the URL under the page — a hash the
// viewer typed, a back or forward step — but never for the page's own writes.
export function onNavigate(listener: () => void): void {
  window.addEventListener("hashchange", listener);
  window.addEventListener("popstate", listener);
}

function writeLocation(next: string): void {
  if (next === window.location.pathname + window.location.search + window.location.hash) return;
  history.replaceState(null, "", next);
}
