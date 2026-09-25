// handouts.ts — "Handout generation" (chgksuite .hndt -> PDF): the port of
// `chgksuite handouts 4s2hndt` (hndt.ts) builds an editable .hndt source from
// the list's questions, merging each question's saved layout settings
// (handout_meta) with its live handout text. "Generate PDF" posts the
// source + referenced images to the server, which typesets and streams an
// ephemeral PDF. On close the per-question settings (everything but the handout
// text) are persisted back. The .hndt is edited either as it is or through
// the fields view, a form over the same text (hndt.ts parseHndtForm/composeHndtForm).

import S from "./i18nstrings.js";
import { xyApp } from "./app.js";
import { xyCrypto } from "./crypto.js";
import { xySync } from "./sync.js";
import { xyChgk } from "./chgk.js";
import { xyHndt } from "./hndt.js";
import { xyHandoutSession } from "./handoutsession.js";
import { namedUrl, revokeNamedUrl } from "./namedurl.js";
import { modal } from "./modal.js";
import { icon } from "./icons_gen.js";
import type { Attachments } from "./attachments.js";
import type { HndtFormBlock } from "./hndt.js";
import type { Board, ListPanel, ListScope } from "./panels.js";
import type { BoardCard } from "./unlock.js";
import type { OpBody } from "./store.js";

const { el, byId, errMsg, downloadBlob, onCmdEnter } = xyApp;

