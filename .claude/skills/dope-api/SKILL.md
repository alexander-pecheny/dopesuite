---
name: dope-api
description: Change dope fests from the shell through the API with dope-cli and an API token — create fests and games, edit settings, access, teams and Flags, game rosters, troikas, numbers, enter and fix results, read the history and revert. Use this whenever a task asks to change or inspect a dope fest (dope.pecheny.me or a staging instance) without clicking through the browser.
---

# Working on a dope fest through the API

Everything an organizer can do on `/host/…` has an API twin (dope ADR-0021).
`dope-cli` sends the requests, authenticated with an API token that acts as the
user who made it.

Build/install: `cd dope && just cli` → `~/.local/bin/dope-cli`.

## Before you can do anything

The token is the **user's** to make. You cannot make it yourself, so ask:

1. They open `/profile` on the instance, create a token under «API-токены» and
   copy it (it is shown once).
2. `dope-cli login --url https://dope.pecheny.me --token <token>` (or pipe it in,
   or set `DOPE_TOKEN`). `dope-cli whoami` shows whose token it is.

`DOPE_URL` points the CLI at another instance for one command, for example a
branch staging site `https://<name>.dopetest.pecheny.me`. **Try a change on
staging first** when it is more than a small correction.

## The commands

```
dope-cli fests                                  # the fests you have a role on
dope-cli fest <id|slug>                         # settings, games, roster counts, your role
dope-cli api GET  /api/fest/<fest>/…            # any endpoint below
dope-cli api POST /api/fest/<fest>/… '{"…":…}'  # body inline, @file.json, or - for stdin
dope-cli api POST …/seed-import/xlsx --file seeds.xlsx
dope-cli api GET  …/export.xlsx --out results.xlsx
```

JSON answers are printed indented. A refusal exits 1 and prints the status and
the server's message, which is written for a person (in Russian). Read it and
act on it: it names the row, the team or the rule.

`<fest>` and `<game>` in a path take the id or the slug.

## Endpoints

Roles: **R** anyone who can see the fest, **M** any role on it (host, admin,
creator), **A** admin or creator (building the fest), **V** an admin, or a host
limited to площадки for those площадки's teams alone (ADR-0032), **C** the creator only. The live-editing writes marked ⓝ refuse with 409 until every team
in the game has a number.

**Fests and access**

| | |
|---|---|
| `GET /api/fests` | your fests |
| `POST /api/fests` | `{title, slug?, description?, start_date?, end_date?, rating_id?, is_public?, kind?}` → the fest. `kind` is `regular` (the default), `synch` or `multi` (ADR-0031) |
| `GET …/settings` M | header with `kind`, your `role`, `games` (each with its `multifest_venue_id`), roster counts, and a synch fest's `multifest_venues` (площадки) |
| `PATCH …/settings` A | any header fields; those left out keep their value. a new `kind` deletes the fest's games and площадки, so a fest with games refuses it unless the body has `replace_games: true` (**ask the user first**); a new synch fest with a `rating_id` then loads its площадки and their games |
| `DELETE /api/fest/<fest>` C | deletes the fest and everything in it |
| `GET …/access` A | members and roles; a host limited to some Games carries their ids in `games`, and one limited to площадки theirs in `multifest_venues` |
| `POST …/access` A | `{changes: [{user, role: "admin"\|"host"}, {user, remove: true}]}` or `{lines: "user:role\n…"}`. A change may carry `games: [<id\|code\|slug>, …]` to limit a host to those Games (`[]` lifts it), and `multifest_venues: [<id\|title>, …]` to limit them to those площадки: their Games, and their teams and numbers to edit; with or without a role |
| `GET …/invites` A | the host invite links of every площадка and Game: `id`, `multifest_venue_id` or `game_id`, `opens` (its title), `label`, `state`, `used`, `left`, `url`, who `joined` |
| `POST …/games/<game>/invites` A | `{label?, max_uses?, ttl_hours?}` mints a link to that Game; whoever follows it becomes a host limited to it → the list |
| `POST …/multifest-venues/<id>/invites` A | the same for a площадка: whoever follows it becomes a host limited to that площадка |
| `POST …/invites/<id>/revoke` A · `DELETE …/invites/<id>` A | stop a Game's link, or remove it with its history; who came in stays a host. A площадка's link is under `…/venue-invites/<id>` |
| `POST /api/join/<code>` | follow a link as yourself → `{fest_id, game_id}` |

**Games**

