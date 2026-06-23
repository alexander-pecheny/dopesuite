package dopeserver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"dope/dope/domain/roster"
	"dope/dope/platform/util"
	"dope/dope/storage/auditmw"
	"dope/dope/storage/festaccess"
	"dope/dope/storage/journal"
	"dope/dope/storage/store"
)

const (
	dbFile             = "fest.db"
	defaultMatchCode   = "A"
	defaultVenueTitle  = "Москва-1"
	defaultGameCode    = "default"
	systemUserUsername = "system"
)

func openFestDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", store.BuildDSN(path))
	if err != nil {
		return nil, err
	}
	// Migrations toggle PRAGMA foreign_keys and run multi-statement
	// schema rewrites; those need to land on a single connection.
	// We pin the pool to 1 connection while migrating, then open it up
	// for runtime concurrency.
	db.SetMaxOpenConns(1)
	if err := migrateDB(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := journal.EnsureTriggers(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := journal.BackfillGameCheckpoints(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	db.SetMaxOpenConns(store.MaxOpenConns)
	db.SetMaxIdleConns(store.MaxOpenConns)
	db.SetConnMaxIdleTime(30 * time.Minute)
	return db, nil
}

// loadActiveContext picks an arbitrary fest/game/first-match to drive the
// transitional single-context handlers. Returns zero values (no error) when the
// DB has no fest yet — that's the default empty state.
func loadActiveContext(db *sql.DB) (festID, gameID int64, matchCode string, err error) {
	row := db.QueryRow(`
select t.id, g.id, coalesce((select m.code from matches m where m.game_id = g.id order by m.position, m.id limit 1), '')
from fests t
join games g on g.fest_id = t.id
order by t.id, g.position, g.id
limit 1`)
	if err = row.Scan(&festID, &gameID, &matchCode); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, 0, "", nil
		}
		return 0, 0, "", err
	}
	return festID, gameID, matchCode, nil
}

