// The gallery: every shared table and the Сетка, drawn from fixtures on one
// page — the skin sheet a table-skin change is judged on. Four screenshots
// (phone × desktop × light × dark) cover what 84 live pages did, with no live
// data, no viewer count and no tab strip to move between two shots. Served at
// /gallery in dev mode only.
import { DopeTable } from "./match-table.js";
import type { GroupStandingsGroup, EKPlayerStatsRow, IndividualStatsRow, RosterTeam, Venue } from "./match-table.js";
import { buildFestGrid, buildReseedStagePanel } from "./fest-grid.js";
import type { FestGridMatch, FestGridStage } from "./fest-grid.js";

const T = DopeTable;
const LONG = "Команда с названием длиннее любой колонки, которую ей отвели";

function bout(code: string, letter: string, venue: number, teams: Array<[string, number, number]>, status = "finished"): FestGridMatch[] {
  return [{
    code, letter, venue, status, participantCount: teams.length,
    slots: teams.map(([name]) => ({label: name})),
    participants: teams.map(([name, total, place]) => ({name, total, place})),
  }];
}

// A Сетка with every box kind: a group Block of three групп (one long name),
// a pod Block whose Ranker sent no sort (М alone), a бой round of four-seat
// and two-seat boxes, and a reseed the Сетка drops.
const festStages: FestGridStage[] = [
  {
    code: "s1-g1", title: "Групповой этап. Группа 1", stage_type: "matches", kind: "rr", grain: {block: "s1", group: "1"},
    sort: [{metric: "points", dir: "desc"}],
    standings: [
      {rank: 1, name: "Ктулху", metrics: {place: 1, points: 9}},
      {rank: 2, name: LONG, metrics: {place: 2, points: 6}},
      {rank: 3, name: "Вина России", metrics: {place: 3, points: 3}},
    ],
    matches: [...bout("s1-g1-1", "A", 1, [["Ктулху", 120, 1], [LONG, 90, 2]]), ...bout("s1-g1-2", "B", 1, [["Вина России", 30, 2], ["Ктулху", 100, 1]])],
  },
  {
    code: "s1-g2", title: "Групповой этап. Группа 2", stage_type: "matches", kind: "rr", grain: {block: "s1", group: "2"},
    sort: [{metric: "points", dir: "desc"}],
    standings: [
      {rank: 1, name: "Дахусим", metrics: {place: 1, points: 10.5}},
      {rank: 2, name: "Злая щитоспинка", metrics: {place: 2, points: 10.5}},
      {rank: 3, name: "Тина Терияки", metrics: {place: 3, points: 0}},
      {rank: 4, name: "Bikes for Peace", metrics: {place: 4, points: 0}},
    ],
    matches: [...bout("s1-g2-1", "C", 2, [["Дахусим", 200, 1], ["Злая щитоспинка", 200, 1]])],
  },
  {
    code: "s2-g1", title: "DE 1", stage_type: "matches", kind: "de", grain: {block: "s2", group: "1"},
    standings: [{rank: 1, name: "Ктулху", metrics: {place: 1}}, {rank: 2, name: "Дахусим", metrics: {place: 2}}],
    matches: [...bout("s2-g1-1", "D", 3, [["Ктулху", 1, 1], ["Дахусим", 0, 2]])],
  },
  {
    code: "s3-r1", title: "Полуфиналы", stage_type: "matches",
    matches: [
      ...bout("s3-r1-1", "E", 1, [["Ктулху", 250, 1], ["Злая щитоспинка", 180, 2], [LONG, 130, 3], ["Кошка вид сзади", 40, 4]]),
      ...bout("s3-r1-2", "F", 2, [["Дахусим", 0, 0], ["Вина России", 0, 0], ["Тина Терияки", 0, 0]], "pending"),
    ],
  },
  {
    code: "s3-r2", title: "Финал", stage_type: "matches",
    matches: [...bout("s3-r2-1", "G", 1, [["Ктулху", 0, 0], ["Дахусим", 0, 0]], "pending")],
  },
];

