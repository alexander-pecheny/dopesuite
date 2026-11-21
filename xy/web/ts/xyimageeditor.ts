// xyimageeditor.ts — the Edited Image editor (ADR-0024), built like Figma: an
// infinite canvas in millimetres with one frame on it, layers on the left, the
// selection's properties on the right, tools on top. Whatever lies inside the
// frame is what gets baked. createEditor mounts one into a root element; the
// board opens it over a card (attachments.ts), /lab/xyimage on a page of its own.
//
// Zoom is a CSS transform over a world laid out at a fixed BASE resolution, so
// zooming never re-lays the text out and a line breaks in the same place at
// every zoom. Freezing (xyimagefreeze.ts) reads that layout back in screen
// pixels and divides by the zoom.
//
// Saving writes a .xyimage (xyimagefile.ts): the Recipe the editor reopens, the
// pictures as added, the frame's layers in stacking order (pictures, and runs
// of black boxes and text between them as vector overlays) and a raster baked
// here from the same overlays. The PDF button has typst place the same layers.
import { xyApp } from "./app.js";
import S from "./i18nstrings.js";
import { icon } from "./icons_gen.js";
import type { IconName } from "./icons_gen.js";
import { xyTypo } from "./typo.js";
import { chgk4s } from "./chgk/chgk.js";
import { PT_MM, freezeBox, overlaySVG } from "./xyimagefreeze.js";
import type { Align, FrozenBox, Layer, Rect, TextStyle, VAlign } from "./xyimagefreeze.js";
import { encodeXyimage, parseXyimage } from "./xyimagefile.js";
import { faceRef, loadFace } from "./xyimagefonts.js";
import type { FaceRef, FontFamily, FontSource } from "./xyimagefonts.js";
import { NOTO_FAMILY, fontPicker, matchFace } from "./xyimagefontpicker.js";
import { GRID_STEP_RANGE, MIN_DISTRIBUTE, align, bounds, distribute, loadGrid, saveGrid, snapPoint, snapShift } from "./xyimagearrange.js";
import type { AlignMode, Axis, Box } from "./xyimagearrange.js";

const { el, errMsg, downloadBlob } = xyApp;

// ---- constants ----

const BASE = 10; // layout px per mm, before the zoom transform
const REAL = 96 / 25.4; // screen px per mm at 100%
const MM_PER_IN = 25.4;
const FONT_ALIAS = "XyimgNoto";
const TYPST_FAMILY = NOTO_FAMILY;
const FACES = [
  { file: "NotoSans-Regular.ttf", weight: "400", style: "normal" },
  { file: "NotoSans-Bold.ttf", weight: "700", style: "normal" },
  { file: "NotoSans-Italic.ttf", weight: "400", style: "italic" },
  { file: "NotoSans-BoldItalic.ttf", weight: "700", style: "italic" },
];
const ZOOM_MIN = 0.05;
const ZOOM_MAX = 400;
const WHEEL_ZOOM = 0.01;
const KEY_ZOOM = 1.25;
const FIT_PAD = 48;
const MIN_MM = 1;
const CLICK_MM = 1.5; // a create-drag shorter than this is a click
const NEW_TEXT = { w: 80, h: 20 };
const NEW_RECT = { w: 30, h: 15 };
const NUDGE = 1;
const NUDGE_BIG = 10;
const BASE64_CHUNK = 0x8000;
const DEFAULT_DPI = 300;
const MAX_COMPARE_DPI = 600;
const LABEL_CHARS = 24;
const NEW_FRAME = { w: 160, h: 100 }; // mm
const DEFAULT_SIZE = 14; // pt
const DEFAULT_LEADING = 1.25;
const MIN_LEADING = 0.5;
const DPI_RANGE = { min: 72, max: 1200 };
const PERCENT = 100;
const HEX_COLOUR = /^#[0-9a-fA-F]{6}$/;
const TENTHS = 10;
const WEIGHT = { regular: 400, bold: 700 };
// A new frame's background, the colour of the paper a handout goes on.
// sloplint: ignore[hardcoded-color] the default of a user's colour field
const WHITE = "#ffffff";
// The Recipe's own version (ADR-0024): bumped on a change of meaning only.
const RECIPE_VERSION = 1;
// Grid lines closer together on screen than this are not drawn.
const MIN_GRID_PX = 4;

// ---- model ----

type Tool = "move" | "text" | "rect";
interface Frame extends Rect { bg: string; dpi: number }
interface TextItem extends Rect { id: number; kind: "text"; text: string; style: TextStyle }
interface RectItem extends Rect { id: number; kind: "rect" }
interface ImageItem extends Rect { id: number; kind: "image"; name: string; bytes: Blob; ext: "png" | "jpg"; url: string; ratio: number }
type Item = TextItem | RectItem | ImageItem;
type Sel = number | "frame" | null;

export interface EditorOptions {
  // What the editor opens on: an Edited Image to reopen, a picture to start
  // from, or nothing for a blank frame.
  file?: Uint8Array;
  picture?: { blob: Blob; name: string };
  // The uploaded Font Families on offer (ADR-0025): the board's, or, on the
  // lab page, whatever is uploaded while it is open.
  fonts?: FontSource;
  // Present when the editor can save (the board): the Save and Close buttons.
  onSave?(bytes: Uint8Array): Promise<void>;
  onClose?(): void;
}

export interface XyEditor {
  // dirty reports whether anything changed since it opened or was last saved.
  dirty(): boolean;
  // retarget moves every box set in a Family of this name, other than fam
  // itself, onto fam (ADR-0025: only the newest Family of a name is live),
  // and reports how many it moved.
  retarget(fam: FontFamily): Promise<number>;
  // bytes is the .xyimage as it stands, baked afresh.
  bytes(): Promise<Uint8Array>;
  destroy(): void;
}

// The faces are fetched once per page: the exact Noto Sans files typst sets
// text in, not the Kit's variable woff2 of the same name.
const faceBytes = new Map<string, ArrayBuffer>();
let fontsLoaded: Promise<void> | null = null;

function loadFonts(): Promise<void> {
  fontsLoaded ??= Promise.all(FACES.map(async (f) => {
    const res = await fetch(`/xyimage/fonts/${f.file}`);
    if (!res.ok) throw new Error(`font ${f.file}: HTTP ${res.status}`);
    const buf = await res.arrayBuffer();
    faceBytes.set(f.file, buf);
    const face = new FontFace(FONT_ALIAS, buf.slice(0), { weight: f.weight, style: f.style });
    document.fonts.add(await face.load());
  })).then(() => undefined);
  return fontsLoaded;
}