func migrateDB(db *sql.DB) error {
	if err := migrateLegacyFestSchema(db); err != nil {
		return err
	}
	// Drop the legacy audit_log AFTER triggers FIRST: later migration steps write
	// to audited tables (fests/users/organizers), and once audit_log is dropped
	// below those stale triggers would fail with "no such table: audit_log".
	if err := auditmw.DropLegacyAuditTriggers(db); err != nil {
		return err
	}
	_, err := db.Exec(`
create table if not exists schema_versions(
  version integer primary key,
  applied_at text not null
);

create table if not exists users(
  id integer primary key,
  telegram_user_id integer unique,
  telegram_username text,
  username text unique,
  is_system integer not null default 0,
  created_at text not null,
  updated_at text not null
);

create table if not exists invites(
  id integer primary key,
  code text not null unique,
  created_by integer not null references users(id),
  used_by integer references users(id),
  used_at text,
  created_at text not null,
  expires_at text not null
);

create table if not exists telegram_login_codes(
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
);

create table if not exists sessions(
  id integer primary key,
  user_id integer not null references users(id) on delete cascade,
  token_hash text not null unique,
  created_at text not null,
  expires_at text not null,
  last_seen_at text not null
);

create table if not exists schemes(
  id integer primary key,
  slug text not null unique,
  title text not null,
  version integer not null,
  schema_json text not null,
  created_at text not null
);

create table if not exists fests(
  id integer primary key,
  slug text not null unique,
  title text not null,
  description text not null default '',
  rating_id integer,
  created_by integer references users(id),
  revision integer not null default 1,
  created_at text not null,
  updated_at text not null
);

create table if not exists fest_organizers(
  fest_id integer not null references fests(id) on delete cascade,
  user_id integer not null references users(id) on delete cascade,
  role text not null default 'admin' check (role in ('creator','admin','host')),
  added_at text not null,
  primary key(fest_id, user_id)
);

create table if not exists fest_teams(
  id integer primary key,
  fest_id integer not null references fests(id) on delete cascade,
  rating_id integer,
  name text not null,
  city text not null default '',
  position real not null,
  number integer,
  deleted integer not null default 0
);

create table if not exists fest_players(
  id integer primary key,
  fest_id integer not null references fests(id) on delete cascade,
  rating_id integer,
  first_name text not null,
  last_name text not null default ''
);

create table if not exists fest_team_players(
  team_id integer not null references fest_teams(id) on delete cascade,
  player_id integer not null references fest_players(id) on delete cascade,
  roster_order integer not null,
  primary key(team_id, player_id)
);

create table if not exists game_player_team_overrides(
  fest_id integer not null references fests(id) on delete cascade,
  game_id integer not null references games(id) on delete cascade,
  player_id integer not null references fest_players(id) on delete cascade,
  source_team_id integer not null references fest_teams(id) on delete cascade,
  override_team_id integer not null references fest_teams(id) on delete cascade,
  created_at text not null,
  updated_at text not null,
  primary key(fest_id, game_id, player_id)
);

create table if not exists teams(
  id integer primary key,
  fest_id integer not null references fests(id) on delete cascade,
  name text not null,
  city text not null default ''
);

create table if not exists players(
  id integer primary key,
  fest_id integer not null references fests(id) on delete cascade,
  first_name text not null,
  last_name text not null default ''
);

create table if not exists team_players(
  team_id integer not null references teams(id) on delete cascade,
  player_id integer not null references players(id) on delete cascade,
  roster_order integer not null,
  primary key(team_id, player_id)
);

create table if not exists games(
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
  updated_at text not null,
  unique(fest_id, code)
);

create table if not exists game_teams(
  game_id integer not null references games(id) on delete cascade,
  team_id integer not null references teams(id) on delete cascade,
  position integer not null,
  primary key(game_id, team_id)
);

create table if not exists game_players(
  game_id integer not null references games(id) on delete cascade,
  player_id integer not null references players(id) on delete cascade,
  position integer not null,
  primary key(game_id, player_id)
);

create table if not exists game_team_players(
  game_id integer not null references games(id) on delete cascade,
  team_id integer not null references teams(id) on delete cascade,
  player_id integer not null references players(id) on delete cascade,
  roster_order integer not null,
  primary key(game_id, team_id, player_id)
);

create table if not exists game_assignments(
  game_id integer not null references games(id) on delete cascade,
  basket integer not null,
  number integer not null,
  team_id integer references teams(id),
  player_id integer references players(id),
  primary key(game_id, basket, number),
  check ((team_id is not null) <> (player_id is not null))
);

create table if not exists venues(
  id integer primary key,
  fest_id integer not null references fests(id) on delete cascade,
  number integer not null,
  title text not null,
  created_at text not null,
  updated_at text not null,
  unique(fest_id, number)
);

create table if not exists stages(
  id integer primary key,
  fest_id integer not null references fests(id) on delete cascade,
  game_id integer not null references games(id) on delete cascade,
  code text not null,
  title text not null,
  stage_type text not null,
  position integer not null,
  status text not null default 'active',
  config_json text not null default '{}',
  unique(game_id, code)
);

create table if not exists matches(
  id integer primary key,
  fest_id integer not null references fests(id) on delete cascade,
  game_id integer not null references games(id) on delete cascade,
  stage_id integer not null references stages(id) on delete cascade,
  code text not null,
  title text not null,
  position integer not null,
  participant_count integer not null,
  venue_id integer references venues(id),
  status text not null default 'active',
  revision integer not null default 1,
  unique(game_id, code)
);

create table if not exists match_slots(
  id integer primary key,
  match_id integer not null references matches(id) on delete cascade,
  slot_index integer not null,
  source_type text not null check (source_type in ('seed','from_match','reseed','placeholder')),
  source_ref_json text not null default '{}',
  team_id integer references teams(id),
  player_id integer references players(id),
  locked integer not null default 0,
  unique(match_id, slot_index)
);

create table if not exists themes(
  id integer primary key,
  match_id integer not null references matches(id) on delete cascade,
  team_id integer not null references teams(id) on delete cascade,
  kind text not null,
  theme_index integer not null,
  player_id integer references players(id),
  unique(match_id, team_id, kind, theme_index)
);

create table if not exists answers(
  id integer primary key,
  theme_id integer not null references themes(id) on delete cascade,
  answer_index integer not null,
  mark text not null default '',
  unique(theme_id, answer_index)
);

create table if not exists match_results(
  match_id integer not null references matches(id) on delete cascade,
  team_id integer not null references teams(id) on delete cascade,
  place real not null default 0,
  total integer not null default 0,
  plus integer not null default 0,
  tiebreak integer not null default 0,
  metrics_json text not null default '{}',
  primary key(match_id, team_id)
);

create table if not exists reseed_entries(
  stage_id integer not null references stages(id) on delete cascade,
  rank integer not null,
  team_id integer not null references teams(id) on delete cascade,
  metrics_json text not null,
  primary key(stage_id, rank)
);

-- journal is the single forward edit log: it is BOTH the durable, replayable
-- record of every mutation AND the source of the events streamed to viewers
-- (it replaces the old write-only "events" table). Each row is one edit, keyed
-- by a per-fest monotonic seq (the fest revision at append time). op is a DSL
-- opcode (see journal_dsl.go) and payload is the compact edit content, which is
-- also what gets broadcast over SSE. Finished runs are folded into
-- journal_segment (zstd) by the archiver; the hot table stays small and the log
-- never expires. See journal_dsl.go / journal_replay.go / journal_archive.go.
create table if not exists journal(
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
create index if not exists journal_fest_seq on journal(fest_id, seq);

-- journal_dict interns table/column names and request-ids to small ids so cold
-- segments stay compact (also used by the audit_log converter).
create table if not exists journal_dict(
  id  integer primary key,
  str text not null unique
);

-- journal_segment holds contiguous runs of journal rows folded into one
-- zstd-compressed DSL stream by the archiver. Append-only; never pruned.
create table if not exists journal_segment(
  id          integer primary key,
  fest_id     integer not null,
  seq_start   integer not null,
  seq_end     integer not null,
  dsl_version integer not null,
  n_records   integer not null,
  blob        blob not null,
  created_at  text not null
);

-- journal_checkpoint stores sparse full-state snapshots of a single GAME so
-- replay/revert never has to start from the literal beginning. Revert is
-- per-game (games are independent units); the checkpoint at a game's first seq
-- is its genesis. seq is the fest revision at capture time.
create table if not exists journal_checkpoint(
  game_id     integer not null,
  seq         integer not null,
  state_blob  blob not null,
  dsl_version integer not null,
  created_at  text not null,
  primary key(game_id, seq)
);

create trigger if not exists team_players_max_9
before insert on team_players
when (select count(*) from team_players where team_id = new.team_id) >= 9
begin
  select raise(abort, 'team roster is limited to 9 players');
end;

create trigger if not exists game_team_players_max_9
before insert on game_team_players
when (select count(*) from game_team_players where game_id = new.game_id and team_id = new.team_id) >= 9
begin
  select raise(abort, 'team roster is limited to 9 players');
end;

create trigger if not exists fest_team_players_max_9
before insert on fest_team_players
when (select count(*) from fest_team_players where team_id = new.team_id) >= 9
begin
  select raise(abort, 'team roster is limited to 9 players');
end;

insert or ignore into schema_versions(version, applied_at) values(2, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`)
	if err != nil {
		return err
	}
	if err := store.AddColumnsIfMissing(db, "fests", []store.ColumnSpec{
		{Name: "start_date", Type: "TEXT"},
		{Name: "end_date", Type: "TEXT"},
		{Name: "is_public", Type: "INTEGER NOT NULL DEFAULT 0"},
	}); err != nil {
		return err
	}
	// The old write-only "events" table is superseded by "journal" (created
	// above). Nothing ever read events back, so it is dropped outright rather
	// than migrated.
	if _, err := db.Exec(`drop table if exists events`); err != nil {
		return err
	}
	// The legacy before/after-snapshot audit_log + its support table are retired;
	// the forward journal (with row-op triggers + per-game checkpoints) is now the
	// durable edit log. Existing audit_log data can be archived first with the
	// `convert-audit` subcommand; here we drop it so it stops consuming space.
	if _, err := db.Exec(`drop table if exists audit_log`); err != nil {
		return err
	}
	if _, err := db.Exec(`drop table if exists audit_trigger_state`); err != nil {
		return err
	}
	// Early-adopter journal tables (created before per-game scoping) miss the
	// game_id column / use the old per-fest checkpoint key. CREATE IF NOT EXISTS
	// won't alter them, so reconcile here. The journal hot rows are preserved
	// (game_id backfills as NULL for old semantic-event rows); the checkpoint
	// cache is rebuilt by backfillGameCheckpoints, so dropping a stale-shaped one
	// is safe.
	if err := store.AddColumnsIfMissing(db, "journal", []store.ColumnSpec{{Name: "game_id", Type: "integer"}}); err != nil {
		return err
	}
	// Created after the game_id column exists (it indexes that column), so it
	// can't run inside the CREATE block above on an early-adopter journal.
	if _, err := db.Exec(`create index if not exists journal_game_seq on journal(game_id, seq)`); err != nil {
		return err
	}
	// Backfill game_id on semantic event rows recorded before they were
	// attributed, borrowing it from a row-op of the same request — so the
	// per-game history shows those earlier edits with descriptions. Idempotent.
	if _, err := db.Exec(`
update journal set game_id = (
  select j2.game_id from journal j2
  where j2.request_id = journal.request_id and j2.game_id is not null limit 1)
where game_id is null and request_id is not null
  and exists (select 1 from journal j3
    where j3.request_id = journal.request_id and j3.game_id is not null)`); err != nil {
		return err
	}
	if !store.ColumnExists(db, "journal_checkpoint", "game_id") {
		if _, err := db.Exec(`drop table if exists journal_checkpoint`); err != nil {
			return err
		}
		if _, err := db.Exec(`create table if not exists journal_checkpoint(
  game_id integer not null, seq integer not null, state_blob blob not null,
  dsl_version integer not null, created_at text not null, primary key(game_id, seq))`); err != nil {
			return err
		}
	}
	if err := festaccess.MigrateFestOrganizerRoles(db); err != nil {
		return err
	}
	if _, err := db.Exec(`insert or ignore into schema_versions(version, applied_at) values(3, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`); err != nil {
		return err
	}
	if err := store.AddColumnsIfMissing(db, "games", []store.ColumnSpec{
		{Name: "state_json", Type: "TEXT NOT NULL DEFAULT '{}'"},
	}); err != nil {
		return err
	}
	if _, err := db.Exec(`insert or ignore into schema_versions(version, applied_at) values(4, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`); err != nil {
		return err
	}
	if err := store.AddColumnsIfMissing(db, "users", []store.ColumnSpec{
		{Name: "password_hash", Type: "TEXT"},
		{Name: "password_salt", Type: "TEXT"},
	}); err != nil {
		return err
	}
	if _, err := db.Exec(`insert or ignore into schema_versions(version, applied_at) values(5, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`); err != nil {
		return err
	}
	if _, err := db.Exec(`insert or ignore into schema_versions(version, applied_at) values(6, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`); err != nil {
		return err
	}
	if _, err := db.Exec(`insert or ignore into schema_versions(version, applied_at) values(7, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`); err != nil {
		return err
	}
	if err := store.AddColumnsIfMissing(db, "fest_teams", []store.ColumnSpec{
		{Name: "number", Type: "INTEGER"},
	}); err != nil {
		return err
	}
	if _, err := db.Exec(`create unique index if not exists fest_teams_fest_number_idx on fest_teams(fest_id, number) where number is not null`); err != nil {
		return err
	}
	if _, err := db.Exec(`insert or ignore into schema_versions(version, applied_at) values(8, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`); err != nil {
		return err
	}
	if err := store.AddColumnsIfMissing(db, "fest_teams", []store.ColumnSpec{
		{Name: "deleted", Type: "INTEGER NOT NULL DEFAULT 0"},
	}); err != nil {
		return err
	}
	if _, err := db.Exec(`insert or ignore into schema_versions(version, applied_at) values(9, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`); err != nil {
		return err
	}
	if err := store.AddColumnsIfMissing(db, "games", []store.ColumnSpec{
		{Name: "slug", Type: "TEXT"},
	}); err != nil {
		return err
	}
	if _, err := db.Exec(`create unique index if not exists games_fest_slug_idx on games(fest_id, slug) where slug is not null`); err != nil {
		return err
	}
	if _, err := db.Exec(`insert or ignore into schema_versions(version, applied_at) values(9, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`); err != nil {
		return err
	}
	if err := relaxFestSlugNotNull(db); err != nil {
		return err
	}
	if _, err := db.Exec(`insert or ignore into schema_versions(version, applied_at) values(10, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`); err != nil {
		return err
	}
	if _, err := db.Exec(`
create table if not exists game_player_team_overrides(
  fest_id integer not null references fests(id) on delete cascade,
  game_id integer not null references games(id) on delete cascade,
  player_id integer not null references fest_players(id) on delete cascade,
  source_team_id integer not null references fest_teams(id) on delete cascade,
  override_team_id integer not null references fest_teams(id) on delete cascade,
  created_at text not null,
  updated_at text not null,
  primary key(fest_id, game_id, player_id)
);
create index if not exists game_player_team_overrides_game_idx on game_player_team_overrides(game_id);
insert or ignore into schema_versions(version, applied_at) values(11, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`); err != nil {
		return err
	}
	if err := auditmw.InstallAuditSchema(db); err != nil {
		return err
	}
	if _, err := db.Exec(`insert or ignore into schema_versions(version, applied_at) values(12, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`); err != nil {
		return err
	}
	// v13: backfill team numbers so every active fest_team has one. Team number
	// is the universal team identity across game formats; an editing guard
	// blocks play until teams are numbered, and this backfill makes existing
	// fests editable without a manual re-number. Gated on the version row so it
	// runs exactly once and does not undo an intentional later "clear numbers".
	var hasV13 int
	if err := db.QueryRow(`select count(*) from schema_versions where version = 13`).Scan(&hasV13); err != nil {
		return err
	}
	if hasV13 == 0 {
		if err := backfillFestTeamNumbers(db); err != nil {
			return err
		}
		if _, err := db.Exec(`insert or ignore into schema_versions(version, applied_at) values(13, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`); err != nil {
			return err
		}
	}
	// v14: give the EK game-scoped `teams` table a `number` natural key (the same
	// universal team identity OD/KSI use). teams.id stays the physical FK for
	// gameplay rows (ON DELETE CASCADE preserved); number drives seed matching so
	// re-seeding follows teams by identity and same-named teams stay distinct.
	// Nullable: scheme-defined teams without a printed number keep working.
	if err := store.AddColumnsIfMissing(db, "teams", []store.ColumnSpec{
		{Name: "number", Type: "INTEGER"},
	}); err != nil {
		return err
	}
	if _, err := db.Exec(`create unique index if not exists teams_fest_number_idx on teams(fest_id, number) where number is not null`); err != nil {
		return err
	}
	var hasV14 int
	if err := db.QueryRow(`select count(*) from schema_versions where version = 14`).Scan(&hasV14); err != nil {
		return err
	}
	if hasV14 == 0 {
		if err := backfillEKTeamNumbers(db); err != nil {
			return err
		}
		if _, err := db.Exec(`insert or ignore into schema_versions(version, applied_at) values(14, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`); err != nil {
			return err
		}
	}
	// v15: give each game a fixed random_seed. Reseed lots (Жребий) are now
	// derived deterministically from this seed, so a reseed recomputes identically
	// every time and an untick/retick (or any unrelated edit) can never reshuffle a
	// tie. Backfill a distinct random seed for every existing game exactly once;
	// new games get one at creation.
	if err := store.AddColumnsIfMissing(db, "games", []store.ColumnSpec{
		{Name: "random_seed", Type: "TEXT NOT NULL DEFAULT ''"},
	}); err != nil {
		return err
	}
	// Seed every newly-created game once, regardless of which insert path made it
	// (SQLite disallows a non-constant column default like randomblob, so a trigger
	// fills it). The update doesn't re-fire this AFTER INSERT trigger, so no loop.
	if _, err := db.Exec(`
create trigger if not exists games_random_seed_default
after insert on games
when new.random_seed = ''
begin
  update games set random_seed = lower(hex(randomblob(16))) where id = new.id;
end`); err != nil {
		return err
	}
	var hasV15 int
	if err := db.QueryRow(`select count(*) from schema_versions where version = 15`).Scan(&hasV15); err != nil {
		return err
	}
	if hasV15 == 0 {
		if _, err := db.Exec(`update games set random_seed = lower(hex(randomblob(16))) where random_seed = ''`); err != nil {
			return err
		}
		if _, err := db.Exec(`insert or ignore into schema_versions(version, applied_at) values(15, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`); err != nil {
			return err
		}
	}
	// v16: per-game "Экран" (projector board) display settings — colours, font
	// scale, column-count override, city/country toggles. Stored as an opaque
	// JSON blob so all hosts of a game share one configuration (the screen is a
	// shared projector, not a per-browser preference).
	if err := store.AddColumnsIfMissing(db, "games", []store.ColumnSpec{
		{Name: "screen_settings_json", Type: "TEXT NOT NULL DEFAULT '{}'"},
	}); err != nil {
		return err
	}
	return nil
}

