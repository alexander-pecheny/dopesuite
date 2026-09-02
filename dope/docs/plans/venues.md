# Venues — implementation plan

Agreed 2026-09-02. Vocabulary is in `dope/CONTEXT.md` (Venue, Representative,
Слот, Registration, Заявка, Состав, Голосование, Спорный) — read those entries
first and use those words in code and UI. ADR-0020 records the buff coupling.

Deliver as four commits on this branch, in this order, each building and
passing `just check` on its own. Do not push. Do not open PRs.

Reference files:
- Export layouts: `~/tournament-tours-10233.xlsx` (one row per team per tour,
  header `Team ID, Название, Город, Тур, 1..N`, a contested answer is its
  text in the cell) and `~/tournament-with-players-10233.xlsx` (`Место, Team
  ID, Название, Город, Флаг, IDplayer, Фамилия, Имя, Отчество`, one row per
  player).
- Prod buff.db schema: `tournaments(id, name, date_start, date_end,
  tournament_type, questions_by_tour, …)`, `players(id, name, patronymic,
  surname)`, `tournament_results(id, team_id, team_current_name,
  team_current_town, …)`. No teams or towns table. Copy a slice of it to
  `dope/.tmp/buff-sample.db` for local work: `ssh vps2day-ee python3 -c …`
  selecting ~2000 players, tournaments with `date_end >= '2026-08-01'`, and
  the results rows of tournament 10233.
- rating API: `GET https://api.rating.chgk.net/seasons` (id, dateStart,
  dateEnd); `GET /teams/{id}/seasons?idseason=N` → `[{idplayer, idseason,
  idteam, dateAdded, dateRemoved, playerNumber}]`.

## Commit 1 — buff: mirror base rosters

Repo `git@code.pecheny.me:pecheny/chgkbuff_reborn.git`, clone to
`~/chgkbuff_reborn`, branch `team-seasons`. Python only (`update_db.py`);
buff's ADR-0001 keeps the updater in Python.

- `DB_INIT` gains
  `seasons(id integer primary key, date_start text, date_end text)` and
  `team_seasons(team_id integer, season_id integer, player_id integer,
  date_added text, date_removed text, player_number integer,
  primary key(team_id, season_id, player_id))`, plus indexes
  `idx_players_surname on players(surname)`,
  `idx_tournament_results_team on tournament_results(team_id)`.
  Run the `CREATE … IF NOT EXISTS` statements on every start so an existing
  db picks them up.
- Nightly `update()` refreshes `seasons`, then for every team id seen in the
  results rows it touched this run, replaces that team's rows for the current
  season (delete + insert) from `/teams/{id}/seasons?idseason=current`.
  Keep the existing `req_sleep` pacing.
- `backfill_team_seasons.py`: every distinct `team_id` with a result in a
  tournament whose `date_start` falls in the current or previous season,
  same replace-per-team, resumable (skip teams already present unless
  `--force`), logs progress every 100 teams. ~0.5 s per team.
- Tests where the repo has them (`internal/store` tests read the schema; keep
  them green — the Go server does not use the new tables).
- Commit. Report the branch name; deployment (pull on the box, run the
  backfill under `nohup` as `ap`) is done by the reviewer after review.

## Commit 2 — dope: venues, slots, registration, заявки, составы, export

### Schema (migration 27, `dope/server/migrations.go`, regenerate the pinned
`schema.sql` with `DOPE_UPDATE_SCHEMA=1`)

- `fests`: add `kind text not null default 'fest'` (`fest`|`venue`),
  `city text not null default ''`, `rating_venue_id integer`.
  `rating_id` keeps meaning "rating tournament id" and stays null on venues.
- `slots(id, fest_id → fests, game_id → games unique, starts_at text,
  rating_tournament_id integer, reg_token text unique, reg_opens_at text,
  reg_closed integer default 0, created_at, updated_at)`.
- `slot_applications(id, slot_id → slots, user_id → users, status text
  check in (pending, accepted, declined), participant_id → participants
  null, created_at, updated_at, unique(slot_id, user_id))`.
- `slot_application_versions(id, application_id → slot_applications, seq
  integer, team_name text, rating_team_id integer, roster_json text,
  created_by → users, created_at, unique(application_id, seq))`. The
  current заявка is its highest seq. Revert = a new version copying an old
  one. `roster_json`: `[{"player_id":24850,"surname":"…","name":"…",
  "patronymic":"…","captain":true}]`; `player_id` 0 means hand-typed.

