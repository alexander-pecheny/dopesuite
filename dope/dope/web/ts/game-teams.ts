// The entrants tab of a flat Game (OD, the minefield, KSI, Multi): who
// plays it. By default the fest's whole team list, which the rating import and
// the fest teams page keep current; or a list of the Game's own, copied from the
// teams it seats at that moment and changed from then on only here. The
// server decides every rule (a team the Game holds answers for stays) and
// answers the tab afresh after each write (roster.GameTeamsView). The Game's
// document reaches the page on its own scope, as after any roster change.

import {td, th} from "./cells.js";
import {resultsPins, resultsTeamCell} from "./standings.js";
import {sheetHead} from "./sheet-pins.js";
import {iconed} from "./icons_gen.js";
import {actionButton, emptyNote, errorText, hint} from "./entrants.js";
import S from "./i18nstrings.js";

const TEAM_PINS = resultsPins();

interface GameTeam {
  id: number;
  number?: number;
  name: string;
  city?: string;
  entered?: boolean;
}

interface GameTeamsView {
  own: boolean;
  teams: GameTeam[];
  candidates: GameTeam[];
}

export interface GameTeamsTab {
  element(): HTMLElement;
  // Fetch the list again, if the tab has been drawn: a roster change.
  refresh(): void;
}

export function createGameTeamsTab(apiBase: string): GameTeamsTab {
  const root = document.createElement("section");
  root.className = "results-wrapper seed-import-panel u-col u-gap-md";
  let view: GameTeamsView | null = null;
  let loaded = false;
  let busy = false;
  let notice = "";

  async function load(): Promise<void> {
    loaded = true;
    try {
      const response = await fetch(`${apiBase}/teams`);
      if (!response.ok) throw new Error((await response.text()).trim());
      view = await response.json() as GameTeamsView;
      notice = "";
    } catch (error) {
      notice = errorText(error);
    }
    render();
  }

  // send makes one change and adopts the tab the server answers with.
  async function send(path: string, method: string, body?: unknown): Promise<void> {
    if (busy) return;
    busy = true;
    notice = "";
    render();
    try {
      const response = await fetch(`${apiBase}/teams${path}`, {
        method,
        headers: {"Content-Type": "application/json"},
        body: body === undefined ? undefined : JSON.stringify(body),
      });
      if (!response.ok) throw new Error((await response.text()).trim());
      view = await response.json() as GameTeamsView;
    } catch (error) {
      notice = S.entrants.state.failed(errorText(error));
    }
    busy = false;
    render();
  }

  function render(): void {
    const nodes: HTMLElement[] = [];
    if (view) {
      nodes.push(modeChoice(view));
      if (notice) nodes.push(hint(notice, true));
      if (view.teams.length) {
        nodes.push(hint(S.entrants.own.summary(view.teams.length)));
        nodes.push(table(view));
      } else {
        nodes.push(emptyNote(S.entrants.own.empty()));
      }
      if (view.own) nodes.push(addForm(view));
    } else if (notice) {
      nodes.push(hint(notice, true));
    }
    root.replaceChildren(...nodes);
  }

  // modeChoice is the two ways the Game takes its teams, each with what it
  // means under it.
  function modeChoice(current: GameTeamsView): HTMLElement {
    const group = document.createElement("div");
    group.className = "u-col u-gap-sm";
    group.setAttribute("role", "radiogroup");
    group.setAttribute("aria-label", S.entrants.tab());
    const choice = (own: boolean, label: string, note: string): HTMLElement => {
      const row = document.createElement("label");
      row.className = "u-row u-gap-xs u-align-center";
      const radio = document.createElement("input");
      radio.type = "radio";
      radio.name = "game-teams-mode";
      radio.checked = current.own === own;
      radio.disabled = busy;
      radio.addEventListener("change", () => {
        if (!own && !window.confirm(S.entrants.own.followConfirm())) {
          render();
          return;
        }
        void send("", "PUT", {own});
      });
      const text = document.createElement("strong");
      text.textContent = label;
      row.append(radio, text);
      const wrap = document.createElement("div");
      wrap.className = "u-col u-gap-xs";
      wrap.append(row, hint(note));
      return wrap;
    };
    group.append(
      choice(false, S.entrants.own.follow(), S.entrants.own.followHint()),
      choice(true, S.entrants.own.own(), S.entrants.own.ownHint()),
    );
    return group;
  }

  function table(current: GameTeamsView): HTMLElement {
    const wrap = document.createElement("div");
    wrap.className = "table-scroll";
    const node = document.createElement("table");
    node.className = "results-table seed-import-table";
    const head = document.createElement("tr");
    head.append(
      TEAM_PINS.mark(th(S.entrants.own.number(), "results-place-head seed-number-head"), "place"),
      TEAM_PINS.mark(th(S.entrants.own.name(), "results-team-head seed-team-head"), "name"),
    );
    // The actions column stays without a list of the Game's own too: it
    // takes the table's spare width, which the number would otherwise get.
    head.appendChild(th("", "entrant-actions-head"));
    node.appendChild(sheetHead([{row: head}]));
    const body = document.createElement("tbody");
    current.teams.forEach((team, index) => body.appendChild(teamRow(current, team, index)));
    node.appendChild(body);
    wrap.appendChild(node);
    return wrap;
  }

  // teamRow is one team of the list; with a list of the Game's own, a team
  // it holds no answers for can be taken off it.
  function teamRow(current: GameTeamsView, team: GameTeam, index: number): HTMLTableRowElement {
    const tr = document.createElement("tr");
    const classes = ["results-row"];
    if (index === 0) classes.push("results-group-first");
    if (index === current.teams.length - 1) classes.push("results-group-last");
    tr.className = classes.join(" ");
    tr.append(
      TEAM_PINS.mark(td(team.number ? String(team.number) : "", "results-place seed-number-cell"), "place"),
      TEAM_PINS.mark(resultsTeamCell(team.name, {city: team.city}), "name"),
    );
    if (!current.own) {
      tr.appendChild(td("", "entrant-actions-cell"));
      return tr;
    }
    const remove = actionButton("trash-2", team.entered ? S.entrants.own.enteredNote() : S.entrants.own.remove(),
      busy || Boolean(team.entered), () => {
        if (!window.confirm(S.entrants.own.removeConfirm(team.name))) return;
        void send(`/${team.id}`, "DELETE");
      });
    tr.appendChild(td(remove, "entrant-actions-cell"));
    return tr;
  }

  // addForm offers the fest's teams that are not on the list.
  function addForm(current: GameTeamsView): HTMLElement {
    if (!current.candidates.length) return hint(S.entrants.own.addNone());
    const form = document.createElement("form");
    form.className = "u-row u-wrap u-gap-sm u-align-center";
    const select = document.createElement("select");
    select.className = "input";
    select.setAttribute("aria-label", S.entrants.own.addPick());
    for (const team of current.candidates) {
      const option = document.createElement("option");
      option.value = String(team.id);
      option.textContent = team.number ? `${team.number}. ${team.name}` : team.name;
      select.appendChild(option);
    }
    const submit = document.createElement("button");
    submit.type = "submit";
    submit.className = "btn";
    submit.disabled = busy;
    submit.append(...iconed("plus", S.entrants.own.addSubmit()));
    form.append(select, submit);
    form.addEventListener("submit", (event) => {
      event.preventDefault();
      void send("", "POST", {team: Number(select.value)});
    });
    return form;
  }

  return {
    element(): HTMLElement {
      if (!loaded) void load();
      return root;
    },
    refresh(): void {
      if (loaded && !busy) void load();
    },
  };
}
