import {test} from "node:test";
import assert from "node:assert/strict";

globalThis.window = {};
globalThis.document = {createElement: () => ({}), activeElement: null};

const {nextVenueNumber, venueDeletable} = await import("./dist/venue.js");

test("a new venue takes the number after the fest's highest", () => {
  assert.equal(nextVenueNumber([]), 1);
  assert.equal(nextVenueNumber(null), 1);
  assert.equal(nextVenueNumber([{number: 2, title: "Актовый зал"}]), 3);
  assert.equal(nextVenueNumber([{number: 5, title: "А"}, {number: 1, title: "Б"}]), 6);
});

test("only a venue no bout plays at can be deleted", () => {
  assert.equal(venueDeletable({number: 1, title: "А"}), true);
  assert.equal(venueDeletable({number: 1, title: "А", bouts: 0}), true);
  assert.equal(venueDeletable({number: 1, title: "А", bouts: 3}), false);
});
