import {stageType} from "./standings.js";
import type {StageRef, StageRefMatch} from "./standings.js";
// Which tabs a Game page shows is Block / Round / Group knowledge, held here
// once; pages render the array and derive nothing of their own.

export type GameKind = "ek" | "si" | "brain" | "ksi" | "od" | "troika";

export type TabKind =
  | "grid" | "block" | "pods" | "round" | "protocol" | "reseed" | "stage"
  | "stats" | "roster" | "venues" | "seed" | "seedImport"
  | "results" | "detailed" | "input" | "screen" | "refusals";

export interface GameTab {
  key: string;
  label: string;
  kind: TabKind;
  stages: string[];
  legacy?: string;
  stage?: StageRef; // ЭК panes draw it: the stage, or a synthetic круг / Пересев
}

export interface GameTabsOptions {
  game: GameKind;
  viewer: boolean;
  seeded?: boolean;
}

export const RESEED_TAB_CODE = "reseeds";

export function gameTabs(stages: StageRef[], options: GameTabsOptions): GameTab[] {
  const host = !options.viewer;
  switch (options.game) {
  case "ek":
  case "si":
    return [
      ...fixedTabs(["grid", "Сетка"], ["venues", "Площадки"], ...when(host, ["seedImport", "Импорт команд"])),
      ...foldReseeds(stages.flatMap((stage) => roundTabs(stage, stages))),
      ...fixedTabs(["stats", "Статистика"], ...when(options.game === "ek", ["roster", "Составы"])),
    ];
  case "brain":
    return brainTabs(stages, host && Boolean(options.seeded), "Индивидуальная статистика");
  // Троечка's tabs are брейн's: a Сетка, a table and протоколы per Block, the
  // пересев, and the per-player statistics its three chairs make interesting.
  case "troika":
    return brainTabs(stages, host && Boolean(options.seeded), "Статистика");
  case "ksi":
    return fixedTabs(["detailed", "Подробно"], ["results", "Итог"], ...when(host, ["refusals", "Отказы"]), ["roster", "Составы"]);
  case "od":
    return fixedTabs(["results", "Итог"], ["detailed", "Подробно"], ["input", "Ввод"], ...when(host, ["screen", "Экран"]), ["roster", "Составы"]);
  }
}

export function canonicalKey(tabs: GameTab[], key: string): string {
  if (tabs.some((tab) => tab.key === key)) return key;
  return tabs.find((tab) => tab.legacy === key)?.key || key;
}

// «1-й групповой этап. Группа 1» → «1-й групповой этап», «DE 1» → «DE»,
// «Плей-офф. 1 этап» + «Плей-офф. 2 этап» → «Плей-офф».
export function blockLabel(stages: StageRef[]): string {
  const first = String(stages[0]?.title || "");
  if (stages.some((stage) => stage.grain?.group)) {
    const named = first.replace(/\.?\s*Группа\s*\S+$/, "");
    if (named !== first) return named || "Групповой этап";
    return first.replace(/\s*\d+$/, "") || first;
  }
  if (stages.length === 1) return first;
  const prefix = first.split(". ")[0];
  if (prefix !== first && stages.every((stage) => String(stage.title || "").startsWith(prefix + ". "))) return prefix;
  return "Плей-офф";
}

export function groupLabel(stage: StageRef): string {
  const title = String(stage.title || "");
  return title.match(/Группа\s*\S+$/)?.[0] || title || `Группа ${stage.grain?.group || "?"}`;
}

type Fixed = [TabKind, string];

function fixedTabs(...tabs: Fixed[]): GameTab[] {
  return tabs.map(([key, label]) => ({key, label, kind: key, stages: []}));
}

function when(cond: boolean, tab: Fixed): Fixed[] {
  return cond ? [tab] : [];
}

function isReseed(stage: StageRef): boolean {
  return stageType(stage) === "reseed";
}

function stageTab(stage: StageRef, kind: TabKind, label = stage.title || stage.code): GameTab {
  return {
    key: `stage:${stage.code}`,
    label,
    kind,
    stages: stage.members || [stage.code],
    legacy: stage.legacy ? `stage:${stage.legacy}` : undefined,
    stage,
  };
}