export async function createEditor(root: HTMLElement, opts: EditorOptions = {}): Promise<XyEditor> {
  await loadFonts();
  const ac = new AbortController();
  const signal = ac.signal;



  const state = {
    frame: { x: 0, y: 0, ...NEW_FRAME, bg: WHITE, dpi: DEFAULT_DPI } as Frame,
    items: [] as Item[],
    sel: null as Sel,
    // Every item selected, the primary (sel) among them: several are picked
    // with Shift or Cmd/Ctrl and then aligned, moved or removed together.
    picked: new Set<number>(),
    tool: "move" as Tool,
    editing: null as number | null,
    zoom: REAL, // screen px per mm; fitFrame sets it at boot
    panX: 0, // screen px of the world origin, inside the view
    panY: 0,
    space: false,
    comparing: false,
    nextId: 1,
    grid: loadGrid(),
  };

  // The Faces a text box names that could not be loaded: its Family was
  // deleted or replaced. Such a box shows in Noto Sans, and cannot be saved.
  const missingFaces = new Set<string>();
  const picker = fontPicker({
    fonts: opts.fonts, missing: missingFaces,
    render: () => render(), renderProps: () => renderProps(),
    say: (text) => { status.textContent = text; },
    button: (title, onClick) => barBtn("file-up", null, title, onClick),
    uploaded: async (fam) => { if (await retarget(fam)) renderProps(); },
  });
  const fonts = picker.fonts;

  const defaultStyle: TextStyle = {
    size: DEFAULT_SIZE, leading: DEFAULT_LEADING, tracking: 0, align: "center", valign: "middle", bold: false, italic: false, hyphens: false,
  };


  // ---- DOM skeleton ----

  function toolBtn(tool: Tool, ico: IconName, title: string): HTMLElement {
    const b = el("button", { class: "xyed-tool", type: "button", title, "aria-label": title, dataset: { tool } }, icon(ico));
    b.addEventListener("click", () => setTool(tool));
    return b;
  }

  function barBtn(ico: IconName | null, label: string | null, title: string, onClick: () => void): HTMLElement {
    const b = el("button", { class: label ? "btn btn-ghost xyed-barbtn" : "xyed-tool", type: "button", title, "aria-label": title },
      ico ? icon(ico) : null, label);
    b.addEventListener("click", onClick);
    return b;
  }

  const fileInput = el("input", { type: "file", accept: "image/png,image/jpeg,image/webp", hidden: true }) as HTMLInputElement;
  const zoomLabel = el("button", { class: "btn btn-ghost xyed-zoom", type: "button", title: S.xyimage.tools.fit() });
  const compareBtn = barBtn("eye", null, S.xyimage.tools.compare(), () => { void toggleCompare(); });
  const status = el("span", { class: "xyed-status" });
  const gridBtn = barBtn("grid-3x3", null, S.xyimage.tools.grid(), () => setGrid({ show: !state.grid.show }));
  const snapBtn = barBtn("magnet", null, S.xyimage.tools.snap(), () => setGrid({ snap: !state.grid.snap }));
  const bar = el("div", { class: "xyed-bar" },
    toolBtn("move", "maximize-2", S.xyimage.tools.move()),
    toolBtn("text", "text-cursor-input", S.xyimage.tools.text()),
    toolBtn("rect", "square", S.xyimage.tools.rect()),
    barBtn("image", null, S.xyimage.tools.picture(), () => fileInput.click()),
    barBtn("wand-sparkles", null, S.xyimage.tools.typo(), typographAll),
    gridBtn,
    snapBtn,
    el("span", { class: "xyed-spacer" }),
    status,
    zoomLabel,
    compareBtn,
    barBtn("file-down", S.xyimage.tools.pdf(), S.xyimage.tools.pdf(), () => { void exportPDF(); }),
    barBtn("file-down", S.xyimage.tools.png(), S.xyimage.tools.png(), () => { void exportPNG(); }),
    fileInput);
  zoomLabel.addEventListener("click", fitFrame);

  const layersList = el("div", { class: "u-col" });
  const layers = el("aside", { class: "xyed-layers" },
    el("div", { class: "xyed-panelhead u-clip-fade", text: S.xyimage.layers.title() }),
    layersList,
    el("p", { class: "xyed-help", text: S.xyimage.layers.help() }));

  const props = el("aside", { class: "xyed-props" });

  const world = el("div", { class: "xyed-world" });
  const frameBg = el("div", { class: "xyed-framebg", dataset: { frame: "1" } });
  const compareImg = el("img", { class: "xyed-compare", alt: "", hidden: true }) as HTMLImageElement;
  world.append(frameBg);
  const mask = el("div", { class: "xyed-mask" });
  const frameLabel = el("div", { class: "xyed-framelabel", dataset: { frame: "1" } });
  const selBox = el("div", { class: "xyed-selbox", hidden: true });
  const HANDLES = ["nw", "n", "ne", "e", "se", "s", "sw", "w"] as const;
  type Handle = typeof HANDLES[number];
  const handleEls = new Map<Handle, HTMLElement>();
  for (const h of HANDLES) {
    const node = el("div", { class: `xyed-handle xyed-h-${h}`, dataset: { handle: h } });
    handleEls.set(h, node);
    selBox.append(node);
  }
  const gridEl = el("div", { class: "xyed-grid", hidden: true });
  const pickBoxes = el("div");
  const view = el("div", { class: "xyed-view", tabindex: "0" }, world, mask, gridEl, frameLabel, pickBoxes, selBox);

  root.classList.add("xyimage-editor");
  root.append(bar, layers, view, props);

  const itemEls = new Map<number, HTMLElement>();
  const bodies = new Map<number, HTMLElement>();

  // ---- coordinates ----

  function viewRect(): DOMRect { return view.getBoundingClientRect(); }

  function toWorld(clientX: number, clientY: number): { x: number; y: number } {
    const r = viewRect();
    return { x: (clientX - r.left - state.panX) / state.zoom, y: (clientY - r.top - state.panY) / state.zoom };
  }

  function toScreen(x: number, y: number): { x: number; y: number } {
    return { x: state.panX + x * state.zoom, y: state.panY + y * state.zoom };
  }

  function base(mm: number): string { return `${mm * BASE}px`; }

  function placeBase(node: HTMLElement, r: Rect): void {
    node.style.left = base(r.x);
    node.style.top = base(r.y);
    node.style.width = base(r.w);
    node.style.height = base(r.h);
  }

  function placeScreen(node: HTMLElement, r: Rect): void {
    const p = toScreen(r.x, r.y);
    node.style.left = `${p.x}px`;
    node.style.top = `${p.y}px`;
    node.style.width = `${r.w * state.zoom}px`;
    node.style.height = `${r.h * state.zoom}px`;
  }

  // ---- items ----

  function itemById(id: number): Item | undefined { return state.items.find((i) => i.id === id); }

  // pickedItems is every selected item, in stacking order.
  function pickedItems(): Item[] {
    if (typeof state.sel !== "number") return [];
    const ids = state.picked.has(state.sel) ? state.picked : new Set([state.sel]);
    return state.items.filter((i) => ids.has(i.id));
  }

  function selRect(): (Rect & { kind: string }) | null {
    if (state.sel === "frame") return { ...state.frame, kind: "frame" };
    const items = pickedItems();
    if (items.length > 1) return { ...bounds(items), kind: "many" };
    return items[0] ?? null;
  }

  const VALIGN_FLEX: Record<VAlign, string> = { top: "flex-start", middle: "center", bottom: "flex-end" };

  function cssFamily(s: TextStyle): string {
    return s.font && !missingFaces.has(s.font.family) ? `"${s.font.family}"` : `"${FONT_ALIAS}"`;
  }

  function styleBody(body: HTMLElement, s: TextStyle): void {
    body.style.fontFamily = cssFamily(s);
    body.style.fontSize = `${s.size * PT_MM * BASE}px`;
    body.style.lineHeight = String(s.leading);
    body.style.letterSpacing = `${s.tracking * PT_MM * BASE}px`;
    body.style.textAlign = s.align;
    body.style.fontWeight = !s.font && s.bold ? "700" : "400";
    body.style.fontStyle = !s.font && s.italic ? "italic" : "normal";
    body.style.hyphens = s.hyphens ? "auto" : "manual";
  }

  function makeItemEl(it: Item): HTMLElement {
    const node = el("div", { class: `xyed-item xyed-${it.kind}${it.kind === "text" ? " u-col" : ""}`, dataset: { item: String(it.id) } });
    if (it.kind === "image") node.append(el("img", { src: it.url, alt: "", draggable: "false" }));
    if (it.kind === "text") {
      const body = el("div", { class: "xyed-body", lang: "ru", spellcheck: "false" });
      body.textContent = it.text;
      body.addEventListener("input", () => { it.text = body.innerText; renderLayers(); });
      body.addEventListener("blur", () => stopEditing());
      bodies.set(it.id, body);
      node.append(body);
    }
    return node;
  }

  // syncItems makes the world's children match state.items, in stacking order.
  function syncItems(): void {
    const live = new Set(state.items.map((i) => i.id));
    for (const [id, node] of itemEls) {
      if (!live.has(id)) { node.remove(); itemEls.delete(id); bodies.delete(id); }
    }
    let prev: Element = frameBg;
    for (const it of state.items) {
      let node = itemEls.get(it.id);
      if (!node) { node = makeItemEl(it); itemEls.set(it.id, node); }
      if (prev.nextSibling !== node) prev.after(node);
      prev = node;
    }
    world.append(compareImg);
  }

  // ---- rendering ----

  function render(): void {
    world.style.transform = `translate(${state.panX}px, ${state.panY}px) scale(${state.zoom / BASE})`;
    const f = state.frame;
    placeBase(frameBg, f);
    frameBg.style.background = f.bg;
    placeBase(compareImg, f);
    placeScreen(mask, f);
    const fl = toScreen(f.x, f.y);
    frameLabel.style.left = `${fl.x}px`;
    frameLabel.style.top = `${fl.y}px`;
    frameLabel.textContent = `${S.xyimage.layers.frame()}  ${round1(f.w)} × ${round1(f.h)}`;
    frameLabel.classList.toggle("xyed-on", state.sel === "frame");
    for (const it of state.items) {
      const node = itemEls.get(it.id)!;
      placeBase(node, it);
      if (it.kind === "text") {
        node.style.justifyContent = VALIGN_FLEX[it.style.valign];
        styleBody(bodies.get(it.id)!, it.style);
        node.classList.toggle("xyed-editing", state.editing === it.id);
      }
    }
    renderSelection();
    renderGrid();
    zoomLabel.textContent = `${Math.round(state.zoom / REAL * PERCENT)}%`;
    for (const b of bar.querySelectorAll<HTMLElement>("[data-tool]")) b.classList.toggle("xyed-on", b.dataset.tool === state.tool);
    compareBtn.classList.toggle("xyed-on", state.comparing);
    gridBtn.classList.toggle("xyed-on", state.grid.show);
    snapBtn.classList.toggle("xyed-on", state.grid.snap);
    view.dataset.tool = state.space ? "pan" : state.tool;
    syncPropValues();
  }

  function renderSelection(): void {
    const r = selRect();
    selBox.hidden = !r || state.comparing;
    if (!r) return;
    placeScreen(selBox, r);
    const cornersOnly = r.kind === "image";
    for (const [h, node] of handleEls) node.hidden = state.editing !== null || r.kind === "many" || (cornersOnly && h.length === 1);
    const many = pickedItems();
    pickBoxes.replaceChildren(...(many.length > 1 && !state.comparing ? many.map((it) => {
      const node = el("div", { class: "xyed-pickbox" });
      placeScreen(node, it);
      return node;
    }) : []));
  }

  // renderGrid draws the grid over the frame, its lines from the frame's
  // corner; it is the editor's, and never baked.
  function renderGrid(): void {
    const px = state.grid.step * state.zoom;
    gridEl.hidden = !state.grid.show || state.comparing || px < MIN_GRID_PX;
    if (gridEl.hidden) return;
    placeScreen(gridEl, state.frame);
    gridEl.style.backgroundSize = `${px}px ${px}px`;
  }

  function setGrid(change: Partial<typeof state.grid>): void {
    Object.assign(state.grid, change);
    saveGrid(state.grid);
    render();
  }

  function round1(v: number): string { return String(Math.round(v * TENTHS) / TENTHS); }

  // ---- layers panel ----

  function itemLabel(it: Item): string {
    if (it.kind === "text") return it.text.replace(/\s+/g, " ").trim().slice(0, LABEL_CHARS) || S.xyimage.layers.text();
    if (it.kind === "rect") return S.xyimage.layers.rect();
    return it.name || S.xyimage.layers.picture();
  }

  const KIND_ICON: Record<Item["kind"], IconName> = { text: "text-cursor-input", rect: "square", image: "image" };

  function renderLayers(): void {
    const rows: HTMLElement[] = [];
    const frameRow = el("button", { class: "xyed-layer", type: "button" }, icon("columns-3"), el("span", { class: "u-clip-fade", text: S.xyimage.layers.frame() }));
    frameRow.classList.toggle("xyed-on", state.sel === "frame");
    frameRow.addEventListener("click", () => select("frame"));
    rows.push(frameRow);
    for (const it of [...state.items].reverse()) {
      const row = el("button", { class: "xyed-layer xyed-layer-child", type: "button" }, icon(KIND_ICON[it.kind]), el("span", { class: "u-clip-fade", text: itemLabel(it) }));
      row.classList.toggle("xyed-on", pickedItems().includes(it));
      row.addEventListener("click", (e) => { if (addsToSelection(e)) togglePicked(it.id); else select(it.id); });
      rows.push(row);
    }
    layersList.replaceChildren(...rows);
  }

  // ---- properties panel ----

  interface NumProp { input: HTMLInputElement; get: () => number; set: (v: number) => void }
  let numProps: NumProp[] = [];

  function numField(label: string, get: () => number, set: (v: number) => void, step = "0.1"): HTMLElement {
    const input = el("input", { class: "input input-narrow", type: "number", step }) as HTMLInputElement;
    input.addEventListener("input", () => {
      const v = Number(input.value);
      if (input.value !== "" && Number.isFinite(v)) { set(v); render(); renderLayers(); }
    });
    numProps.push({ input, get, set });
    return el("label", { class: "field" }, el("span", { text: label }), input);
  }

  function syncPropValues(): void {
    for (const p of numProps) {
      if (document.activeElement === p.input) continue;
      p.input.value = round1(p.get());
    }
  }

  function choice<T extends string>(options: ReadonlyArray<[T, string]>, get: () => T, set: (v: T) => void): HTMLElement {
    const select = el("select", { class: "input" }, options.map(([v, label]) => el("option", { value: v }, label))) as HTMLSelectElement;
    select.value = get();
    select.addEventListener("change", () => { set(select.value as T); render(); });
    return select;
  }

  function check(label: string, get: () => boolean, set: (v: boolean) => void): HTMLElement {
    const input = el("input", { type: "checkbox" }) as HTMLInputElement;
    input.checked = get();
    input.addEventListener("change", () => { set(input.checked); render(); });
    return el("label", { class: "xyed-check" }, input, label);
  }

  function geometry(r: Rect, keepRatio?: number): HTMLElement {
    return el("div", { class: "xyed-grid2" },
      numField(S.xyimage.props.x(), () => r.x, (v) => { r.x = v; }),
      numField(S.xyimage.props.y(), () => r.y, (v) => { r.y = v; }),
      numField(S.xyimage.props.w(), () => r.w, (v) => { r.w = Math.max(MIN_MM, v); if (keepRatio) r.h = r.w / keepRatio; }),
      numField(S.xyimage.props.h(), () => r.h, (v) => { r.h = Math.max(MIN_MM, v); if (keepRatio) r.w = r.h * keepRatio; }));
  }

  function textProps(it: TextItem): HTMLElement[] {
    const s = it.style;
    return [
      el("div", { class: "xyed-grid2" },
        numField(S.xyimage.props.size(), () => s.size, (v) => { s.size = Math.max(1, v); }, "0.5"),
        numField(S.xyimage.props.leading(), () => s.leading, (v) => { s.leading = Math.max(MIN_LEADING, v); }, "0.05"),
        numField(S.xyimage.props.tracking(), () => s.tracking, (v) => { s.tracking = v; }, "0.1")),
      el("label", { class: "field" }, el("span", { text: S.xyimage.props.align() }),
        choice<Align>([["left", S.xyimage.props.alignLeft()], ["center", S.xyimage.props.alignCenter()], ["right", S.xyimage.props.alignRight()], ["justify", S.xyimage.props.alignJustify()]],
          () => s.align, (v) => { s.align = v; })),
      el("label", { class: "field" }, el("span", { text: S.xyimage.props.valign() }),
        choice<VAlign>([["top", S.xyimage.props.valignTop()], ["middle", S.xyimage.props.valignMiddle()], ["bottom", S.xyimage.props.valignBottom()]],
          () => s.valign, (v) => { s.valign = v; })),
      ...picker.fields(s),
      el("div", { class: "u-row u-wrap u-gap-sm" },
        check(S.xyimage.props.hyphens(), () => s.hyphens, (v) => { s.hyphens = v; })),
    ];
  }

  // arrangeButtons lines boxes up: several by their common bounds, one by the
  // frame. Spacing out evenly needs three.
  function arrangeButtons(items: Item[]): HTMLElement {
    const modes: [AlignMode, IconName, string][] = [
      ["left", "align-start-vertical", S.xyimage.arrange.left()],
      ["hcenter", "align-center-vertical", S.xyimage.arrange.hcenter()],
      ["right", "align-end-vertical", S.xyimage.arrange.right()],
      ["top", "align-start-horizontal", S.xyimage.arrange.top()],
      ["vmiddle", "align-center-horizontal", S.xyimage.arrange.vmiddle()],
      ["bottom", "align-end-horizontal", S.xyimage.arrange.bottom()],
    ];
    const to: Box = items.length > 1 ? bounds(items) : state.frame;
    const rows = [el("div", { class: "u-row u-wrap" }, modes.map(([mode, ico, title]) => barBtn(ico, null, title, () => { align(items, mode, to); render(); })))];
    const spread: [Axis, IconName, string][] = [
      ["x", "align-horizontal-distribute-center", S.xyimage.arrange.distributeX()],
      ["y", "align-vertical-distribute-center", S.xyimage.arrange.distributeY()],
    ];
    if (items.length >= MIN_DISTRIBUTE) rows.push(el("div", { class: "u-row" }, spread.map(([axis, ico, title]) => barBtn(ico, null, title, () => { distribute(items, axis); render(); }))));
    return el("div", { class: "u-col" }, rows);
  }

  function orderButtons(id: number): HTMLElement {
    return el("div", { class: "u-row u-wrap u-gap-sm" },
      barBtn("arrow-up", S.xyimage.layers.up(), S.xyimage.layers.up(), () => restack(id, 1)),
      barBtn("arrow-down", S.xyimage.layers.down(), S.xyimage.layers.down(), () => restack(id, -1)),
      barBtn("trash-2", S.xyimage.layers.remove(), S.xyimage.layers.remove(), removeSelected));
  }

  function renderProps(): void {
    numProps = [];
    const f = state.frame;
    const many = pickedItems();
    const sel = many.length === 1 ? many[0] : null;
    const parts: HTMLElement[] = [];
    if (many.length > 1) {
      parts.push(el("div", { class: "xyed-panelhead u-clip-fade", text: S.xyimage.arrange.picked(String(many.length)) }),
        arrangeButtons(many),
        el("div", { class: "u-row u-wrap u-gap-sm" }, barBtn("trash-2", S.xyimage.layers.remove(), S.xyimage.layers.remove(), removeSelected)));
    } else if (!sel) {
      const bg = el("input", { class: "input", type: "text", maxlength: "7", spellcheck: "false", value: f.bg }) as HTMLInputElement;
      bg.addEventListener("input", () => { if (HEX_COLOUR.test(bg.value)) { f.bg = bg.value; render(); } });
      parts.push(el("div", { class: "xyed-panelhead u-clip-fade", text: S.xyimage.props.frame() }), geometry(f),
        el("div", { class: "xyed-grid2" },
          el("label", { class: "field" }, el("span", { text: S.xyimage.props.background() }), bg),
          numField(S.xyimage.props.dpi(), () => f.dpi, (v) => { f.dpi = Math.min(DPI_RANGE.max, Math.max(DPI_RANGE.min, Math.round(v))); }, "1"),
          numField(S.xyimage.props.gridStep(), () => state.grid.step, (v) => {
            state.grid.step = Math.min(GRID_STEP_RANGE.max, Math.max(GRID_STEP_RANGE.min, v));
            saveGrid(state.grid);
          }, "0.5")));
    } else {
      parts.push(el("div", { class: "xyed-panelhead u-clip-fade", text: itemLabel(sel) }),
        geometry(sel, sel.kind === "image" ? sel.ratio : undefined), arrangeButtons([sel]));
      if (sel.kind === "text") parts.push(...textProps(sel));
      parts.push(orderButtons(sel.id));
    }
    props.replaceChildren(...parts);
    syncPropValues();
  }

  // ---- selection, tools, editing ----

  function select(sel: Sel): void {
    if (state.editing !== null && sel !== state.editing) stopEditing();
    state.sel = sel;
    state.picked = new Set(typeof sel === "number" ? [sel] : []);
    renderLayers();
    renderProps();
    render();
  }

  // addsToSelection is whether a click adds to the selection, or takes away
  // from it, instead of replacing it.
  function addsToSelection(e: MouseEvent): boolean {
    return e.shiftKey || e.metaKey || e.ctrlKey;
  }

  function togglePicked(id: number): void {
    const ids = new Set(pickedItems().map((i) => i.id));
    if (ids.has(id)) ids.delete(id); else ids.add(id);
    const rest = [...ids];
    if (!rest.length) select(null);
    else setPicked(rest, ids.has(id) ? id : rest[rest.length - 1]);
  }

  // setPicked selects several items, `primary` among them.
  function setPicked(ids: number[], primary: number): void {
    select(primary);
    state.picked = new Set(ids);
    renderLayers();
    renderProps();
    render();
  }

  function setTool(tool: Tool): void {
    state.tool = tool;
    render();
  }

  function startEditing(id: number, clientX?: number, clientY?: number): void {
    const body = bodies.get(id);
    if (!body) return;
    state.editing = id;
    state.sel = id;
    state.picked = new Set([id]);
    body.contentEditable = "plaintext-only";
    if (body.contentEditable !== "plaintext-only") body.contentEditable = "true";
    body.focus();
    const sel = document.getSelection();
    const at = clientX !== undefined && clientY !== undefined ? document.caretRangeFromPoint?.(clientX, clientY) : null;
    if (sel && at) { sel.removeAllRanges(); sel.addRange(at); } else sel?.selectAllChildren(body);
    renderLayers();
    renderProps();
    render();
  }

  function stopEditing(): void {
    if (state.editing === null) return;
    const body = bodies.get(state.editing);
    state.editing = null;
    if (body) { body.contentEditable = "false"; body.blur(); }
    render();
  }

  function addItem(it: Item): void {
    state.items.push(it);
    syncItems();
    select(it.id);
  }

  function newText(x: number, y: number, w: number, h: number): TextItem {
    return { id: state.nextId++, kind: "text", text: S.xyimage.props.sample(), style: { ...defaultStyle }, x, y, w, h };
  }

  function removeSelected(): void {
    const gone = new Set(pickedItems());
    if (!gone.size) return;
    for (const it of gone) if (it.kind === "image") URL.revokeObjectURL(it.url);
    stopEditing();
    state.items = state.items.filter((i) => !gone.has(i));
    syncItems();
    select(null);
  }

  // cloneItems puts a copy of each item just above it, as Alt-drag does, and
  // returns the copies.
  function cloneItems(items: Item[]): Item[] {
    const copies: Item[] = [];
    for (const it of items) {
      const copy: Item = { ...it, id: state.nextId++ };
      if (copy.kind === "text") copy.style = { ...copy.style };
      if (copy.kind === "image") copy.url = URL.createObjectURL(copy.bytes);
      state.items.splice(state.items.indexOf(it) + 1, 0, copy);
      copies.push(copy);
    }
    syncItems();
    return copies;
  }

  function restack(id: number, by: number): void {
    const i = state.items.findIndex((it) => it.id === id);
    const j = i + by;
    if (i < 0 || j < 0 || j >= state.items.length) return;
    [state.items[i], state.items[j]] = [state.items[j], state.items[i]];
    syncItems();
    renderLayers();
    render();
  }

  // ---- pictures ----

  function loadImage(url: string): Promise<HTMLImageElement> {
    const img = new Image();
    img.src = url;
    return img.decode().then(() => img);
  }

  function canvasBlob(w: number, h: number, paint: (ctx: CanvasRenderingContext2D) => void | Promise<void>): Promise<Blob> {
    const c = document.createElement("canvas");
    c.width = w;
    c.height = h;
    return Promise.resolve(paint(c.getContext("2d")!)).then(() =>
      new Promise((ok, fail) => c.toBlob((b) => (b ? ok(b) : fail(new Error("toBlob"))), "image/png")));
  }

  async function addPicture(file: Blob, name: string, at?: { x: number; y: number }): Promise<void> {
    const probe = URL.createObjectURL(file);
    try {
      const img = await loadImage(probe);
      let bytes = file;
      let ext: "png" | "jpg" = file.type === "image/jpeg" ? "jpg" : "png";
      if (file.type !== "image/jpeg" && file.type !== "image/png") {
        // typst reads PNG and JPEG for sure; anything else is re-encoded once.
        bytes = await canvasBlob(img.naturalWidth, img.naturalHeight, (ctx) => ctx.drawImage(img, 0, 0));
        ext = "png";
      }
      const ratio = img.naturalWidth / img.naturalHeight;
      // Its size at 96 dpi, but never wider than the frame.
      const w = Math.min(state.frame.w, img.naturalWidth / REAL);
      const h = w / ratio;
      const c = at ?? { x: state.frame.x + state.frame.w / 2, y: state.frame.y + state.frame.h / 2 };
      addItem({ id: state.nextId++, kind: "image", name, bytes, ext, url: URL.createObjectURL(bytes), ratio, x: c.x - w / 2, y: c.y - h / 2, w, h });
    } catch (err) { status.textContent = errMsg(err); } finally { URL.revokeObjectURL(probe); }
  }

  fileInput.addEventListener("change", () => {
    const f = fileInput.files?.[0];
    if (f) void addPicture(f, f.name);
    fileInput.value = "";
  });

  view.addEventListener("dragover", (e) => { e.preventDefault(); });
  view.addEventListener("drop", (e) => {
    e.preventDefault();
    const at = toWorld(e.clientX, e.clientY);
    for (const f of e.dataTransfer?.files ?? []) if (f.type.startsWith("image/")) void addPicture(f, f.name, at);
  });

  document.addEventListener("paste", (e: ClipboardEvent) => {
    if (state.editing !== null || isField(e.target)) return;
    for (const item of e.clipboardData?.items ?? []) {
      const f = item.kind === "file" ? item.getAsFile() : null;
      if (f && f.type.startsWith("image/")) { e.preventDefault(); void addPicture(f, f.name); }
    }
  }, { signal });

  // ---- view: pan and zoom ----

  function zoomAt(factor: number, sx: number, sy: number): void {
    const z = Math.min(ZOOM_MAX, Math.max(ZOOM_MIN, state.zoom * factor));
    const k = z / state.zoom;
    state.panX = sx - (sx - state.panX) * k;
    state.panY = sy - (sy - state.panY) * k;
    state.zoom = z;
    render();
  }

  function fitFrame(): void {
    const r = viewRect();
    const f = state.frame;
    state.zoom = Math.max(ZOOM_MIN, Math.min((r.width - 2 * FIT_PAD) / f.w, (r.height - 2 * FIT_PAD) / f.h));
    state.panX = (r.width - f.w * state.zoom) / 2 - f.x * state.zoom;
    state.panY = (r.height - f.h * state.zoom) / 2 - f.y * state.zoom;
    render();
  }

  view.addEventListener("wheel", (e) => {
    if (state.editing !== null && (e.target as HTMLElement).closest(".xyed-body") && !e.ctrlKey) return;
    e.preventDefault();
    const r = viewRect();
    if (e.ctrlKey || e.metaKey) { zoomAt(Math.exp(-e.deltaY * WHEEL_ZOOM), e.clientX - r.left, e.clientY - r.top); return; }
    state.panX -= e.shiftKey && !e.deltaX ? e.deltaY : e.deltaX;
    state.panY -= e.shiftKey && !e.deltaX ? 0 : e.deltaY;
    render();
  }, { passive: false });

  // ---- pointer: select, move, resize, create, pan ----

  type Drag =
    | { kind: "pan"; x0: number; y0: number; px: number; py: number }
    | { kind: "move"; targets: Rect[]; x0: number; y0: number; r0: Rect[]; copy?: boolean }
    | { kind: "none" }
    | { kind: "resize"; target: Rect; handle: Handle; r0: Rect; ratio?: number }
    | { kind: "create"; tool: Tool; x0: number; y0: number; item: Item | null };

  let drag: Drag | null = null;

  function startDrag(e: PointerEvent, d: Drag): void {
    drag = d;
    view.setPointerCapture(e.pointerId);
    e.preventDefault();
  }

  function hitItem(t: HTMLElement): Item | undefined {
    const node = t.closest<HTMLElement>("[data-item]");
    return node ? itemById(Number(node.dataset.item)) : undefined;
  }

  // selTarget is the rectangle the selection stands for: the frame or an item.
  function selTarget(): (Rect & { ratio?: number }) | undefined {
    if (state.sel === "frame") return state.frame;
    if (typeof state.sel === "number") return itemById(state.sel);
    return undefined;
  }

  function moveFrom(p: { x: number; y: number }, targets: Rect[], copy = false): Drag {
    return { kind: "move", targets, x0: p.x, y0: p.y, r0: targets.map((r) => ({ ...r })), copy };
  }

  // grabbed is what a press on an item drags: the whole selection when the
  // item is in it, else the item alone.
  function grabbed(it: Item): Item[] {
    const picked = pickedItems();
    const items = picked.includes(it) ? picked : [it];
    pick(items, it);
    return items;
  }

  function pick(items: Item[], primary: Item): void {
    if (items.length === 1) select(items[0].id);
    else setPicked(items.map((i) => i.id), primary.id);
  }

  // copyDragged swaps what an Alt-drag holds for copies of it, on its first
  // move, so an Alt-click alone copies nothing. The originals stay put.
  function copyDragged(d: Extract<Drag, { kind: "move" }>): void {
    d.copy = false;
    const items = d.targets.filter((t): t is Item => t !== state.frame);
    if (items.length !== d.targets.length) return;
    const primary = items.findIndex((i) => i.id === state.sel);
    const copies = cloneItems(items);
    d.targets = copies;
    pick(copies, copies[Math.max(0, primary)]);
  }

  function panFrom(e: PointerEvent): Drag {
    return { kind: "pan", x0: e.clientX, y0: e.clientY, px: state.panX, py: state.panY };
  }

  // pressDrag decides what a left press starts: a resize from a handle, a new
  // item under a drawing tool, moving an item or the frame, or a pan.
  function pressDrag(e: PointerEvent, t: HTMLElement): Drag {
    const p = toWorld(e.clientX, e.clientY);
    const handle = t.closest<HTMLElement>("[data-handle]")?.dataset.handle as Handle | undefined;
    const sr = selTarget();
    if (handle && sr) return { kind: "resize", target: sr, handle, r0: { ...sr }, ratio: sr.ratio };
    if (state.tool !== "move") {
      const at = snapped(p, true);
      return { kind: "create", tool: state.tool, x0: at.x, y0: at.y, item: null };
    }
    const it = hitItem(t);
    if (it && addsToSelection(e)) { togglePicked(it.id); return { kind: "none" }; }
    if (it) return moveFrom(p, grabbed(it), e.altKey);
    if (t.closest("[data-frame]") || inFrame(p)) {
      if (state.sel !== "frame") select("frame");
      return moveFrom(p, [state.frame]);
    }
    select(null);
    return panFrom(e);
  }

  view.addEventListener("pointerdown", (e) => {
    const t = e.target as HTMLElement;
    if (state.editing !== null && t.closest(".xyed-body")) return; // typing
    stopEditing();
    view.focus({ preventScroll: true });
    const panButton = e.button === 1 || (e.button === 0 && state.space);
    if (panButton) { startDrag(e, panFrom(e)); return; }
    if (e.button === 0) startDrag(e, pressDrag(e, t));
  });

  function inFrame(p: { x: number; y: number }): boolean {
    const f = state.frame;
    return p.x >= f.x && p.x <= f.x + f.w && p.y >= f.y && p.y <= f.y + f.h;
  }

  function resizeRect(d: Extract<Drag, { kind: "resize" }>, p: { x: number; y: number }, keep: boolean): void {
    const r0 = d.r0;
    let x1 = r0.x;
    let y1 = r0.y;
    let x2 = r0.x + r0.w;
    let y2 = r0.y + r0.h;
    if (d.handle.includes("w")) x1 = Math.min(p.x, x2 - MIN_MM);
    if (d.handle.includes("e")) x2 = Math.max(p.x, x1 + MIN_MM);
    if (d.handle.includes("n")) y1 = Math.min(p.y, y2 - MIN_MM);
    if (d.handle.includes("s")) y2 = Math.max(p.y, y1 + MIN_MM);
    const ratio = d.ratio ?? (keep ? r0.w / r0.h : undefined);
    if (ratio) {
      // Keep the proportions, from the corner opposite the handle.
      let w = x2 - x1;
      let h = y2 - y1;
      if (w / h > ratio) w = h * ratio; else h = w / ratio;
      if (d.handle.includes("w")) x1 = x2 - w; else x2 = x1 + w;
      if (d.handle.includes("n")) y1 = y2 - h; else y2 = y1 + h;
    }
    Object.assign(d.target, { x: x1, y: y1, w: x2 - x1, h: y2 - y1 });
  }

  view.addEventListener("pointermove", (e) => {
    if (!drag) return;
    if (drag.kind === "pan") {
      state.panX = drag.px + e.clientX - drag.x0;
      state.panY = drag.py + e.clientY - drag.y0;
      render();
      return;
    }
    if (drag.kind === "none") return;
    const p = snapped(toWorld(e.clientX, e.clientY), drag.kind !== "move");
    if (drag.kind === "move") {
      if (drag.copy) copyDragged(drag);
      moveBy(drag, p.x - drag.x0, p.y - drag.y0);
    } else if (drag.kind === "resize") {
      resizeRect(drag, p, e.shiftKey);
    } else {
      const r = { x: Math.min(p.x, drag.x0), y: Math.min(p.y, drag.y0), w: Math.abs(p.x - drag.x0), h: Math.abs(p.y - drag.y0) };
      if (!drag.item && Math.max(r.w, r.h) >= CLICK_MM) {
        drag.item = drag.tool === "text" ? newText(r.x, r.y, r.w, r.h) : { id: state.nextId++, kind: "rect", ...r };
        addItem(drag.item);
      }
      if (drag.item) Object.assign(drag.item, { x: r.x, y: r.y, w: Math.max(MIN_MM, r.w), h: Math.max(MIN_MM, r.h) });
    }
    render();
  });

  // snapped is a pointer position on the grid, when snapping is on; a move
  // snaps the box instead (moveBy), so the point is left as it is.
  function snapped(p: { x: number; y: number }, onGrid: boolean): { x: number; y: number } {
    if (!onGrid || !state.grid.snap) return p;
    const f = state.frame;
    return { x: snapPoint(p.x, f.x, state.grid.step), y: snapPoint(p.y, f.y, state.grid.step) };
  }

  // moveBy shifts what is dragged; with snapping on, the dragged boxes' common
  // bounds land with their nearer edges on the grid. The frame never snaps to
  // its own grid.
  function moveBy(d: Extract<Drag, { kind: "move" }>, dx: number, dy: number): void {
    const moved = d.r0.map((r) => ({ ...r, x: r.x + dx, y: r.y + dy }));
    if (state.grid.snap && !d.targets.includes(state.frame)) {
      const b = bounds(moved);
      const f = state.frame;
      const sx = snapShift(b.x, b.w, f.x, state.grid.step);
      const sy = snapShift(b.y, b.h, f.y, state.grid.step);
      for (const r of moved) { r.x += sx; r.y += sy; }
    }
    d.targets.forEach((t, i) => { t.x = moved[i].x; t.y = moved[i].y; });
  }

  function endDrag(): void {
    const d = drag;
    drag = null;
    if (!d || d.kind !== "create") { renderLayers(); return; }
    let it = d.item;
    if (!it) {
      // A click: the tool's default size, centred on the click.
      const size = d.tool === "text" ? NEW_TEXT : NEW_RECT;
      const x = d.x0 - size.w / 2;
      const y = d.y0 - size.h / 2;
      it = d.tool === "text" ? newText(x, y, size.w, size.h) : { id: state.nextId++, kind: "rect", x, y, ...size };
      addItem(it);
    }
    state.tool = "move";
    if (it.kind === "text") startEditing(it.id);
    else render();
  }

  view.addEventListener("pointerup", endDrag);
  view.addEventListener("pointercancel", endDrag);

  // The dblclick's target is the view, not the item: the pointerdown before it
  // captured the pointer for a drag, and a captured pointer's clicks are aimed at
  // whoever holds the capture. So the item is found by where the click was.
  view.addEventListener("dblclick", (e) => {
    const under = document.elementsFromPoint(e.clientX, e.clientY);
    const node = under.find((n) => n instanceof HTMLElement && n.closest("[data-item]"));
    const it = node ? hitItem(node as HTMLElement) : undefined;
    if (it?.kind === "text") startEditing(it.id, e.clientX, e.clientY);
  });

  // ---- keyboard ----

  function isField(t: EventTarget | null): boolean {
    return t instanceof HTMLInputElement || t instanceof HTMLSelectElement || t instanceof HTMLTextAreaElement;
  }

  function viewCentre(): { x: number; y: number } {
    const r = viewRect();
    return { x: r.width / 2, y: r.height / 2 };
  }

  const TOOL_KEYS: Record<string, Tool> = { v: "move", t: "text", r: "rect" };

  // zoomKey handles the view's own keys; it reports whether it took the key.
  function zoomKey(e: KeyboardEvent, mod: boolean): boolean {
    const c = viewCentre();
    if (mod && (e.key === "=" || e.key === "+")) zoomAt(KEY_ZOOM, c.x, c.y);
    else if (mod && e.key === "-") zoomAt(1 / KEY_ZOOM, c.x, c.y);
    else if ((mod && e.key === "0") || (e.shiftKey && e.code === "Digit1")) fitFrame();
    else return false;
    e.preventDefault();
    return true;
  }

  const ARROWS: Record<string, [number, number]> = { ArrowLeft: [-1, 0], ArrowRight: [1, 0], ArrowUp: [0, -1], ArrowDown: [0, 1] };

  // selectionKey handles the keys that act on the selection.
  function selectionKey(e: KeyboardEvent): void {
    if (e.key === "Delete" || e.key === "Backspace") { e.preventDefault(); removeSelected(); return; }
    if (e.key === "Enter" && typeof state.sel === "number" && itemById(state.sel)?.kind === "text") {
      e.preventDefault();
      startEditing(state.sel);
      return;
    }
    const dir = ARROWS[e.key];
    const rs: Rect[] = state.sel === "frame" ? [state.frame] : pickedItems();
    if (!dir || !rs.length) return;
    e.preventDefault();
    const step = e.shiftKey ? NUDGE_BIG : NUDGE;
    for (const r of rs) {
      r.x += dir[0] * step;
      r.y += dir[1] * step;
    }
    render();
  }

  function selectAll(): void {
    setPicked(state.items.map((i) => i.id), state.items[state.items.length - 1].id);
  }

  // escapeKey takes Escape for the editor while there is something to let go of:
  // the text being typed, then the selection, then the tool. Only an Escape with
  // none of those left reaches the overlay stack, which closes the editor.
  function escapeKey(e: KeyboardEvent): boolean {
    if (state.editing !== null) stopEditing();
    else if (state.sel !== null) select(null);
    else if (state.tool !== "move") setTool("move");
    else return false;
    e.preventDefault();
    e.stopPropagation();
    return true;
  }

  // Captured, so the editor hears a key before the overlay stack does.
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && escapeKey(e)) return;
    if (state.editing !== null || isField(e.target)) return;
    const mod = e.ctrlKey || e.metaKey;
    if (mod && e.code === "KeyA" && state.items.length) { e.preventDefault(); selectAll(); return; }
    if (zoomKey(e, mod) || mod) return;
    if (e.key === " ") { e.preventDefault(); state.space = true; render(); return; }
    const tool = TOOL_KEYS[e.key.toLowerCase()];
    if (tool) setTool(tool);
    else selectionKey(e);
  }, { capture: true, signal });

  document.addEventListener("keyup", (e) => {
    if (e.key === " ") { state.space = false; render(); }
  }, { signal });

  window.addEventListener("resize", render, { signal });

  // ---- typography ----

  // typographText is the card editor's typography pass for text that is not 4s:
  // line by line, with no marker splitting, so a line opening with a dash gets
  // an em dash rather than being read as a list item. Then the non-breaking
  // gluing, which is most of the point on a picture: a preposition or a number
  // that stays with its word.
  function typographText(text: string): string {
    return text.split("\n").map((line) => (line.trim() ? chgk4s.replaceNoBreak(xyTypo.typography(line, true)) : line)).join("\n");
  }

  function typographAll(): void {
    for (const it of state.items) {
      if (it.kind !== "text") continue;
      const next = typographText(it.text);
      if (next === it.text) continue;
      const body = bodies.get(it.id)!;
      // Through insertText, so Ctrl-Z in the box takes the pass back.
      body.contentEditable = "plaintext-only";
      body.focus();
      document.getSelection()?.selectAllChildren(body);
      document.execCommand("insertText", false, next);
      it.text = body.innerText;
      body.contentEditable = "false";
    }
    state.editing = null;
    renderLayers();
    render();
  }

  // ---- freezing and baking ----

  function ascentPx(s: TextStyle): number {
    const ctx = document.createElement("canvas").getContext("2d")!;
    const italic = !s.font && s.italic ? "italic " : "";
    ctx.font = `${italic}${!s.font && s.bold ? WEIGHT.bold : WEIGHT.regular} ${s.size * PT_MM * BASE}px ${cssFamily(s)}`;
    return ctx.measureText("Hg").fontBoundingBoxAscent * state.zoom / BASE;
  }

  // A frame's layers for export: pictures as they are, and every run of black
  // boxes and text between them as one vector overlay.
  type Out = { image: ImageItem } | { overlay: Layer[]; texts: FrozenBox[] };

  function exportLayers(): Out[] {
    stopEditing();
    const origin = frameBg.getBoundingClientRect();
    const f = state.frame;
    const out: Out[] = [];
    let run: { overlay: Layer[]; texts: FrozenBox[] } | null = null;
    for (const it of state.items) {
      if (it.kind === "image") { run = null; out.push({ image: it }); continue; }
      if (!run) { run = { overlay: [], texts: [] }; out.push(run); }
      if (it.kind === "rect") { run.overlay.push({ box: { x: it.x - f.x, y: it.y - f.y, w: it.w, h: it.h } }); continue; }
      const t = freezeBox(bodies.get(it.id)!, it.style, origin, state.zoom, ascentPx(it.style));
      run.texts.push(t);
      run.overlay.push({ text: t });
    }
    return out;
  }

  function lineCount(out: Out[]): number {
    return out.reduce((a, o) => a + ("texts" in o ? o.texts.reduce((b, t) => b + t.lines.length, 0) : 0), 0);
  }

  function base64(buf: ArrayBuffer): string {
    const bytes = new Uint8Array(buf);
    let s = "";
    for (let i = 0; i < bytes.length; i += BASE64_CHUNK) s += String.fromCharCode(...bytes.subarray(i, i + BASE64_CHUNK));
    return btoa(s);
  }

  // The browser's own render of an overlay needs the faces inside the SVG: an
  // SVG drawn as an image cannot see the page's fonts.
  async function embeddedFaces(texts: FrozenBox[]): Promise<string> {
    const noto = texts.filter((t) => !t.style.font);
    const used = new Set(noto.map((t) => `${t.style.bold ? "700" : "400"}/${t.style.italic ? "italic" : "normal"}`));
    const rules = FACES.filter((f) => used.has(`${f.weight}/${f.style}`)).map((f) =>
      `@font-face{font-family:"${TYPST_FAMILY}";font-weight:${f.weight};font-style:${f.style};src:url(data:font/ttf;base64,${base64(faceBytes.get(f.file)!)})}`);
    for (const ref of usedFaces(texts.map((t) => t.style))) {
      const bytes = await loadFace(fonts, ref);
      rules.push(`@font-face{font-family:"${ref.family}";src:url(data:font/ttf;base64,${base64(bytes.buffer)})}`);
    }
    return rules.join("");
  }

  async function retarget(fam: FontFamily): Promise<number> {
    const moved = textStyles().filter((st) => st.font && st.font.name === fam.name && st.font.zip !== fam.hash);
    for (const st of moved) st.font = faceRef(fam, matchFace(fam, st));
    await Promise.all(usedFaces(moved).map(picker.useFace));
    return moved.length;
  }

  function textStyles(): TextStyle[] {
    return state.items.filter((i): i is TextItem => i.kind === "text").map((t) => t.style);
  }

  // usedFaces is the uploaded Faces some of these styles name, once each.
  function usedFaces(styles: TextStyle[]): FaceRef[] {
    const out = new Map<string, FaceRef>();
    for (const st of styles) if (st.font) out.set(st.font.family, st.font);
    return [...out.values()];
  }

  async function bakePNG(dpi: number): Promise<{ blob: Blob; lines: number }> {
    const out = exportLayers();
    const f = state.frame;
    const k = dpi / MM_PER_IN;
    const W = Math.round(f.w * k);
    const H = Math.round(f.h * k);
    const drawn: { img: HTMLImageElement; r: Rect }[] = [];
    const urls: string[] = [];
    try {
      for (const o of out) {
        if ("image" in o) { drawn.push({ img: await loadImage(o.image.url), r: { x: o.image.x - f.x, y: o.image.y - f.y, w: o.image.w, h: o.image.h } }); continue; }
        const svg = overlaySVG(f.w, f.h, o.overlay, { family: TYPST_FAMILY, fontFaces: await embeddedFaces(o.texts) });
        const url = URL.createObjectURL(new Blob([svg], { type: "image/svg+xml" }));
        urls.push(url);
        drawn.push({ img: await loadImage(url), r: { x: 0, y: 0, w: f.w, h: f.h } });
      }
      const blob = await canvasBlob(W, H, (ctx) => {
        ctx.fillStyle = f.bg;
        ctx.fillRect(0, 0, W, H);
        for (const d of drawn) ctx.drawImage(d.img, d.r.x * k, d.r.y * k, d.r.w * k, d.r.h * k);
      });
      return { blob, lines: lineCount(out) };
    } finally { for (const u of urls) URL.revokeObjectURL(u); }
  }

  async function typstPDF(): Promise<{ blob: Blob; lines: number }> {
    const out = exportLayers();
    const f = state.frame;
    const fd = new FormData();
    const layout: Record<string, unknown>[] = [];
    out.forEach((o, i) => {
      const file = `f${i}`;
      if ("image" in o) {
        const im = o.image;
        layout.push({ kind: "image", file, ext: im.ext, x: im.x - f.x, y: im.y - f.y, w: im.w, h: im.h });
        fd.set(file, im.bytes, `${file}.${im.ext}`);
      } else {
        layout.push({ kind: "svg", file });
        fd.set(file, new Blob([overlaySVG(f.w, f.h, o.overlay, { family: TYPST_FAMILY })], { type: "image/svg+xml" }), `${file}.svg`);
      }
    });
    const faces = bakeFonts();
    for (const zip of new Set(faces.map((x) => x.zip))) fd.append("font", new Blob([await fonts.zip(zip)]), `${zip}.zip`);
    fd.set("layout", JSON.stringify({ w: f.w, h: f.h, bg: f.bg, layers: layout, fonts: faces }));
    status.textContent = S.xyimage.status.working();
    const res = await fetch("/api/xyimage/render", { method: "POST", body: fd, credentials: "same-origin" });
    if (!res.ok) throw new Error((await res.text()).trim() || `HTTP ${res.status}`);
    return { blob: await res.blob(), lines: lineCount(out) };
  }

  async function exportPDF(): Promise<void> {
    try {
      const r = await typstPDF();
      status.textContent = S.xyimage.status.frozenLines(String(r.lines));
      downloadBlob(r.blob, "xyimage.pdf");
    } catch (err) { status.textContent = errMsg(err); }
  }

  async function exportPNG(): Promise<void> {
    try {
      const r = await bakePNG(state.frame.dpi);
      status.textContent = S.xyimage.status.frozenLines(String(r.lines));
      downloadBlob(r.blob, "xyimage.png");
    } catch (err) { status.textContent = errMsg(err); }
  }

  let compareURL: string | null = null;

  async function toggleCompare(): Promise<void> {
    state.comparing = !state.comparing;
    if (compareURL) { URL.revokeObjectURL(compareURL); compareURL = null; }
    compareImg.hidden = true;
    if (state.comparing) {
      try {
        const dpi = Math.min(state.zoom * MM_PER_IN * devicePixelRatio, MAX_COMPARE_DPI);
        compareURL = URL.createObjectURL((await bakePNG(dpi)).blob);
        compareImg.src = compareURL;
        compareImg.hidden = false;
      } catch (err) { status.textContent = errMsg(err); state.comparing = false; }
    }
    view.classList.toggle("xyed-comparing", state.comparing);
    render();
  }


  // ---- saving and reopening ----

  // The Recipe is what the editor reopens: the frame and the items, the text
  // and its styling. Its bake plan is what the exporters read (chgksuite/xyimage).
  interface RecipeItem extends Rect { kind: Item["kind"]; text?: string; style?: TextStyle; src?: number; name?: string; ext?: string; ratio?: number }
  interface Recipe { v: number; frame: Frame; items: RecipeItem[]; bake: unknown }

  function recipeItems(images: ImageItem[]): RecipeItem[] {
    return state.items.map((it) => {
      const r = { kind: it.kind, x: it.x, y: it.y, w: it.w, h: it.h };
      if (it.kind === "text") return { ...r, text: it.text, style: { ...it.style } };
      if (it.kind === "image") return { ...r, src: images.indexOf(it), name: it.name, ext: it.ext, ratio: it.ratio };
      return r;
    });
  }

  // contentKey is the Recipe without its bake, which is what "changed" means.
  function contentKey(): string {
    stopEditing();
    return JSON.stringify({ frame: state.frame, items: recipeItems(state.items.filter((i): i is ImageItem => i.kind === "image")) });
  }

  // bakeFonts is the Recipe's list of the uploaded Faces its overlays use,
  // which an exporter looks up in the Families it is handed.
  function bakeFonts(): { zip: string; face: string; family: string; name: string }[] {
    return usedFaces(textStyles()).map((r) => ({ zip: r.zip, face: r.face, family: r.family, name: r.name }));
  }

  async function serialize(): Promise<Uint8Array> {
    const missing = state.items.find((i): i is TextItem => i.kind === "text" && !!i.style.font && missingFaces.has(i.style.font.family));
    if (missing?.kind === "text") throw new Error(S.xyimage.status.fontMissing(missing.style.font!.name));
    const images = state.items.filter((i): i is ImageItem => i.kind === "image");
    const out = exportLayers();
    const f = state.frame;
    const overlays: string[] = [];
    const layers = out.map((o) => {
      if ("image" in o) {
        const im = o.image;
        return { kind: "image", src: images.indexOf(im), ext: im.ext, x: im.x - f.x, y: im.y - f.y, w: im.w, h: im.h };
      }
      overlays.push(overlaySVG(f.w, f.h, o.overlay, { family: TYPST_FAMILY }));
      return { kind: "svg", over: overlays.length - 1 };
    });
    const recipe: Recipe = { v: RECIPE_VERSION, frame: { ...f }, items: recipeItems(images), bake: { w: f.w, h: f.h, bg: f.bg, layers, fonts: bakeFonts() } };
    const raster = (await bakePNG(f.dpi)).blob;
    return encodeXyimage({
      recipe: JSON.stringify(recipe),
      images: await Promise.all(images.map(async (im) => new Uint8Array(await im.bytes.arrayBuffer()))),
      overlays,
      raster: new Uint8Array(await raster.arrayBuffer()),
    });
  }

  function load(bytes: Uint8Array): void {
    const parts = parseXyimage(bytes);
    const recipe = JSON.parse(parts.recipe) as Recipe;
    Object.assign(state.frame, recipe.frame);
    for (const ri of recipe.items) {
      const r = { x: ri.x, y: ri.y, w: ri.w, h: ri.h, id: state.nextId++ };
      if (ri.kind === "text") state.items.push({ ...r, kind: "text", text: ri.text ?? "", style: { ...defaultStyle, ...ri.style } });
      else if (ri.kind === "rect") state.items.push({ ...r, kind: "rect" });
      else {
        const ext = ri.ext === "jpg" ? "jpg" : "png";
        const blob = new Blob([new Uint8Array(parts.images[ri.src ?? 0])], { type: ext === "jpg" ? "image/jpeg" : "image/png" });
        state.items.push({ ...r, kind: "image", name: ri.name ?? "", bytes: blob, ext, url: URL.createObjectURL(blob), ratio: ri.ratio ?? ri.w / ri.h });
      }
    }
  }

  let savedKey = "";

  async function save(): Promise<void> {
    if (!opts.onSave) return;
    status.textContent = S.xyimage.status.saving();
    try {
      await opts.onSave(await serialize());
      savedKey = contentKey();
      status.textContent = S.xyimage.status.saved();
    } catch (err) { status.textContent = errMsg(err); }
  }

  if (opts.onSave) {
    bar.append(
      barBtn("check", S.xyimage.tools.save(), S.xyimage.tools.save(), () => { void save(); }),
      barBtn("x", null, S.xyimage.tools.close(), () => opts.onClose?.()));
  }

  if (opts.file) load(opts.file);
  bar.append(picker.input);
  syncItems();
  // Its boxes' Faces load before anything is measured: a box laid out in Noto
  // Sans while its Face is on the way would freeze the wrong lines.
  await Promise.all(usedFaces(textStyles()).map(picker.useFace));
  renderLayers();
  renderProps();
  fitFrame();
  if (opts.picture) {
    await addPicture(opts.picture.blob, opts.picture.name);
    // A picture to start from opens framed: the frame takes its size.
    const it = state.items[state.items.length - 1];
    if (it) Object.assign(state.frame, { x: it.x, y: it.y, w: it.w, h: it.h });
    select(null);
    fitFrame();
  }
  savedKey = contentKey();

  return {
    dirty: () => contentKey() !== savedKey,
    retarget,
    bytes: serialize,
    destroy() {
      ac.abort();
      for (const it of state.items) if (it.kind === "image") URL.revokeObjectURL(it.url);
      if (compareURL) URL.revokeObjectURL(compareURL);
      root.replaceChildren();
    },
  };
}
