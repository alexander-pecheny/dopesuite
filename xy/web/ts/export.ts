// export.ts — the list-scope "Export": concatenate the list's card descriptions (in board
// order) into a chgksuite "4s" document, gather any images referenced by
// `(img …)` directives from the cards' attachments, and hand both to the server,
// which composes the requested formats in memory and streams back one file — or
// a zip of all of them. The .docx and the .pdf render the same document: the PDF
// is typeset by typst to look like the docx (same layout, same non-breaking
// spaces/hyphens, same keep-together questions). See internal/server/exportpack.go.

import { xyApp } from "./app.js";
import { xySync } from "./sync.js";
import { xyChgk } from "./chgk.js";
import { xyVersions } from "./versions.js";
import { xyHndt } from "./hndt.js";
import { modal } from "./modal.js";
import type { Attachments } from "./attachments.js";
import type { Board, ListPanel, ListScope } from "./panels.js";
import type { BoardCard } from "./unlock.js";
import S from "./i18nstrings.js";

const { byId, errMsg, downloadBlob } = xyApp;

// exportSource is the 4s document a list exports as: its cards' descriptions in
// board order, blank-line separated. Every format is rendered from this one
// string, which is why the versions are folded back into one question block here
// and nowhere else — a versioned card is still one numbered question.
export function exportSource(cards: ReadonlyArray<BoardCard>): string {
  return cards.map((c) => foldBlankLines(xyVersions.composeVersions(c.desc).trim())).filter(Boolean).join("\n\n") + "\n";
}

// A blank line inside a card is xy's own liberty: to 4s it ends the element, so
// everything past it — the rest of the question, and any field after it — falls
// out of the docx. Each one becomes chgksuite's explicit (LINEBREAK) on the end
// of the line before, which keeps the field one element and the empty line
// visible (the directive plus the newline the join keeps = two breaks). Before a
// marker the blank line separates nothing that is printed, so it just goes.
function foldBlankLines(desc: string): string {
  const out: string[] = [];
  let blanks = 0;
  for (const line of desc.split("\n")) {
    if (!line.trim()) { blanks++; continue; }
    if (blanks && out.length && !xyChgk.startsBlock(line)) out[out.length - 1] += "(LINEBREAK)".repeat(blanks);
    out.push(line);
    blanks = 0;
  }
  return out.join("\n");
}

