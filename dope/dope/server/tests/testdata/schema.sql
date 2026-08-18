-- index fest_teams_fest_number_idx
CREATE UNIQUE INDEX fest_teams_fest_number_idx on fest_teams(fest_id, number) where number is not null;

-- index games_fest_slug_idx
CREATE UNIQUE INDEX games_fest_slug_idx on games(fest_id, slug) where slug is not null;

-- index journal_fest_seq
CREATE INDEX journal_fest_seq on journal(fest_id, seq);

-- index journal_game_seq
CREATE INDEX journal_game_seq on journal(game_id, seq);

-- index journal_request_id
CREATE INDEX journal_request_id on journal(request_id);

-- index participants_fest_roster_number_idx
CREATE UNIQUE INDEX participants_fest_roster_number_idx
  on participants(fest_id, roster, number) where number is not null;

-- table audit_ctx
CREATE TABLE audit_ctx(
  id integer primary key check(id = 1),
  actor_user_id integer,
  request_id text,
  fest_id integer,
  suppress integer not null default 0
);

-- table fest_organizers
CREATE TABLE fest_organizers(
  fest_id integer not null references fests(id) on delete cascade,
  user_id integer not null references users(id) on delete cascade,
  role text not null default 'admin' check (role in ('creator','admin','host')),
  added_at text not null,
  primary key(fest_id, user_id)
);

-- table fest_players
CREATE TABLE fest_players(
  id integer primary key,
  fest_id integer not null references fests(id) on delete cascade,
  rating_id integer,
  first_name text not null,
  last_name text not null default ''
);

-- table fest_team_players
CREATE TABLE fest_team_players(
  team_id integer not null references fest_teams(id) on delete cascade,
  player_id integer not null references fest_players(id) on delete cascade,
  roster_order integer not null,
  primary key(team_id, player_id)
);

-- table fest_teams
CREATE TABLE fest_teams(
  id integer primary key,
  fest_id integer not null references fests(id) on delete cascade,
  rating_id integer,
  name text not null,
  city text not null default '',
  position real not null,
  number integer,
  deleted integer not null default 0
);

-- table fests
CREATE TABLE fests(
  id integer primary key,
  slug text unique,
  title text not null,
  description text not null default '',
  rating_id integer,
  created_by integer references users(id),
  revision integer not null default 1,
  created_at text not null,
  updated_at text not null,
  start_date text,
  end_date text,
  is_public integer not null default 0
);

-- table game_assignments
CREATE TABLE game_assignments(
  game_id integer not null references games(id) on delete cascade,
  basket integer not null,
  number integer not null,
  participant_id integer references participants(id),
  primary key(game_id, basket, number)
);

-- table game_participants
CREATE TABLE game_participants(
  game_id integer not null references games(id) on delete cascade,
  participant_id integer not null references participants(id) on delete cascade,
  position integer not null,
  number integer not null default 0,
  primary key(game_id, participant_id)
);

-- table game_player_team_overrides
CREATE TABLE game_player_team_overrides(
  fest_id integer not null references fests(id) on delete cascade,
  game_id integer not null references games(id) on delete cascade,
  player_id integer not null references fest_players(id) on delete cascade,
  source_team_id integer not null references fest_teams(id) on delete cascade,
  override_team_id integer not null references fest_teams(id) on delete cascade,
  created_at text not null,
  updated_at text not null,
  primary key(fest_id, game_id, player_id)
);

-- table game_team_players
CREATE TABLE game_team_players(
  game_id integer not null references games(id) on delete cascade,
  participant_id integer not null references participants(id) on delete cascade,
  player_id integer not null references players(id) on delete cascade,
  roster_order integer not null,
  primary key(game_id, participant_id, player_id)
);

-- table games
CREATE TABLE games(
  id integer primary key,
  fest_id integer not null references fests(id) on delete cascade,
  code text not null,
  title text not null,
  game_type text not null,
  position integer not null,
  scheme_id integer references schemes(id),
  scheme_json text not null default '{}',
  status text not null default 'pending',
  team_list_source text not null default 'fest' check (team_list_source in ('fest','game')),
  roster_source text not null default 'fest' check (roster_source in ('fest','game')),
  revision integer not null default 1,
  created_at text not null,
  updated_at text not null, state_json TEXT NOT NULL DEFAULT '{}', slug TEXT, random_seed TEXT NOT NULL DEFAULT '', screen_settings_json TEXT NOT NULL DEFAULT '{}', participant_kind TEXT NOT NULL DEFAULT 'team', scheme_dsl TEXT NOT NULL DEFAULT '',
  unique(fest_id, code)
);

-- table invites
CREATE TABLE invites(
  id integer primary key,
  code text not null unique,
  created_by integer not null references users(id),
  used_by integer references users(id),
  used_at text,
  created_at text not null,
  expires_at text not null
);

-- table journal
CREATE TABLE journal(
  id            integer primary key,
  fest_id       integer references fests(id) on delete cascade,
  game_id       integer,
  seq           integer not null,
  ts            text not null,
  actor_user_id integer,
  request_id    text,
  op            integer not null,
  payload       blob not null default x'',
  created_at    text not null
);

-- table journal_checkpoint
CREATE TABLE journal_checkpoint(
  game_id     integer not null,
  seq         integer not null,
  state_blob  blob not null,
  dsl_version integer not null,
  created_at  text not null,
  primary key(game_id, seq)
);

-- table journal_dict
CREATE TABLE journal_dict(
  id  integer primary key,
  str text not null unique
);

-- table journal_segment
CREATE TABLE journal_segment(
  id          integer primary key,
  fest_id     integer not null,
  seq_start   integer not null,
  seq_end     integer not null,
  dsl_version integer not null,
  n_records   integer not null,
  blob        blob not null,
  created_at  text not null
);

