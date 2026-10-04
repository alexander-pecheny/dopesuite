# Codebase Map

## What this is
A system for running tournaments and championships, with a realtime web UI and a
Telegram bot. It supports three formats: EK (эрудит-квартет), OD/KVRM
(командная викторина с раундами по минуте) and KSI (командная своя игра). The
domain is Russian, and so is the UI.

## Stack
- **Backend**: Go 1.26, SQLite 3 (WAL mode, modernc.org/sqlite)
- **Frontend**: strict-TypeScript ES modules (root ADR-0001). The sources are in `dope/web/ts/`. The shared root toolchain (`just build-web`, which uses esbuild and the native tsc) bundles them into `dist/`, which is gitignored and embedded when Go builds the binary. There is a bundle per game page, plus self-contained bundles for the builder pages. No framework is used.
- **Frontend tests**: deno (`deno test --parallel`, in `dope/web/jstest/`)
- **Build/run**: `justfile` (see commands below)
- **Deploy**: `just deploy`, which calls the monorepo's `../deploy.py` (SSH-based)
- **Production** runs on `ssh vps2day-ee`. Use it to run commands on the production server.

dope is one module of the **dopesuite** monorepo. The module root is this
`dope/` directory (`go.mod: module "dope"`), not the root of the repo. The rules
that apply to the whole monorepo — git workflow, toolchain, cross-module
recipes — are in the root `AGENTS.md`.

## Directory Structure

The Go code is split into **seven groups** inside the inner `dope/` directory.
There are no loose `.go` files at its top level. `ARCHITECTURE.md` describes
every package and the rules about which layer may import which.

```
dope/                    # module root (go.mod: module "dope")
  dope/                  # server tree — 7 groups, packages resolve as dope/dope/<group>/<pkg>
    cmd/                 # entry points: dope-server (thin main)
    server/              # package dopeserver — the orchestration trunk + server/tests/ (integration)
    web/                 # HTTP/UI: route (the one dispatcher), pages, hostpages, editbatch, telegrambridge, assets (embed), jstest
    domain/              # game/fest logic: games, core, gamebuild, flatgame, resolver, roster, overrides, imports, entrants, numbering, towns, edit, view
    storage/             # persistence: store, journal, migrate, festwrite, festaccess, auditmw, storeutil, sqlitez, buffdb
    export/              # output: xlsxexport, gameexport
    platform/            # cross-cutting leaves: realtime, roles, markdown, session, metrics, util
scripts/
  loadtest/              # Real SSE load testing suite
  ek_restore/            # DB restoration tools
  cdp.py                 # Chrome DevTools Protocol driver (legacy; UI testing now via the verify skill)
justfile                 # All task runner commands
.env.example             # Config template
../deploy.py             # SSH deployment — the monorepo's shared script
```

## Key Files

`server/` (package `dopeserver`) is the trunk of the program. It wires up the
mux, the write-transaction rules and SSE, and it imports the other groups
directly. Each of its files covers one concern:

