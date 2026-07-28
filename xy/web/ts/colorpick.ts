// colorpick.ts — the label colour control. input[type=color] hands the choice to
// the OS (a full-screen sheet on Android) and invites a shade no other label on
// the board uses; a board's labels are a small shared vocabulary, so this offers
// a fixed palette, with a hex field for the board that needs its own colour.

import { xyApp } from "./app.js";
import { anchorPopup, type AnchoredPopup } from "./popup.js";

const { el } = xyApp;

// Dark enough for the white text a filled chip carries (.label-pick: color #fff).
export const LABEL_COLORS = [
  "#c0392b", "#d35400", "#c99700", "#7f9c2c",
  "#3f9142", "#2c8c7a", "#2f7fb5", "#3a5ea8",
  "#7a5ec2", "#b0508f", "#8a6a55", "#5a6672",
];

// "" when the input is not a colour yet — a half-typed one is not an error.
export function normalizeHex(raw: string): string {
  const v = (raw || "").trim().replace(/^#/, "").toLowerCase();
  const full = v.length === 3 ? v.replace(/./g, (ch) => ch + ch) : v;
  return /^[0-9a-f]{6}$/.test(full) ? "#" + full : "";
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
