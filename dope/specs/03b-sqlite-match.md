# SQLite: бои и результаты

```sql
match_slots(
  id integer primary key,
  match_id integer references matches(id),
  slot_index integer not null,
  source_type text not null,
  source_ref_json text not null default '{}',
  team_id integer references teams(id),
  locked integer not null default 0,
  unique(match_id, slot_index)
);
```

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

```sql
reseed_entries(stage_id integer references stages(id),
               rank integer not null, team_id integer references teams(id),
               metrics_json text not null, primary key(stage_id, rank));

events(id integer primary key, tournament_id integer references tournaments(id),
       revision integer not null, type text not null, payload_json text not null, created_at text not null);
```
