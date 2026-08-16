import {test} from "node:test";
import assert from "node:assert/strict";
import {computeBrainPlayerStats} from "./dist/brain-stats.js";

// Индивидуальная статистика: попытки, верно, неверно per (player, team),
// counted over the regular questions — перестрелки stay out, as the sheet
// leaves them out. Sorted by верно, then попытки, then name.
test("computeBrainPlayerStats aggregates buzzes over regular questions", () => {
  const bouts = [{
    teams: ["Рыб'ending", "Постпопс"],
    regular: 2,
    rows: [
      [{player: "Виктория Корнеева", mark: "right"}, {player: "Олег Сериков", mark: "wrong"}],
      [{player: "Виктория Корнеева", mark: "wrong"}, {player: "", mark: ""}],
      // перестрелка — not counted
      [{player: "Виктория Корнеева", mark: "right"}, {player: "Олег Сериков", mark: "right"}],
    ],
  }];
  const rows = computeBrainPlayerStats(bouts);
  assert.deepEqual(rows, [
    {player: "Виктория Корнеева", team: "Рыб'ending", attempts: 2, right: 1, wrong: 1},
    {player: "Олег Сериков", team: "Постпопс", attempts: 1, right: 0, wrong: 1},
  ]);
});