-- table journal_trigger_state
CREATE TABLE journal_trigger_state(
  id integer primary key check(id = 1),
  fingerprint text not null default ''
);

-- table match_results
CREATE TABLE match_results(
  match_id integer not null references matches(id) on delete cascade,
  participant_id integer not null references participants(id) on delete cascade,
  place real not null default 0,
  total integer not null default 0,
  plus integer not null default 0,
  tiebreak integer not null default 0,
  metrics_json text not null default '{}', place_override REAL,
  primary key(match_id, participant_id)
);

-- table match_slots
CREATE TABLE match_slots(
  id integer primary key,
  match_id integer not null references matches(id) on delete cascade,
  slot_index integer not null,
  source_type text not null check (source_type in ('seed','from_match','reseed','placeholder')),
  source_ref_json text not null default '{}',
  participant_id integer references participants(id),
  locked integer not null default 0,
  unique(match_id, slot_index)
);

-- table matches
CREATE TABLE matches(
  id integer primary key,
  fest_id integer not null references fests(id) on delete cascade,
  game_id integer not null references games(id) on delete cascade,
  stage_id integer not null references stages(id) on delete cascade,
  code text not null,
  title text not null,
  position integer not null,
  round integer not null default 0,
  wave integer not null default 0,
  participant_count integer not null,
  venue_id integer references venues(id),
  status text not null default 'active',
  revision integer not null default 1, state_json TEXT NOT NULL DEFAULT '{}', letter TEXT NOT NULL DEFAULT '',
  unique(game_id, code)
);

-- table participant_players
CREATE TABLE participant_players(
  participant_id integer not null references participants(id) on delete cascade,
  player_id integer not null references players(id) on delete cascade,
  roster_order integer not null,
  primary key(participant_id, player_id)
);

-- table participants
CREATE TABLE participants(
  id integer primary key,
  fest_id integer not null references fests(id) on delete cascade,
  roster text not null default 'team' check (roster in ('team','player')),
  name text not null,
  city text not null default '',
  fest_team_id integer references fest_teams(id),
  fest_player_id integer references fest_players(id)
, number INTEGER);

-- table players
CREATE TABLE players(
  id integer primary key,
  fest_id integer not null references fests(id) on delete cascade,
  first_name text not null,
  last_name text not null default ''
);

-- table schema_versions
CREATE TABLE schema_versions(
  version integer primary key,
  applied_at text not null
);

-- table schemes
CREATE TABLE schemes(
  id integer primary key,
  slug text not null unique,
  title text not null,
  version integer not null,
  schema_json text not null,
  created_at text not null
);

-- table sessions
CREATE TABLE sessions(
  id integer primary key,
  user_id integer not null references users(id) on delete cascade,
  token_hash text not null unique,
  created_at text not null,
  expires_at text not null,
  last_seen_at text not null
);

-- table stage_standings
CREATE TABLE stage_standings(
  stage_id integer not null references stages(id) on delete cascade,
  rank integer not null,
  participant_id integer not null,
  metrics_json text not null default '{}',
  primary key(stage_id, rank));

-- table stages
CREATE TABLE stages(
  id integer primary key,
  fest_id integer not null references fests(id) on delete cascade,
  game_id integer not null references games(id) on delete cascade,
  code text not null,
  title text not null,
  stage_type text not null,
  position integer not null,
  status text not null default 'active',
  config_json text not null default '{}',
  block_code text not null default '',
  wave_index integer not null default 0,
  group_code text not null default '', kind TEXT NOT NULL DEFAULT '',
  unique(game_id, code)
);

-- table telegram_login_codes
CREATE TABLE telegram_login_codes(
  id integer primary key,
  code text not null unique,
  kind text not null check (kind in ('register','login')),
  invite_id integer references invites(id),
  user_id integer references users(id),
  telegram_user_id integer,
  telegram_username text,
  created_at text not null,
  expires_at text not null,
  consumed_at text
, telegram_name TEXT, desired_username TEXT);

-- table users
CREATE TABLE users(
  id integer primary key,
  telegram_user_id integer unique,
  telegram_username text,
  username text unique,
  is_system integer not null default 0,
  created_at text not null,
  updated_at text not null
, password_hash TEXT, password_salt TEXT, telegram_name TEXT);

-- table venues
CREATE TABLE venues(
  id integer primary key,
  fest_id integer not null references fests(id) on delete cascade,
  number integer not null,
  title text not null,
  created_at text not null,
  updated_at text not null,
  unique(fest_id, number)
);

-- trigger fest_team_players_max_9
CREATE TRIGGER fest_team_players_max_9
before insert on fest_team_players
when (select count(*) from fest_team_players where team_id = new.team_id) >= 9
begin
  select raise(abort, 'team roster is limited to 9 players');
end;

-- trigger game_team_players_max_9
CREATE TRIGGER game_team_players_max_9
before insert on game_team_players
when (select count(*) from game_team_players where game_id = new.game_id and participant_id = new.participant_id) >= 9
begin
  select raise(abort, 'team roster is limited to 9 players');
end;

-- trigger games_random_seed_default
CREATE TRIGGER games_random_seed_default
after insert on games
when new.random_seed = ''
begin
  update games set random_seed = lower(hex(randomblob(16))) where id = new.id;
end;

