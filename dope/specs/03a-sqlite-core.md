# SQLite: основа турнира

| Таблица | Ключевые поля |
| --- | --- |
| `schema_versions` | `version`, `applied_at` |
| `users` | `id`, `telegram_user_id?`, `telegram_username?`, `username?`, `is_system`, `created_at`, `updated_at` |
| `invites` | `id`, `code`, `created_by`, `used_by?`, `used_at?`, `created_at`, `expires_at` |
| `telegram_login_codes` | `id`, `code`, `kind`, `invite_id?`, `user_id?`, `telegram_user_id?`, `created_at`, `expires_at`, `consumed_at?` |
| `sessions` | `id`, `user_id`, `token_hash`, `created_at`, `expires_at`, `last_seen_at` |
| `schemes` | `id`, `slug`, `title`, `version`, `schema_json`, `created_at` |
| `fests` | `id`, `slug`, `title`, `description`, `rating_id?`, `created_by`, `revision`, `created_at`, `updated_at` |
| `fest_organizers` | `fest_id`, `user_id`, `added_at` |
| `games` | `id`, `fest_id`, `code`, `title`, `game_type`, `position`, `scheme_id?`, `scheme_json`, `status`, `team_list_source`, `roster_source`, `revision`, `created_at`, `updated_at` |
| `teams` | `id`, `fest_id`, `name`, `city` |
| `players` | `id`, `fest_id`, `first_name`, `last_name` |
| `team_players` | `team_id`, `player_id`, `roster_order` |
| `game_teams` | `game_id`, `team_id`, `position` |
| `game_players` | `game_id`, `player_id`, `position` |
| `game_team_players` | `game_id`, `team_id`, `player_id`, `roster_order` |
| `game_assignments` | `game_id`, `basket`, `number`, `team_id?`, `player_id?` |
| `venues` | `id`, `fest_id`, `number`, `title`, `created_at`, `updated_at` |
| `stages` | `id`, `fest_id`, `game_id`, `code`, `title`, `stage_type`, `position`, `status`, `config_json` |
| `matches` | `id`, `fest_id`, `game_id`, `stage_id`, `code`, `title`, `position`, `participant_count`, `venue_id?`, `status`, `revision` |

## Индексы и уникальность

- `users.username` уникален (но может быть NULL до первого логина); `users.telegram_user_id` уникален (NULL до привязки бота).
- `invites.code` уникален; `telegram_login_codes.code` уникален; `sessions.token_hash` уникален.
- `schemes.slug` уникален.
- `fests.slug` уникален.
- `games`: уникальная пара `(fest_id, code)`.
- `venues`: уникальная пара `(fest_id, number)`.
- `stages`: уникальная пара `(game_id, code)`.
- `matches`: уникальная пара `(game_id, code)`.
- `team_players`: первичный ключ `(team_id, player_id)`.
- `game_team_players`: первичный ключ `(game_id, team_id, player_id)`.
- `game_assignments`: первичный ключ `(game_id, basket, number)`; `check ((team_id is not null) <> (player_id is not null))`.

## Комментарии

- `schemes.schema_json` хранит исходную JSON-схему бракета без потери деталей. `games.scheme_json` — снапшот, привязанный к конкретной игре, чтобы изменение схемы-источника не ломало запущенные игры.
- `games.team_list_source` и `games.roster_source` — `'fest'` (наследование) или `'game'` (override). При переключении на `'game'` система копирует текущее состояние турнира в `game_teams`/`game_team_players`. См. [09-fests-and-games.md](09-fests-and-games.md).
- `fests.created_by` и `fest_organizers` решают, кто может редактировать турнир и проводить бои.
- `fests.rating_id` — опциональная ссылка на `https://rating.chgk.info/tournament/<rating_id>` (целое число). Если NULL — турнир не размечен в рейтинге ЧГК.
- `stages.stage_type` — `matches` или `reseed`; для `reseed` настройки лежат в `config_json`.
- `matches.venue_id` хранит текущую площадку, которую можно менять по ходу турнира.
- В таблице `teams` после v2 нет полей `seed_group`/`seed_position`: соответствие команды и корзины+номера живет в `game_assignments`.
