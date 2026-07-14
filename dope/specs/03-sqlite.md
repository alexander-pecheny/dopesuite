# SQLite

База: `fest.db`. Включаем `PRAGMA foreign_keys = ON`, желательно WAL. Миграции версионируются: `schema_versions` хранит набор примененных версий, миграционный раннер применяет шаги по возрастанию.

## Версии

- `v1` — исходная схема одного турнира с одним бракетом (см. [01-existing.md](01-existing.md)).
- `v2` — пользователи и сессии, отделение турнира от игры, символьный бракет с `game_assignments`. Все шаги выполняются в одной транзакции, идемпотентно по факту наличия `schema_versions(version=2)`.

## Разделы

- [03a-sqlite-core.md](03a-sqlite-core.md) — пользователи и приглашения, турнир, игры, команды, игроки, площадки, этапы, бои.
- [03b-sqlite-match.md](03b-sqlite-match.md) — слоты, темы, ответы, результаты, пересевы, SSE-события.
- [03c-sqlite-field-rules.md](03c-sqlite-field-rules.md) — общие соглашения по полям.
- [03d-sqlite-match-fields.md](03d-sqlite-match-fields.md) — подробности боевых полей, включая `slot_index`.

## Принципы

- JSON-схема бракета хранится целиком в `schemes.schema_json` и снапшотится в `games.scheme_json` при создании игры.
- Все таблицы, кроме версий миграций и пользовательских, относятся к конкретному `fest_id` — напрямую или через `game_id`.
- `stages.game_id` и `matches.game_id` — авторитетный родитель. `fest_id` остается денормализованным полем для быстрых выборок по турниру.
- `match_slots.team_id` и `match_slots.player_id` — кеш разрешения источника, а не источник правды. Резолвер пересчитывает их из `game_assignments` / `match_results` / `reseed_entries`.
- `events` хранит ревизии для SSE и диагностики, но не является единственным источником правды.

## Инварианты приложения

- Состав команды на уровне турнира: не больше 9 строк в `team_players`. Тот же лимит в `game_team_players` для override.
- В бою количество слотов равно `matches.participant_count`.
- В EK-игре ровно `regularThemeCount` тем на команду; перестрелка может добавляться динамически.
- `mark` принимает только `''`, `right`, `wrong`.
- `match_slots.source_type` принимает только `seed`, `from_match`, `reseed`, `placeholder`. Старое значение `team` после v2 не допускается ни на импорте, ни в БД.
- В `game_assignments` ровно одно из `team_id`/`player_id` не NULL.
- `users.username` уникальный и не меняется после первого логина.
- Поля с суффиксом `_index` — внутренние 0-based индексы; `place`, `rank`, `number`, `position`, `basket` — 1-based или человекочитаемые.
