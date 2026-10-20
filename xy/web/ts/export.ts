// export.ts — the list-scope "Export": take the 4s document and the .hndt the list's
// cards assemble into (listexport.ts), gather any images referenced by
// `(img …)` directives from the cards' attachments, and hand both to the server,
// which composes the requested formats in memory and streams back one file — or
// a zip of the several that were ticked. The .docx and the .pdf render the same document: the PDF
// is typeset by typst to look like the docx (same layout, same non-breaking
// spaces/hyphens, same keep-together questions). See internal/server/exportpack.go.

import { xyApp } from "./app.js";
import { xySync } from "./sync.js";
import { xyChgk } from "./chgk.js";
import { xyListExport } from "./listexport.js";
import { modal } from "./modal.js";
import { createTelegramExport } from "./tgexport.js";
import type { Attachments } from "./attachments.js";
import type { Board, ListPanel, ListScope } from "./panels.js";
import type { BoardCard } from "./unlock.js";
import S from "./i18nstrings.js";

const { byId, errMsg, downloadBlob } = xyApp;

export function createExportPanel(board: Board, attachments: Pick<Attachments, "appendImages">): ListPanel {

  // The export modal's formats, in the order they are offered — the same
  // order in the one-format dropdown and in the zip's tick boxes. `server` marks
  // the ones that need the server to render, so offline can disable exactly those.
  // `everyGame: false` marks a format that can set only a ChGK tour: its
  // composer drops #T/#B/#R outright, so an SI, troika or brain package would
  // come back as a flat run of questions with no theme or battle headings, and
  // nothing would say so. Greyed out on such a tour — the same mechanism
  // offline uses on the server formats.
  const EXPORT_FORMATS = [
    { key: "4s", box: "exportFmt4s", server: false, everyGame: true },
    { key: "docx", box: "exportFmtDocx", server: true, everyGame: true },
    { key: "docx_screen", box: "exportFmtDocxScreen", server: true, everyGame: true },
    { key: "docx_spoilers", box: "exportFmtDocxSpoilers", server: true, everyGame: true },
    { key: "pdf", box: "exportFmtPdf", server: true, everyGame: true },
    { key: "pdf_mobile", box: "exportFmtPdfMobile", server: true, everyGame: true },
    { key: "pptx", box: "exportFmtPptx", server: true, everyGame: false },
    { key: "openquiz", box: "exportFmtOpenquiz", server: true, everyGame: false },
    { key: "handouts", box: "exportFmtHandouts", server: true, everyGame: false },
    // Telegram publishes rather than downloads, so it cannot join a zip of
    // files: it is offered in the dropdown and has no tick box of its own.
    { key: "telegram", box: "", server: true, everyGame: false },
  ] as const;
  // The .4s extension chgksuite's CLI reads each game back by (ext_to_game).
  const FOUR_S_EXT: Record<string, string> = { si: ".si4s", troika: ".tr4s", brain: ".br4s" };

  // The .docx and .pptx settings: chgksuite's compose switches, each sent as the
  // form field the server reads (exportopts.go). A checkbox sends "1" or "0",
  // since some of them are on by default. The last values are this reader's own
  // preference, so they live in localStorage; the uploaded files do not.
  const SETTINGS = [
    { id: "exportDocxAnswers", field: "docx_answers" },
    { id: "exportDocxScreen", field: "docx_screen_mode" },
    { id: "exportDocxNoParagraph", field: "docx_noparagraph" },
    { id: "exportDocxOnlyNumber", field: "docx_only_question_number" },
    { id: "exportDocxSameSize", field: "docx_same_source_size" },
    { id: "exportDocxOptimize", field: "docx_optimize" },
    { id: "exportDocxFont", field: "docx_font" },
    { id: "exportPptxDisableNumbers", field: "pptx_disable_numbers" },
    { id: "exportPptxKeepAccents", field: "pptx_keep_accents" },
    { id: "exportPptxOptimize", field: "pptx_optimize" },
    { id: "exportPptxFont", field: "pptx_font" },
  ] as const;
  const SETTING_FILES = [
    { id: "exportDocxTemplate", field: "docx_template" },
    { id: "exportPptxTemplate", field: "pptx_template" },
    { id: "exportPptxConfig", field: "pptx_config" },
  ] as const;
  const SETTINGS_KEY = "xy.export.settings";
  const DOCX_FORMATS = ["docx", "docx_screen", "docx_spoilers"];

  function settingValue(id: string): string {
    const el = byId<HTMLInputElement>(id);
    if (el.type === "checkbox") return el.checked ? "1" : "0";
    return el.value.trim();
  }

  function setSetting(id: string, v: string): void {
    const el = byId<HTMLInputElement>(id);
    if (el.type === "checkbox") el.checked = v === "1";
    else el.value = v;
  }

  function saveSettings(): void {
    const saved: Record<string, string> = {};
    for (const st of SETTINGS) saved[st.id] = settingValue(st.id);
    try { localStorage.setItem(SETTINGS_KEY, JSON.stringify(saved)); } catch (_) { /* the dialog's defaults come back next time */ }
  }

  function restoreSettings(): void {
    let saved: Record<string, string> = {};
    try { saved = JSON.parse(localStorage.getItem(SETTINGS_KEY) || "{}") as Record<string, string>; } catch (_) { return; }
    for (const st of SETTINGS) if (typeof saved[st.id] === "string") setSetting(st.id, saved[st.id]);
  }

  function appendSettings(fd: FormData, formats: string[]): void {
    const docx = formats.some((f) => DOCX_FORMATS.includes(f));
    const pptx = formats.includes("pptx");
    for (const st of SETTINGS) {
      if ((st.field.startsWith("docx_") && docx) || (st.field.startsWith("pptx_") && pptx)) fd.append(st.field, settingValue(st.id));
    }
    for (const f of SETTING_FILES) {
      const file = byId<HTMLInputElement>(f.id).files?.[0];
      if (file && ((f.field.startsWith("docx_") && docx) || (f.field.startsWith("pptx_") && pptx))) fd.append(f.field, file, file.name);
    }
  }

  const exportModal = modal("export");
  let exportCtx: { cards: BoardCard[]; title: string; hndt: string; game: string; commentFirst: boolean; handoutPage: string } | null = null;

  function exportBox(box: string): HTMLInputElement { return byId<HTMLInputElement>(box); }
  function oneFormat(): HTMLSelectElement { return byId<HTMLSelectElement>("exportOneFormat"); }
  function manyMode(): boolean { return byId<HTMLInputElement>("exportModeMany").checked; }

  // unavailable is what this list cannot be exported as right now, computed
  // once per open: it governs the tick boxes and the dropdown's options alike.
  let unavailable = new Set<string>();

  function exportChosen(): string[] {
    if (!manyMode()) {
      const key = oneFormat().value;
      return EXPORT_FORMATS.some((f) => f.key === key) && !unavailable.has(key) ? [key] : [];
    }
    return EXPORT_FORMATS.filter((f) => f.box && exportBox(f.box).checked && !unavailable.has(f.key)).map((f) => f.key);
  }

  // syncExportForm keeps the form honest: the tick boxes belong to the zip and
  // are gone without it, nothing chosen is nothing to do, and the toggle-all
  // label says which way it will go.
  function syncExportForm(): void {
    byId("exportFormats").hidden = !manyMode();
    const chosen = exportChosen();
    byId<HTMLButtonElement>("exportRun").disabled = chosen.length === 0;
    const available = EXPORT_FORMATS.filter((f) => f.box && !unavailable.has(f.key));
    const allOn = available.length > 0 && chosen.length === available.length;
    byId("exportToggleAll").textContent = allOn ? S.export.form.deselectAll() : S.export.form.selectAll();
    byId("exportDocxOpts").hidden = !chosen.some((f) => DOCX_FORMATS.includes(f));
    byId("exportPptxOpts").hidden = !chosen.includes("pptx");
    byId("exportHandoutsOpts").hidden = !chosen.includes("handouts");
  }

  function openExport(scope: ListScope): void {
    const hndt = xyListExport.hndtOf(scope.cards).source;
    exportCtx = { cards: scope.cards, title: scope.title, hndt, game: scope.game, commentFirst: scope.commentFirst, handoutPage: scope.handoutPage };

    // Offline everything but the .4s is unreachable: the other formats render
    // server-side, and even the .4s ships without its images (they are fetched).
    const offline = !xySync.isOnline();
    const chgk = scope.game === "chgk";
    unavailable = new Set(EXPORT_FORMATS
      .filter((f) => (offline && f.server) || (!chgk && !f.everyGame) || (f.key === "handouts" && !hndt.trim()))
      .map((f) => f.key));
    for (const f of EXPORT_FORMATS) {
      if (!f.box) continue;
      const box = exportBox(f.box);
      box.disabled = unavailable.has(f.key);
      if (box.disabled) box.checked = false;
    }
    // The dropdown offers the same formats, so it drops the same ones, and a
    // selection standing on one moves to the first that survived.
    const one = oneFormat();
    for (const opt of Array.from(one.options)) opt.disabled = unavailable.has(opt.value);
    if (unavailable.has(one.value)) one.value = EXPORT_FORMATS.find((f) => !unavailable.has(f.key))?.key ?? "";
    const notes: string[] = [];
    if (offline) notes.push(S.export.notes.offline());
    if (!chgk) notes.push(S.export.notes.gameFormats());
    else if (!hndt.trim()) notes.push(S.export.notes.noHandouts());
    syncExportForm();
    exportModal.open({ onClose: () => { exportCtx = null; } });
    exportModal.message(notes.join(" "));
  }

  // packForm is the body every export sends: the 4s, the name to hand back and
  // the pictures the chosen formats need. null is the reader calling it off over
  // pictures the board no longer has.
  async function packForm(): Promise<FormData | null> {
    if (!exportCtx) return null;
    const { cards, title } = exportCtx;
    const fd = new FormData();
    fd.append("source", xyListExport.exportSource(cards));
    fd.append("filename", title);
    fd.append("game", exportCtx.game);
    if (exportCtx.commentFirst) fd.append("comment_first", "1");
    const needed = xySync.isOnline() ? xyChgk.imageRefs(cards) : new Set<string>();
    const found = await attachments.appendImages(fd, cards, needed);
    const missing = [...needed].filter((n) => !found.has(n));
    if (missing.length && !confirm(S.export.run.missingImagesConfirm(missing.join(", ")))) return null;
    return fd;
  }

  const telegram = createTelegramExport({ boardId: () => board.id });

  // runExport renders the chosen formats. A bare .4s with no images never touches
  // the network — it is the one export that works offline.
  async function runExport(): Promise<void> {
    if (!exportCtx) return;
    const { cards, title, hndt, game, handoutPage } = exportCtx;
    const formats = exportChosen();
    if (!formats.length) return;
    const source = xyListExport.exportSource(cards);
    // Images are fetched (and decrypted) from the server, so offline there are
    // none to be had — the .4s then goes out as bare text rather than not at all.
    const wanted = xySync.isOnline() ? xyChgk.imageRefs(cards) : new Set<string>();
    const wantsImages = formats.includes("4s") && wanted.size > 0;

    if (formats.length === 1 && formats[0] === "4s" && !wantsImages) {
      downloadBlob(new Blob([source], { type: "text/plain;charset=utf-8" }), `${title}${FOUR_S_EXT[game] ?? ".4s"}`);
      exportModal.close();
      return;
    }

    const msg = byId("exportMessage");
    if (!xySync.requireOnline(S.export.run.offlineFormats(), msg)) return;
    // Telegram is a conversation, not a download: it hands over to its own
    // dialog, which asks where to post and then reports what the bot is doing.
    if (formats.length === 1 && formats[0] === "telegram") {
      msg.textContent = S.export.run.progress();
      const pack = await packForm();
      if (!pack) { msg.textContent = ""; return; }
      exportModal.close();
      await telegram.open(pack);
      return;
    }
    const btn = byId<HTMLButtonElement>("exportRun");
    btn.disabled = true;
    msg.textContent = formats.includes("handouts") ? S.export.run.progressHandouts() : S.export.run.progress();
    board.setStatus("saving");
    try {
      const fd = await packForm();
      if (!fd) {
        board.setStatus("saved");
        msg.textContent = "";
        return;
      }
      fd.append("formats", formats.join(","));
      appendSettings(fd, formats);
      if (formats.includes("handouts")) {
        fd.append("hndt", hndt);
        if (handoutPage) fd.append("page", handoutPage);
        const teams = byId<HTMLInputElement>("exportHandoutsTeams").value.trim();
        if (teams) fd.append("teams", teams);
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
      if (!f.box) continue;
      const box = exportBox(f.box);
      if (!box.disabled) box.checked = target;
    }
    syncExportForm();
  });
  for (const f of EXPORT_FORMATS) if (f.box) exportBox(f.box).addEventListener("change", syncExportForm);
  for (const id of ["exportModeOne", "exportModeMany", "exportOneFormat"]) byId(id).addEventListener("change", syncExportForm);
  restoreSettings();
  for (const st of SETTINGS) byId(st.id).addEventListener("change", saveSettings);


  return {
    id: "export", menu: "list", icon: "file-down",
    label: (scope) => scope.grouped ? S.export.menu.labelGrouped() : S.export.menu.label(),
    offered: (scope) => scope.cards.length > 0,
    open: openExport,
  };
}
