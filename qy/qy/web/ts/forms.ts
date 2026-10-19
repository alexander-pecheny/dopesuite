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

const toggleAll = document.getElementById("toggle-all-details");
if (toggleAll) {
  toggleAll.classList.add("btn");
  toggleAll.addEventListener("click", () => {
    const all = [...document.querySelectorAll("details")];
    const open = all.some((d) => !d.open);
    for (const d of all) d.open = open;
    toggleAll.textContent = open ? S.forms.hideAnswers() : S.forms.showAnswers();
  });
}
