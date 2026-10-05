// colorpick.ts — the label colour control. input[type=color] hands the choice to
// the OS (a full-screen sheet on Android) and invites a shade no other label on
// the board uses; a board's labels are a small shared vocabulary, so this offers
// a closed palette and nothing else.

import { xyApp } from "./app.js";
import { anchorPopup, type AnchoredPopup } from "./popup.js";
import S from "./i18nstrings.js";
// The palette is a list of NAMES — "green", "teal" — and the hex a name paints
// is --label-<name>, which each theme sets to a different rung of that hue. It
// has to work that way: no single colour clears 3:1 against both a near-white
// card and a mid-grey one, the two bands do not overlap, so a stored hex is
// legible on one theme and invisible on the other. Generated from
// dopeuikit/palette, the same source core.css and dope's swatch enum read.
import { LABEL_AB, LABEL_COLORS, LEGACY_LABEL_HEX, YIN, YANG } from "./palette_gen.js";

const { el } = xyApp;

export { LABEL_COLORS };

const HEX_DIGITS = 6;
const SHORT_HEX_DIGITS = 3;
const HEX_RADIX = 16;
const CHANNEL_MAX = 255;
// Where r, g and b start in "#rrggbb".
const CHANNEL_OFFSETS = [1, 3, 5];
// The sRGB transfer function (IEC 61966-2-1).
const SRGB_LINEAR_CUTOFF = 0.04045;
const SRGB_LINEAR_SLOPE = 12.92;
const SRGB_OFFSET = 0.055;
const SRGB_SCALE = 1.055;
const SRGB_GAMMA = 2.4;
// Ottosson's OKLab: linear sRGB to LMS, then cube-rooted LMS to the a and b axes.
const OKLAB_LMS = [
  [0.4122214708, 0.5363325363, 0.0514459929],
  [0.2119034982, 0.6806995451, 0.1073969566],
  [0.0883024619, 0.2817188376, 0.6299787005],
];
const OKLAB_A = [1.9779984951, -2.4285922050, 0.4505937099];
const OKLAB_B = [0.0259040371, 0.7827717662, -0.8086757660];
// Rec. 709 luma weights for relative luminance.
const LUMA_WEIGHTS = [0.2126, 0.7152, 0.0722];
// WCAG's flare term in the contrast ratio.
const WCAG_FLARE = 0.05;
// Below this OKLab chroma a colour reads as grey.
const GRAY_CHROMA = 0.03;
const HALF_TURN_DEG = 180;
const FULL_TURN_DEG = 360;
// How many degrees of hue a full chroma mismatch is worth in nearestName.
const CHROMA_WEIGHT_DEG = 30;

