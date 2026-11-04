// The nomination form (server/nominateview.go). Each [data-slot] card starts
// in the "source" state: one box for a link or a whole question, which grows
// as a pasted question does. A link loads (/api/nominate/load), a text
// parses (/api/nominate/parse), and the card turns to the "editor" state:
// tabs for the preview, the fields and the box, then the two links. The box
// then holds the question as text (/api/nominate/preview writes it), and a
// change to it is parsed back into the fields on leaving the tab, the way
// xy's 4s tab and its fields follow each other. The tab is remembered in a
// hidden slotN_view input, so a form shown again opens where it was. An
// empty optional field hides behind a «+» button. The automatic check of the
// rules (/api/nominate/check) runs again as the tournament or the authors
// are typed, and fills the slot's red box, where the nominator can appeal.
// The arrows swap a slot's question with its neighbour's; each slot keeps
// the number of the slot its question was drawn in (slotN_origin), so the
// server moves the question rather than making a new one. Without this
// script the buttons post the form, which does the same and comes back
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

type Rendered = { html: string; text: string };

// render asks for the question as the preview shows it and as text.
async function render(card: HTMLElement): Promise<Rendered | null> {
  const body: Record<string, string> = {};
  for (const name of PARSED_FIELDS) body[name] = field(card, name)?.value ?? "";
  const res = await ask<Rendered>("/api/nominate/preview", body);
  return typeof res === "string" ? null : res;
}

async function refreshPreview(card: HTMLElement): Promise<void> {
  const res = await render(card);
  const el = card.querySelector<HTMLElement>("[data-panel=preview] .richtext");
  if (el && res) el.innerHTML = res.html;
}

// composeText writes the fields into the box as text.
async function composeText(card: HTMLElement): Promise<void> {
  const res = await render(card);
  const raw = field(card, "raw");
  if (!res || !raw) return;
  raw.value = res.text;
  card.dataset.composed = res.text;
  syncButtons(card);
  fitAll(card);
}

// leaveText parses the box back into the fields if it was changed. It says
// false when the text has no answer, and the tab stays open with the reason.
async function leaveText(card: HTMLElement): Promise<boolean> {
  const raw = field(card, "raw")?.value ?? "";
  if (card.dataset.state !== "editor" || card.dataset.view !== "text" || raw === card.dataset.composed) return true;
  const res = await ask<Parsed>("/api/nominate/parse", { text: raw });
  if (typeof res === "string" || !res.ok) {
    message(card, typeof res === "string" ? res : S.nominate.slot.parseFailed());
    return false;
  }
  message(card, "");
  fill(card, res);
  card.dataset.composed = raw;
  return true;
}

async function switchTo(card: HTMLElement, view: string): Promise<void> {
  if (await leaveText(card)) showView(card, view);
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
  if (view === "text" && editing) void composeText(card);
}

function setState(card: HTMLElement, state: "source" | "editor", view: string): void {
  card.dataset.state = state;
  for (const name of ["tabs", "links"]) {
    const el = part(card, name);
    if (el) el.hidden = state !== "editor";
  }
  const clear = card.querySelector<HTMLElement>("[data-clear]");
  if (clear) clear.hidden = state !== "editor";
  const label = card.querySelector<HTMLElement>("[data-panel=text] .field > span");
  if (label) label.textContent = state === "editor" ? S.nominate.slot.textLabel() : S.nominate.slot.source();
  const hint = card.querySelector<HTMLElement>("[data-text-hint]");
  if (hint) hint.hidden = state !== "editor";
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
  void check(card);
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
  delete card.dataset.composed;
  syncButtons(card);
  syncOptional(card);
  setState(card, "source", "text");
  void check(card);
  field(card, "raw")?.focus();
}

// The question parts of a card: everything but its header, which stays
// with the slot's number when two slots swap their questions.
function body(card: HTMLElement): Element[] {
  return [...card.children].filter((el) => !el.hasAttribute("data-head"));
}

// renumber points a moved question's controls at the slot it is now in.
function renumber(card: HTMLElement): void {
  const slot = card.dataset.slot ?? "";
  for (const el of card.querySelectorAll<HTMLElement>("[name^=slot]")) {
    el.setAttribute("name", (el.getAttribute("name") ?? "").replace(/^slot\d+_/, `slot${slot}_`));
  }
  for (const b of card.querySelectorAll<HTMLButtonElement>("[data-panel] button[name=load], [data-panel] button[name=parse]")) b.value = slot;
}

// swap trades the questions of two slots, which is how a nominator orders
// them. Nothing is saved until the form is.
async function swap(a: HTMLElement, b: HTMLElement): Promise<void> {
  if (!(await leaveText(a)) || !(await leaveText(b))) return;
  const bodyA = body(a), bodyB = body(b);
  a.append(...bodyB);
  b.append(...bodyA);
  for (const key of ["state", "view", "composed"]) {
    const va = a.dataset[key], vb = b.dataset[key];
    if (vb === undefined) delete a.dataset[key]; else a.dataset[key] = vb;
    if (va === undefined) delete b.dataset[key]; else b.dataset[key] = va;
  }
  for (const card of [a, b]) {
    renumber(card);
    setState(card, card.dataset.state === "editor" ? "editor" : "source", card.dataset.view || "text");
  }
  b.scrollIntoView({ block: "nearest", behavior: "smooth" });
}

function neighbour(card: HTMLElement, dir: string): HTMLElement | null {
  const n = Number(card.dataset.slot) + (dir === "up" ? -1 : 1);
  return document.querySelector<HTMLElement>(`form[data-nominate] [data-slot="${n}"]`);
}

// The check runs again once the nominator stops typing a link or an author.
const checkDelay = 600;

function wire(card: HTMLElement): void {
  syncButtons(card);
  syncOptional(card);
  if (card.dataset.state === "editor") card.dataset.composed = field(card, "raw")?.value ?? "";
  setState(card, card.dataset.state === "editor" ? "editor" : "source", card.dataset.view || "text");
  let pending = 0;
  card.addEventListener("input", (event) => {
    const el = event.target;
    if (el instanceof HTMLTextAreaElement && el.hasAttribute("data-autogrow")) fit(el);
    if (el === field(card, "raw")) syncButtons(card);
    if (el === field(card, "tournament") || el === field(card, "author")) {
      window.clearTimeout(pending);
      pending = window.setTimeout(() => void check(card), checkDelay);
    }
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
      void switchTo(card, target.dataset.view);
    } else if (target.dataset.move) {
      event.preventDefault();
      const other = neighbour(card, target.dataset.move);
      if (other) void swap(card, other);
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
