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

// makeRoomBelow lets the last bouts of a sheet reach the top too. A frame
// that has nothing left to scroll stops short with the bout halfway down the
// screen, under the ones before it, and on a phone the reader sees those first
// and thinks the link went wrong. So the frame gets what it lacked as an empty
// tail, which the next drawing of the tab drops. It returns where the node now
// stands.
function makeRoomBelow(node: HTMLElement): number {
  const frame = node.closest<HTMLElement>(".sheet-frame");
  const content = node.closest<HTMLElement>(".table-host");
  if (!frame || !content) return node.getBoundingClientRect().top;
  const short = node.getBoundingClientRect().top - frame.getBoundingClientRect().top - Number.parseFloat(getComputedStyle(node).scrollMarginTop || "0");
  if (short <= STEADY_SCROLL_SLACK_PX) return node.getBoundingClientRect().top;
  let tail = content.querySelector<HTMLElement>(":scope > .anchor-tail");
  if (!tail) {
    tail = document.createElement("div");
    tail.className = "anchor-tail";
    tail.setAttribute("aria-hidden", "true");
    content.appendChild(tail);
  }
  tail.style.height = `${(tail.offsetHeight || 0) + short}px`;
  node.scrollIntoView({block: "start"});
  return node.getBoundingClientRect().top;
}

// How long a jump keeps the bout in place while the ones above it lay out,
// and how far it may drift before it is put back.
const STEADY_SCROLL_MS = 2000;
const STEADY_SCROLL_SLACK_PX = 2;

// scrollIntoViewSteady scrolls to a bout or a group a link named, and keeps it
// there. The bouts above it are sized by the guess until they come near the
// view; the first scroll brings some of them near, they lay out at their real
// size, and the target slides away (a crosstable's link on a phone landed
// half a screen low). So the node is put back each frame it drifts, until the
// page settles or the reader takes over: a touch, a wheel or a key stops it.
export function scrollIntoViewSteady(node: HTMLElement): void {
  node.scrollIntoView({block: "start"});
  const wanted = makeRoomBelow(node);
  const started = performance.now();
  let stopped = false;
  const stop = () => {
    stopped = true;
    for (const type of ["pointerdown", "wheel", "keydown", "touchstart"]) window.removeEventListener(type, stop, true);
  };
  for (const type of ["pointerdown", "wheel", "keydown", "touchstart"]) window.addEventListener(type, stop, {capture: true, passive: true});
  const check = () => {
    if (stopped || !node.isConnected) return;
    if (Math.abs(node.getBoundingClientRect().top - wanted) > STEADY_SCROLL_SLACK_PX) node.scrollIntoView({block: "start"});
    if (performance.now() - started < STEADY_SCROLL_MS) requestAnimationFrame(check);
    else stop();
  };
  requestAnimationFrame(check);
}
