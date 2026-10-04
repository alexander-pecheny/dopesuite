// clipfade.ts — the one-line clip every app uses instead of an ellipsis. An
// element with .u-clip-fade stays on one line and is cut at its edge; when its
// text really does not fit, this marks it .is-clipped and core.css fades the
// last few letters out. Text that fits is never faded, whether its box is the
// width of its column or the width of its own text (a crumb, a badge).
//
// It runs from menu.js, which every page of every app loads, so a page only has
// to put the class on the element. Names a person may need to read whole (a
// team, a player) still get the page's fade + popover; this is for chrome text.

const CLASS = "u-clip-fade";
const CLIPPED = "is-clipped";

let frame = 0;

function measure(): void {
  frame = 0;
  const nodes = document.getElementsByClassName(CLASS);
  // Read every width before writing any class, so one pass lays out once.
  const clipped: boolean[] = [];
  for (let i = 0; i < nodes.length; i++) {
    const el = nodes[i];
    clipped.push(el.scrollWidth > el.clientWidth + 1);
  }
  for (let i = 0; i < nodes.length; i++) nodes[i].classList.toggle(CLIPPED, clipped[i]);
}

function schedule(): void {
  if (!frame) frame = requestAnimationFrame(measure);
}

export function bindClipFade(): void {
  if (!document.body) return;
  // Text can change without the box changing size (a full-width title is
  // renamed), so a DOM change re-measures as well as a resize. Our own class
  // writes are attribute changes, which this does not watch.
  new MutationObserver(schedule).observe(document.body, {childList: true, subtree: true, characterData: true});
  window.addEventListener("resize", schedule);
  document.fonts?.ready.then(schedule);
  schedule();
}
