// The seat picker: who sits in this seat. Every bout sheet asks it the same way
// (EK's and Hamsa's theme seat, Troika's chair, brain's question), so one
// control answers it, with one look and one behaviour.
//
// One seat is a <select>, which the platform already draws well on every
// phone. More than one (Erudit-Sextet seats up to three on a theme) is a
// button that opens a panel of the team's roster with a tickbox each, because a
// multiple <select> is unusable on a touch screen. The panel is a single node
// parked on <body> and moved to whichever picker asked for it: a bout draws
// dozens of pickers and none of them needs its own copy of the roster.
//
// The picker knows players by an id the page chooses (a name where the document
// stores names, a player id where it stores those) and nothing about formats.
// Nobody seated is the empty list. The page turns its document's value into ids
// and back at its own edge.
//
//   .seat-picker            the wrap: a name cell (name-cell.ts) with a chevron
//     .seat-picker-control  the <select>, or the button with
//       .seat-picker-text   its one line
//     .popover-inline       the whole seating, never shown: the popover reads it

import {markNameControl} from "./name-cell.js";
import {clamp, floatingPopover} from "./widgets.js";
import S from "./i18nstrings.js";

// Space the panel keeps from the viewport edge, and its smallest height.
const PANEL_MARGIN_PX = 8;
const PANEL_MIN_HEIGHT_PX = 120;

export interface SeatOption {
  id: string;
  name: string;
}

export interface SeatPickerSpec {
  // Who may sit here, in the order the choices are listed.
  roster: readonly SeatOption[];
  // Who sits here now, by id; [] is nobody.
  seated: readonly string[];
  // How many may sit here at once; 1 when left out.
  cap?: number;
  disabled?: boolean;
  // What the seat is: the control's tooltip and its accessible name.
  title?: string;
  // The empty choice of a one-seat picker; an empty line when left out.
  nobody?: string;
  // The closed line of a picker that seats more than one, given the seated
  // players' names; they are joined with commas when left out.
  line?: (names: string[]) => string;
  // The data-* coordinates the page finds the control by (its sheet cursor,
  // host presence, an in-place patch).
  dataset?: Record<string, string>;
  // A choice the host made, with the whole seating after it.
  onChange(seated: string[]): void;
}

export interface SeatPicker {
  readonly element: HTMLElement;
  seated(): string[];
  // Shows a seating the page learned from elsewhere (a remote edit). A
  // <select> the host has open is left alone.
  update(seated: readonly string[]): void;
}

// The control carries this attribute, so a page's cursor kinds and the score
// table's patch find every picker by one selector.
export const SEAT_PICKER_SELECTOR = "[data-seat-picker]";

const pickers = new WeakMap<Element, SeatPicker>();

// seatPickerOf is the picker a control (or anything inside its wrap) belongs to.
export function seatPickerOf(node: Element | null | undefined): SeatPicker | null {
  const wrap = node?.closest(".seat-picker");
  return wrap ? pickers.get(wrap) || null : null;
}

