// The gallery: every shared table and the fest grid, drawn from fixtures on one
// page — the skin sheet a table-skin change is judged on. Four screenshots
// (phone × desktop × light × dark) cover what 84 live pages did, with no live
// data, no viewer count and no tab strip to move between two shots. Served at
// /gallery in dev mode only.
import {buildVenuesTable} from "./venue.js";
import type {Venue} from "./venue.js";
import {buildGroupStandingsView} from "./standings.js";
import type {GroupStandingsGroup} from "./standings.js";
import {buildRosterTable} from "./fest-roster.js";
import type {RosterTeam} from "./fest-roster.js";
import {buildEKStatsTable, buildIndividualStatsTable} from "./ek-stats.js";
import type {EKPlayerStatsRow, IndividualStatsRow} from "./ek-stats.js";
import {markNameOverflow} from "./widgets.js";
import { buildFestGrid, buildReseedStagePanel } from "./fest-grid.js";
import { autocomplete } from "../../../../dopeuikit/assets/ts/suggest.js";
import type { FestGridMatch, FestGridStage } from "./fest-grid.js";
import S from "./i18nstrings.js";

const LONG = "Команда с названием длиннее любой колонки, которую ей отвели";

function bout(code: string, letter: string, venue: number, teams: Array<[string, number, number]>, status = "finished"): FestGridMatch[] {
  return [{
    code, letter, venue, status, participantCount: teams.length,
    slots: teams.map(([name]) => ({label: name})),
    participants: teams.map(([name, total, place]) => ({name, total, place})),
  }];
}

// A fest grid with every box kind: a group Block of three groups (one long
// name), a pod Block whose Ranker sent no sort (M alone), a Match round of
// four-seat and two-seat boxes, and a reseed the fest grid drops.
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

// A stand-in picture for the thumb: the gallery draws from fixtures and reaches
// no network, so the demo photo is a rectangle it carries itself.
const THUMB = "data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='120' height='96'%3E%3Crect width='120' height='96' fill='%2399a3b0'/%3E%3C/svg%3E";

// The kit's badge / amount / card / thumb, the primitives a row of names and
// numbers is built from. dope draws none of them today; they are here because a
// primitive nobody can look at is one every app re-invents.
function chips(): HTMLElement {
  const span = (cls: string, text: string): HTMLElement => {
    const node = document.createElement("span");
    node.className = cls;
    node.textContent = text;
    return node;
  };
  const card = document.createElement("div");
  card.className = "card u-row u-gap-sm u-align-center u-wrap";
  card.append(
    span("list-row-title", S.gallery.chips.name()),
    span("badge", S.gallery.chips.badgeNeutral()),
    span("badge badge-emphasis", S.gallery.chips.badgeEmphasis()),
    span("badge badge-positive", S.gallery.chips.badgePositive()),
    span("badge badge-negative", S.gallery.chips.badgeNegative()),
    span("amount", S.gallery.chips.amountPlain()),
    span("amount amount-positive", S.gallery.chips.amountPositive()),
    span("amount amount-negative", S.gallery.chips.amountNegative()),
    span("amount amount-zero", S.gallery.chips.amountZero()),
  );
  const image = document.createElement("img");
  image.className = "thumb";
  image.src = THUMB;
  image.alt = S.gallery.chips.thumbAlt();
  card.append(image);
  return card;
}

// The suggestfield primitive: an input in a .suggest-anchor with the kit's
// filtered dropdown bound to it — where a list goes when a native <select>
// cannot hold it, because that picker cannot be typed into on a phone. Focus
// the field to see the popover.
const SUGGEST_TOWNS = [
  {value: "Тбилиси", label: "Тбилиси", hint: "Грузия"},
  {value: "Ереван", label: "Ереван", hint: "Армения"},
  {value: "Белград", label: "Белград", hint: "Сербия"},
  {value: "Алматы", label: "Алматы", hint: "Казахстан"},
  {value: "Стамбул", label: "Стамбул", hint: "Турция"},
];

function suggestField(): HTMLElement {
  const input = document.createElement("input");
  input.className = "input";
  input.type = "text";
  input.autocomplete = "off";
  input.spellcheck = false;
  input.placeholder = "Город — начните печатать";
  const anchor = document.createElement("span");
  anchor.className = "suggest-anchor u-col";
  anchor.append(input);
  autocomplete(input, (q) => {
    const needle = q.trim().toLowerCase();
    return SUGGEST_TOWNS.filter((c) => c.label.toLowerCase().startsWith(needle));
  });
  return anchor;
}

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
  // frame (a max-content host around the fest grid's column grid sizes to
  // Chromium's infinite width). Each section scrolls its own table.
  root.classList.add("fits-frame");
  root.replaceChildren(
    section(S.gallery.section.festGrid(), "table-host grid-host", buildFestGrid({stages: festStages}, {stageHeaderLink: false, matchTitleLink: false})),
    section(S.gallery.section.reseed(), "table-host fits-frame", buildReseedStagePanel(reseedStage, {letters: new Map([["s1-g1-1", "A"], ["s1-g1-2", "B"]])})),
    section(S.gallery.section.groupStandings(), "table-host fits-frame", buildGroupStandingsView(groups)),
    section(S.gallery.section.ekStats(), "table-host", buildEKStatsTable(ekStats)),
    section(S.gallery.section.individualStats(), "table-host", buildIndividualStatsTable(individualStats)),
    section(S.gallery.section.venues(), "table-host", buildVenuesTable(venues)),
    section(S.gallery.section.venuesHost(), "table-host", buildVenuesTable(venues, {editable: true, onTitleChange: () => {}})),
    section(S.gallery.section.roster(), "table-host fits-frame", buildRosterTable(roster)),
    section(S.gallery.section.chips(), "fits-frame", chips()),
    section(S.gallery.section.suggest(), "fits-frame", suggestField()),
  );
  requestAnimationFrame(() => markNameOverflow(root, {
    cellSelector: ".results-team",
    nameSelector: ".results-team-name",
    truncatedClass: "results-team-truncated",
  }));
}

const root = document.getElementById("gallery");
if (root) render(root);