-- trigger journal_game_assignments_delete
CREATE TRIGGER journal_game_assignments_delete after delete on game_assignments
begin
  insert into journal(fest_id, game_id, seq, ts, actor_user_id, request_id, op, payload, created_at)
  select (select fest_id from games where id = old.game_id), old.game_id, coalesce((select revision from fests where id = (select fest_id from games where id = old.game_id)), 0), strftime('%Y-%m-%dT%H:%M:%fZ','now'), (select actor_user_id from audit_ctx where id = 1), (select request_id from audit_ctx where id = 1), 3, json_object('t', 'game_assignments', 'r', json_object('game_id', old."game_id", 'basket', old."basket", 'number', old."number")), strftime('%Y-%m-%dT%H:%M:%fZ','now');
end;

-- trigger journal_game_assignments_insert
CREATE TRIGGER journal_game_assignments_insert after insert on game_assignments
begin
  insert into journal(fest_id, game_id, seq, ts, actor_user_id, request_id, op, payload, created_at)
  select (select fest_id from games where id = new.game_id), new.game_id, coalesce((select revision from fests where id = (select fest_id from games where id = new.game_id)), 0), strftime('%Y-%m-%dT%H:%M:%fZ','now'), (select actor_user_id from audit_ctx where id = 1), (select request_id from audit_ctx where id = 1), 1, json_object('t', 'game_assignments', 'r', json_object('game_id', new."game_id", 'basket', new."basket", 'number', new."number", 'participant_id', new."participant_id")), strftime('%Y-%m-%dT%H:%M:%fZ','now');
end;

-- trigger journal_game_assignments_update
CREATE TRIGGER journal_game_assignments_update after update on game_assignments
begin
  insert into journal(fest_id, game_id, seq, ts, actor_user_id, request_id, op, payload, created_at)
  select (select fest_id from games where id = new.game_id), new.game_id, coalesce((select revision from fests where id = (select fest_id from games where id = new.game_id)), 0), strftime('%Y-%m-%dT%H:%M:%fZ','now'), (select actor_user_id from audit_ctx where id = 1), (select request_id from audit_ctx where id = 1), 2, json_object('t', 'game_assignments', 'r', json_remove(json_object('game_id', new."game_id", 'basket', new."basket", 'number', new."number", 'participant_id', new."participant_id"), case when old."participant_id" is new."participant_id" then '$."participant_id"' else '$."__dope_keep__"' end)), strftime('%Y-%m-%dT%H:%M:%fZ','now');
end;

-- trigger journal_game_participants_delete
CREATE TRIGGER journal_game_participants_delete after delete on game_participants
begin
  insert into journal(fest_id, game_id, seq, ts, actor_user_id, request_id, op, payload, created_at)
  select (select fest_id from games where id = old.game_id), old.game_id, coalesce((select revision from fests where id = (select fest_id from games where id = old.game_id)), 0), strftime('%Y-%m-%dT%H:%M:%fZ','now'), (select actor_user_id from audit_ctx where id = 1), (select request_id from audit_ctx where id = 1), 3, json_object('t', 'game_participants', 'r', json_object('game_id', old."game_id", 'participant_id', old."participant_id")), strftime('%Y-%m-%dT%H:%M:%fZ','now');
end;

-- trigger journal_game_participants_insert
CREATE TRIGGER journal_game_participants_insert after insert on game_participants
begin
  insert into journal(fest_id, game_id, seq, ts, actor_user_id, request_id, op, payload, created_at)
  select (select fest_id from games where id = new.game_id), new.game_id, coalesce((select revision from fests where id = (select fest_id from games where id = new.game_id)), 0), strftime('%Y-%m-%dT%H:%M:%fZ','now'), (select actor_user_id from audit_ctx where id = 1), (select request_id from audit_ctx where id = 1), 1, json_object('t', 'game_participants', 'r', json_object('game_id', new."game_id", 'participant_id', new."participant_id", 'position', new."position", 'number', new."number")), strftime('%Y-%m-%dT%H:%M:%fZ','now');
end;

-- trigger journal_game_participants_update
CREATE TRIGGER journal_game_participants_update after update on game_participants
begin
  insert into journal(fest_id, game_id, seq, ts, actor_user_id, request_id, op, payload, created_at)
  select (select fest_id from games where id = new.game_id), new.game_id, coalesce((select revision from fests where id = (select fest_id from games where id = new.game_id)), 0), strftime('%Y-%m-%dT%H:%M:%fZ','now'), (select actor_user_id from audit_ctx where id = 1), (select request_id from audit_ctx where id = 1), 2, json_object('t', 'game_participants', 'r', json_remove(json_object('game_id', new."game_id", 'participant_id', new."participant_id", 'position', new."position", 'number', new."number"), case when old."position" is new."position" then '$."position"' else '$."__dope_keep__"' end, case when old."number" is new."number" then '$."number"' else '$."__dope_keep__"' end)), strftime('%Y-%m-%dT%H:%M:%fZ','now');
end;

-- trigger journal_game_player_team_overrides_delete
CREATE TRIGGER journal_game_player_team_overrides_delete after delete on game_player_team_overrides
begin
  insert into journal(fest_id, game_id, seq, ts, actor_user_id, request_id, op, payload, created_at)
  select (select fest_id from games where id = old.game_id), old.game_id, coalesce((select revision from fests where id = (select fest_id from games where id = old.game_id)), 0), strftime('%Y-%m-%dT%H:%M:%fZ','now'), (select actor_user_id from audit_ctx where id = 1), (select request_id from audit_ctx where id = 1), 3, json_object('t', 'game_player_team_overrides', 'r', json_object('fest_id', old."fest_id", 'game_id', old."game_id", 'player_id', old."player_id")), strftime('%Y-%m-%dT%H:%M:%fZ','now');
end;

