// A tethered game's overrides on its entrants tab (ADR-0038): each one a
// line of the list under the table, with a button to take it back, and the
// dialog that adds one. The dialog names a team from the game's list,
// adds a team in the fest roster's team editor, and finds a person the way
// that editor does (playerPicker). The server applies the overrides and says
// which do not apply any more; the tab only shows it.

import {actionButton, errorText, hint} from "./entrants.js";
import {iconed} from "./icons_gen.js";
import {openTeamDialog, playerPicker, request} from "./team-editor.js";
import type {Answer, PlayerPicker, TeamDetail, TeamPlayer, TeamSave} from "./team-editor.js";
import S from "./i18nstrings.js";

export interface OverridePlayer {
  name: string;
  ratingID?: number;
}

export interface OverrideTeam {
  key?: string;
  number: number;
  name: string;
  city?: string;
  ratingID?: number;
  flags?: string[];
  players?: OverridePlayer[];
}

export interface Override {
  id?: number;
  kind: string;
  team?: string;
  label?: string;
  name?: string;
  city?: string;
  flags?: string[];
  number?: number;
  player?: OverridePlayer;
  to?: string;
  toLabel?: string;
  add?: OverrideTeam;
  applied?: boolean;
}

// The kinds the dialog offers, in its order; a number is typed in the table.
// A bout game takes only who a team is: its entrant list says who sits where.
const DIALOG_KINDS = ["rename", "city", "flags", "add_team", "remove_team", "add_player", "remove_player", "move_player"] as const;
type DialogKind = typeof DIALOG_KINDS[number];
const BOUT_KINDS: readonly DialogKind[] = ["rename", "city", "add_player", "remove_player", "move_player"];

function kindLabel(kind: DialogKind): string {
  const labels: Record<DialogKind, string> = {
    rename: S.entrants.overrides.kindRename(),
    city: S.entrants.overrides.kindCity(),
    flags: S.entrants.overrides.kindFlags(),
    add_team: S.entrants.overrides.kindAddTeam(),
    remove_team: S.entrants.overrides.kindRemoveTeam(),
    add_player: S.entrants.overrides.kindAddPlayer(),
    remove_player: S.entrants.overrides.kindRemovePlayer(),
    move_player: S.entrants.overrides.kindMovePlayer(),
  };
  return labels[kind];
}

// overrideText is an override as the list says it.
export function overrideText(o: Override): string {
  const team = o.label || "";
  const player = o.player?.name || "";
  switch (o.kind) {
  case "rename": return S.entrants.overrides.textRename(team, o.name || "");
  case "city": return S.entrants.overrides.textCity(team, o.city || "");
  case "flags": return S.entrants.overrides.textFlags(team, (o.flags || []).join(", "));
  case "number": return S.entrants.overrides.textNumber(team, String(o.number || ""));
  case "add_team": return S.entrants.overrides.textAddTeam(team);
  case "remove_team": return S.entrants.overrides.textRemoveTeam(team);
  case "add_player": return S.entrants.overrides.textAddPlayer(player, team);
  case "remove_player": return S.entrants.overrides.textRemovePlayer(player, team);
  case "move_player": return S.entrants.overrides.textMovePlayer(player, o.toLabel || "");
  default: return o.kind;
  }
}

// overridesList is the overrides under the table, each with its button to
// take it back, and the mark on one that does not apply any more.
export function overridesList(overrides: Override[], busy: boolean, remove: (id: number) => void): HTMLElement {
  const section = document.createElement("section");
  section.className = "u-col u-gap-sm";
  const title = document.createElement("h3");
  title.textContent = S.entrants.overrides.title();
  section.appendChild(title);
  for (const o of overrides) {
    const row = document.createElement("div");
    row.className = "u-row u-gap-sm u-align-center";
    const text = document.createElement("span");
    text.textContent = overrideText(o);
    row.appendChild(text);
    if (!o.applied) row.appendChild(hint(S.entrants.overrides.notApplied(), true));
    row.appendChild(actionButton("trash-2", S.entrants.overrides.remove(), busy, () => remove(o.id || 0)));
    section.appendChild(row);
  }
  return section;
}

export interface OverrideDialogSpec {
  // api is the game's API base; the person search runs under it.
  api: string;
  teams: OverrideTeam[];
  // kinds narrows what the dialog offers.
  kinds?: readonly DialogKind[];
  editorBase(): Promise<TeamDetail | null>;
  send(o: Override): Promise<Answer>;
}

function select(label: string, options: Array<[string, string]>): {node: HTMLElement; input: HTMLSelectElement} {
  const wrap = document.createElement("label");
  wrap.className = "field";
  const caption = document.createElement("span");
  caption.textContent = label;
  const input = document.createElement("select");
  input.className = "input";
  for (const [value, text] of options) {
    const option = document.createElement("option");
    option.value = value;
    option.textContent = text;
    input.appendChild(option);
  }
  wrap.append(caption, input);
  return {node: wrap, input};
}

