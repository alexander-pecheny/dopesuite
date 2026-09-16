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

// The kit's amount primitive: lining figures, and a tone that says which way the
// number points. A balance gets one — zero is neither owed nor owing and says so
// by being neither colour; a total or a transfer is a plain figure and gets none.
export function amountNode(text: string, minor: number): HTMLElement {
  let tone = "amount-zero";
  if (minor > 0) tone = "amount-positive";
  else if (minor < 0) tone = "amount-negative";
  return el("span", `amount ${tone}`, text);
}

export function amountPlain(text: string): HTMLElement {
  return el("span", "amount", text);
}

export type BadgeTone = "neutral" | "emphasis" | "positive" | "negative";

// The kit's badge: one upright word saying what a row IS. Neutral is the bare
// chip, so it carries no tone class.
export function badge(text: string, tone: BadgeTone = "neutral"): HTMLElement {
  return el("span", tone === "neutral" ? "badge" : `badge badge-${tone}`, text);
}

// stamp turns a stored RFC3339 time into what a person reads: the date and the
// hour, in their own timezone. The stored value is UTC to the second because
// that is what an audit trail needs; nobody reads an audit trail in UTC.
export function stamp(iso: string): string {
  const at = new Date(iso);
  if (Number.isNaN(at.getTime())) return iso;
  const pad = (n: number): string => String(n).padStart(2, "0");
  return `${at.getFullYear()}-${pad(at.getMonth() + 1)}-${pad(at.getDate())} ` +
    `${pad(at.getHours())}:${pad(at.getMinutes())}`;
}

// .list-row spreads its children apart, which is right for "a thing on the left
// and its amount on the right" and wrong for the four-or-five pieces a feed row
// actually has. group() is the left-hand cluster: one child of the row, wrapping
// inside itself, so a long description takes a second line on a phone instead of
// squeezing everything else off it.
export function group(grow = false): HTMLElement {
  return el("span", "u-row u-wrap u-gap-sm u-align-center" + (grow ? " u-grow" : ""));
}
