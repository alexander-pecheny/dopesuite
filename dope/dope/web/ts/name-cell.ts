// The one clipped name. Every team, player and venue name a table can clip is
// built by nameCell and measured by bindNameCells, so a long name fades at the
// edge of its cell and shows whole in the page's one popover (widgets.ts
// floatingPopover), the same way on every page.
//
// The markup is one vocabulary:
//
//   .name-cell             the cell, with the family classes its table lays
//                          out by (results-team, ek-team-cell, ...)
//     .name-cell-wrap      the box the name clips in
//       .name-cell-text    the name itself, focusable, with an aria-label
//     .popover-inline      the whole text, never shown: the popover reads it
//
// and one flag, .name-cell-truncated, which only bindNameCells writes. A page
// draws its cells and never measures them: the shell binds the pass once, and
// it notices new cells, changed text and resized columns by itself.

import {nameNode} from "./cells.js";

// shrinkToFit's floor, and the font size it assumes when the style has none.
const MIN_NAME_FONT_PX = 9;
const DEFAULT_NAME_FONT_PX = 13;

export const NAME_CELL = "name-cell";
export const NAME_CELL_TRUNCATED = "name-cell-truncated";
const NAME_WRAP = "name-cell-wrap";
const NAME_TEXT = "name-cell-text";
// A seat picker's cell has a form control where a name cell has its text. The
// control cannot report its own scrollWidth, so the pass measures its label in
// the control's font instead (controlTextOverflows).
const NAME_CONTROL_ATTR = "data-name-control";
const NAME_SELECTOR = `.${NAME_TEXT}, [${NAME_CONTROL_ATTR}]`;
// The EK stage sheet gives a name two lines and a smaller font before it clips.
const SHRINK_ATTR = "data-name-shrink";

export interface NameCellOptions {
  tag?: "td" | "th" | "span" | "div";
  // The family classes the table lays the cell out by.
  className?: string;
  // What a screen reader and the popover say, when it is not the text shown
  // (a number before the name, a flag that is decoration).
  ariaLabel?: string;
  popoverText?: string;
  // A link to the team's rating page.
  href?: string;
  // An extra class on the name itself (brain's muted unresolved slot).
  textClassName?: string;
  // The detailed sheets set the number, the name and the division badges side by
  // side in one grid (.od-detailed-team-layout); without it the badges and the
  // city sit on a second line under the name.
  layout?: boolean;
  number?: {text: string; className: string};
  badges?: readonly string[];
  city?: string;
  // shrink lets the name wrap onto a second line and step its font down
  // before it is called clipped. It is the EK stage sheet, where a bout's four
  // names share a narrow pinned column.
  shrink?: boolean;
}

// nameCell builds one clipped name. The cell starts unflagged: whether the
// name fits is a question of layout, and the shell's pass answers it once the
// cell is on the page.
export function nameCell(text: string, options: NameCellOptions = {}): HTMLElement {
  const cell = document.createElement(options.tag || "td");
  cell.className = options.className ? `${NAME_CELL} ${options.className}` : NAME_CELL;
  if (options.shrink) cell.setAttribute(SHRINK_ATTR, "");

  const wrap = document.createElement("span");
  wrap.className = NAME_WRAP;
  const name = nameNode(text, options.href || "", options.textClassName ? `${NAME_TEXT} ${options.textClassName}` : NAME_TEXT);
  name.tabIndex = 0;
  name.setAttribute("aria-label", options.ariaLabel ?? text);
  wrap.appendChild(name);

  const badges = teamFlagBadges(options.badges);
  if (options.layout) {
    const layout = document.createElement("span");
    layout.className = "od-detailed-team-layout";
    if (options.number) {
      const number = document.createElement("span");
      number.className = options.number.className;
      number.textContent = options.number.text;
      layout.appendChild(number);
    }
    layout.appendChild(wrap);
    // The badges are the layout grid's third column: beside the name, outside
    // the pill it clips and fades inside.
    if (badges) layout.appendChild(badges);
    cell.appendChild(layout);
  } else {
    const city = options.city ? cityNode(options.city) : null;
    if (badges && city) {
      const line = document.createElement("span");
      line.className = "u-row u-gap-xs u-align-center";
      line.appendChild(badges);
      line.appendChild(city);
      wrap.appendChild(line);
    } else if (badges || city) {
      wrap.appendChild((badges || city)!);
    }
    cell.appendChild(wrap);
  }

  const popover = document.createElement("span");
  popover.className = "popover popover-inline";
  popover.textContent = options.popoverText ?? text;
  cell.appendChild(popover);
  return cell;
}

