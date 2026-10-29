// A venue's page on a synch fest: its teams and their numbers, drawn from the
// server's copy with the device's unsent changes laid over it, so the host can
// keep working without a network (offline.ts). Every change goes through the
// outbox, which sends it at once when it can.

import {
  enqueue, flushOutbox, keepWorker, newClientID, numbersRemap, pendingCount, prepareOffline, projectVenueTeams,
  readOutbox, readTeamCopy, remapPendingAnswers, RETRY_MS, writeOutbox, writeTeamCopy,
} from "./offline.js";
import type {RosterOp, TeamKey, TeamRef, VenueTeam} from "./offline.js";
import {openTeamDialog, request, setInstantSearch} from "./team-editor.js";
import type {Answer, TeamDetail, TeamSave} from "./team-editor.js";
import S from "./i18nstrings.js";

interface PageConfig {
  fest: number;
  venue: number;
  api: string;
  max: number;
  // The venue's Games: the id their unsent answers are kept under, and the
  // page to save for work offline.
  games: Array<{id: number; url: string}>;
}

interface Page {
  config: PageConfig;
  root: HTMLElement;
  table: HTMLTableElement;
  template: HTMLTableRowElement;
  form: HTMLFormElement;
  // The server's last copy of the venue's teams.
  teams: VenueTeam[];
  offline: boolean;
  // A line that says a save reached the server, until the next change.
  notice: string;
}

type ServerTeam = {id: number; name: string; city: string; players: number; multifest_venue_id?: number; venue_number?: number};

function dateTime(at: number): string {
  return new Date(at).toLocaleString("ru-RU", {day: "numeric", month: "long", hour: "2-digit", minute: "2-digit"});
}

// shown is the venue's teams as the host sees them, numbered ones first.
function shown(page: Page): VenueTeam[] {
  const order = (t: VenueTeam) => t.number || Number.MAX_SAFE_INTEGER;
  return projectVenueTeams(page.teams, readOutbox(page.config.fest), page.config.venue).sort((a, b) => order(a) - order(b));
}

function keyOf(team: VenueTeam): TeamKey {
  return {name: team.name, city: team.city, number: team.number};
}

function rowOf(page: Page, team: VenueTeam): HTMLTableRowElement {
  const row = page.template.cloneNode(true) as HTMLTableRowElement;
  row.hidden = false;
  row.removeAttribute("data-team-row");
  row.dataset.teamRef = String(team.ref);
  const number = row.querySelector<HTMLInputElement>("input")!;
  number.name = `number_${team.ref}`;
  number.value = number.defaultValue = team.number ? String(team.number) : "";
  number.setAttribute("aria-label", S.host.venue.numberLabel(team.name));
  const pencil = row.querySelector<HTMLElement>("[data-team-edit]")!;
  pencil.dataset.teamEdit = String(team.ref);
  pencil.setAttribute("aria-label", S.host.roster.editTeamLabel(team.name));
  pencil.title = S.host.roster.editTeamLabel(team.name);
  row.querySelector("[data-team-name]")!.textContent = team.name;
  row.querySelector("[data-team-city]")!.textContent = team.city;
  row.querySelector("[data-team-players]")!.textContent = String(team.players);
  return row;
}

function drawRows(page: Page): void {
  const teams = shown(page);
  for (const row of page.table.querySelectorAll("tr[data-team-ref]")) row.remove();
  page.table.tBodies[0].append(...teams.map((team) => rowOf(page, team)));
  page.form.hidden = teams.length === 0;
  const empty = page.root.querySelector<HTMLElement>("[data-teams-empty]");
  if (empty) empty.hidden = teams.length > 0;
  const count = page.root.querySelector<HTMLElement>("[data-teams-count]");
  if (count) count.textContent = S.host.venue.teamsSubhead(String(teams.length));
  // The numbers' save button wakes on a change (pageforms.ts); new rows are none.
  page.form.querySelector("input")?.dispatchEvent(new Event("input", {bubbles: true}));
}

function statusLine(text: string, danger = false): HTMLElement {
  const p = document.createElement("p");
  p.className = danger ? "hint hint-danger" : "hint";
  p.textContent = text;
  return p;
}

function waitingText(page: Page, waiting: number): string {
  if (page.offline) return waiting ? S.host.offline.waiting(String(waiting)) : S.host.offline.noNetwork();
  return waiting ? S.host.offline.sending(String(waiting)) : page.notice;
}

function drawStatus(page: Page): void {
  const status = page.root.querySelector<HTMLElement>("[data-offline-status]");
  if (!status) return;
  const box = readOutbox(page.config.fest);
  const text = waitingText(page, pendingCount(box, page.config.venue));
  const lines = text ? [statusLine(text)] : [];
  lines.push(...box.refused.map((r) => statusLine(r.team ? S.host.offline.refusedTeam(r.team, r.message) : S.host.offline.refused(r.message), true)));
  if (box.refused.length) {
    const dismiss = document.createElement("button");
    dismiss.type = "button";
    dismiss.className = "btn btn-ghost";
    dismiss.textContent = S.host.offline.dismiss();
    dismiss.addEventListener("click", () => {
      writeOutbox(page.config.fest, {...readOutbox(page.config.fest), refused: []});
      drawStatus(page);
    });
    lines.push(dismiss);
  }
  status.replaceChildren(...lines);
  status.hidden = lines.length === 0;
}

