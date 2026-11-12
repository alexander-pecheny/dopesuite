// The order of a Round's bouts (CONTEXT.md, Bout slot): the host moves a bout to
// another bout's slot — its wave, its venue and its time — when a player is
// late and the next bout is ready, or a team must play in another wave. Each
// Round is a section, its waves rows of cards; dropping one card on another,
// or tapping the two in turn, swaps them. A bout keeps its name and its seats,
// so the bracket never notices. The server holds the rules (a bout that has
// begun stays, nobody plays twice in one wave) and has the last word.

import S from "./i18nstrings.js";

// SlotBout is one bout as the dialog shows it.
export interface SlotBout {
  code: string;
  label: string;
  wave: number;
  venue: string;
  time: string;
  seats: string[];
  // locked: the bout has begun or is finished, and keeps its slot.
  locked: boolean;
}

// SlotRound is one Round's bouts, in their order.
export interface SlotRound {
  title: string;
  bouts: SlotBout[];
}

// swapper sends one swap and answers "" when it went through, else the
// server's words.
export type Swapper = (code: string, withCode: string) => Promise<string>;

// How far a pointer travels before a press becomes a drag, in CSS pixels.
const DRAG_THRESHOLD = 6;

// openRoundOrderDialog opens the dialog over the given Rounds; moved runs
// after every swap that went through.
export function openRoundOrderDialog(title: string, rounds: SlotRound[], swap: Swapper, moved: () => void): void {
  const dialog = document.createElement("dialog");
  dialog.className = "modal-dialog round-order-dialog";
  const body = document.createElement("div");
  body.className = "u-col u-gap-md";
  const head = document.createElement("h2");
  head.textContent = title;
  const hint = document.createElement("p");
  hint.className = "hint";
  hint.textContent = S.roundOrder.hint();
  const error = document.createElement("p");
  error.className = "hint hint-danger";
  error.hidden = true;
  const sections = document.createElement("div");
  sections.className = "u-col u-gap-lg";

  let picked: {round: SlotRound; bout: SlotBout} | null = null;
  let busy = false;

  const draw = () => {
    sections.replaceChildren(...rounds.map((round) => roundSection(round)));
  };

  const trySwap = (round: SlotRound, a: SlotBout, b: SlotBout) => {
    picked = null;
    if (busy || a === b) {
      draw();
      return;
    }
    if (a.locked || b.locked) {
      fail(S.roundOrder.locked((a.locked ? a : b).label));
      draw();
      return;
    }
    busy = true;
    dialog.classList.add("is-busy");
    error.hidden = true;
    void swap(a.code, b.code).then((message) => {
      busy = false;
      dialog.classList.remove("is-busy");
      if (message) {
        fail(message);
      } else {
        swapSlots(round, a, b);
        moved();
      }
      draw();
    });
  };

  const fail = (message: string) => {
    error.textContent = message;
    error.hidden = false;
  };

  const roundSection = (round: SlotRound): HTMLElement => {
    const section = document.createElement("section");
    section.className = "u-col u-gap-sm";
    if (rounds.length > 1 && round.title) {
      const name = document.createElement("h3");
      name.className = "round-order-round";
      name.textContent = round.title;
      section.appendChild(name);
    }
    for (const [wave, bouts] of wavesOf(round.bouts)) {
      const row = document.createElement("div");
      row.className = "round-order-wave";
      const label = document.createElement("div");
      label.className = "round-order-wave-label";
      const times = [...new Set(bouts.map((bout) => bout.time).filter(Boolean))];
      label.textContent = [S.roundOrder.wave(String(wave)), ...times].join(" · ");
      const cards = document.createElement("div");
      cards.className = "round-order-cards";
      for (const bout of bouts) cards.appendChild(card(round, bout));
      row.append(label, cards);
      section.appendChild(row);
    }
    return section;
  };

  const card = (round: SlotRound, bout: SlotBout): HTMLElement => {
    const node = document.createElement("button");
    node.type = "button";
    node.className = "round-order-card";
    node.dataset.bout = bout.code;
    if (bout.locked) node.classList.add("is-locked");
    if (picked?.bout === bout) node.classList.add("is-picked");
    const top = document.createElement("span");
    top.className = "round-order-card-head";
    const name = document.createElement("strong");
    name.textContent = bout.label;
    top.appendChild(name);
    if (bout.venue) {
      const venue = document.createElement("span");
      venue.className = "round-order-card-venue";
      venue.textContent = bout.venue;
      top.appendChild(venue);
    }
    if (bout.locked) {
      const state = document.createElement("span");
      state.className = "round-order-card-venue";
      state.textContent = S.roundOrder.begun();
      top.appendChild(state);
    }
    node.appendChild(top);
    for (const seat of bout.seats) {
      const line = document.createElement("span");
      line.className = "round-order-card-seat";
      line.textContent = seat;
      node.appendChild(line);
    }
    bindDrag(node, sections, (target) => {
      const other = round.bouts.find((entry) => entry.code === target.dataset.bout);
      if (other) trySwap(round, bout, other);
    }, () => {
      // A tap: the first picks the bout, the second swaps it there.
      if (busy) return;
      if (picked && picked.round === round && picked.bout !== bout) {
        trySwap(round, picked.bout, bout);
        return;
      }
      picked = picked?.bout === bout || bout.locked ? null : {round, bout};
      draw();
    }, round.bouts.map((entry) => entry.code));
    return node;
  };

  const actions = document.createElement("div");
  actions.className = "modal-actions";
  const close = document.createElement("button");
  close.type = "button";
  close.className = "btn";
  close.textContent = S.roundOrder.done();
  close.addEventListener("click", () => dialog.close());
  actions.appendChild(close);

  draw();
  body.append(head, hint, sections, error, actions);
  dialog.appendChild(body);
  dialog.addEventListener("close", () => dialog.remove());
  document.body.appendChild(dialog);
  dialog.showModal();
  close.focus();
}

