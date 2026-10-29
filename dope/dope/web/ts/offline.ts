// offline.ts — a venue's work without a network on a synch fest. The host's
// changes to the venue's teams wait in an outbox in localStorage and go to
// the server in order when the network comes back; the OD answers wait in the
// game page's own pending edits (state-sync.ts) and go after the outbox, since
// the server keeps a column's slots to the teams it knows.
//
// The page draws what the host sees from the last copy the server sent and
// the outbox laid over it (projectVenueTeams, projectODState). A team made on
// the device is named by a client id until the server gives it one; the OD
// document names a team by its number at its venue, so the outbox keeps the
// name and number each change was made against.

import type {PatchPath} from "./state-sync.js";

const OUTBOX_PREFIX = "dope.outbox.v1";
const COPY_PREFIX = "dope.offline.v1";
const PENDING_PREFIX = "dope.pending.v2";
// An OD answer entered offline waits a week for the network: a tournament
// day, with room to spare.
export const OFFLINE_PENDING_TTL_MS = 604_800_000;
// First HTTP status that counts as a server error worth retrying, and the
// first that is a refusal.
const SERVER_ERROR_STATUS = 500;
const HTTP_CLIENT_ERROR = 400;
// How often a page with unsent changes tries the network again.
export const RETRY_MS = 10000;
// How often a game page's bar counts what waits.
const BAR_REDRAW_MS = 2000;

export interface TeamPlayer {
  rating_id: number;
  first_name: string;
  last_name: string;
}

// TeamBody is a team as the editor saves it (POST/PUT …/teams).
export interface TeamBody {
  rating_id?: number;
  name: string;
  city: string;
  players: TeamPlayer[];
  multifest_venue_id?: number;
  flags?: string[];
  client_id?: string;
  venue_number?: number;
}

// A team is a server id, or the client id of a team made on this device.
export type TeamRef = number | string;

// TeamKey is a team as the OD document knows it: by name, city and number at
// its venue, as they were when a change was made.
export interface TeamKey {
  name: string;
  city: string;
  number: number;
}

// Each change carries an id once queued, which a document it was laid over
// remembers (projectODState).
export type RosterOp = {id?: string} & (
  | {kind: "create"; cid: string; venue: number; body: TeamBody}
  | {kind: "save"; ref: TeamRef; venue: number; was: TeamKey; body: TeamBody}
  | {kind: "remove"; ref: TeamRef; venue: number; was: TeamKey}
  | {kind: "numbers"; venue: number; numbers: Array<{ref: TeamRef; number: number; was: TeamKey}>});

export interface Outbox {
  ops: RosterOp[];
  // The server's id of each team made on this device, by client id.
  ids: Record<string, number>;
  // What the server refused, as it said it, and the team it was about, for
  // the page to show once.
  refused: Refusal[];
}

export interface Refusal {
  team: string;
  message: string;
}

// VenueTeam is one row of the venue's teams.
export interface VenueTeam {
  ref: TeamRef;
  name: string;
  city: string;
  players: number;
  number: number;
}

function storage(): Storage | null {
  try {
    return window.localStorage;
  } catch (_) {
    return null;
  }
}

function readJSON<T>(key: string): T | null {
  try {
    const raw = storage()?.getItem(key);
    return raw ? JSON.parse(raw) as T : null;
  } catch (_) {
    return null;
  }
}

function writeJSON(key: string, value: unknown): void {
  try {
    storage()?.setItem(key, JSON.stringify(value));
  } catch (_) {
    // A full storage loses the copy, never the page.
  }
}

export function readOutbox(festID: number): Outbox {
  const saved = readJSON<Partial<Outbox>>(`${OUTBOX_PREFIX}:${festID}`);
  return {ops: saved?.ops || [], ids: saved?.ids || {}, refused: saved?.refused || []};
}

