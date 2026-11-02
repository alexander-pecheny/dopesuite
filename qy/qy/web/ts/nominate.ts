// The nomination form (server/nominateform.go). Each [data-slot] card has a
// published/unpublished radio, a [data-part=published] link and a
// [data-part=fields] block for a question typed in. That block switches
// between the pasted text and its fields with tabs, and remembers the view
// in a hidden slotN_view input, so a form shown again opens where it was.
// The parse button asks /api/nominate/parse, and the load button beside a
// gotquestions or telegram link (or Enter in it) asks /api/nominate/load; either fills the
// fields without leaving the page. Without this script both post the form,
// which does the same and comes back unsaved.
import S from "./i18nstrings.js";
import { isLoadable, needsFields, PARSED_FIELDS, type Parsed } from "./nominate-model.js";

function field(card: HTMLElement, name: string): HTMLInputElement | HTMLTextAreaElement | null {
  return card.querySelector(`[name="slot${card.dataset.slot}_${name}"]`);
}

function showView(block: HTMLElement, view: string): void {
  for (const tab of block.querySelectorAll<HTMLElement>("[role=tab]")) {
    tab.classList.toggle("active", tab.dataset.view === view);
    tab.setAttribute("aria-selected", String(tab.dataset.view === view));
  }
  for (const panel of block.querySelectorAll<HTMLElement>("[data-panel]")) panel.hidden = panel.dataset.panel !== view;
  const input = block.querySelector<HTMLInputElement>('input[type=hidden][name$="_view"]');
  if (input) input.value = view;
}

// hasFields says the slot's fields hold something: typed, parsed or loaded.
function hasFields(card: HTMLElement): boolean {
  return PARSED_FIELDS.some((name) => (field(card, name)?.value.trim() ?? "") !== "");
}

function syncParts(card: HTMLElement): void {
  const checked = card.querySelector<HTMLInputElement>('input[type=radio][name$="_kind"]:checked');
  const published = checked?.value !== "unpublished";
  const link = field(card, "link")?.value ?? "";
  const linkPart = card.querySelector<HTMLElement>("[data-part=published]");
  const fieldsPart = card.querySelector<HTMLElement>("[data-part=fields]");
  const load = card.querySelector<HTMLElement>("[data-load]");
  if (linkPart) linkPart.hidden = !published;
  if (load) load.hidden = !(published && isLoadable(link));
  if (fieldsPart) fieldsPart.hidden = !(needsFields(published, link) || hasFields(card));
}

function fill(card: HTMLElement, parsed: Parsed): void {
  for (const name of PARSED_FIELDS) {
    const el = field(card, name);
    if (el) el.value = parsed[name];
  }
}

// message shows a failure under where it happened, or hides it.
function message(where: Element | null, text: string): void {
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
async function ask(url: string, body: object): Promise<Parsed | string> {
  try {
    const res = await fetch(url, {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    if (!res.ok) return (await res.text()).trim() || S.forms.failed();
    return (await res.json()) as Parsed;
  } catch {
    return S.forms.failed();
  }
}

async function parse(card: HTMLElement, block: HTMLElement): Promise<void> {
  const raw = field(card, "raw")?.value.trim() ?? "";
  if (!raw) return;
  const parsed = await ask("/api/nominate/parse", { text: raw });
  const panel = block.querySelector("[data-panel=text]");
  if (typeof parsed === "string" || !parsed.ok) {
    message(panel, typeof parsed === "string" ? parsed : S.nominate.slot.parseFailed());
    return;
  }
  message(panel, "");
  fill(card, parsed);
  showView(block, "fields");
}

// load reads a published question into the fields, from gotquestions or a
// telegram post. When that fails, the fields open on the text view, for the
// nominator to paste it.
async function load(card: HTMLElement, block: HTMLElement): Promise<void> {
  const link = field(card, "link");
  if (!link || !isLoadable(link.value)) return;
  const loaded = await ask("/api/nominate/load", { link: link.value.trim() });
  const where = card.querySelector("[data-part=published]");
  block.hidden = false;
  if (typeof loaded === "string") {
    message(where, loaded);
    showView(block, "text");
    return;
  }
  message(where, "");
  fill(card, loaded);
  const tournament = field(card, "tournament");
  if (tournament && !tournament.value && loaded.tournament) tournament.value = loaded.tournament;
  showView(block, "fields");
}

function wire(card: HTMLElement): void {
  const block = card.querySelector<HTMLElement>("[data-part=fields]");
  if (!block) return;
  showView(block, block.dataset.view || "text");
  syncParts(card);
  card.addEventListener("change", () => syncParts(card));
  field(card, "link")?.addEventListener("input", () => syncParts(card));
  field(card, "link")?.addEventListener("keydown", (event: Event) => {
    if (!(event instanceof KeyboardEvent)) return;
    if (event.key !== "Enter") return;
    event.preventDefault();
    void load(card, block);
  });
  card.addEventListener("click", (event) => {
    const target = event.target instanceof Element ? event.target.closest<HTMLElement>("button") : null;
    if (!target) return;
    if (target.getAttribute("role") === "tab" && target.dataset.view) {
      showView(block, target.dataset.view);
    } else if (target.getAttribute("name") === "parse") {
      event.preventDefault();
      void parse(card, block);
    } else if (target.hasAttribute("data-load")) {
      event.preventDefault();
      void load(card, block);
    }
  });
}

for (const card of document.querySelectorAll<HTMLElement>("form[data-nominate] [data-slot]")) wire(card);

// Enter in a one-line field would submit the form through its first submit
// button, which is the first slot's load or parse. The link field loads its
// own slot (above); the others do nothing.
document.querySelector("form[data-nominate]")?.addEventListener("keydown", (event) => {
  const el = event.target;
  if (event instanceof KeyboardEvent && event.key === "Enter" && el instanceof HTMLInputElement && el.type !== "submit") {
    event.preventDefault();
  }
});
