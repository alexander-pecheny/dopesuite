// Asks before a page with unsaved edits is left. A form is unsaved from the
// first change a person makes in it (a typed input, a pick, or one of the
// nomination form's own buttons, through markUnsaved) until it is submitted.
// A form that saves itself (data-autosave) and a search (method get) never
// count. The browser words the question itself; a page cannot.

const unsaved = new Set<HTMLFormElement>();

function counts(form: HTMLFormElement): boolean {
  return !form.hasAttribute("data-autosave") && form.method !== "get";
}

export function markUnsaved(form: HTMLFormElement | null | undefined): void {
  if (form && counts(form)) unsaved.add(form);
}

function onEdit(event: Event): void {
  const el = event.target;
  if (!event.isTrusted) return;
  if (el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement || el instanceof HTMLSelectElement) markUnsaved(el.form);
}

document.addEventListener("input", onEdit);
document.addEventListener("change", onEdit);

// A submit that went ahead takes the form's edits with it. The listener is on
// the window, which a submit reaches after every listener on the document
// (forms.ts's data-confirm), so one the person called off is already
// prevented here.
window.addEventListener("submit", (event) => {
  if (!event.defaultPrevented && event.target instanceof HTMLFormElement) unsaved.delete(event.target);
});

window.addEventListener("beforeunload", (event) => {
  for (const form of unsaved) {
    if (form.isConnected) {
      event.preventDefault();
      event.returnValue = "";
      return;
    }
  }
});
