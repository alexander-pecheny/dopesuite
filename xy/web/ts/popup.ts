// popup.ts — one owner for a transient popup mounted on <body>.
//
// A popup opened from inside a scrolling container (the list ⋯ menu in the
// kanban, the colour palette in a modal) is clipped if positioned there, so it
// floats on <body> at a fixed spot. These stay OFF the overlay stack: they claim
// Escape in the CAPTURE phase, closing before whatever they were opened from.

export interface AnchoredPopup {
  close(): void;
}

interface PopupDeps {
  // Also counts as "inside", so a second click on the trigger toggles.
  anchor?: HTMLElement;
  onClose?(): void;
}

export function place(node: HTMLElement, anchor: HTMLElement): void {
  const r = anchor.getBoundingClientRect();
  const pad = 8;
  const left = Math.max(pad, Math.min(r.right - node.offsetWidth, window.innerWidth - node.offsetWidth - pad));
  let top = r.bottom + 4;
  if (top + node.offsetHeight > window.innerHeight - pad) top = Math.max(pad, r.top - node.offsetHeight - 4);
  node.style.left = left + "px";
  node.style.top = top + "px";
}

export function anchorPopup(node: HTMLElement, anchor: HTMLElement, deps: PopupDeps = {}): AnchoredPopup {
  document.body.append(node);
  place(node, anchor);

  function close(): void {
    node.remove();
    document.removeEventListener("pointerdown", onOutside, true);
    document.removeEventListener("keydown", onKey, true);
    window.removeEventListener("scroll", close, true);
    window.removeEventListener("resize", close);
    if (deps.onClose) deps.onClose();
  }
  function inside(t: EventTarget | null): boolean {
    return t instanceof Node && (node.contains(t) || !!(deps.anchor && deps.anchor.contains(t)));
  }
  function onOutside(e: PointerEvent): void { if (!inside(e.target)) close(); }
  function onKey(e: KeyboardEvent): void {
    if (e.key !== "Escape") return;
    e.stopImmediatePropagation();
    close();
  }

  document.addEventListener("pointerdown", onOutside, true);
  document.addEventListener("keydown", onKey, true);
  window.addEventListener("scroll", close, true);
  window.addEventListener("resize", close);
  return { close };
}
