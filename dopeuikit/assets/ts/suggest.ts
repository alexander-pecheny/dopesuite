// suggest.ts — the one filtered dropdown for free-text fields: currencies,
// testers, towns, timezones. Wherever a native <select> cannot go because the
// list is too long to scroll or has to be searched, this is what goes instead.
//
// It is in the kit because the reason is not one app's. A native picker on a
// phone cannot be typed into: a field of 160 ISO 4217 codes means spinning a
// wheel past 160 rows to reach GEL, and on a desktop the same field types fine,
// which is why nobody notices. xy loads this module on its own (see
// xy/web/ts/kit/suggest.d.ts); dope and spliff bundle it in.

export interface Choice {
  value: string;
  label: string;
  hint?: string;
  // The label is an IDENTIFIER rather than a name — a currency code, where the
  // hint beside it is what the code means. Set, the label is drawn bold, so the
  // eye runs down the codes and not down the sentence after them.
  strong?: boolean;
}

export interface Suggest {
  /** Redraw the list for whatever is typed now. */
  refresh(): void;
  /** Take the popover down. */
  close(): void;
}

// How far the popover keeps off the edges of the screen when it has to be
// nudged back inside them.
const PAD = 8;

export function autocomplete(
  inp: HTMLInputElement,
  choices: (q: string) => Choice[],
  onPick?: (c: Choice) => void,
): Suggest {
  let pop: HTMLElement | null = null;
  let rows: HTMLElement[] = [];
  let shown: Choice[] = [];
  let active = -1;

  const dismiss = (): void => {
    if (!pop) return;
    pop.remove();
    pop = null;
    rows = [];
    shown = [];
    active = -1;
    window.removeEventListener("scroll", place, true);
    window.removeEventListener("resize", place);
  };

  // The popover is position:fixed, so its coordinates are the viewport's and
  // nothing clips it — the field is routinely inside a scrolling frame or a
  // card, and an absolutely positioned popup is cut off by the first of those
  // it meets. Being fixed, it has to be kept in place by hand while the page
  // scrolls, and nudged back inside the screen when it would hang off it.
  function place(): void {
    if (!pop) return;
    const box = inp.getBoundingClientRect();
    pop.style.minWidth = `${Math.round(box.width)}px`;
    const width = pop.offsetWidth;
    const left = Math.max(PAD, Math.min(box.left, window.innerWidth - width - PAD));
    let top = box.bottom;
    // No room below is the phone's normal case, not an edge one: the field is
    // often in the bottom half of the screen with the keyboard under it.
    if (top + pop.offsetHeight > window.innerHeight - PAD) {
      top = Math.max(PAD, box.top - pop.offsetHeight);
    }
    pop.style.left = `${Math.round(left)}px`;
    pop.style.top = `${Math.round(top)}px`;
  }

  // The highlighted row wraps around the ends of the list, and -1 is "none of
  // them" — the state the popover opens in, so that Enter still submits the
  // form until somebody has actually arrowed onto a choice.
  function highlight(next: number): void {
    if (rows[active]) rows[active].removeAttribute("aria-selected");
    active = rows.length === 0 ? -1 : (next + rows.length) % rows.length;
    if (!rows[active]) return;
    rows[active].setAttribute("aria-selected", "true");
    rows[active].scrollIntoView({ block: "nearest" });
  }

  // A row is taken on pointerdown, which is before the click that would follow
  // it — and by then the row is gone, so that click lands on whatever the
  // popover was covering. Under the currency field that is the next input;
  // under some other field it could be a button. So the pick swallows the one
  // click it caused. The window is short enough that a second, deliberate click
  // cannot fall inside it.
  function swallowGhostClick(): void {
    const stop = (event: Event): void => {
      event.preventDefault();
      event.stopPropagation();
      done();
    };
    const done = (): void => {
      clearTimeout(timer);
      document.removeEventListener("click", stop, true);
    };
    const timer = setTimeout(done, 400);
    document.addEventListener("click", stop, true);
  }

  function take(choice: Choice): void {
    inp.value = choice.value;
    inp.dispatchEvent(new Event("input", { bubbles: true }));
    dismiss();
    swallowGhostClick();
    if (onPick) onPick(choice);
  }

  function draw(): void {
    dismiss();
    const hits = choices(inp.value);
    if (!hits.length) return;
    pop = document.createElement("div");
    pop.className = "menu-dropdown suggest-pop";
    for (const hit of hits) {
      const row = document.createElement("button");
      row.className = "menu-item";
      row.type = "button";
      const label = document.createElement(hit.strong ? "strong" : "span");
      label.textContent = hit.label;
      const hint = document.createElement("span");
      if (hit.hint) {
        hint.className = "suggest-hint";
        hint.textContent = hit.hint;
      }
      row.append(label, hint);
      // pointerdown, not click: a click arrives after the field has lost focus,
      // and the blur below would have taken the popover down first. Preventing
      // the default keeps the focus where it is, on mouse and on a finger alike
      // — the tap-to-pick that the old mousedown handler only got right on a
      // mouse.
      row.addEventListener("pointerdown", (event) => {
        event.preventDefault();
        take(hit);
      });
      rows.push(row);
      shown.push(hit);
      pop.append(row);
    }
    // The popup is a child of the field's own anchor, so it paints in whatever
    // stacking context the field is in — inside a modal it is above the modal,
    // not behind it. The anchor is marked here rather than at bind time,
    // because a field is often wired before it is appended to anything.
    const host = inp.parentElement;
    if (!host) {
      pop = null;
      rows = [];
      shown = [];
      return;
    }
    host.classList.add("suggest-anchor");
    host.append(pop);
    place();
    window.addEventListener("scroll", place, true);
    window.addEventListener("resize", place);
  }

  inp.addEventListener("input", draw);
  inp.addEventListener("focus", draw);
  inp.addEventListener("blur", () => setTimeout(dismiss, 150));
  inp.addEventListener("keydown", (event: KeyboardEvent) => {
    if (event.key === "Escape") {
      if (pop) event.stopPropagation();
      dismiss();
      return;
    }
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      if (!pop) draw();
      if (!pop) return;
      event.preventDefault();
      const down = event.key === "ArrowDown";
      if (active < 0) highlight(down ? 0 : rows.length - 1);
      else highlight(active + (down ? 1 : -1));
      return;
    }
    if (event.key === "Enter" && pop && shown[active]) {
      // Only when a row is picked out: Enter on a field nobody has arrowed
      // through still submits the form.
      event.preventDefault();
      take(shown[active]);
    }
  });

  return { refresh: draw, close: dismiss };
}