export function writeOutbox(festID: number, box: Outbox): void {
  const key = `${OUTBOX_PREFIX}:${festID}`;
  if (box.ops.length || box.refused.length || Object.keys(box.ids).length) writeJSON(key, box);
  else storage()?.removeItem(key);
}

export function newClientID(): string {
  try {
    return crypto.randomUUID();
  } catch (_) {
    return `c${Date.now()}-${Math.random()}`;
  }
}

function sameRef(a: TeamRef, b: TeamRef): boolean {
  return String(a) === String(b);
}

// enqueue adds a change to the outbox. A change to a team made on this device
// that the server has not seen yet folds into its creation, and a newer set of
// a venue's numbers replaces an older one.
export function enqueue(box: Outbox, change: RosterOp): Outbox {
  const op = {...change, id: change.id || newClientID()} as RosterOp;
  const ops = box.ops.slice();
  const pendingCreate = (ref: TeamRef) => ops.findIndex((o) => o.kind === "create" && sameRef(o.cid, ref));
  if (op.kind === "save") {
    const at = pendingCreate(op.ref);
    if (at >= 0) {
      const create = ops[at] as Extract<RosterOp, {kind: "create"}>;
      ops[at] = {...create, body: {...op.body, client_id: create.cid, venue_number: create.body.venue_number}};
      return {...box, ops};
    }
  }
  if (op.kind === "remove") {
    const at = pendingCreate(op.ref);
    if (at >= 0) {
      return {...box, ops: ops.filter((o, i) => i !== at && !mentions(o, op.ref)).map((o) => withoutRef(o, op.ref))};
    }
  }
  if (op.kind === "numbers") {
    return {...box, ops: [...ops.filter((o) => !(o.kind === "numbers" && o.venue === op.venue)), op]};
  }
  return {...box, ops: [...ops, op]};
}

function mentions(op: RosterOp, ref: TeamRef): boolean {
  return (op.kind === "save" || op.kind === "remove") && sameRef(op.ref, ref);
}

function withoutRef(op: RosterOp, ref: TeamRef): RosterOp {
  return op.kind === "numbers" ? {...op, numbers: op.numbers.filter((n) => !sameRef(n.ref, ref))} : op;
}

// projectVenueTeams is the venue's teams as the host sees them: the
// server's last copy with the outbox's changes laid over it.
export function projectVenueTeams(teams: VenueTeam[], box: Outbox, venue: number): VenueTeam[] {
  let out = teams.map((t) => ({...t}));
  const find = (ref: TeamRef) => out.find((t) => sameRef(t.ref, ref) || (typeof ref === "string" && box.ids[ref] === t.ref));
  for (const op of box.ops.filter((o) => o.venue === venue)) {
    if (op.kind === "remove") out = out.filter((t) => t !== find(op.ref));
    else applyToVenue(out, op, find);
  }
  return out;
}

function applyToVenue(teams: VenueTeam[], op: RosterOp, find: (ref: TeamRef) => VenueTeam | undefined): void {
  if (op.kind === "create" && !find(op.cid)) {
    teams.push({ref: op.cid, name: op.body.name, city: op.body.city, players: op.body.players.length, number: op.body.venue_number || 0});
  }
  if (op.kind === "save") {
    const team = find(op.ref);
    if (team) Object.assign(team, {name: op.body.name, city: op.body.city, players: op.body.players.length});
  }
  if (op.kind === "numbers") {
    for (const team of teams) team.number = 0;
    for (const n of op.numbers) {
      const team = find(n.ref);
      if (team) team.number = n.number;
    }
  }
}

// pendingCount is how many changes of a venue still wait for the server.
export function pendingCount(box: Outbox, venue?: number): number {
  return box.ops.filter((op) => venue === undefined || op.venue === venue).length;
}

// ODTeamLike is the part of an OD document's team the outbox changes.
interface ODTeamLike {
  name: string;
  city: string;
  number?: number;
  venue?: number;
  venueNumber?: number;
}

interface ODStateLike {
  teams?: ODTeamLike[];
  venues?: Record<string, {entries?: number[][]}>;
  // The outbox's changes this document already shows.
  offlineApplied?: string[];
}

