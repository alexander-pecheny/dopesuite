// handouts.ts — «Генерация раздаток» (chgksuite .hndt → PDF): the port of
// `chgksuite handouts 4s2hndt` (chgk.ts) builds an editable .hndt source from
// the list's questions, merging each question's saved layout settings
// (handout_meta) with its live handout text. «Сгенерировать PDF» posts the
// source + referenced images to the server, which typesets and streams an
// ephemeral PDF. On close the per-question settings (everything but the handout
// text) are persisted back.

import { xyApp } from "./app.js";
import { xyCrypto } from "./crypto.js";
import { xySync } from "./sync.js";
import { xyChgk } from "./chgk.js";
import { xyHandoutSession } from "./handoutsession.js";
import { namedUrl, revokeNamedUrl } from "./namedurl.js";
import { modal } from "./modal.js";
import type { Attachments } from "./attachments.js";
import type { Board, ListPanel, ListScope } from "./panels.js";
import type { BoardCard, BoardList } from "./unlock.js";
import type { OpBody } from "./store.js";

const { el, byId, errMsg, downloadBlob, onCmdEnter } = xyApp;

export function createHandoutsPanel(board: Board, attachments: Pick<Attachments, "appendImages">): ListPanel {
  const handoutsModal = modal("handouts");
  let handoutsCtx: { list: BoardList; cards: BoardCard[]; numbers: Array<string | null>; title: string } | null = null;   // { list, cards, numbers }
  let handoutsPdfUrl: string | null = null;
  let handoutsDlUrl: string | null = null;

  function openHandouts(scope: ListScope): void {
    // Grouped lists generate one set of handouts for the whole list_of_lists, with
    // question numbers continuous across the group (numberQuestionCards over the
    // concatenated cards), matching the board + docx export.
    const list = scope.list;
    const cards = scope.cards;
    const numbers = xyChgk.numberQuestionCards(cards);
    const metas: Record<number, string> = {};
    for (const c of cards) if (c.handoutMeta) metas[c.id] = c.handoutMeta;
    const source = xyChgk.generateHndt(cards, numbers, metas);
    handoutsCtx = { list, cards, numbers, title: scope.title };
    byId<HTMLTextAreaElement>("handoutsSource").value = source;
    clearHandoutsPdf();
    handoutsModal.open({ onClose: hideHandouts });
    handoutsModal.message(source.trim() ? "" : "В списке нет вопросов с раздаточным материалом.");
    // Pre-stage the referenced images now (in the background) so the first PDF /
    // split_fit generation doesn't pay the gather+upload, and start heartbeating.
    handoutSession.ensure(source).catch(() => {});
    handoutSession.startHeartbeat();
  }

  // WebKit won't render a PDF inside an <iframe> in a standalone web app (macOS
  // Dock app / iOS home-screen PWA — the preview pane comes up blank), and on
  // iOS even the in-browser iframe shows at most a flat first page. No Safari
  // setting changes this; the working path there is a top-level navigation, so
  // those contexts get an «Открыть PDF» button instead of the inline preview.
  function pdfInlinePreviewBroken(): boolean {
    const ua = navigator.userAgent;
    const ios = /iPad|iPhone|iPod/.test(ua) || (navigator.platform === "MacIntel" && navigator.maxTouchPoints > 1);
    const webkitOnly = /AppleWebKit/.test(ua) && !/Chrome|CriOS|EdgiOS|FxiOS|Android/.test(ua);
    const standalone = (navigator as { standalone?: boolean }).standalone === true || (typeof matchMedia === "function" && matchMedia("(display-mode: standalone)").matches);
    return ios || (webkitOnly && standalone);
  }

  function pdfPreviewNode(url: string): HTMLElement {
    if (!pdfInlinePreviewBroken()) return el("iframe", { class: "handouts-pdf-frame", src: url, title: "PDF" });
    return el("div", { class: "handouts-pdf-fallback" },
      el("div", { class: "handouts-pdf-note", text: "Safari не показывает PDF внутри приложения." }),
      el("a", { class: "btn", href: url, target: "_blank", rel: "noopener", text: "Открыть PDF" }));
  }

  function clearHandoutsPdf(): void {
    const pane = byId("handoutsPdf");
    pane.replaceChildren();
    const dl = byId<HTMLAnchorElement>("handoutsDownload");
    dl.hidden = true;
    if (handoutsPdfUrl) { revokeNamedUrl(handoutsPdfUrl); handoutsPdfUrl = null; }
    if (handoutsDlUrl) { URL.revokeObjectURL(handoutsDlUrl); handoutsDlUrl = null; }
  }

  // handoutFileBase names a generated раздатка after the board and the list it came
  // from — «Моя_доска_Тур_1_handouts» — rather than after nothing in particular
  // (issue #43). Only path separators and whitespace are folded away: the name is
  // the one the editor typed, Cyrillic included, and every download it rides on
  // spells it in UTF-8.
  function handoutFileBase(): string {
    const clean = (s: string): string => s.trim().replace(/[\\/\s]+/g, "_");
    const list = (handoutsCtx && (handoutsCtx.title || handoutsCtx.list.title)) || "";
    return [clean(board.state.name), clean(list), "handouts"].filter(Boolean).join("_");
  }

  // persistHandoutMeta writes the edited per-question settings back onto the cards
  // (everything in each .hndt block except the live handout text/image), so the
  // layout is restored next time the modal opens.
  async function persistHandoutMeta(): Promise<void> {
    if (!handoutsCtx) return;
    const source = byId<HTMLTextAreaElement>("handoutsSource").value;
    const byNumber = xyChgk.parseHndtMetaByQuestion(source);
    const { cards, numbers } = handoutsCtx;
    for (let i = 0; i < cards.length; i++) {
      const c = cards[i];
      if (c.kind !== "question") continue;
      const num = numbers[i];
      if (num == null || !(String(num) in byNumber)) continue;
      const meta = byNumber[String(num)] || null;
      const norm = meta && meta.trim() ? meta : null;
      if (norm === (c.handoutMeta || null)) continue;
      try {
        const body: OpBody = { handout_meta_enc: norm ? await xyCrypto.encField(board.dk(), norm) : "" };
        await board.verbs.patch("patchCard", `/api/cards/${c.id}`, body);
        c.handoutMeta = norm;
      } catch (_) { /* best-effort: keep editing even if a write fails */ }
    }
  }

  async function hideHandouts(): Promise<void> {
    void handoutSession.close(); // stop heartbeat + delete the staged images server-side
    await persistHandoutMeta();
    clearHandoutsPdf();
    handoutsCtx = null;
  }

  async function generateHandoutsPdf(): Promise<void> {
    if (!handoutsCtx) return;
    if (!xySync.requireOnline("Генерация PDF доступна только онлайн.", byId("handoutsMessage"))) return;
    const source = byId<HTMLTextAreaElement>("handoutsSource").value;
    const msg = byId("handoutsMessage");
    if (!source.trim()) { msg.textContent = "Пустой источник."; return; }
    const btn = byId<HTMLButtonElement>("handoutsGenerate");
    btn.disabled = true;
    msg.textContent = "Генерация…";
    clearHandoutsPdf();
    try {
      const fd = await handoutsBody(source);
      const res = await fetch("/api/handouts/pdf", { method: "POST", credentials: "same-origin", body: fd });
      if (!res.ok) throw new Error((await res.text()).trim() || `HTTP ${res.status}`);
      const name = handoutFileBase() + ".pdf";
      const blob = await res.blob();
      handoutsPdfUrl = await namedUrl(blob, name);
      byId("handoutsPdf").replaceChildren(pdfPreviewNode(handoutsPdfUrl));
      // Only the preview needs /dl/ (the viewer's Save name); Chromium re-issues a
      // download outside the worker, where that path 404s — so the button gets a blob.
      handoutsDlUrl = URL.createObjectURL(blob);
      const dl = byId<HTMLAnchorElement>("handoutsDownload");
      dl.href = handoutsDlUrl;
      dl.setAttribute("download", name);
      dl.hidden = false;
      msg.textContent = "Готово.";
    } catch (err) {
      msg.textContent = "Не удалось сгенерировать: " + errMsg(err);
    } finally {
      btn.disabled = false;
    }
  }

  // ---- handout image staging (server-side cache) ----
  // Opening the modal uploads the referenced images to the server once; every PDF
  // / split_fit generation then just references the session id, so the images
  // aren't re-decrypted + re-uploaded each time (which dominated the latency). A 5s
  // heartbeat keeps the session alive; the server reaps it after ~1 min of silence
  // (tab closed / backgrounded), and we re-stage on demand if it lapsed.
  function wantedImages(source: string): Set<string> {
    const wanted = new Set<string>();
    for (const m of source.matchAll(/^\s*image:\s*(.+?)\s*$/gm)) wanted.add(m[1]);
    for (const n of xyChgk.imgRefs(source)) wanted.add(n);
    return wanted;
  }

  // stageImages gathers + decrypts the referenced images and uploads them to a new
  // server session, returning { session, names } (null when there are none / on
  // error). The session lifecycle around it lives in handoutSession.
  async function stageImages(source: string): Promise<{ session: string; names: Set<string> } | null> {
    if (!handoutsCtx) return null;
    const wanted = wantedImages(source);
    if (!wanted.size) return null;
    const fd = new FormData();
    const found = await attachments.appendImages(fd, handoutsCtx.cards, wanted);
    try {
      const res = await fetch("/api/handouts/stage", { method: "POST", credentials: "same-origin", body: fd });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const { session } = (await res.json()) as { session: string };
      return { session, names: found };
    } catch (_) { return null; }
  }

  async function heartbeatPing(sessionId: string): Promise<boolean> {
    try {
      const fd = new FormData();
      fd.append("session", sessionId);
      const res = await fetch("/api/handouts/heartbeat", { method: "POST", credentials: "same-origin", body: fd });
      return res.ok;
    } catch (_) { return false; }
  }

  async function unstageSession(sessionId: string): Promise<void> {
    try { await fetch(`/api/handouts/stage?session=${encodeURIComponent(sessionId)}`, { method: "DELETE", credentials: "same-origin" }); } catch (_) {}
  }

  // handoutSession owns the stage-once/heartbeat/reap/cleanup lifecycle (see
  // handoutsession.js); the callbacks above are the board-specific network ops.
  const handoutSession = xyHandoutSession.create({
    wantedNames: wantedImages,
    stage: stageImages,
    heartbeat: heartbeatPing,
    unstage: unstageSession,
  });

  // handoutsBody builds the generate request body: the source + (when there are
  // images) the staged session id, so images aren't re-sent each generate.
  async function handoutsBody(source: string): Promise<FormData> {
    const fd = new FormData();
    fd.append("source", source);
    fd.append("filename", (handoutsCtx && (handoutsCtx.title || handoutsCtx.list.title)) || "handouts");
    const sid = await handoutSession.ensure(source);
    if (sid) fd.append("session", sid);
    return fd;
  }

  // Revive the staged session when the user returns to a backgrounded tab (its
  // heartbeats may have lapsed and the server reaped it).
  document.addEventListener("visibilitychange", async () => {
    if (document.visibilityState !== "visible" || !handoutsModal.isOpen || !handoutsCtx) return;
    if (!(await handoutSession.beat())) handoutSession.ensure(byId<HTMLTextAreaElement>("handoutsSource").value).catch(() => {});
  });


  // generateSplitFitZip runs chgksuite's split_fit on the current .hndt (pages each
  // handout to fit, one fitted PDF per question + an all-questions PDF) and hands
  // the user a zip of all the PDFs. Online-only (shells out server-side).
  async function generateSplitFitZip(): Promise<void> {
    if (!handoutsCtx) return;
    const msg = byId("handoutsMessage");
    if (!xySync.requireOnline("Split-fit доступен только онлайн.", msg)) return;
    const source = byId<HTMLTextAreaElement>("handoutsSource").value;
    if (!source.trim()) { msg.textContent = "Пустой источник."; return; }
    const btn = byId<HTMLButtonElement>("handoutsSplitFit");
    btn.disabled = true;
    msg.textContent = "Split-fit… (подбор раскладки может занять время)";
    try {
      const fd = await handoutsBody(source);
      const res = await fetch("/api/handouts/split_fit", { method: "POST", credentials: "same-origin", body: fd });
      if (!res.ok) throw new Error((await res.text()).trim() || `HTTP ${res.status}`);
      downloadBlob(await res.blob(), handoutFileBase() + ".zip");
      msg.textContent = "Готово — zip со всеми PDF скачан.";
    } catch (err) {
      msg.textContent = "Split-fit не удался: " + errMsg(err);
    } finally {
      btn.disabled = false;
    }
  }

  byId("handoutsGenerate").addEventListener("click", () => { void generateHandoutsPdf(); });
  // Edit the .hndt, regenerate, look: Cmd/Ctrl-Enter is that loop without the trip
  // to the button.
  onCmdEnter(byId("handoutsSource"), () => byId("handoutsGenerate").click());
  byId("handoutsSplitFit").addEventListener("click", () => { void generateSplitFitZip(); });


  return {
    id: "handouts", menu: "list", icon: "file-text",
    label: (scope) => scope.group ? "Генерация раздаток (вся группа)" : "Генерация раздаток",
    open: openHandouts,
  };
}
