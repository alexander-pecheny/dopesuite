# JSON-схема бракета

JSON описывает спортивную схему игры: этапы, бои и переходы между ними. Конкретные команды/игроки в JSON не лежат — они хранятся в БД и привязываются к корзинам/номерам через `game_assignments` (см. [03a-sqlite-core.md](03a-sqlite-core.md) и [09-fests-and-games.md](09-fests-and-games.md)).

См. также: [04a-scheme-sources.md](04a-scheme-sources.md).

## Верхний уровень

```json
{
  "schemaVersion": 2,
  "slug": "studchr-ek-2026",
  "title": "СтудЧР-2026, ЭК",
  "gameType": "ek",
  "questionValues": [10, 20, 30, 40, 50],
  "regularThemeCount": 12,
  "venues": [{"number": 1, "title": "Москва-1"}],
  "stages": []
}
```

`gameType` определяет, какие поля будут считываться (для `ek` — `questionValues`, `regularThemeCount`). Для других форматов будут свои поля; `stages` и `slots` остаются общими.

Референсный JSON для СтудЧР генерируется отдельно:

```bash
python3 scripts/generate_studchr_grid.py > /tmp/studchr-ek-2026.json
```

Пересев задается в `stages` как отдельный этап с `stage_type: "reseed"`.

## Этап и бой

```json
{
  "code": "r8",
  "title": "1/8 финала",
  "stage_type": "matches",
  "position": 2,
  "matches": [{
    "code": "M",
    "title": "Бой M",
    "venue": 1,
    "participantCount": 4,
    "slots": [
      {"fromMatch": {"match": "A", "place": 1}},
      {"fromMatch": {"match": "G", "place": 1}},
      {"fromMatch": {"match": "B", "place": 2}},
      {"fromMatch": {"match": "H", "place": 2}}
    ]
  }]
}
```

## Договоренности

- `stage_type="matches"` содержит `matches`.
- `stage_type="reseed"` содержит настройки пересева вместо боев.
- `venue` — номер дефолтной площадки, не `venue_id`.
- `participantCount` задается явно, чтобы поддерживать бои на 3 и 4 команды.
- `slots` идут в порядке отображения команд в протоколе.
- В слоте допустимы только символьные источники: `seed`, `fromMatch`, `reseed`, `placeholder`. Источник `team` (с явной командой) после v2 запрещен — импорт возвращает ошибку.
- Опциональный `layout` хранит подсказки отображения. В текущем UI один этап показывается одной колонкой, поэтому заходы вроде `1/16 A-F` и `1/16 G-L` лучше задавать отдельными stage.
- В конкретной игре площадку у боя можно менять без изменения JSON.

## Импорт

- `/host/fest/{id}/import` принимает JSON целиком и пересоздает игру выбранного турнира.
- `POST /api/import?fest_id={id}` — API-вариант того же импорта; требует сессию организатора турнира.
- Все слоты импортируются как символьные источники без `player_id`. Если для слота `seed` в момент импорта известно соответствие в `game_assignments` (см. ниже), `match_slots.team_id` сразу заполняется как кеш.
- Если в JSON встречается источник `team` — импорт возвращает 400 c понятным сообщением и не создает турнир.
- Для удобства импорт может содержать опциональный массив `teams` верхнего уровня: `[{"name", "city"?, "basket", "number", "players": ["..."]}]`. По нему создаются записи `teams`, `players`, `team_players` и `game_assignments(basket, number)`. Сами слоты бракета остаются символьными — `seed{basket, number}` находит свою команду через `game_assignments`. Это просто конвенция импорта; в БД соответствие живет отдельно.
