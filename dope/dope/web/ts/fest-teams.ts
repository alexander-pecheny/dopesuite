// The team editor of the fest's teams page (ADR-0024): the host adds a team,
// renames one, changes its people, or takes it off the roster. People are
// suggested from the fest roster as the host types, and anybody else can be
// typed by name. A suggested person who plays for another team moves here.
// The server holds the rules and answers each save; the page reloads after.

import {autocomplete} from "../../../../dopeuikit/assets/ts/suggest.js";
import type {Choice} from "../../../../dopeuikit/assets/ts/suggest.js";
import {icon} from "./icons_gen.js";
import S from "./i18nstrings.js";

interface TeamPlayer {
  rating_id: number;
  first_name: string;
  last_name: string;
  team?: string;
}

interface TeamDetail {
  id: number;
  rating_id: number;
  name: string;
  city: string;
  hand: boolean;
  players: TeamPlayer[];
  choices: TeamPlayer[];
}

function fullName(p: TeamPlayer): string {
  return `${p.first_name} ${p.last_name}`.trim();
}

// playerKey matches the server's roster.PlayerKey: a rating id, else the name.
function playerKey(p: TeamPlayer): string {
  return p.rating_id > 0 ? `rating:${p.rating_id}` : `name:${fullName(p).toLowerCase()}`;
}

// typedPlayer is a person typed by name: the first word is the given name, the
// rest the surname, which is how the rating roster stores people.
function typedPlayer(text: string): TeamPlayer {
  const words = text.trim().split(/\s+/);
  return {rating_id: 0, first_name: words[0] || "", last_name: words.slice(1).join(" ")};
}

