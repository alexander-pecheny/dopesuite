// The nomination form (server/nominateform.go). Each [data-slot] card has a
// published/unpublished radio, a [data-part=published] link and a
// [data-part=fields] block for a question typed in. That block switches
// between the pasted text and its fields with tabs, and remembers the view
// in a hidden slotN_view input, so a form shown again opens where it was.
// The parse button asks /api/nominate/parse and fills the fields without leaving
// the page; without this script it posts the form, which parses and comes
// back unsaved.
import S from "./i18nstrings.js";
import { needsFields, PARSED_FIELDS, type Parsed } from "./nominate-model.js";

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

function syncParts(card: HTMLElement): void {
  const checked = card.querySelector<HTMLInputElement>('input[type=radio][name$="_kind"]:checked');
  const published = checked?.value !== "unpublished";
  const link = field(card, "link")?.value ?? "";
  const linkPart = card.querySelector<HTMLElement>("[data-part=published]");
  const fieldsPart = card.querySelector<HTMLElement>("[data-part=fields]");
  if (linkPart) linkPart.hidden = !published;
  if (fieldsPart) fieldsPart.hidden = !needsFields(published, link);
}

function parseMessage(block: HTMLElement, text: string): void {
  let p = block.querySelector<HTMLElement>("[data-parse-message]");
  if (!p) {
    p = document.createElement("p");
    p.className = "hint hint-danger";
    p.dataset.parseMessage = "";
    block.querySelector("[data-panel=text]")?.append(p);
  }
  p.textContent = text;
  p.hidden = text === "";
}

// askParse sends the pasted text to the parse endpoint; a failure comes back
// as the words to show.
async function askParse(raw: string): Promise<Parsed | string> {
  try {
    const res = await fetch("/api/nominate/parse", {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ text: raw }),
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
  const parsed = await askParse(raw);
  if (typeof parsed === "string" || !parsed.ok) {
    parseMessage(block, typeof parsed === "string" ? parsed : S.nominate.slot.parseFailed());
    return;
  }
  parseMessage(block, "");
  for (const name of PARSED_FIELDS) {
    const el = field(card, name);
    if (el) el.value = parsed[name];
  }
  showView(block, "fields");
}

function wire(card: HTMLElement): void {
  const block = card.querySelector<HTMLElement>("[data-part=fields]");
  if (!block) return;
  showView(block, block.dataset.view || "text");
  syncParts(card);
  card.addEventListener("change", () => syncParts(card));
  field(card, "link")?.addEventListener("input", () => syncParts(card));
  block.addEventListener("click", (event) => {
    const target = event.target instanceof Element ? event.target.closest<HTMLElement>("button") : null;
    if (!target) return;
    if (target.getAttribute("role") === "tab" && target.dataset.view) {
      showView(block, target.dataset.view);
    } else if (target.getAttribute("name") === "parse") {
      event.preventDefault();
      void parse(card, block);
    }
  });
}

for (const card of document.querySelectorAll<HTMLElement>("form[data-nominate] [data-slot]")) wire(card);
