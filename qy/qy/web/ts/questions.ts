// The question cards on the jury's and the results pages (server/
// questionview.go). The server sends each card's fields in data-question with
// a plain-text stand-in; this draws the question with chgksuite's renderer —
// xy's boards and the nomination form's preview use the same — and puts the
// answer, the sources and the author into the card's «show the answer»
// disclosure, ahead of what the server put there (the tournament, the extra
// context, the places).
import { renderPreviewCard } from "../../../../chgksuite/web/ts/preview.js";
import { fourS, imageLinks, PARSED_FIELDS, type ParsedField } from "./nominate-model.js";

export function drawQuestions(root: ParentNode): void {
  for (const box of root.querySelectorAll<HTMLElement>("[data-question]")) {
    const question = box.querySelector("[data-part=question]");
    const fields = box.querySelector("[data-part=fields]");
    if (!question || !fields) continue;
    let sent: Partial<Record<ParsedField, string>>;
    try {
      sent = JSON.parse(box.dataset.question ?? "{}");
    } catch {
      continue;
    }
    const f = {} as Record<ParsedField, string>;
    for (const name of PARSED_FIELDS) f[name] = sent[name] ?? "";
    const images = new Map(imageLinks(f.handout).map((u) => [u, u]));
    fields.replaceChildren();
    const card = renderPreviewCard({ id: 0, kind: "question", desc: fourS(f) }, box.dataset.number || null, images, false, { fold: fields });
    question.replaceWith(card);
  }
}

drawQuestions(document);
