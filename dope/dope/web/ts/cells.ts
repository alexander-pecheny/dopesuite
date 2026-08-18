// Cell and text primitives every dope table is built from: th/td with the
// CellSpec grammar, display formatting (U+2212 minus), and the small DOM
// helpers pages share.

const MINUS_SIGN = "\u2212";

export type CellContentItem = Node | string | number | boolean | null | undefined;
export type CellContent = CellContentItem | CellContentItem[];

export interface CellAttrs {
  dataset?: Record<string, string | number | boolean>;
  [key: string]: unknown;
}

export interface CellSpecObject {
  node?: Node;
  content?: CellContent;
  className?: string;
  attrs?: CellAttrs | null;
  dataset?: Record<string, string | number | boolean>;
}

export type CellSpec = CellContent | CellSpecObject;

interface CellDefaults {
  className?: string;
  attrs?: CellAttrs | null;
}

export function th(content: CellContent, className = "", attrs: CellAttrs = {}): HTMLElement {
  return cell("th", content, className, attrs);
}

export function td(content: CellContent, className = "", attrs: CellAttrs = {}): HTMLElement {
  return cell("td", content, className, attrs);
}

function cell(tagName: string, content: CellContent, className = "", attrs: CellAttrs | null = {}): HTMLElement {
  const node = document.createElement(tagName);
  if (className) node.className = className;
  setContent(node, content);
  applyAttrs(node, attrs);
  return node;
}

export function cellFromSpec(tagName: string, spec: CellSpec, defaults: CellDefaults = {}): Node {
  if (spec instanceof Node) return spec;
  if (typeof spec === "object" && spec !== null && !Array.isArray(spec) && spec.node instanceof Node) return spec.node;
  if (spec === undefined || spec === null || typeof spec !== "object" || Array.isArray(spec)) {
    return cell(tagName, spec ?? "", defaults.className || "", defaults.attrs || {});
  }
  const node = cell(
    tagName,
    Object.prototype.hasOwnProperty.call(spec, "content") ? spec.content : "",
    spec.className ?? defaults.className ?? "",
    spec.attrs || defaults.attrs || {},
  );
  if (spec.dataset) applyDataset(node, spec.dataset);
  return node;
}

function setContent(node: HTMLElement, content: CellContent): void {
  if (content instanceof Node) {
    node.appendChild(content);
    return;
  }
  if (Array.isArray(content)) {
    for (const item of content) {
      if (item instanceof Node) node.appendChild(item);
      else node.appendChild(document.createTextNode(formatDisplayText(item)));
    }
    return;
  }
  node.textContent = formatDisplayText(content);
}

export function formatDisplayText(value: unknown): string {
  return value == null ? "" : String(value).replace(/^-/, MINUS_SIGN);
}

export function applyAttrs(node: HTMLElement, attrs: CellAttrs | null = {}): void {
  if (!attrs) return;
  const {dataset, ...rest} = attrs;
  Object.assign(node, rest);
  if (dataset) applyDataset(node, dataset);
}

function applyDataset(node: HTMLElement, dataset: Record<string, string | number | boolean> = {}): void {
  for (const [key, value] of Object.entries(dataset)) {
    node.dataset[key] = String(value);
  }
}

export function option(value: string | number, label: unknown): HTMLOptionElement {
  const node = document.createElement("option");
  node.value = String(value);
  node.textContent = formatDisplayText(label);
  return node;
}

export function setText(root: ParentNode, selector: string, value: unknown, formatter: (value: unknown) => string = formatDisplayText): void {
  const node = root.querySelector(selector);
  if (node) node.textContent = formatter(value);
}

export function isFormControl(target: unknown): boolean {
  return target instanceof HTMLInputElement ||
    target instanceof HTMLSelectElement ||
    target instanceof HTMLTextAreaElement ||
    target instanceof HTMLButtonElement;
}

export function sameArray(a: unknown, b: unknown): boolean {
  if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    if (a[i] !== b[i]) return false;
  }
  return true;
}

export function cssEscape(value: string): string {
  // Cast because lib.dom types CSS.escape as always present; the runtime check
  // guards older browsers where it isn't.
  const escape = (window.CSS as {escape?: (value: string) => string} | undefined)?.escape;
  return escape ? CSS.escape(value) : String(value).replace(/["\\]/g, "\\$&");
}

export function formatNumber(value: unknown): string {
  return Number.isFinite(Number(value)) ? formatDisplayText(value) : "";
}

export function formatPlace(place: number | null | undefined): string {
  return place != null && place > 0 ? String(place) : "";
}

// nameNode is a link to `href` (an external rating page) when one is given,
// otherwise a plain span — both carrying `className` so styling is the same.
export function nameNode(text: string, href: string, className: string): HTMLElement {
  if (href) {
    const a = document.createElement("a");
    a.className = `${className} quiet-link`;
    a.href = href;
    a.target = "_blank";
    a.rel = "noopener noreferrer";
    a.textContent = text;
    return a;
  }
  const span = document.createElement("span");
  span.className = className;
  span.textContent = text;
  return span;
}
