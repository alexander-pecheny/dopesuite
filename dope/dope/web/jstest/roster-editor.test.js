import { assertEquals } from "jsr:@std/assert";
import {
  emptyPlayer,
  fullName,
  MAX_ROSTER,
  parseRoster,
  rosterWarning,
  serializeRoster,
  setFlag,
  slugify,
  suggestLabel,
} from "./dist/roster-editor.js";

Deno.test("parseRoster trims and defaults", () => {
  const players = parseRoster(
    '[{"player_id":"24850","surname":" Печеный ","name":"Александр","captain":true},{},null]',
  );
  assertEquals(players.length, 3);
  assertEquals(players[0].player_id, 24850);
  assertEquals(players[0].surname, "Печеный");
  assertEquals(players[0].patronymic, "");
  // A roster stored before the flag was a field carries a captain instead.
  assertEquals(players[0].flag, "К");
  assertEquals(players[1], emptyPlayer());
});

Deno.test("parseRoster survives rubbish", () => {
  assertEquals(parseRoster(""), []);
  assertEquals(parseRoster("{"), []);
  assertEquals(parseRoster('{"a":1}'), []);
});

Deno.test("serializeRoster drops the nameless rows the form leaves behind", () => {
  const players = [
    { player_id: 1, surname: "А", name: "Б", patronymic: "", flag: "К" },
    emptyPlayer(),
  ];
  assertEquals(JSON.parse(serializeRoster(players)).length, 1);
});

Deno.test("suggestLabel names the player, their id and the games they are known by", () => {
  assertEquals(
    suggestLabel({ player_id: 24850, surname: "Печеный", name: "Александр", patronymic: "Павлович", flag: "", games: 412 }),
    "Печеный Александр Павлович (24850) · 412 игр",
  );
  assertEquals(
    suggestLabel({ player_id: 24850, surname: "Печеный", name: "Александр", patronymic: "Павлович", flag: "" }),
    "Печеный Александр Павлович (24850)",
  );
  assertEquals(
    suggestLabel({ player_id: 0, surname: "Новый", name: "Игрок", patronymic: "", flag: "" }),
    "Новый Игрок",
  );
  assertEquals(fullName(emptyPlayer()), "");
});

// A team has one captain or none: naming a second sends the first back to
// whatever the base roster says of them.
Deno.test("setFlag keeps at most one captain", () => {
  const base = () => "Б";
  const players = [emptyPlayer(), emptyPlayer(), emptyPlayer()];
  const first = setFlag(players, 0, "К", base);
  assertEquals(first.map((p) => p.flag), ["К", "", ""]);
  const second = setFlag(first, 2, "К", base);
  assertEquals(second.map((p) => p.flag), ["Б", "", "К"]);
  // Any other flag leaves the captain where they are.
  assertEquals(setFlag(second, 1, "Л", base).map((p) => p.flag), ["Б", "Л", "К"]);
});

// A captain is optional; a seventh player is not.
Deno.test("rosterWarning warns above six and about nothing else", () => {
  const named = (i) => ({ player_id: i, surname: `И${i}`, name: "И", patronymic: "", flag: "Б" });
  assertEquals(rosterWarning([emptyPlayer()]), "");
  assertEquals(rosterWarning([named(1)]), "");
  const seven = [1, 2, 3, 4, 5, 6, 7].map(named);
  assertEquals(rosterWarning(seven), `В составе больше ${MAX_ROSTER} игроков.`);
});

Deno.test("slugify derives a URL from a Russian venue name", () => {
  assertEquals(slugify("Санкт-Петербург / Трубников Артём"), "sankt-peterburg-trubnikov-artem");
  assertEquals(slugify("Тбилиси"), "tbilisi");
  assertEquals(slugify("Ёлки-палки 2026"), "elki-palki-2026");
  // Nothing latin to build a slug from, and a slug of digits alone is refused
  // by the server anyway.
  assertEquals(slugify("2026"), "");
  assertEquals(slugify("!!!"), "");
});