export function seatPicker(spec: SeatPickerSpec): SeatPicker {
  const cap = Math.max(1, spec.cap || 1);
  const wrap = document.createElement("span");
  wrap.className = "seat-picker";
  const popover = document.createElement("span");
  popover.className = "popover popover-inline";
  let seated = clean(spec.seated, cap);

  const nameOf = (id: string): string => spec.roster.find((player) => player.id === id)?.name ?? id;
  const names = (): string[] => seated.map(nameOf);

  let control: HTMLSelectElement | HTMLButtonElement;
  let paint: () => void;
  if (cap > 1) {
    const button = document.createElement("button");
    button.type = "button";
    button.setAttribute("aria-haspopup", "true");
    button.setAttribute("aria-expanded", "false");
    const text = document.createElement("span");
    text.className = "seat-picker-text";
    button.appendChild(text);
    button.addEventListener("click", (event) => {
      event.preventDefault();
      togglePanel(button, picker, spec, cap, (next) => {
        seated = next;
        paint();
        spec.onChange([...seated]);
      });
    });
    control = button;
    // The popover lists the seated players whole, one per line; the pass
    // measures the button's own line.
    paint = () => {
      setText(text, spec.line ? spec.line(names()) : names().join(", "));
      setText(popover, names().join("\n"));
    };
  } else {
    const select = document.createElement("select");
    select.addEventListener("change", () => {
      seated = select.value ? [select.value] : [];
      spec.onChange([...seated]);
      // Drop the focus off the dropdown, so the sheet's keys move its cursor
      // again instead of the <select> cycling its options.
      select.blur();
    });
    control = select;
    // The popover's text is the chosen option's, which the name-cell pass
    // copies in as it measures.
    paint = () => fillSelect(select, spec, seated, nameOf);
  }
  control.className = "seat-picker-control";
  control.dataset.seatPicker = "";
  for (const [key, value] of Object.entries(spec.dataset || {})) control.dataset[key] = value;
  control.disabled = Boolean(spec.disabled);
  if (spec.title) {
    control.title = spec.title;
    control.setAttribute("aria-label", spec.title);
  }
  wrap.append(control, popover);
  markNameControl(wrap, control);

  const picker: SeatPicker = {
    element: wrap,
    seated: () => [...seated],
    update(next) {
      if (control instanceof HTMLSelectElement && document.activeElement === control) return;
      const ids = clean(next, cap);
      if (sameIds(ids, seated)) return;
      seated = ids;
      paint();
    },
  };
  paint();
  pickers.set(wrap, picker);
  return picker;
}

// fillSelect lists the nobody choice and the roster, plus the one seated id the
// roster lacks (a player since struck off it), whose id is all there is to show.
function fillSelect(select: HTMLSelectElement, spec: SeatPickerSpec, seated: string[], nameOf: (id: string) => string): void {
  const current = seated[0] || "";
  if (!select.options.length) {
    select.appendChild(option("", spec.nobody || ""));
    for (const player of spec.roster) select.appendChild(option(player.id, player.name));
  }
  if (current && !Array.from(select.options).some((opt) => opt.value === current)) {
    select.appendChild(option(current, nameOf(current)));
  }
  select.value = current;
}

function option(value: string, label: string): HTMLOptionElement {
  const node = document.createElement("option");
  node.value = value;
  node.textContent = label;
  return node;
}

function clean(ids: readonly string[], cap: number): string[] {
  return ids.map((id) => String(id ?? "").trim()).filter(Boolean).slice(0, cap);
}

function sameIds(a: readonly string[], b: readonly string[]): boolean {
  return a.length === b.length && a.every((id, i) => id === b[i]);
}

function setText(node: Element, text: string): void {
  // Writing the same text would be a mutation, and a mutation is a measuring pass.
  if (node.textContent !== text) node.textContent = text;
}

// --- the tickbox panel ------------------------------------------------------

let panelNode: HTMLElement | null = null;
let panelTrigger: HTMLElement | null = null;

function togglePanel(trigger: HTMLButtonElement, picker: SeatPicker, spec: SeatPickerSpec, cap: number,
  choose: (seated: string[]) => void): void {
  if (panelTrigger === trigger) {
    closePanel();
    return;
  }
  openPanel(trigger, picker, spec, cap, choose);
}

