import {test} from "node:test";
import assert from "node:assert/strict";
import {normalizeScreenSettings, packRows, planScreen, teamFlag} from "./dist/screen-board.js";

const rows = (groups) => groups.map((group, i) => ({i, group}));
const metrics = {headH: 10, rowH: 10, gapH: 4, colW: 100, gapPx: 0, availW: 300, availH: 100};

test("normalizeScreenSettings clamps and falls back to the defaults", () => {
  assert.deepEqual(normalizeScreenSettings(null), {bg: "#ffffff", fg: "#000000", muted: "#5f6b7a", fontScale: 1, columns: 0, showCity: true, showCountry: false});
  const s = normalizeScreenSettings({bg: "#111", fontScale: 9, columns: 2.6, showCity: false, muted: 3});
  assert.equal(s.bg, "#111");
  assert.equal(s.fontScale, 2);
  assert.equal(s.columns, 3);
  assert.equal(s.showCity, false);
  assert.equal(s.muted, "#5f6b7a");
});

test("teamFlag: flag by city, globe for сборная, nothing for an unknown town", () => {
  assert.equal(teamFlag("Борский корабел", " Нижний Новгород "), "🇷🇺");
  assert.equal(teamFlag("Сборная мира", "Париж"), "🌍");
  assert.equal(teamFlag("X", "Гадюкино"), "");
});

test("packRows breaks columns by height and charges a gap between groups", () => {
  // groups 0,0,1,2: gap before the third and fourth rows only
  const cols = packRows(rows([0, 0, 1, 2]), 10, 4, 20);
  assert.deepEqual(cols.map((c) => c.map((r) => r.i)), [[0, 1], [2], [3]]);
  assert.deepEqual(packRows(rows([0, 1]), 10, 4, 100).length, 1);
  assert.deepEqual(packRows([], 10, 4, 100), []);
});

test("planScreen auto mode picks the column count with the largest zoom", () => {
  // 6 rows, one per place; one column is 10+6*10+5*4=90 tall → zoom 1.11 vs width-bound 3 cols of 2 rows (34 tall) → zoom 1 by width
  const plan = planScreen(rows([0, 1, 2, 3, 4, 5]), metrics, {columns: 0, fontScale: 1});
  assert.equal(plan.columns.length, 2);
  assert.deepEqual(plan.columns.map((c) => c.length), [3, 3]);
  assert.ok(Math.abs(plan.zoom - Math.min(100 / 48, 300 / 200) * 0.985) < 1e-9);
  assert.equal(plan.teamCol, null);
});

test("planScreen honours a forced column count and spends leftover width on the team column", () => {
  const plan = planScreen(rows([0, 1, 2, 3]), {...metrics, availW: 1000}, {columns: 1, fontScale: 0.5});
  assert.equal(plan.columns.length, 1);
  // height-bound: the column is ~62px tall, so zoom ≈ 100/62 (the search stops within half a px)
  assert.ok(plan.teamCol > 160 && plan.teamCol <= 640);
  assert.ok(Math.abs(plan.zoom / ((100 / 62) * 0.985 * 0.5) - 1) < 0.02);
  assert.equal(planScreen([], metrics, {columns: 0, fontScale: 1}), null);
});