// projectODState lays the outbox over an OD document: the teams made, renamed
// and taken off on the device, and the numbers it gave, with the venue's
// answers renumbered the way the server will renumber them once the outbox
// reaches it. A document remembers the changes laid over it, so one built on
// it from a live delta does not take them twice.
export function projectODState(state: unknown, box: Outbox): unknown {
  const doc = state as ODStateLike | null;
  if (!doc || !Array.isArray(doc.teams) || !doc.venues) return state;
  const applied = new Set(doc.offlineApplied || []);
  const fresh = box.ops.filter((op) => !applied.has(op.id || ""));
  if (fresh.length === 0) return state;
  const out: ODStateLike = {...doc, teams: doc.teams.map((t) => ({...t})), venues: {...doc.venues},
    offlineApplied: box.ops.map((op) => op.id || "")};
  for (const op of fresh) applyToOD(out, op);
  return out;
}

function sameTeam(team: ODTeamLike, key: {name: string; city: string}): boolean {
  return team.name === key.name && (team.city || "") === (key.city || "");
}

function applyToOD(doc: ODStateLike, op: RosterOp): void {
  const teams = doc.teams!;
  const find = (key: {name: string; city: string}) => teams.find((t) => t.venue === op.venue && sameTeam(t, key));
  if (op.kind === "create" && !find(op.body)) {
    teams.push({name: op.body.name, city: op.body.city, number: 0, venue: op.venue, venueNumber: op.body.venue_number || 0});
  }
  const known = op.kind === "save" || op.kind === "remove" ? find(op.was) : undefined;
  if (known && op.kind === "save") Object.assign(known, {name: op.body.name, city: op.body.city});
  if (known && op.kind === "remove") teams.splice(teams.indexOf(known), 1);
  if (op.kind === "numbers") renumberOD(doc, op);
}

function renumberOD(doc: ODStateLike, op: Extract<RosterOp, {kind: "numbers"}>): void {
  for (const team of doc.teams!.filter((t) => t.venue === op.venue)) {
    team.venueNumber = op.numbers.find((n) => sameTeam(team, n.was))?.number || 0;
  }
  const sheet = doc.venues![String(op.venue)];
  if (sheet?.entries) doc.venues![String(op.venue)] = {...sheet, entries: remapNumbers(sheet.entries, numbersRemap(op)) as number[][]};
}

// numbersRemap maps each old number to the new one, for the teams a set of
// numbers moved.
export function numbersRemap(op: Extract<RosterOp, {kind: "numbers"}>): Map<number, number> {
  const remap = new Map<number, number>();
  for (const n of op.numbers) {
    if (n.was.number > 0 && n.was.number !== n.number) remap.set(n.was.number, n.number);
  }
  return remap;
}

// remapNumbers renumbers every number in an answer column, or in a column
// of columns.
export function remapNumbers(value: unknown, remap: Map<number, number>): unknown {
  if (typeof value === "number") return remap.get(value) ?? value;
  if (Array.isArray(value)) return value.map((v) => remapNumbers(v, remap));
  return value;
}

// remapPendingAnswers renumbers the OD answers a game page keeps unsent for a
// venue, so they say what they meant once the server renumbers its own.
export function remapPendingAnswers(gameID: number, venue: number, remap: Map<number, number>): void {
  if (remap.size === 0) return;
  const key = `${PENDING_PREFIX}:game-state:${gameID}`;
  const ops = readJSON<Array<{path: PatchPath; value: unknown}>>(key);
  if (!Array.isArray(ops)) return;
  const sheet = (path: PatchPath) => path[0] === "venues" && String(path[1]) === String(venue) && path[2] === "entries";
  writeJSON(key, ops.map((op) => sheet(op.path) ? {...op, value: remapNumbers(op.value, remap)} : op));
}