// markNameControl makes a seat picker's cell a name cell: `cell` is flagged
// when the label `control` shows does not fit it, and its popover shows the
// whole label. The page keeps the popover's text (for a <select>, the pass
// copies the chosen option into it).
export function markNameControl(cell: HTMLElement, control: HTMLElement): void {
  cell.classList.add(NAME_CELL);
  control.setAttribute(NAME_CONTROL_ATTR, "");
}

// teamFlagBadges is a team's Flags after its name: small, quiet pills stating a
// fact about the team. Null when it carries none, so a cell that has nothing to
// say adds no node.
export function teamFlagBadges(flags: readonly string[] | undefined): HTMLElement | null {
  if (!flags || flags.length === 0) return null;
  const wrap = document.createElement("span");
  wrap.className = "u-row u-gap-xs u-align-center team-flags";
  for (const flag of flags) {
    const badge = document.createElement("span");
    badge.className = "team-flag";
    badge.textContent = flag;
    wrap.appendChild(badge);
  }
  return wrap;
}

// The city is chrome text under the name, not part of it: the popover leaves
// it out, and it fades the kit's way (.u-clip-fade) when it is too long.
function cityNode(city: string): HTMLElement {
  const node = document.createElement("span");
  node.className = "results-team-city u-clip-fade";
  node.textContent = city;
  return node;
}

// ---- the measuring pass ----

export interface NameCellPass {
  // Measure every name cell under the root again on the next frame.
  refresh(): void;
  dispose(): void;
}

const passes = new WeakMap<Element, NameCellPass>();

// bindNameCells keeps .name-cell-truncated true on exactly the name cells
// under `root` whose name does not fit. It measures a cell when it appears,
// when its text changes, when its name's box is resized and when a control in
// it changes, all in one frame: every width is read before any class is
// written, so a pass lays the page out once. Binding a root twice returns the
// first binding.
export function bindNameCells(root: Element): NameCellPass {
  const bound = passes.get(root);
  if (bound) return bound;

  const dirty = new Set<Element>();
  // The name each observed cell is measured by, kept so a removed cell can be
  // let go of without reading the cell again.
  const observed = new Map<Element, Element>();
  let frame = 0;

  const resize = typeof ResizeObserver === "function"
    ? new ResizeObserver((entries) => {
      for (const entry of entries) {
        const cell = entry.target.closest(`.${NAME_CELL}`);
        if (cell) dirty.add(cell);
      }
      schedule();
    })
    : null;

  function schedule(): void {
    if (!frame) frame = requestAnimationFrame(measure);
  }

  function track(cell: Element): void {
    dirty.add(cell);
    if (observed.has(cell)) return;
    const name = cell.querySelector(NAME_SELECTOR);
    if (!name) return;
    observed.set(cell, name);
    resize?.observe(name);
  }

  function forget(cell: Element): void {
    const name = observed.get(cell);
    if (!name) return;
    observed.delete(cell);
    resize?.unobserve(name);
  }

  // cellsIn is a node's own name cells: itself, and every one inside it.
  function cellsIn(node: Node): Element[] {
    if (!(node instanceof Element)) return [];
    const cells = Array.from(node.querySelectorAll(`.${NAME_CELL}`));
    if (node.classList.contains(NAME_CELL)) cells.unshift(node);
    return cells;
  }

  function onMutations(records: MutationRecord[]): void {
    for (const record of records) {
      // Text that changed inside a cell: its name, or the popover's text.
      const inside = record.target instanceof Element ? record.target : record.target.parentElement;
      const owner = inside?.closest(`.${NAME_CELL}`);
      if (owner) track(owner);
      for (const node of record.addedNodes) cellsIn(node).forEach(track);
      for (const node of record.removedNodes) {
        for (const cell of cellsIn(node)) {
          if (!cell.isConnected) forget(cell);
        }
      }
    }
    if (dirty.size) schedule();
  }

  // A <select> takes its new value with no change to the DOM, so the pass
  // listens for the change itself.
  function onChange(event: Event): void {
    const cell = event.target instanceof Element ? event.target.closest(`.${NAME_CELL}`) : null;
    if (!cell) return;
    dirty.add(cell);
    schedule();
  }

  function measure(): void {
    frame = 0;
    const cells = Array.from(dirty).filter((cell) => cell.isConnected);
    dirty.clear();
    const plain: Array<{cell: Element; clipped: boolean; popover?: string}> = [];
    const shrinking: Array<{cell: Element; name: HTMLElement}> = [];
    for (const cell of cells) {
      const name = cell.querySelector<HTMLElement>(NAME_SELECTOR);
      if (!name) continue;
      if (cell.hasAttribute(SHRINK_ATTR)) {
        shrinking.push({cell, name});
      } else if (name.hasAttribute(NAME_CONTROL_ATTR)) {
        const label = controlLabel(name);
        plain.push({
          cell,
          clipped: Boolean(label) && controlTextOverflows(name, label),
          popover: name instanceof HTMLSelectElement ? label : undefined,
        });
      } else {
        plain.push({cell, clipped: isClipped(name)});
      }
    }
    for (const {cell, clipped, popover} of plain) {
      cell.classList.toggle(NAME_CELL_TRUNCATED, clipped);
      if (popover === undefined) continue;
      const source = cell.querySelector(".popover-inline");
      // Writing the same text would be a mutation, and a mutation is a pass.
      if (source && source.textContent !== popover) source.textContent = popover;
    }
    // A shrink reads after each write by its nature, so these go last, after
    // the batch above has been laid out.
    for (const {cell, name} of shrinking) {
      cell.classList.toggle(NAME_CELL_TRUNCATED, shrinkToFit(name));
    }
  }

  const mutations = typeof MutationObserver === "function" ? new MutationObserver(onMutations) : null;
  mutations?.observe(root, {childList: true, subtree: true, characterData: true});
  root.addEventListener("change", onChange);
  // A web font that arrives late changes every width at once.
  const refresh = (): void => {
    root.querySelectorAll(`.${NAME_CELL}`).forEach(track);
    schedule();
  };
  document.fonts?.ready.then(refresh);
  refresh();

  const pass: NameCellPass = {
    refresh,
    dispose() {
      if (frame) cancelAnimationFrame(frame);
      frame = 0;
      mutations?.disconnect();
      resize?.disconnect();
      root.removeEventListener("change", onChange);
      observed.clear();
      dirty.clear();
      passes.delete(root);
    },
  };
  passes.set(root, pass);
  return pass;
}

