// handoutpage.ts — a tour's handout page setup: chgksuite's handout flags
// (paper, margins, font, cell width, and split_fit's image shrinking) kept as
// JSON on the list, or on its group, which a grouped list follows. Every
// handout render sends it (internal/server/handoutpage.go). An empty field is
// chgksuite's default and is left out.

import S from "./i18nstrings.js";
import { xyApp } from "./app.js";
import { modal } from "./modal.js";
import type { Board, ListScope } from "./panels.js";

const { byId, errMsg } = xyApp;

const CLOSE_AFTER_SAVE_MS = 600;

// The formats the paper dropdown offers, in mm; A4 is the default and absent.
const PAPERS: Record<string, [number, number]> = { a5: [148, 210], a3: [297, 420], letter: [216, 279] };

// The number fields, by the JSON key each one sets.
const NUMBERS = [
  ["margin_top", "handoutPageMarginTop"], ["margin_bottom", "handoutPageMarginBottom"],
  ["margin_left", "handoutPageMarginLeft"], ["margin_right", "handoutPageMarginRight"],
  ["font_size", "handoutPageFontSize"], ["boxwidth", "handoutPageBoxWidth"], ["tikz_mm", "handoutPageTikz"],
  ["image_shrink_percent", "handoutPageShrink"], ["min_resize_image", "handoutPageMinResize"],
  ["image_bottom_space_row_ratio", "handoutPageBottomSpace"],
] as const;

type Page = Record<string, number | string | boolean>;

function parsePage(json: string): Page {
  try {
    const v: unknown = JSON.parse(json || "{}");
    return v && typeof v === "object" ? v as Page : {};
  } catch (_) { return {}; }
}

function paperOf(p: Page): string {
  if (p.paperwidth === undefined && p.paperheight === undefined) return "";
  const hit = Object.entries(PAPERS).find(([, [w, h]]) => p.paperwidth === w && p.paperheight === h);
  return hit ? hit[0] : "custom";
}

export function createHandoutPage(board: Board) {
  const pageModal = modal("handoutPage");
  const input = (id: string): HTMLInputElement => byId<HTMLInputElement>(id);
  let target: { scope: ListScope; onSaved: (json: string) => void } | null = null;

  function syncPaper(): void {
    byId("handoutPageSize").hidden = byId<HTMLSelectElement>("handoutPagePaper").value !== "custom";
  }

  function fill(p: Page): void {
    byId<HTMLSelectElement>("handoutPagePaper").value = paperOf(p);
    input("handoutPageWidth").value = p.paperwidth !== undefined ? String(p.paperwidth) : "";
    input("handoutPageHeight").value = p.paperheight !== undefined ? String(p.paperheight) : "";
    for (const [key, id] of NUMBERS) input(id).value = p[key] !== undefined ? String(p[key]) : "";
    input("handoutPageFont").value = typeof p.font === "string" ? p.font : "";
    input("handoutPageAutoResize").checked = p.no_auto_resize_images !== true;
    syncPaper();
  }

  function read(): Page {
    const p: Page = {};
    const paper = byId<HTMLSelectElement>("handoutPagePaper").value;
    if (paper === "custom") {
      const w = input("handoutPageWidth").value, h = input("handoutPageHeight").value;
      if (w) p.paperwidth = Number(w);
      if (h) p.paperheight = Number(h);
    } else if (PAPERS[paper]) {
      [p.paperwidth, p.paperheight] = PAPERS[paper];
    }
    for (const [key, id] of NUMBERS) {
      const v = input(id).value;
      if (v !== "") p[key] = Number(v);
    }
    const font = input("handoutPageFont").value.trim();
    if (font) p.font = font;
    if (!input("handoutPageAutoResize").checked) p.no_auto_resize_images = true;
    return p;
  }

  function open(scope: ListScope, onSaved: (json: string) => void): void {
    target = { scope, onSaved };
    fill(parsePage(scope.handoutPage));
    byId("handoutPageMessage").textContent = "";
    pageModal.open({ onClose: () => { target = null; } });
  }

  async function save(): Promise<void> {
    if (!target) return;
    const { scope, onSaved } = target;
    const msg = byId("handoutPageMessage");
    const page = read();
    const json = Object.keys(page).length ? JSON.stringify(page) : "";
    try {
      if (scope.group) await board.verbs.patch("patchGroup", `/api/list-groups/${scope.group.id}`, { handout_page: json });
      else await board.verbs.patch("patchList", `/api/lists/${scope.list.id}`, { handout_page: json });
      (scope.group ?? scope.list).handoutPage = json || null;
      onSaved(json);
      msg.textContent = S.board.handoutPage.saved();
      setTimeout(pageModal.close, CLOSE_AFTER_SAVE_MS);
    } catch (err) {
      msg.textContent = errMsg(err);
    }
  }

  byId("handoutPagePaper").addEventListener("change", syncPaper);
  byId("handoutPageForm").addEventListener("submit", (e) => { e.preventDefault(); void save(); });

  return { open };
}
