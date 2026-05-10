# JSON-схема: источники и пересев

## Где лежит пересев

Пересев лежит в `stages` как этап с `stage_type: "reseed"`. Это явно показывает, что он находится между двумя игровыми этапами и имеет собственный статус готовности.

Слоты будущих боев ссылаются на код этого этапа: `{"reseed": {"stage": "reseed_after_r8", "rank": 8}}`.

## Источники слота

```json
{"seed": {"basket": 1, "number": 1}}
{"fromMatch": {"match": "A", "place": 1}}
{"reseed": {"stage": "reseed_after_r8", "rank": 8}}
{"placeholder": "Пересев-8"}
```

После v2 источник `team` (с явной командой внутри слота) удален. Если бракет нужно «привязать» к конкретным командам — это делается через `game_assignments` на уровне игры, а не внутри JSON-сетки.

В источнике `seed` поле называется `number` (1-based номер команды в корзине), чтобы не путать со `slot_index` и `match_results.place`.

## Пересев

```json
{
  "code": "reseed_after_r8",
  "title": "Пересев перед 1/4",
  "stage_type": "reseed",
  "position": 3,
  "teams": [
    {"fromMatch": {"match": "M", "place": 1}},
    {"fromMatch": {"match": "M", "place": 2}}
  ],
  "sort": [
    {"metric": "place_sum", "dir": "asc"},
    {"metric": "total", "dir": "desc"},
    {"metric": "plus", "dir": "desc"},
    {"metric": "correct_50", "dir": "desc"},
    {"metric": "correct_40", "dir": "desc"},
    {"metric": "correct_30", "dir": "desc"},
    {"metric": "correct_20", "dir": "desc"},
    {"metric": "draw", "dir": "desc"}
  ]
}
```

`teams` перечисляет всех участников пересева. Готовность определяется по этим источникам: пока все указанные бои не закончены, этап-пересев остается `pending`.