async function request(url: string, method: string, body?: unknown): Promise<{ok: boolean; data: unknown; message: string}> {
  try {
    const response = await fetch(url, {
      method,
      headers: body === undefined ? undefined : {"Content-Type": "application/json"},
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    const text = (await response.text()).trim();
    if (!response.ok) return {ok: false, data: null, message: text || S.fest.teamEdit.failed()};
    return {ok: true, data: text ? JSON.parse(text) : null, message: ""};
  } catch {
    return {ok: false, data: null, message: S.fest.teamEdit.failed()};
  }
}

function openTeamDialog(api: string, team: TeamDetail): void {
  const isNew = !(team.id > 0);
  const draft: TeamPlayer[] = team.players.slice();

  const dialog = document.createElement("dialog");
  dialog.className = "modal-dialog roster-dialog";
  const form = document.createElement("form");
  form.className = "u-col u-gap-md";

  const title = document.createElement("h2");
  title.textContent = isNew ? S.fest.teamEdit.titleNew() : S.fest.teamEdit.title(team.name);
  const hint = document.createElement("p");
  hint.className = "hint";
  hint.textContent = team.hand || isNew ? S.fest.teamEdit.hintHand() : S.fest.teamEdit.hintRating();

  const field = (label: string, value: string): [HTMLElement, HTMLInputElement] => {
    const wrap = document.createElement("label");
    wrap.className = "field";
    const caption = document.createElement("span");
    caption.textContent = label;
    const input = document.createElement("input");
    input.type = "text";
    input.className = "input";
    input.autocomplete = "off";
    input.value = value;
    wrap.append(caption, input);
    return [wrap, input];
  };
  const [nameField, nameInput] = field(S.fest.teamEdit.name(), team.name);
  nameInput.required = true;
  const [cityField, cityInput] = field(S.fest.teamEdit.city(), team.city);

  const list = document.createElement("div");
  list.className = "u-col u-gap-xs";
  const error = document.createElement("p");
  error.className = "hint hint-danger";
  error.hidden = true;
  const fail = (message: string) => {
    error.textContent = message;
    error.hidden = false;
  };
  const drawList = () => {
    list.replaceChildren(...draft.map((player, index) => playerRow(player, () => {
      draft.splice(index, 1);
      drawList();
    })));
  };
  drawList();

  // The add row: a field that suggests the fest's people, and its button.
  const input = document.createElement("input");
  input.type = "text";
  input.className = "input";
  input.autocomplete = "off";
  input.spellcheck = false;
  input.placeholder = S.fest.teamEdit.addPlaceholder();
  const anchor = document.createElement("span");
  anchor.className = "suggest-anchor u-col u-grow";
  anchor.appendChild(input);
  const byValue = new Map<string, TeamPlayer>();
  const suggestions: Choice[] = team.choices.map((c) => {
    const value = `${playerKey(c)}`;
    byValue.set(value, c);
    const other = c.team && c.team !== team.name ? S.fest.teamEdit.movesFrom(c.team) : "";
    return {value, label: fullName(c), hint: other};
  });
  let picked: TeamPlayer | null = null;
  const suggest = autocomplete(input, (q) => {
    const needle = q.trim().toLowerCase();
    if (!needle) return [];
    const taken = new Set(draft.map(playerKey));
    return suggestions
      .filter((c) => !taken.has(c.value))
      .filter((c) => c.label.toLowerCase().split(/\s+/).some((word) => word.startsWith(needle)) || c.label.toLowerCase().startsWith(needle))
      .slice(0, 12);
  }, (choice) => {
    picked = byValue.get(choice.value) || null;
    add();
  });
  const add = () => {
    const player = picked || typedPlayer(input.value);
    picked = null;
    if (!fullName(player)) return;
    if (draft.some((p) => playerKey(p) === playerKey(player))) {
      fail(S.fest.teamEdit.playerTwice(fullName(player)));
      return;
    }
    error.hidden = true;
    draft.push({rating_id: player.rating_id, first_name: player.first_name, last_name: player.last_name});
    input.value = "";
    suggest.close();
    drawList();
    input.focus();
  };
  const addButton = document.createElement("button");
  addButton.type = "button";
  addButton.className = "btn";
  addButton.textContent = S.fest.teamEdit.add();
  addButton.addEventListener("click", add);
  input.addEventListener("keydown", (event) => {
    // Enter adds the typed person; the form's own submit is the save button.
    if (event.key === "Enter") {
      event.preventDefault();
      add();
    }
  });
  const addRow = document.createElement("div");
  addRow.className = "u-row u-gap-sm u-align-center";
  addRow.append(anchor, addButton);

  const busy = (on: boolean) => {
    for (const button of form.querySelectorAll<HTMLButtonElement>("button")) button.disabled = on;
  };
  const done = () => {
    dialog.close();
    window.location.reload();
  };

  // Taking the team off is not a peer of save and cancel: a row of its own.
  let deleteRow: HTMLElement | null = null;
  if (!isNew) {
    const remove = document.createElement("button");
    remove.type = "button";
    remove.className = "btn btn-ghost";
    remove.textContent = S.fest.teamEdit.delete();
    remove.addEventListener("click", () => {
      if (!window.confirm(S.fest.teamEdit.deleteConfirm(team.name))) return;
      busy(true);
      void request(`${api}/teams/${team.id}`, "DELETE").then((answer) => {
        busy(false);
        if (answer.ok) done();
        else fail(answer.message);
      });
    });
    deleteRow = document.createElement("div");
    deleteRow.className = "u-row";
    deleteRow.appendChild(remove);
  }

  const actions = document.createElement("div");
  actions.className = "modal-actions";
  const cancel = document.createElement("button");
  cancel.type = "button";
  cancel.className = "btn";
  cancel.textContent = S.fest.teamEdit.cancel();
  cancel.addEventListener("click", () => dialog.close());
  const save = document.createElement("button");
  save.type = "submit";
  save.className = "btn";
  save.textContent = S.fest.teamEdit.save();
  actions.append(cancel, save);

  form.addEventListener("submit", (event) => {
    event.preventDefault();
    error.hidden = true;
    // A name left in the field counts as added: the host typed it to have it.
    if (input.value.trim()) add();
    if (!error.hidden) return;
    busy(true);
    const body = {name: nameInput.value, city: cityInput.value, players: draft};
    const sent = isNew ? request(`${api}/teams`, "POST", body) : request(`${api}/teams/${team.id}`, "PUT", body);
    void sent.then((answer) => {
      busy(false);
      if (answer.ok) done();
      else fail(answer.message);
    });
  });

  form.append(title, hint, nameField, cityField, list, addRow, error);
  if (deleteRow) form.appendChild(deleteRow);
  form.appendChild(actions);
  dialog.appendChild(form);
  dialog.addEventListener("close", () => {
    suggest.close();
    dialog.remove();
  });
  document.body.appendChild(dialog);
  dialog.showModal();
  (isNew ? nameInput : input).focus();
}

// playerRow is one person of the draft: the name, and a cross to take them off.
function playerRow(player: TeamPlayer, remove: () => void): HTMLElement {
  const row = document.createElement("div");
  row.className = "u-row u-gap-sm u-align-center u-justify-between";
  const name = document.createElement("span");
  name.textContent = fullName(player);
  const button = document.createElement("button");
  button.type = "button";
  button.className = "action-icon";
  const label = S.fest.teamEdit.remove(fullName(player));
  button.title = label;
  button.setAttribute("aria-label", label);
  button.appendChild(icon("x"));
  button.addEventListener("click", remove);
  row.append(name, button);
  return row;
}

// Bind the page: the add button carries the fest's API, each pencil its team.
(() => {
  const addButton = document.querySelector<HTMLElement>("[data-team-add]");
  const api = addButton?.dataset.teamAdd || "";
  if (!addButton || !api) return;
  const open = async (id: string) => {
    const answer = await request(`${api}/teams/${id}`, "GET");
    if (answer.ok) openTeamDialog(api, answer.data as TeamDetail);
  };
  addButton.addEventListener("click", () => void open("new"));
  for (const pencil of document.querySelectorAll<HTMLElement>("[data-team-edit]")) {
    pencil.addEventListener("click", (event) => {
      event.preventDefault();
      void open(pencil.dataset.teamEdit || "");
    });
  }
})();