// The sheets enter protocols by круг — every группа at once — because that is
// the order the бои are played in; a tab per группа is the Сетка's job.
function roundTabs(stage: StageRef, stages: StageRef[]): GameTab[] {
  const block = stage.grain?.block;
  if (!block || !stage.grain?.group) return [stageTab(stage, isReseed(stage) ? "reseed" : "stage", stageTabLabel(stage))];
  const groups = stages.filter((s) => s.grain?.block === block && s.grain?.group);
  if (groups.length < 2) return [stageTab(stage, "stage", stageTabLabel(stage))];
  if (stage !== groups[0]) return [];
  const byRound = new Map<number, StageRefMatch[]>();
  for (const group of groups) {
    for (const match of group.matches || []) {
      const round = Number(match.round || 1);
      byRound.set(round, [...(byRound.get(round) || []), {...match, group: groupLabel(group)}]);
    }
  }
  const members = groups.map((group) => group.code);
  // The synthetic codes land in URLs, so they read as words: the block's slug
  // where the scheme names one; the old `@` spellings ride along for bookmarks.
  const slug = groups[0].slug || "";
  const standings = {code: slug || `${block}-standings`, legacy: `${block}@standings`, title: blockLabel(groups), stage_type: "standings", members};
  return [stageTab(standings, "block"), ...Array.from(byRound.keys()).sort((a, b) => a - b).map((round) => stageTab({
    code: `${slug || block}-r${round}`,
    legacy: `${block}@r${round}`,
    title: `Круг ${round}`,
    stage_type: "matches",
    matches: byRound.get(round) || [],
    members,
  }, "round"))];
}

// Личная СИ reseeds before every play-off round; seven identical tabs said
// nothing six of them didn't. A lone reseed keeps its own tab.
function foldReseeds(tabs: GameTab[]): GameTab[] {
  const reseeds = tabs.filter((tab) => tab.kind === "reseed");
  if (reseeds.length < 2) return tabs;
  const members = reseeds.map((tab) => tab.stages[0]);
  const folded = stageTab({code: RESEED_TAB_CODE, title: "Пересев", stage_type: "reseed", members}, "reseed");
  return tabs.flatMap((tab) => tab.kind !== "reseed" ? [tab] : tab === reseeds[0] ? [folded] : []);
}

function stageTabLabel(stage: StageRef): string {
  if (isReseed(stage)) return "Пересев";
  switch (stage.code) {
  case "r16_run1":
    return "1/16-1";
  case "r16_run2":
    return "1/16-2";
  case "r8":
    return "1/8";
  case "r4":
    return "1/4";
  case "r2":
    return "1/2";
  case "final":
    return "Финал";
  default:
    return stage.title || stage.code;
  }
}

// Per Block its crosstab (or pod board) and протоколы, as the source workbook
// had them; «table» / «protocol» are the pre-Block hashes.
function brainTabs(stages: StageRef[], seeded: boolean, statsLabel: string): GameTab[] {
  const tabs = fixedTabs(["grid", "Сетка"]);
  let table: string | undefined = "table";
  let protocol: string | undefined = "protocol";
  for (const block of blocks(stages)) {
    const codes = block.stages.map((stage) => stage.code);
    const label = blockLabel(block.stages);
    const ranks = block.stages.some((stage) => stage.kind === "rr");
    if (ranks) {
      tabs.push({key: `block:${block.code}`, label, kind: "block", stages: codes, legacy: table});
      table = undefined;
    } else if (block.stages.some((stage) => stage.grain?.group)) {
      tabs.push({key: `block:${block.code}`, label, kind: "pods", stages: codes});
    }
    tabs.push({key: `protocol:${block.code}`, label: `${label} (протоколы)`, kind: "protocol", stages: codes, legacy: protocol});
    protocol = undefined;
  }
  const reseeds = stages.filter(isReseed).map((stage) => stage.code);
  if (reseeds.length) tabs.push({key: "reseed", label: "Пересев", kind: "reseed", stages: reseeds});
  tabs.push(...fixedTabs(["stats", statsLabel], ["roster", "Составы"], ...when(seeded, ["seed", "Посев"])));
  return tabs;
}

// A Block is a run of stages sharing a grain.block; a reseed ends the run.
function blocks(stages: StageRef[]): Array<{code: string; stages: StageRef[]}> {
  const out: Array<{code: string; stages: StageRef[]}> = [];
  for (const stage of stages) {
    const code = stage.grain?.block || "";
    const last = out[out.length - 1];
    if (isReseed(stage)) out.push({code: "", stages: []});
    else if (last && last.stages.length && last.code === code) last.stages.push(stage);
    else out.push({code, stages: [stage]});
  }
  return out.filter((block) => block.stages.length);
}
