---
name: dope-api
description: Change dope fests from the shell through the API with dope-cli and an API token — create fests and games, edit settings, access, teams and Flags, player overrides, troikas, numbers, enter and fix results, read the history and revert. Use this whenever a task asks to change or inspect a dope fest (dope.pecheny.me or a staging instance) without clicking through the browser.
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
creator), **A** admin or creator (building the fest), **C** the creator only. The live-editing writes marked ⓝ refuse with 409 until every team
in the game has a number.

**Fests and access**

| | |
|---|---|
| `GET /api/fests` | your fests |
| `POST /api/fests` | `{title, slug?, description?, start_date?, end_date?, rating_id?, is_public?}` → the fest |
| `GET …/settings` M | header, your `role`, `games`, roster counts |
| `PATCH …/settings` A | any header fields; those left out keep their value |
| `DELETE /api/fest/<fest>` C | deletes the fest and everything in it |
| `GET …/access` A | members and roles; a host limited to some Games carries their ids in `games` |
| `POST …/access` A | `{changes: [{user, role: "admin"\|"host"}, {user, remove: true}]}` or `{lines: "user:role\n…"}`. A change may carry `games: [<id\|code\|slug>, …]` to limit a host to those Games (`[]` lifts it), with or without a role |

**Games**

| | |
|---|---|
| `GET …/entrants` A | whom a game may seat: `id` for `entrants`, `ref` for `entrant_refs` (a rating player not seated anywhere yet has only a ref) |
| `POST …/games` A | `{game_type, entrants?, …}`, see below → the game |
| `GET\|PATCH …/games/<game>/settings` A | `{title, slug, scheme_dsl}`; `scheme_dsl` only for brain |
| `POST …/games/<game>/clear` A | back to just-created, keeps the id and the URLs |
| `DELETE …/games/<game>` A | |
| `GET …/games/<game>/journal` A | history, newest first; each entry has `revert_to` |
| `POST …/games/<game>/revert` A | `{target: <revert_to>}` undoes that entry and everything after it |
| `POST …/scheme-import` A | a pasted JSON scheme; **replaces every game of the fest** |

`game_type` is `od` (`od_tours`, `od_questions`), `kd` — Кубок Дружбы
(`od_tours`, `od_questions`, `kd_tables`, a prime; players go into the state's
`players: [{card, name, team}]` by PATCH, and `GET …/results` answers the
personal standings), `ksi` (`ksi_themes`),
`ksi_stickers` (`ksi_themes`, `stickers: {neutral|x2|nowrong|emptywrong: {color,
max}}`), `multi` (`multi_games`, `multi_sorting`, in the creation form's
grammar), or `brain`, `si`, `troika`, `hamsa`, `ek`, `es` with `dsl` in the scheme
language (dope/docs/scheme-dsl.md). `ek` and `es` take a JSON `scheme` instead
of `dsl`. Leaving `entrants` out seats everyone.

**Roster**

