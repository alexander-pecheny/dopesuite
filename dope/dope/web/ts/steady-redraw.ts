// steady-redraw.ts — a page redraw that does not move what the host is
// looking at.
//
// A wall of bouts sizes the ones off screen by a guess (content-visibility:
// auto with a contain-intrinsic-size), so the browser lays out only what is in
// view. The guess is right for a bout only once the browser has drawn it and
// remembered its size; a redraw that builds every bout anew throws those
// memories away. Each bout above the view then falls back to the guess, the
// sum of the errors moves the bout under the cursor, and the sheet jumps — the
// further down the page, the further it jumps.
//
// redrawSteady keeps both halves: it hands each new bout the size its old self
// had, by id, and it scrolls the frame so the first bout in view stays where it
// was.

interface Size {
  w: number;
  h: number;
}

const sizes = new WeakMap<HTMLElement, Map<string, Size>>();

// The frame that scrolls the sheet: the nearest ancestor that scrolls, else
// the page.
function scrollerOf(node: HTMLElement): HTMLElement {
  for (let at = node.parentElement; at; at = at.parentElement) {
    const overflow = getComputedStyle(at).overflowY;
    if ((overflow === "auto" || overflow === "scroll") && at.scrollHeight > at.clientHeight) return at;
  }
  return (document.scrollingElement || document.documentElement) as HTMLElement;
}

function frameTop(scroller: HTMLElement): number {
  return scroller === document.scrollingElement || scroller === document.documentElement
    ? 0
    : scroller.getBoundingClientRect().top;
}

// redrawSteady runs swap, which replaces root's content, keeping the sizes of
// the items matching selector by their ids. keepView also scrolls the frame so
// the first item in view does not move; a redraw of the same view wants it,
// the first drawing of another tab does not.
export function redrawSteady(root: HTMLElement, selector: string, swap: () => void, keepView = true): void {
  let known = sizes.get(root);
  if (!known) {
    known = new Map();
    sizes.set(root, known);
  }
  const scroller = scrollerOf(root);
  const top = frameTop(scroller);
  let anchor: {id: string; offset: number} | null = null;
  for (const node of root.querySelectorAll<HTMLElement>(selector)) {
    if (!node.id) continue;
    const rect = node.getBoundingClientRect();
    if (rect.height > 0) known.set(node.id, {w: rect.width, h: rect.height});
    if (!anchor && rect.bottom > top) anchor = {id: node.id, offset: rect.top - top};
  }

  swap();

  for (const node of root.querySelectorAll<HTMLElement>(selector)) {
    const size = node.id ? known.get(node.id) : undefined;
    if (size) node.style.containIntrinsicSize = `auto ${Math.round(size.w)}px auto ${Math.round(size.h)}px`;
  }
  if (!keepView || !anchor) return;
  const same = document.getElementById(anchor.id);
  if (!same || !root.contains(same)) return;
  const drift = same.getBoundingClientRect().top - frameTop(scroller) - anchor.offset;
  if (Math.abs(drift) >= 1) scroller.scrollTop += drift;
}
