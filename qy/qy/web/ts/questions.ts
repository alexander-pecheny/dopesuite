// The question cards on the jury's and the results pages (server/
// questionview.go). The server sends each card's fields in data-question with
// a plain-text stand-in; this draws the question with chgksuite's renderer —
// xy's boards and the nomination form's preview use the same — and puts the
// answer, the sources and the author into the card's «show the answer»
// disclosure, ahead of what the server put there (the tournament, the extra
// context, the places).
//
// A question not written in Russian also carries its translation in
// data-translation. The card then shows the switch the server left hidden,
// starting on the original, and draws whichever side is picked; the
// disclosure with the translation as plain text, which stands in for the
// switch without the script, goes.
import { renderPreviewCard } from "../../../../chgksuite/web/ts/preview.js";
import { fourS, imageLinks, PARSED_FIELDS, type ParsedField } from "./nominate-model.js";

function readFields(json: string | undefined): Record<ParsedField, string> | null {
  let sent: Partial<Record<ParsedField, string>>;
  try {
    sent = JSON.parse(json ?? "{}");
  } catch {
    return null;
  }
  const f = {} as Record<ParsedField, string>;
  for (const name of PARSED_FIELDS) f[name] = sent[name] ?? "";
  return f;
}

// draw puts f in place of the card's question, current, and its answer and
// the rest into fields; it returns the new question element.
function draw(box: HTMLElement, current: Element, fields: Element, f: Record<ParsedField, string>): Element {
  const images = new Map(imageLinks(f.handout).map((u) => [u, u]));
  fields.replaceChildren();
  const card = renderPreviewCard({ id: 0, kind: "question", desc: fourS(f) }, box.dataset.number || null, images, false, { fold: fields });
  current.replaceWith(card);
  return card;
}

export function drawQuestions(root: ParentNode): void {
  for (const box of root.querySelectorAll<HTMLElement>("[data-question]")) {
    const question = box.querySelector("[data-part=question]");
    const fields = box.querySelector("[data-part=fields]");
    const orig = readFields(box.dataset.question);
    if (!question || !fields || !orig) continue;
    let current = draw(box, question, fields, orig);
    const tr = readFields(box.dataset.translation);
    const langs = box.querySelector<HTMLElement>("[data-part=langs]");
    if (!tr || !langs) continue;
    box.querySelector("[data-part=translation]")?.remove();
    langs.hidden = false;
    langs.addEventListener("click", (e) => {
      const tab = (e.target as Element).closest<HTMLElement>("[role=tab]");
      if (!tab || tab.classList.contains("active")) return;
      for (const t of langs.querySelectorAll<HTMLElement>("[role=tab]")) {
        t.classList.toggle("active", t === tab);
        t.setAttribute("aria-selected", String(t === tab));
      }
      current = draw(box, current, fields, tab.dataset.view === "tr" ? tr : orig);
    });
  }
}

drawQuestions(document);