function textField(label: string): {node: HTMLElement; input: HTMLInputElement} {
  const wrap = document.createElement("label");
  wrap.className = "field";
  const caption = document.createElement("span");
  caption.textContent = label;
  const input = document.createElement("input");
  input.type = "text";
  input.className = "input";
  wrap.append(caption, input);
  return {node: wrap, input};
}

function playerName(p: TeamPlayer): string {
  return `${p.first_name} ${p.last_name}`.trim();
}

// openOverrideDialog asks what to change, then the fields that change needs.
export function openOverrideDialog(spec: OverrideDialogSpec): void {
  const dialog = document.createElement("dialog");
  dialog.className = "modal-dialog roster-dialog";
  const form = document.createElement("form");
  form.className = "u-col u-gap-md";
  const title = document.createElement("h2");
  title.textContent = S.entrants.overrides.add();
  const kind = select(S.entrants.overrides.kind(), (spec.kinds || DIALOG_KINDS).map((k) => [k, kindLabel(k)]));
  const fields = document.createElement("div");
  fields.className = "u-col u-gap-md";
  const error = hint("", true);
  error.hidden = true;
  const teamOptions = spec.teams.map((t): [string, string] => [t.key || "", `${t.number}. ${t.name}`]);
  let read: (unrated: boolean) => Override | string = () => "";
  let picker: PlayerPicker | null = null;

  const draw = async (): Promise<void> => {
    picker?.close();
    picker = null;
    const k = kind.input.value as DialogKind;
    if (k === "add_team") {
      dialog.close();
      addTeam(spec);
      return;
    }
    const team = select(S.entrants.overrides.team(), teamOptions);
    const parts: HTMLElement[] = [team.node];
    if (k === "rename" || k === "city" || k === "flags") {
      const value = textField(k === "rename" ? S.entrants.overrides.name() : k === "city" ? S.entrants.overrides.city() : S.entrants.overrides.flags());
      parts.push(value.node);
      read = () => {
        const typed = value.input.value;
        if (k === "rename") return {kind: k, team: team.input.value, name: typed};
        if (k === "city") return {kind: k, team: team.input.value, city: typed};
        return {kind: k, team: team.input.value, flags: typed.split(",").map((f) => f.trim()).filter(Boolean)};
      };
    } else if (k === "remove_team") {
      read = () => ({kind: k, team: team.input.value});
    } else if (k === "remove_player") {
      const players = select(S.entrants.overrides.player(), []);
      const fill = () => {
        const chosen = spec.teams.find((t) => t.key === team.input.value);
        players.input.replaceChildren(...(chosen?.players || []).map((p) => new Option(p.name, JSON.stringify(p))));
      };
      team.input.addEventListener("change", fill);
      fill();
      parts.push(players.node);
      read = () => (players.input.value ? {kind: k, team: team.input.value, player: JSON.parse(players.input.value) as OverridePlayer} : S.entrants.overrides.player());
    } else {
      const base = await spec.editorBase();
      if (!base) return;
      picker = playerPicker(spec.api, base);
      const label = document.createElement("span");
      label.textContent = S.entrants.overrides.player();
      const pick = document.createElement("div");
      pick.className = "field";
      pick.append(label, picker.node);
      // A moved player goes to the team asked; an added one joins the team.
      if (k === "move_player") team.node.querySelector("span")!.textContent = S.entrants.overrides.to();
      parts.unshift(pick);
      const p = picker;
      read = (unrated) => {
        const person = unrated ? p.typed() : p.picked();
        if (!person) return unrated ? S.fest.teamEdit.unratedNeedsName() : S.fest.teamEdit.pickOrUnrated();
        const player = {name: playerName(person), ratingID: person.rating_id || undefined};
        return k === "move_player" ? {kind: k, to: team.input.value, player} : {kind: k, team: team.input.value, player};
      };
    }
    fields.replaceChildren(...parts);
  };
  kind.input.addEventListener("change", () => void draw());

  const submit = async (unrated: boolean): Promise<void> => {
    const o = read(unrated);
    if (typeof o === "string") {
      error.textContent = o;
      error.hidden = false;
      return;
    }
    const answer = await spec.send(o);
    if (answer.ok) dialog.close();
    else {
      error.textContent = answer.message;
      error.hidden = false;
    }
  };
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    void submit(false);
  });
  const buttons = document.createElement("div");
  buttons.className = "modal-actions";
  const unrated = document.createElement("button");
  unrated.type = "button";
  unrated.className = "btn btn-ghost";
  unrated.textContent = S.fest.teamEdit.unratedButton();
  unrated.addEventListener("click", () => void submit(true));
  const cancel = document.createElement("button");
  cancel.type = "button";
  cancel.className = "btn";
  cancel.textContent = S.fest.teamEdit.cancel();
  cancel.addEventListener("click", () => dialog.close());
  const save = document.createElement("button");
  save.type = "submit";
  save.className = "btn";
  save.textContent = S.fest.teamEdit.save();
  buttons.append(unrated, cancel, save);
  const syncUnrated = () => { unrated.hidden = !["add_player", "move_player"].includes(kind.input.value); };
  kind.input.addEventListener("change", syncUnrated);
  syncUnrated();
  form.append(title, kind.node, fields, error, buttons);
  dialog.appendChild(form);
  dialog.addEventListener("close", () => {
    picker?.close();
    dialog.remove();
  });
  document.body.appendChild(dialog);
  dialog.showModal();
  void draw();
}

