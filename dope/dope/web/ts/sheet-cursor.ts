// The sheet cursor: one active-cell selection for every editable grid — ЭК's
// бой and stage tables, КСИ's answers, брейн's protocols laid side by side, ОД's
// entry grid. A page describes its grid (rows, the columns each row has, how a
// DOM cell maps to a coordinate and back) and how to apply values; the cursor
// owns the rest: click/shift/drag ranges, arrow keys with clamping (rows may be
// ragged, so moving down into a shorter row clamps the column), Tab, Home/End,
// mark keys and Delete on the whole selection, copy and paste as a tab-separated
// grid, the touch tap-cycle, the active cell and its row highlight.
//
// The geometry, the key interpretation and the clipboard grammar are pure
// (sheetModel, keyAction, parseClipboardGrid, the mark tokens) and tested
// without a DOM; createSheetCursor binds them to one root element.

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

// ---- marks: the one value space for ЭК, КСИ and брейн cells ------------------

export type Mark = "right" | "wrong" | "";

// Every spelling a pasted or typed mark may take, across the pages: +/−, 1/0,
// the Latin and Cyrillic q/w keys, the words, and the п/м the ЭК sheets used.
const RIGHT_TOKENS = ["+", "1", "right", "y", "yes", "✓", "v", "да", "п", "п.", "q", "й"];
const WRONG_TOKENS = ["-", "−", "0", "wrong", "n", "no", "x", "✗", "нет", "м", "м.", "w", "ц"];

export function parseMark(text: unknown): Mark {
  const value = String(text ?? "").trim().toLowerCase();
  if (RIGHT_TOKENS.includes(value)) return "right";
  if (WRONG_TOKENS.includes(value)) return "wrong";
  return "";
}

// markOf reads a cell's mark off its class; serializeMark is what a copy of it
// carries: "+", "-" or nothing.
export function markOf(cell: Element | null | undefined): Mark {
  if (cell?.classList.contains("right")) return "right";
  if (cell?.classList.contains("wrong")) return "wrong";
  return "";
}

export function serializeMark(cell: Element | null | undefined): string {
  const mark = markOf(cell);
  return mark === "right" ? "+" : mark === "wrong" ? "-" : "";
}

// A touch tap cycles empty → right → wrong → empty.
export function cycleMark(cell: Element): Mark {
  const mark = markOf(cell);
  return mark === "" ? "right" : mark === "right" ? "wrong" : "";
}

// markKey maps a keydown to the mark it sets, or null when the key is not a
// mark key. q/й and w/ц are the two home-row keys under either layout.
export function markKey(event: {key: string; code?: string}): Mark | null {
  const key = event.key.toLowerCase();
  if (key === "q" || key === "й" || key === "+" || key === "1" || event.code === "NumpadAdd") return "right";
  if (key === "w" || key === "ц" || key === "-" || key === "2" || event.code === "NumpadSubtract") return "wrong";
  return null;
}

// ---- geometry ----------------------------------------------------------------

// A sheet may be ragged either way: ЭК's stage sheet stacks бои with different
// shootout counts (a row's column count varies), брейн lays бои side by side
// with different question counts (a column's row count varies). Each axis is
// asked given the other; a rectangular sheet ignores the argument.
export interface SheetGeometry {
  rows: (col: number) => number;
  cols: (row: number) => number;
}

export interface SheetModel {
  clamp(coord: CellCoord): CellCoord | null;
  move(from: CellCoord, dRow: number, dCol: number): CellCoord | null;
  rect(anchor: CellCoord, focus: CellCoord): CellRect;
}

export function sheetModel(geometry: SheetGeometry): SheetModel {
  const within = (n: number, count: number) => Math.min(Math.max(n, 0), count - 1);
  // Clamp the column against the row we came from, the row against that
  // column, then the column once more against the row we landed in.
  function clampFrom(coord: CellCoord, fromRow: number): CellCoord | null {
    const cols0 = geometry.cols(within(fromRow, Math.max(1, geometry.rows(0))));
    if (cols0 <= 0) return null;
    const col0 = within(coord.col, cols0);
    const rows = geometry.rows(col0);
    if (rows <= 0) return null;
    const row = within(coord.row, rows);
    const cols = geometry.cols(row);
    if (cols <= 0) return null;
    return {row, col: within(coord.col, cols)};
  }
  return {
    clamp: (coord) => clampFrom(coord, coord.row),
    move: (from, dRow, dCol) => clampFrom({row: from.row + dRow, col: from.col + dCol}, from.row),
    rect: (anchor, focus) => ({
      rowStart: Math.min(anchor.row, focus.row),
      rowEnd: Math.max(anchor.row, focus.row),
      colStart: Math.min(anchor.col, focus.col),
      colEnd: Math.max(anchor.col, focus.col),
    }),
  };
}

