// Interaction widgets shared by the game pages: cell nav bar, virtual keypad,
// floating popovers, sync-status dot, team-name overflow, cell range selection,
// and the viewer counter. DOM-only — no table building, no sync.

export function clamp(value: number, min: number, max: number): number {
  return Math.max(min, Math.min(max, value));
}

export interface CellNavBarOptions {
  onPrev?: () => void;
  onNext?: () => void;
  prevLabel?: string;
  nextLabel?: string;
}

export interface CellNavBar {
  show(): void;
  hide(): void;
}

// installCellNavBar mounts a floating ↑/↓ bar pinned just above the on-screen
// keyboard for advancing between editable cells. Mobile numeric keypads
// (inputmode=numeric/decimal) have no Return key on iOS, so this is the only
// way to step cell-to-cell without dismissing the keypad. Rendered only on
// coarse-pointer (touch) devices — on desktop, Enter/Tab already do this.
//
// The caller drives visibility with show()/hide(); buttons fire onPrev/onNext
// on `pointerdown` with the default prevented, so the focused input is never
// blurred and the keyboard stays up while we programmatically move focus.
export function installCellNavBar(options: CellNavBarOptions = {}): CellNavBar {
  const coarse = typeof window.matchMedia === "function" &&
    window.matchMedia("(pointer: coarse)").matches;
  if (!coarse) return {show: () => {}, hide: () => {}};

  const {onPrev, onNext, prevLabel = "▲", nextLabel = "▼"} = options;
  const bar = document.createElement("div");
  bar.className = "entry-nav-bar";
  bar.hidden = true;
  const make = (label: string, aria: string, handler?: () => void) => {
    const button = document.createElement("button");
    button.type = "button";
    button.textContent = label;
    button.setAttribute("aria-label", aria);
    button.addEventListener("pointerdown", (event) => {
      event.preventDefault();
      handler?.();
    });
    return button;
  };
  bar.append(
    make(prevLabel, "Предыдущая ячейка", onPrev),
    make(nextLabel, "Следующая ячейка", onNext),
  );
  document.body.appendChild(bar);

  let visible = false;
  // Pin to the visual viewport's box (see installVirtualKeypad): iOS resolves
  // fixed + right:0 against the document width when the page scrolls
  // horizontally, overflowing the screen and skewing the arrows.
  const position = () => {
    if (!visible) return;
    const vv = window.visualViewport;
    if (vv) {
      bar.style.left = `${Math.round(vv.offsetLeft)}px`;
      bar.style.right = "auto";
      bar.style.width = `${Math.round(vv.width)}px`;
      bar.style.top = `${Math.round(vv.offsetTop + vv.height - bar.offsetHeight)}px`;
      bar.style.bottom = "auto";
    } else {
      bar.style.left = "0px";
      bar.style.right = "0px";
      bar.style.width = "auto";
      bar.style.top = "auto";
      bar.style.bottom = "0px";
    }
  };
  const vv = window.visualViewport;
  if (vv) {
    vv.addEventListener("resize", position);
    vv.addEventListener("scroll", position);
  }
  return {
    show() {
      visible = true;
      bar.hidden = false; // unhide before measuring offsetHeight
      position();
    },
    hide() {
      visible = false;
      bar.hidden = true;
    },
  };
}

export interface VirtualKeypadOptions {
  onDigit?: (digit: string) => void;
  onBackspace?: () => void;
  onNav?: (dx: number, dy: number) => void;
}

export interface VirtualKeypad {
  show(): void;
  hide(): void;
  visible(): boolean;
  height(): number;
}