-- trigger journal_game_player_team_overrides_insert
CREATE TRIGGER journal_game_player_team_overrides_insert after insert on game_player_team_overrides
begin
  insert into journal(fest_id, game_id, seq, ts, actor_user_id, request_id, op, payload, created_at)
  select (select fest_id from games where id = new.game_id), new.game_id, coalesce((select revision from fests where id = (select fest_id from games where id = new.game_id)), 0), strftime('%Y-%m-%dT%H:%M:%fZ','now'), (select actor_user_id from audit_ctx where id = 1), (select request_id from audit_ctx where id = 1), 1, json_object('t', 'game_player_team_overrides', 'r', json_object('fest_id', new."fest_id", 'game_id', new."game_id", 'player_id', new."player_id", 'source_team_id', new."source_team_id", 'override_team_id', new."override_team_id", 'created_at', new."created_at", 'updated_at', new."updated_at")), strftime('%Y-%m-%dT%H:%M:%fZ','now');
end;

-- trigger journal_game_player_team_overrides_update
CREATE TRIGGER journal_game_player_team_overrides_update after update on game_player_team_overrides
begin
  insert into journal(fest_id, game_id, seq, ts, actor_user_id, request_id, op, payload, created_at)
  select (select fest_id from games where id = new.game_id), new.game_id, coalesce((select revision from fests where id = (select fest_id from games where id = new.game_id)), 0), strftime('%Y-%m-%dT%H:%M:%fZ','now'), (select actor_user_id from audit_ctx where id = 1), (select request_id from audit_ctx where id = 1), 2, json_object('t', 'game_player_team_overrides', 'r', json_remove(json_object('fest_id', new."fest_id", 'game_id', new."game_id", 'player_id', new."player_id", 'source_team_id', new."source_team_id", 'override_team_id', new."override_team_id", 'created_at', new."created_at", 'updated_at', new."updated_at"), case when old."source_team_id" is new."source_team_id" then '$."source_team_id"' else '$."__dope_keep__"' end, case when old."override_team_id" is new."override_team_id" then '$."override_team_id"' else '$."__dope_keep__"' end, case when old."created_at" is new."created_at" then '$."created_at"' else '$."__dope_keep__"' end, case when old."updated_at" is new."updated_at" then '$."updated_at"' else '$."__dope_keep__"' end)), strftime('%Y-%m-%dT%H:%M:%fZ','now');
end;

-- trigger journal_game_team_players_delete
CREATE TRIGGER journal_game_team_players_delete after delete on game_team_players
begin
  insert into journal(fest_id, game_id, seq, ts, actor_user_id, request_id, op, payload, created_at)
  select (select fest_id from games where id = old.game_id), old.game_id, coalesce((select revision from fests where id = (select fest_id from games where id = old.game_id)), 0), strftime('%Y-%m-%dT%H:%M:%fZ','now'), (select actor_user_id from audit_ctx where id = 1), (select request_id from audit_ctx where id = 1), 3, json_object('t', 'game_team_players', 'r', json_object('game_id', old."game_id", 'participant_id', old."participant_id", 'player_id', old."player_id")), strftime('%Y-%m-%dT%H:%M:%fZ','now');
end;

-- trigger journal_game_team_players_insert
CREATE TRIGGER journal_game_team_players_insert after insert on game_team_players
begin
  insert into journal(fest_id, game_id, seq, ts, actor_user_id, request_id, op, payload, created_at)
  select (select fest_id from games where id = new.game_id), new.game_id, coalesce((select revision from fests where id = (select fest_id from games where id = new.game_id)), 0), strftime('%Y-%m-%dT%H:%M:%fZ','now'), (select actor_user_id from audit_ctx where id = 1), (select request_id from audit_ctx where id = 1), 1, json_object('t', 'game_team_players', 'r', json_object('game_id', new."game_id", 'participant_id', new."participant_id", 'player_id', new."player_id", 'roster_order', new."roster_order")), strftime('%Y-%m-%dT%H:%M:%fZ','now');
end;

-- trigger journal_game_team_players_update
CREATE TRIGGER journal_game_team_players_update after update on game_team_players
begin
  insert into journal(fest_id, game_id, seq, ts, actor_user_id, request_id, op, payload, created_at)
  select (select fest_id from games where id = new.game_id), new.game_id, coalesce((select revision from fests where id = (select fest_id from games where id = new.game_id)), 0), strftime('%Y-%m-%dT%H:%M:%fZ','now'), (select actor_user_id from audit_ctx where id = 1), (select request_id from audit_ctx where id = 1), 2, json_object('t', 'game_team_players', 'r', json_remove(json_object('game_id', new."game_id", 'participant_id', new."participant_id", 'player_id', new."player_id", 'roster_order', new."roster_order"), case when old."roster_order" is new."roster_order" then '$."roster_order"' else '$."__dope_keep__"' end)), strftime('%Y-%m-%dT%H:%M:%fZ','now');
end;

-- trigger journal_games_delete
CREATE TRIGGER journal_games_delete after delete on games
begin
  insert into journal(fest_id, game_id, seq, ts, actor_user_id, request_id, op, payload, created_at)
  select (select fest_id from games where id = old.id), old.id, coalesce((select revision from fests where id = (select fest_id from games where id = old.id)), 0), strftime('%Y-%m-%dT%H:%M:%fZ','now'), (select actor_user_id from audit_ctx where id = 1), (select request_id from audit_ctx where id = 1), 3, json_object('t', 'games', 'r', json_object('id', old."id")), strftime('%Y-%m-%dT%H:%M:%fZ','now');
end;

