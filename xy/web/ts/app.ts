// app.ts — shared frontend helpers. API fetch wrappers, HTML escaping, tiny
// DOM builder, derived card titles.

export interface AuthMe {
  user_id: number;
  username?: string | null;
  telegram?: string | null;
  default_author?: string | null;
  card_title?: string | null;
  sizes?: unknown;
}

async function fetchJSON(url: string, init?: RequestInit): Promise<unknown> {
  const res = await fetch(url, { credentials: "same-origin", ...init });
  if (!res.ok) {
    const text = (await res.text()).trim();
    throw new Error(text || `HTTP ${res.status}`);
  }
  if (res.status === 204) return null;
  return res.json();
}

async function fetchVoid(url: string, init?: RequestInit): Promise<void> {
  const res = await fetch(url, { credentials: "same-origin", ...init });
  if (!res.ok) {
    const text = (await res.text()).trim();
    throw new Error(text || `HTTP ${res.status}`);
  }
}

function jpost(url: string, body: unknown): Promise<unknown> {
  return fetchJSON(url, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
}
function jpatch(url: string, body: unknown): Promise<void> {
  return fetchVoid(url, { method: "PATCH", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
}
function jput(url: string, body: unknown): Promise<void> {
  return fetchVoid(url, { method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
}
function jdelete(url: string): Promise<void> {
  return fetchVoid(url, { method: "DELETE" });
}

function escapeHtml(s: unknown): string {
  return String(s).replace(/[&<>"']/g, (c) => (
    { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" } as Record<string, string>
  )[c]!);
}

export type ElChild = Node | string | number | boolean | null | undefined | ElChild[];
export type ElProps = Record<string, unknown>;

// el(tag, props, ...children) — minimal DOM builder. props.class, props.text,
// event handlers as on*, everything else set as attribute/property.
function el(tag: string, props: ElProps = {}, ...children: ElChild[]): HTMLElement {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(props)) {
    if (k === "class") node.className = String(v);
    else if (k === "text") node.textContent = String(v);
    else if (k === "dataset") Object.assign(node.dataset, v);
    else if (k.startsWith("on") && typeof v === "function") {
      node.addEventListener(k.slice(2).toLowerCase(), v as EventListener);
    } else if (v === true) node.setAttribute(k, "");
    else if (v !== false && v != null) node.setAttribute(k, String(v));
  }
  for (const c of children.flat(Infinity as 1) as Array<Exclude<ElChild, ElChild[]>>) {
    if (c == null || c === false) continue;
    node.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return node;
}

// deriveTitle turns a card's plain-text description into a short preview: all
// whitespace (incl. line breaks) collapsed to single spaces, then trimmed to a
// word boundary at `max` chars. Flowing across lines — rather than stopping at
// the first line — keeps the preview useful for questions whose first line is
// uninformative (handout "Раздаточный материал:", duplet/blitz lead-ins).
function deriveTitle(desc: string | null | undefined, max = 80): string {
  const t = (desc || "").replace(/\s+/g, " ").trim();
  if (!t) return "(пусто)";
  if (t.length <= max) return t;
  const cut = t.slice(0, max);
  const sp = cut.lastIndexOf(" ");
  return (sp > max * 0.5 ? cut.slice(0, sp) : cut) + "…";
}

async function requireLogin(): Promise<AuthMe | { offline: true } | null> {
  try {
    const me = (await fetchJSON("/api/auth/me")) as AuthMe | null;
    if (me && me.user_id) return me;
  } catch (e) {
    // A network failure (offline) throws TypeError; an HTTP error (e.g. 401)
    // throws a plain Error. Only bounce to /login for the latter — offline, we
    // let the page boot and render from the cached mirror.
    if (e instanceof TypeError || (typeof navigator !== "undefined" && navigator.onLine === false)) {
      return { offline: true };
    }
  }
  window.location.replace("/login");
  return null;
}

// ---- board display sizes (workspace width / list width / card height) ----
// A per-user, all-boards preference stored server-side (users.sizes, plain
// JSON) and edited on /profile; the board page applies it from the snapshot.
// Shared here because the write path (profile.ts) and the read path (board.ts)
// must agree on defaults, ranges and the null="unlimited" convention.
// 1512 = the logical width of a 14" MacBook screen — a board that fills a
// laptop and stays a centred column on anything wider.
export interface Sizes {
  boardW: number | null;
  listW: number;
  cardLines: number | null;
  cardFont: number;
}

const SIZES_DEFAULT: Sizes = { boardW: 1512, listW: 280, cardLines: 3, cardFont: 14 };
const SIZES_RANGE = {
  BOARD_W_MIN: 800, BOARD_W_MAX: 3200,   // MAX = «вся ширина»
  LIST_W_MIN: 200, LIST_W_MAX: 640,
  CARD_LINES_MAX: 12,                    // MAX = «без ограничения»
  CARD_FONT_MIN: 10, CARD_FONT_MAX: 18,  // px; default 14 = --text-sm
};

const inRange = (n: number, lo: number, hi: number): boolean => Number.isFinite(n) && n >= lo && n < hi;
// null is a *choice* ("no cap"), so it must survive a round-trip — only a missing
// or out-of-range value falls back to the default.
const pickSize = (v: unknown, lo: number, hi: number, dflt: number | null): number | null =>
  v === null ? null : inRange(Number(v), lo, hi) ? Number(v) : dflt;

// Clamp a raw sizes object (from the snapshot/me, or null when never set) into
// the usable shape. The server stores whatever the client sent, so validation
// lives here — a stale or hand-edited value can never break the layout.
function sanitizeSizes(s: unknown): Sizes {
  if (!s || typeof s !== "object") return { ...SIZES_DEFAULT };
  const raw = s as Partial<Record<keyof Sizes, unknown>>;
  return {
    boardW: pickSize(raw.boardW, SIZES_RANGE.BOARD_W_MIN, SIZES_RANGE.BOARD_W_MAX, SIZES_DEFAULT.boardW),
    listW: pickSize(raw.listW, SIZES_RANGE.LIST_W_MIN, SIZES_RANGE.LIST_W_MAX + 1, SIZES_DEFAULT.listW) ?? SIZES_DEFAULT.listW,
    cardLines: pickSize(raw.cardLines, 1, SIZES_RANGE.CARD_LINES_MAX, SIZES_DEFAULT.cardLines),
    // no null sentinel here — sizes saved before this knob existed carry
    // cardFont: null (the server canonicalizes absent fields to null)
    cardFont: inRange(Number(raw.cardFont), SIZES_RANGE.CARD_FONT_MIN, SIZES_RANGE.CARD_FONT_MAX + 1)
      ? Number(raw.cardFont) : SIZES_DEFAULT.cardFont,
  };
}

// applySizes drives the three CSS vars; `root` defaults to <html> (the board),
// the profile preview passes its own container instead.
function applySizes(s: Sizes, root: HTMLElement = document.documentElement): void {
  root.style.setProperty("--kanban-max-w", s.boardW == null ? "none" : s.boardW + "px");
  root.style.setProperty("--klist-w", s.listW + "px");
  root.style.setProperty("--kcard-lines", s.cardLines == null ? "none" : String(s.cardLines));
  root.style.setProperty("--kcard-font", s.cardFont + "px");
}

// plusIcon draws the "+" the UI used to spell with the ➕ emoji: an inline SVG
// stroked in currentColor, so it follows the button's text color — the emoji
// glyph is dark on every platform and all but disappears on the dark theme.
function plusIcon(): SVGSVGElement {
  const NS = "http://www.w3.org/2000/svg";
  const svg = document.createElementNS(NS, "svg");
  svg.setAttribute("class", "plus-ico");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("aria-hidden", "true");
  const p = document.createElementNS(NS, "path");
  p.setAttribute("d", "M12 5v14M5 12h14");
  p.setAttribute("fill", "none");
  p.setAttribute("stroke", "currentColor");
  p.setAttribute("stroke-width", "2.5");
  p.setAttribute("stroke-linecap", "round");
  svg.append(p);
  return svg;
}

// swapPlusIcon replaces a compiled page button's leading ➕ with plusIcon().
// The .dopeui vocabulary has no svg primitive, so the pages ship the emoji as
// authored and each page's script swaps it at boot.
function swapPlusIcon(btn: HTMLElement): void {
  const rest = (btn.textContent ?? "").replace(/^➕\s*/, "");
  btn.replaceChildren(plusIcon(), ...(rest ? [" " + rest] : []));
}

// wireGenPassphrase makes `button` fill `input` with a fresh passphrase and copy
// it to the clipboard. Shared by the board-create and Trello-import flows (both
// mint a new board from a passphrase). `generate` is injected so this module
// stays free of the crypto dependency. No status text — the field visibly
// changing on each click is confirmation enough, and the copy is silent.
function wireGenPassphrase(button: HTMLElement, input: HTMLInputElement, generate: () => string): void {
  button.addEventListener("click", async () => {
    const pass = generate();
    input.value = pass;
    input.focus();
    try {
      await navigator.clipboard.writeText(pass);
    } catch { /* clipboard unavailable/denied — the passphrase is visible in the field */ }
  });
}

export const xySizes = { DEFAULT: SIZES_DEFAULT, ...SIZES_RANGE, sanitize: sanitizeSizes, apply: applySizes };

export const xyApp = { fetchJSON, fetchVoid, jpost, jpatch, jput, jdelete, escapeHtml, el, deriveTitle, requireLogin, plusIcon, swapPlusIcon, wireGenPassphrase };