export function createHandoutsPanel(board: Board, attachments: Pick<Attachments, "appendImages" | "cardAttachments">): ListPanel {
  const handoutsModal = modal("handouts");
  let handoutsCtx: { cards: BoardCard[]; numbers: Array<string | null>; title: string } | null = null;
  let handoutsPdfUrl: string | null = null;
  let handoutsDlUrl: string | null = null;

  function openHandouts(scope: ListScope): void {
    // Grouped lists generate one set of handouts for the whole list_of_lists, with
    // question numbers continuous across the group (numberQuestionCards over the
    // concatenated cards), matching the board + docx export.
    const cards = scope.cards;
    const { numbers, source } = xyHndt.hndtOf(cards);
    handoutsCtx = { cards, numbers, title: scope.title };
    byId<HTMLTextAreaElement>("handoutsSource").value = source;
    clearHandoutsPdf();
    handoutsModal.open({ onClose: hideHandouts });
    // After the modal is shown: the form measures its text boxes as it draws.
    setView("fields");
    // Pre-stage the referenced images now (in the background) so the first PDF /
    // split_fit generation doesn't pay the gather+upload, and start heartbeating.
    handoutSession.ensure(source).catch(() => {});
    handoutSession.startHeartbeat();
  }

  // ---- the fields view: the .hndt as a form ----
  // The textarea stays the one document: every edit in the form is composed
  // straight back into it, and switching to the fields re-reads it, so an edit made
  // in the text shows up in the form and nothing is kept on the side.
  const sourceEl = byId<HTMLTextAreaElement>("handoutsSource");
  const fieldsEl = byId("handoutsFields");
  const VIEWS = { fields: byId("handoutsTabFields"), text: byId("handoutsTabText") };
  type View = keyof typeof VIEWS;

  function setView(view: View): void {
    for (const v of Object.keys(VIEWS) as View[]) VIEWS[v].classList.toggle("active", v === view);
    fieldsEl.hidden = view !== "fields";
    sourceEl.hidden = view !== "text";
    if (view === "fields") renderFields();
  }

  function renderFields(): void {
    const blocks = xyHndt.parseHndtForm(sourceEl.value);
    const write = (): void => { sourceEl.value = xyHndt.composeHndtForm(blocks); };
    const shown = blocks.filter((b) => !b.blank);
    if (!shown.length) {
      fieldsEl.replaceChildren(el("p", { class: "hint", text: S.board.handouts.fieldsEmpty() }));
      return;
    }
    fieldsEl.replaceChildren(el("div", { class: "u-col u-gap-md" }, ...shown.map((b) => handoutBox(b, write))));
  }

  // number builds one labelled number field over a setting; empty removes it.
  // The browser's spinner is swapped for two chevrons that fill the right end
  // of the field, which are bigger to hit and follow the theme.
  function number(b: HndtFormBlock, key: string, label: string, write: () => void, title?: string): HTMLElement {
    const input = el("input", { class: "hndt-num-input", type: "number", min: "1", inputmode: "numeric" }) as HTMLInputElement;
    input.value = xyHndt.hndtGet(b, key) ?? "";
    input.addEventListener("input", () => { xyHndt.hndtSet(b, key, input.value.trim() || null); write(); });
    const step = (by: number): void => {
      input.value = String(Math.max(1, (parseInt(input.value, 10) || 0) + by));
      input.dispatchEvent(new Event("input"));
    };
    const btn = (glyph: "chevron-up" | "chevron-down", aria: string, by: number): HTMLElement => {
      // Out of the tab order: the arrow keys already step a focused field.
      const node = el("button", { class: "hndt-num-step", type: "button", tabindex: "-1", "aria-label": aria }, icon(glyph));
      node.addEventListener("click", () => step(by));
      return node;
    };
    const field = el("span", { class: "hndt-num" }, input,
      el("span", { class: "hndt-num-steps" }, btn("chevron-up", S.board.handouts.stepUp(), 1), btn("chevron-down", S.board.handouts.stepDown(), -1)));
    return el("label", { class: "u-row u-gap-xs u-align-center", title: title || "" }, el("span", { class: "fld-label", text: label }), field);
  }

  // seg builds a two-way switch; `on` says which side is lit.
  function seg(labels: [string, string], on: () => 0 | 1, pick: (i: 0 | 1) => void): HTMLElement {
    const btns = labels.map((text) => el("button", { class: "seg-btn", type: "button", text }) as HTMLButtonElement);
    const sync = (): void => btns.forEach((btn, i) => btn.classList.toggle("active", i === on()));
    btns.forEach((btn, i) => btn.addEventListener("click", () => { pick(i as 0 | 1); sync(); }));
    sync();
    return el("div", { class: "seg" }, ...btns);
  }

  function fitArea(ta: HTMLTextAreaElement): void {
    ta.style.height = "";
    if (ta.scrollHeight > ta.clientHeight) ta.style.height = `${ta.scrollHeight + ta.offsetHeight - ta.clientHeight}px`;
  }
  // The monospace face is fetched the first time a box uses it, after the
  // boxes were measured in the fallback, so they are measured again then.
  document.fonts?.addEventListener("loadingdone", () => {
    for (const ta of fieldsEl.querySelectorAll("textarea")) fitArea(ta);
  });

  // cardFor is the card a block's for_question points at, for its pictures.
  function cardFor(b: HndtFormBlock): BoardCard | null {
    if (!handoutsCtx) return null;
    const i = handoutsCtx.numbers.findIndex((n) => n != null && n === xyHndt.hndtGet(b, "for_question"));
    return i >= 0 ? handoutsCtx.cards[i] : null;
  }

  function handoutBox(b: HndtFormBlock, write: () => void): HTMLElement {
    const inside = el("input", { type: "checkbox" }) as HTMLInputElement;
    inside.checked = xyHndt.hndtGet(b, "question_label") === "inside";
    inside.addEventListener("change", () => { xyHndt.hndtSet(b, "question_label", inside.checked ? "inside" : null); write(); });
    // One line on a desktop pane. Two groups, so a phone breaks the line
    // between them and not inside either.
    const settings = el("div", { class: "u-row u-gap-sm u-align-center u-wrap" },
      el("div", { class: "u-row u-gap-sm u-align-center" },
        number(b, "for_question", S.board.handouts.fieldQuestion(), write),
        el("label", { class: "attach-lossless", title: S.board.handouts.fieldInsideTitle() }, inside, " " + S.board.handouts.fieldInside())),
      el("div", { class: "u-row u-gap-sm u-align-center" },
        number(b, "columns", S.board.handouts.fieldColumns(), write),
        number(b, "rows", S.board.handouts.fieldRows(), write, S.board.handouts.fieldRowsTitle())));

    const ta = el("textarea", { class: "card-desc", spellcheck: "false", rows: "3" }) as HTMLTextAreaElement;
    ta.value = b.text;
    // It grows with the handout, like the card editor's fields, instead of
    // scrolling inside a box of its own.
    ta.style.overflowY = "hidden";
    ta.addEventListener("input", () => { b.text = ta.value; write(); fitArea(ta); });
    requestAnimationFrame(() => fitArea(ta));
    // The picker offers the pictures attached to the question the block is for,
    // and keeps the one it names even when that is attached elsewhere.
    const sel = el("select", { class: "input", "aria-label": S.board.handouts.fieldImageLabel() }) as HTMLSelectElement;
    const options = (names: string[]): void => {
      const all = !b.image || names.includes(b.image) ? names : [b.image, ...names];
      sel.replaceChildren(...all.map((n) => el("option", { value: n, text: n })));
      sel.value = b.image;
    };
    const loadOptions = async (): Promise<void> => {
      const card = cardFor(b);
      const atts = card ? await attachments.cardAttachments(card.id) : [];
      options(atts.filter((a) => a.mime.startsWith("image/")).map((a) => a.name));
    };
    options([]);
    void loadOptions();
    sel.addEventListener("focus", () => { void loadOptions(); });
    sel.addEventListener("change", () => { b.image = sel.value; write(); });
    const syncKind = (): void => { ta.hidden = b.kind !== "text"; sel.hidden = b.kind !== "image"; };
    const switches = el("div", { class: "u-row u-gap-sm u-align-center u-wrap" },
      seg([S.card.handout.modeText(), S.card.handout.modeImage()], () => (b.kind === "image" ? 1 : 0), (i) => {
        b.kind = i ? "image" : "text";
        if (b.kind === "image" && !b.image && sel.value) b.image = sel.value;
        syncKind(); write();
      }),
      seg([S.board.handouts.alignCenter(), S.board.handouts.alignLeft()], () => (Number(xyHndt.hndtGet(b, "no_center")) ? 1 : 0), (i) => {
        xyHndt.hndtSet(b, "no_center", i ? "1" : null); write();
      }));
    syncKind();
    return el("div", { class: "hndt-block u-col u-gap-sm" }, settings, switches, ta, sel);
  }

  VIEWS.fields.addEventListener("click", () => setView("fields"));
  VIEWS.text.addEventListener("click", () => setView("text"));

  // WebKit won't render a PDF inside an <iframe> in a standalone web app (macOS
  // Dock app / iOS home-screen PWA — the preview pane comes up blank), and on
  // iOS even the in-browser iframe shows at most a flat first page. No Safari
  // setting changes this; the working path there is a top-level navigation, so
  // those contexts get an "Open PDF" button instead of the inline preview.
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
      el("div", { class: "handouts-pdf-note", text: S.board.handouts.safariNote() }),
      el("a", { class: "btn", href: url, target: "_blank", rel: "noopener", text: S.board.handouts.openPdf() }));
  }

  function clearHandoutsPdf(): void {
    const pane = byId("handoutsPdf");
    pane.replaceChildren();
    const dl = byId<HTMLAnchorElement>("handoutsDownload");
    dl.hidden = true;
    if (handoutsPdfUrl) { revokeNamedUrl(handoutsPdfUrl); handoutsPdfUrl = null; }
    if (handoutsDlUrl) { URL.revokeObjectURL(handoutsDlUrl); handoutsDlUrl = null; }
  }

  // handoutFileBase names a generated handout after the board and the list it came
  // from — "My_board_Tour_1_handouts" — rather than after nothing in particular
  // (issue #43). Only path separators and whitespace are folded away: the name is
  // the one the editor typed, Cyrillic included, and every download it rides on
  // spells it in UTF-8.
  function handoutFileBase(): string {
    const clean = (s: string): string => s.trim().replace(/[\\/\s]+/g, "_");
    return [clean(board.state.name), clean(handoutsCtx?.title || ""), "handouts"].filter(Boolean).join("_");
  }

  // persistHandoutMeta writes the edited per-question settings back onto the cards
  // (everything in each .hndt block except the live handout text/image), so the
  // layout is restored next time the modal opens.
  async function persistHandoutMeta(): Promise<void> {
    if (!handoutsCtx) return;
    const source = byId<HTMLTextAreaElement>("handoutsSource").value;
    const byNumber = xyHndt.parseHndtMetaByQuestion(source);
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
    if (!xySync.requireOnline(S.board.handouts.pdfOffline(), byId("handoutsMessage"))) return;
    const source = byId<HTMLTextAreaElement>("handoutsSource").value;
    const msg = byId("handoutsMessage");
    if (!source.trim()) { msg.textContent = S.board.handouts.sourceEmpty(); return; }
    const btn = byId<HTMLButtonElement>("handoutsGenerate");
    btn.disabled = true;
    msg.textContent = S.board.handouts.generating();
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
      msg.textContent = S.board.handouts.generated();
    } catch (err) {
      msg.textContent = S.board.handouts.generateFailed(errMsg(err));
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
  // handoutsession.ts); the callbacks above are the board-specific network ops.
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
    fd.append("filename", handoutsCtx?.title || "handouts");
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
    if (!xySync.requireOnline(S.board.handouts.splitfitOffline(), msg)) return;
    const source = byId<HTMLTextAreaElement>("handoutsSource").value;
    if (!source.trim()) { msg.textContent = S.board.handouts.sourceEmpty(); return; }
    const btn = byId<HTMLButtonElement>("handoutsSplitFit");
    btn.disabled = true;
    msg.textContent = S.board.handouts.splitfitting();
    try {
      const fd = await handoutsBody(source);
      const res = await fetch("/api/handouts/split_fit", { method: "POST", credentials: "same-origin", body: fd });
      if (!res.ok) throw new Error((await res.text()).trim() || `HTTP ${res.status}`);
      downloadBlob(await res.blob(), handoutFileBase() + ".zip");
      msg.textContent = S.board.handouts.splitfitDone();
    } catch (err) {
      msg.textContent = S.board.handouts.splitfitFailed(errMsg(err));
    } finally {
      btn.disabled = false;
    }
  }

  byId("handoutsGenerate").addEventListener("click", () => { void generateHandoutsPdf(); });
  // Edit the .hndt, regenerate, look: Cmd/Ctrl-Enter is that loop without the trip
  // to the button.
  onCmdEnter(byId("handoutsSource"), () => byId("handoutsGenerate").click());
  onCmdEnter(byId("handoutsFields"), () => byId("handoutsGenerate").click());
  byId("handoutsSplitFit").addEventListener("click", () => { void generateSplitFitZip(); });


  return {
    id: "handouts", menu: "list", icon: "file-text",
    label: (scope) => scope.grouped ? S.board.handouts.menuGroup() : S.board.handouts.title(),
    // Handouts are the exception, not the rule: a tour without one has nothing
    // for this panel to open, and the row would only say so after the click.
    // A SI tour has nothing for it either: the generator keys a question's
    // settings by its number, and every theme has a № 10. The inline
    // «[handout: …]» still reaches the .docx and the .pdf; it is
    // the PDF generation that is not modelled at this grain yet.
    offered: (scope) => scope.game !== "si" && xyHndt.hndtOf(scope.cards).source.trim() !== "",
    open: openHandouts,
  };
}
