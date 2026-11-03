// The nomination form (server/nominateview.go). Each [data-slot] card starts
// in the "source" state: one box for a link or a whole question, which grows
// as a pasted question does. A link loads (/api/nominate/load), a text
// parses (/api/nominate/parse), and the card turns to the "editor" state:
// tabs for the preview, the fields and the box, then the two links. The tab
// is remembered in a hidden slotN_view input, so a form shown again opens
// where it was. An empty optional field hides behind a «+» button. The
// automatic check of the rules (/api/nominate/check) runs again whenever
// the tournament or the authors change, and fills the slot's red box, where
// the nominator can appeal. Without
// this script the buttons post the form, which does the same and comes back
// unsaved.
import S from "./i18nstrings.js";
import { isLoadable, isSingleLink, PARSED_FIELDS, type Parsed } from "./nominate-model.js";

type Control = HTMLInputElement | HTMLTextAreaElement;

function field(card: HTMLElement, name: string): Control | null {
  return card.querySelector(`[name="slot${card.dataset.slot}_${name}"]`);
}

function part(card: HTMLElement, name: string): HTMLElement | null {
  return card.querySelector(`[data-part=${name}]`);
}

// fit sizes a self-growing box to its text. A hidden box measures 0, so a
// panel refits its boxes when it is shown.
function fit(ta: HTMLTextAreaElement): void {
  if (ta.offsetParent === null) return;
  ta.style.height = "";
  if (ta.value === "") return;
  ta.style.height = "auto";
  ta.style.height = ta.scrollHeight + (ta.offsetHeight - ta.clientHeight) + "px";
}

function fitAll(root: Element): void {
  for (const ta of root.querySelectorAll<HTMLTextAreaElement>("textarea[data-autogrow]")) fit(ta);
}

function showView(card: HTMLElement, view: string): void {
  const editing = card.dataset.state === "editor";
  if (!editing) view = "text";
  card.dataset.view = view;
  for (const tab of card.querySelectorAll<HTMLElement>("[role=tab]")) {
    tab.classList.toggle("active", tab.dataset.view === view);
    tab.setAttribute("aria-selected", String(tab.dataset.view === view));
  }
  for (const panel of card.querySelectorAll<HTMLElement>("[data-panel]")) panel.hidden = panel.dataset.panel !== view;
  const input = field(card, "view");
  if (input) input.value = view;
  fitAll(card);
  if (view === "preview") void refreshPreview(card);
}

function setState(card: HTMLElement, state: "source" | "editor", view: string): void {
  card.dataset.state = state;
  for (const name of ["tabs", "links"]) {
    const el = part(card, name);
    if (el) el.hidden = state !== "editor";
  }
  const clear = card.querySelector<HTMLElement>("[data-clear]");
  if (clear) clear.hidden = state !== "editor";
  showView(card, view);
}

// syncButtons shows the load button for a link in the box and the parse
// button for anything else.
function syncButtons(card: HTMLElement): void {
  const link = isSingleLink(field(card, "raw")?.value ?? "");
  const load = card.querySelector<HTMLElement>("[data-load]");
  const parse = card.querySelector<HTMLElement>("[data-parse]");
  if (load) load.hidden = !link;
  if (parse) parse.hidden = link;
}

function filled(el: Element): boolean {
  return [...el.querySelectorAll<Control>("textarea, input[type=text], input:not([type])")].some((c) => c.value.trim() !== "")
    || el.querySelector('a[href^="/nominate/file/"]') !== null;
}

// syncOptional hides each empty optional field behind its «+» button.
function syncOptional(card: HTMLElement): void {
  let any = false;
  for (const el of card.querySelectorAll<HTMLElement>("[data-optional]")) {
    const add = card.querySelector<HTMLElement>(`[data-add="${el.dataset.field}"]`);
    const show = filled(el);
    el.hidden = !show;
    if (add) add.hidden = show;
    any ||= !show;
  }
  const adds = card.querySelector<HTMLElement>("[data-adds]");
  if (adds) adds.hidden = !any;
}

function addField(card: HTMLElement, button: HTMLElement): void {
  const el = card.querySelector<HTMLElement>(`[data-optional][data-field="${button.dataset.add}"]`);
  if (!el) return;
  el.hidden = false;
  button.hidden = true;
  const adds = card.querySelector<HTMLElement>("[data-adds]");
  if (adds) adds.hidden = ![...adds.querySelectorAll<HTMLElement>("[data-add]")].some((b) => !b.hidden);
  el.querySelector<HTMLTextAreaElement>("textarea")?.focus();
  fitAll(el);
}

function fill(card: HTMLElement, parsed: Parsed): void {
  for (const name of PARSED_FIELDS) {
    const el = field(card, name);
    if (el) el.value = parsed[name];
  }
  syncOptional(card);
}

// message shows a failure under the source box, or hides it.
function message(card: HTMLElement, text: string): void {
  const where = card.querySelector("[data-panel=text]");
  if (!where) return;
  let p = where.querySelector<HTMLElement>(":scope > [data-message]");
  if (!p) {
    p = document.createElement("p");
    p.className = "hint hint-danger";
    p.dataset.message = "";
    where.append(p);
  }
  p.textContent = text;
  p.hidden = text === "";
}