| | |
|---|---|
| `GET …/entrants` A | whom a game may seat: `id` for `entrants`, `ref` for `entrant_refs` (a rating player not seated anywhere yet has only a ref) |
| `POST …/games` A | `{game_type, title?, slug?, entrants?, …}`, see below → the game. An empty `title` is the format's |
| `GET\|PATCH …/games/<game>/settings` A | `{title, slug, scheme_dsl, hidden_divisions, outside_divisions, grid_width}` (`grid_width`: the Сетка's column width in px, 160–600, 0 for the default); `scheme_dsl` changes only for a format whose scheme its page edits (brain, ЭК, ЭС, личная СИ, Тройка, Хамса), a flat Game refuses a changed one, and so does a Game pasted as JSON before 2026-10-06, which has no `scheme_dsl` (production's chr2026/ek-3); a scheme that does not compile answers 400 with the compiler's message; `hidden_divisions` lists the зачёты (Flag short names) the game does not offer, out of the read-only `divisions` its teams carry (ОД, КСИ, Мультиигры), and is written as sent; or PATCH `shown_divisions` instead, the offered ones to show, as the page ticks them: the other offered ones are hidden, and one hidden before that no team carries now stays hidden; `outside_divisions` (ОД) lists the зачёты whose teams play вне зачёта — below everyone else in «Все», «в/з» for a place, ranked as usual in their own зачёт |
| `POST …/games/<game>/shape` A | a Мультиигры game's mini-games again, on a game already created: `{spec, sorting}` in the creation form's `multi_games` and `multi_sorting` syntax; `GET …/settings` answers the current ones as `shape`. A mini-game keeps its results under its name and its tasks' results by position; a spec that would drop an entered value (a mini-game removed or renamed, a task removed, a value its task no longer takes) answers 400 with the reason. Answers the settings |
| `POST …/games/<game>/clear` A | back to just-created, keeps the id and the URLs |
| `DELETE …/games/<game>` A | |
| `GET …/games/<game>/journal` A | history, newest first; each entry has `revert_to` |
| `POST …/games/<game>/revert` A | `{target: <revert_to>}` undoes that entry and everything after it |

`game_type` is `od` (`od_tours`, `od_questions`), `kd` — Кубок Дружбы
(`od_tours`, `od_questions`, `kd_tables`, a prime no smaller than the tours;
register a player with one PATCH op per card, path `["players", "<card>"]`,
value `{name, team}`, and free a card with value `null`; cards run 1…tables²;
`GET …/results` answers the personal standings), `ksi` (`ksi_themes`),
`ksi_stickers` (`ksi_themes`, `stickers: {neutral|x2|nowrong|emptywrong: {color,
max}}`), `multi` (`multi_games`, `multi_sorting`, in the creation form's
grammar), or `brain`, `si`, `troika`, `hamsa`, `ek`, `es` with `dsl` in the scheme
language (dope/docs/scheme-dsl.md). Every one of them needs a `dsl`: no game is
created from pasted JSON. Leaving `entrants` out seats everyone.

**Roster**

