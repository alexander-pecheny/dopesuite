// colorpick.ts — the label colour control. input[type=color] hands the choice to
// the OS (a full-screen sheet on Android) and invites a shade no other label on
// the board uses; a board's labels are a small shared vocabulary, so this offers
// a fixed palette, with a hex field for the board that needs its own colour.

import { xyApp } from "./app.js";
import { anchorPopup, type AnchoredPopup } from "./popup.js";

const { el } = xyApp;

// uchu's pastel palette (uchu.style), shade 5 of all eight hues — one rung
// across the set, which is what an OKLCH palette buys you: they read as equals
// rather than as one loud colour beside seven quiet ones.
export const LABEL_COLORS = [
  "#a84151", "#d69870", "#ebd697", "#77bb79",
  "#3d64ac", "#674292", "#e5b5c7", "#bfc1c3",
];

// uchu's own ink and paper. The palette is designed against these two, and every
// LABEL_COLORS entry clears WCAG AA (4.5:1) against whichever textOn picks.
const YIN = "#080a0d";
const YANG = "#fdfdfd";

// "" when the input is not a colour yet — a half-typed one is not an error.
export function normalizeHex(raw: string): string {
  const v = (raw || "").trim().replace(/^#/, "").toLowerCase();
  const full = v.length === 3 ? v.replace(/./g, (ch) => ch + ch) : v;
  return /^[0-9a-f]{6}$/.test(full) ? "#" + full : "";
}

function luminance(hex: string): number {
  const [r, g, b] = [1, 3, 5]
    .map((i) => parseInt(hex.slice(i, i + 2), 16) / 255)
    .map((u) => (u <= 0.04045 ? u / 12.92 : ((u + 0.055) / 1.055) ** 2.4));
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

// textOn returns the ink that reads better on `bg`, or "" when bg is not a
// colour. A label's colour is the user's to choose, so the text has to follow
// it — a hardcoded white is what forced every palette entry to be dark.
export function textOn(bg: string): string {
  const hex = normalizeHex(bg);
  if (!hex) return "";
  const l = luminance(hex);
  const onYin = (l + 0.05) / (luminance(YIN) + 0.05);
  const onYang = (luminance(YANG) + 0.05) / (l + 0.05);
  return onYin >= onYang ? YIN : YANG;
}

export interface ColorField {
  node: HTMLElement;
  value(): string;
  set(hex: string): void;
}

export function colorField(host: HTMLElement, initial: string): ColorField {
  let current = normalizeHex(initial) || LABEL_COLORS[0];
  host.classList.add("color-field");
  const btn = el("button", {
    class: "color-field-btn", type: "button",
    title: "Цвет метки", "aria-label": "Цвет метки",
  }) as HTMLButtonElement;
  host.replaceChildren(btn);

  let popup: AnchoredPopup | null = null;
  let cells: HTMLElement[] = [];

  function pick(hex: string): void {
    current = hex;
    btn.style.backgroundColor = hex;
    for (const cell of cells) cell.classList.toggle("is-on", cell.dataset.c === hex);
  }

  function close(): void {
    if (popup) popup.close();
  }

  function open(): void {
    const grid = el("div", { class: "color-grid" });
    const hex = el("input", {
      // size, or the input's intrinsic 20-character width sets the popup's, and
      // the palette grid rattles around inside a box twice as wide as it needs.
      class: "input color-hex", type: "text", value: current, maxlength: 7, size: 9,
      spellcheck: "false", autocomplete: "off", placeholder: "#rrggbb",
      "aria-label": "Свой цвет в формате #rrggbb",
    }) as HTMLInputElement;
    cells = LABEL_COLORS.map((c) => {
      const cell = el("button", {
        class: "color-cell", type: "button", title: c, "aria-label": c, dataset: { c },
      }) as HTMLButtonElement;
      cell.style.backgroundColor = c;
      cell.addEventListener("click", () => { hex.value = c; pick(c); close(); });
      grid.append(cell);
      return cell;
    });
    hex.addEventListener("input", () => {
      const v = normalizeHex(hex.value);
      // Mark "#zzz" rather than ignoring it: the swatch keeps the old colour.
      hex.classList.toggle("is-bad", !v && hex.value.trim() !== "");
      if (v) pick(v);
    });
    const pop = el("div", { class: "menu-dropdown menu-fixed color-pop" }, grid, hex);
    popup = anchorPopup(pop, btn, { anchor: host, onClose: () => { popup = null; cells = []; } });
    pick(current);
  }

  btn.addEventListener("click", () => { if (popup) close(); else open(); });
  pick(current);

  return {
    node: host,
    value: () => current,
    set: (hex) => { const v = normalizeHex(hex); if (v) pick(v); },
  };
}