// ask posts to one of the form's endpoints; a failure comes back as the
// words to show.
async function ask<T>(url: string, body: object): Promise<T | string> {
  try {
    const res = await fetch(url, {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    if (!res.ok) return (await res.text()).trim() || S.forms.failed();
    return (await res.json()) as T;
  } catch {
    return S.forms.failed();
  }
}

async function refreshPreview(card: HTMLElement): Promise<void> {
  const body: Record<string, string> = {};
  for (const name of PARSED_FIELDS) body[name] = field(card, name)?.value ?? "";
  const res = await ask<{ html: string }>("/api/nominate/preview", body);
  const el = card.querySelector<HTMLElement>("[data-panel=preview] .richtext");
  if (el && typeof res !== "string") el.innerHTML = res.html;
}

// check asks whether the question passes the rules and shows what it fails.
async function check(card: HTMLElement): Promise<void> {
  const body = { tournament: field(card, "tournament")?.value ?? "", author: field(card, "author")?.value ?? "" };
  const res = await ask<{ problems: string[]; unchecked: boolean }>("/api/nominate/check", body);
  if (typeof res === "string") return;
  const box = card.querySelector<HTMLElement>("[data-lint]");
  const list = card.querySelector<HTMLElement>("[data-lint-list]");
  const unchecked = card.querySelector<HTMLElement>("[data-lint-unchecked]");
  if (list) {
    list.replaceChildren(...res.problems.map((t) => {
      const p = document.createElement("p");
      p.textContent = t;
      return p;
    }));
  }
  if (box) box.hidden = res.problems.length === 0;
  if (unchecked) unchecked.hidden = !res.unchecked;
  if (box) fitAll(box);
}

// fillFrom loads the link in the box or parses the text in it, and opens
// the question on its preview.
async function fillFrom(card: HTMLElement): Promise<void> {
  const raw = field(card, "raw")?.value.trim() ?? "";
  if (!raw) return;
  let res: Parsed | string;
  if (isSingleLink(raw)) {
    res = isLoadable(raw) ? await ask<Parsed>("/api/nominate/load", { link: raw }) : S.nominate.slot.loadFailed();
    const link = field(card, "link");
    if (link) link.value = raw;
  } else {
    res = await ask<Parsed>("/api/nominate/parse", { text: raw });
    if (typeof res !== "string" && !res.ok) res = S.nominate.slot.parseFailed();
  }
  if (typeof res === "string") {
    message(card, res);
    return;
  }
  message(card, "");
  fill(card, res);
  const tournament = field(card, "tournament");
  if (tournament && !tournament.value && res.tournament) tournament.value = res.tournament;
  setState(card, "editor", "preview");
  void check(card);
}

// clear empties the slot and puts it back to the box alone. Nothing changes
// on the server until the form is saved.
function clear(card: HTMLElement): void {
  for (const c of card.querySelectorAll<HTMLInputElement | HTMLTextAreaElement>("textarea, input")) {
    if (c instanceof HTMLInputElement && (c.type === "checkbox" || c.type === "radio")) c.checked = false;
    else if (!(c instanceof HTMLInputElement && (c.type === "hidden" || c.type === "submit"))) c.value = "";
  }
  for (const a of card.querySelectorAll('a[href^="/nominate/file/"]')) a.closest(".u-row")?.remove();
  message(card, "");
  syncButtons(card);
  syncOptional(card);
  setState(card, "source", "text");
  void check(card);
  field(card, "raw")?.focus();
}

function wire(card: HTMLElement): void {
  syncButtons(card);
  syncOptional(card);
  setState(card, card.dataset.state === "editor" ? "editor" : "source", card.dataset.view || "text");
  card.addEventListener("change", (event) => {
    if (event.target === field(card, "tournament") || event.target === field(card, "author")) void check(card);
  });
  card.addEventListener("input", (event) => {
    const el = event.target;
    if (el instanceof HTMLTextAreaElement && el.hasAttribute("data-autogrow")) fit(el);
    if (el === field(card, "raw")) syncButtons(card);
  });
  field(card, "raw")?.addEventListener("keydown", (event: Event) => {
    if (!(event instanceof KeyboardEvent) || event.key !== "Enter" || event.shiftKey) return;
    const raw = field(card, "raw")?.value ?? "";
    if (!isSingleLink(raw) && !(event.ctrlKey || event.metaKey)) return;
    event.preventDefault();
    void fillFrom(card);
  });
  card.addEventListener("click", (event) => {
    const target = event.target instanceof Element ? event.target.closest<HTMLElement>("button") : null;
    if (!target) return;
    if (target.getAttribute("role") === "tab" && target.dataset.view) {
      showView(card, target.dataset.view);
    } else if (target.hasAttribute("data-parse") || target.hasAttribute("data-load")) {
      event.preventDefault();
      void fillFrom(card);
    } else if (target.hasAttribute("data-clear")) {
      event.preventDefault();
      clear(card);
    } else if (target.dataset.add) {
      addField(card, target);
    }
  });
}

for (const card of document.querySelectorAll<HTMLElement>("form[data-nominate] [data-slot]")) wire(card);

// Enter in a one-line field would submit the form through its first submit
// button, which is the first slot's clear. The source box loads its own slot
// (above); the other fields do nothing.
document.querySelector("form[data-nominate]")?.addEventListener("keydown", (event) => {
  const el = event.target;
  if (event instanceof KeyboardEvent && event.key === "Enter" && el instanceof HTMLInputElement && el.type !== "submit") {
    event.preventDefault();
  }
});