| File | Lines | Purpose |
|------|-------|---------|
| `server/main.go` | ~830 | Entry point, mux wiring, HTTP server, SSE event handlers |
| `server/routes_api.go` | ~500 | The **route table** for `/api/fest/` and `/api/auth/` (ADR-0016). Each endpoint is one row: the mux pattern, its `route.Access` level (Read, Editor, Manager and so on, with `.Numbered()` to add the numbering guard), and the handler. The handlers are in the same file and all look like `func(w, r, route.Scope) error`. A handler never checks the method, never checks a guard and never calls `http.Error`: the dispatcher in `web/route` resolves `{fest}` and `{game}`, the session and the role, and writes out whatever error the handler returns |
| `server/routes_api_tokens.go` | ~50 | `/api/auth/tokens`: the caller's API tokens (ADR-0021). The tokens themselves live in `domain/core/apitoken.go`, and `Engine.LookupSession` reads `Authorization: Bearer …` before the cookie |
| `web/hostpages/api.go` | ~710 | **The host pages' JSON twins** (ADR-0021), registered into the `/api` table by `APIRoutes`: fests, access, games and their settings, teams and Flags, player overrides, troikas, rating import, numbers, the history and revert. Each route calls the same function its form does (`CreateFest`, `UpdateGameSettings`, `AssignFestNumbers`, `RevertGame`…). A new host form is not finished until its twin is here |
| `server/routes_fest.go` | ~105 | The `/fest/` viewer table: the public fest page, the game pages (with the `/static` snapshot handle and the xlsx download) |
| `server/scoped_api.go` | ~310 | What the scoped endpoints share below HTTP: match scope resolution, broadcasts, the state PUT, screen settings, match-view loaders, reseed |
| `web/hostpages/routes.go` | ~120 | The `/host/` table (`Server.routes()`), with the host denial policy: no session or no role → back to `/host`, the wrong role → 403 |
| `web/hostpages/server.go` | ~60 | `Server`, built on `pages.Host`, and `festPage(w, r, festID, build)`. Every host fest page goes through that one loader: it reads the fest header, turns `ErrNoRows` into a 404, calls `build`, and renders the result with `RenderDoc`. `pages.Host` exposes the engine and the few operations the pages share; a page that needs the database itself writes `s.h.Engine().DB` |
| `web/route/route.go` | ~460 | **The dispatcher**. It holds `Table.Handle(pattern, access, handler)`, the `Access` levels, `Scope`, the denial policies, `Status` errors and `WriteError`, the JSON writers and the same-origin check. `route_test.go` holds the access matrix, which covers every combination of level, caller, and public or private fest |
| `server/db.go` | ~110 | DB open (`openFestDB`), active context, id resolution |
| `server/migrations.go` | ~1400 | The schema, written as a list of `[]schema.Migration`, plus the backfill functions they call. `dopecore/schema` applies each one exactly once, in order. A new step gets the next number and goes at the end. `server/tests/testdata/schema.sql` records what the list produces from an empty file; regenerate it with `DOPE_UPDATE_SCHEMA=1`. To rehearse the migrations against a copy of production, run with `DOPE_REHEARSE_DB=<snapshot>` |
| `server/auth.go` | ~600 | Sessions, password login, and dope's adapter for the Telegram handshake. The state machine itself is in `dopecore/tglogin`; dope supplies the write transaction, its own users table with an `is_system` column, and its own error text |
| `server/matchview.go` | ~815 | Fest/match view loading + match-update application |
| `server/import_scheme.go` | ~120 | The pasted-scheme importer (`/api/import`, `/api/fest/{fest}/scheme-import`, the host form): clears the fest, calls `gamebuild.Materialise` |
| `server/static_mode.go` | ~440 | The "DDoS lockdown". `staticGovernor.step` is the pure hysteresis logic, `lockdownServes` decides per request whether to serve the static copy, `spliceInit` is the one place the init payload is spliced in, and there is a snapshot cache |
| `server/serve_html.go` | ~360 | Builds the HTML init payloads for the host, viewer and game pages, and versions the assets. `canEdit` is the only place where the init payload looks at the role |
| `server/host_accessors.go` | ~190 | Dependency-inversion adapter (`*server` → leaf `Host` interfaces) |
| `server/testapi.go` | ~185 | The single exported test seam for `server/tests/` |

The heavy domain and persistence logic lives in the leaf groups:
`storage/store` (schema, queries, the view and scheme types, and pure scoring),
`storage/journal` (the forward journal), `domain/imports` (bulk roster and
rating import), `domain/core` (the `Engine`) and `export/*` (xlsx and json
export). Undo and redo over the audit log are in `domain/core`, together with
`storage/auditmw` and `storage/migrate`.

### Frontend (sources `dope/web/ts/`; `assets/static/` keeps only styles.css + dist/)

