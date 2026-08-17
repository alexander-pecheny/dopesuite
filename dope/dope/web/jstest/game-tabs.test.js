import {test} from "node:test";
import assert from "node:assert/strict";
import {gameTabs, canonicalKey, blockLabel, groupLabel, RESEED_TAB_CODE} from "./dist/game-tabs.js";

const group = (n, extra = {}) => ({
  code: `s1-g${n}`,
  title: `Групповой этап. Группа ${n}`,
  stage_type: "matches",
  kind: "rr",
  grain: {block: "s1", group: String(n)},
  matches: [
    {code: `s1-g${n}-1`, title: "Бой 1", round: 1},
    {code: `s1-g${n}-2`, title: "Бой 2", round: 2},
  ],
  ...extra,
});
const playoff = {code: "s2-r1", title: "Финал", stage_type: "matches", grain: {block: "s2"}, matches: []};
const keys = (tabs) => tabs.map((t) => t.key);
const labels = (tabs) => tabs.map((t) => t.label);

// The sheets enter protocols by круг — «Круг 1» through «Круг 4», every группа
// at once — because that is the order the бои are played in. A tab per группа is
// the order they are ranked in, which the Сетка already shows.
test("ЭК: a Block of Groups is one standings tab and one tab per круг", () => {
  const tabs = gameTabs([group(1), group(2), playoff], {game: "ek", viewer: true});
  assert.deepEqual(labels(tabs), ["Сетка", "Площадки", "Групповой этап", "Круг 1", "Круг 2", "Финал", "Статистика", "Составы"]);
  assert.deepEqual(keys(tabs).slice(2, 6), ["stage:s1-standings", "stage:s1-r1", "stage:s1-r2", "stage:s2-r1"]);
  assert.deepEqual(tabs.slice(2, 6).map((t) => t.kind), ["block", "round", "round", "stage"]);
  // A tab is assembled from real server stages, which is what gets fetched.
  assert.deepEqual(tabs[2].stages, ["s1-g1", "s1-g2"]);
  assert.deepEqual(tabs[3].stages, ["s1-g1", "s1-g2"]);
  assert.deepEqual(tabs[5].stages, ["s2-r1"]);
  // The pane draws the tab's stage: synthetic for a круг, with every группа's
  // бой of that round, each saying which table it was.
  assert.equal(tabs[2].stage.stage_type, "standings");
  assert.deepEqual(tabs[3].stage.matches.map((m) => [m.code, m.group]), [["s1-g1-1", "Группа 1"], ["s1-g2-1", "Группа 2"]]);
  assert.equal(tabs[5].stage.title, "Финал");
});

test("ЭК: the synthetic codes read as the block's slug when the scheme names one", () => {
  const tabs = gameTabs([group(1, {slug: "group-stage"}), group(2, {slug: "group-stage"}), playoff], {game: "ek", viewer: true});
  assert.deepEqual(keys(tabs).slice(2, 5), ["stage:group-stage", "stage:group-stage-r1", "stage:group-stage-r2"]);
});

test("ЭК: a Block of one Group keeps its own tab", () => {
  const only = {...group(1), title: "Группа"};
  const tabs = gameTabs([only, playoff], {game: "ek", viewer: true});
  assert.deepEqual(keys(tabs).slice(2, 4), ["stage:s1-g1", "stage:s2-r1"]);
  assert.equal(tabs[2].kind, "stage");
  assert.equal(tabs[2].stage, only);
});

// Every reseed used to be a tab of its own — личная СИ showed seven of them.
// They fold into one «Пересев», keeping their codes so the pane can draw each
// этап's table; a lone reseed keeps its own name and place.
test("ЭК: N reseeds fold into one Пересев, one keeps its tab", () => {
  const stages = [
    {code: "s1", title: "Групповой этап", stage_type: "matches"},
    {code: "s2-reseed", title: "Пересев", stage_type: "reseed"},
    {code: "s2-r1", title: "Плей-офф. 1 этап", stage_type: "matches"},
    {code: "s2-r2-reseed", title: "Пересев", stage_type: "reseed"},
    {code: "s2-r2", title: "Плей-офф. 2 этап", stage_type: "matches"},
  ];
  const tabs = gameTabs(stages, {game: "si", viewer: true});
  assert.deepEqual(keys(tabs), ["grid", "venues", "stage:s1", `stage:${RESEED_TAB_CODE}`, "stage:s2-r1", "stage:s2-r2", "stats"]);
  assert.equal(tabs[3].kind, "reseed");
  assert.deepEqual(tabs[3].stages, ["s2-reseed", "s2-r2-reseed"]);
  const lone = gameTabs(stages.slice(0, 3), {game: "si", viewer: true});
  assert.deepEqual(keys(lone).slice(2, 5), ["stage:s1", "stage:s2-reseed", "stage:s2-r1"]);
  assert.equal(lone[3].label, "Пересев");
  assert.deepEqual(lone[3].stages, ["s2-reseed"]);
});

test("ЭК: the host gets «Импорт команд», an individual game has no составы", () => {
  assert.deepEqual(keys(gameTabs([], {game: "ek", viewer: false})), ["grid", "venues", "seedImport", "stats", "roster"]);
  assert.deepEqual(keys(gameTabs([], {game: "si", viewer: false})), ["grid", "venues", "seedImport", "stats"]);
});

