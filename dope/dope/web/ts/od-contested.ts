// od-contested.ts — the OD's contested answers as the hosts and the jury work with them:
// the dialog a host records one in, and the tab the jury rules in. The page
// (od.ts) owns the document and says what a number means; this module draws
// and talks to the server.
import {autocomplete} from "../../../../dopeuikit/assets/ts/suggest.js";
import {contestedKey, groupContested} from "./od-protocol.js";
import {sheetHead} from "./sheet-pins.js";
import type {ContestedGroup, ContestedStatus, ContestedText} from "./od-protocol.js";
import S from "./i18nstrings.js";

// How many earlier answers the suggest list offers at once.
const SUGGEST_LIMIT = 8;

// fetchContested reads the Game's contested answers with their text; null when the
// server refused (a viewer has no business asking).
export async function fetchContested(apiBase: string): Promise<ContestedText[] | null> {
  try {
    const response = await fetch(`${apiBase}/contested`, {headers: {Accept: "application/json"}});
    return response.ok ? await response.json() as ContestedText[] : null;
  } catch {
    return null;
  }
}

// sendContested writes one change and answers the whole list, or null after
// telling the person why it failed.
export async function sendContested(apiBase: string, method: string, path: string, body: object): Promise<ContestedText[] | null> {
  try {
    const response = await fetch(`${apiBase}/contested${path}`, {
      method,
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify(body),
    });
    if (!response.ok) throw new Error((await response.text()).trim());
    return await response.json() as ContestedText[];
  } catch (error) {
    window.alert((error as Error).message || S.od.contested.saveFailed());
    return null;
  }
}

export interface ContestedDialogSpec {
  question: number;
  // The team's number as the sheet shows it, and the contested answer already on that
  // cell, if any.
  number: number;
  existing?: ContestedText;
  // Everything recorded so far, for the suggest list.
  known: readonly ContestedText[];
  teamName: (number: number) => string | null;
  // save gets every team the host typed, in the order typed.
  save: (numbers: number[], answer: string) => void;
  remove: () => void;
}

// openContestedDialog asks for the team and its answer. The answer field
// offers the answers other teams gave to the question, because the jury rules
// on each answer once.
export function openContestedDialog(spec: ContestedDialogSpec): void {
  const dialog = document.createElement("dialog");
  dialog.className = "modal-dialog";
  const form = document.createElement("form");
  form.className = "u-col u-gap-md";

  const title = document.createElement("h2");
  title.textContent = S.od.contested.dialogTitle(String(spec.question + 1));

  const [numberField, numberInput] = field(S.od.contested.teamLabel());
  numberInput.placeholder = S.od.contested.teamPlaceholder();
  numberInput.value = spec.number ? String(spec.number) : "";
  const teamHint = document.createElement("p");
  teamHint.className = "hint";

  const [answerField, answerInput] = field(S.od.contested.answerLabel());
  answerInput.value = spec.existing?.answer || "";
  const answerHint = document.createElement("p");
  answerHint.className = "hint";
  answerHint.textContent = S.od.contested.answerHint();
  const groups = groupContested(spec.known.filter((item) => item.question === spec.question));
  autocomplete(answerInput, (typed) => {
    const needle = contestedKey(typed);
    return groups
      .filter((group) => contestedKey(group.answer).includes(needle))
      .slice(0, SUGGEST_LIMIT)
      .map((group) => ({value: group.answer, label: group.answer, hint: S.od.contested.given(group.numbers.length)}));
  });

  const actions = document.createElement("div");
  actions.className = "modal-actions";
  if (spec.existing) {
    const remove = button(S.od.contested.delete(), "btn btn-danger");
    remove.addEventListener("click", () => {
      dialog.close();
      spec.remove();
    });
    const spacer = document.createElement("span");
    spacer.className = "u-grow";
    actions.append(remove, spacer);
  }
  const cancel = button(S.od.contested.cancel(), "btn");
  cancel.addEventListener("click", () => dialog.close());
  const submit = button(S.od.contested.save(), "btn btn-primary");
  submit.type = "submit";
  actions.append(cancel, submit);

  // The hint names every team typed, or the first number that is no team.
  const sync = () => {
    const parts = numberParts(numberInput.value);
    const unknown = parts.find((part) => !/^\d+$/.test(part) || spec.teamName(Number(part)) === null);
    teamHint.hidden = parts.length === 0;
    teamHint.textContent = unknown === undefined
      ? typedNumbers(numberInput.value).map((number) => spec.teamName(number)).join(", ")
      : S.od.contested.noTeam(unknown);
    teamHint.classList.toggle("hint-danger", unknown !== undefined);
    submit.disabled = parts.length === 0 || unknown !== undefined || answerInput.value.trim() === "";
  };
  numberInput.addEventListener("input", sync);
  answerInput.addEventListener("input", sync);
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    if (submit.disabled) return;
    dialog.close();
    spec.save(typedNumbers(numberInput.value), answerInput.value.trim());
  });
  sync();

  form.append(title, numberField, teamHint, answerField, answerHint, actions);
  dialog.appendChild(form);
  dialog.addEventListener("close", () => dialog.remove(), {once: true});
  document.body.appendChild(dialog);
  dialog.showModal();
  (spec.number ? answerInput : numberInput).focus();
}