### buff access — `dope/dope/storage/buffdb` (or wherever ARCHITECTURE.md
puts read-only external stores)

`Open(path)` returns a store or a `Disabled` one when the path is empty or
the file is missing; every method on `Disabled` returns empty results. Env
`DOPE_BUFF_DB`. Queries:
- `Players(prefix, limit)` — surname prefix (`like 'X%'`), then name.
- `TeamName(id)` — latest `team_current_name` for a team id.
- `Teams(prefix, limit)` — distinct team names by prefix.
- `BaseRoster(teamID, at time)` — player ids from `team_seasons` for the
  season containing `at`; `(nil, false)` when the team has no rows.
- `PlayableTournaments(on time)` — `date_start <= on <= date_end`, types
  Синхрон, Строго синхронный, Асинхрон, ordered синхроны first, then by
  date_start.
- `Tournament(id)` — name and `questions_by_tour`.
Table-missing errors are treated as empty (fail soft, ADR-0020).
Unit tests run against a fixture sqlite built in the test.

### Access

Extend the route access resolution (`dope/web/route`, `festaccess`): a user
may **read** every page of a game whose fest is a venue if they are a fest
member or own an accepted `slot_applications` row for that game's slot.
Venue games are never public-read, whatever `is_public` says. Add the cases
to `route_test.go`'s matrix.

### Public pages (`dope/web/hostpages/public_pages.go` style, dopeuikit)

- `/` gets a link «Площадки» to `/venues`; the fest index query filters
  `kind='fest'`.
- `/venues`: a table of public venues — name (link), city, next slot date,
  registration state, accepted-team count, rating venue link — with one text
  input that filters rows client-side on any cell. Sort by next slot date,
  venues without one last.
- `/venue/{slug}`: description, rating link, upcoming slots (date, tournament
  name if set, registration «открыта / откроется <когда> / закрыта»,
  accepted count) and past slots (date, tournament). Private → 404.
- `/reg/{token}`: slot, venue, date, tournament if set. Not open yet →
  «Регистрация откроется …». Open, not logged in → the Telegram login
  handshake with return-to this page. Logged in → the заявка form (team name,
  rating team id with the buff team name shown beside it once typed,
  состав editor) or the user's existing заявка with its status; editable in
  any status. Closed → only the user's own заявка, still editable.
- Состав editor (small TS module, `dope/web/ts/roster-editor.ts`): rows of
  a suggest input over `/api/buff/players?q=` showing «Фамилия Имя Отчество
  (id)», a «нет в базе» fallback that turns the row into three typed name
  fields with id 0, and a captain radio. Flags are computed server-side on
  save and shown read-only: К captain, Б in `BaseRoster`, Л otherwise;
  team id 0 → Б for everyone but the captain. Warn above six players.

### Host pages

- `/host`: a «Создать площадку» form beside the fest one: name, slug, city,
  rating venue id, description, public. Creates a `fests` row with
  `kind='venue'` and the creator organizer row.
- Venue dashboard (`/host/fest/{venue}`): the existing fest dash minus what
  does not apply, plus a slot list and «Новый слот» (datetime, tournament
  suggest). Creating a slot creates its ОД game (`game_type` as dope names
  ОД, `team_list_source='game'`, `roster_source='game'`, tour composition
  from `questions_by_tour` when the tournament is set) and its token.
- Slot page `/host/fest/{venue}/slot/{slot}`, one long page:
  1. Header: datetime, tournament (suggest over
     `/api/buff/tournaments?q=`; setting it fills the tour composition),
     game status, «Клонировать» → modal with datetime prefilled +7 days;
     clone copies game settings and a fresh token, `reg_opens_at` shifted
     by the same delta if set; no tournament, no заявки, no voting.
  2. Links: registration URL with a copy button, `reg_opens_at` field,
     closed toggle, «Сменить ссылку» (new token).
  3. Заявки: filing order; columns team, rating id (link to
     `rating.chgk.info/teams/{id}`), submitter (`t.me/{username}` or
     `tg://user?id=` when no username), filed, last edit, roster size,
     flags summary, actions Принять / Отклонить / Вернуть в ожидание. Row
     expander: every version with author, time, diff-ish summary, «Вернуть
     эту версию», and an inline edit form (same editor as `/reg`) that saves
     a new version by the Representative.
     Accept: create `participants` (+ `game_participants`) with the next free
     Number, link `fest_teams` by `rating_team_id` when non-zero (create the
     registry row if absent), write `participant_players` from the roster.
     Decline / Вернуть: unseat (delete participant rows) — refuse with 409
     if the game already holds results for that Number.
     Roster versions after acceptance rewrite `participant_players`.
  4. (voting — commit 3)
  5. Downloads: `…/export/tours.xlsx` and `…/export/players.xlsx`; link to
     the game page.
