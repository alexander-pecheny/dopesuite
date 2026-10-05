import {assertEquals} from "jsr:@std/assert";
import * as ek from "./dist/ek-protocol.js";

// A bout of two teams, slot 0 is team 11 with two people, slot 1 is team 12.
const view = {
  participants: [
    {id: 11, roster: [{id: 101, name: "Анна"}, {id: 102, name: "Борис"}]},
    {id: 12, roster: [{id: 201, name: "Вера"}]},
  ],
};

Deno.test("a mark is tracked by slot on the view and sent by team id", () => {
  const payload = {team: 0, theme: 3, answer: 2, mark: "right"};
  const path = ek.opPath(payload);
  assertEquals(path, ["participants", 0, "themes", 3, "answers", 2]);
  assertEquals(ek.blobOp({path, value: ek.opValue(payload)}, view),
    {path: ["participants", "11", "themes", 3, "answers", 2], value: "right"});
});

Deno.test("a shootout mark goes to the shootout themes", () => {
  const payload = {team: 1, theme: 0, answer: 0, mark: "wrong", shootout: true};
  assertEquals(ek.blobOp({path: ek.opPath(payload), value: "wrong"}, view),
    {path: ["participants", "12", "shootoutThemes", 0, "answers", 0], value: "wrong"});
});

Deno.test("a place is a pin, and an empty place clears it", () => {
  const path = ek.opPath({team: 1, place: 2});
  assertEquals(ek.blobOp({path, value: 2}, view), {path: ["participants", "12", "pin"], value: 2});
  assertEquals(ek.blobOp({path, value: 0}, view), {op: "remove", path: ["participants", "12", "pin"]});
});

Deno.test("a seating is sent as the players' ids", () => {
  const payload = {team: 0, theme: 1, players: ["Борис", "Анна"]};
  assertEquals(ek.blobOp({path: ek.opPath(payload), value: ek.opValue(payload)}, view),
    {path: ["participants", "11", "themes", 1, "players"], value: [102, 101]});
});

Deno.test("an op the view can no longer resolve is dropped, not retried", () => {
  assertEquals(ek.blobOp({path: ["participants", 5, "themes", 0, "answers", 0], value: "right"}, view), null);
  const seating = {team: 0, theme: 1, players: ["Кто-то"]};
  assertEquals(ek.blobOp({path: ek.opPath(seating), value: ek.opValue(seating)}, view), null);
});

Deno.test("a payload that is no cell is not tracked", () => {
  assertEquals(ek.opPath({team: 0}), null);
});
