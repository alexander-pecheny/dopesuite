// Arranging an Edited Image's boxes (ADR-0024): Figma's align and distribute,
// and snapping a dragged box to the grid.
import { test } from "node:test";
import assert from "node:assert/strict";
import { align, bounds, distribute, snapPoint, snapShift } from "../web/assets/static/dist/xyimagearrange.js";

const boxes = () => [{ x: 10, y: 5, w: 20, h: 10 }, { x: 40, y: 30, w: 10, h: 20 }];

test("bounds holds every box", () => {
  assert.deepEqual(bounds(boxes()), { x: 10, y: 5, w: 40, h: 45 });
});

test("several boxes align to their common bounds", () => {
  const rs = boxes();
  align(rs, "left", bounds(rs));
  assert.deepEqual(rs.map((r) => r.x), [10, 10]);
  align(rs, "bottom", bounds(rs));
  assert.deepEqual(rs.map((r) => r.y + r.h), [50, 50]);
  align(rs, "hcenter", { x: 0, y: 0, w: 100, h: 100 });
  assert.deepEqual(rs.map((r) => r.x + r.w / 2), [50, 50]);
});

test("one box aligns to the frame it is given", () => {
  const r = { x: 3, y: 4, w: 10, h: 10 };
  align([r], "right", { x: 0, y: 0, w: 100, h: 50 });
  align([r], "vmiddle", { x: 0, y: 0, w: 100, h: 50 });
  assert.deepEqual(r, { x: 90, y: 20, w: 10, h: 10 });
});

test("distribute evens the gaps and keeps the outermost boxes", () => {
  const rs = [{ x: 0, y: 0, w: 10, h: 1 }, { x: 70, y: 0, w: 30, h: 1 }, { x: 12, y: 0, w: 20, h: 1 }];
  distribute(rs, "x");
  assert.deepEqual(rs.map((r) => r.x), [0, 70, 30]);
});

test("a dragged box snaps by its nearer edge", () => {
  assert.equal(snapPoint(7.4, 2, 5), 7);
  // Left edge 11 is 1 from 10; right edge 11+8=19 is 1 from 20: the left wins a tie.
  assert.equal(snapShift(11, 8, 0, 5), -1);
  // Left edge 12 is 2 from 10; right edge 12+7.5=19.5 is 0.5 from 20.
  assert.equal(snapShift(12, 7.5, 0, 5), 0.5);
});