// backfillEKTeamNumbers gives existing EK `teams` rows a number, matched to
// fest_teams by name, so re-seeding finds them by identity instead of creating
// duplicates. Best-effort and conflict-free: ambiguous (duplicate) names are
// skipped, and a number already claimed by another teams row in the fest is not
// reused — those rows stay null (the unresolvable legacy ambiguity this whole
// change exists to prevent going forward). Gated by the caller to run once.
func backfillEKTeamNumbers(db *sql.DB) error {
	festRows, err := db.Query(`select distinct fest_id from teams where number is null`)
	if err != nil {
		return err
	}
	var festIDs []int64
	for festRows.Next() {
		var id int64
		if err := festRows.Scan(&id); err != nil {
			festRows.Close()
			return err
		}
		festIDs = append(festIDs, id)
	}
	if err := festRows.Err(); err != nil {
		festRows.Close()
		return err
	}
	festRows.Close()

	for _, festID := range festIDs {
		// fest_teams name -> number, dropping ambiguous duplicate names.
		numByName := map[string]int64{}
		ambiguous := map[string]bool{}
		ftRows, err := db.Query(`select name, number from fest_teams where fest_id = ? and deleted = 0 and number is not null`, festID)
		if err != nil {
			return err
		}
		for ftRows.Next() {
			var name string
			var number int64
			if err := ftRows.Scan(&name, &number); err != nil {
				ftRows.Close()
				return err
			}
			key := roster.SeedTeamNameKey(name)
			if _, seen := numByName[key]; seen {
				ambiguous[key] = true
			} else {
				numByName[key] = number
			}
		}
		if err := ftRows.Err(); err != nil {
			ftRows.Close()
			return err
		}
		ftRows.Close()
		for key := range ambiguous {
			delete(numByName, key)
		}

		// Numbers already claimed by teams rows in this fest.
		claimed := map[int64]bool{}
		clRows, err := db.Query(`select number from teams where fest_id = ? and number is not null`, festID)
		if err != nil {
			return err
		}
		for clRows.Next() {
			var n int64
			if err := clRows.Scan(&n); err != nil {
				clRows.Close()
				return err
			}
			claimed[n] = true
		}
		if err := clRows.Err(); err != nil {
			clRows.Close()
			return err
		}
		clRows.Close()

		teamRows, err := db.Query(`select id, name from teams where fest_id = ? and number is null order by id`, festID)
		if err != nil {
			return err
		}
		type pending struct {
			id  int64
			num int64
		}
		var assigns []pending
		for teamRows.Next() {
			var id int64
			var name string
			if err := teamRows.Scan(&id, &name); err != nil {
				teamRows.Close()
				return err
			}
			num, ok := numByName[roster.SeedTeamNameKey(name)]
			if ok && !claimed[num] {
				claimed[num] = true
				assigns = append(assigns, pending{id: id, num: num})
			}
		}
		if err := teamRows.Err(); err != nil {
			teamRows.Close()
			return err
		}
		teamRows.Close()
		for _, a := range assigns {
			if _, err := db.Exec(`update teams set number = ? where id = ?`, a.num, a.id); err != nil {
				return err
			}
		}
	}
	return nil
}

