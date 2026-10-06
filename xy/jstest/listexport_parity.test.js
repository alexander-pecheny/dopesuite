// The browser's copy of the List assembly against the Go reference: every case
// in internal/listexport/testdata/cases.json is Cards in and what they export
// as out, written by `go test ./internal/listexport -update`.
import { test } from "node:test";
import assert from "node:assert/strict";
import { xyListExport } from "../web/assets/static/dist/listexport.js";
import cases from "../internal/listexport/testdata/cases.json" with { type: "json" };

test("the corpus covers the 4s, the game and the .hndt", () => {
  assert.ok(cases.length > 20);
  assert.ok(cases.some((c) => c.game === "si"));
  assert.ok(cases.some((c) => c.hndt.startsWith("///preamble")));
});

for (const c of cases) {
  test(`listexport: ${c.name}`, () => {
    assert.equal(xyListExport.exportSource(c.cards), c.source, "source");
    assert.equal(xyListExport.exportGame(c.cards), c.game, "game");
    assert.equal(xyListExport.hndtOf(c.cards).source, c.hndt, "hndt");
  });
}
