# SQLite: бои и результаты

```sql
match_slots(
  id integer primary key,
  match_id integer references matches(id),
  slot_index integer not null,
  source_type text not null check (source_type in ('seed','from_match','reseed','placeholder')),
  source_ref_json text not null default '{}',
  team_id integer references teams(id),
  player_id integer references players(id),
  locked integer not null default 0,
  unique(match_id, slot_index)
);
```

`team_id` и `player_id` — кеш разрешения слота. Резолвер пересчитывает их по правилу:

- `seed` → `game_assignments(game_id=<игры боя>, basket=<source_ref.basket>, number=<source_ref.number>)`.
- `from_match` → `match_results.team_id` (или будущий `player_id` для индивидуальных форматов) предыдущего боя с указанным `place`.
- `reseed` → `reseed_entries.team_id` по указанному `(stage, rank)`.
- `placeholder` → не разрешается, оба ID остаются NULL.

`locked=1` означает, что хост вручную поставил команду/игрока в слот; резолвер больше не перетирает значение, пока флаг включен.

```sql
themes(id integer primary key, match_id integer references matches(id),
       team_id integer references teams(id), kind text not null,
       theme_index integer not null, player_id integer references players(id),
       unique(match_id, team_id, kind, theme_index));

answers(id integer primary key, theme_id integer references themes(id),
        answer_index integer not null, mark text not null default '',
        unique(theme_id, answer_index));

match_results(match_id integer references matches(id), team_id integer references teams(id),
              place real not null default 0, total integer not null default 0,
              plus integer not null default 0, tiebreak integer not null default 0,
              metrics_json text not null default '{}', primary key(match_id, team_id));
```

`themes`, `answers`, `match_results` — EK-специфичные таблицы. Для других `game_type` будут добавляться свои таблицы состояния (например, `brain_questions`, `si_questions`); общая структура `match_slots`/`game_assignments` остается переиспользуемой.

```sql
reseed_entries(stage_id integer references stages(id),
               rank integer not null, team_id integer references teams(id),
               metrics_json text not null, primary key(stage_id, rank));

events(id integer primary key, tournament_id integer references tournaments(id),
       revision integer not null, type text not null, payload_json text not null, created_at text not null);
```
