// kd-view.ts — the friendship cup's own tabs on the OD page (ADR-0026): the
// personal standings, and the players with their route cards, which the host
// registers there and prints. The tables themselves are the OD sheet's teams
// and every other tab draws them as it draws teams.
import {td, th} from "./cells.js";
import {iconed, icon} from "./icons_gen.js";
import {resultsTeamCell} from "./standings.js";
import * as kd from "./kd-protocol.js";
import type {KDPlayer, KDState} from "./kd-protocol.js";
import S from "./i18nstrings.js";

export interface KDViewContext {
  state: KDState;
  tourLengths: number[];
  tables: number;
}

// tablesOf reads how many tables a friendship cup seats: the scheme says so,
// else the document's teams are the tables.
export function tablesOf(scheme: {kdTables?: unknown}, state: KDState): number {
  const n = Number(scheme.kdTables);
  return Number.isInteger(n) && n > 0 ? n : state.teams.length;
}

// === personal standings ===

export function buildPersonalView(ctx: KDViewContext): HTMLElement {
  const rows = kd.standings(ctx.state, ctx.tourLengths, ctx.tables);
  if (!rows.length) return emptyNote();
  const started = kd.toursStarted(ctx.state, ctx.tourLengths);
  const size = ctx.tourLengths[0] || 0;
  const table = document.createElement("table");
  table.className = "results-table";
  const head = document.createElement("tr");
  head.append(th(S.od.kd.place(), "results-place-head"), th(S.od.kd.player(), "results-team-head"), th(S.od.kd.team()), th(S.od.head.total(), "results-num-head results-total-head"));
  ctx.tourLengths.forEach((_, t) => head.appendChild(th(S.od.detailed.tour(String(t + 1)), "results-tour-head")));
  for (let k = 0; k < kd.TIEBREAKS && size - k > 0; k++) {
    head.appendChild(th(S.od.kd.toursTook(String(size - k)), "results-num-head", {title: S.od.kd.toursTookHint(String(size - k))}));
  }
  const thead = document.createElement("thead");
  thead.appendChild(head);
  const tbody = document.createElement("tbody");
  for (const row of rows) {
    const tr = document.createElement("tr");
    tr.className = "results-row";
    tr.append(td(row.place || "—", "results-place"), resultsTeamCell(row.player.name), td(row.player.team || ""), td(row.total, "results-num total-cell results-total"));
    row.tours.forEach((took, t) => tr.appendChild(started[t]
      ? td(took, "results-tour", {title: S.od.kd.atTable(String(row.tables[t]))})
      : td("·", "results-tour results-tour-pending", {title: S.od.kd.atTable(String(row.tables[t]))})));
    for (let k = 0; k < kd.TIEBREAKS && size - k > 0; k++) tr.appendChild(td(row.best[k], "results-num"));
    tbody.appendChild(tr);
  }
  table.append(thead, tbody);
  const wrapper = document.createElement("div");
  wrapper.className = "results-wrapper";
  wrapper.appendChild(table);
  return wrapper;
}

function emptyNote(): HTMLElement {
  const note = document.createElement("p");
  note.className = "hint";
  note.textContent = S.od.kd.empty();
  return note;
}

// === players and route cards ===

export interface KDPlayersOptions extends KDViewContext {
  viewer: boolean;
  festID: string | number | null | undefined;
  // current is the document the page holds now. A click reads it, not the
  // state the tab was drawn from, which a remote update may have replaced.
  current: () => KDState;
  // register writes one player under his card, unregister frees a card; each
  // is a patch of its own, and the page re-renders from its state.
  register: (player: KDPlayer) => void;
  unregister: (card: number) => void;
  // refresh draws the tab again without writing, to show a refusal.
  refresh: () => void;
}

// notice is what the last refused add said, shown above the add row until the
// host types again or tries again.
let notice = "";

// sent is the registration the page wrote last, and returned is what the add
// row gets back after the server refused it: the name and team the host typed,
// so he fixes the card instead of typing the person again.
let sent: KDPlayer | null = null;
let returned: KDPlayer | null = null;

// showRefusal puts the server's refusal of a registration above the add row,
// for the page to draw, and hands the refused person back to the add row.
export function showRefusal(message: string): void {
  notice = message;
  returned = sent;
  sent = null;
}