// "" when the input is not a colour yet — a half-typed one is not an error.
export function normalizeHex(raw: string): string {
  const v = (raw || "").trim().replace(/^#/, "").toLowerCase();
  const full = v.length === SHORT_HEX_DIGITS ? v.replace(/./g, (ch) => ch + ch) : v;
  return full.length === HEX_DIGITS && /^[0-9a-f]+$/.test(full) ? "#" + full : "";
}

// sRGB hex → the OKLab colour plane. Lightness is dropped on the way out: it is
// the theme's to decide, so it plays no part in deciding WHICH colour this is.
function linearRGB(hex: string): number[] {
  return CHANNEL_OFFSETS
    .map((i) => parseInt(hex.slice(i, i + 2), HEX_RADIX) / CHANNEL_MAX)
    .map((u) => (u <= SRGB_LINEAR_CUTOFF ? u / SRGB_LINEAR_SLOPE : ((u + SRGB_OFFSET) / SRGB_SCALE) ** SRGB_GAMMA));
}

function dot(w: number[], v: number[]): number {
  return w.reduce((sum, wi, i) => sum + wi * v[i], 0);
}

function toLab(hex: string): [number, number] {
  const rgb = linearRGB(hex);
  const lms = OKLAB_LMS.map((row) => Math.cbrt(dot(row, rgb)));
  return [dot(OKLAB_A, lms), dot(OKLAB_B, lms)];
}

function hueDeg(a: number, b: number): number {
  return Math.atan2(b, a) * HALF_TURN_DEG / Math.PI;
}

// nearestName snaps a colour the palette does not name to the one it is nearest.
// HUE decides it, not distance across the plane: straight-line distance is
// dominated by how saturated a colour is, and it sent a muted blue to cyan
// because cyan's rung happens to be the paler of the two. Chroma only breaks a
// tie between names that share a hue — brown is a drained orange and nothing
// else tells them apart — and a colour with almost none of it is grey, which is
// the one name no hue can find.
function nearestName(hex: string): string {
  const [a, b] = toLab(hex);
  const chroma = Math.hypot(a, b);
  if (chroma < GRAY_CHROMA) return "gray";
  const hue = hueDeg(a, b);
  let best = "gray", score = Infinity;
  for (const [name, [na, nb]] of Object.entries(LABEL_AB)) {
    if (name === "gray") continue;
    const nc = Math.hypot(na, nb);
    const apart = Math.abs(((hue - hueDeg(na, nb) + FULL_TURN_DEG + HALF_TURN_DEG) % FULL_TURN_DEG) - HALF_TURN_DEG);
    const s = apart + CHROMA_WEIGHT_DEG * Math.abs(chroma - nc) / (chroma + nc);
    if (s < score) [best, score] = [name, s];
  }
  return best;
}

// labelName reads what a label has STORED and answers with a palette name.
// New labels store the name itself. A board made before the palette was named
// stored the hex the picker gave it: the eight presets are recognised exactly,
// and a hex typed into the custom field — which no longer exists — is snapped to
// whichever name it is nearest. So every label ends up on the palette, and the
// name is written back the next time anyone edits it. Nothing is migrated: the
// stored value is only ever READ this way, so a snap that reads wrong is one
// edit away from being fixed rather than something baked into the board.
export function labelName(stored: string): string {
  const v = (stored || "").trim().toLowerCase();
  if (LABEL_COLORS.includes(v)) return v;
  const hex = normalizeHex(v);
  if (!hex) return "";
  return LEGACY_LABEL_HEX[hex] || nearestName(hex);
}

// labelFill / labelInk turn what a label stored into what CSS should paint.
// The value is `var(--label-green)`, never a hex, so the browser re-reads it
// when the theme flips and nothing has to re-render.
export function labelFill(stored: string): string {
  const name = labelName(stored);
  return name ? `var(--label-${name})` : "var(--muted)";
}

export function labelInk(stored: string): string {
  const name = labelName(stored);
  return name ? `var(--label-${name}-ink)` : textOn(stored);
}

function luminance(hex: string): number {
  return dot(LUMA_WEIGHTS, linearRGB(hex));
}

// textOn returns the ink that reads better on `bg`, or "" when bg is not a
// colour. Only unnamed leftovers reach it now — a named colour's ink is
// generated beside the colour itself.
export function textOn(bg: string): string {
  const hex = normalizeHex(bg);
  if (!hex) return "";
  const l = luminance(hex);
  const onYin = (l + WCAG_FLARE) / (luminance(YIN) + WCAG_FLARE);
  const onYang = (luminance(YANG) + WCAG_FLARE) / (l + WCAG_FLARE);
  return onYin >= onYang ? YIN : YANG;
}

export interface ColorField {
  node: HTMLElement;
  value(): string;
  set(name: string): void;
}

export function colorField(host: HTMLElement, initial: string): ColorField {
  let current = labelName(initial) || LABEL_COLORS[0];
  host.classList.add("color-field");
  const btn = el("button", {
    class: "color-field-btn", type: "button",
    title: S.chrome.colorpick.buttonTitle(), "aria-label": S.chrome.colorpick.buttonTitle(),
  }) as HTMLButtonElement;
  host.replaceChildren(btn);

  let popup: AnchoredPopup | null = null;
  let cells: HTMLElement[] = [];

  function pick(name: string): void {
    current = name;
    btn.style.background = labelFill(name);
    for (const cell of cells) cell.classList.toggle("is-on", cell.dataset.c === name);
  }

  function close(): void {
    if (popup) popup.close();
  }

  function open(): void {
    const grid = el("div", { class: "color-grid" });
    cells = LABEL_COLORS.map((c) => {
      const cell = el("button", {
        class: "color-cell", type: "button", title: c, "aria-label": c, dataset: { c },
      }) as HTMLButtonElement;
      cell.style.background = labelFill(c);
      cell.addEventListener("click", () => { pick(c); close(); });
      grid.append(cell);
      return cell;
    });
    const pop = el("div", { class: "menu-dropdown menu-fixed color-pop" }, grid);
    popup = anchorPopup(pop, btn, { anchor: host, onClose: () => { popup = null; cells = []; } });
    pick(current);
  }

  btn.addEventListener("click", () => { if (popup) close(); else open(); });
  pick(current);

  return {
    node: host,
    value: () => current,
    set: (name) => { const v = labelName(name); if (v) pick(v); },
  };
}
