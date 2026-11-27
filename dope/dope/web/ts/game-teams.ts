// The entrants tab of a flat Game (OD, the minefield, KSI, Multi): who
// plays it. Either the fest's teams, which the rating import and the fest
// teams page keep current, or the Game's separate list: a copy of those teams
// made the first time, then the Game's alone. The host changes a team's
// number right in the table and the rest in the fest roster's team editor
// (team-editor.ts: the rating site's teams and people, and anybody typed by
// name), adds a team the same way, clears the list or loads it from a roster
// sheet, and every change is saved at once. The Game keeps the list while it plays the fest's
// teams, so the host may switch back and forth. The server decides every rule
// (a team the Game holds answers for stays) and answers the tab afresh after
// each write (roster.GameTeamsView).

import {td, th} from "./cells.js";
import {resultsPins, resultsTeamCell} from "./standings.js";
import {sheetHead} from "./sheet-pins.js";
import {iconed} from "./icons_gen.js";
import {actionButton, emptyNote, errorText, hint} from "./entrants.js";
import {openTeamDialog, request} from "./team-editor.js";
import type {Answer, TeamDetail, TeamPlayer} from "./team-editor.js";
import S from "./i18nstrings.js";

const TEAM_PINS = resultsPins();
// How long the tab waits after an edit before it saves, so a run of edits
// goes as one write.
const SAVE_DELAY_MS = 300;

interface GamePlayer {
  name: string;
  ratingID?: number;
}

interface GameTeam {
  number: number;
  name: string;
  city?: string;
  ratingID?: number;
  flags?: string[];
  players?: GamePlayer[];
  // prev is the number the team had when the list was last saved, which
  // carries its answers to a new one.
  prev?: number;
  entered?: boolean;
}

interface GameTeamsView {
  own: boolean;
  teams: GameTeam[];
}

export interface GameTeamsTab {
  element(): HTMLElement;
  // Fetch the list again, if the tab has been drawn: a roster change.
  refresh(): void;
}

// teamPlayer is a person of the list as the team editor holds people: the
// first word the given name, the rest the surname.
function teamPlayer(player: GamePlayer): TeamPlayer {
  const [first = "", ...rest] = player.name.trim().split(/\s+/);
  return {rating_id: player.ratingID || 0, first_name: first, last_name: rest.join(" ")};
}

function nextNumber(teams: GameTeam[]): number {
  return teams.reduce((top, team) => Math.max(top, team.number || 0), 0) + 1;
}