export function buildPlayersView(opts: KDPlayersOptions): HTMLElement {
  const panel = document.createElement("div");
  panel.className = "u-col u-gap-md";
  const note = document.createElement("p");
  note.className = "hint";
  note.textContent = S.od.kd.tablesNote(String(opts.tables));
  panel.appendChild(note);
  const list = kd.players(opts.state, opts.tables);
  if (!opts.viewer) {
    let refused: HTMLElement | null = null;
    if (notice) {
      refused = document.createElement("p");
      refused.className = "hint hint-danger";
      refused.textContent = notice;
      panel.appendChild(refused);
    }
    const form = addForm(opts, list);
    // The refusal is about what was typed; once the host types again it has
    // been read, and it goes.
    form.addEventListener("input", () => {
      notice = "";
      refused?.remove();
      refused = null;
    });
    panel.appendChild(form);
  }
  // Nine tours of tables are wider than a phone: the table scrolls on its
  // own rather than pushing the page sideways.
  const scroll = document.createElement("div");
  scroll.className = "kd-players-scroll";
  scroll.appendChild(list.length ? playersTable(opts, list) : emptyNote());
  panel.appendChild(scroll);
  if (!opts.viewer) panel.appendChild(printRow(opts, list));
  return panel;
}

// printRow prints the route cards: the registered players' with their names,
// or blank ones numbered 1…N for the vases the players draw from at the door
// (the regulations' order), whose numbers the host then types in here.
function printRow(opts: KDPlayersOptions, list: KDPlayer[]): HTMLElement {
  const row = document.createElement("div");
  row.className = "u-row u-gap-sm u-wrap";
  if (list.length) {
    const print = document.createElement("button");
    print.type = "button";
    print.className = "btn";
    print.append(...iconed("file-text", S.od.kd.print()));
    print.addEventListener("click", () => printCards(opts, list));
    row.appendChild(print);
  }
  const count = document.createElement("input");
  count.type = "text";
  count.inputMode = "numeric";
  count.className = "input";
  count.size = 4;
  count.value = String(blankCardCount(opts, list));
  count.setAttribute("aria-label", S.od.kd.blankCount());
  count.title = S.od.kd.blankCount();
  const blank = document.createElement("button");
  blank.type = "button";
  blank.className = "btn";
  blank.append(...iconed("file-text", S.od.kd.printBlank()));
  blank.addEventListener("click", () => {
    const n = Number(count.value.trim());
    if (!Number.isInteger(n) || n < 1) return;
    if (n > kd.maxCard(opts.tables)) {
      notice = S.od.kd.cardTooHigh(String(opts.tables), String(kd.maxCard(opts.tables)));
      opts.refresh();
      return;
    }
    printCards(opts, Array.from({length: n}, (_, i) => ({card: i + 1, name: "", team: ""})));
  });
  row.append(count, blank);
  return row;
}

// blankCardCount is how many blank cards to offer: six a table, as last
// year's sheet sat them, never fewer than the highest card given out, and
// never more than the tables tell apart.
function blankCardCount(opts: KDPlayersOptions, list: KDPlayer[]): number {
  return Math.min(kd.maxCard(opts.tables), Math.max(opts.tables * 6, ...list.map((p) => p.card)));
}

function playersTable(opts: KDPlayersOptions, list: KDPlayer[]): HTMLElement {
  const table = document.createElement("table");
  table.className = "match-table kd-players-table";
  const head = document.createElement("tr");
  head.append(th(S.od.kd.card()), th(S.od.kd.player(), "results-team-head"), th(S.od.kd.team()));
  opts.tourLengths.forEach((_, t) => head.appendChild(th(S.od.detailed.tour(String(t + 1)))));
  if (!opts.viewer) head.appendChild(th(""));
  const thead = document.createElement("thead");
  thead.appendChild(head);
  const tbody = document.createElement("tbody");
  for (const player of list) {
    const tr = document.createElement("tr");
    tr.append(td(player.card), resultsTeamCell(player.name), td(player.team || ""));
    opts.tourLengths.forEach((_, t) => tr.appendChild(td(kd.kdTable(player.card, t + 1, opts.tables))));
    if (!opts.viewer) {
      const remove = document.createElement("button");
      remove.type = "button";
      remove.className = "action-icon";
      const label = S.od.kd.remove(player.name);
      remove.title = label;
      remove.setAttribute("aria-label", label);
      remove.appendChild(icon("trash-2"));
      remove.addEventListener("click", () => {
        notice = "";
        sent = null;
        opts.unregister(player.card);
      });
      tr.appendChild(td(remove));
    }
    tbody.appendChild(tr);
  }
  table.append(thead, tbody);
  return table;
}

