// importpack.ts — "Import": the board's one file picker. What the file IS is
// ours to work out, not the reader's to declare — an xy archive goes to
// bundleimportpanel.ts to be appended to this board (ADR-0013), and everything
// else is a package of questions for a new list.
//
// A package (.4s / .zip / .docx) becomes a new list. The
// server parses the upload with the Go port of chgksuite's parser
// (internal/chgk/chgkimport) and hands back 4s source plus the images it
// references. Everything below happens client-side under the board key: the
// list, its cards and the image attachments are all encrypted before they go
// back up.
//
// A .4s (or a .zip of one plus its images) is already in our own format, so it
// imports straight away. A .docx has been through a lossy heuristic parse, so it
// goes to the verification screen first.

import { xyApp } from "./app.js";
import { xyCrypto } from "./crypto.js";
import { xySync } from "./sync.js";
import { xyChgk } from "./chgk.js";
import { xyRank } from "./rank.js";
import { byRank } from "./dragrank.js";
import { nowStamp } from "./carddetail.js";
import { modal } from "./modal.js";
import { sniffBundle } from "./bundleimport.js";
import type { BundleImport } from "./bundleimportpanel.js";
import type { Board, BoardPanel } from "./panels.js";
import S from "./i18nstrings.js";

const { jpost, byId, errMsg } = xyApp;
const { keyBetween } = xyRank;

// The verification screen renders the parsed blocks the way the list preview
// does; the renderer is the board's.
export type PreviewRenderer = (card: { id: number; kind: string; desc: string }, number: string | null, imgMap: Map<string, string>, screen: boolean) => HTMLElement;