| | |
|---|---|
| `POST …/rating-import` A | pulls the roster from rating.chgk.info (the fest needs `rating_id`) and merges the host's edits over it. `{preview: true}` answers the `plan` (teams added, dropped, renamed, players per team, edits kept, `conflicts`) and writes nothing: **preview first, and show the user**. A conflict is a player the site moved away from where the host put them; the host's placement stays unless its `key` is in `accept_site`. A 409 lists `dropped` and `added` teams: repeat with `{merge: {"<fest team id>": <new rating id>}, drop: [<fest team id>]}` after **asking the user** |
| `POST …/rating-import/undo` A | puts the roster back as it was before the last import; refused once the roster was edited by hand since |
| `GET …/teams` A | teams with their `flags`, and `hand` / `edited` for teams the host made or changed |
| `GET …/teams/{id}` · `GET …/teams/new` A | one team with its `players` and every fest person to suggest (`choices`) |
| `POST …/teams` · `PUT …/teams/{id}` A | `{name, city, players: [{rating_id, first_name, last_name}]}`: make a team by hand, or set a team's name, city and people. A rating id of 0 is a person the site does not know. Adding someone who plays for another team moves them. A re-import keeps these edits (ADR-0024) |
| `DELETE …/teams/{id}` A | takes a team without results off the roster; a rating team stays off through imports |
| `GET …/rating/players?q=` · `GET …/rating/teams?q=` A | the rating site's people and teams for a name's start (buff's mirror, else the site's API). `POST …/teams` takes a `rating_id` to add a site team, under a one-off `name` if wanted |
| `GET …/rating/teams/{id}/base` A | a site team's current base roster: `{team, players}` |
| `GET …/teams/export.xlsx` · `POST …/teams/xlsx` A | the roster as a sheet, a row per person; a sheet loads back (multipart `file`, `?preview=1` first) and sets every team it names, leaving the rest |
| `PATCH …/teams/flags` A | `{flags: {"<team id>": "МЮ, Студ"}}`; teams left out keep theirs |
| `GET …/players` A | players, overrides, and the ids an override takes |
| `POST …/players/overrides` A | `{player_id, team_id, game_ids}`: the player plays for `team_id` in those games (КСИ and ЭК games only; at least one) |
| `PUT …/players/overrides` A | `{player_id, source_team_id, team_id, game_ids}`: the three ids name an existing override, `game_ids` becomes its new list (not empty). To move the player to another team, delete and add |
| `DELETE …/players/overrides?player_id=&source_team_id=&team_id=` A | |
| `GET\|POST …/troikas` A | list / add `{lines: "Имя: Игрок, Игрок, Игрок\n…"}` |
| `PUT\|DELETE …/troikas/<id>` A | `{name, players: [...], applied?}`; `applied` moves the troika to that place in the order of applications (the list's `applied`), which a troikas list follows and `seed: players` breaks its last tie by |
| `GET …/numbers` A | teams with numbers |
| `POST …/numbers/assign` A | `{assignments: [{team_id, number}]}`; others keep theirs, a moved number leaves its old holder, `number: 0` takes a team's number away |
| `POST …/numbers/match` A | `{text: "<n>\t<team>\n…"}` proposes pairs, saves nothing |
| `POST …/numbers/auto` · `…/numbers/clear` A | 1…n alphabetically / none |

**Results (live editing, what the game pages send)**

| | |
|---|---|
| `GET /api/fest/<fest>` R · `GET …/games/<game>` R | the fest view: stages, standings |
| `GET …/games/<game>/state` R | a whole-document game (ОД, КСИ, Мультиигры): the document |
| `PATCH …/games/<game>/state` M ⓝ | `{ops: [{op: "set"\|"remove", path: [...], value}]}` into that document |
| `GET …/games/<game>/matches/<code>` R | one бой of a bracket game (ЭК, брейн, СИ, Тройка, Хамса, ЭС) |
| `PATCH …/matches/<code>/state` M ⓝ | ops into the бой: `path: ["participants", <slot>, …]` |
| `POST …/matches/<code>/finish` M ⓝ | `{finished: true\|false}` |
| `POST …/matches/<code>/venue` M | `{number}` |
| `POST …/stages/<stage>/reseed` M ⓝ · `PUT …/draw` A ⓝ | reseed a stage / seat a drawn slot `{slot, participant}` |
| `GET …/seed-import` M · `POST …/seed-import/{ksi,run,xlsx,decline}` M | seeds |
| `GET\|PUT …/games/<game>/screen-settings` · `GET …/venues` · `PUT …/venues/<n>` | the projector board, venues |
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
  `scheme-import`, a rating import whose preview drops teams or players, a
  `revert` over someone else's edits, `drop` in a rating import,
  `numbers/clear` or `numbers/auto` on a numbered fest.
- **The token cannot change the password, the username or reach /admin.** If
  the token stops working (the CLI says «токен не принят»), it expired, was
  revoked, or the password changed. The user makes a new one.
- Names, cities and flags are Russian. Keep them as the user wrote them.
