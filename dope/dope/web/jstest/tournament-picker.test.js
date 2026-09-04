import {assertEquals} from "jsr:@std/assert";
import {allLabel, order, shows} from "./dist/tournament-picker.js";

const card = (id, difficulty, teams, kind = "sync", keep = true) => ({id, difficulty, teams, kind, keep});

const controls = (over = {}) => ({kind: "all", from: null, to: null, sort: "difficulty-asc", ...over});

Deno.test("the type dropdown keeps one kind or all of them", () => {
  const sync = card("1", 3, 10, "sync");
  const async = card("2", 3, 10, "async");
  assertEquals(shows(sync, controls({kind: "sync"})), true);
  assertEquals(shows(async, controls({kind: "sync"})), false);
  assertEquals(shows(async, controls({kind: "async"})), true);
  assertEquals(shows(async, controls()), true);
});

Deno.test("a difficulty bound keeps what is inside it, and everything unforecast", () => {
  assertEquals(shows(card("1", 3), controls({from: 4})), false);
  assertEquals(shows(card("1", 5), controls({from: 4})), true);
  assertEquals(shows(card("1", 5), controls({to: 4})), false);
  assertEquals(shows(card("1", 4), controls({from: 4, to: 4})), true);
  // No forecast is not a low one: a bound must not hide it.
  assertEquals(shows(card("1", 0), controls({from: 4, to: 5})), true);
});

Deno.test("the order runs by the chosen key, unforecast last", () => {
  const cards = [card("hard", 6, 10), card("none", 0, 99), card("easy", 3, 50)];
  assertEquals(order(cards, controls()).map((c) => c.id), ["easy", "hard", "none"]);
  assertEquals(
    order(cards, controls({sort: "difficulty-desc"})).map((c) => c.id),
    ["hard", "easy", "none"],
  );
  assertEquals(
    order(cards, controls({sort: "teams-desc"})).map((c) => c.id),
    ["none", "easy", "hard"],
  );
});

Deno.test("a card ticked off sinks below every card still in play", () => {
  const cards = [card("out", 1, 90, "sync", false), card("in", 8, 1)];
  assertEquals(order(cards, controls()).map((c) => c.id), ["in", "out"]);
  assertEquals(order(cards, controls({sort: "teams-desc"})).map((c) => c.id), ["in", "out"]);
});

Deno.test("the one select-all button offers the move that changes something", () => {
  assertEquals(allLabel([card("1", 3, 10), card("2", 3, 10, "sync", false)]), "none");
  assertEquals(allLabel([card("1", 3, 10, "sync", false)]), "all");
  // Nothing on screen: clearing an empty list is a no-op either way.
  assertEquals(allLabel([]), "all");
});
