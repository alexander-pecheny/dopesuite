// dom.js — the jstest fake DOM: enough of an Element for modules that build
// with el(), toggle hidden, set text and listen for events. No layout, no
// selectors beyond ids. Every test that needs a page builds one with page().
export class FakeNode {
  constructor(tag = "div", props = {}) {
    this.tag = tag;
    this.hidden = false;
    this._text = "";
    this.value = "";
    this.checked = false;
    this.disabled = false;
    this.title = "";
    this.className = "";
    this.dataset = {};
    this.attrs = {};
    this.kids = [];
    this.handlers = {};
    this.classes = new Set();
    this.parentElement = null;
    this.style = {};
    this.scrollHeight = 0;
    this.offsetWidth = 0;
    this.offsetHeight = 0;
    Object.assign(this, props);
  }
  get classList() {
    const s = this.classes;
    return {
      add: (...c) => c.forEach((x) => s.add(x)),
      remove: (...c) => c.forEach((x) => s.delete(x)),
      toggle: (c, on) => { (on ?? !s.has(c)) ? s.add(c) : s.delete(c); return s.has(c); },
      contains: (c) => s.has(c),
    };
  }
  // textContent replaces the subtree, as in the DOM; reading it flattens it.
  get textContent() { return this._text || this.kids.map((k) => (typeof k === "string" ? k : k.textContent)).join(""); }
  set textContent(v) { this._text = String(v); this.kids = []; }
  get text() { return this.textContent; }
  get id() { return this.attrs.id ?? this._id ?? ""; }
  set id(v) { this._id = v; }
  // The reflected attributes a fresh element takes its properties from.
  setAttribute(k, v) {
    this.attrs[k] = String(v);
    if (k === "value" || k === "title") this[k] = String(v);
    if (k === "checked" || k === "disabled" || k === "hidden") this[k] = true;
  }
  getAttribute(k) { return k in this.attrs ? this.attrs[k] : null; }
  removeAttribute(k) { delete this.attrs[k]; }
  _adopt(n) { if (n && typeof n === "object") n.parentElement = this; return n; }
  append(...n) { for (const x of n.flat()) if (x != null) this.kids.push(this._adopt(x)); }
  appendChild(n) { this.kids.push(this._adopt(n)); return n; }
  prepend(...n) { this.kids.unshift(...n.map((x) => this._adopt(x))); }
  replaceChildren(...n) { this._text = ""; this.kids = n.flat().filter((x) => x != null).map((x) => this._adopt(x)); }
  replaceWith(n) { const p = this.parentElement; if (p) p.kids = p.kids.map((k) => (k === this ? p._adopt(n) : k)); }
  remove() { const p = this.parentElement; if (p) p.kids = p.kids.filter((k) => k !== this); }
  focus() { this.focused = (this.focused || 0) + 1; }
  blur() {}
  getBoundingClientRect() { return { left: 0, top: 0, width: 0, height: 0, right: 0, bottom: 0 }; }
  scrollIntoView() {}
  select() {}
  click() { this.fire("click"); }
  closest(sel) { const cls = sel.startsWith(".") ? sel.slice(1) : null; let n = this; while (n) { if (cls ? n.classes.has(cls) : n.tag === sel) return n; n = n.parentElement; } return null; }
  contains(n) { if (n === this) return true; return this.kids.some((k) => typeof k === "object" && k.contains && k.contains(n)); }
  // querySelector knows tags, .class, #id and [attr] on the subtree.
  *walk() { for (const k of this.kids) if (k && typeof k === "object" && k.walk) { yield k; yield* k.walk(); } }
  matches(sel) {
    if (sel.startsWith(".")) return this.classes.has(sel.slice(1)) || this.className.split(/\s+/).includes(sel.slice(1));
    if (sel.startsWith("#")) return this.id === sel.slice(1);
    const m = /^([a-z]+)?(?:\[([^\]=]+)(?:="?([^"\]]*)"?)?\])?$/.exec(sel);
    if (!m) return false;
    if (m[1] && this.tag !== m[1]) return false;
    if (m[2] && (this.attrs[m[2]] === undefined || (m[3] !== undefined && this.attrs[m[2]] !== m[3]))) return false;
    return true;
  }
  querySelector(sel) { for (const n of this.walk()) if (n.matches(sel)) return n; return null; }
  querySelectorAll(sel) { return [...this.walk()].filter((n) => n.matches(sel)); }
  addEventListener(type, fn) { (this.handlers[type] ||= []).push(fn); }
  removeEventListener(type, fn) { this.handlers[type] = (this.handlers[type] || []).filter((f) => f !== fn); }
  fire(type, ev = {}) {
    const e = { type, target: this, currentTarget: this, preventDefault() {}, stopPropagation() {}, ...ev };
    for (const fn of this.handlers[type] || []) fn(e);
    const on = this["on" + type];
    if (typeof on === "function") on(e);
    return e;
  }
}
export function fakeNode(tag = "div", props = {}) { return new FakeNode(tag, props); }

