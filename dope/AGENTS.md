# Codebase Map

## What This Is
Tournament/championship management system with real-time web UI and Telegram bot. Handles EK (эрудит-квартет), OD/KVRM (командная викторина с раундами по минуте), and KSI (командная своя игра) formats. Russian-language domain.

## Stack
- **Backend**: Go 1.26, SQLite 3 (WAL mode, modernc.org/sqlite)
- **Frontend**: strict-TypeScript ES modules (root ADR-0001), sources in `dope/web/ts/`; the shared root toolchain (`just build-web`, esbuild + native tsc) bundles them into the gitignored `dist/` embedded at go-build time — per-page game bundles plus self-contained builder-page bundles. No framework.
- **Frontend tests**: deno (`deno test --parallel`, in `dope/web/jstest/`)
- **Build/run**: `justfile` (see commands below)
- **Deploy**: `just deploy`, which calls the monorepo's `../deploy.py` (SSH-based)
- **Production** is at `ssh vps2day-ee`, use it to run commands on production server

dope is one module of the **dopesuite** monorepo; the module root is this `dope/`
directory (`go.mod: module "dope"`), not the repo root. See the root `AGENTS.md`
for the monorepo rules (git workflow, toolchain, cross-module recipes).

## Directory Structure

The Go code is organised into **seven semantic groups** under the inner `dope/`
directory (no loose `.go` files at its top level). See `ARCHITECTURE.md` for the
full package-by-package breakdown and the layering rules.