-- trigger journal_games_insert
CREATE TRIGGER journal_games_insert after insert on games
begin
  insert into journal(fest_id, game_id, seq, ts, actor_user_id, request_id, op, payload, created_at)
  select (select fest_id from games where id = new.id), new.id, coalesce((select revision from fests where id = (select fest_id from games where id = new.id)), 0), strftime('%Y-%m-%dT%H:%M:%fZ','now'), (select actor_user_id from audit_ctx where id = 1), (select request_id from audit_ctx where id = 1), 1, json_object('t', 'games', 'r', json_object('id', new."id", 'fest_id', new."fest_id", 'code', new."code", 'title', new."title", 'game_type', new."game_type", 'position', new."position", 'scheme_id', new."scheme_id", 'scheme_json', new."scheme_json", 'status', new."status", 'team_list_source', new."team_list_source", 'roster_source', new."roster_source", 'revision', new."revision", 'created_at', new."created_at", 'updated_at', new."updated_at", 'state_json', new."state_json", 'slug', new."slug", 'random_seed', new."random_seed", 'screen_settings_json', new."screen_settings_json", 'participant_kind', new."participant_kind", 'scheme_dsl', new."scheme_dsl")), strftime('%Y-%m-%dT%H:%M:%fZ','now');
end;

-- trigger journal_games_update
CREATE TRIGGER journal_games_update after update on games
begin
  insert into journal(fest_id, game_id, seq, ts, actor_user_id, request_id, op, payload, created_at)
  select (select fest_id from games where id = new.id), new.id, coalesce((select revision from fests where id = (select fest_id from games where id = new.id)), 0), strftime('%Y-%m-%dT%H:%M:%fZ','now'), (select actor_user_id from audit_ctx where id = 1), (select request_id from audit_ctx where id = 1), 2, json_object('t', 'games', 'r', json_remove(json_object('id', new."id", 'fest_id', new."fest_id", 'code', new."code", 'title', new."title", 'game_type', new."game_type", 'position', new."position", 'scheme_id', new."scheme_id", 'scheme_json', new."scheme_json", 'status', new."status", 'team_list_source', new."team_list_source", 'roster_source', new."roster_source", 'revision', new."revision", 'created_at', new."created_at", 'updated_at', new."updated_at", 'state_json', new."state_json", 'slug', new."slug", 'random_seed', new."random_seed", 'screen_settings_json', new."screen_settings_json", 'participant_kind', new."participant_kind", 'scheme_dsl', new."scheme_dsl"), case when old."fest_id" is new."fest_id" then '$."fest_id"' else '$."__dope_keep__"' end, case when old."code" is new."code" then '$."code"' else '$."__dope_keep__"' end, case when old."title" is new."title" then '$."title"' else '$."__dope_keep__"' end, case when old."game_type" is new."game_type" then '$."game_type"' else '$."__dope_keep__"' end, case when old."position" is new."position" then '$."position"' else '$."__dope_keep__"' end, case when old."scheme_id" is new."scheme_id" then '$."scheme_id"' else '$."__dope_keep__"' end, case when old."scheme_json" is new."scheme_json" then '$."scheme_json"' else '$."__dope_keep__"' end, case when old."status" is new."status" then '$."status"' else '$."__dope_keep__"' end, case when old."team_list_source" is new."team_list_source" then '$."team_list_source"' else '$."__dope_keep__"' end, case when old."roster_source" is new."roster_source" then '$."roster_source"' else '$."__dope_keep__"' end, case when old."revision" is new."revision" then '$."revision"' else '$."__dope_keep__"' end, case when old."created_at" is new."created_at" then '$."created_at"' else '$."__dope_keep__"' end, case when old."updated_at" is new."updated_at" then '$."updated_at"' else '$."__dope_keep__"' end, case when old."state_json" is new."state_json" then '$."state_json"' else '$."__dope_keep__"' end, case when old."slug" is new."slug" then '$."slug"' else '$."__dope_keep__"' end, case when old."random_seed" is new."random_seed" then '$."random_seed"' else '$."__dope_keep__"' end, case when old."screen_settings_json" is new."screen_settings_json" then '$."screen_settings_json"' else '$."__dope_keep__"' end, case when old."participant_kind" is new."participant_kind" then '$."participant_kind"' else '$."__dope_keep__"' end, case when old."scheme_dsl" is new."scheme_dsl" then '$."scheme_dsl"' else '$."__dope_keep__"' end)), strftime('%Y-%m-%dT%H:%M:%fZ','now');
end;

-- trigger journal_match_results_delete
CREATE TRIGGER journal_match_results_delete after delete on match_results
begin
  insert into journal(fest_id, game_id, seq, ts, actor_user_id, request_id, op, payload, created_at)
  select (select fest_id from games where id = (select game_id from matches where id = old.match_id)), (select game_id from matches where id = old.match_id), coalesce((select revision from fests where id = (select fest_id from games where id = (select game_id from matches where id = old.match_id))), 0), strftime('%Y-%m-%dT%H:%M:%fZ','now'), (select actor_user_id from audit_ctx where id = 1), (select request_id from audit_ctx where id = 1), 3, json_object('t', 'match_results', 'r', json_object('match_id', old."match_id", 'participant_id', old."participant_id")), strftime('%Y-%m-%dT%H:%M:%fZ','now');
end;

