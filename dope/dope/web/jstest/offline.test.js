import {test} from "node:test";
import assert from "node:assert/strict";

const store = new Map();
globalThis.window = {
  localStorage: {
    getItem: (k) => store.has(k) ? store.get(k) : null,
    setItem: (k, v) => store.set(k, String(v)),
    removeItem: (k) => store.delete(k),
  },
};

const {
  enqueue, flushOutbox, projectODState, projectVenueTeams, readOutbox, remapPendingAnswers, writeOutbox,
} = await import("./dist/offline.js");

const empty = () => ({ops: [], ids: {}, refused: []});
const alfa = {ref: 1, name: "Альфа", city: "Минск", players: 4, number: 1};
const beta = {ref: 2, name: "Бета", city: "Минск", players: 5, number: 2};
const body = (name, number) => ({name, city: "Минск", players: [{rating_id: 0, first_name: "Новый", last_name: name}], venue_number: number});

test("a change to a team the server has not seen folds into its creation", () => {
  let box = enqueue(empty(), {kind: "create", cid: "c1", venue: 7, body: body("Гамма", 3)});
  box = enqueue(box, {kind: "save", ref: "c1", venue: 7, was: {name: "Гамма", city: "Минск", number: 3}, body: body("Гамма-2")});
  assert.equal(box.ops.length, 1);
  assert.equal(box.ops[0].body.name, "Гамма-2");
  assert.equal(box.ops[0].body.venue_number, 3);
  box = enqueue(box, {kind: "numbers", venue: 7, numbers: [{ref: "c1", number: 4, was: {name: "Гамма-2", city: "Минск", number: 3}}]});
  box = enqueue(box, {kind: "remove", ref: "c1", venue: 7, was: {name: "Гамма-2", city: "Минск", number: 4}});
  assert.deepEqual(box.ops.map((op) => op.kind), ["numbers"]);
  assert.deepEqual(box.ops[0].numbers, []);
});

test("a newer set of a площадка's numbers replaces the older one, after the teams it names", () => {
  let box = enqueue(empty(), {kind: "numbers", venue: 7, numbers: []});
  box = enqueue(box, {kind: "create", cid: "c1", venue: 7, body: body("Гамма", 3)});
  box = enqueue(box, {kind: "numbers", venue: 7, numbers: [{ref: "c1", number: 5, was: {name: "Гамма", city: "Минск", number: 3}}]});
  assert.deepEqual(box.ops.map((op) => op.kind), ["create", "numbers"]);
});

test("the площадка's teams show what the device changed", () => {
  let box = enqueue(empty(), {kind: "create", cid: "c1", venue: 7, body: body("Гамма", 3)});
  box = enqueue(box, {kind: "save", ref: 2, venue: 7, was: {name: "Бета", city: "Минск", number: 2}, body: body("Бета-люкс")});
  box = enqueue(box, {kind: "numbers", venue: 7, numbers: [
    {ref: 1, number: 2, was: {name: "Альфа", city: "Минск", number: 1}},
    {ref: 2, number: 1, was: {name: "Бета-люкс", city: "Минск", number: 2}},
    {ref: "c1", number: 3, was: {name: "Гамма", city: "Минск", number: 3}},
  ]});
  box = enqueue(box, {kind: "create", cid: "c9", venue: 8, body: body("Чужая", 1)});
  const shown = projectVenueTeams([alfa, beta], box, 7);
  assert.deepEqual(shown.map((t) => [t.name, t.number, t.players]), [["Альфа", 2, 4], ["Бета-люкс", 1, 1], ["Гамма", 3, 1]]);
});

