import { assertEquals } from "jsr:@std/assert";
import {
  emptyPlayer,
  fullName,
  MAX_ROSTER,
  parseRoster,
  rosterWarning,
  serializeRoster,
  nextTeamName,
  setCaptain,
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
  assertEquals(players[0].captain, true);
  assertEquals(players[1], emptyPlayer());
});

Deno.test("parseRoster survives rubbish", () => {
  assertEquals(parseRoster(""), []);
  assertEquals(parseRoster("{"), []);
  assertEquals(parseRoster('{"a":1}'), []);
});

Deno.test("serializeRoster drops the nameless rows the form leaves behind", () => {
  const players = [
    { player_id: 1, surname: "А", name: "Б", patronymic: "", captain: true },
    emptyPlayer(),
  ];
  assertEquals(JSON.parse(serializeRoster(players)).length, 1);
});

Deno.test("suggestLabel names the player, their id and the games they are known by", () => {
  assertEquals(
    suggestLabel({ player_id: 24850, surname: "Печеный", name: "Александр", patronymic: "Павлович", captain: false, games: 412 }),
    "Печеный Александр Павлович (24850) · 412 игр",
  );
  assertEquals(
    suggestLabel({ player_id: 24850, surname: "Печеный", name: "Александр", patronymic: "Павлович", captain: false }),
    "Печеный Александр Павлович (24850)",
  );
  assertEquals(
    suggestLabel({ player_id: 0, surname: "Новый", name: "Игрок", patronymic: "", captain: false }),
    "Новый Игрок",
  );
  assertEquals(fullName(emptyPlayer()), "");
});

Deno.test("setCaptain keeps exactly one", () => {
  const players = [emptyPlayer(), emptyPlayer(), emptyPlayer()];
  const first = setCaptain(players, 0);
  assertEquals(first.map((p) => p.captain), [true, false, false]);
  const second = setCaptain(first, 2);
  assertEquals(second.map((p) => p.captain), [false, false, true]);
});

Deno.test("rosterWarning asks for a captain and warns above six", () => {
  const named = (i) => ({ player_id: i, surname: `И${i}`, name: "И", patronymic: "", captain: false });
  assertEquals(rosterWarning([emptyPlayer()]), "");
  assertEquals(rosterWarning([named(1)]), "Отметьте капитана.");
  assertEquals(rosterWarning(setCaptain([named(1)], 0)), "");
  const seven = setCaptain([1, 2, 3, 4, 5, 6, 7].map(named), 0);
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

// The rating id names the team, but a name typed by hand belongs to whoever
// typed it.
Deno.test("nextTeamName overwrites only what the id put there", () => {
  assertEquals(nextTeamName("", "", "Gay Guerrilla"), "Gay Guerrilla");
  assertEquals(nextTeamName("   ", "", "Gay Guerrilla"), "Gay Guerrilla");
  assertEquals(nextTeamName("Мантисса", "Мантисса", "Gay Guerrilla"), "Gay Guerrilla");
  assertEquals(nextTeamName("Наша команда", "", "Gay Guerrilla"), "Наша команда");
  assertEquals(nextTeamName("Наша команда", "Мантисса", "Gay Guerrilla"), "Наша команда");
  // An id buff does not know leaves the box alone rather than emptying it.
  assertEquals(nextTeamName("Наша команда", "", ""), "Наша команда");
  assertEquals(nextTeamName("", "", ""), "");
});