-- trigger journal_match_results_insert
CREATE TRIGGER journal_match_results_insert after insert on match_results
begin
  insert into journal(fest_id, game_id, seq, ts, actor_user_id, request_id, op, payload, created_at)
  select (select fest_id from games where id = (select game_id from matches where id = new.match_id)), (select game_id from matches where id = new.match_id), coalesce((select revision from fests where id = (select fest_id from games where id = (select game_id from matches where id = new.match_id))), 0), strftime('%Y-%m-%dT%H:%M:%fZ','now'), (select actor_user_id from audit_ctx where id = 1), (select request_id from audit_ctx where id = 1), 1, json_object('t', 'match_results', 'r', json_object('match_id', new."match_id", 'participant_id', new."participant_id", 'place', new."place", 'total', new."total", 'plus', new."plus", 'tiebreak', new."tiebreak", 'metrics_json', new."metrics_json", 'place_override', new."place_override")), strftime('%Y-%m-%dT%H:%M:%fZ','now');
end;

-- trigger journal_match_results_update
CREATE TRIGGER journal_match_results_update after update on match_results
begin
  insert into journal(fest_id, game_id, seq, ts, actor_user_id, request_id, op, payload, created_at)
  select (select fest_id from games where id = (select game_id from matches where id = new.match_id)), (select game_id from matches where id = new.match_id), coalesce((select revision from fests where id = (select fest_id from games where id = (select game_id from matches where id = new.match_id))), 0), strftime('%Y-%m-%dT%H:%M:%fZ','now'), (select actor_user_id from audit_ctx where id = 1), (select request_id from audit_ctx where id = 1), 2, json_object('t', 'match_results', 'r', json_remove(json_object('match_id', new."match_id", 'participant_id', new."participant_id", 'place', new."place", 'total', new."total", 'plus', new."plus", 'tiebreak', new."tiebreak", 'metrics_json', new."metrics_json", 'place_override', new."place_override"), case when old."place" is new."place" then '$."place"' else '$."__dope_keep__"' end, case when old."total" is new."total" then '$."total"' else '$."__dope_keep__"' end, case when old."plus" is new."plus" then '$."plus"' else '$."__dope_keep__"' end, case when old."tiebreak" is new."tiebreak" then '$."tiebreak"' else '$."__dope_keep__"' end, case when old."metrics_json" is new."metrics_json" then '$."metrics_json"' else '$."__dope_keep__"' end, case when old."place_override" is new."place_override" then '$."place_override"' else '$."__dope_keep__"' end)), strftime('%Y-%m-%dT%H:%M:%fZ','now');
end;

-- trigger journal_match_slots_delete
CREATE TRIGGER journal_match_slots_delete after delete on match_slots
begin
  insert into journal(fest_id, game_id, seq, ts, actor_user_id, request_id, op, payload, created_at)
  select (select fest_id from games where id = (select game_id from matches where id = old.match_id)), (select game_id from matches where id = old.match_id), coalesce((select revision from fests where id = (select fest_id from games where id = (select game_id from matches where id = old.match_id))), 0), strftime('%Y-%m-%dT%H:%M:%fZ','now'), (select actor_user_id from audit_ctx where id = 1), (select request_id from audit_ctx where id = 1), 3, json_object('t', 'match_slots', 'r', json_object('id', old."id")), strftime('%Y-%m-%dT%H:%M:%fZ','now');
end;

-- trigger journal_match_slots_insert
CREATE TRIGGER journal_match_slots_insert after insert on match_slots
begin
  insert into journal(fest_id, game_id, seq, ts, actor_user_id, request_id, op, payload, created_at)
  select (select fest_id from games where id = (select game_id from matches where id = new.match_id)), (select game_id from matches where id = new.match_id), coalesce((select revision from fests where id = (select fest_id from games where id = (select game_id from matches where id = new.match_id))), 0), strftime('%Y-%m-%dT%H:%M:%fZ','now'), (select actor_user_id from audit_ctx where id = 1), (select request_id from audit_ctx where id = 1), 1, json_object('t', 'match_slots', 'r', json_object('id', new."id", 'match_id', new."match_id", 'slot_index', new."slot_index", 'source_type', new."source_type", 'source_ref_json', new."source_ref_json", 'participant_id', new."participant_id", 'locked', new."locked")), strftime('%Y-%m-%dT%H:%M:%fZ','now');
end;

-- trigger journal_match_slots_update
CREATE TRIGGER journal_match_slots_update after update on match_slots
begin
  insert into journal(fest_id, game_id, seq, ts, actor_user_id, request_id, op, payload, created_at)
  select (select fest_id from games where id = (select game_id from matches where id = new.match_id)), (select game_id from matches where id = new.match_id), coalesce((select revision from fests where id = (select fest_id from games where id = (select game_id from matches where id = new.match_id))), 0), strftime('%Y-%m-%dT%H:%M:%fZ','now'), (select actor_user_id from audit_ctx where id = 1), (select request_id from audit_ctx where id = 1), 2, json_object('t', 'match_slots', 'r', json_remove(json_object('id', new."id", 'match_id', new."match_id", 'slot_index', new."slot_index", 'source_type', new."source_type", 'source_ref_json', new."source_ref_json", 'participant_id', new."participant_id", 'locked', new."locked"), case when old."match_id" is new."match_id" then '$."match_id"' else '$."__dope_keep__"' end, case when old."slot_index" is new."slot_index" then '$."slot_index"' else '$."__dope_keep__"' end, case when old."source_type" is new."source_type" then '$."source_type"' else '$."__dope_keep__"' end, case when old."source_ref_json" is new."source_ref_json" then '$."source_ref_json"' else '$."__dope_keep__"' end, case when old."participant_id" is new."participant_id" then '$."participant_id"' else '$."__dope_keep__"' end, case when old."locked" is new."locked" then '$."locked"' else '$."__dope_keep__"' end)), strftime('%Y-%m-%dT%H:%M:%fZ','now');
end;