const reseedStage: FestGridStage = {
  code: "s2-reseed", stage_type: "reseed", sort: [{metric: "total", dir: "desc"}, {metric: "plus", dir: "desc"}],
  reseedEntries: [
    {rank: 1, name: "Ктулху", metrics: {match: "s1-g1-1+s1-g1-2", total: 470, plus: 480}},
    {rank: 2, name: LONG, metrics: {match: "s1-g1-1", total: 90, plus: 120}},
    {rank: 3, name: "Вина России", metrics: {match: "s1-g1-2", total: -30, plus: 20}},
  ],
};

const groups: GroupStandingsGroup[] = [
  {title: "Группа 1", roundCount: 3, rows: [
    {name: "Ярослав Кудымов", points: 10.5, rounds: [2, 2.5, 3]},
    {name: "Алексей Погорелов", points: 10, rounds: [3, 3, 2]},
    {name: LONG, points: 8.5, rounds: [2, 2.5, 1]},
  ]},
  {title: "Группа 2", roundCount: 3, rows: [
    {name: "Никита Косенков", points: 9, rounds: [3, 3, 3]},
    {name: "Есения Погорелова", points: 6, rounds: [2, 2, 2]},
  ]},
];

const ekStats: EKPlayerStatsRow[] = [
  {player: "Тимур Трубачеев", team: "Стол", sum: 180, plus: 200, battles: 3, rightTotal: 6, right: [1, 2, 0, 1, 2], wrong: [0, 0, 1, 0, 0], share: 0.67},
  {player: "Филипп Тучак", team: LONG, sum: -40, plus: 60, battles: 2, rightTotal: 2, right: [1, 1, 0, 0, 0], wrong: [0, 0, 0, 1, 1], share: 0},
];

const individualStats: IndividualStatsRow[] = [
  {player: "Ярослав Кудымов", sum: 470, plus: 480, battles: 4, right: [2, 3, 3, 4, 5]},
  {player: LONG, sum: 90, plus: 120, battles: 4, right: [1, 1, 0, 1, 1]},
];

const venues: Venue[] = [{number: 1, title: "Рим, Алиенора"}, {number: 2, title: ""}, {number: 3, title: LONG}];

const roster: RosterTeam[] = [
  {number: 1, name: "Детективы для элит", city: "Санкт-Петербург", ratingID: 1, players: [{name: "Дмитрий Яшин", ratingID: 1}, {name: "Анастасия Банникова"}, {name: "Даниил Чеченин"}, {name: "Денис Потехин"}]},
  {number: 2, name: LONG, city: "Москва", players: ["Леонид Карлинский", "Михаил Коблик"]},
  {number: 3, name: "Bikes for Peace", players: []},
];

function section(title: string, host: string, node: HTMLElement): HTMLElement {
  const wrap = document.createElement("section");
  wrap.className = "gallery-section";
  const head = document.createElement("h2");
  head.className = "gallery-head";
  head.textContent = title;
  wrap.appendChild(head);
  const frame = document.createElement("div");
  frame.className = host;
  frame.appendChild(node);
  wrap.appendChild(frame);
  return wrap;
}

function render(root: HTMLElement): void {
  // The mount is a table-host; as the page's whole width it must fit the
  // frame (a max-content host around the Сетка's column grid sizes to
  // Chromium's infinite width). Each section scrolls its own table.
  root.classList.add("fits-frame");
  root.replaceChildren(
    section("Сетка", "table-host grid-host", buildFestGrid({stages: festStages}, {stageHeaderLink: false, matchTitleLink: false})),
    section("Пересев", "table-host fits-frame", buildReseedStagePanel(reseedStage, {letters: new Map([["s1-g1-1", "A"], ["s1-g1-2", "B"]])})),
    section("Групповой этап", "table-host fits-frame", T.buildGroupStandingsView(groups)),
    section("Статистика ЭК", "table-host", T.buildEKStatsTable(ekStats)),
    section("Статистика личной СИ", "table-host", T.buildIndividualStatsTable(individualStats)),
    section("Площадки", "table-host", T.buildVenuesTable(venues)),
    section("Площадки, ведущий", "table-host", T.buildVenuesTable(venues, {editable: true, onTitleChange: () => {}})),
    section("Составы", "table-host fits-frame", T.buildRosterTable(roster)),
  );
  requestAnimationFrame(() => T.markNameOverflow(root, {
    cellSelector: ".results-team",
    nameSelector: ".results-team-name",
    truncatedClass: "results-team-truncated",
  }));
}

const root = document.getElementById("gallery");
if (root) render(root);