export function createGameTeamsTab(apiBase: string): GameTeamsTab {
  const root = document.createElement("section");
  root.className = "results-wrapper seed-import-panel u-col u-gap-md";
  let view: GameTeamsView | null = null;
  // draft is the separate list as the table shows it, saved after each edit.
  let draft: GameTeam[] = [];
  let loaded = false;
  let busy = false;
  let notice = "";
  let saveTimer = 0;
  let saveAgain = false;

  function adopt(next: GameTeamsView): void {
    view = next;
    draft = next.teams.map((team) => ({...team, prev: team.number}));
  }

  async function call(path: string, init: RequestInit): Promise<GameTeamsView> {
    const response = await fetch(`${apiBase}/teams${path}`, init);
    if (!response.ok) throw new Error((await response.text()).trim());
    return await response.json() as GameTeamsView;
  }

  async function load(): Promise<void> {
    loaded = true;
    try {
      adopt(await call("", {}));
      notice = "";
    } catch (error) {
      notice = errorText(error);
    }
    render();
  }

  // send makes one change, adopts the tab the server answers with and draws
  // it again.
  async function send(path: string, init: RequestInit): Promise<void> {
    busy = true;
    notice = "";
    try {
      adopt(await call(path, init));
    } catch (error) {
      notice = S.entrants.state.failed(errorText(error));
    }
    busy = false;
    render();
  }

  function json(method: string, body: unknown): RequestInit {
    return {method, headers: {"Content-Type": "application/json"}, body: JSON.stringify(body)};
  }

  // save sends the draft. The table already shows it, so a save that goes
  // through draws nothing again; a refused one says why above the table.
  async function save(): Promise<void> {
    if (busy) {
      saveAgain = true;
      return;
    }
    busy = true;
    try {
      const next = await call("/list", json("PUT", {teams: draft}));
      view = next;
      draft.forEach((team) => { team.prev = team.number; });
      notice = "";
    } catch (error) {
      notice = S.entrants.state.failed(errorText(error));
    }
    busy = false;
    drawNotice();
    if (saveAgain) {
      saveAgain = false;
      void save();
    }
  }

  function saveSoon(): void {
    window.clearTimeout(saveTimer);
    saveTimer = window.setTimeout(() => void save(), SAVE_DELAY_MS);
  }

  // saveNow saves a change that adds or takes away a row, then draws the
  // table again.
  async function saveNow(): Promise<void> {
    window.clearTimeout(saveTimer);
    await send("/list", json("PUT", {teams: draft}));
  }

  const noticeSlot = document.createElement("div");

  function drawNotice(): void {
    noticeSlot.replaceChildren(...(notice ? [hint(notice, true)] : []));
  }

  function render(): void {
    drawNotice();
    const nodes: HTMLElement[] = [];
    if (view) {
      nodes.push(modeChoice(view), noticeSlot);
      if (view.own) nodes.push(listButtons());
      if (view.teams.length) nodes.push(hint(S.entrants.own.summary(view.teams.length)));
      if (view.own) nodes.push(ownTable());
      else if (view.teams.length) nodes.push(festTable(view.teams));
      else nodes.push(emptyNote(S.entrants.own.empty()));
    } else {
      nodes.push(noticeSlot);
    }
    root.replaceChildren(...nodes);
  }

  // modeChoice is the two ways the Game takes its teams, on one line.
  function modeChoice(current: GameTeamsView): HTMLElement {
    const group = document.createElement("div");
    group.className = "u-row u-wrap u-gap-md u-align-center";
    group.setAttribute("role", "radiogroup");
    group.setAttribute("aria-label", S.entrants.tab());
    const choice = (own: boolean, label: string): HTMLElement => {
      const row = document.createElement("label");
      row.className = "u-row u-gap-xs u-align-center";
      const radio = document.createElement("input");
      radio.type = "radio";
      radio.name = "game-teams-mode";
      radio.checked = current.own === own;
      radio.disabled = busy;
      radio.addEventListener("change", () => {
        const entered = current.teams.some((team) => team.entered);
        if (!own && entered && !window.confirm(S.entrants.own.followConfirm())) {
          render();
          return;
        }
        window.clearTimeout(saveTimer);
        void send("", json("PUT", {own}));
      });
      const text = document.createElement("strong");
      text.textContent = label;
      row.append(radio, text);
      return row;
    };
    group.append(choice(false, S.entrants.own.follow()), choice(true, S.entrants.own.own()));
    return group;
  }

  // listButtons clear the list, load it from a roster sheet, or give the
  // sheet to fill in.
  function listButtons(): HTMLElement {
    const row = document.createElement("div");
    row.className = "u-row u-wrap u-gap-sm u-align-center";
    const clear = document.createElement("button");
    clear.type = "button";
    clear.className = "btn";
    clear.disabled = busy || !draft.length;
    clear.append(...iconed("trash-2", S.entrants.own.clear()));
    clear.addEventListener("click", () => {
      if (!window.confirm(S.entrants.own.clearConfirm())) return;
      // A team the Game holds answers for stays; the server would refuse
      // the whole list without it.
      draft = draft.filter((team) => team.entered);
      void saveNow();
    });
    const file = document.createElement("input");
    file.type = "file";
    file.accept = ".xlsx";
    file.hidden = true;
    file.addEventListener("change", () => {
      const picked = file.files?.[0];
      if (!picked) return;
      const form = new FormData();
      form.append("file", picked);
      window.clearTimeout(saveTimer);
      void send("/xlsx", {method: "POST", body: form});
    });
    const upload = document.createElement("button");
    upload.type = "button";
    upload.className = "btn";
    upload.disabled = busy;
    upload.append(...iconed("file-up", S.entrants.own.xlsxImport()));
    upload.addEventListener("click", () => file.click());
    const sample = document.createElement("a");
    sample.className = "btn";
    sample.href = `${apiBase}/teams/sample.xlsx`;
    sample.download = "teams.xlsx";
    sample.append(...iconed("file-down", S.entrants.own.xlsxSample()));
    const add = document.createElement("button");
    add.type = "button";
    add.className = "btn";
    add.disabled = busy;
    add.append(...iconed("plus", S.entrants.own.addTeam()));
    add.addEventListener("click", () => void editTeam(null));
    row.append(add, clear, upload, file, sample);
    return row;
  }

  function headRow(columns: Array<[string, string]>): HTMLTableRowElement {
    const head = document.createElement("tr");
    for (const [label, className] of columns) head.appendChild(th(label, className));
    return head;
  }

  // festTable is the fest's teams the Game plays, to read.
  function festTable(teams: GameTeam[]): HTMLElement {
    const node = document.createElement("table");
    node.className = "results-table seed-import-table game-teams-table";
    const head = document.createElement("tr");
    head.append(
      TEAM_PINS.mark(th(S.entrants.own.number(), "results-place-head seed-number-head"), "place"),
      TEAM_PINS.mark(th(S.entrants.own.name(), "results-team-head seed-team-head"), "name"),
      th(S.entrants.own.flags(), "game-teams-flags"),
      th(S.entrants.own.players(), "game-teams-players"),
    );
    node.appendChild(sheetHead([{row: head}]));
    const body = document.createElement("tbody");
    teams.forEach((team, index) => {
      const tr = rowShell(index, teams.length);
      tr.append(
        TEAM_PINS.mark(td(team.number ? String(team.number) : "", "results-place seed-number-cell"), "place"),
        TEAM_PINS.mark(resultsTeamCell(team.name, {city: team.city}), "name"),
        td((team.flags || []).join(", "), "game-teams-flags"),
        td((team.players || []).map((p) => p.name).join(", "), "game-teams-players"),
      );
      body.appendChild(tr);
    });
    node.appendChild(body);
    return scroll(node);
  }

  function scroll(table: HTMLElement): HTMLElement {
    const wrap = document.createElement("div");
    wrap.className = "table-scroll";
    wrap.appendChild(table);
    return wrap;
  }

  function rowShell(index: number, count: number): HTMLTableRowElement {
    const tr = document.createElement("tr");
    const classes = ["results-row"];
    if (index === 0) classes.push("results-group-first");
    if (index === count - 1) classes.push("results-group-last");
    tr.className = classes.join(" ");
    return tr;
  }

  // ownTable is the separate list: the number a field of its own, the rest
  // read here and changed in the fest roster's team editor.
  function ownTable(): HTMLElement {
    const node = document.createElement("table");
    node.className = "results-table seed-import-table game-teams-table";
    node.appendChild(sheetHead([{row: headRow([
      [S.entrants.own.number(), "results-place-head seed-number-head"],
      [S.entrants.own.name(), "results-team-head seed-team-head"],
      [S.entrants.own.flags(), "game-teams-flags"],
      [S.entrants.own.players(), "game-teams-players"],
      ["", "entrant-actions-head"],
    ])}]));
    const body = document.createElement("tbody");
    draft.forEach((team, index) => body.appendChild(ownRow(team, index)));
    node.appendChild(body);
    return scroll(node);
  }

  function ownRow(team: GameTeam, index: number): HTMLTableRowElement {
    const tr = rowShell(index, draft.length);
    const number = document.createElement("input");
    number.type = "text";
    number.inputMode = "numeric";
    number.className = "seed-number-input";
    number.value = String(team.number || "");
    number.setAttribute("aria-label", S.entrants.own.number());
    number.addEventListener("change", () => {
      team.number = Number(number.value.trim()) || 0;
      saveSoon();
    });
    const actions = document.createElement("div");
    actions.className = "u-row u-gap-xs";
    actions.append(
      actionButton("pencil", S.entrants.own.edit(team.name), busy, () => void editTeam(team)),
      actionButton("trash-2", team.entered ? S.entrants.own.enteredNote() : S.entrants.own.remove(), busy || Boolean(team.entered), () => {
        draft.splice(draft.indexOf(team), 1);
        void saveNow();
      }),
    );
    tr.append(
      td(number, "results-place seed-number-cell"),
      resultsTeamCell(team.name, {city: team.city}),
      td((team.flags || []).join(", "), "game-teams-flags"),
      td((team.players || []).map((p) => p.name).join(", "), "game-teams-players"),
      td(actions, "entrant-actions-cell"),
    );
    return tr;
  }

  // editorBase is what the team editor offers every team: the fest's people
  // to suggest and its Divisions, fetched once.
  let editorBase: Promise<TeamDetail | null> | null = null;
  function teamDetail(team: GameTeam | null): Promise<TeamDetail | null> {
    editorBase ??= request(`${apiBase}/teams/editor`, "GET").then((answer) => (answer.ok ? answer.data as TeamDetail : null));
    return editorBase.then((base) => base && {
      ...base,
      // A row of the list is no fest team: local keeps the editor from
      // taking it for a new one, and hand lets the host pick its rating team.
      id: 0, local: team !== null, hand: true, multifest_venue_id: 0, multifest_venues: [],
      rating_id: team?.ratingID || 0, name: team?.name || "", city: team?.city || "", flags: team?.flags || [],
      players: (team?.players || []).map(teamPlayer),
    });
  }

  // editTeam opens the fest roster's team editor on a row of the list, or on
  // a new team (null), and saves the list with what it gives back.
  async function editTeam(team: GameTeam | null): Promise<void> {
    const detail = await teamDetail(team);
    if (!detail) return;
    // The row is found by its place: a refused save puts copies back.
    const at = team ? draft.indexOf(team) : -1;
    // A save the server refuses leaves the list as it was, and the editor
    // open with the reason.
    const keep = async (change: () => void): Promise<Answer> => {
      const before = draft.map((t) => ({...t}));
      change();
      const answer = await persist();
      if (!answer.ok) draft = before;
      render();
      return answer;
    };
    openTeamDialog(apiBase, detail, 0, {
      texts: {deleteConfirm: (name) => S.entrants.own.removeConfirm(name)},
      save: (body) => keep(() => {
        const row: GameTeam = at >= 0 ? draft[at] : {number: nextNumber(draft), name: ""};
        row.name = body.name.trim();
        row.city = body.city.trim();
        row.flags = body.flags;
        row.players = body.players.map((p) => ({name: `${p.first_name} ${p.last_name}`.trim(), ratingID: p.rating_id || undefined}));
        row.ratingID = (team ? body.rating_team : body.rating_id) || undefined;
        if (at < 0) draft.push(row);
      }),
      remove: () => keep(() => { draft.splice(at, 1); }),
    });
  }

  // persist saves the list as the editor's save does: the answer says
  // whether it went through, and why not.
  async function persist(): Promise<Answer> {
    window.clearTimeout(saveTimer);
    try {
      adopt(await call("/list", json("PUT", {teams: draft})));
      notice = "";
      return {ok: true, message: ""};
    } catch (error) {
      return {ok: false, message: errorText(error)};
    }
  }

  return {
    element(): HTMLElement {
      if (!loaded) void load();
      return root;
    },
    refresh(): void {
      // The fest's teams change under a Game that plays them; a separate
      // list changes only here, and a refetch would drop what is being typed.
      if (loaded && !busy && !view?.own) void load();
    },
  };
}