// page(ids) builds a document whose getElementById knows exactly these ids.
export function page(ids = []) {
  const byId = new Map();
  for (const id of ids) byId.set(id, fakeNode("div", { attrs: { id } }));
  const doc = {
    body: fakeNode("body"),
    // The subtree walk for querySelector covers the ids known to the page.
    *walk() { for (const n of byId.values()) { yield n; yield* n.walk(); } },
    querySelector(sel) { for (const n of doc.walk()) if (n.matches(sel)) return n; return null; },
    querySelectorAll(sel) { return [...doc.walk()].filter((n) => n.matches(sel)); },
    documentElement: fakeNode("html"),
    title: "",
    activeElement: null,
    createElement: (tag) => fakeNode(tag),
    createElementNS: (_ns, tag) => fakeNode(tag),
    createTextNode: (text) => ({ tag: "#text", textContent: text, get text() { return text; } }),
    createDocumentFragment: () => fakeNode("#fragment"),
    getElementById: (id) => byId.get(id) || null,
    addEventListener() {},
    removeEventListener() {},
  };
  return {
    doc,
    byId: (id) => {
      const n = byId.get(id);
      if (!n) throw new Error(`page is missing #${id}`);
      return n;
    },
    node: (id) => byId.get(id),
    add: (id) => { const n = fakeNode("div", { attrs: { id } }); byId.set(id, n); return n; },
  };
}

// A recording overlay stack: open/pop bookkeeping without history.
export function fakeStack() {
  const frames = [];
  const stack = {
    frames,
    open(entry) { frames.push(entry); },
    replace(el, entry) { frames[frames.length - 1] = entry; },
    isTop: (el) => frames.length > 0 && frames[frames.length - 1].el === el,
    depth: () => frames.length,
    // pop runs the top's close as the browser's popstate would.
    async pop() {
      const f = frames.pop();
      if (f) await f.close();
    },
  };
  return stack;
}

// installDOM makes the page the module-level document the dist modules reach
// for (byId, modal(), el()). Call it before importing them.
export function installDOM(ids = []) {
  const p = page(ids);
  globalThis.document = p.doc;
  globalThis.Node = FakeNode;
  globalThis.Element = FakeNode;
  globalThis.HTMLElement = FakeNode;
  globalThis.HTMLInputElement = FakeNode;
  globalThis.window = globalThis;
  globalThis.location = { pathname: "/board/7", href: "http://xy.test/board/7", search: "", hash: "" };
  // A history whose back() fires popstate, so the live overlay stack closes
  // what the modules open.
  const listeners = {};
  globalThis.addEventListener = (type, fn) => { (listeners[type] ||= []).push(fn); };
  globalThis.removeEventListener = (type, fn) => { listeners[type] = (listeners[type] || []).filter((f) => f !== fn); };
  globalThis.history = {
    state: null,
    pushState(st) { this.state = st; },
    replaceState(st) { this.state = st; },
    back() { queueMicrotask(() => { for (const fn of listeners.popstate || []) fn({}); }); },
  };
  p.doc.addEventListener = (type, fn) => { (listeners[type] ||= []).push(fn); };
  p.fire = (type, ev = {}) => { for (const fn of listeners[type] || []) fn(ev); };
  globalThis.alert = (m) => { (globalThis.__alerts ||= []).push(String(m)); };
  globalThis.confirm = () => true;
  return p;
}

// fakeBoard is a Board (panels.ts) over a small mutable state, with recording
// verbs. Every write lands in `writes` as [kind, path, body].
export function fakeBoard(state = {}) {
  const st = {
    role: "owner", name: "Доска", lists: [], groups: [], cards: [], labels: [], sessions: [],
    cardLabels: [], cardSessions: [], tourTesters: [], unread: {}, sizes: {}, defaultAuthor: "",
    cardTitle: "question", feedDefault: "all", timezone: "", announceCities: null, sessionTitleMode: "",
    ...state,
  };
  const writes = [];
  const byRank = (a, b) => (a.rank < b.rank ? -1 : a.rank > b.rank ? 1 : 0);
  const verb = (kind) => async (k, path, body) => { writes.push([kind, k, path, body]); return { id: 1000 + writes.length }; };
  const board = {
    id: 7,
    state: st,
    dk: () => ({ key: "K" }),
    cardsOf: (id) => st.cards.filter((c) => c.listId === id).sort(byRank),
    listsInGroup: (gid) => st.lists.filter((l) => l.groupId === gid).sort(byRank),
    groupById: (gid) => st.groups.find((g) => g.id === gid),
    assignmentsOf: (cardId, sid) => st.cardLabels.filter((a) => a.cardId === cardId && (sid === undefined || a.sessionId === sid)),
    playingsOf: (cardId) => st.cardSessions.filter((p) => p.cardId === cardId).map((p) => p.sessionId),
    sessionMeta: (id) => { const s = st.sessions.find((x) => x.id === id); return s ? JSON.parse(s.meta) : null; },
    sessionName: (id) => { const s = st.sessions.find((x) => x.id === id); return s ? JSON.parse(s.meta).title || "тест" : "тест"; },
    verbs: { create: verb("create"), post: verb("post"), patch: verb("patch"), put: verb("put"), del: verb("del") },
    renders: 0,
    render() { board.renders++; },
    statuses: [],
    setStatus(op) { board.statuses.push(op); },
    reloads: 0,
    async reload() { board.reloads++; },
    writes,
  };
  return board;
}