// backfillFestTeamNumbers assigns a number to every active (non-deleted)
// fest_team that lacks one, per fest, continuing past the largest number ever
// used in that fest (soft-deleted rows included, so a returning team can't
// collide). Deterministic order (position, id) matches the import/auto-assign
// ordering. Idempotent and a no-op once every active team is numbered; the
// caller gates it on the v13 version row so a later host "clear numbers" is not
// silently undone on the next startup.
func backfillFestTeamNumbers(db *sql.DB) error {
	rows, err := db.Query(`select distinct fest_id from fest_teams where deleted = 0 and number is null`)
	if err != nil {
		return err
	}
	var festIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		festIDs = append(festIDs, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, festID := range festIDs {
		var maxNum sql.NullInt64
		if err := db.QueryRow(`select max(number) from fest_teams where fest_id = ?`, festID).Scan(&maxNum); err != nil {
			return err
		}
		next := maxNum.Int64 + 1 // NullInt64 zero value is 0, so this is 1 when no numbers exist
		idRows, err := db.Query(`select id from fest_teams where fest_id = ? and deleted = 0 and number is null order by position, id`, festID)
		if err != nil {
			return err
		}
		var ids []int64
		for idRows.Next() {
			var id int64
			if err := idRows.Scan(&id); err != nil {
				idRows.Close()
				return err
			}
			ids = append(ids, id)
		}
		if err := idRows.Err(); err != nil {
			idRows.Close()
			return err
		}
		idRows.Close()
		for _, id := range ids {
			if _, err := db.Exec(`update fest_teams set number = ? where id = ?`, next, id); err != nil {
				return err
			}
			next++
		}
	}
	return nil
}

// relaxFestSlugNotNull rebuilds the fests table so slug is nullable. The
// original create-table set slug to NOT NULL UNIQUE; we want slug to be
// optional — set only when the user picks one. SQLite can't ALTER a column's
// nullability, so we copy the table.
func relaxFestSlugNotNull(db *sql.DB) error {
	var notNull int
	if err := db.QueryRow(`select "notnull" from pragma_table_info('fests') where name = 'slug'`).Scan(&notNull); err != nil {
		return err
	}
	if notNull == 0 {
		return nil
	}
	ctx := context.Background()
	if _, err := db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		return err
	}
	fkOff := true
	defer func() {
		if fkOff {
			_, _ = db.Exec(`PRAGMA foreign_keys = ON`)
		}
	}()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// legacy_alter_table = ON keeps RENAME TO from rewriting fest_id FK
	// references in other tables — we'll point them back at the new fests
	// table just by creating one with the original name.
	if _, err := tx.ExecContext(ctx, `PRAGMA legacy_alter_table = ON`); err != nil {
		return err
	}
	legacyOn := true
	defer func() {
		if legacyOn {
			_, _ = tx.ExecContext(ctx, `PRAGMA legacy_alter_table = OFF`)
		}
	}()
	if _, err := tx.ExecContext(ctx, `alter table fests rename to fests_slug_migration_old`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
create table fests(
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
)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
insert into fests(id, slug, title, description, rating_id, created_by, revision, created_at, updated_at, start_date, end_date, is_public)
select id, slug, title, description, rating_id, created_by, revision, created_at, updated_at, start_date, end_date, is_public
from fests_slug_migration_old`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `drop table fests_slug_migration_old`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `PRAGMA legacy_alter_table = OFF`); err != nil {
		return err
	}
	legacyOn = false
	if err := tx.Commit(); err != nil {
		return err
	}
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		return err
	}
	fkOff = false
	return verifyForeignKeys(db)
}

func migrateLegacyFestSchema(db *sql.DB) error {
	ctx := context.Background()
	hasLegacy, err := sqliteTableExists(ctx, db, "tournaments")
	if err != nil || !hasLegacy {
		return err
	}
	hasFests, err := sqliteTableExists(ctx, db, "fests")
	if err != nil {
		return err
	}
	if hasFests {
		return errors.New("cannot migrate legacy fest schema: both tournaments and fests tables exist")
	}
	if _, err := db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		return err
	}
	foreignKeysOff := true
	defer func() {
		if foreignKeysOff {
			_, _ = db.Exec(`PRAGMA foreign_keys = ON`)
		}
	}()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `drop trigger if exists tournament_team_players_max_9`); err != nil {
		return err
	}
	tableRenames := []struct {
		Old string
		New string
	}{
		{"tournaments", "fests"},
		{"tournament_organizers", "fest_organizers"},
		{"tournament_teams", "fest_teams"},
		{"tournament_players", "fest_players"},
		{"tournament_team_players", "fest_team_players"},
	}
	for _, rename := range tableRenames {
		if err := renameTableIfExists(ctx, tx, rename.Old, rename.New); err != nil {
			return err
		}
	}
	for _, table := range []string{
		"fest_organizers",
		"fest_teams",
		"fest_players",
		"teams",
		"players",
		"games",
		"venues",
		"stages",
		"matches",
		"events",
	} {
		if err := renameColumnIfExists(ctx, tx, table, "tournament_id", "fest_id"); err != nil {
			return err
		}
	}
	if err := rebuildLegacyGamesTable(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		return err
	}
	foreignKeysOff = false
	return verifyForeignKeys(db)
}

func renameTableIfExists(ctx context.Context, tx *sql.Tx, oldName, newName string) error {
	oldExists, err := sqliteTableExists(ctx, tx, oldName)
	if err != nil || !oldExists {
		return err
	}
	newExists, err := sqliteTableExists(ctx, tx, newName)
	if err != nil {
		return err
	}
	if newExists {
		return fmt.Errorf("cannot rename %s to %s: target table exists", oldName, newName)
	}
	_, err = tx.ExecContext(ctx, fmt.Sprintf("alter table %s rename to %s", oldName, newName))
	return err
}

func renameColumnIfExists(ctx context.Context, tx *sql.Tx, table, oldName, newName string) error {
	tableExists, err := sqliteTableExists(ctx, tx, table)
	if err != nil || !tableExists {
		return err
	}
	oldExists, err := sqliteColumnExists(ctx, tx, table, oldName)
	if err != nil || !oldExists {
		return err
	}
	newExists, err := sqliteColumnExists(ctx, tx, table, newName)
	if err != nil {
		return err
	}
	if newExists {
		return fmt.Errorf("cannot rename %s.%s to %s: target column exists", table, oldName, newName)
	}
	_, err = tx.ExecContext(ctx, fmt.Sprintf("alter table %s rename column %s to %s", table, oldName, newName))
	return err
}

func rebuildLegacyGamesTable(ctx context.Context, tx *sql.Tx) error {
	exists, err := sqliteTableExists(ctx, tx, "games")
	if err != nil || !exists {
		return err
	}
	hasStateJSON, err := sqliteColumnExists(ctx, tx, "games", "state_json")
	if err != nil {
		return err
	}
	stateJSONExpr := "'{}'"
	if hasStateJSON {
		stateJSONExpr = "coalesce(state_json, '{}')"
	}
	if _, err := tx.ExecContext(ctx, `PRAGMA legacy_alter_table = ON`); err != nil {
		return err
	}
	legacyAlterTableOn := true
	defer func() {
		if legacyAlterTableOn {
			_, _ = tx.ExecContext(ctx, `PRAGMA legacy_alter_table = OFF`)
		}
	}()
	if _, err := tx.ExecContext(ctx, `alter table games rename to games_fest_migration_old`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
create table games(
  id integer primary key,
  fest_id integer not null references fests(id) on delete cascade,
  code text not null,
  title text not null,
  game_type text not null,
  position integer not null,
  scheme_id integer references schemes(id),
  scheme_json text not null default '{}',
  state_json text not null default '{}',
  status text not null default 'pending',
  team_list_source text not null default 'fest' check (team_list_source in ('fest','game')),
  roster_source text not null default 'fest' check (roster_source in ('fest','game')),
  revision integer not null default 1,
  created_at text not null,
  updated_at text not null,
  unique(fest_id, code)
)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
insert into games(id, fest_id, code, title, game_type, position, scheme_id, scheme_json, state_json, status, team_list_source, roster_source, revision, created_at, updated_at)
select id, fest_id, code, title, game_type, position, scheme_id, scheme_json, %s, status,
       case team_list_source when 'tournament' then 'fest' else team_list_source end,
       case roster_source when 'tournament' then 'fest' else roster_source end,
       revision, created_at, updated_at
from games_fest_migration_old`, stateJSONExpr)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `drop table games_fest_migration_old`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `PRAGMA legacy_alter_table = OFF`); err != nil {
		return err
	}
	legacyAlterTableOn = false
	return nil
}

