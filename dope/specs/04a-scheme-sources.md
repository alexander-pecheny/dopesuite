# JSON-схема: источники и пересев

## Где лежит пересев

Пересев лежит в `stages` как этап с `stage_type: "reseed"`. Это явно показывает, что он находится между двумя игровыми этапами и имеет собственный статус готовности.

Слоты будущих боев ссылаются на код этого этапа: `{"reseed": {"stage": "reseed_after_r8", "rank": 8}}`.

## Источники слота

```json
{"seed": {"basket": 1, "position": 1}}
{"fromMatch": {"match": "A", "place": 1}}
{"reseed": {"stage": "reseed_after_r8", "rank": 8}}
{"team": {"id": "external-team-id"}}
{"placeholder": "Пересев-8"}
```

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