test("ЭК: a legacy `@` bookmark canonicalises to the tab it meant", () => {
  const tabs = gameTabs([group(1), group(2)], {game: "ek", viewer: true});
  assert.equal(canonicalKey(tabs, "stage:s1@standings"), "stage:s1-standings");
  assert.equal(canonicalKey(tabs, "stage:s1@r2"), "stage:s1-r2");
  assert.equal(canonicalKey(tabs, "stage:s1-r2"), "stage:s1-r2");
  assert.equal(canonicalKey(tabs, "stage:nowhere"), "stage:nowhere");
});

// The brain tab set mirrors the source workbook: the Сетка, then per Block its
// crosstab (when the Block ranks) or pod board (DE) and its протоколы, the one
// Пересев, the stats and the составы.
test("брейн: a ranking Block is a crosstab tab and a протоколы tab", () => {
  const stages = [group(1), group(2), {code: "s2-reseed", title: "Пересев", stage_type: "reseed", kind: "reseed"}, playoff];
  const tabs = gameTabs(stages, {game: "brain", viewer: true});
  assert.deepEqual(keys(tabs), ["grid", "block:s1", "protocol:s1", "protocol:s2", "reseed", "stats", "roster"]);
  assert.deepEqual(labels(tabs).slice(1, 4), ["Групповой этап", "Групповой этап (протоколы)", "Финал (протоколы)"]);
  assert.deepEqual(tabs[1].kind, "block");
  assert.deepEqual(tabs[1].stages, ["s1-g1", "s1-g2"]);
  assert.deepEqual(tabs[3].stages, ["s2-r1"]);
  assert.deepEqual(tabs[4].stages, ["s2-reseed"]);
});

test("брейн: pods get a block tab and a протоколы tab; a bare bracket only протоколы", () => {
  const pod = (n) => ({code: `s2-g${n}`, title: `DE ${n}`, stage_type: "matches", kind: "matches", grain: {block: "s2", group: String(n)}});
  const bracket = ["1/2 финала", "Финал"].map((title, i) => ({code: `s3-r${i + 1}`, title, stage_type: "matches", grain: {block: "s3"}}));
  const tabs = gameTabs([pod(1), pod(2), ...bracket], {game: "brain", viewer: true});
  assert.deepEqual(keys(tabs).slice(1, 4), ["block:s2", "protocol:s2", "protocol:s3"]);
  assert.deepEqual(labels(tabs).slice(1, 4), ["DE", "DE (протоколы)", "Плей-офф (протоколы)"]);
  assert.equal(tabs[1].kind, "pods");
});

test("брейн: the host gets «Посев» only for a seeded scheme", () => {
  assert.deepEqual(keys(gameTabs([], {game: "brain", viewer: false, seeded: true})), ["grid", "stats", "roster", "seed"]);
  assert.deepEqual(keys(gameTabs([], {game: "brain", viewer: false})), ["grid", "stats", "roster"]);
  assert.deepEqual(keys(gameTabs([], {game: "brain", viewer: true, seeded: true})), ["grid", "stats", "roster"]);
});

// The pre-Block hashes: one crosstable, one протоколы. Old bookmarks land on
// the first Block's pair rather than silently falling back to the Сетка.
test("брейн: legacy #table and #protocol land on the first Block's pair", () => {
  const pod = {code: "s1-g1", title: "DE 1", stage_type: "matches", grain: {block: "s1", group: "1"}};
  const tabs = gameTabs([pod, {...group(1), code: "s2-g1", grain: {block: "s2", group: "1"}}], {game: "brain", viewer: true});
  assert.equal(canonicalKey(tabs, "protocol"), "protocol:s1");
  assert.equal(canonicalKey(tabs, "table"), "block:s2");
  assert.equal(canonicalKey(gameTabs([], {game: "brain", viewer: true}), "table"), "table");
});

test("КСИ and ЧГК keep their fixed strips, host-only tabs gated", () => {
  assert.deepEqual(keys(gameTabs([], {game: "ksi", viewer: false})), ["detailed", "results", "refusals", "roster"]);
  assert.deepEqual(keys(gameTabs([], {game: "ksi", viewer: true})), ["detailed", "results", "roster"]);
  assert.deepEqual(keys(gameTabs([], {game: "od", viewer: false})), ["results", "detailed", "input", "screen", "roster"]);
  assert.deepEqual(keys(gameTabs([], {game: "od", viewer: true})), ["results", "detailed", "input", "roster"]);
});

// A Block is named by the групп's common prefix («1-й групповой этап. Группа 1»
// → «1-й групповой этап», «DE 1» → «DE»), a bracket by its rounds' common
// prefix, else «Плей-офф».
test("blockLabel names a Block from grain and titles, never leaves it blank", () => {
  assert.equal(blockLabel([group(1)]), "Групповой этап");
  assert.equal(blockLabel([{...group(1), title: "Группа 1"}]), "Групповой этап");
  assert.equal(blockLabel([{...group(1), title: "DE 1"}]), "DE");
  assert.equal(blockLabel([playoff]), "Финал");
  assert.equal(blockLabel([{title: "Плей-офф. 1 этап"}, {title: "Плей-офф. 2 этап"}]), "Плей-офф");
  assert.equal(blockLabel([{title: "1/4 финала"}, {title: "Финал"}]), "Плей-офф");
});

test("groupLabel is the «Группа N» tail, else the title, else the grain", () => {
  assert.equal(groupLabel(group(3)), "Группа 3");
  assert.equal(groupLabel({title: "DE 1", grain: {block: "s2", group: "1"}}), "DE 1");
  assert.equal(groupLabel({grain: {block: "s2", group: "4"}}), "Группа 4");
});
