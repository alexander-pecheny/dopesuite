// dom.js — the jstest fake DOM: enough of an Element for modules that build
// with el(), toggle hidden, set text and listen for events. No layout, no
// selectors beyond ids. Every test that needs a page builds one with page().
export function fakeNode(tag = "div", props = {}) {
  const node = {
    tag,
    hidden: false,
    textContent: "",
    value: "",
    checked: false,
    disabled: false,
    title: "",
    className: "",
    dataset: {},
    attrs: {},
    kids: [],
    handlers: {},
    classes: new Set(),
    get classList() {
      const s = node.classes;
      return {
        add: (...c) => c.forEach((x) => s.add(x)),
        remove: (...c) => c.forEach((x) => s.delete(x)),
        toggle: (c, on) => { (on ?? !s.has(c)) ? s.add(c) : s.delete(c); return s.has(c); },
        contains: (c) => s.has(c),
      };
    },
    setAttribute(k, v) { node.attrs[k] = String(v); },
    getAttribute(k) { return k in node.attrs ? node.attrs[k] : null; },
    removeAttribute(k) { delete node.attrs[k]; },
    append(...n) { node.kids.push(...n.flat().filter((x) => x != null)); },
    appendChild(n) { node.kids.push(n); return n; },
    prepend(...n) { node.kids.unshift(...n); },
    replaceChildren(...n) { node.kids = n.flat().filter((x) => x != null); },
    remove() {},
    focus() { node.focused = (node.focused || 0) + 1; },
    select() {},
    click() { node.fire("click"); },
    closest() { return null; },
    querySelector() { return null; },
    querySelectorAll() { return []; },
    contains: (n) => n === node,
    addEventListener(type, fn) { (node.handlers[type] ||= []).push(fn); },
    removeEventListener(type, fn) { node.handlers[type] = (node.handlers[type] || []).filter((f) => f !== fn); },
    fire(type, ev = {}) {
      const e = { type, target: node, preventDefault() {}, stopPropagation() {}, ...ev };
      for (const fn of node.handlers[type] || []) fn(e);
      const on = node["on" + type];
      if (typeof on === "function") on(e);
      return e;
    },
    // Text of the subtree, for assertions on rendered output.
    get text() { return node.textContent || node.kids.map((k) => (typeof k === "string" ? k : k.text)).join(""); },
  };
  Object.assign(node, props);
  return node;
}

// page(ids) builds a document whose getElementById knows exactly these ids.
export function page(ids = []) {
  const byId = new Map();
  for (const id of ids) byId.set(id, fakeNode("div", { id }));
  const doc = {
    body: fakeNode("body"),
    documentElement: fakeNode("html"),
    title: "",
    activeElement: null,
    createElement: (tag) => fakeNode(tag),
    createElementNS: (_ns, tag) => fakeNode(tag),
    createTextNode: (text) => ({ tag: "#text", textContent: text, get text() { return text; } }),
    createDocumentFragment: () => fakeNode("#fragment"),
    getElementById: (id) => byId.get(id) || null,
    querySelector: () => null,
    querySelectorAll: () => [],
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
    add: (id) => { const n = fakeNode("div", { id }); byId.set(id, n); return n; },
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