| | |
|---|---|
| `POST …/rating-import` A | pulls the roster from rating.chgk.info (the fest needs `rating_id`). In a `synch` fest it also puts every team on its площадка and gives each площадка without a game an ОД of the tournament's tours and merges the host's edits over it. `{preview: true}` answers the `plan` (teams added, dropped, renamed, players per team, edits kept, `conflicts`) and writes nothing: **preview first, and show the user**. A conflict is a player the site moved away from where the host put them; the host's placement stays unless its `key` is in `accept_site`. A 409 lists `dropped` and `added` teams: repeat with `{merge: {"<fest team id>": <new rating id>}, drop: [<fest team id>]}` after **asking the user** |
| `POST …/rating-import/undo` A | puts the roster back as it was before the last import; refused once the roster was edited by hand since |
| `GET …/teams` V | teams with their `flags`, and `hand` / `edited` for teams the host made or changed |
| `GET …/teams/{id}` · `GET …/teams/new` V | one team with its `players` and every fest person to suggest (`choices`) |
| `POST …/teams` · `PUT …/teams/{id}` V | `{name, city, players: [{rating_id, first_name, last_name}], multifest_venue_id?}`: make a team by hand, or set a team's name, city and people. In a fest with площадки a team made by hand plays at `multifest_venue_id` (a площадка host's own one when left out). A rating id of 0 is a person the site does not know. Adding someone who plays for another team moves them. A re-import keeps these edits (ADR-0024) |
| `DELETE …/teams/{id}` V | takes a team without results off the roster; a rating team stays off through imports |
| `GET …/rating/players?q=` · `GET …/rating/teams?q=` V | the rating site's people and teams for a name's start (buff's mirror, else the site's API). `POST …/teams` takes a `rating_id` to add a site team, under a one-off `name` if wanted |
| `GET …/rating/teams/{id}/base` V | a site team's current base roster: `{team, players}` |
| `GET …/teams/export.xlsx` · `POST …/teams/xlsx` A | the roster as a sheet, a row per person; a sheet loads back (multipart `file`, `?preview=1` first) and sets every team it names, leaving the rest |
| `PATCH …/teams/flags` A | `{flags: {"<team id>": "МЮ, Студ"}}`; teams left out keep theirs |
| `GET …/players` A | every fest player and the team they play for |
| `GET …/games/<game>/roster` R | the roster each team plays this Game with, `hand` for one the host keeps by hand; `?choices=1` adds the fest's players to suggest |
| `PUT …/games/<game>/rosters/<participant>` M | `{players: ["Имя Фамилия", …]}` keeps that team's roster in this Game by hand (ЭК, ЭС, брейн, Хамса, КСИ): exactly these people. A person from another team plays this Game for this one, and a seed by players' places counts them here. Not empty, nobody twice, nobody dropped who has something entered for the team |
| `DELETE …/games/<game>/rosters/<participant>` M | gives the team back the roster it brought, under the same rule |
| `GET\|POST …/troikas` A | list / add `{lines: "Имя: Игрок, Игрок, Игрок\n…"}` |
| `PUT\|DELETE …/troikas/<id>` A | `{name, players: [...], applied?}`; `applied` moves the troika to that place in the order of applications (the list's `applied`), which a troikas list follows and `seed: players` breaks its last tie by |
| `GET …/numbers` V | teams with numbers. In a fest with площадки each numbers its own teams, and every numbers call there needs `?venue=<id>` (the ids are in `…/settings` → `multifest_venues`) |
| `POST …/numbers/assign` V | `{assignments: [{team_id, number}]}`; others keep theirs, a moved number leaves its old holder, `number: 0` takes a team's number away |
| `POST …/numbers/match` V | `{text: "<n>\t<team>\n…"}` proposes pairs, saves nothing |
| `POST …/numbers/auto` · `…/numbers/clear` V | 1…n alphabetically / none |

**Results (live editing, what the game pages send)**

| | |
|---|---|
| `GET /api/fest/<fest>` R · `GET …/games/<game>` R | the fest view: stages, standings |
| `GET …/games/<game>/state` R | a whole-document game (ОД, КСИ, Мультиигры): the document |
| `PATCH …/games/<game>/state` M ⓝ | `{ops: [{op: "set"\|"remove", path: [...], value}]}` into that document |
| `GET …/games/<game>/matches/<code>` R | one бой of a bracket game (ЭК, брейн, СИ, Тройка, Хамса, ЭС) |
| `PATCH …/matches/<code>/state` M ⓝ | ops into the бой: `path: ["participants", <slot>, …]` |
| `POST …/matches/<code>/finish` M ⓝ | `{finished: true\|false}` |
| `POST …/matches/<code>/venue` M | `{number, hosts?}`: one of the game's venues; `hosts` are the бой's own (a final run by the head judge), `""` its venue's |
| `POST …/matches/<code>/slot` M | `{with: "<code>"}`: the two бои of one Round swap their slots — заход, venue, time and place in the order; each keeps its letter and seats, so the bracket is untouched. Refused for a бой that has begun, бои of different Rounds (or groups), and a swap that seats someone twice in one заход |
| `POST …/stages/<stage>/reseed` M ⓝ · `PUT …/draw` A ⓝ | reseed a stage / seat a drawn slot `{slot, participant}` |
| `GET …/seed-import` M · `POST …/seed-import/{ksi,run,xlsx,decline}` M | seeds |
| `GET\|PUT …/games/<game>/screen-settings` | the projector board |
| `GET /api/fest/<fest>/rooms` R | the fest's Площадки (ADR-0037): `rooms` (`id`, `title`, `games` whose venues are in it), every бой played in one (`bouts`: `room`, `venue`, `startsAt`, `game`, `gameTitle`, `code`, `letter`, `finished`, `hosts`), the `games` that have venues, and whether the caller may edit (`editable`). The page is `/fest/<fest>/venues` |
| `POST …/rooms` M · `PUT …/rooms/<id>` M · `DELETE …/rooms/<id>` M | add `{title}` (the room, «Б-414»; one title per fest, case, spaces and ё aside), rename `{title}`, delete one no game's venue is in (the refusal names the games). Each answers as the GET |
| `GET …/games/<game>/venues` R | the game's venues: the numbers its scheme deals, each with its map line: `room` (id), `title` (the room's), `hosts`, and how many `bouts` sit there. A бой's head reads its room («Б-414 18:00»), or «пл. N» with none |
| `PUT …/games/<game>/venues` M · `PUT …/games/<game>/venues/<n>` M | the game's **venue map**: `{venues: [{number, room?, hosts?}]}` for any number of venues in one call, or `{room?, hosts?}` for one. `room` is a room's id or its title as the fest has it (a title it has no room for is refused; add the room first), `""` takes the venue out of its room; a field left out stays. A number the game does not deal is refused. The tab's «Скопировать из игры» is this call with another game's rooms. Each answers with the game's venues |
| `GET …/results` · `…/export.xlsx` R · `…/export.json.gz` M | results |

## Rules that matter

- **Read before you write, and read again after.** Learn the shape of a document
  or a бой from `GET …/state` or `GET …/matches/<code>` before you send ops, and
  check the answer, which is the committed state. Never guess a path.
- **Every write is in the game's history under the user's name**, and viewers
  see it live. `GET …/journal` is how you show what you did. `revert` is how you
  undo it, and it also undoes everything after it, including other people's
  edits. So check the journal for later entries before you revert.
- **Ask before anything destructive**: deleting a fest or a game, `clear`,
  a rating import whose preview drops teams or players, a
  `revert` over someone else's edits, `drop` in a rating import,
  `numbers/clear` or `numbers/auto` on a numbered fest.
- **The token cannot change the password, the username or reach /admin.** If
  the token stops working (the CLI says «токен не принят»), it expired, was
  revoked, or the password changed. The user makes a new one.
- Names, cities and flags are Russian. Keep them as the user wrote them.