// installVirtualKeypad mounts a full on-screen numeric keypad pinned to the
// bottom of the visual viewport. It replaces the OS keyboard for digit-only
// cell entry on touch devices: the host <input> sets inputmode="none" so
// iOS/Android suppress their native keypad (which looks out of place and,
// on iOS, lacks a Return key), and these keys drive the input via callbacks.
// Layout: a navigation row (← ↑ ↓ →) above a 3-column digit pad (1–9, then a
// double-width 0 and ⌫). Rendered only on coarse-pointer devices — on desktop
// the physical keyboard and arrow-key navigation already cover this, so it
// returns no-ops. Buttons fire on `pointerdown` with the default prevented so
// the focused input is never blurred and its caret/selection survive editing.
export function installVirtualKeypad(options: VirtualKeypadOptions = {}): VirtualKeypad {
  const coarse = typeof window.matchMedia === "function" &&
    window.matchMedia("(pointer: coarse)").matches;
  if (!coarse) return {show: () => {}, hide: () => {}, visible: () => false, height: () => 0};

  const {onDigit, onBackspace, onNav} = options;
  const pad = document.createElement("div");
  pad.className = "entry-keypad";
  pad.hidden = true;

  const key = (label: string, aria: string, className: string, handler?: () => void) => {
    const button = document.createElement("button");
    button.type = "button";
    button.className = className;
    button.textContent = label;
    button.setAttribute("aria-label", aria);
    button.addEventListener("pointerdown", (event) => {
      event.preventDefault();
      handler?.();
    });
    return button;
  };

  const navRow = document.createElement("div");
  navRow.className = "entry-keypad-nav";
  navRow.append(
    key("←", "Предыдущая колонка", "entry-keypad-key entry-keypad-arrow", () => onNav?.(-1, 0)),
    key("↑", "Предыдущая строка", "entry-keypad-key entry-keypad-arrow", () => onNav?.(0, -1)),
    key("↓", "Следующая строка", "entry-keypad-key entry-keypad-arrow", () => onNav?.(0, 1)),
    key("→", "Следующая колонка", "entry-keypad-key entry-keypad-arrow", () => onNav?.(1, 0)),
  );

  const digits = document.createElement("div");
  digits.className = "entry-keypad-digits";
  for (let n = 1; n <= 9; n++) {
    digits.appendChild(key(String(n), String(n), "entry-keypad-key", () => onDigit?.(String(n))));
  }
  digits.appendChild(key("0", "0", "entry-keypad-key entry-keypad-zero", () => onDigit?.("0")));
  digits.appendChild(key("⌫", "Удалить", "entry-keypad-key entry-keypad-back", () => onBackspace?.()));

  pad.append(navRow, digits);
  document.body.appendChild(pad);

  let isVisible = false;
  // Pin to the visual viewport's box explicitly. iOS Safari resolves
  // position:fixed + right:0 against the document width when the page scrolls
  // horizontally (our entry table is wide), which overflows the screen — so we
  // set left/width/top from visualViewport instead of relying on left/right:0.
  const position = () => {
    if (!isVisible) return;
    const vv = window.visualViewport;
    if (vv) {
      pad.style.left = `${Math.round(vv.offsetLeft)}px`;
      pad.style.right = "auto";
      pad.style.width = `${Math.round(vv.width)}px`;
      pad.style.top = `${Math.round(vv.offsetTop + vv.height - pad.offsetHeight)}px`;
      pad.style.bottom = "auto";
    } else {
      pad.style.left = "0px";
      pad.style.right = "0px";
      pad.style.width = "auto";
      pad.style.top = "auto";
      pad.style.bottom = "0px";
    }
  };
  const vv = window.visualViewport;
  if (vv) {
    vv.addEventListener("resize", position);
    vv.addEventListener("scroll", position);
  }
  return {
    show() {
      isVisible = true;
      pad.hidden = false; // unhide before measuring offsetHeight
      position();
    },
    hide() {
      isVisible = false;
      pad.hidden = true;
    },
    visible: () => isVisible,
    height: () => (isVisible ? pad.offsetHeight : 0),
  };
}

export interface FloatingPopoverSpec {
  trigger: string;
  popover: string;
  anchor: string;
}

export interface FloatingPopoverOptions {
  root?: Element | Document | null;
  specs?: FloatingPopoverSpec[];
}

export interface FloatingPopover {
  bind(): void;
  hide(): void;
  position(): void;
}