func sqliteTableExists(ctx context.Context, q store.Queryer, name string) (bool, error) {
	var count int
	if err := q.QueryRowContext(ctx, `select count(*) from sqlite_master where type = 'table' and name = ?`, name).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func sqliteColumnExists(ctx context.Context, q store.Queryer, table, column string) (bool, error) {
	rows, err := q.QueryContext(ctx, `select name from pragma_table_info(?)`, table)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func verifyForeignKeys(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		var table string
		var rowID, parent, fkID any
		if err := rows.Scan(&table, &rowID, &parent, &fkID); err != nil {
			return err
		}
		return fmt.Errorf("foreign key check failed after legacy fest migration: table=%s rowid=%v parent=%v fkid=%v", table, rowID, parent, fkID)
	}
	return rows.Err()
}

func ensureSystemUser(ctx context.Context, tx *sql.Tx) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `select id from users where is_system = 1 limit 1`).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	now := util.UtcNow()
	return store.InsertReturningID(ctx, tx, `
insert into users(telegram_user_id, telegram_username, username, is_system, created_at, updated_at)
values(null, null, ?, 1, ?, ?)`, systemUserUsername, now, now)
}

func defaultGameID(ctx context.Context, q store.Queryer, festID int64) (int64, error) {
	var id int64
	err := q.QueryRowContext(ctx, `select id from games where fest_id = ? order by position, id limit 1`, festID).Scan(&id)
	return id, err
}

// util.ValidateSlug enforces the slug grammar: 1-64 chars of a-z, 0-9, hyphen;
// the slug cannot be all digits (so it never collides with a numeric ID lookup).

// resolveGameID accepts either a positive integer (the game id) or a slug and
// returns the numeric game id within the given fest.
func resolveGameID(ctx context.Context, q store.Queryer, festID int64, ref string) (int64, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return 0, sql.ErrNoRows
	}
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil && id > 0 {
		var found int64
		if err := q.QueryRowContext(ctx, `select id from games where id = ? and fest_id = ?`, id, festID).Scan(&found); err != nil {
			return 0, err
		}
		return found, nil
	}
	var id int64
	if err := q.QueryRowContext(ctx, `select id from games where fest_id = ? and slug = ?`, festID, ref).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}
