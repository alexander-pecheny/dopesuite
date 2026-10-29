// The team editor (ADR-0024): the host adds a team, renames one, changes its
// people, or takes it off the roster. People are suggested from the fest
// roster and from everybody the rating site knows, and anybody else can be
// typed by name. A suggested person who plays for another team of the fest
// moves here. A new team can be the rating site's team, found by name, and
// still play under a one-off name; the base-roster button adds its base roster
// to start from. The page that opens the editor says how a save is sent: the
// fest's teams page sends it at once, a venue's page through its outbox.

import {autocomplete} from "../../../../dopeuikit/assets/ts/suggest.js";
import type {Choice} from "../../../../dopeuikit/assets/ts/suggest.js";
import {icon} from "./icons_gen.js";
import S from "./i18nstrings.js";

// Most people the player picker offers at once.
const PLAYER_CHOICE_LIMIT = 14;

export interface TeamPlayer {
  rating_id: number;
  first_name: string;
  last_name: string;
  patronymic?: string;
  games?: number;
  team?: string;
}

export interface TeamDetail {
  id: number;
  rating_id: number;
  name: string;
  city: string;
  hand: boolean;
  players: TeamPlayer[];
  choices: TeamPlayer[];
  multifest_venue_id: number;
  // The venues the host may put a team they made on (ADR-0032).
  multifest_venues?: Array<{id: number; title: string}>;
  // The team's Divisions, and every one the fest's teams carry.
  flags: string[] | null;
  divisions: string[];
  // A team made on this device that the server has not seen yet.
  local?: boolean;
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

export async function request(url: string, method: string, body?: unknown): Promise<{ok: boolean; data: unknown; message: string}> {
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

// How long the editor waits after the last letter before it asks the rating
// site; the server's mirror of the site (instantSearch) is asked at once.
const SEARCH_DEBOUNCE_MS = 250;
let instantSearch = false;

export function setInstantSearch(on: boolean): void {
  instantSearch = on;
}

// TeamSave is a team as the editor sends it.
export interface TeamSave {
  rating_id: number;
  name: string;
  city: string;
  players: Array<{rating_id: number; first_name: string; last_name: string}>;
  multifest_venue_id?: number;
  flags: string[];
  venue_number?: number;
}

export interface Answer {
  ok: boolean;
  message: string;
}

// EditorActions is how the page that opened the editor keeps a change.
export interface EditorActions {
  save(body: TeamSave): Promise<Answer>;
  remove(): Promise<Answer>;
  // Without a network the rating site cannot be searched.
  offline?: boolean;
  // A new team on a venue's page takes its number there in the editor:
  // the number offered, and the ones the venue's teams already have.
  venueNumber?: {offered: number; taken: number[]; max: number};
}

// remoteChoices keeps the rating site's answers per query and asks for a new
// one once the host stops typing, or at every letter when the server searches
// its mirror. Until an answer comes the last one stays up, so the list does
// not blink empty between letters, and each answer redraws it.
function remoteChoices<T>(url: (q: string) => string, redraw: () => void): (q: string) => T[] {
  const answers = new Map<string, T[]>();
  let last: T[] = [];
  let timer = 0;
  return (q: string) => {
    const key = q.trim().toLowerCase();
    if (key.length < 2) return [];
    const known = answers.get(key);
    if (known) {
      last = known;
      return known;
    }
    window.clearTimeout(timer);
    timer = window.setTimeout(() => {
      void request(url(key), "GET").then((answer) => {
        answers.set(key, answer.ok ? answer.data as T[] : []);
        redraw();
      });
    }, instantSearch ? 0 : SEARCH_DEBOUNCE_MS);
    return last;
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

// divisionsField picks a team's Divisions: a box for each one the fest's
// teams carry, and a field for a new one. read gives the picked short names.
function divisionsField(team: TeamDetail): {node: HTMLElement; read(): string[]} {
  const own = team.flags || [];
  const known = team.divisions || [];
  // A div, not field's label: the boxes have labels of their own.
  const [label, otherInput] = field(S.fest.teamEdit.divisions(), own.filter((short) => !known.includes(short)).join(", "));
  const wrap = document.createElement("div");
  wrap.className = label.className;
  wrap.append(...label.childNodes);
  otherInput.setAttribute("aria-label", known.length ? S.fest.teamEdit.divisionsOther() : S.fest.teamEdit.divisions());
  if (known.length) otherInput.placeholder = S.fest.teamEdit.divisionsOther();
  const boxes = document.createElement("div");
  boxes.className = "u-row u-gap-md u-wrap";
  const checks = known.map((short) => {
    const label = document.createElement("label");
    label.className = "checkbox";
    const box = document.createElement("input");
    box.type = "checkbox";
    box.value = short;
    box.checked = own.includes(short);
    label.append(box, short);
    boxes.appendChild(label);
    return box;
  });
  if (checks.length) otherInput.before(boxes);
  wrap.appendChild(hintLine(S.fest.teamEdit.divisionsHint()));
  return {
    node: wrap,
    read: () => [...checks.filter((box) => box.checked).map((box) => box.value),
      ...otherInput.value.split(",").map((short) => short.trim()).filter(Boolean)],
  };
}

function hintLine(text: string): HTMLElement {
  const hint = document.createElement("p");
  hint.className = "hint";
  hint.textContent = text;
  return hint;
}

// venueNumberField is a new team's number on a venue's page: a venue takes its
// number at once, so its answers can be entered before the server has seen it.
function venueNumberField(spec: NonNullable<EditorActions["venueNumber"]>): {node: HTMLElement; value(): number; refusal(): string} {
  const [node, input] = field(S.fest.teamEdit.venueNumber(), String(spec.offered));
  input.type = "number";
  input.min = "1";
  input.max = String(spec.max);
  input.classList.add("input-narrow");
  const value = () => Number(input.value);
  const refusal = () => {
    const n = value();
    if (!Number.isInteger(n) || n < 1 || n > spec.max) return S.fest.teamEdit.venueNumberRange(String(spec.max));
    return spec.taken.includes(n) ? S.fest.teamEdit.venueNumberTaken(String(n)) : "";
  };
  return {node, value, refusal};
}

// ratingTeamPicker finds the rating site's team a new team is, by name, and
// shows the one picked with a button to unpick it.
function ratingTeamPicker(api: string, initial: RatingTeam | null, onChange: (team: RatingTeam | null) => void): {node: HTMLElement; close(): void} {
  const node = document.createElement("div");
  node.className = "u-col u-gap-xs";
  let suggest: {close(): void} | null = null;
  const draw = (team: RatingTeam | null) => {
    suggest?.close();
    suggest = null;
    if (team) {
      node.replaceChildren(...pickedRatingTeam(team, () => {
        draw(null);
        onChange(null);
      }));
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
    const found = remoteChoices<RatingTeam>((q) => `${api}/rating/teams?q=${encodeURIComponent(q)}`, () => picker.refresh());
    const picker = autocomplete(teamInput, (q) => found(q).map((t) => {
      const value = String(t.rating_id);
      byValue.set(value, t);
      return {value, label: t.name, hint: [t.city, S.fest.teamEdit.ratingId(value)].filter(Boolean).join(" · ")};
    }), (choice) => {
      const picked = byValue.get(choice.value);
      if (!picked) return;
      draw(picked);
      onChange(picked);
    });
    suggest = picker;
    node.replaceChildren(teamField);
  };
  draw(initial);
  return {node, close: () => suggest?.close()};
}

function pickedRatingTeam(team: RatingTeam, unpick: () => void): HTMLElement[] {
  const picked = document.createElement("div");
  picked.className = "u-row u-gap-sm u-align-center u-justify-between";
  const label = document.createElement("span");
  const where = team.city ? `${team.name} (${team.city})` : team.name;
  label.textContent = S.fest.teamEdit.ratingTeamPicked(where, String(team.rating_id));
  picked.append(label, button("btn btn-ghost", S.fest.teamEdit.ratingTeamUnpick(), unpick));
  const caption = document.createElement("span");
  caption.textContent = S.fest.teamEdit.ratingTeam();
  return [caption, picked];
}

function button(className: string, text: string, click: () => void): HTMLButtonElement {
  const node = document.createElement("button");
  node.type = "button";
  node.className = className;
  node.textContent = text;
  node.addEventListener("click", click);
  return node;
}

// venuePicker is where a team the host made plays; a new one starts on the
// venue the page shows. A rating team plays where the site says, and a host
// with one venue has nothing to pick.
function venuePicker(team: TeamDetail, isNew: boolean, pageVenue: number): HTMLSelectElement | null {
  const venues = team.multifest_venues || [];
  if (venues.length <= 1 || !(isNew || team.hand)) return null;
  const select = document.createElement("select");
  select.className = "input";
  const current = isNew ? pageVenue : team.multifest_venue_id;
  for (const venue of venues) select.appendChild(new Option(venue.title, String(venue.id), false, venue.id === current));
  const label = document.createElement("label");
  label.className = "field";
  const caption = document.createElement("span");
  caption.textContent = S.fest.teamEdit.venue();
  label.append(caption, select);
  return select;
}

// playerChoices is who the add row offers for what was typed: the fest's own
// people first, since moving one is the usual edit, then the rating site's.
function playerChoices(team: TeamDetail, q: string, draft: TeamPlayer[], rated: TeamPlayer[], byValue: Map<string, TeamPlayer>): Choice[] {
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
  for (const p of team.choices) {
    if (matches(fullName(p), q)) offer(p, p.team && p.team !== team.name ? S.fest.teamEdit.movesFrom(p.team) : "");
  }
  for (const p of rated) {
    const games = p.games ? S.fest.teamEdit.games(p.games) : "";
    offer(p, [p.patronymic || "", games, S.fest.teamEdit.ratingId(String(p.rating_id))].filter(Boolean).join(" · "));
  }
  return out.slice(0, PLAYER_CHOICE_LIMIT);
}

export function openTeamDialog(api: string, team: TeamDetail, pageVenue: number, actions: EditorActions): void {
  const isNew = !(team.id > 0) && !team.local;
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
  const number = isNew && actions.venueNumber ? venueNumberField(actions.venueNumber) : null;
  const divisions = divisionsField(team);

  const venueSelect = venuePicker(team, isNew, pageVenue);

  const error = document.createElement("p");
  error.className = "hint hint-danger";
  error.hidden = true;
  const fail = (message: string) => {
    error.textContent = message;
    error.hidden = false;
  };

  // A new team: the rating site's team it is, if any.
  const rating = isNew && !actions.offline ? ratingTeamPicker(api, ratingTeam, (picked) => {
    ratingTeam = picked;
    if (picked && !nameInput.value.trim()) nameInput.value = picked.name;
    if (picked && !cityInput.value.trim()) cityInput.value = picked.city;
    drawBaseButton();
  }) : null;

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
  const suggest = autocomplete(input, (q) => playerChoices(team, q, draft, rated(q), byValue), (choice) => {
    picked = byValue.get(choice.value) || null;
    add();
  });
  let picked: TeamPlayer | null = null;
  // add puts the picked suggestion on the team; a typed name alone is refused,
  // so nobody lands on it without a rating id by accident. unrated adds the
  // typed name as someone the site does not know yet: id 0, which the
  // organizer matches on import.
  const add = (unrated = false) => {
    const player = unrated ? typedPlayer(input.value) : picked;
    picked = null;
    if (!player || !fullName(player)) {
      if (input.value.trim() || unrated) fail(unrated ? S.fest.teamEdit.unratedNeedsName() : S.fest.teamEdit.pickOrUnrated());
      input.focus();
      return;
    }
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
  const addButton = button("btn", S.fest.teamEdit.add(), () => add());
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
  const unratedRow = document.createElement("div");
  unratedRow.className = "u-col u-gap-xs u-align-start";
  unratedRow.append(button("btn btn-ghost", S.fest.teamEdit.unratedButton(), () => add(true)), hintLine(S.fest.teamEdit.unratedHint()));

  // The base-roster button: the rating team's base roster, added to the list as a
  // start the host then trims or extends. A team without one gets no button.
  const baseRow = document.createElement("div");
  baseRow.className = "u-row";
  const drawBaseButton = () => {
    baseRow.replaceChildren();
    if (!ratingTeam || actions.offline) return;
    const team = ratingTeam;
    void request(`${api}/rating/teams/${team.rating_id}/base`, "GET").then((answer) => {
      if (ratingTeam !== team) return;
      if (!answer.ok) {
        fail(S.fest.teamEdit.baseRosterFailed());
        return;
      }
      const players = (answer.data as {players: TeamPlayer[]}).players || [];
      if (players.length === 0) return;
      const base = document.createElement("button");
      base.type = "button";
      base.className = "btn btn-ghost";
      base.textContent = S.fest.teamEdit.baseRoster();
      base.addEventListener("click", () => {
        error.hidden = true;
        for (const p of players) push(p);
        drawList();
      });
      baseRow.replaceChildren(base);
    });
  };
  drawBaseButton();

  const busy = (on: boolean) => {
    for (const button of form.querySelectorAll<HTMLButtonElement>("button")) button.disabled = on;
  };
  const finish = (answer: Answer) => {
    busy(false);
    if (answer.ok) dialog.close();
    else fail(answer.message);
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
      void actions.remove().then(finish);
    });
    deleteRow = document.createElement("div");
    deleteRow.className = "u-row";
    deleteRow.appendChild(remove);
  }

  const buttons = document.createElement("div");
  buttons.className = "modal-actions";
  const cancel = document.createElement("button");
  cancel.type = "button";
  cancel.className = "btn";
  cancel.textContent = S.fest.teamEdit.cancel();
  cancel.addEventListener("click", () => dialog.close());
  const save = document.createElement("button");
  save.type = "submit";
  save.className = "btn";
  save.textContent = S.fest.teamEdit.save();
  buttons.append(cancel, save);

  form.addEventListener("submit", (event) => {
    event.preventDefault();
    error.hidden = true;
    // A name left in the field is added if it was picked, and refused if not.
    if (input.value.trim()) add();
    if (!error.hidden) return;
    const body: TeamSave = {
      rating_id: isNew && ratingTeam ? ratingTeam.rating_id : 0,
      name: nameInput.value, city: cityInput.value,
      players: draft.map((p) => ({rating_id: p.rating_id, first_name: p.first_name, last_name: p.last_name})),
      multifest_venue_id: venueSelect ? Number(venueSelect.value) : undefined,
      flags: divisions.read(),
    };
    if (number) {
      const refusal = number.refusal();
      if (refusal) {
        fail(refusal);
        return;
      }
      body.venue_number = number.value();
    }
    busy(true);
    void actions.save(body).then(finish);
  });

  form.append(title, hint);
  if (actions.offline) form.appendChild(hintLine(S.fest.teamEdit.offlineHint()));
  if (rating) form.appendChild(rating.node);
  form.append(nameField, cityField);
  if (number) form.appendChild(number.node);
  if (venueSelect) form.appendChild(venueSelect.closest("label")!);
  form.appendChild(divisions.node);
  form.append(list, addRow, unratedRow, baseRow, error);
  if (deleteRow) form.appendChild(deleteRow);
  form.appendChild(buttons);
  dialog.appendChild(form);
  dialog.addEventListener("close", () => {
    suggest.close();
    rating?.close();
    dialog.remove();
  });
  document.body.appendChild(dialog);
  dialog.showModal();
  (isNew ? (rating?.node.querySelector("input") || nameInput) : input).focus();
}

// playerRow is one person of the draft: the name, and a cross to take them off.
function playerRow(player: TeamPlayer, remove: () => void): HTMLElement {
  const row = document.createElement("div");
  row.className = "u-row u-gap-sm u-align-center u-justify-between";
  const name = document.createElement("span");
  name.textContent = fullName(player);
  if (player.rating_id <= 0) {
    const badge = document.createElement("span");
    badge.className = "badge";
    badge.textContent = S.fest.teamEdit.noRatingId();
    name.append(" ", badge);
  }
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