- Exports (`dope/export/xlsxexport`): reuse `BuildODSheet` for tours; add
  the players sheet: Место from the game's standings (ties shared), Team ID
  (0 allowed), Название, Город (fest_teams.city or the venue city), Флаг,
  IDplayer, Фамилия, Имя, Отчество. Golden-test both against the two
  reference workbooks' layout (headers, row shape), not their data.

### API (`routes_api.go`)

- `GET /api/buff/players?q=`, `GET /api/buff/teams?q=`,
  `GET /api/buff/team/{id}`, `GET /api/buff/tournaments?q=&on=` — session
  required, thin JSON over buffdb.
- `POST /reg/{token}` and the host actions as form posts, following the
  host routes' existing style (`route.Table`, `Denial` policies).

## Commit 3 — voting

- Schema (migration 28): `slot_votings(id, slot_id unique, token unique,
  kind check in (one, any, ranked), per_team integer, opens_at, closes_at,
  candidates_json, frozen integer default 0, created_at)`;
  `slot_ballots(id, voting_id, user_id, team_name text, choice_json,
  discarded integer default 0, created_at, unique(voting_id, user_id))`.
- Host slot page section 4: «Создать голосование» → the candidate list from
  `PlayableTournaments(slot.starts_at)`, синхроны first then асинхроны, all
  ticked; an «добавить по id» input; kind radio; per-team checkbox; opens /
  closes datetimes. Once created: the link + copy, the live tally, ballots
  with «Отклонить» per ballot. Candidates freeze at the first ballot.
- `/vote/{token}`: before opens_at → when it opens; open → login then the
  ballot (radio / checkboxes / three ordered selects), team name field in
  per-team mode, one ballot per user, re-vote replaces; after closes_at →
  the tally for anyone with the link.
- Tally (pure Go, unit-tested): one → count; any → count ticks; ranked →
  3/2/1/0 Borda. Per-team mode groups ballots by trimmed, case-folded team
  name and counts each team once (latest ballot). Discarded ballots are
  excluded.

## Commit 4 — спорные

- Schema (migration 29): `od_contested(id, game_id → games, question
  integer, participant_id → participants, answer text, accepted_here
  integer default 0, created_by → users, created_at, unique(game_id,
  question, participant_id))`.
- API: `GET/POST/PATCH/DELETE /api/fest/{fest}/game/{game}/contested`
  (editor for writes, the game's readers for GET). The game document the page
  reads (ADR-0013) carries the list so the client never fetches it
  separately; the SSE state push includes it.
- Ввод (`od.ts` `entryCell` / `applyEntryCellDisplay`): typing `?` in a
  question's cell opens a modal: team number (prefilled from the row if any),
  answer text. Save stores the row and shows the cell as the team number on
  yellow; the entry itself is **not** written as taken. A cell with a pending
  спорный is not counted by `questionStats`/`teamTookQuestion`;
  `accepted_here` ones are counted as taken in Итог only.
- Подробно (`buildDetailedScoreTable`): yellow cell for any спорный; an
  extra mark (e.g. «✓» suffix and a distinct kit tone) for `accepted_here`.
- Итог (`resultsAnswerCell` and the tour/total cells): tour and total show
  `12 (+3?)` when the team has 3 pending спорных there; sorting unchanged.
- Host list: a «Спорные» section on the slot page (or a tab on the game)
  listing them with «Принят на площадке» toggle and delete.
- Export: tours sheet writes the answer text in the cell for every спорный,
  whatever `accepted_here` says.
- Deno tests for the pure parts (`od-protocol.ts` counting, Итог label);
  `verify` + `design-review` on all three tabs.

## Conventions the reviewer will check

- `just check` green at every commit; the pinned schema regenerated.
- One comment per hundred lines at most; English in code and docs, Russian
  only in host-facing strings and domain nouns.
- New UI built from kit primitives; run `design-review` and `verify`
  (headless Chrome via agent-browser) on `/venues`, `/venue/{slug}`,
  `/reg/{token}`, `/vote/{token}`, the slot page and the ОД tabs.
- Never `pkill`; dev servers are killed by port.