-- trigger journal_matches_delete
CREATE TRIGGER journal_matches_delete after delete on matches
begin
  insert into journal(fest_id, game_id, seq, ts, actor_user_id, request_id, op, payload, created_at)
  select (select fest_id from games where id = old.game_id), old.game_id, coalesce((select revision from fests where id = (select fest_id from games where id = old.game_id)), 0), strftime('%Y-%m-%dT%H:%M:%fZ','now'), (select actor_user_id from audit_ctx where id = 1), (select request_id from audit_ctx where id = 1), 3, json_object('t', 'matches', 'r', json_object('id', old."id")), strftime('%Y-%m-%dT%H:%M:%fZ','now');
end;

-- trigger journal_matches_insert
CREATE TRIGGER journal_matches_insert after insert on matches
begin
  insert into journal(fest_id, game_id, seq, ts, actor_user_id, request_id, op, payload, created_at)
  select (select fest_id from games where id = new.game_id), new.game_id, coalesce((select revision from fests where id = (select fest_id from games where id = new.game_id)), 0), strftime('%Y-%m-%dT%H:%M:%fZ','now'), (select actor_user_id from audit_ctx where id = 1), (select request_id from audit_ctx where id = 1), 1, json_object('t', 'matches', 'r', json_object('id', new."id", 'fest_id', new."fest_id", 'game_id', new."game_id", 'stage_id', new."stage_id", 'code', new."code", 'title', new."title", 'position', new."position", 'round', new."round", 'wave', new."wave", 'participant_count', new."participant_count", 'venue_id', new."venue_id", 'status', new."status", 'revision', new."revision", 'state_json', new."state_json", 'letter', new."letter")), strftime('%Y-%m-%dT%H:%M:%fZ','now');
end;

-- trigger journal_matches_update
CREATE TRIGGER journal_matches_update after update on matches
when old."id" is not new."id" or old."fest_id" is not new."fest_id" or old."game_id" is not new."game_id" or old."stage_id" is not new."stage_id" or old."code" is not new."code" or old."title" is not new."title" or old."position" is not new."position" or old."round" is not new."round" or old."wave" is not new."wave" or old."participant_count" is not new."participant_count" or old."venue_id" is not new."venue_id" or old."status" is not new."status" or old."revision" is not new."revision" or old."letter" is not new."letter"
begin
  insert into journal(fest_id, game_id, seq, ts, actor_user_id, request_id, op, payload, created_at)
  select (select fest_id from games where id = new.game_id), new.game_id, coalesce((select revision from fests where id = (select fest_id from games where id = new.game_id)), 0), strftime('%Y-%m-%dT%H:%M:%fZ','now'), (select actor_user_id from audit_ctx where id = 1), (select request_id from audit_ctx where id = 1), 2, json_object('t', 'matches', 'r', json_remove(json_object('id', new."id", 'fest_id', new."fest_id", 'game_id', new."game_id", 'stage_id', new."stage_id", 'code', new."code", 'title', new."title", 'position', new."position", 'round', new."round", 'wave', new."wave", 'participant_count', new."participant_count", 'venue_id', new."venue_id", 'status', new."status", 'revision', new."revision", 'letter', new."letter"), case when old."fest_id" is new."fest_id" then '$."fest_id"' else '$."__dope_keep__"' end, case when old."game_id" is new."game_id" then '$."game_id"' else '$."__dope_keep__"' end, case when old."stage_id" is new."stage_id" then '$."stage_id"' else '$."__dope_keep__"' end, case when old."code" is new."code" then '$."code"' else '$."__dope_keep__"' end, case when old."title" is new."title" then '$."title"' else '$."__dope_keep__"' end, case when old."position" is new."position" then '$."position"' else '$."__dope_keep__"' end, case when old."round" is new."round" then '$."round"' else '$."__dope_keep__"' end, case when old."wave" is new."wave" then '$."wave"' else '$."__dope_keep__"' end, case when old."participant_count" is new."participant_count" then '$."participant_count"' else '$."__dope_keep__"' end, case when old."venue_id" is new."venue_id" then '$."venue_id"' else '$."__dope_keep__"' end, case when old."status" is new."status" then '$."status"' else '$."__dope_keep__"' end, case when old."revision" is new."revision" then '$."revision"' else '$."__dope_keep__"' end, case when old."letter" is new."letter" then '$."letter"' else '$."__dope_keep__"' end)), strftime('%Y-%m-%dT%H:%M:%fZ','now');
end;

-- trigger journal_stage_standings_delete
CREATE TRIGGER journal_stage_standings_delete after delete on stage_standings
begin
  insert into journal(fest_id, game_id, seq, ts, actor_user_id, request_id, op, payload, created_at)
  select (select fest_id from games where id = (select game_id from stages where id = old.stage_id)), (select game_id from stages where id = old.stage_id), coalesce((select revision from fests where id = (select fest_id from games where id = (select game_id from stages where id = old.stage_id))), 0), strftime('%Y-%m-%dT%H:%M:%fZ','now'), (select actor_user_id from audit_ctx where id = 1), (select request_id from audit_ctx where id = 1), 3, json_object('t', 'stage_standings', 'r', json_object('stage_id', old."stage_id", 'rank', old."rank")), strftime('%Y-%m-%dT%H:%M:%fZ','now');
end;

-- trigger journal_stage_standings_insert
CREATE TRIGGER journal_stage_standings_insert after insert on stage_standings
begin
  insert into journal(fest_id, game_id, seq, ts, actor_user_id, request_id, op, payload, created_at)
  select (select fest_id from games where id = (select game_id from stages where id = new.stage_id)), (select game_id from stages where id = new.stage_id), coalesce((select revision from fests where id = (select fest_id from games where id = (select game_id from stages where id = new.stage_id))), 0), strftime('%Y-%m-%dT%H:%M:%fZ','now'), (select actor_user_id from audit_ctx where id = 1), (select request_id from audit_ctx where id = 1), 1, json_object('t', 'stage_standings', 'r', json_object('stage_id', new."stage_id", 'rank', new."rank", 'participant_id', new."participant_id", 'metrics_json', new."metrics_json")), strftime('%Y-%m-%dT%H:%M:%fZ','now');