// addTeam is the add_team override: a new team made in the fest roster's
// team editor, which the game adds to its source's teams.
function addTeam(spec: OverrideDialogSpec): void {
  void spec.editorBase().then((base) => {
    if (!base) return;
    const detail: TeamDetail = {...base, id: 0, hand: true, rating_id: 0, name: "", city: "", flags: [], players: [], multifest_venue_id: 0, multifest_venues: []};
    openTeamDialog(spec.api, detail, 0, {
      texts: {deleteConfirm: (name) => S.entrants.own.removeConfirm(name)},
      remove: () => Promise.resolve({ok: true, message: ""}),
      save: (body: TeamSave) => spec.send({
        kind: "add_team",
        add: {
          number: 0, name: body.name, city: body.city, flags: body.flags, ratingID: body.rating_id || undefined,
          players: body.players.map((p) => ({name: `${p.first_name} ${p.last_name}`.trim(), ratingID: p.rating_id || undefined})),
        },
      }),
    });
  });
}

interface TeamsSourceView {
  bout?: boolean;
  from: number;
  sources: Array<{id: number; title: string}>;
  teams: OverrideTeam[];
  overrides: Override[];
}

export interface TeamSourceBlock {
  element: HTMLElement;
  load(): void;
}

// teamSourceBlock is a bout game's team source line over its entrant list
// (ADR-0038): where its teams' names and players come from, its overrides
// and the button that adds one. A game that seats no teams draws nothing.
// changed runs after every change, for the list under it to read its names
// again.
export function teamSourceBlock(apiBaseOf: () => string, changed: () => void): TeamSourceBlock {
  const element = document.createElement("div");
  element.className = "u-col u-gap-sm";
  let editorBase: Promise<TeamDetail | null> | null = null;
  const draw = (view: TeamsSourceView, notice: string): void => {
    const apiBase = apiBaseOf();
    if (!view.bout) {
      element.replaceChildren();
      return;
    }
    const bar = document.createElement("div");
    bar.className = "u-row u-wrap u-gap-sm u-align-center";
    const label = document.createElement("span");
    label.className = "hint";
    label.textContent = S.entrants.overrides.sourceLabel();
    const source = document.createElement("select");
    source.className = "input";
    source.setAttribute("aria-label", S.entrants.overrides.sourceLabel());
    source.append(new Option(S.entrants.own.follow(), "0"),
      ...view.sources.map((game) => new Option(S.entrants.own.sourceGame(game.title), String(game.id))));
    source.value = String(view.from);
    source.addEventListener("change", () => void write("", "PUT", {from: Number(source.value)}));
    const add = document.createElement("button");
    add.type = "button";
    add.className = "btn";
    add.append(...iconed("plus", S.entrants.overrides.add()));
    add.addEventListener("click", () => openOverrideDialog({
      api: apiBase,
      teams: view.teams,
      kinds: BOUT_KINDS,
      editorBase: () => {
        editorBase ??= request(`${apiBase}/teams/editor`, "GET").then((answer) => (answer.ok ? answer.data as TeamDetail : null));
        return editorBase;
      },
      send: (o) => write("/overrides", "POST", o),
    }));
    bar.append(label, source, add);
    const nodes: HTMLElement[] = [bar];
    if (notice) nodes.push(hint(notice, true));
    if (view.overrides.length) nodes.push(overridesList(view.overrides, false, (id) => void write(`/overrides/${id}`, "DELETE")));
    element.replaceChildren(...nodes);
  };
  const write = async (path: string, method: string, body?: unknown): Promise<Answer> => {
    const answer = await request(`${apiBaseOf()}/teams${path}`, method, body);
    if (answer.ok) {
      draw(answer.data as TeamsSourceView, "");
      changed();
    }
    return answer;
  };
  const load = (): void => {
    editorBase = null;
    void request(`${apiBaseOf()}/teams`, "GET").then((answer) => {
      if (answer.ok) draw(answer.data as TeamsSourceView, "");
      else element.replaceChildren();
    }).catch((error) => draw({from: 0, sources: [], teams: [], overrides: []}, errorText(error)));
  };
  return {element, load};
}