// isClipped is the one definition of "this text does not fit its box", epsilon
// included.
export function isClipped(el: Element | null | undefined): boolean {
  return Boolean(el && el.scrollWidth > el.clientWidth + 1);
}

// controlLabel is the text a seat control shows: a <select>'s chosen option,
// a button's own line.
function controlLabel(control: HTMLElement): string {
  if (control instanceof HTMLSelectElement) return control.selectedOptions?.[0]?.textContent || "";
  return control.textContent || "";
}

// controlTextOverflows says whether a form control's text is wider than the
// room inside its padding. A <select> or a button cannot report its own
// scrollWidth, so the text is measured in the control's font instead.
let controlMeasureContext: CanvasRenderingContext2D | null = null;
export function controlTextOverflows(control: HTMLElement | null, label: string): boolean {
  if (!control || !label) return false;
  const style = getComputedStyle(control);
  const available = control.clientWidth - parseFloat(style.paddingLeft || "0") - parseFloat(style.paddingRight || "0");
  if (available <= 0) return false;
  controlMeasureContext ||= document.createElement("canvas").getContext("2d");
  if (!controlMeasureContext) return false;
  controlMeasureContext.font = style.font;
  return controlMeasureContext.measureText(label).width > available + 1;
}

// shrinkToFit steps a wrapping name's font down until its lines fit the cell's
// height, to 9px at the least, and says whether it still does not fit.
function shrinkToFit(name: HTMLElement): boolean {
  const minSize = MIN_NAME_FONT_PX;
  name.style.fontSize = "";
  const baseSize = parseFloat(getComputedStyle(name).fontSize) || DEFAULT_NAME_FONT_PX;
  const tooTall = (): boolean => name.scrollHeight > name.clientHeight + 1;
  if (tooTall()) {
    let size = Math.floor(baseSize) - 1;
    while (size >= minSize) {
      name.style.fontSize = `${size}px`;
      if (!tooTall()) break;
      size -= 1;
    }
    if (size < minSize) name.style.fontSize = `${minSize}px`;
  }
  return tooTall() || isClipped(name);
}