// addForm registers one player: the next free card is offered, the name may
// be anyone's, and a fest player's name fills in his team.
function addForm(opts: KDPlayersOptions, list: KDPlayer[]): HTMLElement {
  const form = document.createElement("form");
  form.className = "u-row u-wrap u-gap-sm u-align-center";
  form.dataset.kdAddForm = "";
  const card = document.createElement("input");
  card.type = "text";
  card.inputMode = "numeric";
  card.className = "input";
  card.size = 4;
  card.value = String(kd.nextFreeCard(list));
  card.setAttribute("aria-label", S.od.kd.card());
  card.dataset.kdField = "card";
  // The offered card follows the list until the host types his own.
  card.dataset.kdAuto = "";
  card.addEventListener("input", () => delete card.dataset.kdAuto);
  const name = document.createElement("input");
  name.type = "text";
  name.className = "input";
  name.size = 28;
  name.placeholder = S.od.kd.namePlaceholder();
  name.setAttribute("aria-label", S.od.kd.namePlaceholder());
  name.dataset.kdName = "";
  name.dataset.kdField = "name";
  const team = document.createElement("input");
  team.type = "text";
  team.className = "input";
  team.size = 24;
  team.placeholder = S.od.kd.teamPlaceholder();
  team.setAttribute("aria-label", S.od.kd.teamPlaceholder());
  team.dataset.kdField = "team";
  const suggestions = document.createElement("datalist");
  suggestions.id = "kd-fest-players";
  name.setAttribute("list", suggestions.id);
  const teamOf = new Map<string, string>();
  void loadFestPlayers(opts.festID).then((people) => {
    for (const person of people) {
      teamOf.set(person.name, person.team);
      const option = document.createElement("option");
      option.value = person.name;
      option.label = person.team;
      suggestions.appendChild(option);
    }
  });
  name.addEventListener("change", () => {
    const known = teamOf.get(name.value.trim());
    if (known && !team.value.trim()) team.value = known;
  });
  const add = document.createElement("button");
  add.type = "submit";
  add.className = "btn";
  add.append(...iconed("plus", S.od.kd.add()));
  form.append(card, name, team, suggestions, add);
  // A registration the server refused comes back into the row once; the card
  // stays the offered free one, since the refused card is the one taken.
  if (returned) {
    name.value = returned.name;
    team.value = returned.team || "";
    returned = null;
  }
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    const number = Number(card.value.trim());
    const who = name.value.trim();
    const holder = kd.players(opts.current()).find((p) => p.card === number);
    if (!Number.isInteger(number) || number < 1) notice = S.od.kd.cardInvalid();
    else if (number > kd.maxCard(opts.tables)) notice = S.od.kd.cardTooHigh(String(opts.tables), String(kd.maxCard(opts.tables)));
    else if (!who) notice = S.od.kd.nameRequired();
    else if (holder) notice = S.od.kd.cardTaken(String(number), holder.name);
    else notice = "";
    if (notice) {
      // The refusal redraws the tab; what the host typed stays for him to fix.
      const draft = captureDraft(form.ownerDocument);
      opts.refresh();
      restoreDraft(form.ownerDocument, draft);
      return;
    }
    sent = {card: number, name: who, team: team.value.trim()};
    opts.register(sent);
    document.querySelector<HTMLInputElement>("[data-kd-name]")?.focus();
  });
  return form;
}

// PlayersDraft is what the host has typed into the add row and not sent yet,
// and where his cursor is. A remote update redraws the players tab, and the
// echo of every save is one, so the page carries the draft across the redraw.
export interface PlayersDraft {
  values: Record<string, string>;
  auto: boolean;
  focused: string | null;
  selection: [number, number] | null;
}

export function captureDraft(root: ParentNode): PlayersDraft | null {
  const form = root.querySelector<HTMLFormElement>("[data-kd-add-form]");
  if (!form) return null;
  const draft: PlayersDraft = {values: {}, auto: false, focused: null, selection: null};
  const active = form.ownerDocument.activeElement;
  for (const input of form.querySelectorAll<HTMLInputElement>("[data-kd-field]")) {
    const field = input.dataset.kdField || "";
    draft.values[field] = input.value;
    if (field === "card") draft.auto = input.dataset.kdAuto !== undefined;
    if (input === active) {
      draft.focused = field;
      draft.selection = input.selectionStart === null ? null : [input.selectionStart, input.selectionEnd ?? input.selectionStart];
    }
  }
  return draft;
}

