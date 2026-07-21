// app.js — shared frontend helpers (ES module + window.xyApp global).
// API fetch wrappers, HTML escaping, tiny DOM builder, derived card titles.

async function fetchJSON(url, init) {
  const res = await fetch(url, { credentials: "same-origin", ...init });
  if (!res.ok) {
    const text = (await res.text()).trim();
    throw new Error(text || `HTTP ${res.status}`);
  }
  if (res.status === 204) return null;
  return res.json();
}

async function fetchVoid(url, init) {
  const res = await fetch(url, { credentials: "same-origin", ...init });
  if (!res.ok) {
    const text = (await res.text()).trim();
    throw new Error(text || `HTTP ${res.status}`);
  }
}

function jpost(url, body) {
  return fetchJSON(url, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
}
function jpatch(url, body) {
  return fetchVoid(url, { method: "PATCH", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
}
function jput(url, body) {
  return fetchVoid(url, { method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
}
function jdelete(url) {
  return fetchVoid(url, { method: "DELETE" });
}

function escapeHtml(s) {
  return String(s).replace(/[&<>"']/g, (c) => (
    { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]
  ));
}

// el(tag, props, ...children) — minimal DOM builder. props.class, props.text,
// event handlers as on*, everything else set as attribute/property.
function el(tag, props = {}, ...children) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(props)) {
    if (k === "class") node.className = v;
    else if (k === "text") node.textContent = v;
    else if (k === "dataset") Object.assign(node.dataset, v);
    else if (k.startsWith("on") && typeof v === "function") node.addEventListener(k.slice(2).toLowerCase(), v);
    else if (v === true) node.setAttribute(k, "");
    else if (v !== false && v != null) node.setAttribute(k, v);
  }
  for (const c of children.flat()) {
    if (c == null || c === false) continue;
    node.append(c.nodeType ? c : document.createTextNode(String(c)));
  }
  return node;
}

// deriveTitle turns a card's plain-text description into a short preview: all
// whitespace (incl. line breaks) collapsed to single spaces, then trimmed to a
// word boundary at `max` chars. Flowing across lines — rather than stopping at
// the first line — keeps the preview useful for questions whose first line is
// uninformative (handout "Раздаточный материал:", duplet/blitz lead-ins).
function deriveTitle(desc, max = 80) {
  const t = (desc || "").replace(/\s+/g, " ").trim();
  if (!t) return "(пусто)";
  if (t.length <= max) return t;
  const cut = t.slice(0, max);
  const sp = cut.lastIndexOf(" ");
  return (sp > max * 0.5 ? cut.slice(0, sp) : cut) + "…";
}

async function requireLogin() {
  try {
    const me = await fetchJSON("/api/auth/me");
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
// Shared here because the write path (profile.js) and the read path (board.js)
// must agree on defaults, ranges and the null="unlimited" convention.
// 1512 = the logical width of a 14" MacBook screen — a board that fills a
// laptop and stays a centred column on anything wider.
const SIZES_DEFAULT = { boardW: 1512, listW: 280, cardLines: 3, cardFont: 14 };
const SIZES_RANGE = {
  BOARD_W_MIN: 800, BOARD_W_MAX: 3200,   // MAX = «вся ширина»
  LIST_W_MIN: 200, LIST_W_MAX: 640,
  CARD_LINES_MAX: 12,                    // MAX = «без ограничения»
  CARD_FONT_MIN: 10, CARD_FONT_MAX: 18,  // px; default 14 = --text-sm
};

const inRange = (n, lo, hi) => Number.isFinite(n) && n >= lo && n < hi;
// null is a *choice* ("no cap"), so it must survive a round-trip — only a missing
// or out-of-range value falls back to the default.
const pickSize = (v, lo, hi, dflt) => (v === null ? null : inRange(Number(v), lo, hi) ? Number(v) : dflt);

// Clamp a raw sizes object (from the snapshot/me, or null when never set) into
// the usable shape. The server stores whatever the client sent, so validation
// lives here — a stale or hand-edited value can never break the layout.
function sanitizeSizes(s) {
  if (!s || typeof s !== "object") return { ...SIZES_DEFAULT };
  return {
    boardW: pickSize(s.boardW, SIZES_RANGE.BOARD_W_MIN, SIZES_RANGE.BOARD_W_MAX, SIZES_DEFAULT.boardW),
    listW: pickSize(s.listW, SIZES_RANGE.LIST_W_MIN, SIZES_RANGE.LIST_W_MAX + 1, SIZES_DEFAULT.listW),
    cardLines: pickSize(s.cardLines, 1, SIZES_RANGE.CARD_LINES_MAX, SIZES_DEFAULT.cardLines),
    // no null sentinel here — sizes saved before this knob existed carry
    // cardFont: null (the server canonicalizes absent fields to null)
    cardFont: inRange(Number(s.cardFont), SIZES_RANGE.CARD_FONT_MIN, SIZES_RANGE.CARD_FONT_MAX + 1)
      ? Number(s.cardFont) : SIZES_DEFAULT.cardFont,
  };
}

// applySizes drives the three CSS vars; `root` defaults to <html> (the board),
// the profile preview passes its own container instead.
function applySizes(s, root = document.documentElement) {
  root.style.setProperty("--kanban-max-w", s.boardW == null ? "none" : s.boardW + "px");
  root.style.setProperty("--klist-w", s.listW + "px");
  root.style.setProperty("--kcard-lines", s.cardLines == null ? "none" : String(s.cardLines));
  root.style.setProperty("--kcard-font", s.cardFont + "px");
}

// plusIcon draws the "+" the UI used to spell with the ➕ emoji: an inline SVG
// stroked in currentColor, so it follows the button's text color — the emoji
// glyph is dark on every platform and all but disappears on the dark theme.
function plusIcon() {
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
function swapPlusIcon(btn) {
  const rest = btn.textContent.replace(/^➕\s*/, "");
  btn.replaceChildren(plusIcon(), ...(rest ? [" " + rest] : []));
}

// wireGenPassphrase makes `button` fill `input` with a fresh passphrase and copy
// it to the clipboard. Shared by the board-create and Trello-import flows (both
// mint a new board from a passphrase). `generate` is injected so this module
// stays free of the crypto dependency. No status text — the field visibly
// changing on each click is confirmation enough, and the copy is silent.
function wireGenPassphrase(button, input, generate) {
  button.addEventListener("click", async () => {
    const pass = generate();
    input.value = pass;
    input.focus();
    try {
      await navigator.clipboard.writeText(pass);
    } catch (_) { /* clipboard unavailable/denied — the passphrase is visible in the field */ }
  });
}

export const xySizes = { DEFAULT: SIZES_DEFAULT, ...SIZES_RANGE, sanitize: sanitizeSizes, apply: applySizes };

export const xyApp = { fetchJSON, fetchVoid, jpost, jpatch, jput, jdelete, escapeHtml, el, deriveTitle, requireLogin, plusIcon, swapPlusIcon, wireGenPassphrase };
if (typeof window !== "undefined") {
  window.xyApp = xyApp;
  window.xySizes = xySizes;
}