export function createFloatingPopover(options: FloatingPopoverOptions): FloatingPopover {
  const root = options.root;
  const specs = options.specs || [];
  if (!root || specs.length === 0) {
    return {bind: () => {}, hide: () => {}, position: () => {}};
  }

  let popoverNode: HTMLElement | null = null;
  let active: {trigger: Element; spec: FloatingPopoverSpec} | null = null;

  function triggerFor(target: EventTarget | null): Element | null {
    if (!(target instanceof Element)) return null;
    for (const spec of specs) {
      const trigger = target.closest(spec.trigger);
      if (trigger && root!.contains(trigger)) return trigger;
    }
    return null;
  }

  function specFor(trigger: Element): FloatingPopoverSpec | null {
    return specs.find((spec) => trigger.matches(spec.trigger)) || null;
  }

  function ensureNode(): HTMLElement {
    if (!popoverNode) {
      popoverNode = document.createElement("div");
      popoverNode.className = "popover floating-name-popover";
      document.body.appendChild(popoverNode);
    }
    return popoverNode;
  }

  function show(trigger: Element): void {
    const spec = specFor(trigger);
    const source = spec ? trigger.querySelector(spec.popover) : null;
    const text = source?.textContent?.trim() || "";
    if (!spec || !text) {
      hide();
      return;
    }
    const popover = ensureNode();
    popover.textContent = text;
    popover.classList.add("visible");
    active = {trigger, spec};
    position();
  }

  function hide(): void {
    if (!popoverNode) return;
    popoverNode.classList.remove("visible", "above");
    popoverNode.textContent = "";
    popoverNode.style.removeProperty("top");
    popoverNode.style.removeProperty("left");
    popoverNode.style.removeProperty("max-width");
    active = null;
  }

  function position(): void {
    if (!active || !popoverNode) return;
    const {trigger, spec} = active;
    if (!document.body.contains(trigger) || !trigger.matches(spec.trigger)) {
      hide();
      return;
    }
    const anchor = trigger.querySelector(spec.anchor) || trigger;
    const rect = anchor.getBoundingClientRect();
    if (rect.width <= 0 || rect.height <= 0 || rect.bottom < 0 || rect.top > window.innerHeight) {
      hide();
      return;
    }

    const margin = 8;
    const popover = popoverNode;
    popover.style.maxWidth = `${Math.max(80, Math.min(420, window.innerWidth - margin * 2))}px`;
    popover.style.visibility = "hidden";
    popover.classList.add("visible");

    const width = popover.offsetWidth;
    const height = popover.offsetHeight;
    const maxLeft = Math.max(margin, window.innerWidth - width - margin);
    const left = clamp(rect.left, margin, maxLeft);
    const belowTop = rect.bottom - 2;
    const aboveTop = rect.top - height + 2;
    const shouldOpenUp = belowTop + height > window.innerHeight - margin && rect.top > window.innerHeight - rect.bottom;
    const maxTop = Math.max(margin, window.innerHeight - height - margin);
    const top = clamp(shouldOpenUp ? aboveTop : belowTop, margin, maxTop);

    popover.classList.toggle("above", shouldOpenUp);
    popover.style.left = `${Math.round(left)}px`;
    popover.style.top = `${Math.round(top)}px`;
    popover.style.visibility = "";
  }

  function onPointerOver(event: PointerEvent): void {
    // On touch, pointerover fires while swiping across cells; showing here
    // would pop the popover on every swipe. Touch shows via tap (see onTapEnd).
    if (event.pointerType === "touch") return;
    const trigger = triggerFor(event.target);
    if (!trigger || active?.trigger === trigger) return;
    show(trigger);
  }

  let tapStart: {x: number; y: number} | null = null;
  const TAP_MOVE_THRESHOLD = 10;

  function onTapStart(event: PointerEvent): void {
    if (event.pointerType !== "touch") return;
    tapStart = {x: event.clientX, y: event.clientY};
  }

  function onTapEnd(event: PointerEvent): void {
    if (event.pointerType !== "touch" || !tapStart) return;
    const moved = Math.hypot(event.clientX - tapStart.x, event.clientY - tapStart.y);
    tapStart = null;
    if (moved > TAP_MOVE_THRESHOLD) return; // a swipe, not a tap
    const trigger = triggerFor(event.target);
    if (trigger) {
      if (active?.trigger !== trigger) show(trigger);
    } else {
      hide();
    }
  }

  function onPointerOut(event: PointerEvent): void {
    if (event.pointerType === "touch") return;
    const trigger = active?.trigger;
    if (!trigger || !(event.target instanceof Node) || !trigger.contains(event.target)) return;
    if (event.relatedTarget instanceof Node && trigger.contains(event.relatedTarget)) return;
    if (!trigger.matches(":focus-within")) hide();
  }

  function onFocusIn(event: FocusEvent): void {
    const trigger = triggerFor(event.target);
    if (trigger) show(trigger);
  }

  function onFocusOut(event: FocusEvent): void {
    const trigger = active?.trigger;
    if (!trigger || !(event.target instanceof Node) || !trigger.contains(event.target)) return;
    window.setTimeout(() => {
      if (!trigger.matches(":focus-within") && !trigger.matches(":hover")) hide();
    }, 0);
  }

  let positionFrame = 0;
  function schedulePosition(): void {
    if (positionFrame) return;
    positionFrame = requestAnimationFrame(() => {
      positionFrame = 0;
      position();
    });
  }

  function onPointerDownOutside(event: PointerEvent): void {
    if (!active || event.pointerType !== "touch") return;
    if (event.target instanceof Node && active.trigger.contains(event.target)) return;
    hide();
  }

  function bind(): void {
    document.documentElement.classList.add("floating-popovers-enabled");
    document.addEventListener("pointerover", onPointerOver);
    document.addEventListener("pointerout", onPointerOut);
    document.addEventListener("focusin", onFocusIn);
    document.addEventListener("focusout", onFocusOut);
    document.addEventListener("pointerdown", onPointerDownOutside, true);
    document.addEventListener("pointerdown", onTapStart, true);
    document.addEventListener("pointerup", onTapEnd, true);
    window.addEventListener("scroll", schedulePosition, {capture: true, passive: true});
  }

  return {bind, hide, position};
}