// restoreDraft puts a draft back into a freshly drawn add row. An offered
// card the host never touched is not restored: the redraw offers the card
// that is free now.
export function restoreDraft(root: ParentNode, draft: PlayersDraft | null): void {
  if (!draft) return;
  const form = root.querySelector<HTMLFormElement>("[data-kd-add-form]");
  if (!form) return;
  for (const input of form.querySelectorAll<HTMLInputElement>("[data-kd-field]")) {
    const field = input.dataset.kdField || "";
    if (field === "card" && draft.auto) continue;
    if (field in draft.values) input.value = draft.values[field];
    if (field === "card") delete input.dataset.kdAuto;
  }
  if (draft.focused) {
    const input = form.querySelector<HTMLInputElement>(`[data-kd-field="${draft.focused}"]`);
    if (input) {
      input.focus();
      if (draft.selection) input.setSelectionRange(draft.selection[0], draft.selection[1]);
    }
  }
}

interface FestPerson {
  name: string;
  team: string;
}

// loadFestPlayers reads the fest roster once for the name suggestions; a
// failure leaves the host typing names by hand, which is always allowed.
let festPeople: Promise<FestPerson[]> | null = null;
function loadFestPlayers(festID: string | number | null | undefined): Promise<FestPerson[]> {
  if (!festID) return Promise.resolve([]);
  festPeople ||= fetch(`/api/fest/${encodeURIComponent(String(festID))}/roster`)
    .then((response) => response.ok ? response.json() : null)
    .then((body: {teams?: Array<{name?: string; players?: Array<{name?: string}>}>} | null) => {
      const out: FestPerson[] = [];
      for (const team of body?.teams || []) {
        for (const player of team.players || []) {
          if (player.name) out.push({name: player.name, team: team.name || ""});
        }
      }
      return out;
    })
    .catch(() => []);
  return festPeople;
}

// printCards lays every player's route card out on a sheet of its own for the
// browser's print dialog, and takes it away again once the dialog closes.
function printCards(opts: KDPlayersOptions, list: KDPlayer[]): void {
  document.querySelector(".kd-print")?.remove();
  const sheet = document.createElement("div");
  sheet.className = "kd-print";
  for (const player of list) sheet.appendChild(routeCard(opts, player));
  document.body.appendChild(sheet);
  document.body.classList.add("kd-printing");
  const done = () => {
    document.body.classList.remove("kd-printing");
    sheet.remove();
    window.removeEventListener("afterprint", done);
  };
  window.addEventListener("afterprint", done);
  window.print();
}

function routeCard(opts: KDPlayersOptions, player: KDPlayer): HTMLElement {
  const card = document.createElement("section");
  card.className = "kd-card u-col u-gap-xs";
  const title = document.createElement("h2");
  title.textContent = S.od.kd.cardTitle(String(player.card));
  const who = document.createElement("p");
  // A blank card, drawn from a vase, carries a line for the player's name.
  who.textContent = player.name ? (player.team ? `${player.name} · ${player.team}` : player.name) : S.od.kd.cardNameLine();
  card.append(title, who);
  const table = document.createElement("table");
  table.className = "kd-card-table";
  const tours = document.createElement("tr");
  const seats = document.createElement("tr");
  const points = document.createElement("tr");
  tours.appendChild(th(S.od.kd.cardTour()));
  seats.appendChild(th(S.od.kd.cardTable()));
  points.appendChild(th(S.od.kd.cardPoints()));
  opts.tourLengths.forEach((_, t) => {
    tours.appendChild(th(String(t + 1)));
    seats.appendChild(td(kd.kdTable(player.card, t + 1, opts.tables)));
    points.appendChild(td(""));
  });
  table.append(tours, seats, points);
  card.appendChild(table);
  if (kd.isJoker(player.card, opts.tables)) {
    const joker = document.createElement("p");
    joker.className = "hint";
    joker.textContent = S.od.kd.cardJoker(String(kd.kdTable(player.card, 1, opts.tables)));
    card.appendChild(joker);
  }
  return card;
}
