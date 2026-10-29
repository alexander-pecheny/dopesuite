// Shared behaviour for the server-rendered builder pages, replacing the inline
// on* handlers those pages used to carry (CSP-friendly, data-attribute driven):
//   - [data-confirm] on a <form> or its clicked submit button: window.confirm()
//     gate before submit.
//   - [data-confirm-unsent] on a <form>: window.confirm() gate before submit,
//     only while this device keeps changes the server has not seen (the
//     logout, which drops them).
//   - [data-confirm-changed] on a <select> inside a <form>: window.confirm()
//     gate before submit, only when the select's choice differs from the one
//     the page came with.
//   - [data-dirty-submit] on a <form>: its submit buttons stay disabled until
//     a field differs from the value the page came with.
//     A form the server sent back after refusing it ([data-refused=yes]) counts
//     as changed, since what it shows is not what is saved.
//   - [data-unique-values="message"] on a <form>: two of its [data-unique]
//     fields holding the same value are both marked invalid with that
//     message, which stops the submit at the first of them.
//   - [data-copy] on a button: copy its text to the clipboard, and show
//     data-copied on the button for a moment.
//   - [data-select-all] on a field: select its text on focus/click (copy helper).
//   - [data-autosubmit] on a control: submit its form on change (the access-role
//     selects, which saved on change via an inline onchange before).
//   - [data-dialog-open="id"] on a button: showModal() that <dialog>.
//   - [data-dialog-close] on a button inside a <dialog>: close it.
//   - [data-dialog-auto] on a <dialog>: showModal() it as the page loads. This
//     is for the dialog a server-rendered page opens with, because asking it is
//     why the page was rendered — the roster import's reconcile is one.

import {unsentEverywhere} from "./offline.js";

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
    return;
  }
  if (!(form instanceof HTMLFormElement)) return;
  const unsent = form.getAttribute("data-confirm-unsent");
  if (unsent && unsentEverywhere() && !window.confirm(unsent)) {
    event.preventDefault();
    return;
  }
  for (const select of form.querySelectorAll("select[data-confirm-changed]")) {
    const options = [...(select as HTMLSelectElement).options];
    if (!options.some((o) => o.selected !== o.defaultSelected)) continue;
    if (!window.confirm(select.getAttribute("data-confirm-changed") ?? "")) {
      event.preventDefault();
      return;
    }
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

const COPIED_MS = 1500;

document.addEventListener("click", (event) => {
  const target = event.target;
  const button = target instanceof Element ? target.closest<HTMLElement>("[data-copy]") : null;
  if (!button) return;
  const label = button.textContent;
  void navigator.clipboard.writeText(button.dataset.copy ?? "").then(() => {
    button.textContent = button.dataset.copied ?? label;
    setTimeout(() => {
      button.textContent = label;
    }, COPIED_MS);
  });
});

function syncDirtySubmit(form: HTMLFormElement): void {
  const dirty = form.dataset.refused === "yes" || [...form.elements].some((el) =>
    (el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement) && el.type !== "hidden" && el.value !== el.defaultValue);
  for (const button of form.querySelectorAll<HTMLButtonElement>("button[type=submit]")) button.disabled = !dirty;
}

function syncUnique(form: HTMLFormElement): void {
  const message = form.dataset.uniqueValues || "";
  const fields = [...form.querySelectorAll<HTMLInputElement>("input[data-unique]")];
  const count = new Map<string, number>();
  for (const field of fields) {
    const value = field.value.trim();
    if (value) count.set(value, (count.get(value) || 0) + 1);
  }
  for (const field of fields) {
    const taken = (count.get(field.value.trim()) || 0) > 1;
    field.setCustomValidity(taken ? message : "");
    field.setAttribute("aria-invalid", String(taken));
  }
}

document.addEventListener("input", (event) => {
  const form = (event.target as FormControl).form;
  if (form?.hasAttribute("data-unique-values")) syncUnique(form);
  if (form?.hasAttribute("data-dirty-submit")) syncDirtySubmit(form);
});

function openAutoDialogs(): void {
  for (const el of document.querySelectorAll("dialog[data-dialog-auto]")) {
    const dialog: DialogLike = el as DialogLike;
    if (typeof dialog.showModal === "function") dialog.showModal();
    else dialog.setAttribute("open", "");
  }
}
function boot(): void {
  for (const form of document.querySelectorAll<HTMLFormElement>("form[data-unique-values]")) syncUnique(form);
  for (const form of document.querySelectorAll<HTMLFormElement>("form[data-dirty-submit]")) syncDirtySubmit(form);
  openAutoDialogs();
}
if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", boot);
} else {
  boot();
}

export {};