// ---- keys --------------------------------------------------------------------

export type KeyAction =
  | {kind: "move"; dRow: number; dCol: number; extend: boolean}
  | {kind: "home"; extend: boolean}
  | {kind: "end"; extend: boolean}
  | {kind: "tab"; dCol: number}
  | {kind: "mark"; mark: Mark}
  | {kind: "clear"}
  | {kind: "edit"; text: string | null};

export interface KeyLike {
  key: string;
  code?: string;
  shiftKey?: boolean;
  metaKey?: boolean;
  ctrlKey?: boolean;
  altKey?: boolean;
}

// keyAction reads a keydown as a cursor action. Arrows (Shift extends), Home
// and End move; Backspace/Delete/Space clear the selection. In "marks" sheets
// the mark keys set values. In "text" sheets Tab steps a column (the browser's
// focus order would leave the highlight behind), Enter/F2 open the cell's
// editor and any printable character opens it with that character typed.
// null: not ours.
export function keyAction(event: KeyLike, values: "marks" | "text"): KeyAction | null {
  const shift = Boolean(event.shiftKey);
  switch (event.key) {
    case "ArrowLeft": return {kind: "move", dRow: 0, dCol: -1, extend: shift};
    case "ArrowRight": return {kind: "move", dRow: 0, dCol: 1, extend: shift};
    case "ArrowUp": return {kind: "move", dRow: -1, dCol: 0, extend: shift};
    case "ArrowDown": return {kind: "move", dRow: 1, dCol: 0, extend: shift};
    case "Home": return {kind: "home", extend: shift};
    case "End": return {kind: "end", extend: shift};
    case "Backspace":
    case "Delete":
    case " ": return {kind: "clear"};
  }
  if (values === "marks") {
    const mark = markKey(event);
    return mark === null ? null : {kind: "mark", mark};
  }
  if (event.key === "Tab") return {kind: "tab", dCol: shift ? -1 : 1};
  if (event.key === "Enter" || event.key === "F2") return {kind: "edit", text: null};
  if (!event.metaKey && !event.ctrlKey && !event.altKey && event.key.length === 1) return {kind: "edit", text: event.key};
  return null;
}

// ---- clipboard ---------------------------------------------------------------

// parseClipboardGrid splits pasted text into rows of tab-separated cells; a
// trailing newline (every spreadsheet adds one) is not an empty last row.
export function parseClipboardGrid(text: string): string[][] {
  const lines = text.replace(/\r\n/g, "\n").replace(/\r/g, "\n").split("\n");
  if (lines.length > 1 && lines[lines.length - 1] === "") lines.pop();
  return lines.map((line) => line.split("\t"));
}

export function serializeGrid(rows: string[][]): string {
  return rows.map((cols) => cols.join("\t")).join("\n");
}

// ---- the DOM binding ---------------------------------------------------------

// The classes the cursor paints; "" leaves one out (ОД has no anchor mark
// and highlights rows itself).
export interface SheetCursorClasses {
  selected: string;
  anchor: string;
  top: string;
  bottom: string;
  left: string;
  right: string;
  active: string;
  row: string;
}

export interface SheetSpec extends SheetGeometry {
  root: HTMLElement;
  cellSelector?: string;
  // Whether the sheet takes input at all right now (a spectator, a finished
  // бой, another tab in front). Reads still work: the cursor can be shown.
  readonly?: () => boolean;
  // Whether keys typed anywhere on the page belong to this sheet: a page with
  // several sheets or tabs says which one is in front.
  active?: () => boolean;
  coordOf: (cell: Element) => CellCoord | null | undefined;
  cellAt: (coord: CellCoord) => HTMLElement | null | undefined;
  // "marks": cells hold right/wrong/empty and the mark keys write them.
  // "text": cells hold text a page-side editor edits (onEdit).
  values: "marks" | "text";
  serialize?: (cell: Element) => string;
  parse?: (text: string) => unknown;
  // The value a touch tap advances a cell to; null disables the tap-cycle.
  cycle?: ((cell: Element) => unknown) | null;
  applyValues: (edits: CellEdit[]) => void;
  // "text" sheets: open the cell's editor, with `text` already typed or none.
  onEdit?: (cell: HTMLElement, text: string | null) => void;
  // A page's first look at a key, before the cursor's own reading; return true
  // to say it was handled (ОД sends ArrowUp on the top row to the column's tick).
  onKey?: (event: KeyboardEvent, focus: CellCoord) => boolean;
  onActive?: (cell: HTMLElement | null, coord: CellCoord | null) => void;
  onSelectionChange?: (selection: {anchor: CellCoord; focus: CellCoord; rect: CellRect} | null) => void;
  // The row elements the active cell's row highlight applies to; default is
  // the cell's own <tr> (ЭК's two-row table has two per team).
  rowsOf?: (cell: HTMLElement) => Element[];
  // Where the cursor listens for keys: the document (default) or the root.
  keyTarget?: "document" | "root";
  classes?: Partial<SheetCursorClasses>;
}