const SYNC_STATUS_LABELS: Record<string, string> = {
  saved: "Синхронизировано",
  saving: "Синхронизация",
  reconnecting: "Переподключение",
  error: "Ошибка",
};

export function createStatusReporter(statusNode: HTMLElement | null | undefined): (state: string) => void {
  const node = statusNode;
  if (!node) return () => {};
  return function setStatus(state: string) {
    node.dataset.state = state;
    const label = SYNC_STATUS_LABELS[state] || SYNC_STATUS_LABELS.saving;
    node.setAttribute("aria-label", label);
    node.title = label;
  };
}

// The standalone ✏️/👀 icons were folded into the ☰ menu (menu.js).
// These now register the menu's context-aware jump item instead of mounting
// an icon; .refresh() re-points it after SPA navigation. statusNode is kept
// for call-site compatibility but unused.

export interface NameOverflowConfig {
  cellSelector: string;
  nameSelector: string;
  truncatedClass: string;
  citySelector?: string;
  cityTruncatedClass?: string;
}

// markNameOverflow flags every cell under `root` whose inner name (and optional
// city) is clipped, so the page can show a fade + popover. Reads are batched
// ahead of writes so the measure loop never triggers a reflow mid-pass.
export function markNameOverflow(root: ParentNode | null | undefined, cfg: NameOverflowConfig): void {
  if (!root) return;
  const cells = root.querySelectorAll(cfg.cellSelector);
  const readings = new Array<boolean>(cells.length);
  for (let i = 0; i < cells.length; i++) {
    const name = cells[i].querySelector(cfg.nameSelector);
    readings[i] = Boolean(name && name.scrollWidth > name.clientWidth + 1);
  }
  for (let i = 0; i < cells.length; i++) {
    cells[i].classList.toggle(cfg.truncatedClass, readings[i]);
    if (cfg.citySelector && cfg.cityTruncatedClass) {
      const city = cells[i].querySelector(cfg.citySelector);
      city?.classList.toggle(cfg.cityTruncatedClass, city.scrollWidth > city.clientWidth + 1);
    }
  }
}