// unsentEverywhere says whether this device keeps changes the server has not
// seen, in any fest: the logout asks first.
export function unsentEverywhere(): boolean {
  const store = storage();
  if (!store) return false;
  for (let i = 0; i < store.length; i++) {
    const key = store.key(i) || "";
    if (key.startsWith(`${PENDING_PREFIX}:`) || (key.startsWith(`${OUTBOX_PREFIX}:`) && (readJSON<Outbox>(key)?.ops.length || 0) > 0)) return true;
  }
  return false;
}

// ---- sending ----

export interface FlushResult {
  // Whether the outbox is empty now.
  empty: boolean;
  // Whether the network failed, as opposed to the server refusing.
  offline: boolean;
}

interface Wire {
  url: string;
  method: string;
  body?: unknown;
}

function wireOf(api: string, op: RosterOp, ids: Record<string, number>): Wire | null {
  const id = (ref: TeamRef) => typeof ref === "number" ? ref : ids[ref];
  switch (op.kind) {
    case "create":
      return {url: `${api}/teams`, method: "POST", body: {...op.body, client_id: op.cid, multifest_venue_id: op.venue}};
    case "save":
      return id(op.ref) ? {url: `${api}/teams/${id(op.ref)}`, method: "PUT", body: op.body} : null;
    case "remove":
      return id(op.ref) ? {url: `${api}/teams/${id(op.ref)}`, method: "DELETE"} : null;
    case "numbers": {
      const assignments = op.numbers.filter((n) => id(n.ref)).map((n) => ({team_id: id(n.ref), number: n.number}));
      return {url: `${api}/numbers/assign?venue=${op.venue}`, method: "POST", body: {assignments}};
    }
  }
}

function labelOf(op: RosterOp): string {
  if (op.kind === "create") return op.body.name;
  if (op.kind === "save") return op.body.name;
  if (op.kind === "remove") return op.was.name;
  return "";
}

async function sendOne(wire: Wire): Promise<{status: number; text: string}> {
  const response = await fetch(wire.url, {
    method: wire.method,
    headers: wire.body === undefined ? undefined : {"Content-Type": "application/json"},
    body: wire.body === undefined ? undefined : JSON.stringify(wire.body),
  });
  return {status: response.status, text: (await response.text()).trim()};
}

let flushing: Promise<FlushResult> | null = null;

type Sent = {status: number; text: string} | "offline";

// sendHead sends the outbox's first change: what the server answered, or
// "offline" when it could not be asked. A change that needs a team the server
// never made is not sent and counts as done.
async function sendHead(api: string, op: RosterOp, ids: Record<string, number>): Promise<Sent> {
  const wire = wireOf(api, op, ids);
  if (!wire) return {status: 0, text: ""};
  try {
    const sent = await sendOne(wire);
    return sent.status >= SERVER_ERROR_STATUS ? "offline" : sent;
  } catch (_) {
    return "offline";
  }
}

// settle takes a sent change off the outbox. The head may have changed while
// the change was on its way: a creation took a later edit, which then goes
// after it as a change of the team the server made, or a newer set of numbers
// replaced the one sent, which is sent next.
function settle(now: Outbox, op: RosterOp, sent: {status: number; text: string}): void {
  const head = now.ops[0];
  const same = op.kind === "create" ? head?.kind === "create" && head.cid === op.cid : JSON.stringify(head) === JSON.stringify(op);
  if (!head || !same) return;
  now.ops.shift();
  if (sent.status >= HTTP_CLIENT_ERROR) {
    now.refused.push({team: labelOf(op), message: sent.text});
    if (op.kind === "create") now.ops = now.ops.filter((o) => !mentions(o, op.cid)).map((o) => withoutRef(o, op.cid));
    return;
  }
  if (op.kind !== "create" || head.kind !== "create") return;
  try {
    now.ids[op.cid] = (JSON.parse(sent.text) as {id: number}).id;
  } catch (_) {
    // An answer without the team's id leaves its later changes unsent.
  }
  if (JSON.stringify(head.body) !== JSON.stringify(op.body)) {
    now.ops.unshift({id: newClientID(), kind: "save", ref: op.cid, venue: op.venue,
      was: {name: op.body.name, city: op.body.city, number: op.body.venue_number || 0}, body: head.body});
  }
}

