// The roster dialog of a game's roster tab (CONTEXT.md, Game roster): the host
// changes one team's roster for this game only. It lists the players, each with
// a cross, except the ones who already have something entered in this game;
// adds a fest player (suggested as the host types) or anybody typed by name;
// and gives the team back its fest roster. The server holds the same rules and
// has the last word.

import {autocomplete} from "../../../../dopeuikit/assets/ts/suggest.js";
import type {Choice} from "../../../../dopeuikit/assets/ts/suggest.js";
import {icon} from "./icons_gen.js";
import type {FestPlayerChoice, RosterPlayer, RosterTeam} from "./fest-roster.js";
import S from "./i18nstrings.js";

interface DraftPlayer {
  name: string;
  locked: boolean;
}

// openGameRosterDialog opens the dialog for one team; saved runs after a save
// or a reset went through, to draw the tab again.
export function openGameRosterDialog(team: RosterTeam, choices: FestPlayerChoice[], apiBase: string, saved: () => void): void {
  const participantID = Number(team.participantID);
  if (!(participantID > 0)) return;
  const draft: DraftPlayer[] = (team.players || []).map((p) => {
    const info: RosterPlayer = typeof p === "string" ? {name: p} : p;
    return {name: info.name || "", locked: Boolean(info.locked)};
  }).filter((p) => p.name);

  const dialog = document.createElement("dialog");
  dialog.className = "modal-dialog roster-dialog";
  const form = document.createElement("form");
  form.className = "u-col u-gap-md";

  const title = document.createElement("h2");
  title.textContent = S.fest.rosterEdit.title(team.name || "");
  const hint = document.createElement("p");
  hint.className = "hint";
  hint.textContent = S.fest.rosterEdit.hint();

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

  // The add row: a field that suggests the fest's players, and its button.
  const input = document.createElement("input");
  input.type = "text";
  input.className = "input";
  input.autocomplete = "off";
  input.spellcheck = false;
  input.placeholder = S.fest.rosterEdit.addPlaceholder();
  const anchor = document.createElement("span");
  anchor.className = "suggest-anchor u-col u-grow";
  anchor.appendChild(input);
  const suggestions: Choice[] = choices.map((c) => ({value: c.Name, label: c.Name, hint: c.Team}));
  const suggest = autocomplete(input, (q) => {
    const needle = q.trim().toLowerCase();
    if (!needle) return [];
    const taken = new Set(draft.map((p) => p.name.toLowerCase()));
    return suggestions
      .filter((c) => !taken.has(c.value.toLowerCase()))
      .filter((c) => c.label.toLowerCase().split(/\s+/).some((word) => word.startsWith(needle)) || c.label.toLowerCase().startsWith(needle))
      .slice(0, 12);
  }, (choice) => {
    input.value = choice.value;
    add();
  });
  const addButton = document.createElement("button");
  addButton.type = "button";
  addButton.className = "btn";
  addButton.textContent = S.fest.rosterEdit.add();
  const add = () => {
    const name = input.value.replace(/\s*\(.*$/, "").trim();
    if (!name) return;
    if (draft.some((p) => p.name.toLowerCase() === name.toLowerCase())) {
      fail(S.fest.rosterEdit.playerTwice(name));
      return;
    }
    error.hidden = true;
    draft.push({name, locked: false});
    input.value = "";
    suggest.close();
    drawList();
    input.focus();
  };
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

  const actions = document.createElement("div");
  actions.className = "modal-actions";
  const busy = (on: boolean) => {
    for (const button of form.querySelectorAll<HTMLButtonElement>(".modal-actions button, [data-roster-reset]")) button.disabled = on;
  };
  // Giving the fest roster back is not a peer of save and cancel, and on a
  // phone the three do not fit one row: it gets a row of its own, above them.
  let resetRow: HTMLElement | null = null;
  if (team.hand) {
    const reset = document.createElement("button");
    reset.type = "button";
    reset.className = "btn btn-ghost";
    reset.dataset.rosterReset = "";
    reset.textContent = S.fest.rosterEdit.reset();
    reset.addEventListener("click", () => {
      if (!window.confirm(S.fest.rosterEdit.resetConfirm(team.name || ""))) return;
      busy(true);
      void send("DELETE", null).then((message) => {
        busy(false);
        if (message) fail(message);
        else done();
      });
    });
    resetRow = document.createElement("div");
    resetRow.className = "u-row";
    resetRow.appendChild(reset);
  }
  const cancel = document.createElement("button");
  cancel.type = "button";
  cancel.className = "btn";
  cancel.textContent = S.fest.rosterEdit.cancel();
  cancel.addEventListener("click", () => dialog.close());
  const save = document.createElement("button");
  save.type = "submit";
  save.className = "btn";
  save.textContent = S.fest.rosterEdit.save();
  actions.append(cancel, save);

  const send = async (method: "PUT" | "DELETE", players: string[] | null): Promise<string> => {
    try {
      const response = await fetch(`${apiBase}/rosters/${participantID}`, {
        method,
        headers: players ? {"Content-Type": "application/json"} : undefined,
        body: players ? JSON.stringify({players}) : undefined,
      });
      if (response.ok) return "";
      const text = (await response.text()).trim();
      return text || S.fest.rosterEdit.failed();
    } catch {
      return S.fest.rosterEdit.failed();
    }
  };
  const done = () => {
    dialog.close();
    saved();
  };

  form.addEventListener("submit", (event) => {
    event.preventDefault();
    error.hidden = true;
    // A name left in the field counts as added: the host typed it to have it.
    if (input.value.trim()) add();
    if (!error.hidden) return;
    busy(true);
    void send("PUT", draft.map((p) => p.name)).then((message) => {
      busy(false);
      if (message) fail(message);
      else done();
    });
  });

  form.append(title, hint, list);
  // A lock's title is a tooltip, which a phone never shows: say once, in
  // words, what the lock means.
  if (draft.some((p) => p.locked)) {
    const why = document.createElement("p");
    why.className = "hint";
    why.append(icon("lock"), " ", S.fest.rosterEdit.locked());
    form.appendChild(why);
  }
  form.append(addRow, error);
  if (resetRow) form.appendChild(resetRow);
  form.appendChild(actions);
  dialog.appendChild(form);
  dialog.addEventListener("close", () => {
    suggest.close();
    dialog.remove();
  });
  document.body.appendChild(dialog);
  dialog.showModal();
  input.focus();
}

// playerRow is one player of the draft: the name, and a cross to take them off
// — or, for a player with results in this game, a lock that says why not.
function playerRow(player: DraftPlayer, remove: () => void): HTMLElement {
  const row = document.createElement("div");
  row.className = "u-row u-gap-sm u-align-center u-justify-between";
  const name = document.createElement("span");
  name.textContent = player.name;
  row.appendChild(name);
  if (player.locked) {
    const lock = document.createElement("span");
    lock.className = "hint roster-dialog-mark";
    lock.title = S.fest.rosterEdit.locked();
    lock.setAttribute("aria-label", S.fest.rosterEdit.locked());
    lock.appendChild(icon("lock"));
    row.appendChild(lock);
    return row;
  }
  const button = document.createElement("button");
  button.type = "button";
  button.className = "action-icon";
  const label = S.fest.rosterEdit.remove(player.name);
  button.title = label;
  button.setAttribute("aria-label", label);
  button.appendChild(icon("x"));
  button.addEventListener("click", remove);
  row.appendChild(button);
  return row;
}
