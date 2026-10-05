// The team editor of the fest's teams page (ADR-0024): the host adds a team,
// renames one, changes its people, or takes it off the roster. People are
// suggested from the fest roster and from everybody the rating site knows, and
// anybody else can be typed by name. A suggested person who plays for another
// team of the fest moves here. A new team can be the rating site's team,
// found by name, and still play under a one-off name; the base-roster button adds its
// base roster to start from. The server holds the rules and answers each save;
// the page reloads after.

import {autocomplete} from "../../../../dopeuikit/assets/ts/suggest.js";
import type {Choice} from "../../../../dopeuikit/assets/ts/suggest.js";
import {icon} from "./icons_gen.js";
import S from "./i18nstrings.js";

// The rating search waits for a pause in typing this long.
const SEARCH_DEBOUNCE_MS = 250;
// Most people the player picker offers at once.
const PLAYER_CHOICE_LIMIT = 14;

interface TeamPlayer {
  rating_id: number;
  first_name: string;
  last_name: string;
  patronymic?: string;
  games?: number;
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

interface RatingTeam {
  rating_id: number;
  name: string;
  city: string;
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

function matches(label: string, q: string): boolean {
  const needle = q.trim().toLowerCase();
  if (!needle) return false;
  const words = label.toLowerCase().split(/\s+/);
  return needle.split(/\s+/).every((part) => words.some((word) => word.startsWith(part)));
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

// remoteChoices keeps the rating site's answers per query and asks for a new
// one a moment after the host stops typing; each answer redraws the list.
function remoteChoices<T>(url: (q: string) => string, redraw: () => void): (q: string) => T[] {
  const answers = new Map<string, T[]>();
  let timer = 0;
  return (q: string) => {
    const key = q.trim().toLowerCase();
    if (key.length < 2) return [];
    const known = answers.get(key);
    if (known) return known;
    window.clearTimeout(timer);
    timer = window.setTimeout(() => {
      void request(url(key), "GET").then((answer) => {
        answers.set(key, answer.ok ? answer.data as T[] : []);
        redraw();
      });
    }, SEARCH_DEBOUNCE_MS);
    return [];
  };
}

function field(label: string, value: string): [HTMLElement, HTMLInputElement] {
  const wrap = document.createElement("label");
  wrap.className = "field";
  const caption = document.createElement("span");
  caption.textContent = label;
  const input = document.createElement("input");
  input.type = "text";
  input.className = "input";
  input.autocomplete = "off";
  input.spellcheck = false;
  input.value = value;
  wrap.append(caption, input);
  return [wrap, input];
}

function hintLine(text: string): HTMLElement {
  const hint = document.createElement("p");
  hint.className = "hint";
  hint.textContent = text;
  return hint;
}

function openTeamDialog(api: string, team: TeamDetail): void {
  const isNew = !(team.id > 0);
  const draft: TeamPlayer[] = team.players.slice();
  let ratingTeam: RatingTeam | null = team.rating_id > 0 ? {rating_id: team.rating_id, name: team.name, city: team.city} : null;

  const dialog = document.createElement("dialog");
  dialog.className = "modal-dialog roster-dialog";
  const form = document.createElement("form");
  form.className = "u-col u-gap-md";

  const title = document.createElement("h2");
  title.textContent = isNew ? S.fest.teamEdit.titleNew() : S.fest.teamEdit.title(team.name);
  const hint = hintLine(team.hand || isNew ? S.fest.teamEdit.hintHand() : S.fest.teamEdit.hintRating());

  const [nameField, nameInput] = field(S.fest.teamEdit.name(), team.name);
  nameInput.required = true;
  nameField.appendChild(hintLine(S.fest.teamEdit.nameHint()));
  const [cityField, cityInput] = field(S.fest.teamEdit.city(), team.city);

  const error = document.createElement("p");
  error.className = "hint hint-danger";
  error.hidden = true;
  const fail = (message: string) => {
    error.textContent = message;
    error.hidden = false;
  };

  // A new team: the rating site's team it is, if any.
  const ratingBlock = document.createElement("div");
  ratingBlock.className = "u-col u-gap-xs";
  let teamSuggest: {close(): void} | null = null;
  const drawRatingBlock = () => {
    teamSuggest?.close();
    teamSuggest = null;
    if (!isNew) {
      ratingBlock.replaceChildren();
      return;
    }
    if (ratingTeam) {
      const picked = document.createElement("div");
      picked.className = "u-row u-gap-sm u-align-center u-justify-between";
      const label = document.createElement("span");
      const where = ratingTeam.city ? `${ratingTeam.name} (${ratingTeam.city})` : ratingTeam.name;
      label.textContent = S.fest.teamEdit.ratingTeamPicked(where, String(ratingTeam.rating_id));
      const unpick = document.createElement("button");
      unpick.type = "button";
      unpick.className = "btn btn-ghost";
      unpick.textContent = S.fest.teamEdit.ratingTeamUnpick();
      unpick.addEventListener("click", () => {
        ratingTeam = null;
        drawRatingBlock();
        drawBaseButton();
      });
      picked.append(label, unpick);
      const caption = document.createElement("span");
      caption.textContent = S.fest.teamEdit.ratingTeam();
      ratingBlock.replaceChildren(caption, picked);
      return;
    }
    const [teamField, teamInput] = field(S.fest.teamEdit.ratingTeam(), "");
    teamInput.placeholder = S.fest.teamEdit.ratingTeamPlaceholder();
    const anchor = document.createElement("span");
    anchor.className = "suggest-anchor u-col";
    teamInput.replaceWith(anchor);
    anchor.appendChild(teamInput);
    teamField.appendChild(hintLine(S.fest.teamEdit.ratingTeamHint()));
    const byValue = new Map<string, RatingTeam>();
    const found = remoteChoices<RatingTeam>((q) => `${api}/rating/teams?q=${encodeURIComponent(q)}`, () => suggest.refresh());
    const suggest = autocomplete(teamInput, (q) => found(q).map((t) => {
      const value = String(t.rating_id);
      byValue.set(value, t);
      return {value, label: t.name, hint: [t.city, S.fest.teamEdit.ratingId(value)].filter(Boolean).join(" · ")};
    }), (choice) => {
      const picked = byValue.get(choice.value);
      if (!picked) return;
      ratingTeam = picked;
      if (!nameInput.value.trim()) nameInput.value = picked.name;
      if (!cityInput.value.trim()) cityInput.value = picked.city;
      drawRatingBlock();
      drawBaseButton();
    });
    teamSuggest = suggest;
    ratingBlock.replaceChildren(teamField);
  };
  drawRatingBlock();

  const list = document.createElement("div");
  list.className = "u-col u-gap-xs";
  const drawList = () => {
    list.replaceChildren(...draft.map((player, index) => playerRow(player, () => {
      draft.splice(index, 1);
      drawList();
    })));
  };
  drawList();
  const push = (player: TeamPlayer): boolean => {
    if (!fullName(player) || draft.some((p) => playerKey(p) === playerKey(player))) return false;
    draft.push({rating_id: player.rating_id, first_name: player.first_name, last_name: player.last_name});
    return true;
  };

  // The add row: the fest's people and the rating site's, and its button.
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
  const rated = remoteChoices<TeamPlayer>((q) => `${api}/rating/players?q=${encodeURIComponent(q)}`, () => suggest.refresh());
  const suggest = autocomplete(input, (q) => {
    const taken = new Set(draft.map(playerKey));
    const seen = new Set<string>();
    const out: Choice[] = [];
    const offer = (p: TeamPlayer, hint: string) => {
      const value = playerKey(p);
      if (taken.has(value) || seen.has(value)) return;
      seen.add(value);
      byValue.set(value, p);
      out.push({value, label: fullName(p), hint});
    };
    // The fest's own people first: moving one is the usual edit.
    for (const p of team.choices) {
      if (matches(fullName(p), q)) offer(p, p.team && p.team !== team.name ? S.fest.teamEdit.movesFrom(p.team) : "");
    }
    for (const p of rated(q)) {
      const games = p.games ? S.fest.teamEdit.games(p.games) : "";
      offer(p, [p.patronymic || "", games, S.fest.teamEdit.ratingId(String(p.rating_id))].filter(Boolean).join(" · "));
    }
    return out.slice(0, PLAYER_CHOICE_LIMIT);
  }, (choice) => {
    picked = byValue.get(choice.value) || null;
    add();
  });
  let picked: TeamPlayer | null = null;
  const add = () => {
    const player = picked || typedPlayer(input.value);
    picked = null;
    if (!fullName(player)) return;
    if (!push(player)) {
      fail(S.fest.teamEdit.playerTwice(fullName(player)));
      return;
    }
    error.hidden = true;
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

  // The base-roster button: the rating team's base roster, added to the list as a
  // start the host then trims or extends.
  const baseRow = document.createElement("div");
  baseRow.className = "u-row";
  const drawBaseButton = () => {
    baseRow.replaceChildren();
    if (!ratingTeam) return;
    const base = document.createElement("button");
    base.type = "button";
    base.className = "btn btn-ghost";
    base.textContent = S.fest.teamEdit.baseRoster();
    const teamID = ratingTeam.rating_id;
    base.addEventListener("click", () => {
      base.disabled = true;
      void request(`${api}/rating/teams/${teamID}/base`, "GET").then((answer) => {
        base.disabled = false;
        if (!answer.ok) {
          fail(S.fest.teamEdit.baseRosterFailed());
          return;
        }
        error.hidden = true;
        for (const p of (answer.data as {players: TeamPlayer[]}).players || []) push(p);
        drawList();
      });
    });
    baseRow.appendChild(base);
  };
  drawBaseButton();

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
    const body = {
      rating_id: isNew && ratingTeam ? ratingTeam.rating_id : 0,
      name: nameInput.value, city: cityInput.value,
      players: draft.map((p) => ({rating_id: p.rating_id, first_name: p.first_name, last_name: p.last_name})),
    };
    const sent = isNew ? request(`${api}/teams`, "POST", body) : request(`${api}/teams/${team.id}`, "PUT", body);
    void sent.then((answer) => {
      busy(false);
      if (answer.ok) done();
      else fail(answer.message);
    });
  });

  form.append(title, hint, ratingBlock, nameField, cityField, list, addRow, baseRow, error);
  if (deleteRow) form.appendChild(deleteRow);
  form.appendChild(actions);
  dialog.appendChild(form);
  dialog.addEventListener("close", () => {
    suggest.close();
    teamSuggest?.close();
    dialog.remove();
  });
  document.body.appendChild(dialog);
  dialog.showModal();
  (isNew ? (ratingBlock.querySelector("input") || nameInput) : input).focus();
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

interface SheetPlan {
  added_teams: string[];
  renamed: Array<{from: string; to: string}>;
  players: Array<{team: string; added: string[]; removed: string[]}>;
}

async function sendSheet(api: string, file: File, preview: boolean): Promise<{ok: boolean; plan: SheetPlan | null; message: string}> {
  const body = new FormData();
  body.append("file", file);
  try {
    const response = await fetch(`${api}/teams/xlsx${preview ? "?preview=1" : ""}`, {method: "POST", body});
    const text = (await response.text()).trim();
    if (!response.ok) return {ok: false, plan: null, message: text || S.fest.rosterSheet.failed()};
    return {ok: true, plan: (JSON.parse(text) as {plan: SheetPlan}).plan, message: ""};
  } catch {
    return {ok: false, plan: null, message: S.fest.rosterSheet.failed()};
  }
}

// openSheetDialog shows what a roster sheet changes, and loads it on confirm.
function openSheetDialog(api: string, file: File, plan: SheetPlan): void {
  const dialog = document.createElement("dialog");
  dialog.className = "modal-dialog roster-dialog";
  const form = document.createElement("form");
  form.className = "u-col u-gap-md";
  const title = document.createElement("h2");
  title.textContent = S.fest.rosterSheet.title();
  const lines = document.createElement("div");
  lines.className = "u-col u-gap-sm";
  const muted = (text: string) => {
    const line = document.createElement("p");
    line.className = "hint";
    line.textContent = text;
    return line;
  };
  const empty = !plan.added_teams.length && !plan.renamed.length && !plan.players.length;
  if (empty) lines.appendChild(muted(S.fest.rosterSheet.nothing()));
  if (plan.added_teams.length) lines.appendChild(muted(S.fest.rosterSheet.addedTeams(plan.added_teams.join(", "))));
  for (const r of plan.renamed) lines.appendChild(muted(S.fest.rosterSheet.renamed(r.from, r.to)));
  for (const team of plan.players) {
    const block = document.createElement("div");
    const name = document.createElement("strong");
    name.textContent = team.team;
    block.appendChild(name);
    if (team.added.length) block.appendChild(muted(S.fest.rosterSheet.playersAdded(team.added.join(", "))));
    if (team.removed.length) block.appendChild(muted(S.fest.rosterSheet.playersRemoved(team.removed.join(", "))));
    lines.appendChild(block);
  }
  const error = document.createElement("p");
  error.className = "hint hint-danger";
  error.hidden = true;
  const actions = document.createElement("div");
  actions.className = "modal-actions";
  const cancel = document.createElement("button");
  cancel.type = "button";
  cancel.className = "btn";
  cancel.textContent = S.fest.rosterSheet.cancel();
  cancel.addEventListener("click", () => dialog.close());
  actions.appendChild(cancel);
  if (!empty) {
    const confirm = document.createElement("button");
    confirm.type = "submit";
    confirm.className = "btn";
    confirm.textContent = S.fest.rosterSheet.confirm();
    actions.appendChild(confirm);
  }
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    for (const button of form.querySelectorAll<HTMLButtonElement>("button")) button.disabled = true;
    void sendSheet(api, file, false).then((answer) => {
      if (answer.ok) {
        dialog.close();
        window.location.reload();
        return;
      }
      for (const button of form.querySelectorAll<HTMLButtonElement>("button")) button.disabled = false;
      error.textContent = answer.message;
      error.hidden = false;
    });
  });
  form.append(title, muted(S.fest.rosterSheet.hint()), lines, error, actions);
  dialog.appendChild(form);
  dialog.addEventListener("close", () => dialog.remove());
  document.body.appendChild(dialog);
  dialog.showModal();
}

// The load-from-xlsx button: pick a file, see what it changes, confirm.
(() => {
  const button = document.querySelector<HTMLElement>("[data-team-xlsx]");
  const api = document.querySelector<HTMLElement>("[data-team-add]")?.dataset.teamAdd || "";
  if (!button || !api) return;
  const picker = document.createElement("input");
  picker.type = "file";
  picker.accept = ".xlsx,application/vnd.openxmlformats-officedocument.spreadsheetml.sheet";
  picker.hidden = true;
  document.body.appendChild(picker);
  button.addEventListener("click", () => picker.click());
  picker.addEventListener("change", () => {
    const file = picker.files?.[0];
    picker.value = "";
    if (!file) return;
    void sendSheet(api, file, true).then((answer) => {
      if (answer.ok && answer.plan) openSheetDialog(api, file, answer.plan);
      else window.alert(answer.message);
    });
  });
})();