// flushOutbox sends the fest's changes in order, one at a time, until the
// network fails or the outbox is empty. A change the server refuses is
// dropped and its reason kept for the page to show; the changes that need a
// team the server refused to make go with it.
export function flushOutbox(festID: number, api: string): Promise<FlushResult> {
  if (flushing) return flushing;
  const run = async (): Promise<FlushResult> => {
    for (let box = readOutbox(festID); box.ops.length; box = readOutbox(festID)) {
      const op = box.ops[0];
      const sent = await sendHead(api, op, box.ids);
      if (sent === "offline") return {empty: false, offline: true};
      const now = readOutbox(festID);
      settle(now, op, sent);
      // Once all is sent, the page draws from the server's ids again.
      if (now.ops.length === 0) now.ids = {};
      writeOutbox(festID, now);
    }
    return {empty: true, offline: false};
  };
  const locks = (navigator as Navigator & {locks?: LockManager}).locks;
  flushing = (locks ? locks.request(`${OUTBOX_PREFIX}:${festID}`, run) : run()).finally(() => {
    flushing = null;
  });
  return flushing;
}

// ---- the local copy of the teams, for the editor without a network ----

export interface TeamCopy {
  // The editor's answer for a new team: the fest's people to suggest, the
  // venues and divisions to offer.
  fresh: unknown;
  // Each team of the venue as the editor opens it, by id.
  teams: Record<string, unknown>;
  at: number;
}

export function readTeamCopy(festID: number, venue: number): TeamCopy | null {
  return readJSON<TeamCopy>(`${COPY_PREFIX}:${festID}:${venue}`);
}

export function writeTeamCopy(festID: number, venue: number, copy: TeamCopy): void {
  writeJSON(`${COPY_PREFIX}:${festID}:${venue}`, copy);
}

// ---- the service worker ----

// prepareOffline asks the service worker to save these pages and everything
// they load, and answers the ones it could not save.
export async function prepareOffline(urls: string[]): Promise<string[]> {
  const registration = await navigator.serviceWorker.register("/sw.js");
  await navigator.serviceWorker.ready;
  const worker = registration.active || navigator.serviceWorker.controller;
  if (!worker) return urls;
  try {
    await navigator.storage?.persist?.();
  } catch (_) {
    // A browser that will not promise to keep the copy still keeps it a while.
  }
  return await new Promise<string[]>((resolve) => {
    const channel = new MessageChannel();
    channel.port1.onmessage = (event) => resolve((event.data as {failed?: string[]})?.failed || []);
    worker.postMessage({type: "prepare", urls}, [channel.port2]);
  });
}

// keepWorker updates the worker of a page that was prepared before, so a
// deploy reaches it.
export function keepWorker(): void {
  if (!("serviceWorker" in navigator)) return;
  void navigator.serviceWorker.getRegistration("/").then((registration) => registration?.update()).catch(() => undefined);
}

// mountOfflineBar shows, at the top of a game page, that the network is gone
// or that changes wait for it, and redraws when either changes.
export function mountOfflineBar(waiting: () => number, texts: {offline: (n: number) => string; sending: (n: number) => string}): () => void {
  const bar = document.createElement("div");
  bar.className = "offline-bar";
  bar.hidden = true;
  document.body.prepend(bar);
  const draw = () => {
    const n = waiting();
    let text = "";
    if (!navigator.onLine) text = texts.offline(n);
    else if (n > 0) text = texts.sending(n);
    bar.textContent = text;
    bar.hidden = !text;
  };
  window.addEventListener("online", draw);
  window.addEventListener("offline", draw);
  window.setInterval(draw, BAR_REDRAW_MS);
  draw();
  return draw;
}
