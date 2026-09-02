// The one filtered dropdown for free-text fields: testers, towns, timezones,
// rating.chgk.info players — wherever a native <select> cannot go because the
// list is too long to scroll or has to be searched. Shared by both apps so a
// field cannot silently ship without one, which is how xy's /profile and its
// first-run modal ended up with a bare timezone box while the session form had
// a working picker.

export interface Choice {
  value: string;
  label: string;
  hint?: string;
}

// A source answers what to offer for what has been typed. It may answer later
// — a server-backed one does — and only the newest answer is ever drawn.
export type ChoiceSource = (query: string) => Choice[] | Promise<Choice[]>;

export interface AutocompleteOptions {
  // How long a keystroke waits before the source is asked. Opening the field
  // asks at once: that is a request to see the list, not a search.
  debounceMs?: number;
}

const DEBOUNCE_MS = 150;

export function autocomplete(
  input: HTMLInputElement,
  choices: ChoiceSource,
  onPick?: (choice: Choice) => void,
  options: AutocompleteOptions = {},
): void {
  let pop: HTMLElement | null = null;
  let items: HTMLElement[] = [];
  let active = -1;
  let seq = 0;
  let timer = 0;

  const dismiss = (): void => {
    if (pop) pop.remove();
    pop = null;
    items = [];
    active = -1;
  };

  const highlight = (next: number): void => {
    if (!items.length) return;
    active = (next + items.length) % items.length;
    items.forEach((item, i) => item.classList.toggle("is-active", i === active));
    items[active].scrollIntoView({block: "nearest"});
  };

  const pick = (choice: Choice): void => {
    input.value = choice.value;
    input.dispatchEvent(new Event("input", {bubbles: true}));
    dismiss();
    onPick?.(choice);
  };

  const draw = (hits: Choice[]): void => {
    dismiss();
    if (!hits.length) return;
    pop = document.createElement("div");
    pop.className = "menu-dropdown suggest-pop";
    for (const hit of hits) {
      const item = document.createElement("button");
      item.type = "button";
      item.className = "menu-item";
      const label = document.createElement("span");
      label.textContent = hit.label;
      const hint = document.createElement("span");
      if (hit.hint) hint.className = "suggest-hint";
      hint.textContent = hit.hint || "";
      item.append(label, hint);
      // mousedown, not click: the field's own blur would tear the popup down
      // before a click ever landed.
      item.addEventListener("mousedown", (event) => {
        event.preventDefault();
        pick(hit);
      });
      items.push(item);
      pop.append(item);
    }
    // The popup is absolutely positioned, so its parent must be the positioning
    // context — marked here rather than at bind time, because a field is often
    // wired before it is appended to anything.
    const host = input.parentElement;
    if (!host) {
      dismiss();
      return;
    }
    host.classList.add("suggest-anchor");
    host.append(pop);
    // top:100% would mean the bottom of the ANCHOR, which is only the field on
    // a form that wraps each one. A form that puts every input in one column
    // would drop the popup under the whole form, so measure the input's box.
    const hostBox = host.getBoundingClientRect();
    const inputBox = input.getBoundingClientRect();
    pop.style.top = `${Math.round(inputBox.bottom - hostBox.top)}px`;
    pop.style.minWidth = `${Math.round(inputBox.width)}px`;
    // A row is as wide as the name plus what tells it from a namesake, which
    // on a phone is wider than the field: slide the popup back inside.
    const margin = 8;
    const width = pop.getBoundingClientRect().width;
    const room = Math.max(margin, window.innerWidth - margin - width);
    pop.style.left = `${Math.round(Math.min(inputBox.left, room) - hostBox.left)}px`;
  };

  const ask = (): void => {
    const mine = ++seq;
    const answer = choices(input.value);
    if (Array.isArray(answer)) {
      draw(answer);
      return;
    }
    void answer.then((hits) => {
      // A later keystroke has already asked: its answer is the one to draw.
      if (mine === seq) draw(hits);
    }).catch(() => {
      if (mine === seq) dismiss();
    });
  };

  input.addEventListener("input", () => {
    window.clearTimeout(timer);
    timer = window.setTimeout(ask, options.debounceMs ?? DEBOUNCE_MS);
  });
  input.addEventListener("focus", ask);
  input.addEventListener("blur", () => setTimeout(dismiss, 150));
  input.addEventListener("keydown", (event) => {
    if (event.key === "Escape") {
      dismiss();
      return;
    }
    if (!items.length) return;
    if (event.key === "ArrowDown") {
      event.preventDefault();
      highlight(active + 1);
    } else if (event.key === "ArrowUp") {
      event.preventDefault();
      highlight(active - 1);
    } else if (event.key === "Enter" && active >= 0) {
      event.preventDefault();
      items[active].dispatchEvent(new MouseEvent("mousedown"));
    }
  });
}
