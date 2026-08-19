// preview.ts — xy's one screen rendering of a question: a 4s card as the docx
// export would set it. renderPreviewCard turns a card into DOM — a numbered
// question with its answer, зачёт and the rest, or a heading/meta block — over
// renderRich, which walks chgk's inline runs (bold, italic, links, (screen …),
// line and page breaks, images) in print or screen mode. The list preview, the
// card editor's Просмотр and the import preview all draw through it.
import { xyApp } from "./app.js";
import { xyChgk } from "./chgk.js";
import type { ScreenValue } from "./chgk.js";
import type { BoardCard } from "./unlock.js";

const { el } = xyApp;

export interface PvCard { id: number; kind: string; desc: string; listId?: number }
const PV_LABELS = xyChgk.QUESTION_LABELS;

// fillPreviewImages swaps the "[изображение: …]" placeholders inside an already
// rendered preview for the images that have since resolved.
export function fillPreviewImages(root: ParentNode, imgMap: Map<string, string>): void {
  for (const ph of root.querySelectorAll<HTMLElement>(".pv-img-missing")) {
    const url = imgMap.get(ph.dataset.img || "");
    if (url) ph.replaceWith(el("img", { class: "pv-img", src: url, alt: ph.dataset.img }));
  }
}

// fieldOpts returns the render options for a field given the screen-mode toggle.
// Meta/headings are never screen-transformed. `nbsp` (non-breaking spaces/
// hyphens) applies everywhere except sources and handouts, like docx.
export interface RichOpts { accents?: boolean; brackets?: boolean; nbsp?: boolean }
function fieldOpts(field: string, screen: boolean): RichOpts {
  const nbsp = field !== "source" && field !== "handout";
  if (!screen) return { accents: false, brackets: false, nbsp };
  return { accents: true, brackets: !xyChgk.fieldKeepsBrackets(field), nbsp };
}

// renderRich turns a 4s text element into DOM, mirroring the docx render: inline
// bold/italic/underline/strike/small-caps, links, (screen …), explicit
// (LINEBREAK)/(PAGEBREAK), and (img …) handouts (shown inline). opts.{accents,
// brackets} select print vs. screen mode; opts.nbsp glues non-breaking
// spaces/hyphens into plain text. Styling is applied via the CSSOM (.style.*) to
// stay within the strict CSP.
export function renderRich(text: string, imgMap: Map<string, string>, opts: RichOpts = {}): DocumentFragment {
  const screenSide = !!(opts.accents || opts.brackets);
  const nb = (t: string): string => (opts.nbsp ? xyChgk.replaceNoBreak(t) : t);
  const frag = document.createDocumentFragment();
  // An image renders as a block, so it already ends its line; under pre-wrap the
  // source's own newline right after "(img …)" would add a second, empty one.
  let afterImg = false;
  for (let [type, val] of xyChgk.renderRuns(text, opts)) {
    if (afterImg) {
      afterImg = false;
      if (!type && typeof val === "string" && val.startsWith("\n")) val = val.slice(1);
    }
    if (type === "linebreak") { frag.append(el("br")); continue; }
    if (type === "pagebreak") { frag.append(el("hr", { class: "pv-pagebreak" })); continue; }
    if (type === "img") {
      const name = xyChgk.imgName(val);
      const url = imgMap.get(name);
      if (url) frag.append(el("img", { class: "pv-img", src: url, alt: name }));
      else frag.append(el("span", { class: "pv-img-missing", dataset: { img: name }, text: `[изображение: ${name}]` }));
      afterImg = true;
      continue;
    }
    if (type === "screen") {
      const sv = val as ScreenValue;
      frag.append(document.createTextNode(nb((screenSide ? sv.for_screen : sv.for_print) || "")));
      continue;
    }
    if (type === "hyperlink") {
      frag.append(el("a", { class: "pv-link", href: val, target: "_blank", rel: "noopener noreferrer", text: val }));
      continue;
    }
    if (!type) { frag.append(document.createTextNode(nb(val as string))); continue; }
    const span = el("span", { text: nb(val as string) });
    if (type.includes("italic")) span.style.fontStyle = "italic";
    if (type.includes("bold")) span.style.fontWeight = "bold";
    if (type.includes("underline")) span.style.textDecoration = "underline";
    if (type === "strike") span.style.textDecoration = "line-through";
    if (type === "sc") span.classList.add("pv-sc");
    frag.append(span);
  }
  return frag;
}