end;

-- trigger journal_stage_standings_update
CREATE TRIGGER journal_stage_standings_update after update on stage_standings
begin
  insert into journal(fest_id, game_id, seq, ts, actor_user_id, request_id, op, payload, created_at)
  select (select fest_id from games where id = (select game_id from stages where id = new.stage_id)), (select game_id from stages where id = new.stage_id), coalesce((select revision from fests where id = (select fest_id from games where id = (select game_id from stages where id = new.stage_id))), 0), strftime('%Y-%m-%dT%H:%M:%fZ','now'), (select actor_user_id from audit_ctx where id = 1), (select request_id from audit_ctx where id = 1), 2, json_object('t', 'stage_standings', 'r', json_remove(json_object('stage_id', new."stage_id", 'rank', new."rank", 'participant_id', new."participant_id", 'metrics_json', new."metrics_json"), case when old."participant_id" is new."participant_id" then '$."participant_id"' else '$."__dope_keep__"' end, case when old."metrics_json" is new."metrics_json" then '$."metrics_json"' else '$."__dope_keep__"' end)), strftime('%Y-%m-%dT%H:%M:%fZ','now');
end;

-- trigger journal_stages_delete
CREATE TRIGGER journal_stages_delete after delete on stages
begin
  insert into journal(fest_id, game_id, seq, ts, actor_user_id, request_id, op, payload, created_at)
  select (select fest_id from games where id = old.game_id), old.game_id, coalesce((select revision from fests where id = (select fest_id from games where id = old.game_id)), 0), strftime('%Y-%m-%dT%H:%M:%fZ','now'), (select actor_user_id from audit_ctx where id = 1), (select request_id from audit_ctx where id = 1), 3, json_object('t', 'stages', 'r', json_object('id', old."id")), strftime('%Y-%m-%dT%H:%M:%fZ','now');
end;

-- trigger journal_stages_insert
CREATE TRIGGER journal_stages_insert after insert on stages
begin
  insert into journal(fest_id, game_id, seq, ts, actor_user_id, request_id, op, payload, created_at)
  select (select fest_id from games where id = new.game_id), new.game_id, coalesce((select revision from fests where id = (select fest_id from games where id = new.game_id)), 0), strftime('%Y-%m-%dT%H:%M:%fZ','now'), (select actor_user_id from audit_ctx where id = 1), (select request_id from audit_ctx where id = 1), 1, json_object('t', 'stages', 'r', json_object('id', new."id", 'fest_id', new."fest_id", 'game_id', new."game_id", 'code', new."code", 'title', new."title", 'stage_type', new."stage_type", 'position', new."position", 'status', new."status", 'config_json', new."config_json", 'block_code', new."block_code", 'wave_index', new."wave_index", 'group_code', new."group_code", 'kind', new."kind")), strftime('%Y-%m-%dT%H:%M:%fZ','now');
end;

-- trigger journal_stages_update
CREATE TRIGGER journal_stages_update after update on stages
begin
  insert into journal(fest_id, game_id, seq, ts, actor_user_id, request_id, op, payload, created_at)
  select (select fest_id from games where id = new.game_id), new.game_id, coalesce((select revision from fests where id = (select fest_id from games where id = new.game_id)), 0), strftime('%Y-%m-%dT%H:%M:%fZ','now'), (select actor_user_id from audit_ctx where id = 1), (select request_id from audit_ctx where id = 1), 2, json_object('t', 'stages', 'r', json_remove(json_object('id', new."id", 'fest_id', new."fest_id", 'game_id', new."game_id", 'code', new."code", 'title', new."title", 'stage_type', new."stage_type", 'position', new."position", 'status', new."status", 'config_json', new."config_json", 'block_code', new."block_code", 'wave_index', new."wave_index", 'group_code', new."group_code", 'kind', new."kind"), case when old."fest_id" is new."fest_id" then '$."fest_id"' else '$."__dope_keep__"' end, case when old."game_id" is new."game_id" then '$."game_id"' else '$."__dope_keep__"' end, case when old."code" is new."code" then '$."code"' else '$."__dope_keep__"' end, case when old."title" is new."title" then '$."title"' else '$."__dope_keep__"' end, case when old."stage_type" is new."stage_type" then '$."stage_type"' else '$."__dope_keep__"' end, case when old."position" is new."position" then '$."position"' else '$."__dope_keep__"' end, case when old."status" is new."status" then '$."status"' else '$."__dope_keep__"' end, case when old."config_json" is new."config_json" then '$."config_json"' else '$."__dope_keep__"' end, case when old."block_code" is new."block_code" then '$."block_code"' else '$."__dope_keep__"' end, case when old."wave_index" is new."wave_index" then '$."wave_index"' else '$."__dope_keep__"' end, case when old."group_code" is new."group_code" then '$."group_code"' else '$."__dope_keep__"' end, case when old."kind" is new."kind" then '$."kind"' else '$."__dope_keep__"' end)), strftime('%Y-%m-%dT%H:%M:%fZ','now');
end;

-- trigger participant_players_max_9
CREATE TRIGGER participant_players_max_9
before insert on participant_players
when (select count(*) from participant_players where participant_id = new.participant_id) >= 9
begin
  select raise(abort, 'team roster is limited to 9 players');
end;

