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

export const xyApp = { fetchJSON, fetchVoid, jpost, jpatch, jput, jdelete, escapeHtml, el, deriveTitle, requireLogin };
if (typeof window !== "undefined") window.xyApp = xyApp;
