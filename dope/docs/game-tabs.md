# Game tabs as one module (#6)

Status: open. Strength: worth exploring. #3 (grain on the fest view) is done,
which is what makes this cheap now.

## The problem

Which tabs a Game page shows is Block / Round / Group knowledge
(`CONTEXT.md`), and three pages rebuild it from titles and code suffixes:

- `web/ts/match-table.ts:857` `foldReseedStages` (many reseeds → one
  «Пересев»), `:886` `roundStages` (a Block of Groups → one tab per круг),
  `:965` `canonicalStageCode` (legacy `@` bookmarks), `:992` `stageTabLabel`.
  Used by `ek.ts:1416` `ekSchemeStages` and `:1324` `gameSubnavItems`.
- `web/ts/brain.ts:184` `blockBuckets` (walks `scheme.stages`, cuts at
  reseeds), `:207` `blockLabel` (regexes «Группа N» and «. » prefixes out of
  titles), `:231` `visibleTabs`, `:250` `podBucket`.
- `web/ts/si.ts:185` `visibleTabs` — its own list again.

Two pages can therefore disagree about the same scheme, and a label rule
(«DE» from «DE 1») lives in a regex on one page.

## The change

One module, `web/ts/game-tabs.ts`:

```ts
export interface GameTab { key: string; label: string; kind: "grid" | "block" | "round" | "reseed" | "stage" | "stats" | "roster" | "venues" | "seed" | "seedImport"; stages: string[] }
export function gameTabs(stages: StageRef[], opts: {game: "ek" | "brain" | "si" | "od" | "ksi"; viewer: boolean; seeded?: boolean}): GameTab[]
```

It owns Block grouping (by `grain.block`, never by title), круг tabs, the one
Пересев, labels (from `grain` and the Block's title, not from a регексп on
«Группа»), and legacy-code translation. Pages render the array and never
derive; `blockBuckets`, both `visibleTabs`, `foldReseedStages`, `roundStages`
delete or move in. Whether the server should emit the tabs outright (a
`Tabs []TabView` on `store.FestView`, computed in `domain/festview`) was left
open on 15 Aug; the user's stated preference is "bare minimum on the client,
all state on the server", so prefer the server if the labels need anything
the client does not already hold.

## Acceptance

- One place computes tabs; `deno test` covers: a Block of Groups → круг
  tabs; a Block of one Group → its own tab; N reseeds → one Пересев, one
  reseed → its own; pods → a block tab and a протоколы tab; a legacy `@`
  code canonicalises.
- ЭК, КИнСБФ and Личная СИ show the same tabs as before this change
  (screenshot the tab strip of each on dopetest and diff).
- No regex on «Группа» or «. » outside the module.

## Traps

- ЭК's `stage-cache.ts` keys panes by the *displayed* stage code (a круг is
  a synthetic stage with `members`); keep `members` on the tab.
- The brain page's block tab shows a crosstab only for a ranking Block
  (`bucket.ranks`) and a pod board for DE — that is `Kind` on the fest view
  (`StageView.Kind`), not a title.
- The ЭК host has an extra «Импорт команд» tab and the brain host a «Посев»
  tab; both are `viewer`-gated in the page today.