export function createExportPanel(board: Board, attachments: Pick<Attachments, "appendImages">): ListPanel {

  // The export modal's five formats, in the order they are offered. `server` marks
  // the ones that need the server to render, so offline can disable exactly those.
  const EXPORT_FORMATS = [
    { key: "4s", box: "exportFmt4s", server: false },
    { key: "docx", box: "exportFmtDocx", server: true },
    { key: "pdf", box: "exportFmtPdf", server: true },
    { key: "pdf_mobile", box: "exportFmtPdfMobile", server: true },
    { key: "handouts", box: "exportFmtHandouts", server: true },
  ] as const;

  const exportModal = modal("export");
  let exportCtx: { cards: BoardCard[]; title: string; hndt: string } | null = null;

  function exportBox(box: string): HTMLInputElement { return byId<HTMLInputElement>(box); }
  function exportChosen(): string[] {
    return EXPORT_FORMATS.filter((f) => exportBox(f.box).checked && !exportBox(f.box).disabled).map((f) => f.key);
  }

  // syncExportForm keeps the button row honest: nothing ticked is nothing to do,
  // and the toggle-all label says which way it will go.
  function syncExportForm(): void {
    const chosen = exportChosen();
    byId<HTMLButtonElement>("exportRun").disabled = chosen.length === 0;
    const available = EXPORT_FORMATS.filter((f) => !exportBox(f.box).disabled);
    const allOn = available.length > 0 && chosen.length === available.length;
    byId("exportToggleAll").textContent = allOn ? S.export.form.deselectAll() : S.export.form.selectAll();
  }

  function openExport(scope: ListScope): void {
    const hndt = xyHndt.hndtOf(scope.cards).source;
    exportCtx = { cards: scope.cards, title: scope.title, hndt };

    // Offline everything but the .4s is unreachable: the other formats render
    // server-side, and even the .4s ships without its images (they are fetched).
    const offline = !xySync.isOnline();
    for (const f of EXPORT_FORMATS) {
      const box = exportBox(f.box);
      box.disabled = (offline && f.server) || (f.key === "handouts" && !hndt.trim());
      if (box.disabled) box.checked = false;
    }
    const notes: string[] = [];
    if (offline) notes.push(S.export.notes.offline());
    if (!hndt.trim()) notes.push(S.export.notes.noHandouts());
    syncExportForm();
    exportModal.open({ onClose: () => { exportCtx = null; } });
    exportModal.message(notes.join(" "));
  }

  // runExport renders the ticked formats. A bare .4s with no images never touches
  // the network — it is the one export that works offline.
  async function runExport(): Promise<void> {
    if (!exportCtx) return;
    const { cards, title, hndt } = exportCtx;
    const formats = exportChosen();
    if (!formats.length) return;
    const source = exportSource(cards);
    // Images are fetched (and decrypted) from the server, so offline there are
    // none to be had — the .4s then goes out as bare text rather than not at all.
    const wanted = xySync.isOnline() ? xyChgk.imageRefs(cards) : new Set<string>();
    const wantsImages = formats.includes("4s") && wanted.size > 0;

    if (formats.length === 1 && formats[0] === "4s" && !wantsImages) {
      downloadBlob(new Blob([source], { type: "text/plain;charset=utf-8" }), `${title}.4s`);
      exportModal.close();
      return;
    }

    const msg = byId("exportMessage");
    if (!xySync.requireOnline(S.export.run.offlineFormats(), msg)) return;
    const btn = byId<HTMLButtonElement>("exportRun");
    btn.disabled = true;
    msg.textContent = formats.includes("handouts") ? S.export.run.progressHandouts() : S.export.run.progress();
    board.setStatus("saving");
    try {
      const fd = new FormData();
      fd.append("source", source);
      fd.append("filename", title);
      fd.append("formats", formats.join(","));
      if (formats.includes("handouts")) fd.append("hndt", hndt);

      // Images are only shipped for the .4s (which references them by name);
      // docx and pdf embed their own copies, so nothing else needs the upload.
      const needed = new Set<string>();
      if (formats.some((f) => f !== "4s") || wantsImages) for (const n of wanted) needed.add(n);
      const found = await attachments.appendImages(fd, cards, needed);
      const missing = [...needed].filter((n) => !found.has(n));
      if (missing.length && !confirm(S.export.run.missingImagesConfirm(missing.join(", ")))) {
        board.setStatus("saved");
        msg.textContent = "";
        return;
      }
      const res = await fetch("/api/export/pack", { method: "POST", credentials: "same-origin", body: fd });
      if (!res.ok) throw new Error((await res.text()).trim() || `HTTP ${res.status}`);
      downloadBlob(await res.blob(), filenameFromResponse(res) || `${title}.zip`);
      board.setStatus("saved");
      exportModal.close();
    } catch (err) {
      board.setStatus("error");
      msg.textContent = S.export.run.failed(errMsg(err));
    } finally {
      btn.disabled = false;
      syncExportForm();
    }
  }

  // filenameFromResponse reads the name the server chose, so a single-format pack
  // arrives as foo.docx rather than foo.zip.
  function filenameFromResponse(res: Response): string {
    const m = /filename="([^"]+)"/.exec(res.headers.get("Content-Disposition") || "");
    return m ? m[1] : "";
  }


  byId("exportForm").addEventListener("submit", (e) => { e.preventDefault(); void runExport(); });
  byId("exportToggleAll").addEventListener("click", () => {
    const target = byId("exportToggleAll").textContent === S.export.form.selectAll();
    for (const f of EXPORT_FORMATS) {
      const box = exportBox(f.box);
      if (!box.disabled) box.checked = target;
    }
    syncExportForm();
  });
  for (const f of EXPORT_FORMATS) exportBox(f.box).addEventListener("change", syncExportForm);


  return {
    id: "export", menu: "list", icon: "file-down",
    label: (scope) => scope.grouped ? S.export.menu.labelGrouped() : S.export.menu.label(),
    offered: (scope) => scope.cards.length > 0,
    open: openExport,
  };
}