async function refreshTeams(page: Page): Promise<void> {
  const answer = await request(`${page.config.api}/teams`, "GET");
  if (!answer.ok) return;
  page.teams = (answer.data as ServerTeam[]).filter((t) => t.multifest_venue_id === page.config.venue)
    .map((t) => ({ref: t.id, name: t.name, city: t.city, players: t.players, number: t.venue_number || 0}));
}

// sync sends what waits, and draws the page again from what the server has.
async function sync(page: Page): Promise<void> {
  const {fest, api} = page.config;
  if (pendingCount(readOutbox(fest)) === 0) return;
  const result = await flushOutbox(fest, api);
  page.offline = result.offline;
  if (result.empty) await refreshTeams(page);
  drawRows(page);
  drawStatus(page);
}

// keep queues a change and tries to send it. A refusal of this very change
// goes back to the editor, which is still open.
async function keep(page: Page, op: RosterOp): Promise<Answer> {
  const {fest} = page.config;
  page.notice = "";
  const before = readOutbox(fest).refused.length;
  writeOutbox(fest, enqueue(readOutbox(fest), op));
  drawRows(page);
  drawStatus(page);
  await sync(page);
  const box = readOutbox(fest);
  if (box.refused.length <= before) return {ok: true, message: ""};
  const message = box.refused.pop()?.message || "";
  writeOutbox(fest, box);
  drawStatus(page);
  return {ok: false, message};
}

// ---- the editor ----

async function fetchDetail(page: Page, ref: number | "new"): Promise<TeamDetail | null> {
  const {fest, venue, api} = page.config;
  const answer = await request(`${api}/teams/${ref}`, "GET");
  if (!answer.ok) {
    page.offline = true;
    drawStatus(page);
    return null;
  }
  const copy = readTeamCopy(fest, venue);
  if (copy && typeof ref === "number") writeTeamCopy(fest, venue, {...copy, teams: {...copy.teams, [ref]: answer.data}});
  return answer.data as TeamDetail;
}

// copiedDetail is a team as the device last saw it: the server's copy, or
// what the outbox holds for a team made or changed here.
function copiedDetail(page: Page, ref: TeamRef | "new"): TeamDetail | null {
  const {fest, venue} = page.config;
  const copy = readTeamCopy(fest, venue);
  if (!copy) return null;
  const fresh = copy.fresh as TeamDetail;
  if (ref === "new") return {...fresh, id: 0, players: []};
  const team = shown(page).find((t) => String(t.ref) === String(ref));
  if (!team) return null;
  const saved = copy.teams[String(ref)] as TeamDetail | undefined;
  const op = readOutbox(fest).ops.filter((o): o is Extract<RosterOp, {kind: "create" | "save"}> =>
    (o.kind === "create" && o.cid === ref) || (o.kind === "save" && String(o.ref) === String(ref))).pop();
  const players = op ? op.body.players : saved?.players || [];
  return {...fresh, ...saved, id: typeof ref === "number" ? ref : 0, local: typeof ref === "string",
    name: team.name, city: team.city, players: players.map((p) => ({...p})), choices: fresh.choices};
}

// detail is a team as the editor opens it: the server's answer, else the
// device's copy. A team made on the device and not sent yet is only there.
async function detail(page: Page, ref: TeamRef | "new"): Promise<TeamDetail | null> {
  const local = typeof ref === "string" && ref !== "new";
  const online = !page.offline && !local ? await fetchDetail(page, ref as number | "new") : null;
  return online || copiedDetail(page, ref);
}

async function openEditor(page: Page, ref: TeamRef | "new"): Promise<void> {
  const team = await detail(page, ref);
  if (!team) {
    window.alert(S.host.offline.notPrepared());
    return;
  }
  const {venue, api, max} = page.config;
  const teams = shown(page);
  const row = teams.find((t) => String(t.ref) === String(ref));
  const taken = teams.map((t) => t.number).filter((n) => n > 0);
  const body = (save: TeamSave) => ({...save, multifest_venue_id: venue});
  if (ref === "new") {
    openTeamDialog(api, team, venue, {
      offline: page.offline,
      venueNumber: {offered: Math.max(0, ...taken) + 1, taken, max},
      save: (save) => keep(page, {kind: "create", cid: newClientID(), venue, body: body(save)}),
      remove: () => Promise.resolve({ok: true, message: ""}),
    });
    return;
  }
  if (!row) return;
  openTeamDialog(api, team, venue, {
    offline: page.offline,
    save: (save) => keep(page, {kind: "save", ref: row.ref, venue, was: keyOf(row), body: body(save)}),
    remove: () => keep(page, {kind: "remove", ref: row.ref, venue, was: keyOf(row)}),
  });
}

