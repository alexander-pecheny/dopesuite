// Shared behaviour for the server-rendered builder pages, replacing the inline
// on* handlers those pages used to carry (CSP-friendly, data-attribute driven):
//   - [data-confirm] on a <form> or its clicked submit button: window.confirm()
//     gate before submit.
//   - [data-select-all] on a field: select its text on focus/click (copy helper).
//   - [data-autosubmit] on a control: submit its form on change (the access-role
//     selects, which saved on change via an inline onchange before).
//   - [data-dialog-open="id"] on a button: showModal() that <dialog>.
//   - [data-dialog-close] on a button inside a <dialog>: close it.
//   - [data-filter-rows="tableId"] on an input: hide the rows of that table
//     that do not contain what was typed.
//   - [data-copy-target="id"] on a button: copy that field's value.

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

// filterRows is the one text box over a table: a row survives when its whole
// text contains the query, so any column filters.
export function filterRows(table: HTMLElement, query: string): void {
  const needle = query.trim().toLowerCase();
  const rows = table.querySelectorAll("tbody tr");
  rows.forEach((row) => {
    const text = (row.textContent || "").toLowerCase();
    (row as HTMLElement).hidden = needle !== "" && !text.includes(needle);
  });
}

document.addEventListener("input", (event) => {
  const el = event.target;
  if (!(el instanceof HTMLElement)) return;
  const id = el.getAttribute("data-filter-rows");
  if (!id) return;
  const table = document.getElementById(id);
  if (table) filterRows(table, (el as HTMLInputElement).value || "");
});

document.addEventListener("click", (event) => {
  const target = event.target;
  if (!(target instanceof Element)) return;
  const button = target.closest("[data-copy-target]");
  if (!button) return;
  const id = button.getAttribute("data-copy-target");
  const field = id ? document.getElementById(id) : null;
  if (!(field instanceof HTMLInputElement)) return;
  field.select();
  void navigator.clipboard?.writeText(field.value);
});

export {};