```
dope/                    # module root (go.mod: module "dope")
  dope/                  # server tree — 7 groups, packages resolve as dope/dope/<group>/<pkg>
    cmd/                 # entry points: dope-server (thin main), telegram-bot
    server/              # package dopeserver — the orchestration trunk + server/tests/ (integration)
    web/                 # HTTP/UI: route (the one dispatcher), pages, hostpages, editbatch, telegrambridge, assets (embed), jstest
    domain/              # game/fest logic: games, core, gamebuild, flatgame, resolver, roster, overrides, imports, numbering, edit, view
    storage/             # persistence: store, journal, migrate, festwrite, festaccess, auditmw, storeutil, sqlitez
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

The orchestration package `server/` (package `dopeserver`) is the trunk; it wires
the mux, the write-tx discipline and SSE, and imports the groups directly. Its
files, each one concern:

| File | Lines | Purpose |
|------|-------|---------|
| `server/main.go` | ~830 | Entry point, mux wiring, HTTP server, SSE event handlers |
| `server/routes_api.go` | ~500 | The `/api/fest/` and `/api/auth/` **route table** (ADR-0016): one row per endpoint — mux pattern, `route.Access` (Read / Editor / Manager…, `.Numbered()` for the numbering guard), handler — and the handlers, each `func(w, r, route.Scope) error`. No method checks, no guards, no `http.Error` in a handler: the dispatcher in `web/route` resolves `{fest}`/`{game}`, the session and the role, and writes the error a handler returns |
| `server/routes_fest.go` | ~105 | The `/fest/` viewer table: the public fest page, the game pages (with the `/static` snapshot handle and the xlsx download) |
| `server/scoped_api.go` | ~310 | What the scoped endpoints share below HTTP: match scope resolution, broadcasts, the state PUT, screen settings, match-view loaders, reseed |
| `web/hostpages/routes.go` | ~120 | The `/host/` table (`Server.routes()`), with the host denial policy: no session or no role → back to `/host`, the wrong role → 403 |
| `web/hostpages/server.go` | ~60 | `Server` over `pages.Host` and `festPage(w, r, festID, build)` — the one loader every host fest page goes through: fest header, `ErrNoRows` → 404, build → `RenderDoc`. `pages.Host` hands out the engine and the few operations the pages share; a page that needs the DB says `s.h.Engine().DB` |
| `web/route/route.go` | ~460 | **The dispatcher**: `Table.Handle(pattern, access, handler)`, `Access` levels, `Scope`, the denial policies, `Status` errors + `WriteError`, the JSON writers, the same-origin check. `route_test.go` holds the access matrix (every level × caller × public/private fest) |
| `server/db.go` | ~110 | DB open (`openFestDB`), active context, id resolution |
| `server/migrations.go` | ~1400 | The schema as a list: `[]schema.Migration` (`storage/schema` applies them once each, in order) and the backfills they call; a new step takes the next number and goes at the end. `server/tests/testdata/schema.sql` pins what the list makes of an empty file (`DOPE_UPDATE_SCHEMA=1` regenerates); `DOPE_REHEARSE_DB=<snapshot>` walks a prod copy through them |
| `server/auth.go` | ~600 | Sessions, password login, the Telegram handshake's adapter (the state machine is `dopecore/tglogin`; dope brings its write tx, its users table with `is_system`, its error text) |
| `server/matchview.go` | ~815 | Fest/match view loading + match-update application |
| `server/import_scheme.go` | ~110 | The pasted-scheme importer (`/api/import`, the host form): clears the fest, calls `gamebuild.Materialise` |
| `server/static_mode.go` | ~425 | "DDoS lockdown" static-snapshot degradation layer |
| `server/serve_html.go` | ~360 | Host/viewer/game HTML init payloads + asset versioning; `canEdit` is the one place the init payload asks the role |
| `server/host_accessors.go` | ~190 | Dependency-inversion adapter (`*server` → leaf `Host` interfaces) |
| `server/testapi.go` | ~185 | The single exported test seam for `server/tests/` |

Heavy domain/persistence logic lives in the leaf groups: `storage/store` (schema,
queries, view/scheme types, pure scoring), `storage/journal` (forward journal),
`domain/imports` (bulk roster/rating import), `domain/core` (the `Engine`),
`export/*` (xlsx/json export). Audit-log undo/redo lives in `domain/core` +
`storage/auditmw`/`migrate`.

### Frontend (sources `dope/web/ts/`; `assets/static/` keeps only styles.css + dist/)

| File | Lines | Purpose |
|------|-------|---------|
| `styles.css` | ~4500 | dope's **app CSS layer** only (tournament tables/grids/screen/stickers + dope vars + dark overrides). The shared design system — tokens, controls, buttons, chrome, utilities, themes — lives in DopeUIKit's `assets/core.css` (~1030 lines); the server serves `/static/styles.css` as core + this layer concatenated (`kit.Assets`, called from `dope/server/css.go`). The tournament domain used to live in `core.css`; it was moved down here, so **do not add tournament-specific rules to the kit** — they belong in this file. |
| `pageforms.ts` | ~60 | Shared behaviour for the server-rendered builder pages, replacing the inline `on*` handlers they used to carry (CSP-friendly, data-attribute driven: `[data-confirm]`, `[data-select-all]`, `[data-autosubmit]`, `[data-dialog-open="id"]`, `[data-dialog-close]`) |
| `ek.ts` | ~3450 | The ЭК page, host and spectator alike — match score editing, undo/redo, stage panes, SSE sync; a `viewer` flag from the URL prefix gates every control. imports the shared modules below + `stage-cache.ts` + `game-tabs.ts` |
| `od.ts` | ~3800 | OD/KVRM host/viewer — tabbed results/input sheets, entry cell navigation, SSE sync |
| `od-protocol.ts` · `ksi-protocol.ts` · `brain-protocol.ts` | ~330 · ~280 · ~35 | **A Protocol's document as the page reads it** (ADR-0018): the state shape, `parseState(raw, …)` — the adapter from whatever the server stored to what the renderers may trust (padded grids, normalised shootout rounds, the sticker grid, the two брейн sides) — and the arithmetic over it (ОД: `questionStats`, totals, tour sums, the rating, the shootout tiebreak, `placesFor`, `rows`; КСИ: `rulesOf`, `markContribution`, `computeThemeValue`, `scoreSheet`, `rankedResultRows`). Pure: every function takes the state it reads, so the pages call them with theirs and `jstest/*-protocol.test.js` with fixtures |
| `cells.ts` | ~140 | Cell primitives every table is built from — `th`/`td` with the CellSpec grammar, display formatting (U+2212 minus), `nameNode`, the small DOM helpers (`setText`, `cssEscape`, `isFormControl`) |
| `score-table.ts` | ~530 | A бой's score table — `buildFlatScoreTable`/`buildTwoRowScoreTable`, `computePlaces`, and the node index + `patchScoreTable` that update it in place from a MatchView |
| `standings.ts` | ~190 | The server's tables as the pages draw them (ADR-0011): `standingsTable` (the one builder behind every standings-shaped table: пересев, группы, статистика, площадки, составы, the брейн crosstab), `resultsTeamCell` (the one fading name cell), `buildGroupStandingsView`, and the fest-view stage refs (`festLetters`, `letteredTitle`, `stageType`) |
| `screen-board.ts` | ~210 | The ОД Экран (projector board) without the DOM: `ScreenSettings` + `normalizeScreenSettings`, `CITY_COUNTRY` + `teamFlag`, `packRows` (column-major, a gap between place groups) and `planScreen(rows, metrics, settings) → {columns, zoom, teamCol}` — od.ts measures one probe column, hands the px in, and paints the plan; `jstest/screen-board.test.js` drives the packing and the zoom choice |
| `venue.ts` | ~75 | Площадка — `normalizeVenue`/`formatVenue*` and `buildVenuesTable`; fest-grid imports it too |
| `fest-roster.ts` | ~170 | Составы — `fetchFestRoster` (cached per fest), `buildRosterTable`, `buildRosterView` |
| `ek-stats.ts` | ~225 | ЭК's Статистика folds (`computeEKPlayerStats`, `computeIndividualPlayerStats`) and their tables; the sibling of `brain-stats.ts` and `group-stats.ts` |
| `game-shell.ts` | ~320 | **The game shell** — `mountGameDocument(spec)` (ADR-0018): the lifecycle of a page whose whole document is one state blob (ОД, КСИ) — loader, live events and scoped writer on the game-state scope composed over the page's `adopt`/`apply` callbacks; `scope`, `load`, `save`, `overlay`, `isPending`. And `mountGamePage(spec)` — `mountGamePage(spec)`: what every game page mounts before it draws its own thing: the ☰ jump links and downloads, the unnumbered-teams banner, the status dot + viewer counter (`indicator`), the client recorder, `renderChrome()` (the header trail — «Мои фесты» only on the /host tree, derived from the route — and `document.title` as `game · fest` or `section · fest`), and host presence whose cursors are a declared list of element kinds (selector + the data-* keys the sheet cursor uses); a page keeps only its data adopters and renderers |
| `game-page.ts` | ~330 | Page plumbing — window-globals contract (init payloads, menu chrome), route parsing, the header breadcrumb trail (🏠 / [Мои фесты] / фест / игра / раздел, mirroring the URL; the Мои фесты crumb only on the /host tree), menu jump/download mounts, localStorage snapshot cache, init/cache/fetch game-data loader |
| `sheet-cursor.ts` | ~590 | **The sheet cursor** — one active-cell selection for every editable grid (ЭК бой and stage sheets, КСИ, брейн, ОД's entry grid): a page describes its grid (rows/cols, may be ragged on either axis, coordOf/cellAt) and applies values; the cursor owns click/shift/drag ranges, arrows with clamping (ЭК's spill into the next бой is plain arithmetic on the stacked stage sheet), Home/End, mark keys and Delete on the selection, copy/paste as a tab grid, the touch tap-cycle, the active cell and row highlight. The geometry, key reading, clipboard grammar and the mark tokens (`parseMark`: +/−, 1/0, q/й, w/ц, п/м, the words) are pure and tested |
| `widgets.ts` | ~660 | Interaction widgets — cell nav bar, virtual keypad, floating popovers, sync-status dot, name overflow, viewer counter |
| `state-sync.ts` | ~1500 | **The SSE engine** — two primitives every game page composes: `createLiveEvents` reads (a scope map: what a delta chains onto, who adopts the result; seq dedupe, gap, epoch reset, iOS-wake recovery, stream-injectable for tests) and `createScopedWriter` writes (cell patches coalesced per scope, structural `send` with an intent, both overlaid on every view until acked, retried, persisted to localStorage, flushed on hide); `createSyncIndicator` derives the status dot; client recorder, host presence |
| `si.ts` | ~1750 | KSI (team jeopardy) page — question/answer tables, team/player rows, detailed/results/refusals tabs |
| `game-tabs.ts` | ~240 | The one place tabs come from: `gameTabs(stages, {game, viewer, seeded})` — Blocks off `grain`, круг tabs across a Block's Groups, reseeds folded into one «Пересев», Block/Group labels (`blockLabel`, `groupLabel`), legacy hashes (`canonicalKey`). ЭК, брейн, КСИ, ЧГК and the Сетка's column titles render it and derive nothing |
| `fest-grid.ts` | ~920 | The Сетка — `planGrid` first (pure: what each column is, the shared row unit, each box's span, a Block's packing via `packBlock`), then the painters read the plan: бой boxes and Group tables built of the same `.grid-slot-cell` cells in one `.grid-box`, reseed panels, truncated team names. No module state: each drawn grid keeps its own Blocks and letters, so two on a page coexist |
| `menu.ts` (kit) | — | Site-wide chrome (`window.dopeMenu`) — theme/contrast toggle, hamburger menu, account links. Lives in dopeuikit (`assets/ts/`), served at `/static/menu.js`, loaded on every page |
| `stage-cache.ts` | 289 | Shared pane cache (`createStageCache`) for EK — per-stage match state, deduped prefetch, SSE routing. Used by `ek.ts` |
| `login.ts` (kit) | — | Multi-step auth UI — username → password/code branch, redirect on success (dopeuikit `assets/ts/`) |
| `profile.ts` | 49 | Password change form (new password vs change password modes) |
| `gallery.ts` | ~140 | The gallery (`/gallery`, dev mode only): every shared table and the Сетка from fixtures on one page — the skin sheet `scripts/matrix.py` (`just matrix`, the verify skill's hand-over matrix) shoots first |

**Module seam (ADR-0003, amended by root ADR-0001; ADR-0015 for the shell, the engine and the cursor)**: each game page loads ONE `dist/<page>.js` bundle built from `dope/web/ts/pages/<page>.ts` — the init-payload boot, then the self-booting page module. The SSE engine is `state-sync.ts`: every page composes `createLiveEvents` + `createScopedWriter` (ОД/КСИ register one game-state scope, ЭК and брейн one per бой; both take injectable stream/fetch for tests). Cross-file wiring is named ES imports from the module that owns a symbol — there is no re-export desk; the only published globals are `window.dopeMenu`/`window.dopeMenuConfig` (typed in dopeuikit's `globals.d.ts`) and the server-inlined init payloads. Frontend tests import per-file ESM emitted to `web/jstest/dist/`.

## How to Run / Build / Test
```bash
just dev-web-only     # Server only. Usually you should run this unless you need to test changes related to bot
just dev              # Run server + bot concurrently (hot reload from disk)
just test             # Go tests + deno JS tests, incl. the studchr replays on the direct transport (~25 s) — the conformance gate
just test-full        # the same plus the studchr replays over HTTP (~90 s: handlers, auth, write path); run before a merge
just test-js          # Frontend tests only
just fmt              # gofmt
just vet              # go vet
just pre-commit       # fmt + vet + tidy-check + test (run before committing)
just deploy           # SSH deploy to VPS
just invite [days]    # Generate invite code
```

Server listens on port **9672** by default (override with `$PORT`). Database defaults to `fest.db` (override with `$DOPE_DB`).

## Architecture Patterns

**Real-time SSE sync**: Global `server.mu` RWMutex guards state; separate `server.subMu` for SSE subscribers. Per-fest subscription maps. Delta events with epoch/seq gap detection; full snapshots on resync.

**Audit log**: All mutations go to `audit_log` table via `storage/auditmw`. Undo/redo via `domain/core/revert.go`. Old logs compressed (`storage/sqlitez/audit_compress.go`) and pruned by age/disk size; audit/history data conversion lives in `storage/migrate`.

**Auth**: Session cookies (HTTP-only), role hierarchy `system → organizer → host → viewer`, per-fest scoped API tokens, Telegram bot bridges via shared-secret endpoints.

**Assets**: Embedded by the `web/assets` package (`//go:embed static`), served by `server`. Content-hash ETags for cache-busting. Dev mode reads from `dope/web/assets/static` on disk for hot reload.

**Write pattern**: Single global write lock + SQLite WAL → serialized writes. Broadcasts go out after commit. Slow-write canary detects contention.

**Game types**: EK, OD, KSI implemented as pluggable modules with independent question/match state.

## Testing UI Changes
Use the `verify` skill (repo root `.claude/skills/verify/`): `agent-browser`
drives a persistent headless Chrome from the shell (login/flow/screenshot/mobile
emulation all documented there). `just matrix` is the skill's hand-over matrix
as a tool: HEAD against the working tree, 22 pages × phone/desktop × light/dark,
pixel-diffed, in ~2.5 minutes (`scripts/matrix.py`).

## UI markup (DopeUIKit)
No hand-written HTML anywhere. **DopeUIKit** (`pecheny.me/dopeuikit`, vendored via
`replace => ../dopeuikit`) has two layers: `ui/` is the generic DSL **engine**
(parser, validator, expansion framework, printer, builder, codegen — no vocabulary,
no CSS class names) and `kit/` is the shared **design system** (core vocab +
expanders + Chrome + generated builder + `core.css`/fonts). `dope/web/ui` is dope's
thin **overlay** on the kit (imports `pecheny.me/dopeuikit/kit`).

- **Static pages** are authored in `.dopeui` (`dope/web/assets/ui/`: login, ek,
  od, si, brain) as typed primitives — `page`, `gametopbar`, `mount`… — compiled
  to HTML at startup by the dope `App` (`dope/web/ui/app.go`, `Compile`). The
  overlay adds the game topbar + mounts + dope page kinds + the `init` marker prop
  (`init="__EK_INIT__"` emits the exact byte-string `serve_html.go` splices the
  per-request JSON payload over). Spec: DopeUIKit `DESIGN.md` (engine + kit) +
  `dope/web/ui/vocab.json`/`expand.go` (dope overlay).
- **Header path**: every server-rendered page's `publictopbar` carries a
  `crumbs` trail built by the helpers in `web/pages/crumbs.go`
  (`HostCrumbs`/`FestCrumbs`/`AdminCrumbs` + `Trail`), so no page restates its
  ancestry. The lone `←` back link these bars used to carry is gone — the
  second-to-last crumb is the same destination and also says what it is. The
  primitive itself is DopeUIKit's; the game pages paint the same classes
  client-side (`game-page.ts`).
- **Dynamic pages** (admin/audit/journal/register/numbers + hostpages: dash,
  games, home, teams, imports, players, and the two public pages — the fest
  index at `/` and a fest's own page, `hostpages/public_pages.go`) are built with
  the same package's typed builder (`Render`) in `dope/web/pages/` and
  `dope/web/hostpages/`. There are no hand-written `html/template` strings left:
  the public pages were the last two, which is why they kept a bare "←" after
  everything else moved to the trail. A fest's markdown description reaches the
  page through the `richtext` primitive + `ui.Raw`, the engine's one unescaped
  escape hatch — never build a `Raw` from request data. Their former
  inline `on*` handlers moved to `pageforms.js`, keyed on `data-*` attributes
  (`data-confirm`/`data-autosubmit`/`data-dialog-open`/…) — never re-add inline
  handlers (CSP forbids them).
- **Scripts**: sources are strict-TS ES modules; pages load built bundles via the
  `page` `classicscripts` prop (`dist/<name>.js`), `menu.js` boots first. The
  vocabulary is closed — unknown primitive/prop, bad enum value, or duplicate id
  is a compile error.

## Design System
When building a new page or UI component, you MUST use the existing design system —
DopeUIKit's `assets/core.css` (shared tokens/controls/buttons/chrome/utilities/
themes) plus dope's `styles.css` layer (tournament-specific classes) — its CSS
variables (colors, spacing, typography), layout grids, table styles, and component
classes, and the shared JS building blocks (`cells.ts`, `standings.ts`, `score-table.ts`,
`window.dopeMenu` from the kit's `menu.ts`, etc.). Do not introduce bespoke one-off styles or
hand-rolled widgets when a design-system equivalent exists.

Order of preference, strictly:
1. **Reuse** an existing variable / class / component as-is.
2. If something is genuinely missing, **extend the design system** — add a
   tournament-specific class to dope's `styles.css` layer, or a genuinely shared
   token/primitive to DopeUIKit's `core.css`/kit (both apps consume it), rather
   than inlining a local solution. New tokens follow the existing naming and must
   themselves be built from existing variables where possible.
3. Only as a last resort, and with a comment explaining why, add page-local
   styling — but first reconsider whether step 2 is the right call.

This keeps every page visually consistent and themable (light/dark/high-contrast
all derive from the shared variables).

## CSS Convention
All CSS values must use variables — no static values on classes

## Reuse
Always reuse existing functions and classes before creating new ones

## Deployment Config
Run `just deploy` to deploy, it already handles everything that's needed.
Prod deploys come from `main` ONLY — merge the branch, push `main` to origin,
then deploy; never deploy a branch to prod. A branch may go to staging (`just deploy-staging` → dopetest)
for a live test.

## Production Server
- **Access**: `ssh vps2day-ee` (login user is `ap`; host `vm46153`). Some paths need `sudo` (systemd hardening hides them). If you are already on this host, skip the `ssh` and run commands directly.
- **Live DB**: `/var/lib/dope/fest.db` (SQLite WAL mode; `-wal`/`-shm` sidecars alongside). This is the real prod DB — *not* `/home/ap/fest.db` (stale copy).
- **Services** (systemd): `dope.service` (live match server, binary `/opt/dope/dope-server`, `WorkingDirectory=/var/lib/dope`, `PORT=8090`, `EnvironmentFile=-/etc/dope.env`, `ReadWritePaths=/var/lib/dope`) and `dope-bot.service` (Telegram bot). Inspect with `systemctl cat dope.service`; find the live DB via `sudo ls -l /proc/$(systemctl show -p MainPID --value dope.service)/fd | grep .db`.
- **Backups**: ad-hoc `*.bak` snapshots live alongside the DB in `/var/lib/dope/` plus a `/var/lib/dope/backups/` dir.
- **Consistent backup**: no `sqlite3` CLI on the box. The service holds the DB open, so snapshot online via Python: `python3 -c "import sqlite3; src=sqlite3.connect('/var/lib/dope/fest.db'); dst=sqlite3.connect('<dest>'); src.backup(dst)"` — never a bare `cp` of the `.db` alone (would miss the WAL).