export interface TeamNameOverflowController {
  schedule(targetRoot?: ParentNode): void;
  updateDetailed(targetRoot?: ParentNode): void;
  updateResults(targetRoot?: ParentNode): void;
}

export function createTeamNameOverflowController({root, detailed, results}: {
  root: ParentNode;
  detailed: NameOverflowConfig;
  results: NameOverflowConfig;
}): TeamNameOverflowController {
  function updateDetailed(targetRoot: ParentNode = root): void {
    markNameOverflow(targetRoot, detailed);
  }
  function updateResults(targetRoot: ParentNode = root): void {
    markNameOverflow(targetRoot, results);
  }
  let frame = 0;
  function schedule(targetRoot: ParentNode = root): void {
    if (frame) cancelAnimationFrame(frame);
    frame = requestAnimationFrame(() => {
      frame = 0;
      updateDetailed(targetRoot);
      updateResults(targetRoot);
    });
  }
  return {schedule, updateDetailed, updateResults};
}

export interface CellCoord {
  row: number;
  col: number;
}

export interface CellRect {
  rowStart: number;
  rowEnd: number;
  colStart: number;
  colEnd: number;
}

export interface CellEdit {
  cell: Element;
  value: unknown;
}

export interface CellRangeClasses {
  selected: string;
  anchor: string;
  top: string;
  bottom: string;
  left: string;
  right: string;
}

export interface CellRangeSelectionOptions {
  root: HTMLElement;
  cellSelector?: string;
  readonly?: boolean | (() => boolean);
  coordOf: (cell: Element) => CellCoord | null | undefined;
  cellAtCoord: (coord: CellCoord | null) => HTMLElement | null | undefined;
  serialize?: (cell: Element | null | undefined) => string;
  parse?: (text: string) => unknown;
  applyValues?: (edits: CellEdit[]) => void;
  onSelectionChange?: (selection: {anchor: CellCoord | null; focus: CellCoord | null; rect: CellRect | null} | null) => void;
  onActiveChange?: (cell: HTMLElement, coord: CellCoord | null) => void;
  // cycle(cell) -> next value (in applyValues' value space) for a touch tap.
  // When provided, tapping a cell on a touch device advances it through its
  // states (e.g. empty → right → wrong → empty), the only way to enter data
  // on mobile where there is no physical +/- keyboard.
  cycle?: ((cell: Element) => unknown) | null;
  classes?: Partial<CellRangeClasses>;
}

export interface CellRangeSelection {
  bind(): void;
  unbind(): void;
  setSelection(anchor: CellCoord | null, focus?: CellCoord | null, opts?: {focus?: boolean; preventScroll?: boolean}): void;
  clearSelection(): void;
  deleteSelected(): boolean;
  selectedCells(): HTMLElement[];
  refresh(): void;
  readonly anchor: CellCoord | null;
  readonly focus: CellCoord | null;
  readonly rect: CellRect | null;
}

