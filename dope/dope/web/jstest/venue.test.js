import {test} from "node:test";
import assert from "node:assert/strict";

globalThis.window = {};
globalThis.document = {createElement: () => ({}), activeElement: null};

const {boutWhere, nameVenuesByRoom, nextVenueNumber, venueDeletable, venuesShort} = await import("./dist/venue.js");

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

// A fest that names venues by room shows the room where the number was.
test("a bout's head names its venue by number, or by room when the fest says so", () => {
  const venue = {number: 1, title: "ауд. 308"};
  assert.equal(boutWhere(venue, "18:00"), "пл. 1 18:00, ауд. 308");
  nameVenuesByRoom(true);
  try {
    assert.equal(boutWhere(venue, "18:00"), "ауд. 308 18:00");
    assert.equal(boutWhere({number: 2, title: ""}, ""), "пл. 2");
    assert.equal(venuesShort([venue, {number: 2, title: "ауд. 310"}]), "ауд. 308, ауд. 310");
  } finally {
    nameVenuesByRoom(false);
  }
  assert.equal(venuesShort([{number: 2, title: "Б"}, venue]), "пл. 1, 2");
});
