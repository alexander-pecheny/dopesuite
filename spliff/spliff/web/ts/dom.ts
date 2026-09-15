// The handful of DOM helpers every Spliff page is built from. There is no
// hand-written HTML anywhere in the app: a page's shell is a .dopeui source the
// server compiles, and everything below it is built here, element by element,
// out of the design system's own class names.

export function byId<T extends HTMLElement>(id: string): T {
  const node = document.getElementById(id);
  if (!node) throw new Error(`page is missing #${id}`);
  return node as T;
}

export function maybe<T extends HTMLElement>(id: string): T | null {
  return document.getElementById(id) as T | null;
}

export function el<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  className?: string,
  text?: string,
): HTMLElementTagNameMap[K] {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

export function clear(node: HTMLElement): void {
  node.replaceChildren();
}

export function show(node: HTMLElement | null, visible: boolean): void {
  if (node) node.hidden = !visible;
}

export function setText(node: HTMLElement | null, text: string): void {
  if (node) node.textContent = text;
}

// amountNode is the one place an amount becomes an element: lining figures, and
// a colour that says which way it points. Zero is neither owed nor owing, and
// says so by being neither colour.
export function amountNode(text: string, minor: number): HTMLElement {
  let cls = "amount amount-zero";
  if (minor > 0) cls = "amount amount-positive";
  else if (minor < 0) cls = "amount amount-negative";
  return el("span", cls, text);
}

export function tag(text: string, dead = false): HTMLElement {
  return el("span", dead ? "tag tag-dead" : "tag", text);
}
