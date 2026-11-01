// od-contested.ts — the OD's contested answers as the hosts and the jury work with them:
// the dialog a host records one in, and the tab the jury rules in. The page
// (od.ts) owns the document and says what a number means; this module draws
// and talks to the server.
import {autocomplete} from "../../../../dopeuikit/assets/ts/suggest.js";
import {contestedKey, groupContested} from "./od-protocol.js";
import {sheetHead} from "./sheet-pins.js";
import {icon} from "./icons_gen.js";
import type {IconName} from "./icons_gen.js";
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
  // The question (from 0) of the selected cell, null when no cell is selected;
  // questions is how many the Game has.
  question: number | null;
  questions: number;
  // The venue whose numbers the team field takes, in a synch fest.
  venue?: string;
  // The team's number as the sheet shows it, and the contested answer already on that
  // cell, if any.
  number: number;
  existing?: ContestedText;
  // Everything recorded so far, for the suggest list.
  known: readonly ContestedText[];
  teamName: (number: number) => string | null;
  // save gets the question and every team the host typed, in the order typed.
  save: (question: number, numbers: number[], answer: string) => void;
  remove: () => void;
}

// openContestedDialog asks for the question, the team and its answer. The
// answer field offers the answers other teams gave to the question, because
// the jury rules on each answer once.
export function openContestedDialog(spec: ContestedDialogSpec): void {
  const {dialog, form, actions, submit} = modal(spec.venue ? S.od.contested.dialogTitleVenue(spec.venue) : S.od.contested.dialogTitle());

  const [questionField, questionInput] = field(S.od.contested.questionLabel());
  questionInput.inputMode = "numeric";
  questionInput.value = spec.question === null ? "" : String(spec.question + 1);
  const questionHint = hint();
  questionHint.classList.add("hint-danger");

  const [numberField, numberInput] = field(S.od.contested.teamLabel());
  numberInput.placeholder = S.od.contested.teamPlaceholder();
  numberInput.value = spec.number ? String(spec.number) : "";
  const teamHint = hint();

  const [answerField, answerInput] = field(S.od.contested.answerLabel());
  answerInput.value = spec.existing?.answer || "";
  const answerHint = hint();
  answerHint.textContent = S.od.contested.answerHint();
  autocomplete(answerInput, (typed) => {
    const question = questionOf(questionInput.value, spec.questions);
    const needle = contestedKey(typed);
    return groupContested(spec.known.filter((item) => item.question === question))
      .filter((group) => contestedKey(group.answer).includes(needle))
      .slice(0, SUGGEST_LIMIT)
      .map((group) => ({value: group.answer, label: group.answer, hint: S.od.contested.given(group.numbers.length)}));
  });

  if (spec.existing) {
    const remove = button(S.od.contested.delete(), "btn btn-danger");
    remove.addEventListener("click", () => {
      dialog.close();
      spec.remove();
    });
    const spacer = document.createElement("span");
    spacer.className = "u-grow";
    actions.prepend(remove, spacer);
  }

  // The hints name every team typed, or the first number that is no team,
  // and a question the Game does not have.
  const sync = () => {
    const question = questionOf(questionInput.value, spec.questions);
    const typedQuestion = questionInput.value.trim();
    questionHint.hidden = typedQuestion === "" || question !== null;
    questionHint.textContent = S.od.contested.noQuestion(typedQuestion);
    const parts = numberParts(numberInput.value);
    const unknown = parts.find((part) => !/^\d+$/.test(part) || spec.teamName(Number(part)) === null);
    teamHint.hidden = parts.length === 0;
    teamHint.textContent = unknown === undefined
      ? typedNumbers(numberInput.value).map((number) => spec.teamName(number)).join(", ")
      : S.od.contested.noTeam(unknown);
    teamHint.classList.toggle("hint-danger", unknown !== undefined);
    submit.disabled = question === null || parts.length === 0 || unknown !== undefined || answerInput.value.trim() === "";
  };
  for (const input of [questionInput, numberInput, answerInput]) input.addEventListener("input", sync);
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    const question = questionOf(questionInput.value, spec.questions);
    if (submit.disabled || question === null) return;
    dialog.close();
    spec.save(question, typedNumbers(numberInput.value), answerInput.value.trim());
  });
  sync();

  form.insertBefore(fields(questionField, questionHint, numberField, teamHint, answerField, answerHint), actions);
  show(dialog);
  (spec.question === null ? questionInput : spec.number ? answerInput : numberInput).focus();
}