test("an ОД document takes the device's teams and numbers once, its answers renumbered", () => {
  const doc = {
    teams: [{name: "Альфа", city: "Минск", number: 11, venue: 7, venueNumber: 1}, {name: "Бета", city: "Минск", number: 12, venue: 7, venueNumber: 2}],
    venues: {"7": {entries: [[1, 2], [2, 0]], completed: [true, false]}},
  };
  let box = enqueue(empty(), {kind: "create", cid: "c1", venue: 7, body: body("Гамма", 3)});
  box = enqueue(box, {kind: "numbers", venue: 7, numbers: [
    {ref: 1, number: 2, was: {name: "Альфа", city: "Минск", number: 1}},
    {ref: 2, number: 1, was: {name: "Бета", city: "Минск", number: 2}},
    {ref: "c1", number: 3, was: {name: "Гамма", city: "Минск", number: 3}},
  ]});
  const once = projectODState(doc, box);
  assert.deepEqual(once.teams.map((t) => [t.name, t.venueNumber]), [["Альфа", 2], ["Бета", 1], ["Гамма", 3]]);
  assert.deepEqual(once.venues["7"].entries, [[2, 1], [1, 0]]);
  // A live delta builds on the document the page holds: the swap stays one.
  const twice = projectODState(JSON.parse(JSON.stringify(once)), box);
  assert.deepEqual(twice.venues["7"].entries, [[2, 1], [1, 0]]);
  assert.equal(twice.teams.length, 3);
  assert.deepEqual(doc.venues["7"].entries, [[1, 2], [2, 0]], "the server's copy is left as it was");
});

test("the answers a game page keeps unsent are renumbered with the площадка", () => {
  store.set("dope.pending.v2:game-state:71", JSON.stringify([
    {op: "set", path: ["venues", "7", "entries", 0, 1], value: 2, ts: 1},
    {op: "set", path: ["venues", "8", "entries", 0, 0], value: 2, ts: 1},
  ]));
  remapPendingAnswers(71, 7, new Map([[2, 5]]));
  const ops = JSON.parse(store.get("dope.pending.v2:game-state:71"));
  assert.deepEqual(ops.map((op) => op.value), [5, 2]);
});

function fakeFetch(answers) {
  const calls = [];
  globalThis.fetch = async (url, init) => {
    calls.push({url, method: init.method, body: init.body ? JSON.parse(init.body) : undefined});
    const next = answers.shift();
    if (next instanceof Error) throw next;
    return {status: next.status, text: async () => next.text ?? ""};
  };
  return calls;
}

test("the outbox goes in order, and a team made offline is named by the server's id after", async () => {
  globalThis.navigator = {};
  let box = enqueue(empty(), {kind: "create", cid: "c1", venue: 7, body: body("Гамма", 3)});
  box = enqueue(box, {kind: "numbers", venue: 7, numbers: [{ref: "c1", number: 4, was: {name: "Гамма", city: "Минск", number: 3}}]});
  writeOutbox(1, box);
  const calls = fakeFetch([{status: 200, text: JSON.stringify({id: 40})}, {status: 200, text: "[]"}]);
  const result = await flushOutbox(1, "/api/fest/1");
  assert.deepEqual(result, {empty: true, offline: false});
  assert.equal(calls[0].body.client_id, "c1");
  assert.equal(calls[0].body.multifest_venue_id, 7);
  assert.equal(calls[1].url, "/api/fest/1/numbers/assign?venue=7");
  assert.deepEqual(calls[1].body.assignments, [{team_id: 40, number: 4}]);
  assert.deepEqual(readOutbox(1), {ops: [], ids: {}, refused: []}, "a sent outbox keeps nothing");
});

test("without a network the outbox keeps everything, and a refusal takes the changes that need it", async () => {
  let box = enqueue(empty(), {kind: "create", cid: "c2", venue: 7, body: body("Дельта", 1)});
  box = enqueue(box, {kind: "save", ref: 1, venue: 7, was: {name: "Альфа", city: "Минск", number: 1}, body: body("Альфа")});
  writeOutbox(2, box);
  fakeFetch([new TypeError("offline")]);
  assert.deepEqual(await flushOutbox(2, "/api/fest/2"), {empty: false, offline: true});
  assert.equal(readOutbox(2).ops.length, 2);
  writeOutbox(2, enqueue(readOutbox(2), {kind: "save", ref: "c2", venue: 7, was: {name: "Дельта", city: "Минск", number: 1}, body: body("Дельта")}));
  fakeFetch([{status: 400, text: "Номер 1 на площадке уже у команды «Альфа»."}, {status: 200, text: "{}"}]);
  assert.deepEqual(await flushOutbox(2, "/api/fest/2"), {empty: true, offline: false});
  assert.deepEqual(readOutbox(2).refused, [{team: "Дельта", message: "Номер 1 на площадке уже у команды «Альфа»."}]);
});