| File | Lines | Purpose |
|------|-------|---------|
| `styles.css` | ~4500 | dope's **app CSS layer** and nothing else: the tournament tables, grids, screen and stickers, dope's own variables, and the dark-theme overrides. The shared design system — tokens, controls, buttons, chrome, utilities and themes — is in DopeUIKit's `assets/core.css`, about 1030 lines. The server serves `/static/styles.css` as core.css and this layer concatenated (`kit.Assets`, called from `dope/server/css.go`). The tournament rules used to live in `core.css` and were moved down here, so **do not add tournament-specific rules to the kit**. They belong in this file. |
| `pageforms.ts` | ~60 | Shared behaviour for the server-rendered builder pages, replacing the inline `on*` handlers they used to carry (CSP-friendly, data-attribute driven: `[data-confirm]`, `[data-select-all]`, `[data-autosubmit]`, `[data-dialog-open="id"]`, `[data-dialog-close]`) |
| `ek.ts` | ~3450 | The ЭК page, for both the host and spectators: editing match scores, undo and redo, the stage panes and SSE sync. A `viewer` flag taken from the URL prefix disables every control. It imports the shared modules listed below, plus `stage-cache.ts` and `game-tabs.ts` |
| `od.ts` | ~3800 | The OD/KVRM page for host and viewer: the results and input sheets as tabs, navigation between entry cells, and SSE sync |
| `od-protocol.ts` · `ksi-protocol.ts` · `brain-protocol.ts` · `multi-protocol.ts` · `troika-protocol.ts` | ~330 · ~280 · ~35 · ~215 · ~135 | **A Protocol's document, as the page reads it** (ADR-0018). Each file holds the shape of the state, a `parseState(raw, …)` that converts whatever the server stored into something the renderers can trust (padded grids, normalised shootout rounds, the sticker grid, the two брейн sides), and the arithmetic over it. For ОД that is `questionStats`, the totals, the tour sums, the rating, the shootout tiebreak, `placesFor` and `rows`. For КСИ it is `rulesOf`, `markContribution`, `computeThemeValue`, `scoreSheet` and `rankedResultRows`. For Мультиигры it is `rulesOf` (the мини-игры and the value domain of each column), `scoreSheet` and `rankedResultRows`. For Тройка it is `parseState`, `questionScore` (each correct answer is worth the тема's нарицательная on its own) and `swapFrom`. All of it is pure: every function is given the state it reads, so the pages pass theirs and `jstest/*-protocol.test.js` passes fixtures |
| `cells.ts` | ~140 | The cell primitives that every table is built from: `th` and `td` with the CellSpec grammar, display formatting (the U+2212 minus sign), `nameNode`, and the small DOM helpers `setText`, `cssEscape` and `isFormControl` |
| `score-table.ts` | ~570 | The score table of a бой: `buildFlatScoreTable` and `buildTwoRowScoreTable`, `computePlaces`, and the node index together with `patchScoreTable`, which updates the table in place from a MatchView. The builders place and pin the leading columns (row marker, name, Σ, place and its gap) themselves; `scoreSheetPins({rowMarker, place, total})` is that pinned block, and a format passes its own through the `pins` option (Хамса's wider Σ, ОД's detailed sheet with `place: false`) |
| `sheet-pins.ts` | ~115 | **Pinned columns, declared once.** `declarePins(columns, {start})` takes a sheet's pinned columns in order, each `{key, width}` with the width as CSS (the var() its width rule reads), and returns `Pins`: `offsets` (each a calc() of the widths before it, so a phone's media-query widths move the columns after it), `end`, `mark(cell, key)` (writes `position: sticky` and `left` on the cell and adds `.pinned`, plus `.pin-edge` on the last column; a key the sheet did not declare is left to scroll), `markSpan` (a cell over the whole block, Хамса's раунд row) and `markTrailing` (an element that rides just past the block: Мультиигры' мини-игра name, Составы' «Игроки» head). `bindPinnedScroll(frame)` toggles `.pins-scrolled` on the frame, which draws the one scrolled-under cue off `.pin-edge`; `mountGamePage` binds it on every page's sheet frame. No CSS rule places a pinned cell. `jstest/sheet-pins.test.js` asserts the offsets |
| `standings.ts` | ~190 | How the pages draw the tables the server sends (ADR-0011). `standingsTable` is the single builder behind every table shaped like standings: пересев, группы, статистика, площадки, составы and the брейн crosstab. `resultsTeamCell` is a results table's name cell, built on `name-cell.ts`. `standingsTable` pins the first place and name column (`resultsPins`, which ОД, КСИ, КД and Участники use for the tables they build by hand). There is also `buildGroupStandingsView` and the fest-view stage helpers `festLetters`, `letteredTitle` and `stageType` |
| `name-cell.ts` | ~340 | **The one clipped name.** `nameCell(text, {tag, className, number, badges, city, layout, ariaLabel, popoverText, href, shrink})` builds every team, player and venue name a table can clip, in one vocabulary: `.name-cell` (beside the family classes its table lays it out by, such as `results-team`, `ek-team-cell`, `od-detailed-team-cell`, `brain-name-head`, `grid-slot-team`, `venue-label`), `.name-cell-wrap`, `.name-cell-text` and the hidden `.popover-inline` the page's one popover reads. `bindNameCells(root)` is the one measuring pass, bound by `mountGamePage` (and the gallery): a MutationObserver, a ResizeObserver on every name and the `change` event mark cells dirty, and one frame reads every width before it writes `.name-cell-truncated`. `shrink` is ЭК's stage sheet, where a name steps its font down to two lines before it fades. `markNameControl` makes a seat picker (`seat-picker.ts`) a name cell measured by its control's label. A page draws name cells and never measures them; `styles.css` has one fade rule for all of them ("clipped names") |
| `seat-picker.ts` | ~300 | **The one seat picker**: who sits in a seat, on every бой sheet (ЭК's and Эрудит-Секстет's theme, Тройка's chair, брейн's question, Хамса's theme). `seatPicker({roster, seated, cap, disabled, title, nobody, line, dataset, onChange})` returns `{element, seated(), update(seated)}`. Players are `{id, name}` with an id the page chooses (a name where the document stores names, a player id as text where it stores ids), and nobody is `[]`; the page converts its document's value at its own edge. One seat is a `<select>`, more is a button opening one tickbox panel parked on `<body>`. The wrap (`.seat-picker`) is a name cell with the chevron and the fade, measured by the shell's pass. `seatPickerOf(node)` finds a control's picker, which is how `score-table.ts`'s in-place patch updates ЭК's pickers; `SEAT_PICKER_SELECTOR` is the cursor kinds' selector. `jstest/seat-picker.test.js` drives it |
| `screen-board.ts` | ~210 | The ОД Экран (the projector board), with no DOM involved: `ScreenSettings` and `normalizeScreenSettings`, `CITY_COUNTRY` and `teamFlag`, `packRows` (which fills column by column and leaves a gap between place groups), and `planScreen(rows, metrics, settings)`, which returns `{columns, zoom, teamCol}`. od.ts measures one probe column, passes the pixel sizes in, and then paints the plan it gets back. `jstest/screen-board.test.js` tests the packing and the choice of zoom |
| `venue.ts` | ~200 | Площадка: `normalizeVenue`, the `formatVenue*` helpers and `buildVenuesTable` (for a host: rename, delete an unused venue, and the add row under the table). fest-grid imports it as well |
| `fest-roster.ts` | ~170 | Составы: `fetchFestRoster`, which caches per fest, plus `buildRosterTable` and `buildRosterView` |
| `ek-stats.ts` | ~225 | The folds behind ЭК's Статистика tab (`computeEKPlayerStats` and `computeIndividualPlayerStats`) and the tables that show them. It is the counterpart of `brain-stats.ts` and `group-stats.ts` |
| `game-shell.ts` | ~320 | **The game shell**, and two entry points. `mountGameDocument(spec)` (ADR-0018) handles the lifecycle of a page whose entire document is one state blob, which is ОД and КСИ: it composes the loader, the live events and the scoped writer on the game-state scope over the page's own `adopt` and `apply` callbacks, and returns `scope`, `load`, `save`, `overlay` and `isPending`. `mountGamePage(spec)` is what every game page mounts before it draws anything of its own: the ☰ jump links and downloads, the banner about unnumbered teams, the status dot and viewer counter (`indicator`), the client recorder, `renderChrome()` (which draws the header trail, including the «Мои фесты» crumb on the /host tree only, and sets `document.title` to `game · fest` or `section · fest`), and host presence, whose cursors are a declared list of element kinds — a selector plus the `data-*` keys the sheet cursor uses. It also binds the name-cell pass (`name-cell.ts`), the name popover on `<body>` and the pinned-column scroll cue on the sheet frame (`sheet-pins.ts`). A page is then left with only its data adopters and its renderers |
| `bout-page.ts` | ~630 | **The bout page**: `mountBoutPage(spec)` is the lifecycle of a Game whose document is many бои, each its own match state (брейн, Хамса, Тройка). It is `mountGameDocument`'s counterpart and mounts the same shell. The page gives it `parse` (a бой's view to the state the page trusts), `buildTab`, the roster tab's builder and a few format options (which tabs fit the frame, an in-place `repaint`). The module owns the rest: the бои and their parsed states, the `fest:`, `match:<game>:`, `venues:<fest>` and `game-roster:<game>` scopes, the writer with the recorder and the recovery of un-acked edits after a reload, the boot order (cursors, stream, fetch, then presence), the render loop with the tab bar in the URL (`url-state.ts`) and `redrawSteady`, the venues, and the host's writes (finish, venue and start time, the draw, the reseed). It also exports `finishControl` and `venueEditor`, which ЭК uses too. `jstest/bout-page.test.js` drives it with a fake stream and fetch |
| `game-page.ts` | ~330 | The page plumbing: the contract for the window globals (init payloads and menu chrome), route parsing, the header breadcrumb trail (🏠 / [Мои фесты] / фест / игра / раздел, which mirrors the URL, with the Мои фесты crumb on the /host tree only), the menu jump and download mounts, the localStorage snapshot cache, and the game-data loader that tries the init payload, then the cache, then a fetch |
| `sheet-cursor.ts` | ~590 | **The sheet cursor**: one active-cell selection shared by every editable grid, which is the ЭК бой and stage sheets, КСИ, брейн and ОД's entry grid. A page describes its grid — rows and columns, which may be ragged on either axis, plus `coordOf` and `cellAt` — and says how to apply a value. The cursor owns everything else: click, shift-click and drag ranges; the arrow keys with clamping (on the stacked stage sheet, ЭК spilling into the next бой is just arithmetic); Home and End; the mark keys and Delete over the selection; copy and paste as a tab-separated grid; the tap-cycle on touch; and the highlight on the active cell and row. The geometry, the key reading, the clipboard grammar and the mark tokens (`parseMark` accepts +/−, 1/0, q/й, w/ц, п/м and the words) are all pure and tested |
| `widgets.ts` | ~600 | The interaction widgets: the cell navigation bar, the virtual keypad, the floating name popover (`floatingPopover`, whose one spec in `NAME_POPOVER_SPECS` is the name cell's), the sync-status dot, the scroll edges and fades, the tab bar and the viewer counter. Measuring a clipped name is not here: it is `name-cell.ts` |
| `state-sync.ts` | ~1500 | **The SSE engine**, as two primitives that every game page composes. `createLiveEvents` handles reading: it takes a scope map saying what each delta chains onto and who adopts the result, and it deduplicates by seq, detects gaps, resets on a new epoch, recovers when iOS wakes the tab, and accepts an injected stream for tests. `createScopedWriter` handles writing: cell patches are coalesced per scope, structural changes go through `send` with an intent, both are overlaid on every view until the server acks them, and they are retried, persisted to localStorage and flushed when the page is hidden. `createSyncIndicator` derives the status dot from all this. The file also holds the client recorder and host presence |
| `si.ts` | ~1750 | The KSI (team jeopardy) page: the question and answer tables, the team and player rows, and the detailed, results and refusals tabs |
| `multi.ts` | ~370 | The Мультиигры page. Each мини-игра gets a block of columns with its own subtotal, Итог comes last, and Σ+ is shown only for a мини-игра that can take points away. The numeric cells cycle through a small set of values when clicked, and accept typing when the set is wide |
| `brain.ts` · `hamsa.ts` | ~790 · ~820 | The брейн and Хамса pages, mounted on `bout-page.ts`: each keeps its parse step, its бой sheet, its tabs and its sheet cursor |
| `troika.ts` | ~1060 | The Тройка page, mounted on `bout-page.ts`. A бой has three chair rows per side, across темы of three вопросы each. A «рассадка» column appears before every тема at which a side turned round. The page also draws the группа tables and the Статистика fold |
| `crosstable.ts` | ~165 | The группа cross-table that every two-seat format draws: who played whom, how the бой ended, and the block's standings columns next to it. It also exports `slotKey`, `crossSlot` and `standingsByParticipant`, which a caller needs in order to feed it. Both Брейн and Тройка use it, and a format that wants an extra column passes the name of the metric |
| `troika-stats.ts` | ~125 | Тройка's Статистика fold. It separates what a player answered first from what they repeated after someone else had already said it, and gives the rate of repeats out of the ones they had a chance at |
| `game-tabs.ts` | ~240 | The only place tabs come from: `gameTabs(stages, {game, viewer, seeded})`. It derives Blocks from `grain`, makes one круг tab across a Block's Groups, folds several reseeds into a single «Пересев», produces the Block and Group labels (`blockLabel`, `groupLabel`) and maps old URL hashes (`canonicalKey`). ЭК, брейн, КСИ, ЧГК and the Сетка's column titles just render what it returns and work nothing out for themselves |
| `url-state.ts` | ~65 | Where a page keeps its place in the URL. `tabFromHash` and `setHashTab` handle the hash, which names the tab (pass `canonicalKey` for брейн's and Тройка's old hashes). `param` and `setParam` handle the query string, which carries what the viewer chose to look at. Every write is a `replaceState` that rebuilds path, search and hash together, so writing one half never drops the other. `onNavigate` listens to both `hashchange` and `popstate`. ОД, КСИ, брейн, Тройка and Мультиигры all read their tab through it |
| `entrants.ts` | ~500 | The Участники tab every buzzer Game mounts (ADR-0023): the source picker with its зачёт filter, the entrant list with seed numbers, declines and the waiting list, and the hand edits — add, move, remove, rename a one-off. `createEntrantsTab({apiBase, onChanged, onRebuilt})` returns `element()`, `refresh()` for a fest event and `adopt()` for an init payload. The server decides every rule and answers the tab afresh after each write |
| `divisions.ts` | ~80 | A Division (зачёт) as a viewer looks at it (ADR-0020): `divisionsOf` (every distinct Flag among a game's teams, in the order first seen), `inDivision`, reading and writing `?division=`, and `divisionChipRow`, which is one `.match-tab` chip per Division. The ranking itself stays in the two Protocol modules, which are given the rows a Division holds |
| `fest-grid.ts` | ~920 | The Сетка. `planGrid` runs first and is pure: it decides what each column is, the shared row unit, how far each box spans, and how a Block is packed (`packBlock`). The painters then read that plan and draw it: бой boxes and Group tables, both built from the same `.grid-slot-cell` cells inside one `.grid-box`, plus the reseed panels. Its team names are `name-cell.ts` cells, and a long box title fades the kit's way (`.u-clip-fade`). The module keeps no state of its own — each grid it draws holds its own Blocks and letters, so two grids can sit on one page |
| `menu.ts` (kit) | — | The site-wide chrome, exposed as `window.dopeMenu`: the theme and contrast toggle, the hamburger menu and the account links. It lives in dopeuikit (`assets/ts/`), is served at `/static/menu.js`, and is loaded on every page |
| `stage-cache.ts` | 289 | The shared pane cache for EK (`createStageCache`): match state per stage, prefetching with deduplication, and SSE routing. Used by `ek.ts` |
| `login.ts` (kit) | — | The multi-step login UI: the username step, then either the password or the code branch, then a redirect on success. It lives in dopeuikit's `assets/ts/` |
| `profile.ts` | 49 | The password form. It has two modes: setting a new password, and changing an existing one |
| `gallery.ts` | ~140 | The gallery at `/gallery`, which only works in dev mode. It puts every shared table and the Сетка, built from fixtures, on a single page. `scripts/matrix.py` (`just matrix`, the verify skill's hand-over matrix) shoots this page first, because a change to a table skin shows up here in four screenshots instead of a hundred |

**How the modules fit together** (ADR-0003, amended by root ADR-0001; ADR-0015
covers the shell, the engine and the cursor). Each game page loads exactly ONE
bundle, `dist/<page>.js`, built from `dope/web/ts/pages/<page>.ts`. That bundle
first reads the init payload and then boots the page module, which starts
itself. The SSE engine is `state-sync.ts`, and every page composes
`createLiveEvents` with `createScopedWriter`. ОД and КСИ register a single
game-state scope; ЭК registers one per бой, and брейн, Хамса and Тройка get theirs from `mountBoutPage`. Both primitives accept an
injected stream and fetch so tests can drive them. To use something from another
file, import it by name from the module that owns it. There is no file that
re-exports other modules. The only globals we publish are `window.dopeMenu` and
`window.dopeMenuConfig`, typed in dopeuikit's `globals.d.ts`, plus the init
payloads the server inlines. The frontend tests import per-file ESM that is
emitted to `web/jstest/dist/`.

## How to Run / Build / Test
```bash
just dev              # Server (hot reload from disk); polls telegram if TELEGRAM_BOT_TOKEN is set
just test             # Go tests plus the deno JS tests, including the studchr replays over the direct transport (~25 s). This is the conformance gate.
just test-full        # the same, plus the studchr replays over HTTP (~90 s, covering the handlers, auth and the write path). Run this before a merge.
just test-js          # Frontend tests only
just fmt              # gofmt
just vet              # go vet
just check            # this module: fmt + vet + tidy-check + test-full
just pre-commit       # the whole repo, via the root justfile. Run before committing.
just cli              # build dope-cli into ~/.local/bin (the dope-api skill)
just deploy           # SSH deploy to VPS
just invite [days]    # Generate invite code
```

Server listens on port **9672** by default (override with `$PORT`). Database defaults to `fest.db` (override with `$DOPE_DB`).

## Architecture Patterns

**Realtime SSE sync**: a global `server.mu` RWMutex guards the state, and a separate `server.subMu` guards the SSE subscribers. Subscriptions are held in a map per fest. Clients receive delta events and detect gaps by epoch and seq; when they resync they get a full snapshot.

**Audit log**: every mutation is written to the `audit_log` table by `storage/auditmw`. Undo and redo are in `domain/core/revert.go`. Old log entries are compressed by `storage/sqlitez/audit_compress.go` and then pruned by age and by disk size. The code that converts audit and history data lives in `storage/migrate`.

**Auth**: sessions are kept in HTTP-only cookies. The roles are ordered `system → organizer → host → viewer`. An API token (ADR-0021), made on `/profile`, acts as its user on every route except changing the password or the username and `/admin`; changing the password revokes them all. Agents use it through `dope-cli` and the `dope-api` skill. The Telegram bot talks to the server through endpoints protected by a shared secret.

**Assets**: the `web/assets` package embeds them with `//go:embed static`, and `server` serves them. ETags are content hashes, which is what busts the cache. In dev mode the files are read from `dope/web/assets/static` on disk instead, so they reload without a rebuild.

**Writes**: there is a single global write lock, and SQLite runs in WAL mode, so writes are serialised. Broadcasts are sent only after the transaction commits. A slow-write canary reports when writes start contending.

**Game types**: each format (EK, ES, OD, KSI, SI, brain, Multi, Troika, Hamsa, the friendship cup) is a `games.Definition` plus a Protocol. Everything the rest of the code asks about a format is a fact on one of the two, and nothing outside `domain/games` and `domain/protocol` compares or switches on a game-type code (ADR-0027, enforced by `domain/games/guard_test.go`). ARCHITECTURE.md lists what adding a format takes.

## Testing UI Changes
Use the `verify` skill, in `.claude/skills/verify/` at the repo root. It drives
a persistent headless Chrome from the shell with `agent-browser`, and documents
how to log in, walk a flow, take screenshots and emulate a phone.

`just matrix` (`scripts/matrix.py`) is the skill's hand-over matrix as a tool.
It builds the working tree, seeds its own fixture fest with `dope-server
seed-fixture`, shoots 27 pages in four combinations of phone/desktop and
light/dark, and compares each one against the golden image committed in
`scripts/matrix-goldens`. It takes about a minute, so it is meant to be run on a
commit. When a change to the UI is intended, run `just matrix --bless` to adopt
the new screenshots, and commit them together with the change.

## UI markup (DopeUIKit)
Never write HTML by hand. **DopeUIKit** (`pecheny.me/dopeuikit`, used through
`replace => ../dopeuikit`) has two layers. `ui/` is the generic DSL **engine**:
the parser, validator, expansion framework, printer, builder and codegen. It
knows no vocabulary and no CSS class names. `kit/` is the shared **design
system**: the core vocabulary, the expanders, Chrome, the generated builder,
`core.css` and the fonts. `dope/web/ui` is dope's thin **overlay** on top of the
kit, and it imports `pecheny.me/dopeuikit/kit`.

- **Static pages** are written in `.dopeui` files, in `dope/web/assets/ui/`:
  login, ek, od, si and brain. They are made of typed primitives such as `page`,
  `gametopbar` and `mount`, and dope's `App` compiles them to HTML at startup
  (`dope/web/ui/app.go`, `Compile`). The overlay adds the game topbar, the
  mounts, dope's page kinds and the `init` marker prop: `init="__EK_INIT__"`
  emits exactly the byte-string that `serve_html.go` later replaces with the
  per-request JSON payload. The engine and kit are specified in DopeUIKit's
  `DESIGN.md`, and dope's overlay in `dope/web/ui/vocab.json` and `expand.go`.
- **Header path**: the `publictopbar` of every server-rendered page carries a
  `crumbs` trail, built by the helpers in `web/pages/crumbs.go`
  (`HostCrumbs`, `FestCrumbs`, `AdminCrumbs` and `Trail`). That way no page has
  to spell out where it sits. These bars used to carry a lone `←` back link as
  well; it was removed, because the second-to-last crumb goes to the same place
  and also says what that place is. The primitive itself belongs to DopeUIKit,
  and the game pages draw the same classes from the client side
  (`game-page.ts`).
- **Dynamic pages** are built with the same package's typed builder (`Render`),
  in `dope/web/pages/` and `dope/web/hostpages/`. They are: admin, audit,
  journal, register and numbers; the host pages dash, games, home, teams,
  imports and players; and the two public pages, the fest index at `/` and a
  fest's own page (`hostpages/public_pages.go`). No hand-written
  `html/template` strings are left anywhere. The public pages were the last two
  to be converted, which is why they kept a bare "←" for a while after every
  other page had moved to the crumb trail. A fest's markdown description reaches
  the page through the `richtext` primitive and `ui.Raw`, which is the engine's
  only way to emit unescaped HTML — never build a `Raw` out of request data.
  These pages used to carry inline `on*` handlers; those moved to
  `pageforms.js` and are now triggered by `data-*` attributes such as
  `data-confirm`, `data-autosubmit` and `data-dialog-open`. Never add an inline
  handler back, because CSP forbids them.
- **Scripts**: the sources are strict-TypeScript ES modules. A page loads the
  built bundles through the `classicscripts` prop of `page` (`dist/<name>.js`),
  and `menu.js` always boots first. The vocabulary is closed: an unknown
  primitive or prop, an invalid enum value or a duplicate id is a compile
  error.

## Design System
When you build a new page or UI component, you MUST use the design system that
already exists. That means two CSS files: DopeUIKit's `assets/core.css`, which
holds the shared tokens, controls, buttons, chrome, utilities and themes, and
dope's own `styles.css` layer, which holds the tournament-specific classes. Use
their CSS variables for colours, spacing and typography, and their layout grids,
table styles and component classes. Use the shared JS building blocks too:
`cells.ts`, `standings.ts`, `score-table.ts`, `window.dopeMenu` from the kit's
`menu.ts`, and so on. Don't write a one-off style or hand-roll a widget when the
design system already has one.

Follow this order, strictly:
1. **Reuse** an existing variable, class or component as it is.
2. If something really is missing, **extend the design system**. Add a
   tournament-specific class to dope's `styles.css` layer, or, if it is genuinely
   shared, add a token or primitive to DopeUIKit's `core.css` or kit, which both
   apps use. Do that instead of solving it locally. A new token follows the
   existing naming, and should itself be built out of existing variables wherever
   that is possible.
3. Only as a last resort, add styling that belongs to one page, and write a
   comment explaining why. Before you do, think again about whether step 2 is
   really not the answer.

This is what keeps every page looking consistent and themable: light, dark and
high-contrast are all derived from the same shared variables.

## CSS convention
Every CSS value must come from a variable. Don't put a literal value on a class.

## Reuse
Look for an existing function or class before you write a new one.

## Deployment
Run `just deploy`. It already does everything that is needed.

Only deploy to production from `main`: merge the branch, push `main` to origin,
and then deploy. Never deploy a branch to production. If you want to test a
branch live, deploy it to staging instead, with `just deploy-staging`, which
goes to dopetest.

## Production Server
- **Access**: `ssh vps2day-ee` (login user is `ap`; host `vm46153`). Some paths need `sudo` (systemd hardening hides them). If you are already on this host, skip the `ssh` and run commands directly.
- **Live DB**: `/var/lib/dope/fest.db`, SQLite in WAL mode, with the `-wal` and `-shm` files next to it. This is the real production database. `/home/ap/fest.db` is a stale copy, *not* the live one.
- **Services** (systemd): `dope.service` (live match server, binary `/opt/dope/dope-server`, `WorkingDirectory=/var/lib/dope`, `PORT=8090`, `EnvironmentFile=-/etc/dope.env`, `ReadWritePaths=/var/lib/dope`) and `dope-bot.service` (Telegram bot). Inspect with `systemctl cat dope.service`; find the live DB via `sudo ls -l /proc/$(systemctl show -p MainPID --value dope.service)/fd | grep .db`.
- **Backups**: ad-hoc `*.bak` snapshots live alongside the DB in `/var/lib/dope/` plus a `/var/lib/dope/backups/` dir.
- **Taking a consistent backup**: there is no `sqlite3` CLI on this box, and the service keeps the database open, so take the snapshot online through Python: `python3 -c "import sqlite3; src=sqlite3.connect('/var/lib/dope/fest.db'); dst=sqlite3.connect('<dest>'); src.backup(dst)"`. Never just `cp` the `.db` file on its own, because that misses the WAL.