// openMoveDialog asks an organizer which question an answer moves to.
export function openMoveDialog(group: ContestedGroup, questions: number, move: (to: number) => void): void {
  const {dialog, form, actions, submit} = modal(S.od.contested.moveTitle(group.answer, String(group.question + 1)));
  submit.textContent = S.od.contested.move();
  const [questionField, questionInput] = field(S.od.contested.questionLabel());
  questionInput.inputMode = "numeric";
  const questionHint = hint();
  questionHint.classList.add("hint-danger");
  const target = () => {
    const to = questionOf(questionInput.value, questions);
    return to === group.question ? null : to;
  };
  const sync = () => {
    const typed = questionInput.value.trim();
    questionHint.hidden = typed === "" || questionOf(typed, questions) !== null;
    questionHint.textContent = S.od.contested.noQuestion(typed);
    submit.disabled = target() === null;
  };
  questionInput.addEventListener("input", sync);
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    const to = target();
    if (to === null) return;
    dialog.close();
    move(to);
  });
  sync();
  form.insertBefore(fields(questionField, questionHint), actions);
  show(dialog);
  questionInput.focus();
}

// questionOf reads a question's number as the hosts call it (from 1) and
// answers its index, or null for no question of the Game.
function questionOf(text: string, questions: number): number | null {
  const typed = text.trim();
  if (!/^\d+$/.test(typed)) return null;
  const n = Number(typed);
  return n >= 1 && n <= questions ? n - 1 : null;
}

// modal is a dialog's frame: the title, and the actions Cancel and Save,
// which a caller puts its fields above.
function modal(caption: string) {
  const dialog = document.createElement("dialog");
  dialog.className = "modal-dialog";
  const form = document.createElement("form");
  form.className = "u-col u-gap-md";
  const title = document.createElement("h2");
  title.textContent = caption;
  const actions = document.createElement("div");
  actions.className = "modal-actions";
  const cancel = button(S.od.contested.cancel(), "btn");
  cancel.addEventListener("click", () => dialog.close());
  const submit = button(S.od.contested.save(), "btn btn-primary");
  submit.type = "submit";
  actions.append(cancel, submit);
  form.append(title, actions);
  dialog.appendChild(form);
  dialog.addEventListener("close", () => dialog.remove(), {once: true});
  return {dialog, form, actions, submit};
}

function show(dialog: HTMLDialogElement): void {
  document.body.appendChild(dialog);
  dialog.showModal();
}

function fields(...nodes: HTMLElement[]): DocumentFragment {
  const fragment = document.createDocumentFragment();
  fragment.append(...nodes);
  return fragment;
}

function hint(): HTMLParagraphElement {
  const node = document.createElement("p");
  node.className = "hint";
  return node;
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
  // rule is the jury's; without it the ruling is text, with nothing to press.
  rule?: (group: ContestedGroup, status: ContestedStatus) => void;
  // manage is an organizer's: each answer can go, or move to another question.
  manage?: {
    questions: number;
    move: (group: ContestedGroup, to: number) => void;
    remove: (group: ContestedGroup) => void;
  };
}

// rulingSwitch is the jury's switch of three for one answer.
function rulingSwitch(group: ContestedGroup, rule: (group: ContestedGroup, status: ContestedStatus) => void): HTMLElement {
  const seg = document.createElement("div");
  seg.className = "seg";
  seg.setAttribute("role", "group");
  for (const [status, label] of STATUSES) {
    const choice = button(label(), "seg-btn" + (status === group.status ? " active" : ""));
    choice.setAttribute("aria-pressed", String(status === group.status));
    choice.addEventListener("click", () => {
      if (status !== group.status) rule(group, status);
    });
    seg.appendChild(choice);
  }
  return seg;
}

// rulingText is the ruling as a host reads it, with nothing to press.
function rulingText(status: ContestedStatus): HTMLElement {
  const text = document.createElement("span");
  text.textContent = STATUSES.find(([s]) => s === status)?.[1]() || "";
  return text;
}

function manageButtons(group: ContestedGroup, manage: NonNullable<ContestedViewSpec["manage"]>): HTMLElement {
  const wrap = document.createElement("span");
  wrap.className = "u-row u-gap-xs";
  wrap.append(
    actionButton("arrow-up-down", S.od.contested.move(), () => openMoveDialog(group, manage.questions, (to) => manage.move(group, to))),
    actionButton("trash-2", S.od.contested.delete(), () => {
      if (window.confirm(S.od.contested.deleteConfirm(group.answer, String(group.question + 1)))) manage.remove(group);
    }),
  );
  return wrap;
}

function actionButton(name: IconName, label: string, onClick: () => void): HTMLButtonElement {
  const node = button("", "action-icon");
  node.title = label;
  node.setAttribute("aria-label", label);
  node.appendChild(icon(name));
  node.addEventListener("click", onClick);
  return node;
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
    // An organizer's buttons ride the answer's line, which a phone keeps on screen.
    const line = document.createElement("div");
    line.className = "u-row u-gap-sm u-align-center";
    line.appendChild(answer);
    if (spec.manage) line.appendChild(manageButtons(group, spec.manage));
    cell.append(line, teams, spec.rule ? rulingSwitch(group, spec.rule) : rulingText(group.status));
  }
  const wrap = document.createElement("div");
  wrap.className = "table-scroll";
  wrap.appendChild(table);
  return wrap;
}
