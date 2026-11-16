// [data-fold]: a block that may be long, such as a question in the admins'
// table of nominations. One taller than the fold is cut to its first lines
// under a fade, and a click (or Enter) opens it whole. Without this script the
// block is simply shown whole.
import S from "./i18nstrings.js";

function measure(): void {
  for (const box of document.querySelectorAll<HTMLElement>("[data-fold]:not([aria-expanded=true])")) {
    box.dataset.folded = "";
    if (box.scrollHeight <= box.clientHeight) {
      delete box.dataset.folded;
      continue;
    }
    box.tabIndex = 0;
    box.title = S.forms.unfold();
    box.setAttribute("aria-expanded", "false");
  }
}

// Again once the pictures are in: a handout makes a short question long.
measure();
window.addEventListener("load", measure);

function unfold(target: EventTarget | null): boolean {
  const box = target instanceof Element ? target.closest<HTMLElement>("[data-folded]") : null;
  if (!box) return false;
  delete box.dataset.folded;
  box.removeAttribute("title");
  box.removeAttribute("tabindex");
  box.setAttribute("aria-expanded", "true");
  return true;
}

// Capture, so the click that opens a block does not also follow a link or
// toggle the answer inside it.
document.addEventListener(
  "click",
  (event) => {
    if (unfold(event.target)) event.preventDefault();
  },
  true,
);

document.addEventListener("keydown", (event) => {
  if ((event.key === "Enter" || event.key === " ") && event.target instanceof HTMLElement && event.target.hasAttribute("data-folded")) {
    event.preventDefault();
    unfold(event.target);
  }
});
