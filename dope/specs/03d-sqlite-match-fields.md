# SQLite: поля боя

## `match_slots`

- `slot_index` — 0-based позиция команды в протоколе боя. Для боя на 4 команды это `0..3`, для `1/4` это `0..2`.
- `slot_index` не является местом команды. Место хранится в `match_results.place`.
- `slot_index` не меняется, если источник слота разрешился в другую команду после пересева.
- `source_type` — `seed`, `from_match`, `reseed`, `placeholder`. Старое значение `team` после v2 не допускается.
- `source_ref_json` — параметры источника:
  - `seed`: `{"basket": 1, "number": 3, "label"?: "К1-3"}`.
  - `from_match`: `{"match": "A", "place": 1, "label"?: "A1"}`.
  - `reseed`: `{"stage": "reseed_after_r8", "rank": 8, "label"?: "Пересев-8"}`.
  - `placeholder`: `{"placeholder": "TBD", "label"?: ""}`.
- `team_id` / `player_id` — кеш разрешения; ровно один из них может быть не NULL после успешного разрешения. До разрешения оба NULL.
- `locked=1` — ручная замена команды/игрока ведущим; автоматический resolver больше не перезаписывает кеш.

## `themes` и `answers`

- `themes.kind` — `regular` или `shootout`.
- `theme_index` — 0-based номер темы внутри `kind`.
- `player_id` — выбранный игрок; `null`, если тема еще не назначена.
- `answer_index` — 0-based индекс в `questionValues`; сейчас `0..4` для `10,20,30,40,50`.
- `mark` — нормализованная отметка: `''`, `right`, `wrong`.

## `match_results`

- `place=0` означает, что место еще не выставлено.
- `place` имеет тип `real`, чтобы поддержать дележи вроде `3.5`.
- `total` — сумма с отрицательными ответами обычного боя.
- `plus` — сумма только правильных ответов обычного боя.
- `tiebreak` — сумма перестрелки; не влияет на `total`.
- `metrics_json` хранит производные счетчики: `correct_50`, `wrong_20`, массивы `correctCounts`, `wrongCounts`.

## Пересевы и события

- Пересев описывается строкой в `stages` с `stage_type='reseed'` и принадлежит конкретной игре через `stages.game_id`.
- Настройки пересева лежат в `stages.config_json`: кто участвует и по каким метрикам сортировать.
- `reseed_entries.rank` — 1-based номер пересева.
- `reseed_entries.stage_id` ссылается на этап-пересев, а не на бой.
- `reseed_entries.metrics_json` фиксирует показатели, по которым команда получила этот ранг.
- `events.revision` совпадает с `fests.revision` после операции и используется для SSE replay/debug.
