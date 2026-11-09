// dom.ts — the DOM builder the question renderer (preview.ts) draws with, and
// the apps that show its questions with it.

export type ElChild = Node | string | number | boolean | null | undefined | ElChild[];
export type ElProps = Record<string, unknown>;

// el(tag, props, ...children) — minimal DOM builder. props.class, props.text,
// event handlers as on*, everything else set as attribute/property.
export function el(tag: string, props: ElProps = {}, ...children: ElChild[]): HTMLElement {
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