// wavesOf is a Round's bouts by wave, in the order they are listed.
function wavesOf(bouts: SlotBout[]): Array<[number, SlotBout[]]> {
  const waves = new Map<number, SlotBout[]>();
  for (const bout of bouts) waves.set(bout.wave, [...(waves.get(bout.wave) || []), bout]);
  return [...waves.entries()].sort((a, b) => a[0] - b[0]);
}

// swapSlots does on the dialog's copy what the server did: each bout takes the
// other's place, wave, venue and time.
export function swapSlots(round: SlotRound, a: SlotBout, b: SlotBout): void {
  const ia = round.bouts.indexOf(a);
  const ib = round.bouts.indexOf(b);
  if (ia < 0 || ib < 0) return;
  [a.wave, b.wave] = [b.wave, a.wave];
  [a.venue, b.venue] = [b.venue, a.venue];
  [a.time, b.time] = [b.time, a.time];
  round.bouts[ia] = b;
  round.bouts[ib] = a;
}

// bindDrag lets a card be dragged onto another card of its Round (codes) with
// a mouse or a finger, through pointer events, which a phone sends too: the
// card follows the pointer and the card under it lights up. A press that
// never travels is a tap, as is Enter on the focused card.
function bindDrag(node: HTMLElement, root: HTMLElement, dropped: (target: HTMLElement) => void, tapped: () => void, codes: string[]): void {
  let start: {x: number; y: number; id: number} | null = null;
  let dragging = false;
  let over: HTMLElement | null = null;
  const targetAt = (x: number, y: number): HTMLElement | null => {
    // The dragged card rides under the pointer: the target is the card below it.
    for (const element of document.elementsFromPoint(x, y)) {
      const hit = element.closest<HTMLElement>(".round-order-card");
      if (!hit || hit === node) continue;
      return root.contains(hit) && codes.includes(hit.dataset.bout || "") ? hit : null;
    }
    return null;
  };
  const end = () => {
    node.classList.remove("is-dragging");
    node.style.transform = "";
    over?.classList.remove("is-over");
    over = null;
    start = null;
    dragging = false;
  };
  node.addEventListener("pointerdown", (event) => {
    if (event.button !== 0) return;
    start = {x: event.clientX, y: event.clientY, id: event.pointerId};
  });
  node.addEventListener("pointermove", (event) => {
    if (!start || event.pointerId !== start.id) return;
    const dx = event.clientX - start.x;
    const dy = event.clientY - start.y;
    if (!dragging && Math.hypot(dx, dy) < DRAG_THRESHOLD) return;
    if (!dragging) {
      dragging = true;
      node.setPointerCapture(event.pointerId);
      node.classList.add("is-dragging");
    }
    event.preventDefault();
    node.style.transform = `translate(${dx}px, ${dy}px)`;
    const target = targetAt(event.clientX, event.clientY);
    if (target !== over) {
      over?.classList.remove("is-over");
      over = target;
      over?.classList.add("is-over");
    }
  });
  // A drag ends in a click on the card too; it is not a tap.
  let dragged = false;
  node.addEventListener("pointerup", (event) => {
    if (!start || event.pointerId !== start.id) return;
    dragged = dragging;
    const target = over;
    end();
    if (dragged && target) dropped(target);
  });
  node.addEventListener("pointercancel", end);
  // A tap, a click or Enter on the focused card.
  node.addEventListener("click", () => {
    if (dragged) {
      dragged = false;
      return;
    }
    tapped();
  });
}
