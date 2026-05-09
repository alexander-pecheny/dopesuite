# SQLite: основа турнира

| Таблица | Ключевые поля |
| --- | --- |
| `schema_versions` | `version`, `applied_at` |
| `schemes` | `id`, `slug`, `title`, `version`, `schema_json`, `created_at` |
| `tournaments` | `id`, `scheme_id`, `slug`, `title`, `revision`, `created_at`, `updated_at` |
| `teams` | `id`, `tournament_id`, `name`, `city`, `seed_group`, `seed_position` |
| `players` | `id`, `tournament_id`, `first_name`, `last_name` |
| `team_players` | `team_id`, `player_id`, `roster_order` |
| `venues` | `id`, `tournament_id`, `number`, `title`, `created_at`, `updated_at` |
| `stages` | `id`, `tournament_id`, `code`, `title`, `stage_type`, `position`, `status`, `config_json` |
| `matches` | `id`, `tournament_id`, `stage_id`, `code`, `title`, `position`, `participant_count`, `venue_id`, `status`, `revision` |

## Индексы и уникальность

- `schemes.slug` уникален.
- `tournaments.slug` уникален.
- `venues`: уникальная пара `(tournament_id, number)`.
- `matches`: уникальная пара `(tournament_id, code)`.
- `team_players`: первичный ключ `(team_id, player_id)`.

## Комментарии

- `schemes.schema_json` хранит исходную схему без потери деталей.
- `teams.seed_group/seed_position` нужны для стартовой жеребьевки и импорта.
- `stages.stage_type` — `matches` или `reseed`; для `reseed` настройки лежат в `config_json`.
- `matches.venue_id` хранит текущую площадку, которую можно менять по ходу турнира.