export function createImportPanel(board: Board, renderPreviewCard: PreviewRenderer, bundles: BundleImport): BoardPanel {
  interface ImportImage { name: string; data: string; mime: string }
  interface ImportCard { id: number; kind: string; desc: string }
  interface ImportPkg { name: string; source: string; images?: ImportImage[] }

  // importCtx holds the package awaiting confirmation on the verification screen.
  let importCtx: { name: string; images: ImportImage[]; imgMap: Map<string, string>; splitTours: boolean } | null = null;

  const importPickModal = modal("importPick");

  // The picker recognises the file as soon as it is chosen, so the reader sees
  // what will happen before pressing anything — and "split by tours", which
  // means nothing to an archive, goes away when one is picked.
  let sniffed: Awaited<ReturnType<typeof sniffBundle>> = null;

  function openImportPick(): void {
    byId<HTMLFormElement>("importPickForm").reset();
    sniffed = null;
    showKind("");
    importPickModal.open();
  }

  function showKind(what: string): void {
    byId("importPickWhat").textContent = what;
    const tours = byId("importSplitTours").closest("label");
    if (tours) (tours as HTMLElement).hidden = sniffed != null;
  }

  byId("importFile").addEventListener("change", () => {
    const file = byId<HTMLInputElement>("importFile").files?.[0];
    sniffed = null;
    showKind("");
    if (!file) return;
    showKind(S.import.pack.readingFile());
    void sniffBundle(file).then((b) => {
      sniffed = b;
      showKind(b
        ? S.import.pack.sniffedArchive(b.bundle.board.name, b.bundle.exported_at.slice(0, 10))
        : S.import.pack.sniffedPackage());
    });
  });

  byId("importPickForm").addEventListener("submit", async (e) => {
    e.preventDefault();
    const files = byId<HTMLInputElement>("importFile").files;
    const file = files && files[0];
    if (!file) return;
    const splitTours = byId<HTMLInputElement>("importSplitTours").checked;
    importPickModal.close();
    const bundle = sniffed ?? await sniffBundle(file);
    if (bundle) {
      bundles.openWith(file, bundle.bundle, bundle.bytesOf);
      return;
    }
    await importFile(file, splitTours);
  });

  async function importFile(file: File, splitTours: boolean): Promise<void> {
    if (!xySync.requireOnline(S.import.pack.offline())) return;
    board.setStatus("saving");
    try {
      const fd = new FormData();
      fd.append("file", file, file.name);
      const res = await fetch("/api/import/parse", { method: "POST", credentials: "same-origin", body: fd });
      if (!res.ok) throw new Error((await res.text()).trim() || `HTTP ${res.status}`);
      const pkg = (await res.json()) as ImportPkg;
      board.setStatus("saved");
      // A .docx parse is a guess; let the user check it before it becomes a list.
      if (/\.docx$/i.test(file.name)) openImportVerify(pkg, splitTours);
      else await commitImport(pkg.name, pkg.source, pkg.images, splitTours);
    } catch (err) {
      board.setStatus("error");
      alert(S.import.pack.parseFailed(errMsg(err)));
    }
  }

  // ---- verification screen (docx) ----

  const importModal = modal("import");

  // importCards splits 4s source the way the export path joins it: one card per
  // blank-line-separated block. Each card's kind comes from its leading marker.
  function importCards(source: string): ImportCard[] {
    return source
      .split(/\n[ \t]*\n/)
      .map((b) => b.trim())
      .filter(Boolean)
      .map((desc, i) => ({ id: -(i + 1), kind: importKind(desc), desc }));
  }

  // importKind maps a 4s block to an xy card kind. A question is recognised by its
  // fields, not by its first line: compose_4s puts the "№ N" directive ahead of the
  // "? …" marker, and an unmarked block ("pre") is question text whose author
  // didn't prefix it.
  function importKind(desc: string): string {
    const blocks = xyChgk.parseBlocks(desc);
    if (blocks.some((b) => b.type === "question" || b.type === "answer" || b.type === "pre")) return "question";
    if (blocks.some((b) => b.type === "heading" || b.type === "ljheading")) return "heading";
    return "meta";
  }

  // importImgMap turns the package's base64 images into object URLs so the preview
  // can show handouts exactly as the list will once imported.
  function importImgMap(images: ImportImage[] | undefined): Map<string, string> {
    const map = new Map<string, string>();
    for (const img of images || []) {
      const bytes = Uint8Array.from(atob(img.data), (c) => c.charCodeAt(0));
      map.set(img.name, URL.createObjectURL(new Blob([bytes], { type: img.mime })));
    }
    return map;
  }

  function openImportVerify(pkg: ImportPkg, splitTours: boolean): void {
    importModal.close();
    importCtx = { name: pkg.name, images: pkg.images || [], imgMap: importImgMap(pkg.images), splitTours };
    byId("importTitle").textContent = S.import.pack.verifyTitle(pkg.name);
    const src = byId<HTMLTextAreaElement>("importSource");
    src.value = pkg.source;
    importModal.open({ onClose: hideImportVerify });
    renderImportPreview();
    src.focus();
    // Focusing puts the caret at the end; the user wants to read from the top.
    src.setSelectionRange(0, 0);
    src.scrollTop = 0;
  }

  // renderImportPreview re-renders the right pane from whatever is in the editor,
  // using the same renderer the list preview uses — so what you check is what you get.
  function renderImportPreview(): void {
    const ctx = importCtx;
    if (!ctx) return;
    const body = byId("importPreview");
    const cards = importCards(byId<HTMLTextAreaElement>("importSource").value);
    const numbers = xyChgk.numberQuestionCards(cards);
    body.replaceChildren();
    cards.forEach((card, i) => body.append(renderPreviewCard(card, numbers[i], ctx.imgMap, false)));
    const qs = cards.filter((c) => c.kind === "question").length;
    byId("importCount").textContent = S.import.pack.blockCount(String(cards.length), String(qs));
  }

  function hideImportVerify(): void {
    if (importCtx) for (const url of importCtx.imgMap.values()) URL.revokeObjectURL(url);
    importCtx = null;
    byId("importPreview").replaceChildren();
  }

  byId("importSource").addEventListener("input", debounceImportPreview());
  byId("importCommit").addEventListener("click", async () => {
    if (!importCtx) return;
    const { name, images, splitTours } = importCtx;
    const source = byId<HTMLTextAreaElement>("importSource").value;
    importModal.close();
    await commitImport(name, source, images, splitTours);
  });

  // Re-rendering the whole preview on every keystroke is wasteful on a big package.
  function debounceImportPreview(): () => void {
    let t: ReturnType<typeof setTimeout> | null = null;
    return () => {
      if (t) clearTimeout(t);
      t = setTimeout(() => { if (importCtx) renderImportPreview(); }, 200);
    };
  }

  // ---- commit: 4s source + images → a new encrypted list (or a group of them) ----

  // splitCardsByTours groups the blocks into tours: a "## …" section block starts
  // a new tour and names its list (the section card itself is kept, so the 4s
  // source survives export intact). Blocks before the first section — usually the
  // editors/date preamble — become their own leading list.
  function splitCardsByTours(cards: ImportCard[]): Array<{ title: string; cards: ImportCard[] }> {
    const tours: Array<{ title: string; cards: ImportCard[] }> = [];
    let cur: { title: string; cards: ImportCard[] } | null = null;
    for (const c of cards) {
      const sec = xyChgk.parseBlocks(c.desc).find((b) => b.type === "section");
      if (sec) {
        cur = { title: sec.text.split("\n")[0].trim() || S.import.pack.tourFallback(String(tours.length + 1)), cards: [] };
        tours.push(cur);
      } else if (!cur) {
        cur = { title: S.import.pack.preamble(), cards: [] };
        tours.push(cur);
      }
      cur.cards.push(c);
    }
    return tours;
  }

  // commitImport creates the list(s), one card per 4s block, and attaches each
  // image to the card whose text references it via an `(img …)` directive. With
  // splitTours on, each tour becomes its own list and the lists are linked into a
  // list group — continuous numbering and combined export across tours.
  //
  // The lists and cards are posted directly (jpost), not through the sync
  // outbox: an import is online-only anyway, and mutate() hands back a negative
  // temp id whenever the queue is non-empty — which the attachment upload, a plain
  // POST to /api/cards/{id}/attachments, cannot use. Going direct keeps every id real.
  async function commitImport(name: string, source: string, images: ImportImage[] | undefined, splitTours: boolean): Promise<void> {
    const cards = importCards(source);
    if (!cards.length) { alert(S.import.pack.noQuestions()); return; }
    if (!xySync.requireOnline(S.import.pack.offline())) return;
    const tours = splitTours ? splitCardsByTours(cards) : [];
    // The server refuses a group of one, and a group of one is pointless anyway.
    const grouped = tours.length >= 2;
    const title = (prompt(grouped ? S.import.pack.promptGroup() : S.import.pack.promptList(), name || S.import.pack.defaultTitle()) || "").trim();
    if (!title) return;
    const parts = grouped ? tours : [{ title, cards }];

    board.setStatus("saving");
    const byName = new Map((images || []).map((i): [string, ImportImage] => [i.name, i]));
    let done = 0, attached = 0;
    const failed: string[] = []; // images the server refused — the card would keep a dead (img …)
    try {
      const key = board.dk();
      const ranks = [...board.state.lists].sort(byRank);
      let rank: string | null = ranks.length ? ranks[ranks.length - 1].rank : null;
      const listIds: number[] = [];
      for (const part of parts) {
        rank = keyBetween(rank, null);
        const lres = (await jpost(`/api/boards/${board.id}/lists`, {
          title_enc: await xyCrypto.encField(key, part.title), rank, type: "normal",
        })) as { id: number };
        listIds.push(lres.id);
        board.state.lists.push({ id: lres.id, type: "normal", rank, groupId: null, title: part.title });

        let cardRank: string | null = null;
        for (const c of part.cards) {
          cardRank = keyBetween(cardRank, null);
          const res = (await jpost(`/api/lists/${lres.id}/cards`, {
            description_enc: await xyCrypto.encField(key, c.desc), rank: cardRank, kind: c.kind,
          })) as { id: number };
          board.state.cards.push({ id: res.id, listId: lres.id, kind: c.kind, rank: cardRank, desc: c.desc, handoutMeta: null, alias: null, createdAt: nowStamp() });
          done++;
          // Attach only the images this card actually references, so a handout lands
          // on the question that uses it (which is where the preview/export look).
          for (const ref of xyChgk.imageRefs([c])) {
            const img = byName.get(ref);
            if (!img) continue;
            if (await attachImported(res.id, img)) attached++;
            else failed.push(ref);
          }
        }
      }
      if (grouped) {
        await jpost(`/api/boards/${board.id}/list-groups`, { name_enc: await xyCrypto.encField(key, title), list_ids: listIds });
        // Reload rather than mirror group_id/groups[] locally — import is online-only.
        await board.reload();
      } else board.render();
      board.setStatus("saved");
      let msg = grouped
        ? S.import.pack.doneGrouped(String(parts.length), String(done), String(attached))
        : S.import.pack.doneSingle(String(done), String(attached));
      if (splitTours && !grouped) msg += S.import.pack.noToursNote();
      // A dropped image is invisible otherwise: the card keeps its (img …) directive
      // but the picture is gone, and the parse response is not kept to retry from.
      if (failed.length) msg += S.import.pack.imagesFailed(String(failed.length)) + failed.join(", ");
      alert(msg);
    } catch (err) {
      // The lists and the cards created so far are already on the server — show them
      // rather than leaving the board looking as if nothing happened.
      board.render();
      board.setStatus("error");
      alert(S.import.pack.aborted(String(done), errMsg(err)));
    }
  }

  // attachImported encrypts one imported image and posts it as an attachment of
  // `cardId`, under the same filename the (img …) directive refers to. Lossless:
  // re-encoding would change nothing but could degrade a handout. Returns false (and
  // lets the caller report it) if the server rejects it — e.g. an oversized scan.
  async function attachImported(cardId: number, img: ImportImage): Promise<boolean> {
    try {
      const key = board.dk();
      const bytes = Uint8Array.from(atob(img.data), (c) => c.charCodeAt(0));
      const cipher = await xyCrypto.encBytes(key, bytes);
      const fd = new FormData();
      fd.append("meta", JSON.stringify({
        filename_enc: await xyCrypto.encField(key, img.name),
        mime: img.mime, lossless: true,
        event_payload_enc: await xyCrypto.encField(key, JSON.stringify({ file: img.name })),
      }));
      fd.append("blob", new Blob([cipher], { type: "application/octet-stream" }), "blob");
      const res = await fetch(`/api/cards/${cardId}/attachments`, {
        method: "POST", credentials: "same-origin", body: fd,
      });
      return res.ok;
    } catch (_) { return false; }
  }


  return {
    id: "import", menu: "board", icon: "file-up",
    label: S.import.pack.label(),
    title: S.import.pack.menuTitle(),
    open: openImportPick,
  };
}