export function createCellRangeSelection(options: CellRangeSelectionOptions): CellRangeSelection {
  const {
    root,
    cellSelector = ".answer-cell",
    readonly = () => false,
    coordOf,
    cellAtCoord,
    serialize = (cell) => (cell?.textContent || "").trim(),
    parse = (text) => String(text || "").trim(),
    applyValues,
    onSelectionChange,
    onActiveChange,
    cycle = null,
    classes = {},
  } = options;
  const cls: CellRangeClasses = {
    selected: "cell-selected",
    anchor: "cell-selection-anchor",
    top: "cell-selection-top",
    bottom: "cell-selection-bottom",
    left: "cell-selection-left",
    right: "cell-selection-right",
    ...classes,
  };
  const allClasses = [cls.selected, cls.anchor, cls.top, cls.bottom, cls.left, cls.right];
  const isReadonly = typeof readonly === "function" ? readonly : () => Boolean(readonly);

  let anchor: CellCoord | null = null;
  let focusCoord: CellCoord | null = null;
  let dragState: {anchor: CellCoord; focus: CellCoord} | null = null;
  let suppressNextClick = false;
  let tapStart: {cell: Element; x: number; y: number} | null = null;

  function rect(): CellRect | null {
    if (!anchor || !focusCoord) return null;
    return {
      rowStart: Math.min(anchor.row, focusCoord.row),
      rowEnd: Math.max(anchor.row, focusCoord.row),
      colStart: Math.min(anchor.col, focusCoord.col),
      colEnd: Math.max(anchor.col, focusCoord.col),
    };
  }

  function clearClasses(): void {
    root.querySelectorAll(`${cellSelector}.${cls.selected}, ${cellSelector}.${cls.anchor}`).forEach((cell) => {
      cell.classList.remove(...allClasses);
    });
  }

  function renderClasses(): void {
    clearClasses();
    const r = rect();
    if (!r) return;
    for (let row = r.rowStart; row <= r.rowEnd; row++) {
      for (let col = r.colStart; col <= r.colEnd; col++) {
        const cell = cellAtCoord({row, col});
        if (!cell) continue;
        cell.classList.add(cls.selected);
        if (row === r.rowStart) cell.classList.add(cls.top);
        if (row === r.rowEnd) cell.classList.add(cls.bottom);
        if (col === r.colStart) cell.classList.add(cls.left);
        if (col === r.colEnd) cell.classList.add(cls.right);
      }
    }
    const anchorCell = cellAtCoord(anchor);
    if (anchorCell) anchorCell.classList.add(cls.anchor);
  }

  function selectedCells(): HTMLElement[] {
    const out: HTMLElement[] = [];
    const r = rect();
    if (!r) return out;
    for (let row = r.rowStart; row <= r.rowEnd; row++) {
      for (let col = r.colStart; col <= r.colEnd; col++) {
        const cell = cellAtCoord({row, col});
        if (cell) out.push(cell);
      }
    }
    return out;
  }

  function setSelection(newAnchor: CellCoord | null, newFocus: CellCoord | null = newAnchor, opts: {focus?: boolean; preventScroll?: boolean} = {}): void {
    anchor = newAnchor ? {row: newAnchor.row, col: newAnchor.col} : null;
    focusCoord = newFocus ? {row: newFocus.row, col: newFocus.col} : null;
    renderClasses();
    onSelectionChange?.({anchor, focus: focusCoord, rect: rect()});
    const focusCell = cellAtCoord(focusCoord);
    if (focusCell) {
      if (opts.focus !== false) focusCell.focus({preventScroll: opts.preventScroll});
      onActiveChange?.(focusCell, focusCoord);
    }
  }

  function clearSelection(): void {
    anchor = null;
    focusCoord = null;
    clearClasses();
    onSelectionChange?.(null);
  }

  function deleteSelected(): boolean {
    if (isReadonly()) return false;
    const cells = selectedCells();
    if (cells.length === 0) return false;
    const empty = parse("");
    const edits: CellEdit[] = [];
    for (const cell of cells) edits.push({cell, value: empty});
    applyValues?.(edits);
    return true;
  }

  function copySelection(event: ClipboardEvent): boolean {
    const r = rect();
    if (!r) return false;
    const lines: string[] = [];
    for (let row = r.rowStart; row <= r.rowEnd; row++) {
      const cols: string[] = [];
      for (let col = r.colStart; col <= r.colEnd; col++) {
        const cell = cellAtCoord({row, col});
        cols.push(cell ? serialize(cell) : "");
      }
      lines.push(cols.join("\t"));
    }
    event.clipboardData?.setData("text/plain", lines.join("\n"));
    event.preventDefault();
    return true;
  }

  function pasteSelection(event: ClipboardEvent): boolean {
    if (isReadonly() || !anchor) return false;
    const text = event.clipboardData?.getData("text/plain") || "";
    if (!text) return false;
    event.preventDefault();
    const normalized = text.replace(/\r\n/g, "\n").replace(/\r/g, "\n");
    const lines = normalized.split("\n");
    if (lines.length > 1 && lines[lines.length - 1] === "") lines.pop();
    const grid = lines.map((line) => line.split("\t"));
    if (grid.length === 0) return false;
    const r = rect();
    if (!r) return false;
    const startRow = r.rowStart;
    const startCol = r.colStart;
    const edits: CellEdit[] = [];
    let lastRow = startRow;
    let lastCol = startCol;
    for (let rOff = 0; rOff < grid.length; rOff++) {
      const cols = grid[rOff];
      for (let cOff = 0; cOff < cols.length; cOff++) {
        const cell = cellAtCoord({row: startRow + rOff, col: startCol + cOff});
        if (!cell) continue;
        edits.push({cell, value: parse(cols[cOff])});
        lastRow = startRow + rOff;
        lastCol = startCol + cOff;
      }
    }
    if (edits.length > 0) applyValues?.(edits);
    setSelection({row: startRow, col: startCol}, {row: lastRow, col: lastCol}, {focus: true});
    return true;
  }

  function handleMouseDown(event: MouseEvent): void {
    if (event.button !== 0 || isReadonly()) return;
    const target = event.target;
    const cell = target instanceof Element ? target.closest(cellSelector) : null;
    if (!cell || !root.contains(cell)) return;
    if (target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement || target instanceof HTMLSelectElement) return;
    const coord = coordOf(cell);
    if (!coord) return;
    event.preventDefault();
    suppressNextClick = Boolean(event.shiftKey && anchor);
    const nextAnchor = event.shiftKey && anchor ? anchor : coord;
    setSelection(nextAnchor, coord, {preventScroll: true});
    dragState = {anchor: nextAnchor, focus: coord};
    document.addEventListener("mouseup", handleMouseUp, {once: true});
  }

  function handleMouseUp(): void {
    dragState = null;
  }

  function handleMouseOver(event: MouseEvent): void {
    if (!dragState || isReadonly()) return;
    const target = event.target;
    const cell = target instanceof Element ? target.closest(cellSelector) : null;
    if (!cell || !root.contains(cell)) return;
    const coord = coordOf(cell);
    if (!coord) return;
    if (coord.row === dragState.focus.row && coord.col === dragState.focus.col) return;
    dragState.focus = coord;
    setSelection(dragState.anchor, coord, {focus: false});
  }

  function handleClickCapture(event: MouseEvent): void {
    if (suppressNextClick) {
      suppressNextClick = false;
      event.stopPropagation();
    }
  }

  function isEditableTarget(target: EventTarget | null): boolean {
    return target instanceof HTMLInputElement
      || target instanceof HTMLTextAreaElement
      || target instanceof HTMLSelectElement
      || (target instanceof HTMLElement && target.isContentEditable);
  }

  function handleCopy(event: ClipboardEvent): void {
    const target = event.target;
    if (isEditableTarget(target)) return;
    if (!(target instanceof Node && root.contains(target)) && !root.contains(document.activeElement)) return;
    copySelection(event);
  }

  function handlePaste(event: ClipboardEvent): void {
    const target = event.target;
    if (isEditableTarget(target)) return;
    if (!(target instanceof Node && root.contains(target)) && !root.contains(document.activeElement)) return;
    pasteSelection(event);
  }

  // Touch taps cycle a cell's value via `cycle` (see option docs). We track
  // the touch from pointerdown so we can tell a tap from a scroll: if the
  // finger moves more than a few px or lifts off a different cell, it was a
  // scroll and we leave the value alone. Gated on pointerType === "touch", so
  // mouse clicks (desktop select/drag) are unaffected.
  const TAP_MOVE_TOLERANCE = 10;

  function handlePointerDown(event: PointerEvent): void {
    if (event.pointerType !== "touch" || !cycle || isReadonly()) {
      tapStart = null;
      return;
    }
    const target = event.target;
    const cell = target instanceof Element ? target.closest(cellSelector) : null;
    if (!cell || !root.contains(cell) || isEditableTarget(target)) {
      tapStart = null;
      return;
    }
    tapStart = {cell, x: event.clientX, y: event.clientY};
  }

  function handlePointerUp(event: PointerEvent): void {
    if (event.pointerType !== "touch" || !cycle) return;
    const start = tapStart;
    tapStart = null;
    if (!start || isReadonly()) return;
    const target = event.target;
    const cell = target instanceof Element ? target.closest(cellSelector) : null;
    if (!cell || cell !== start.cell) return;
    if (Math.abs(event.clientX - start.x) > TAP_MOVE_TOLERANCE
      || Math.abs(event.clientY - start.y) > TAP_MOVE_TOLERANCE) return;
    const value = cycle(cell);
    if (value === undefined || value === null) return;
    applyValues?.([{cell, value}]);
    const coord = coordOf(cell);
    if (coord) setSelection(coord, coord, {focus: false});
  }

  function handlePointerCancel(): void {
    tapStart = null;
  }

  function bind(): void {
    root.addEventListener("mousedown", handleMouseDown);
    root.addEventListener("mouseover", handleMouseOver);
    root.addEventListener("click", handleClickCapture, true);
    root.addEventListener("pointerdown", handlePointerDown);
    root.addEventListener("pointerup", handlePointerUp);
    root.addEventListener("pointercancel", handlePointerCancel);
    document.addEventListener("copy", handleCopy);
    document.addEventListener("paste", handlePaste);
  }

  function unbind(): void {
    root.removeEventListener("mousedown", handleMouseDown);
    root.removeEventListener("mouseover", handleMouseOver);
    root.removeEventListener("click", handleClickCapture, true);
    root.removeEventListener("pointerdown", handlePointerDown);
    root.removeEventListener("pointerup", handlePointerUp);
    root.removeEventListener("pointercancel", handlePointerCancel);
    document.removeEventListener("copy", handleCopy);
    document.removeEventListener("paste", handlePaste);
  }

  return {
    bind,
    unbind,
    setSelection,
    clearSelection,
    deleteSelected,
    selectedCells,
    refresh: renderClasses,
    get anchor() { return anchor; },
    get focus() { return focusCoord; },
    get rect() { return rect(); },
  };
}

