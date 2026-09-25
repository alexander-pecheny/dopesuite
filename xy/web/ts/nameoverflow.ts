// nameoverflow.ts — a one-line name that does not fit its box: dope's
// fade + popover, pared to one trigger per scope. The item whose name is
// clipped gets a flag class (the CSS fade hangs on it, no ellipsis), and
// hovering or focusing that item floats the full name in one shared node
// appended to <body> — position:fixed, so a scroller's clip and the tiles or
// columns beside it never crop it.
//
// Clipping is a layout question, so it can only be answered after the browser
// has laid the page out: measure() re-reads on the next frame, coalesced, and
// every caller that rebuilds its DOM calls it.

export interface NameOverflowOptions {
  // The container the items live in; its rebuilds are what measure() re-reads.
  root: HTMLElement;
  // The element that carries the flag and answers the hover — the card, the
  // list head. `name` is the one-line text inside it, or the item itself when
  // it matches (a heading that is its own name).
  item: string;
  name: string;
  truncatedClass: string;
}

export interface NameOverflow {
  measure(): void;
  hide(): void;
}

export function createNameOverflow({ root, item, name, truncatedClass }: NameOverflowOptions): NameOverflow {
  let tip: HTMLElement | null = null;
  let tipFor: Element | null = null;

  function nameOf(node: Element): Element | null {
    return node.matches(name) ? node : node.querySelector(name);
  }

  function show(node: HTMLElement): void {
    if (tipFor === node) return;
    const anchor = nameOf(node);
    const text = anchor?.textContent;
    if (!anchor || !text) return;
    tipFor = node;
    if (!tip) {
      tip = document.createElement("div");
      tip.className = "popover floating-name-popover";
      document.body.append(tip);
    }
    tip.textContent = text;
    tip.classList.add("visible");
    const r = anchor.getBoundingClientRect();
    const left = Math.max(8, Math.min(r.left, window.innerWidth - tip.offsetWidth - 8));
    let top = r.bottom + 2;
    if (top + tip.offsetHeight > window.innerHeight - 8) top = r.top - tip.offsetHeight - 2;
    tip.style.left = `${left}px`;
    tip.style.top = `${top}px`;
  }

  function hide(): void {
    if (tip) tip.classList.remove("visible");
    tipFor = null;
  }

  function itemOf(target: EventTarget | null): HTMLElement | null {
    return target instanceof Element ? target.closest<HTMLElement>(item) : null;
  }

  function measureNow(): void {
    // A rebuild strands the popover on a node that is no longer on the page.
    if (tipFor && !root.contains(tipFor)) hide();
    for (const node of root.querySelectorAll(item)) {
      const text = nameOf(node);
      node.classList.toggle(truncatedClass, !!text && text.scrollWidth > text.clientWidth + 1);
    }
  }

  let frame = 0;
  function measure(): void {
    if (frame) return;
    frame = requestAnimationFrame(() => { frame = 0; measureNow(); });
  }

  root.addEventListener("pointerover", (e) => {
    const node = itemOf(e.target);
    if (node && node.classList.contains(truncatedClass)) show(node);
  });
  root.addEventListener("pointerout", (e) => {
    const node = itemOf(e.target);
    if (node && !node.contains(e.relatedTarget instanceof Node ? e.relatedTarget : null)) hide();
  });
  root.addEventListener("focusin", (e) => {
    const node = itemOf(e.target);
    if (node && node.classList.contains(truncatedClass)) show(node);
  });
  root.addEventListener("focusout", hide);
  window.addEventListener("scroll", hide, true);
  window.addEventListener("resize", () => { hide(); measure(); });

  return { measure, hide };
}
