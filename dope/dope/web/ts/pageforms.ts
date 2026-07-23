// Shared behaviour for the server-rendered builder pages, replacing the inline
// on* handlers those pages used to carry (CSP-friendly, data-attribute driven):
//   - [data-confirm] on a <form> or its clicked submit button: window.confirm()
//     gate before submit.
//   - [data-select-all] on a field: select its text on focus/click (copy helper).
//   - [data-autosubmit] on a control: submit its form on change (the access-role
//     selects, which saved on change via an inline onchange before).
//   - [data-dialog-open="id"] on a button: showModal() that <dialog>.
//   - [data-dialog-close] on a button inside a <dialog>: close it.

type SelectableField = HTMLElement & { select?: () => void };
type FormControl = HTMLElement & {
  form?: (HTMLFormElement & { requestSubmit?: () => void }) | null;
};
type DialogLike = HTMLElement & { showModal?: () => void; close?: () => void };

document.addEventListener("submit", (event) => {
  const form = event.target;
  const message =
    (event.submitter && event.submitter.getAttribute("data-confirm")) ||
    (form instanceof HTMLElement && form.getAttribute("data-confirm"));
  if (message && !window.confirm(message)) {
    event.preventDefault();
  }
});

function selectAll(event: Event): void {
  const el = event.target;
  if (el instanceof HTMLElement && el.hasAttribute("data-select-all")) {
    const field: SelectableField = el;
    if (typeof field.select === "function") field.select();
  }
}
document.addEventListener("focus", selectAll, true);
document.addEventListener("click", selectAll, true);

document.addEventListener("change", (event) => {
  const el = event.target;
  if (el instanceof HTMLElement && el.hasAttribute("data-autosubmit")) {
    const form = (el as FormControl).form;
    if (form) form.requestSubmit ? form.requestSubmit() : form.submit();
  }
});

document.addEventListener("click", (event) => {
  const target = event.target;
  if (!(target instanceof Element)) return;
  const opener = target.closest("[data-dialog-open]");
  if (opener) {
    const id = opener.getAttribute("data-dialog-open");
    const dialog: DialogLike | null = id ? document.getElementById(id) : null;
    if (dialog) {
      if (typeof dialog.showModal === "function") dialog.showModal();
      else dialog.setAttribute("open", "");
    }
    return;
  }
  const closer = target.closest("[data-dialog-close]");
  if (closer) {
    const dialog: DialogLike | null = closer.closest("dialog");
    if (dialog) {
      if (typeof dialog.close === "function") dialog.close();
      else dialog.removeAttribute("open");
    }
  }
});

export {};