// openPanel lists the roster under the trigger, ticked from the picker's own
// seating, which the page keeps current through update().
function openPanel(trigger: HTMLButtonElement, picker: SeatPicker, spec: SeatPickerSpec, cap: number,
  choose: (seated: string[]) => void): void {
  closePanel();
  const panel = document.createElement("div");
  panel.className = "popover menu-dropdown seat-picker-panel";
  panel.setAttribute("role", "group");
  if (spec.title) panel.setAttribute("aria-label", spec.title);
  const seated = picker.seated();
  const boxes: HTMLInputElement[] = [];
  const refreshDisabled = (): void => {
    const picked = boxes.filter((box) => box.checked).length;
    for (const box of boxes) box.disabled = !box.checked && picked >= cap;
  };
  for (const member of spec.roster) {
    const row = document.createElement("label");
    row.className = "menu-item seat-picker-option";
    const box = document.createElement("input");
    box.type = "checkbox";
    box.value = member.id;
    box.checked = seated.includes(member.id);
    box.addEventListener("change", () => {
      refreshDisabled();
      choose(boxes.filter((item) => item.checked).map((item) => item.value));
    });
    boxes.push(box);
    const name = document.createElement("span");
    name.textContent = member.name;
    row.append(box, name);
    panel.appendChild(row);
  }
  if (boxes.length === 0) {
    const empty = document.createElement("p");
    empty.className = "menu-item muted";
    empty.textContent = S.seat.empty();
    panel.appendChild(empty);
  }
  refreshDisabled();
  document.body.appendChild(panel);
  panelNode = panel;
  panelTrigger = trigger;
  trigger.setAttribute("aria-expanded", "true");
  // The hover popovers stand down while a panel is open: two floating surfaces
  // over one cell is one too many.
  document.documentElement.classList.add("seat-picker-open");
  floatingPopover().hide();
  positionPanel();
  boxes[0]?.focus();
  document.addEventListener("pointerdown", onPointerDown, true);
  document.addEventListener("keydown", onKeydown, true);
  window.addEventListener("scroll", positionPanel, {capture: true, passive: true});
  window.addEventListener("resize", positionPanel);
}

function closePanel(): void {
  if (!panelNode) return;
  panelNode.remove();
  panelNode = null;
  panelTrigger?.setAttribute("aria-expanded", "false");
  panelTrigger = null;
  document.documentElement.classList.remove("seat-picker-open");
  document.removeEventListener("pointerdown", onPointerDown, true);
  document.removeEventListener("keydown", onKeydown, true);
  window.removeEventListener("scroll", positionPanel, {capture: true} as EventListenerOptions);
  window.removeEventListener("resize", positionPanel);
}

function onPointerDown(event: PointerEvent): void {
  if (!(event.target instanceof Node)) return;
  if (panelNode?.contains(event.target) || panelTrigger?.contains(event.target)) return;
  closePanel();
}

// Esc closes the panel and hands the focus back to its button, so the next
// arrow key moves the sheet cursor rather than the tickboxes.
function onKeydown(event: KeyboardEvent): void {
  if (event.key !== "Escape" || !panelNode) return;
  const trigger = panelTrigger;
  event.preventDefault();
  event.stopPropagation();
  closePanel();
  trigger?.focus();
}

// positionPanel parks the panel under its button, kept inside the viewport and
// flipped above when the button is near the bottom: a sheet scrolls in both
// directions, so no fixed side can be assumed. A sheet redrawn under the panel
// takes its button away, and the panel goes with it.
function positionPanel(): void {
  const panel = panelNode;
  const trigger = panelTrigger;
  if (!panel || !trigger) return;
  if (!trigger.isConnected) {
    closePanel();
    return;
  }
  const rect = trigger.getBoundingClientRect();
  const margin = PANEL_MARGIN_PX;
  panel.style.position = "fixed";
  panel.style.maxHeight = `${Math.max(PANEL_MIN_HEIGHT_PX, window.innerHeight - 2 * margin)}px`;
  const width = panel.offsetWidth;
  const height = panel.offsetHeight;
  const left = clamp(rect.left, margin, Math.max(margin, window.innerWidth - width - margin));
  const below = rect.bottom + margin + height <= window.innerHeight;
  const top = below ? rect.bottom + 2 : Math.max(margin, rect.top - height - 2);
  panel.style.left = `${left}px`;
  panel.style.top = `${top}px`;
}