// ---- the numbers ----

function saveNumbers(page: Page): void {
  const {venue, games, fest} = page.config;
  const numbers = shown(page).map((team) => {
    const input = page.form.querySelector<HTMLInputElement>(`tr[data-team-ref="${CSS.escape(String(team.ref))}"] input`);
    return {ref: team.ref, number: Number(input?.value || 0), was: keyOf(team)};
  });
  const op: RosterOp = {kind: "numbers", venue, numbers};
  for (const game of games) remapPendingAnswers(game.id, venue, numbersRemap(op));
  void keep(page, op).then((answer) => {
    if (!answer.ok) {
      window.alert(answer.message);
      return;
    }
    if (pendingCount(readOutbox(fest), venue) === 0) page.notice = S.host.venue.numbersSaved();
    drawStatus(page);
  });
}

// ---- work without a network ----

// copyTeams keeps what the editor needs without a network: the new team's
// answer and each team of the venue.
async function copyTeams(page: Page): Promise<boolean> {
  const {fest, venue, api} = page.config;
  const fresh = await request(`${api}/teams/new`, "GET");
  if (!fresh.ok) return false;
  const copy = {fresh: fresh.data, teams: {} as Record<string, unknown>, at: Date.now()};
  for (const team of page.teams) {
    const answer = await request(`${api}/teams/${team.ref}`, "GET");
    if (answer.ok) copy.teams[String(team.ref)] = answer.data;
  }
  writeTeamCopy(fest, venue, copy);
  return true;
}

function drawPrepared(page: Page, note: HTMLElement): void {
  const copy = readTeamCopy(page.config.fest, page.config.venue);
  note.textContent = copy ? S.host.offline.prepared(dateTime(copy.at)) : S.host.offline.prepareHint();
}

async function prepare(page: Page, button: HTMLButtonElement, note: HTMLElement): Promise<void> {
  button.disabled = true;
  note.textContent = S.host.offline.preparing();
  const pages = [location.pathname, ...page.config.games.map((g) => g.url)];
  try {
    const [failed, copied] = await Promise.all([prepareOffline(pages), copyTeams(page)]);
    if (failed.length || !copied) note.textContent = S.host.offline.prepareFailed();
    else drawPrepared(page, note);
  } catch (_) {
    note.textContent = S.host.offline.prepareFailed();
  }
  button.disabled = false;
}

function bindOffline(page: Page): void {
  const button = page.root.querySelector<HTMLButtonElement>("[data-offline-prepare]");
  const note = page.root.querySelector<HTMLElement>("[data-offline-prepared]");
  if (!button || !note) return;
  if (!("serviceWorker" in navigator)) {
    button.disabled = true;
    note.textContent = S.host.offline.unsupported();
    return;
  }
  drawPrepared(page, note);
  button.addEventListener("click", () => void prepare(page, button, note));
  // A page prepared before keeps its worker and its copy fresh while online.
  if (readTeamCopy(page.config.fest, page.config.venue)) {
    keepWorker();
    if (navigator.onLine) void copyTeams(page).then(() => drawPrepared(page, note));
  }
}

function bindNetwork(page: Page): void {
  window.addEventListener("online", () => {
    page.offline = false;
    drawStatus(page);
    void sync(page);
  });
  window.addEventListener("offline", () => {
    page.offline = true;
    drawStatus(page);
  });
  window.setInterval(() => void sync(page), RETRY_MS);
}

function mount(root: HTMLElement): void {
  const table = root.querySelector<HTMLTableElement>("table");
  const template = root.querySelector<HTMLTableRowElement>("tr[data-team-row]");
  const form = root.querySelector<HTMLFormElement>("form[data-venue-numbers]");
  const addButton = root.querySelector<HTMLElement>("[data-team-add]");
  if (!table || !template || !form || !addButton) return;
  const page: Page = {
    config: JSON.parse(root.dataset.venuePage || "{}") as PageConfig,
    teams: JSON.parse(root.dataset.venueTeams || "[]") as VenueTeam[],
    root, table, template, form, offline: !navigator.onLine, notice: "",
  };
  setInstantSearch(addButton.dataset.teamSearch === "instant");
  addButton.addEventListener("click", () => void openEditor(page, "new"));
  table.addEventListener("click", (event) => {
    const ref = (event.target as HTMLElement).closest<HTMLElement>("[data-team-edit]")?.dataset.teamEdit;
    if (!ref) return;
    event.preventDefault();
    void openEditor(page, /^\d+$/.test(ref) ? Number(ref) : ref);
  });
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    saveNumbers(page);
  });
  bindOffline(page);
  bindNetwork(page);
  drawRows(page);
  drawStatus(page);
  void sync(page);
}

const root = document.querySelector<HTMLElement>("[data-venue-page]");
if (root) mount(root);
