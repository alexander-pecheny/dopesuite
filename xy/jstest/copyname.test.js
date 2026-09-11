// copyname.test.js — what a clone is called before the reader renames it.
// "X (копия)" is the wrong prefill the second time round: two boards of one
// name, indistinguishable in the list. The suffix counts instead.
import { test } from "node:test";
import assert from "node:assert/strict";
import { installDOM } from "./dom.js";

installDOM([]);
const { xyApp } = await import("../web/assets/static/dist/app.js");
const { xySync } = await import("../web/assets/static/dist/sync.js");
const { suggestCopyName, takenBoardNames } = await import("../web/assets/static/dist/copyname.js");

test("the first free suffix wins, and the count starts at 2", () => {
  assert.equal(suggestCopyName("Синхрон", []), "Синхрон (копия)");
  assert.equal(suggestCopyName("Синхрон", ["Синхрон"]), "Синхрон (копия)");
  assert.equal(suggestCopyName("Синхрон", ["Синхрон (копия)"]), "Синхрон (копия 2)");
  assert.equal(
    suggestCopyName("Синхрон", ["Синхрон", "Синхрон (копия)", "Синхрон (копия 2)"]),
    "Синхрон (копия 3)",
  );
});

test("a gap in the run is filled rather than skipped past", () => {
  assert.equal(
    suggestCopyName("Синхрон", ["Синхрон (копия)", "Синхрон (копия 3)"]),
    "Синхрон (копия 2)",
  );
});

test("names are compared trimmed, the way the server stores them", () => {
  assert.equal(suggestCopyName("Синхрон", ["  Синхрон (копия)  "]), "Синхрон (копия 2)");
});

test("another board's name is not this board's collision", () => {
  assert.equal(suggestCopyName("Синхрон", ["Бриз (копия)", "Синхрон 2026"]), "Синхрон (копия)");
});

test("a reader with thirty clones gets the plain suffix back, not a thirty-first guess", () => {
  const taken = ["Синхрон (копия)"];
  for (let n = 2; n <= 30; n++) taken.push(`Синхрон (копия ${n})`);
  assert.equal(suggestCopyName("Синхрон", taken), "Синхрон (копия)");
});

test("the server answers; a legacy board with no readable name contributes none", async () => {
  const real = xyApp.fetchJSON;
  xyApp.fetchJSON = async () => [
    { name: "Синхрон", schema_version: 2 },
    { name: "Синхрон (копия)", schema_version: 2 },
    { name: "", schema_version: 1 }, // legacy: the name is still in name_enc
  ];
  try {
    assert.deepEqual((await takenBoardNames()).sort(), ["Синхрон", "Синхрон (копия)"]);
  } finally {
    xyApp.fetchJSON = real;
  }
});

test("offline falls back to the cached list rather than colliding", async () => {
  const realFetch = xyApp.fetchJSON;
  const realList = xySync.getBoardList;
  xyApp.fetchJSON = async () => { throw new Error("offline"); };
  xySync.getBoardList = async () => [{ name: "Синхрон (копия)", schema_version: 2 }];
  try {
    const taken = await takenBoardNames();
    assert.deepEqual(taken, ["Синхрон (копия)"]);
    assert.equal(suggestCopyName("Синхрон", taken), "Синхрон (копия 2)");
  } finally {
    xyApp.fetchJSON = realFetch;
    xySync.getBoardList = realList;
  }
});

test("no list anywhere is not an error — the plain suffix stands", async () => {
  const realFetch = xyApp.fetchJSON;
  const realList = xySync.getBoardList;
  xyApp.fetchJSON = async () => { throw new Error("offline"); };
  xySync.getBoardList = async () => { throw new Error("no db"); };
  try {
    assert.deepEqual(await takenBoardNames(), []);
  } finally {
    xyApp.fetchJSON = realFetch;
    xySync.getBoardList = realList;
  }
});