export interface SheetCursor {
  bind(): void;
  unbind(): void;
  // select(coord) collapses to one cell; select(anchor, focus) is a range;
  // select(null) clears.
  select(anchor: CellCoord | null, focus?: CellCoord | null, opts?: {focus?: boolean; preventScroll?: boolean}): void;
  moveBy(dRow: number, dCol: number, extend?: boolean): void;
  clear(): void;
  // Re-apply the selection and highlight after the page rebuilt its cells.
  refresh(): void;
  selectedCells(): HTMLElement[];
  readonly anchor: CellCoord | null;
  readonly focus: CellCoord | null;
  readonly rect: CellRect | null;
  readonly activeCell: HTMLElement | null;
}

const DEFAULT_CLASSES: SheetCursorClasses = {
  selected: "cell-selected",
  anchor: "cell-selection-anchor",
  top: "cell-selection-top",
  bottom: "cell-selection-bottom",
  left: "cell-selection-left",
  right: "cell-selection-right",
  active: "active",
  row: "active-team-row",
};

const TAP_MOVE_TOLERANCE = 10;

export function createSheetCursor(spec: SheetSpec): SheetCursor {
  const {
    root,
    cellSelector = ".answer-cell",
    readonly = () => false,
    active = () => true,
    coordOf,
    cellAt,
    values,
    applyValues,
  } = spec;
  const model = sheetModel(spec);
  const cls: SheetCursorClasses = {...DEFAULT_CLASSES, ...(spec.classes || {})};
  const selectionClasses = [cls.selected, cls.anchor, cls.top, cls.bottom, cls.left, cls.right].filter(Boolean);
  const mark = (node: Element, name: string) => { if (name) node.classList.add(name); };
  const serialize = spec.serialize || (values === "marks" ? serializeMark : (cell: Element) => (cell.textContent || "").trim());
  const parse = spec.parse || (values === "marks" ? parseMark : (text: string) => String(text || "").trim());
  const cycle = spec.cycle === undefined ? (values === "marks" ? cycleMark : null) : spec.cycle;
  const rowsOf = spec.rowsOf || ((cell: HTMLElement) => {
    const row = cell.closest("tr");
    return row ? [row] : [];
  });

  let anchor: CellCoord | null = null;
  let focusCoord: CellCoord | null = null;
  let activeNode: HTMLElement | null = null;
  let highlightedRows: Element[] = [];
  let dragState: {anchor: CellCoord; focus: CellCoord} | null = null;
  let suppressNextClick = false;
  let tapStart: {cell: Element; x: number; y: number} | null = null;

  function at(coord: CellCoord | null): HTMLElement | null {
    return coord ? cellAt(coord) || null : null;
  }

  function rect(): CellRect | null {
    return anchor && focusCoord ? model.rect(anchor, focusCoord) : null;
  }

  function cellsIn(r: CellRect): HTMLElement[] {
    const out: HTMLElement[] = [];
    for (let row = r.rowStart; row <= r.rowEnd; row++) {
      for (let col = r.colStart; col <= r.colEnd; col++) {
        const cell = at({row, col});
        if (cell) out.push(cell);
      }
    }
    return out;
  }

  function selectedCells(): HTMLElement[] {
    const r = rect();
    return r ? cellsIn(r) : [];
  }

  function clearClasses(): void {
    root.querySelectorAll(selectionClasses.map((name) => `${cellSelector}.${name}`).join(", ")).forEach((cell) => {
      cell.classList.remove(...selectionClasses);
    });
  }

  function paint(): void {
    clearClasses();
    const r = rect();
    if (r) {
      for (let row = r.rowStart; row <= r.rowEnd; row++) {
        for (let col = r.colStart; col <= r.colEnd; col++) {
          const cell = at({row, col});
          if (!cell) continue;
          mark(cell, cls.selected);
          if (row === r.rowStart) mark(cell, cls.top);
          if (row === r.rowEnd) mark(cell, cls.bottom);
          if (col === r.colStart) mark(cell, cls.left);
          if (col === r.colEnd) mark(cell, cls.right);
        }
      }
      const anchorCell = at(anchor);
      if (anchorCell) mark(anchorCell, cls.anchor);
    }
    paintActive(at(focusCoord));
  }

  function paintActive(cell: HTMLElement | null): void {
    if (cls.active) {
      // A rebuilt table leaves the old node detached; sweep any stray marker too.
      activeNode?.classList.remove(cls.active);
      root.querySelectorAll(`${cellSelector}.${cls.active}`).forEach((node) => node.classList.remove(cls.active));
    }
    if (cls.row) {
      for (const row of highlightedRows) row.classList.remove(cls.row);
      root.querySelectorAll(`.${cls.row}`).forEach((row) => row.classList.remove(cls.row));
    }
    highlightedRows = [];
    activeNode = cell;
    if (!cell || readonly()) return;
    if (cls.active) cell.classList.add(cls.active);
    if (cls.row) {
      highlightedRows = rowsOf(cell);
      for (const row of highlightedRows) row.classList.add(cls.row);
    }
  }

  function select(newAnchor: CellCoord | null, newFocus?: CellCoord | null, opts: {focus?: boolean; preventScroll?: boolean} = {}): void {
    anchor = newAnchor ? model.clamp(newAnchor) : null;
    focusCoord = newFocus ? model.clamp(newFocus) : anchor;
    if (!anchor || !focusCoord) {
      anchor = focusCoord = null;
    }
    paint();
    const r = rect();
    spec.onSelectionChange?.(anchor && focusCoord && r ? {anchor, focus: focusCoord, rect: r} : null);
    const focusCell = at(focusCoord);
    if (focusCell && opts.focus !== false) focusCell.focus({preventScroll: opts.preventScroll});
    spec.onActive?.(focusCell, focusCoord);
  }

  function moveBy(dRow: number, dCol: number, extend = false): void {
    const from = focusCoord;
    if (!from) {
      select(model.clamp({row: 0, col: 0}));
      return;
    }
    const next = model.move(from, dRow, dCol);
    if (!next) return;
    select(extend ? anchor || from : next, next);
  }

  function clear(): void {
    anchor = focusCoord = null;
    clearClasses();
    paintActive(null);
    spec.onSelectionChange?.(null);
  }

  function apply(cells: HTMLElement[], value: unknown): void {
    if (readonly() || cells.length === 0) return;
    applyValues(cells.map((cell) => ({cell, value})));
  }

  function targetCells(): HTMLElement[] {
    const cells = selectedCells();
    if (cells.length > 1) return cells;
    const one = at(focusCoord);
    return one ? [one] : [];
  }

  function copySelection(event: ClipboardEvent): void {
    const r = rect();
    if (!r) return;
    const rows: string[][] = [];
    for (let row = r.rowStart; row <= r.rowEnd; row++) {
      const cols: string[] = [];
      for (let col = r.colStart; col <= r.colEnd; col++) {
        const cell = at({row, col});
        cols.push(cell ? serialize(cell) : "");
      }
      rows.push(cols);
    }
    event.clipboardData?.setData("text/plain", serializeGrid(rows));
    event.preventDefault();
  }

  function pasteSelection(event: ClipboardEvent): void {
    const r = rect();
    if (readonly() || !r) return;
    const text = event.clipboardData?.getData("text/plain") || "";
    if (!text) return;
    event.preventDefault();
    const grid = parseClipboardGrid(text);
    const edits: CellEdit[] = [];
    let last: CellCoord = {row: r.rowStart, col: r.colStart};
    grid.forEach((cols, rOff) => {
      cols.forEach((text, cOff) => {
        const coord = {row: r.rowStart + rOff, col: r.colStart + cOff};
        const cell = at(coord);
        if (!cell) return;
        edits.push({cell, value: parse(text)});
        last = coord;
      });
    });
    if (edits.length > 0) applyValues(edits);
    select({row: r.rowStart, col: r.colStart}, last, {focus: true});
  }

  function handleKeydown(event: KeyboardEvent): void {
    if (!active() || !focusCoord) return;
    const target = event.target;
    if (isEditableTarget(target)) return;
    if (spec.onKey?.(event, focusCoord)) return;
    const action = keyAction(event, values);
    if (!action) return;
    event.preventDefault();
    switch (action.kind) {
      case "move": moveBy(action.dRow, action.dCol, action.extend); return;
      case "home": moveBy(-Number.MAX_SAFE_INTEGER, 0, action.extend); return;
      case "end": moveBy(Number.MAX_SAFE_INTEGER, 0, action.extend); return;
      case "tab": moveBy(0, action.dCol, false); return;
      case "mark": apply(targetCells(), action.mark); return;
      case "clear": apply(targetCells(), parse("")); return;
      case "edit": {
        const cell = at(focusCoord);
        if (cell && !readonly()) spec.onEdit?.(cell, action.text);
        return;
      }
    }
  }

  function cellOf(target: EventTarget | null): Element | null {
    const cell = target instanceof Element ? target.closest(cellSelector) : null;
    return cell && root.contains(cell) ? cell : null;
  }

  function isEditableTarget(target: EventTarget | null): boolean {
    return target instanceof HTMLInputElement
      || target instanceof HTMLTextAreaElement
      || target instanceof HTMLSelectElement
      || (target instanceof HTMLElement && target.isContentEditable);
  }

  function handleMouseDown(event: MouseEvent): void {
    if (event.button !== 0 || readonly() || isEditableTarget(event.target)) return;
    const cell = cellOf(event.target);
    const coord = cell ? coordOf(cell) : null;
    if (!coord) return;
    event.preventDefault();
    suppressNextClick = Boolean(event.shiftKey && anchor);
    const nextAnchor = event.shiftKey && anchor ? anchor : coord;
    select(nextAnchor, coord, {preventScroll: true});
    dragState = {anchor: nextAnchor, focus: coord};
    document.addEventListener("mouseup", () => { dragState = null; }, {once: true});
  }

  function handleMouseOver(event: MouseEvent): void {
    if (!dragState || readonly()) return;
    const cell = cellOf(event.target);
    const coord = cell ? coordOf(cell) : null;
    if (!coord || (coord.row === dragState.focus.row && coord.col === dragState.focus.col)) return;
    dragState.focus = coord;
    select(dragState.anchor, coord, {focus: false});
  }

  function handleClickCapture(event: MouseEvent): void {
    if (suppressNextClick) {
      suppressNextClick = false;
      event.stopPropagation();
    }
  }

  function handleCopy(event: ClipboardEvent): void {
    if (isEditableTarget(event.target) || !owns(event.target)) return;
    copySelection(event);
  }

  function handlePaste(event: ClipboardEvent): void {
    if (isEditableTarget(event.target) || !owns(event.target)) return;
    pasteSelection(event);
  }

  function owns(target: EventTarget | null): boolean {
    return (target instanceof Node && root.contains(target)) || root.contains(document.activeElement);
  }

  // A touch tap cycles the cell's value — the only way to enter a mark on a
  // phone with no +/− keys. Tracked from pointerdown so a scroll that starts on
  // a cell (moved > tolerance, or lifted elsewhere) leaves the value alone.
  function handlePointerDown(event: PointerEvent): void {
    tapStart = null;
    if (event.pointerType !== "touch" || !cycle || readonly() || isEditableTarget(event.target)) return;
    const cell = cellOf(event.target);
    if (cell) tapStart = {cell, x: event.clientX, y: event.clientY};
  }

  function handlePointerUp(event: PointerEvent): void {
    const start = tapStart;
    tapStart = null;
    if (event.pointerType !== "touch" || !cycle || !start || readonly()) return;
    const cell = cellOf(event.target);
    if (!cell || cell !== start.cell) return;
    if (Math.abs(event.clientX - start.x) > TAP_MOVE_TOLERANCE || Math.abs(event.clientY - start.y) > TAP_MOVE_TOLERANCE) return;
    const value = cycle(cell);
    if (value === undefined || value === null) return;
    applyValues([{cell, value}]);
    const coord = coordOf(cell);
    if (coord) select(coord, coord, {focus: false});
  }

  function handlePointerCancel(): void {
    tapStart = null;
  }

  const keyTarget = () => (spec.keyTarget === "root" ? root : document);

  function bind(): void {
    root.addEventListener("mousedown", handleMouseDown);
    root.addEventListener("mouseover", handleMouseOver);
    root.addEventListener("click", handleClickCapture, true);
    root.addEventListener("pointerdown", handlePointerDown);
    root.addEventListener("pointerup", handlePointerUp);
    root.addEventListener("pointercancel", handlePointerCancel);
    keyTarget().addEventListener("keydown", handleKeydown as EventListener);
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
    keyTarget().removeEventListener("keydown", handleKeydown as EventListener);
    document.removeEventListener("copy", handleCopy);
    document.removeEventListener("paste", handlePaste);
  }

  return {
    bind,
    unbind,
    select,
    moveBy,
    clear,
    refresh: paint,
    selectedCells,
    get anchor() { return anchor; },
    get focus() { return focusCoord; },
    get rect() { return rect(); },
    get activeCell() { return activeNode; },
  };
}