// numberParts splits "3, 7 12" at commas, semicolons and spaces.
function numberParts(text: string): string[] {
  return text.split(/[\s,;]+/).filter(Boolean);
}

// typedNumbers reads "3, 7 12" as the teams 3, 7 and 12, each once, leaving
// out whatever is not a whole number.
export function typedNumbers(text: string): number[] {
  return [...new Set(numberParts(text).filter((part) => /^\d+$/.test(part)).map(Number))];
}

// field is a labelled text input. It sits in a suggest anchor, so a list
// hangs under it.
function field(caption: string): [HTMLElement, HTMLInputElement] {
  const label = document.createElement("label");
  label.className = "field";
  const text = document.createElement("span");
  text.textContent = caption;
  const input = document.createElement("input");
  input.type = "text";
  input.className = "input";
  input.autocomplete = "off";
  const anchor = document.createElement("span");
  anchor.className = "suggest-anchor u-col";
  anchor.appendChild(input);
  label.append(text, anchor);
  return [label, input];
}

function button(text: string, className: string): HTMLButtonElement {
  const node = document.createElement("button");
  node.type = "button";
  node.className = className;
  node.textContent = text;
  return node;
}

const STATUSES: Array<[ContestedStatus, () => string]> = [
  ["pending", () => S.od.contested.pending()],
  ["accepted", () => S.od.contested.accepted()],
  ["rejected", () => S.od.contested.rejected()],
];

export interface ContestedViewSpec {
  known: readonly ContestedText[];
  teamLabel: (number: number) => string;
  rule: (group: ContestedGroup, status: ContestedStatus) => void;
}

// buildContestedView is the jury's tab: one row per answer to a question,
// the teams that gave it, and the ruling as a switch of three.
export function buildContestedView(spec: ContestedViewSpec): HTMLElement {
  const groups = groupContested(spec.known);
  if (groups.length === 0) {
    const empty = document.createElement("p");
    empty.className = "hint";
    empty.textContent = S.od.contested.empty();
    return empty;
  }
  const table = document.createElement("table");
  table.className = "data-table";
  const head = document.createElement("tr");
  // The question column is as wide as its caption; the answer takes the rest.
  for (const [caption, className] of [[S.od.contested.colQuestion(), "col-fit"], [S.od.contested.colAnswer(), ""]]) {
    const cell = document.createElement("th");
    cell.className = className;
    cell.textContent = caption;
    head.appendChild(cell);
  }
  table.appendChild(sheetHead([{row: head}]));
  const body = table.createTBody();
  for (const group of groups) {
    const row = body.insertRow();
    const number = row.insertCell();
    number.className = "col-fit";
    number.textContent = String(group.question + 1);
    // The answer, who gave it and the ruling stack in one cell, so the switch
    // stays on a phone's screen. The stack keeps each line to its own width,
    // so the switch is no wider than its three choices.
    const cell = document.createElement("div");
    cell.className = "u-col u-gap-sm u-align-start";
    row.insertCell().appendChild(cell);
    const answer = document.createElement("strong");
    answer.textContent = group.answer;
    // The jury gets no teams (number 0): it rules on the answer alone.
    const teams = document.createElement("span");
    teams.className = "hint";
    teams.textContent = group.numbers.filter((n) => n > 0).map(spec.teamLabel).join(", ");
    teams.hidden = teams.textContent === "";
    const seg = document.createElement("div");
    seg.className = "seg";
    seg.setAttribute("role", "group");
    for (const [status, label] of STATUSES) {
      const choice = button(label(), "seg-btn" + (status === group.status ? " active" : ""));
      choice.setAttribute("aria-pressed", String(status === group.status));
      choice.addEventListener("click", () => {
        if (status !== group.status) spec.rule(group, status);
      });
      seg.appendChild(choice);
    }
    cell.append(answer, teams, seg);
  }
  const wrap = document.createElement("div");
  wrap.className = "table-scroll";
  wrap.appendChild(table);
  return wrap;
}
