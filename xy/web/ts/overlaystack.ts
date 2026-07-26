// overlaystack.ts — one owner for every overlay's dismissal.
//
// Android's PWA has no Escape key, so the back button is the only way out of a
// modal — and xy pushed no history, so back exited the whole app instead of
// closing the card. Every overlay now registers here on open: the stack pushes a
// history entry, and back, Escape and the ✕/↩️ buttons all funnel through the
// same popstate, so the three gestures cannot drift apart.
//
// The stack does NOT show or hide anything. Each overlay keeps its own open
// function and hands its existing close function in as `close`; this module only
// decides WHEN that runs. An overlay whose close re-opens another (the card
// returning to the list preview it was opened from) just pushes again.

export interface OverlayEntry {
  // The overlay element, so callers can ask whether it is the live one.
  el: HTMLElement;
  // The overlay's own teardown — exactly what its ✕ button used to do.
  close(): void | Promise<void>;
  // Optional gate: return false to keep the overlay open (the card's unsaved
  // -changes prompt). Asked once per dismissal, whichever gesture triggered it.
  confirm?(): Promise<boolean>;
}

interface StackDeps {
  history: Pick<History, "pushState" | "replaceState" | "back">;
  location: Pick<Location, "href">;
  addPopListener(fn: () => void): void;
  addKeyListener(fn: (e: KeyboardEvent) => void): void;
}

// A pushed entry remembers the URL it was opened at and a one-shot flag: once
// the user has answered `confirm`, the second pass must not ask again.
interface Frame extends OverlayEntry {
  confirmed: boolean;
  url: string;
}

export function createOverlayStack(deps: StackDeps) {
  const frames: Frame[] = [];
  // Guards the window between asking `confirm` and acting on the answer, so a
  // second back press mid-prompt cannot pop two overlays.
  let asking = false;

  function top(): Frame | undefined { return frames[frames.length - 1]; }

  // open registers an overlay as the new topmost and gives it a history entry.
  // `url` is for overlays that are addressable (the card's ?card=); the rest
  // push the URL unchanged, so back still has something to consume.
  function open(entry: OverlayEntry, url?: string): void {
    const at = url || deps.location.href;
    frames.push({ ...entry, confirmed: false, url: at });
    deps.history.pushState({ xyOverlay: frames.length }, "", at);
  }

  // pop is the dismissal every gesture routes through: it asks the browser to go
  // back, and the popstate below does the actual teardown.
  function pop(): void {
    if (!frames.length || asking) return;
    deps.history.back();
  }

  // replace swaps the topmost overlay for another at the same depth, for a
  // hand-off rather than a dismissal: the list preview opening a card is one
  // step forward, not one back. Closing and re-opening would race — the push
  // would land before the pop's popstate did — so history is only rewritten.
  function replace(el: HTMLElement, entry: OverlayEntry, url?: string): void {
    if (!isTop(el)) { open(entry, url); return; }
    const at = url || deps.location.href;
    frames[frames.length - 1] = { ...entry, confirmed: false, url: at };
    deps.history.replaceState({ xyOverlay: frames.length }, "", at);
  }

  function isTop(el: HTMLElement): boolean { return top()?.el === el; }
  function depth(): number { return frames.length; }

  async function onPop(): Promise<void> {
    const frame = top();
    if (!frame) return; // nothing of ours is open — the navigation was the user's
    if (frame.confirm && !frame.confirmed) {
      // The entry we were going to consume is already gone, so put one back
      // while we ask: declining must leave the overlay exactly as it was —
      // including its address, which the browser has already reverted.
      deps.history.pushState({ xyOverlay: frames.length }, "", frame.url);
      asking = true;
      let ok = false;
      try {
        ok = await frame.confirm();
      } finally {
        asking = false;
      }
      if (!ok) return;
      frame.confirmed = true;
      deps.history.back(); // round two, this time without the question
      return;
    }
    frames.pop();
    await frame.close();
  }

  deps.addPopListener(() => { void onPop(); });
  // Escape is the desktop spelling of the same gesture. Registering it once
  // here replaced a per-overlay keydown listener in every module — and with it
  // the card's guard that hard-coded four other overlays' hidden state.
  deps.addKeyListener((e) => {
    if (e.key !== "Escape" || !frames.length) return;
    e.stopPropagation();
    pop();
  });

  return { open, pop, replace, isTop, depth };
}

export type OverlayStack = ReturnType<typeof createOverlayStack>;

// The page's stack. Overlays live in five modules; threading one instance
// through all of them buys nothing, since there is only ever one history.
// Built on first use rather than at import, so the tests can import this module
// (and the factory above) without a DOM.
let instance: OverlayStack | null = null;
function live(): OverlayStack {
  if (!instance) {
    instance = createOverlayStack({
      history,
      location,
      addPopListener: (fn) => addEventListener("popstate", fn),
      addKeyListener: (fn) => document.addEventListener("keydown", fn),
    });
  }
  return instance;
}

export const overlayStack: OverlayStack = {
  open: (entry, url) => live().open(entry, url),
  pop: () => live().pop(),
  replace: (el, entry, url) => live().replace(el, entry, url),
  isTop: (el) => live().isTop(el),
  depth: () => live().depth(),
};
