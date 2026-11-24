// The behaviour qy's server-rendered pages share, driven by data attributes so
// the CSP needs no inline script:
//   - [data-confirm] on a form: ask before it is submitted.
//   - [data-autosave] on a form: post it whenever a control changes, without
//     leaving the page. The jury's likes and marks are saved this way, so a
//     long portion is never lost to a closed tab. The submit button stays, and
//     a browser without this script still saves with it.
//   - #toggle-all-details, the archive's button that opens every answer on a
//     results page. The old site wired it with an inline script, which the CMS
//     strips.
//   - leaving a page with an unsaved form asks first (unsaved.ts).
//   - [data-question]: a question card, drawn by chgksuite's renderer
//     (questions.ts).
//   - [data-fold]: a long block cut short under a fade until clicked
//     (fold.ts); after the cards, which it measures.
import S from "./i18nstrings.js";
import { formBody, statusText } from "./forms-model.js";

document.addEventListener("submit", (event) => {
  const form = event.target;
  const message = form instanceof HTMLElement ? form.getAttribute("data-confirm") : null;
  if (message && !window.confirm(message)) event.preventDefault();
});

async function save(form: HTMLFormElement): Promise<void> {
  const status = form.querySelector("#autosaveStatus");
  if (status) status.textContent = S.forms.saving();
  let text: string;
  try {
    const res = await fetch(form.action, {
      method: "POST",
      headers: { Accept: "application/json", "Content-Type": "application/x-www-form-urlencoded" },
      credentials: "same-origin",
      body: formBody(new FormData(form).entries()),
    });
    text = statusText(res.ok, await res.text(), S.forms.failed());
  } catch {
    text = S.forms.failed();
  }
  if (status) status.textContent = text;
}

document.addEventListener("change", (event) => {
  const el = event.target;
  if (!(el instanceof HTMLInputElement || el instanceof HTMLSelectElement)) return;
  const form = el.form;
  if (form && form.hasAttribute("data-autosave")) void save(form);
});

// The button that opens every answer at once and closes them again: on the
// archive's results pages (#toggle-all-details, written into the page) and
// over the question cards of the jury's and the admins' pages
// (data-toggle-answers, hidden until here). Where the cards mark their
// answers (data-part=answer), only those open, not an admin's editor; an
// archive page marks none, and every disclosure on it is an answer. The
// label follows the answers as they are opened one by one too.
function answers(): HTMLDetailsElement[] {
  const marked = [...document.querySelectorAll<HTMLDetailsElement>("details[data-part=answer]")];
  return marked.length > 0 ? marked : [...document.querySelectorAll<HTMLDetailsElement>("details")];
}

const answerToggles = [...document.querySelectorAll<HTMLElement>("#toggle-all-details, [data-toggle-answers]")];

function syncAnswerToggles(): void {
  const all = answers();
  const open = all.length > 0 && all.every((d) => d.open);
  for (const t of answerToggles) t.textContent = open ? S.forms.hideAnswers() : S.forms.showAnswers();
}

for (const toggle of answerToggles) {
  toggle.classList.add("btn");
  toggle.hidden = false;
  toggle.addEventListener("click", () => {
    const all = answers();
    const open = all.some((d) => !d.open);
    for (const d of all) d.open = open;
    syncAnswerToggles();
  });
}
if (answerToggles.length > 0) document.addEventListener("toggle", syncAnswerToggles, true);
import "./nominate.js";
import "./questions.js";
import "./fold.js";
import "./unsaved.js";