// renderFieldBody renders a field value, turning a chgksuite "- …" list into a
// numbered 1./2./… list (with an optional preamble) — this is also how blitz /
// duplet questions and multi-part answers render. Otherwise a plain rich run.
// Works for every field (question, answer, source, comment, …), not just sources.
export function renderFieldBody(text: string, imgMap: Map<string, string>, opts: RichOpts): DocumentFragment {
  const frag = document.createDocumentFragment();
  const lst = xyChgk.splitList(text);
  if (lst.items) {
    if (lst.preamble.trim()) frag.append(renderRich(lst.preamble, imgMap, opts));
    const box = el("div", { class: "pv-list" });
    lst.items.forEach((it, i) => {
      const li = el("div", { class: "pv-list-item" }, el("span", { class: "pv-list-num", text: `${i + 1}.` }));
      const body = el("div", { class: "pv-list-body" });
      body.append(renderRich(it, imgMap, opts));
      li.append(body);
      box.append(li);
    });
    frag.append(box);
  } else {
    frag.append(renderRich(lst.preamble, imgMap, opts));
  }
  return frag;
}

// pvSmallCls: sources and authors are set smaller, like the docx/PDF exports
// (12pt body → 10pt).
function pvSmallCls(field: string): string {
  return field === "source" || field === "author" ? "pv-small" : "";
}

// pvField renders a "Label: value" line, numbering any "- …" list. The caption
// rules (a "!!Label" override, the plural source label) are xyChgk's, shared with
// the copy targets.
function pvField(field: string, text: string, imgMap: Map<string, string>, screen: boolean, cls: string): HTMLElement {
  const cap = xyChgk.fieldCaption(field, text);
  const node = el("div", { class: "pv-field" + (cls ? " " + cls : "") },
    el("strong", { class: "pv-label", text: cap.label + ": " }));
  node.append(renderFieldBody(cap.text, imgMap, fieldOpts(field, screen)));
  return node;
}

// renderPreviewCard renders one card the way the docx export would: a question
// card becomes a numbered question with its answer/zachet/etc.; meta/heading/
// section/editor/date cards become their corresponding paragraphs/headings.
// `edit` builds the ✏️ jump-to-editor button — only the list preview passes
// one; the card-detail preview (already inside the editor) leaves it off.
export function renderPreviewCard(card: PvCard, number: string | null, imgMap: Map<string, string>, screen: boolean, edit?: (card: BoardCard) => HTMLElement): HTMLElement {
  const blocks = xyChgk.parseBlocks(card.desc);
  const find = (t: string) => blocks.find((b) => b.type === t);

  if (card.kind === "question" || find("question")) {
    const wrap = el("article", { class: "pv-q", dataset: { cardId: card.id } });
    const handout = find("handout");
    if (handout) wrap.append(pvField("handout", handout.text, imgMap, screen, "pv-handout"));
    // Question line: small inline ✏️ (edit lists only) + bold "Вопрос N." label
    // (overridable) + question text (which may itself be a blitz/duplet list).
    const qov = xyChgk.applyOverride(xyChgk.questionText(card.desc));
    const qLabel = qov.label || "Вопрос";
    const qline = el("div", { class: "pv-q-text" });
    if (edit) qline.append(edit(card as BoardCard));
    qline.append(el("strong", { class: "pv-label", text: `${qLabel}${number ? " " + number : ""}. ` }));
    qline.append(renderFieldBody(qov.text, imgMap, fieldOpts("question", screen)));
    wrap.append(qline);
    for (const f of ["answer", "zachet", "nezachet", "comment", "source", "author"]) {
      const b = find(f);
      if (b) wrap.append(pvField(f, b.text, imgMap, screen, pvSmallCls(f)));
    }
    return wrap;
  }

  // Non-question card: render each block by type (never screen-transformed).
  const wrap = el("div", { class: "pv-block", dataset: { cardId: card.id } });
  for (const b of blocks) {
    if (b.type === "number" || b.type === "setcounter") continue; // numbering directive only
    if (b.type === "heading" || b.type === "ljheading") {
      const h = el("h2", { class: "pv-heading" });
      h.append(renderRich(b.text, imgMap, { nbsp: true }));
      wrap.append(h);
    } else if (b.type === "section") {
      const h = el("h3", { class: "pv-section" });
      h.append(renderRich(b.text, imgMap, { nbsp: true }));
      wrap.append(h);
    } else if (PV_LABELS[b.type]) {
      wrap.append(pvField(b.type, b.text, imgMap, false, pvSmallCls(b.type)));
    } else {
      const p = el("p", { class: "pv-meta" });
      p.append(renderRich(b.text, imgMap, { nbsp: true }));
      wrap.append(p);
    }
  }
  // Inline ✏️ tucked in front of the block's first line (edit lists only).
  if (edit) (wrap.firstElementChild || wrap).prepend(edit(card as BoardCard));
  return wrap;
}