// createViewerCounter renders a live "NN👀" concurrent-viewer tally
// immediately to the left of the sync-status tick. The span is created and
// inserted dynamically (no markup change needed) and stays hidden until a
// positive count arrives. setCount is driven by "viewers" SSE events.
export function createViewerCounter(statusNode: HTMLElement | null | undefined): {setCount(count: unknown): void} {
  if (!statusNode || !statusNode.parentElement) {
    return {setCount: () => {}};
  }
  const node = document.createElement("span");
  node.className = "viewers-count";
  node.hidden = true;
  node.setAttribute("aria-label", "Зрителей онлайн");
  // Number and eyes are separate children so the flex `gap` spaces them — a
  // single "N👀" text node would render them touching.
  const num = document.createElement("span");
  num.className = "viewers-count-num";
  const eyes = document.createElement("span");
  eyes.className = "viewers-count-eyes";
  eyes.textContent = "\u{1F440}";
  eyes.setAttribute("aria-hidden", "true");
  node.append(num, eyes);
  statusNode.parentElement.insertBefore(node, statusNode);
  return {
    setCount(count) {
      const n = Number(count);
      if (!Number.isFinite(n) || n <= 0) {
        node.hidden = true;
        num.textContent = "";
        return;
      }
      num.textContent = String(n);
      node.title = `Зрителей онлайн: ${n}`;
      node.hidden = false;
    },
  };
}
