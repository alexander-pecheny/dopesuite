package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	dbFile             = "fest.db"
	defaultMatchCode   = "A"
	defaultVenueTitle  = "Москва-1"
	defaultGameCode    = "default"
	defaultGameType    = "ek"
	systemUserUsername = "system"
)

type VenueView struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
}

type FestView struct {
	Slug              string      `json:"slug"`
	Title             string      `json:"title"`
	Revision          int64       `json:"revision"`
	UpdatedAt         string      `json:"updatedAt"`
	SchemaJSON        string      `json:"schemaJson,omitempty"`
	QuestionValues    [5]int      `json:"questionValues"`
	RegularThemeCount int         `json:"regularThemeCount"`
	Venues            []VenueView `json:"venues"`
	Stages            []StageView `json:"stages"`
}

type StageView struct {
	Code     string          `json:"code"`
	Title    string          `json:"title"`
	Type     string          `json:"stage_type"`
	Position int             `json:"position"`
	Status   string          `json:"status"`
	Matches  []FestMatchView `json:"matches,omitempty"`
}

type FestMatchView struct {
	Code             string             `json:"code"`
	Title            string             `json:"title"`
	Position         int                `json:"position"`
	ParticipantCount int                `json:"participantCount"`
	Status           string             `json:"status"`
	Revision         int64              `json:"revision"`
	Venue            *VenueView         `json:"venue,omitempty"`
	Teams            []MatchTeamSummary `json:"teams"`
}

type MatchTeamSummary struct {
	Name       string  `json:"name"`
	Source     string  `json:"source,omitempty"`
	SourceType string  `json:"sourceType,omitempty"`
	Place      float64 `json:"place"`
	Total      int     `json:"total"`
	Plus       int     `json:"plus"`
	Tiebreak   int     `json:"tiebreak"`
}

type venueUpdateRequest struct {
	Title string `json:"title"`
}

type matchVenueRequest struct {
	Number      int `json:"number"`
	VenueNumber int `json:"venueNumber"`
}

type eventEnvelope struct {
	Scope    string          `json:"scope"`
	Revision int64           `json:"revision"`
	Data     json.RawMessage `json:"data"`
}

type festScheme struct {
	SchemaVersion     int             `json:"schemaVersion"`
	Slug              string          `json:"slug"`
	Title             string          `json:"title"`
	GameType          string          `json:"gameType"`
	QuestionValues    []int           `json:"questionValues"`
	RegularThemeCount int             `json:"regularThemeCount"`
	Venues            []schemeVenue   `json:"venues"`
	Stages            []schemeStage   `json:"stages"`
	Teams             []schemeTeam    `json:"teams"`
	TourComp          json.RawMessage `json:"tourComp,omitempty"`
	NTeams            int             `json:"nTeams,omitempty"`
	Themes            int             `json:"themes,omitempty"`
	Participants      []string        `json:"participants,omitempty"`
}

type schemeTeam struct {
	Name    string   `json:"name"`
	City    string   `json:"city"`
	Basket  int      `json:"basket"`
	Number  int      `json:"number"`
	Players []string `json:"players"`
}

type schemeVenue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
}

type schemeStage struct {
	Code      string          `json:"code"`
	Title     string          `json:"title"`
	StageType string          `json:"stage_type"`
	Position  int             `json:"position"`
	Matches   []schemeMatch   `json:"matches"`
	Teams     []schemeSlot    `json:"teams"`
	Sort      json.RawMessage `json:"sort"`
	Config    json.RawMessage `json:"config"`
	Layout    json.RawMessage `json:"layout"`
}

type schemeMatch struct {
	Code             string       `json:"code"`
	Title            string       `json:"title"`
	Venue            int          `json:"venue"`
	ParticipantCount int          `json:"participantCount"`
	Slots            []schemeSlot `json:"slots"`
}

type schemeSlot struct {
	Seed        *schemeSeedRef      `json:"seed,omitempty"`
	FromMatch   *schemeFromMatchRef `json:"fromMatch,omitempty"`
	Reseed      *schemeReseedRef    `json:"reseed,omitempty"`
	Team        *schemeTeamRef      `json:"team,omitempty"`
	Placeholder string              `json:"placeholder,omitempty"`
	Label       string              `json:"label,omitempty"`
}

type schemeSeedRef struct {
	Basket   int `json:"basket"`
	Number   int `json:"number"`
	Position int `json:"position"`
}

type schemeFromMatchRef struct {
	Match string `json:"match"`
	Place int    `json:"place"`
}

type schemeReseedRef struct {
	Stage string `json:"stage"`
	Rank  int    `json:"rank"`
}

type schemeTeamRef struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	City    string   `json:"city"`
	Label   string   `json:"label"`
	Players []string `json:"players"`
}

type dbQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type dbMatchState struct {
	MatchID      int64
	Code         string
	Title        string
	Status       string
	Revision     int64
	FestRevision int64
	UpdatedAt    time.Time
	StageCode    string
	StageTitle   string
	Venue        *VenueView
	State        MatchState
	TeamIDs      []int64
}

func openFestDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys = ON; PRAGMA journal_mode = WAL;`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := migrateDB(db); err != nil {
		_ = db.Close()
		return nil, err
	}
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
  number integer
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

create table if not exists events(
  id integer primary key,
  fest_id integer not null references fests(id) on delete cascade,
  revision integer not null,
  type text not null,
  payload_json text not null,
  created_at text not null
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
	if err := addColumnsIfMissing(db, "fests", []columnSpec{
		{Name: "start_date", Type: "TEXT"},
		{Name: "end_date", Type: "TEXT"},
		{Name: "is_public", Type: "INTEGER NOT NULL DEFAULT 0"},
	}); err != nil {
		return err
	}
	if _, err := db.Exec(`insert or ignore into schema_versions(version, applied_at) values(3, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`); err != nil {
		return err
	}
	if err := addColumnsIfMissing(db, "games", []columnSpec{
		{Name: "state_json", Type: "TEXT NOT NULL DEFAULT '{}'"},
	}); err != nil {
		return err
	}
	if _, err := db.Exec(`insert or ignore into schema_versions(version, applied_at) values(4, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`); err != nil {
		return err
	}
	if err := addColumnsIfMissing(db, "users", []columnSpec{
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
	if err := addColumnsIfMissing(db, "fest_teams", []columnSpec{
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
	if err := addColumnsIfMissing(db, "games", []columnSpec{
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

func sqliteTableExists(ctx context.Context, q dbQueryer, name string) (bool, error) {
	var count int
	if err := q.QueryRowContext(ctx, `select count(*) from sqlite_master where type = 'table' and name = ?`, name).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func sqliteColumnExists(ctx context.Context, q dbQueryer, table, column string) (bool, error) {
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

type columnSpec struct {
	Name string
	Type string
}

func addColumnsIfMissing(db *sql.DB, table string, columns []columnSpec) error {
	rows, err := db.Query(`select name from pragma_table_info(?)`, table)
	if err != nil {
		return err
	}
	existing := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, col := range columns {
		if existing[col.Name] {
			continue
		}
		if _, err := db.Exec(fmt.Sprintf("alter table %s add column %s %s", table, col.Name, col.Type)); err != nil {
			return err
		}
	}
	return nil
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
	now := utcNow()
	return insertReturningID(ctx, tx, `
insert into users(telegram_user_id, telegram_username, username, is_system, created_at, updated_at)
values(null, null, ?, 1, ?, ?)`, systemUserUsername, now, now)
}

func defaultGameID(ctx context.Context, q dbQueryer, festID int64) (int64, error) {
	var id int64
	err := q.QueryRowContext(ctx, `select id from games where fest_id = ? order by position, id limit 1`, festID).Scan(&id)
	return id, err
}

// validateSlug enforces the slug grammar: 1-64 chars of a-z, 0-9, hyphen;
// the slug cannot be all digits (so it never collides with a numeric ID lookup).
func validateSlug(slug string) error {
	if len(slug) == 0 {
		return errors.New("slug is empty")
	}
	if len(slug) > 64 {
		return errors.New("slug is longer than 64 characters")
	}
	allDigit := true
	for _, r := range slug {
		switch {
		case r >= 'a' && r <= 'z':
			allDigit = false
		case r == '-':
			allDigit = false
		case r >= '0' && r <= '9':
			// ok
		default:
			return errors.New("slug may contain only a-z, 0-9 and hyphen")
		}
	}
	if allDigit {
		return errors.New("slug cannot be all digits")
	}
	return nil
}

// resolveFestID accepts either a positive integer (the fest id) or a slug and
// returns the numeric fest id. Returns sql.ErrNoRows if no fest matches.
func resolveFestID(ctx context.Context, q dbQueryer, ref string) (int64, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return 0, sql.ErrNoRows
	}
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil && id > 0 {
		var found int64
		if err := q.QueryRowContext(ctx, `select id from fests where id = ?`, id).Scan(&found); err != nil {
			return 0, err
		}
		return found, nil
	}
	var id int64
	if err := q.QueryRowContext(ctx, `select id from fests where slug = ?`, ref).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

// resolveGameID accepts either a positive integer (the game id) or a slug and
// returns the numeric game id within the given fest.
func resolveGameID(ctx context.Context, q dbQueryer, festID int64, ref string) (int64, error) {
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

func insertTheme(ctx context.Context, tx *sql.Tx, matchID, teamID int64, kind string, themeIndex int, playerID int64, answers [5]string) error {
	var player any
	if playerID > 0 {
		player = playerID
	}
	themeID, err := insertReturningID(ctx, tx, `
insert into themes(match_id, team_id, kind, theme_index, player_id)
values(?, ?, ?, ?, ?)`, matchID, teamID, kind, themeIndex, player)
	if err != nil {
		return err
	}
	for answerIndex, mark := range answers {
		if _, err := tx.ExecContext(ctx, `
insert into answers(theme_id, answer_index, mark)
values(?, ?, ?)`, themeID, answerIndex, normalizeMark(mark)); err != nil {
			return err
		}
	}
	return nil
}

func insertReturningID(ctx context.Context, tx *sql.Tx, query string, args ...any) (int64, error) {
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (s *server) serveViewerHTML(w http.ResponseWriter, r *http.Request) {
	s.serveAppHTML(w, r, "static/viewer.html")
}

func (s *server) serveHostHTML(w http.ResponseWriter, r *http.Request) {
	s.serveAppHTML(w, r, "static/host.html")
}

func (s *server) serveAppHTML(w http.ResponseWriter, r *http.Request, path string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.assetNoCache {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeFileFS(w, r, s.assets, path)
}

func (s *server) handleImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !requireSameOriginUnsafe(w, r) {
		return
	}
	user, ok := s.lookupSession(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	festID, err := resolveFestID(r.Context(), s.db, strings.TrimSpace(r.URL.Query().Get("fest_id")))
	if err != nil || festID <= 0 {
		http.Error(w, "missing fest_id", http.StatusBadRequest)
		return
	}
	allowed, err := s.isOrganizer(r.Context(), festID, user.UserID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !allowed {
		exists, _, err := s.festVisibility(r.Context(), festID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !exists {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	defer r.Body.Close()

	var scheme festScheme
	if err := json.NewDecoder(r.Body).Decode(&scheme); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}

	if err := s.importSchemeIntoFest(r.Context(), festID, scheme); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	gameID, err := defaultGameID(r.Context(), s.db, festID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.mu.RLock()
	view, err := s.loadFestViewLocked(festID, gameID)
	s.mu.RUnlock()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data, _ := json.Marshal(view)
	s.broadcastState(festID, "fest", view.Revision, data)
	writeJSON(w, data)
}

func (s *server) importScheme(scheme festScheme) (FestView, error) {
	if s.db == nil {
		return FestView{}, errors.New("sqlite is not enabled")
	}
	if err := validateScheme(scheme); err != nil {
		return FestView{}, err
	}
	schemaJSON, err := json.Marshal(scheme)
	if err != nil {
		return FestView{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return FestView{}, err
	}
	defer tx.Rollback()

	if err := clearImportedData(ctx, tx); err != nil {
		return FestView{}, err
	}

	now := utcNow()
	systemID, err := ensureSystemUser(ctx, tx)
	if err != nil {
		return FestView{}, err
	}
	schemeID, err := insertReturningID(ctx, tx, `
insert into schemes(slug, title, version, schema_json, created_at)
values(?, ?, ?, ?, ?)`, scheme.Slug, scheme.Title, maxInt(scheme.SchemaVersion, 2), string(schemaJSON), now)
	if err != nil {
		return FestView{}, err
	}
	festID, err := insertReturningID(ctx, tx, `
insert into fests(slug, title, description, rating_id, created_by, revision, created_at, updated_at, is_public)
values(?, ?, '', null, ?, 1, ?, ?, 1)`, scheme.Slug, scheme.Title, systemID, now, now)
	if err != nil {
		return FestView{}, err
	}
	if _, err := tx.ExecContext(ctx, `
insert into fest_organizers(fest_id, user_id, added_at)
values(?, ?, ?)`, festID, systemID, now); err != nil {
		return FestView{}, err
	}
	gameType := scheme.GameType
	if gameType == "" {
		gameType = defaultGameType
	}
	gameID, err := insertReturningID(ctx, tx, `
insert into games(fest_id, code, title, game_type, position, scheme_id, scheme_json, status, team_list_source, roster_source, revision, created_at, updated_at)
values(?, ?, ?, ?, 1, ?, ?, 'pending', 'fest', 'fest', 1, ?, ?)`,
		festID, defaultGameCode, scheme.Title, gameType, schemeID, string(schemaJSON), now, now)
	if err != nil {
		return FestView{}, err
	}

	venueIDs := make(map[int]int64, len(scheme.Venues))
	for _, venue := range scheme.Venues {
		venueID, err := insertReturningID(ctx, tx, `
insert into venues(fest_id, number, title, created_at, updated_at)
values(?, ?, ?, ?, ?)`, festID, venue.Number, venue.Title, now, now)
		if err != nil {
			return FestView{}, err
		}
		venueIDs[venue.Number] = venueID
	}

	assignmentTeams := make(map[[2]int]int64, len(scheme.Teams))
	for _, team := range scheme.Teams {
		teamID, err := insertReturningID(ctx, tx, `
insert into teams(fest_id, name, city)
values(?, ?, ?)`, festID, team.Name, team.City)
		if err != nil {
			return FestView{}, err
		}
		for rosterOrder, fullName := range team.Players {
			fullName = strings.TrimSpace(fullName)
			if fullName == "" {
				continue
			}
			firstName, lastName := splitPlayerName(fullName)
			playerID, err := insertReturningID(ctx, tx, `
insert into players(fest_id, first_name, last_name)
values(?, ?, ?)`, festID, firstName, lastName)
			if err != nil {
				return FestView{}, err
			}
			if _, err := tx.ExecContext(ctx, `
insert into team_players(team_id, player_id, roster_order)
values(?, ?, ?)`, teamID, playerID, rosterOrder); err != nil {
				return FestView{}, err
			}
		}
		if _, err := tx.ExecContext(ctx, `
insert into game_assignments(game_id, basket, number, team_id, player_id)
values(?, ?, ?, ?, null)`, gameID, team.Basket, team.Number, teamID); err != nil {
			return FestView{}, err
		}
		assignmentTeams[[2]int{team.Basket, team.Number}] = teamID
	}

	firstMatchCode := ""
	for stageIndex, stage := range scheme.Stages {
		position := stage.Position
		if position == 0 {
			position = stageIndex + 1
		}
		configJSON := stageConfigJSON(stage)
		stageType := stage.StageType
		if stageType == "" {
			stageType = "matches"
		}
		stageID, err := insertReturningID(ctx, tx, `
insert into stages(fest_id, game_id, code, title, stage_type, position, status, config_json)
values(?, ?, ?, ?, ?, ?, 'pending', ?)`, festID, gameID, stage.Code, stage.Title, stageType, position, configJSON)
		if err != nil {
			return FestView{}, err
		}
		if stageType != "matches" {
			continue
		}
		for matchIndex, match := range stage.Matches {
			if firstMatchCode == "" {
				firstMatchCode = match.Code
			}
			participantCount := match.ParticipantCount
			if participantCount == 0 {
				participantCount = len(match.Slots)
			}
			var venueID any
			if id, ok := venueIDs[match.Venue]; ok {
				venueID = id
			}
			matchID, err := insertReturningID(ctx, tx, `
insert into matches(fest_id, game_id, stage_id, code, title, position, participant_count, venue_id, status, revision)
values(?, ?, ?, ?, ?, ?, ?, ?, 'pending', 1)`, festID, gameID, stageID, match.Code, match.Title, matchIndex+1, participantCount, venueID)
			if err != nil {
				return FestView{}, err
			}
			for slotIndex, slot := range match.Slots {
				sourceType, sourceRef := slotSource(slot)
				var resolvedTeamID int64
				if sourceType == "seed" && slot.Seed != nil {
					number := slot.Seed.Number
					if number == 0 {
						number = slot.Seed.Position
					}
					resolvedTeamID = assignmentTeams[[2]int{slot.Seed.Basket, number}]
				}
				if _, err := tx.ExecContext(ctx, `
insert into match_slots(match_id, slot_index, source_type, source_ref_json, team_id, locked)
values(?, ?, ?, ?, ?, 0)`, matchID, slotIndex, sourceType, sourceRef, nullableInt64(resolvedTeamID)); err != nil {
					return FestView{}, err
				}
				if resolvedTeamID > 0 {
					for themeIndex := 0; themeIndex < themeCount; themeIndex++ {
						if err := insertTheme(ctx, tx, matchID, resolvedTeamID, "regular", themeIndex, 0, [5]string{}); err != nil {
							return FestView{}, err
						}
					}
				}
			}
		}
	}

	if _, err := tx.ExecContext(ctx, `
insert into events(fest_id, revision, type, payload_json, created_at)
values(?, 1, 'import', ?, ?)`, festID, string(schemaJSON), now); err != nil {
		return FestView{}, err
	}
	if err := tx.Commit(); err != nil {
		return FestView{}, err
	}

	s.festID = festID
	s.activeGameID = gameID
	if firstMatchCode != "" {
		s.activeMatchCode = firstMatchCode
	}
	return s.loadFestViewLocked(s.festID, s.activeGameID)
}

// importSchemeIntoFest wipes the fest's existing games (and
// dependent rows) and creates a single new game from the supplied scheme.
// The fest row itself stays intact.
func (s *server) importSchemeIntoFest(ctx context.Context, festID int64, scheme festScheme) error {
	if s.db == nil {
		return errors.New("sqlite is not enabled")
	}
	if err := validateScheme(scheme); err != nil {
		return err
	}
	if len(scheme.Teams) > 0 {
		return errors.New("команды загружаются только из rating.chgk.info; уберите teams из JSON-схемы и переимпортируйте участников")
	}
	schemaJSON, err := json.Marshal(scheme)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := clearFestImportData(ctx, tx, festID); err != nil {
		return err
	}

	now := utcNow()
	schemeSlug := scheme.Slug + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	schemeID, err := insertReturningID(ctx, tx, `
insert into schemes(slug, title, version, schema_json, created_at)
values(?, ?, ?, ?, ?)`, schemeSlug, scheme.Title, maxInt(scheme.SchemaVersion, 2), string(schemaJSON), now)
	if err != nil {
		return err
	}
	gameType := scheme.GameType
	if gameType == "" {
		gameType = defaultGameType
	}
	gameTitle := scheme.Title
	if strings.TrimSpace(gameTitle) == "" {
		gameTitle = "Игра"
	}
	gameID, err := insertReturningID(ctx, tx, `
insert into games(fest_id, code, title, game_type, position, scheme_id, scheme_json, state_json, status, team_list_source, roster_source, revision, created_at, updated_at)
values(?, ?, ?, ?, 1, ?, ?, '{}', 'pending', 'fest', 'fest', 1, ?, ?)`,
		festID, defaultGameCode, gameTitle, gameType, schemeID, string(schemaJSON), now, now)
	if err != nil {
		return err
	}

	venueIDs := make(map[int]int64, len(scheme.Venues))
	for _, venue := range scheme.Venues {
		venueID, err := insertReturningID(ctx, tx, `
insert into venues(fest_id, number, title, created_at, updated_at)
values(?, ?, ?, ?, ?)`, festID, venue.Number, venue.Title, now, now)
		if err != nil {
			return err
		}
		venueIDs[venue.Number] = venueID
	}

	assignmentTeams := make(map[[2]int]int64, len(scheme.Teams))
	for _, team := range scheme.Teams {
		teamID, err := insertReturningID(ctx, tx, `
insert into teams(fest_id, name, city)
values(?, ?, ?)`, festID, team.Name, team.City)
		if err != nil {
			return err
		}
		for rosterOrder, fullName := range team.Players {
			fullName = strings.TrimSpace(fullName)
			if fullName == "" {
				continue
			}
			firstName, lastName := splitPlayerName(fullName)
			playerID, err := insertReturningID(ctx, tx, `
insert into players(fest_id, first_name, last_name)
values(?, ?, ?)`, festID, firstName, lastName)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
insert into team_players(team_id, player_id, roster_order)
values(?, ?, ?)`, teamID, playerID, rosterOrder); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `
insert into game_assignments(game_id, basket, number, team_id, player_id)
values(?, ?, ?, ?, null)`, gameID, team.Basket, team.Number, teamID); err != nil {
			return err
		}
		assignmentTeams[[2]int{team.Basket, team.Number}] = teamID
	}

	for stageIndex, stage := range scheme.Stages {
		position := stage.Position
		if position == 0 {
			position = stageIndex + 1
		}
		configJSON := stageConfigJSON(stage)
		stageType := stage.StageType
		if stageType == "" {
			stageType = "matches"
		}
		stageID, err := insertReturningID(ctx, tx, `
insert into stages(fest_id, game_id, code, title, stage_type, position, status, config_json)
values(?, ?, ?, ?, ?, ?, 'pending', ?)`, festID, gameID, stage.Code, stage.Title, stageType, position, configJSON)
		if err != nil {
			return err
		}
		if stageType != "matches" {
			continue
		}
		for matchIndex, match := range stage.Matches {
			participantCount := match.ParticipantCount
			if participantCount == 0 {
				participantCount = len(match.Slots)
			}
			var venueID any
			if id, ok := venueIDs[match.Venue]; ok {
				venueID = id
			}
			matchID, err := insertReturningID(ctx, tx, `
insert into matches(fest_id, game_id, stage_id, code, title, position, participant_count, venue_id, status, revision)
values(?, ?, ?, ?, ?, ?, ?, ?, 'pending', 1)`, festID, gameID, stageID, match.Code, match.Title, matchIndex+1, participantCount, venueID)
			if err != nil {
				return err
			}
			for slotIndex, slot := range match.Slots {
				sourceType, sourceRef := slotSource(slot)
				var resolvedTeamID int64
				if sourceType == "seed" && slot.Seed != nil {
					number := slot.Seed.Number
					if number == 0 {
						number = slot.Seed.Position
					}
					resolvedTeamID = assignmentTeams[[2]int{slot.Seed.Basket, number}]
				}
				if _, err := tx.ExecContext(ctx, `
insert into match_slots(match_id, slot_index, source_type, source_ref_json, team_id, locked)
values(?, ?, ?, ?, ?, 0)`, matchID, slotIndex, sourceType, sourceRef, nullableInt64(resolvedTeamID)); err != nil {
					return err
				}
				if resolvedTeamID > 0 && gameType == "ek" {
					for themeIndex := 0; themeIndex < themeCount; themeIndex++ {
						if err := insertTheme(ctx, tx, matchID, resolvedTeamID, "regular", themeIndex, 0, [5]string{}); err != nil {
							return err
						}
					}
				}
			}
		}
	}

	if _, err := bumpFestRevisionTx(ctx, tx, festID, "import", string(schemaJSON)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

// clearFestImportData drops all per-fest rows that an import would
// recreate (games, stages, matches, venues, teams, players, events). The
// fest row and its organizers stay.
func clearFestImportData(ctx context.Context, tx *sql.Tx, festID int64) error {
	statements := []string{
		`delete from events where fest_id = ?`,
		`delete from games where fest_id = ?`,
		`delete from team_players where team_id in (select id from teams where fest_id = ?)`,
		`delete from teams where fest_id = ?`,
		`delete from players where fest_id = ?`,
		`delete from venues where fest_id = ?`,
	}
	for _, sqlText := range statements {
		if _, err := tx.ExecContext(ctx, sqlText, festID); err != nil {
			return err
		}
	}
	return nil
}

func validateScheme(scheme festScheme) error {
	if strings.TrimSpace(scheme.Slug) == "" {
		return errors.New("schema slug is required")
	}
	if strings.TrimSpace(scheme.Title) == "" {
		return errors.New("schema title is required")
	}
	gameType := scheme.GameType
	if gameType == "" {
		gameType = defaultGameType
	}
	if gameType == "ek" && len(scheme.Stages) == 0 {
		return errors.New("schema stages are required")
	}
	stageCodes := make(map[string]struct{}, len(scheme.Stages))
	matchCodes := make(map[string]struct{})
	for _, stage := range scheme.Stages {
		if strings.TrimSpace(stage.Code) == "" {
			return errors.New("stage code is required")
		}
		if _, exists := stageCodes[stage.Code]; exists {
			return fmt.Errorf("duplicate stage code %q", stage.Code)
		}
		stageCodes[stage.Code] = struct{}{}
		stageType := stage.StageType
		if stageType == "" {
			stageType = "matches"
		}
		if stageType != "matches" && stageType != "reseed" {
			return fmt.Errorf("bad stage_type %q", stage.StageType)
		}
		if stageType == "matches" && len(stage.Matches) == 0 {
			return fmt.Errorf("stage %q has no matches", stage.Code)
		}
		for _, match := range stage.Matches {
			if strings.TrimSpace(match.Code) == "" {
				return fmt.Errorf("match code is required in stage %q", stage.Code)
			}
			if _, exists := matchCodes[match.Code]; exists {
				return fmt.Errorf("duplicate match code %q", match.Code)
			}
			matchCodes[match.Code] = struct{}{}
			if match.ParticipantCount > 0 && len(match.Slots) != match.ParticipantCount {
				return fmt.Errorf("match %q participantCount does not match slots", match.Code)
			}
			for slotIndex, slot := range match.Slots {
				if slot.Team != nil {
					return fmt.Errorf("match %q slot %d uses removed source %q; use seed{basket,number}; teams come from rating.chgk.info import", match.Code, slotIndex, "team")
				}
			}
		}
	}
	assignmentKeys := make(map[[2]int]string, len(scheme.Teams))
	for index, team := range scheme.Teams {
		if strings.TrimSpace(team.Name) == "" {
			return fmt.Errorf("teams[%d].name is required", index)
		}
		if team.Basket <= 0 || team.Number <= 0 {
			return fmt.Errorf("teams[%d] (%q) must have basket>=1 and number>=1", index, team.Name)
		}
		key := [2]int{team.Basket, team.Number}
		if existing, ok := assignmentKeys[key]; ok {
			return fmt.Errorf("teams[%d] (%q) collides with %q on basket %d / number %d", index, team.Name, existing, team.Basket, team.Number)
		}
		assignmentKeys[key] = team.Name
	}
	return nil
}

// clearImportedData wipes fest-scoped data so importScheme can recreate
// the world. Auth tables (users, invites, telegram_login_codes, sessions) and
// schema_versions are intentionally untouched.
func clearImportedData(ctx context.Context, tx *sql.Tx) error {
	tables := []string{
		"events",
		"reseed_entries",
		"match_results",
		"answers",
		"themes",
		"match_slots",
		"matches",
		"stages",
		"game_assignments",
		"game_team_players",
		"game_players",
		"game_teams",
		"games",
		"team_players",
		"players",
		"teams",
		"venues",
		"fest_organizers",
		"fests",
		"schemes",
	}
	for _, table := range tables {
		if _, err := tx.ExecContext(ctx, "delete from "+table); err != nil {
			return err
		}
	}
	return nil
}

func stageConfigJSON(stage schemeStage) string {
	config := map[string]json.RawMessage{}
	if len(stage.Teams) > 0 {
		data, _ := json.Marshal(stage.Teams)
		config["teams"] = data
	}
	if len(stage.Sort) > 0 {
		config["sort"] = stage.Sort
	}
	if len(stage.Config) > 0 {
		config["config"] = stage.Config
	}
	if len(stage.Layout) > 0 {
		config["layout"] = stage.Layout
	}
	return mustJSON(config)
}

func slotSource(slot schemeSlot) (string, string) {
	if slot.Seed != nil {
		number := slot.Seed.Number
		if number == 0 {
			number = slot.Seed.Position
		}
		return "seed", mustJSON(map[string]any{
			"basket": slot.Seed.Basket,
			"number": number,
			"label":  slot.Label,
		})
	}
	if slot.FromMatch != nil {
		return "from_match", mustJSON(map[string]any{
			"match": slot.FromMatch.Match,
			"place": slot.FromMatch.Place,
			"label": slot.Label,
		})
	}
	if slot.Reseed != nil {
		return "reseed", mustJSON(map[string]any{
			"stage": slot.Reseed.Stage,
			"rank":  slot.Reseed.Rank,
			"label": slot.Label,
		})
	}
	if slot.Placeholder != "" {
		return "placeholder", mustJSON(map[string]string{
			"placeholder": slot.Placeholder,
			"label":       slot.Label,
		})
	}
	if slot.Label != "" {
		return "placeholder", mustJSON(map[string]string{"label": slot.Label})
	}
	return "placeholder", "{}"
}

func (s *server) loadFestViewLocked(festID, gameID int64) (FestView, error) {
	if s.db == nil {
		match := buildView(s.state)
		return FestView{
			Slug:              "legacy",
			Title:             match.Title,
			Revision:          match.Revision,
			UpdatedAt:         match.UpdatedAt,
			QuestionValues:    questionValues,
			RegularThemeCount: themeCount,
		}, nil
	}

	ctx := context.Background()
	var view FestView
	view.QuestionValues = questionValues
	view.RegularThemeCount = themeCount
	if festID == 0 {
		view.Slug = ""
		view.Title = ""
		view.UpdatedAt = ""
		return view, nil
	}
	var updatedAt string
	if err := s.db.QueryRowContext(ctx, `
select coalesce(t.slug, ''), t.title, t.revision, t.updated_at, coalesce(g.scheme_json, '')
from fests t
left join games g on g.fest_id = t.id and g.id = ?
where t.id = ?`, gameID, festID).
		Scan(&view.Slug, &view.Title, &view.Revision, &updatedAt, &view.SchemaJSON); err != nil {
		return FestView{}, err
	}
	view.UpdatedAt = updatedAt

	venues, err := loadVenues(ctx, s.db, festID)
	if err != nil {
		return FestView{}, err
	}
	view.Venues = venues

	stageRows, err := s.db.QueryContext(ctx, `
select id, code, title, stage_type, position, status
from stages
where fest_id = ?
order by position, id`, festID)
	if err != nil {
		return FestView{}, err
	}
	defer stageRows.Close()

	type stageRecord struct {
		ID    int64
		Stage StageView
	}
	var stageRecords []stageRecord
	for stageRows.Next() {
		var stageID int64
		var stage StageView
		if err := stageRows.Scan(&stageID, &stage.Code, &stage.Title, &stage.Type, &stage.Position, &stage.Status); err != nil {
			return FestView{}, err
		}
		stageRecords = append(stageRecords, stageRecord{ID: stageID, Stage: stage})
	}
	if err := stageRows.Err(); err != nil {
		return FestView{}, err
	}
	if err := stageRows.Close(); err != nil {
		return FestView{}, err
	}
	for _, record := range stageRecords {
		matches, err := loadFestMatches(ctx, s.db, record.ID)
		if err != nil {
			return FestView{}, err
		}
		record.Stage.Matches = matches
		view.Stages = append(view.Stages, record.Stage)
	}
	return view, nil
}

func loadFestMatches(ctx context.Context, q dbQueryer, stageID int64) ([]FestMatchView, error) {
	rows, err := q.QueryContext(ctx, `
select m.id, m.code, m.title, m.position, m.participant_count, m.status, m.revision,
       v.number, v.title
from matches m
left join venues v on v.id = m.venue_id
where m.stage_id = ?
order by m.position, m.id`, stageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type matchRecord struct {
		ID    int64
		Match FestMatchView
	}
	var records []matchRecord
	for rows.Next() {
		var matchID int64
		var match FestMatchView
		var venueNumber sql.NullInt64
		var venueTitle sql.NullString
		if err := rows.Scan(&matchID, &match.Code, &match.Title, &match.Position, &match.ParticipantCount, &match.Status, &match.Revision, &venueNumber, &venueTitle); err != nil {
			return nil, err
		}
		if venueNumber.Valid {
			match.Venue = &VenueView{Number: int(venueNumber.Int64), Title: venueTitle.String}
		}
		records = append(records, matchRecord{ID: matchID, Match: match})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	var matches []FestMatchView
	for _, record := range records {
		teams, err := loadMatchSummaries(ctx, q, record.ID)
		if err != nil {
			return nil, err
		}
		record.Match.Teams = teams
		matches = append(matches, record.Match)
	}
	return matches, nil
}

func loadMatchSummaries(ctx context.Context, q dbQueryer, matchID int64) ([]MatchTeamSummary, error) {
	rows, err := q.QueryContext(ctx, `
select t.name, ms.source_type, ms.source_ref_json, coalesce(r.place, 0), coalesce(r.total, 0),
       coalesce(r.plus, 0), coalesce(r.tiebreak, 0)
from match_slots ms
left join teams t on t.id = ms.team_id
left join match_results r on r.match_id = ms.match_id and r.team_id = ms.team_id
where ms.match_id = ?
order by ms.slot_index`, matchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var teams []MatchTeamSummary
	for rows.Next() {
		var team MatchTeamSummary
		var name sql.NullString
		var sourceRef string
		if err := rows.Scan(&name, &team.SourceType, &sourceRef, &team.Place, &team.Total, &team.Plus, &team.Tiebreak); err != nil {
			return nil, err
		}
		team.Source = slotSourceLabel(team.SourceType, sourceRef)
		if name.Valid && name.String != "" {
			team.Name = name.String
		} else {
			team.Name = team.Source
		}
		teams = append(teams, team)
	}
	return teams, rows.Err()
}

func (s *server) loadVenuesLocked(festID int64) ([]VenueView, error) {
	return loadVenues(context.Background(), s.db, festID)
}

func loadVenues(ctx context.Context, q dbQueryer, festID int64) ([]VenueView, error) {
	rows, err := q.QueryContext(ctx, `
select number, title from venues
where fest_id = ?
order by number`, festID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var venues []VenueView
	for rows.Next() {
		var venue VenueView
		if err := rows.Scan(&venue.Number, &venue.Title); err != nil {
			return nil, err
		}
		venues = append(venues, venue)
	}
	return venues, rows.Err()
}

func (s *server) loadMatchViewLocked(festID int64, code string) (MatchView, error) {
	if s.db == nil {
		return buildView(s.state), nil
	}
	match, err := loadDBMatchState(context.Background(), s.db, festID, code)
	if err != nil {
		return MatchView{}, err
	}
	return matchViewFromDBState(match), nil
}

func (s *server) loadScopedMatchViewLocked(scope matchScope) (MatchView, error) {
	if s.db == nil {
		return buildView(s.state), nil
	}
	match, err := loadDBMatchStateByScope(context.Background(), s.db, scope)
	if err != nil {
		return MatchView{}, err
	}
	return matchViewFromDBState(match), nil
}

func loadDBMatchState(ctx context.Context, q dbQueryer, festID int64, code string) (dbMatchState, error) {
	return loadDBMatchStateWhere(ctx, q, `m.fest_id = ? and m.code = ?`, festID, code)
}

func loadDBMatchStateByScope(ctx context.Context, q dbQueryer, scope matchScope) (dbMatchState, error) {
	return loadDBMatchStateWhere(ctx, q, `m.id = ? and m.fest_id = ? and m.game_id = ?`, scope.MatchID, scope.FestID, scope.GameID)
}

func loadDBMatchStateWhere(ctx context.Context, q dbQueryer, where string, args ...any) (dbMatchState, error) {
	var match dbMatchState
	var updatedAt string
	var venueNumber sql.NullInt64
	var venueTitle sql.NullString
	if err := q.QueryRowContext(ctx, `
select m.id, m.code, m.title, m.status, m.revision,
       t.revision, t.updated_at, s.code, s.title, v.number, v.title
from matches m
join fests t on t.id = m.fest_id
join stages s on s.id = m.stage_id
left join venues v on v.id = m.venue_id
where `+where, args...).
		Scan(&match.MatchID, &match.Code, &match.Title, &match.Status, &match.Revision,
			&match.FestRevision, &updatedAt, &match.StageCode, &match.StageTitle, &venueNumber, &venueTitle); err != nil {
		return dbMatchState{}, err
	}
	match.UpdatedAt = parseDBTime(updatedAt)
	if venueNumber.Valid {
		match.Venue = &VenueView{Number: int(venueNumber.Int64), Title: venueTitle.String}
	}
	match.State = MatchState{
		Title:     match.Title,
		Finished:  match.Status == "finished",
		Revision:  match.Revision,
		UpdatedAt: match.UpdatedAt,
	}

	slotRows, err := q.QueryContext(ctx, `
select ms.slot_index, ms.team_id, coalesce(t.name, ''), coalesce(r.place, 0), ms.source_ref_json
from match_slots ms
left join teams t on t.id = ms.team_id
left join match_results r on r.match_id = ms.match_id and r.team_id = ms.team_id
where ms.match_id = ?
order by ms.slot_index`, match.MatchID)
	if err != nil {
		return dbMatchState{}, err
	}
	defer slotRows.Close()

	type slotRecord struct {
		Index     int
		TeamID    sql.NullInt64
		Name      string
		Place     float64
		SourceRef string
	}
	var slots []slotRecord
	for slotRows.Next() {
		var slotIndex int
		var teamID sql.NullInt64
		var name string
		var place float64
		var sourceRef string
		if err := slotRows.Scan(&slotIndex, &teamID, &name, &place, &sourceRef); err != nil {
			return dbMatchState{}, err
		}
		slots = append(slots, slotRecord{
			Index:     slotIndex,
			TeamID:    teamID,
			Name:      name,
			Place:     place,
			SourceRef: sourceRef,
		})
	}
	if err := slotRows.Err(); err != nil {
		return dbMatchState{}, err
	}
	if err := slotRows.Close(); err != nil {
		return dbMatchState{}, err
	}
	for _, slot := range slots {
		for len(match.State.Teams) <= slot.Index {
			match.State.Teams = append(match.State.Teams, TeamState{})
			match.TeamIDs = append(match.TeamIDs, 0)
		}
		if !slot.TeamID.Valid {
			match.State.Teams[slot.Index] = TeamState{
				Name:   placeholderName(slot.SourceRef),
				Themes: make([]ThemeEntry, themeCount),
			}
			continue
		}
		team, err := loadTeamState(ctx, q, match.MatchID, slot.TeamID.Int64, slot.Name, slot.Place)
		if err != nil {
			return dbMatchState{}, err
		}
		match.State.Teams[slot.Index] = team
		match.TeamIDs[slot.Index] = slot.TeamID.Int64
	}
	normalizeState(&match.State)
	return match, nil
}

func loadTeamState(ctx context.Context, q dbQueryer, matchID, teamID int64, name string, place float64) (TeamState, error) {
	team := TeamState{
		Name:   name,
		Place:  place,
		Themes: make([]ThemeEntry, themeCount),
	}

	rosterRows, err := q.QueryContext(ctx, `
select p.first_name, p.last_name
from team_players tp
join players p on p.id = tp.player_id
where tp.team_id = ?
order by tp.roster_order`, teamID)
	if err != nil {
		return TeamState{}, err
	}
	for rosterRows.Next() {
		var firstName, lastName string
		if err := rosterRows.Scan(&firstName, &lastName); err != nil {
			_ = rosterRows.Close()
			return TeamState{}, err
		}
		team.Roster = append(team.Roster, joinPlayerName(firstName, lastName))
	}
	if err := rosterRows.Close(); err != nil {
		return TeamState{}, err
	}

	themeRows, err := q.QueryContext(ctx, `
select th.id, th.kind, th.theme_index, coalesce(p.first_name, ''), coalesce(p.last_name, '')
from themes th
left join players p on p.id = th.player_id
where th.match_id = ? and th.team_id = ?
order by case th.kind when 'regular' then 0 else 1 end, th.theme_index`, matchID, teamID)
	if err != nil {
		return TeamState{}, err
	}
	defer themeRows.Close()

	type themeRecord struct {
		ID        int64
		Kind      string
		Index     int
		FirstName string
		LastName  string
	}
	var themeRecords []themeRecord
	for themeRows.Next() {
		var themeID int64
		var kind string
		var themeIndex int
		var firstName, lastName string
		if err := themeRows.Scan(&themeID, &kind, &themeIndex, &firstName, &lastName); err != nil {
			return TeamState{}, err
		}
		themeRecords = append(themeRecords, themeRecord{
			ID:        themeID,
			Kind:      kind,
			Index:     themeIndex,
			FirstName: firstName,
			LastName:  lastName,
		})
	}
	if err := themeRows.Err(); err != nil {
		return TeamState{}, err
	}
	if err := themeRows.Close(); err != nil {
		return TeamState{}, err
	}

	shootout := make(map[int]ThemeEntry)
	maxShootout := -1
	for _, record := range themeRecords {
		entry := ThemeEntry{
			Player:  joinPlayerName(record.FirstName, record.LastName),
			Answers: [5]string{},
		}
		answers, err := loadThemeAnswers(ctx, q, record.ID)
		if err != nil {
			return TeamState{}, err
		}
		entry.Answers = answers
		switch record.Kind {
		case "regular":
			if record.Index >= 0 && record.Index < len(team.Themes) {
				team.Themes[record.Index] = entry
			}
		case "shootout":
			if record.Index >= 0 {
				shootout[record.Index] = entry
				if record.Index > maxShootout {
					maxShootout = record.Index
				}
			}
		}
	}
	if maxShootout >= 0 {
		team.ShootoutThemes = make([]ThemeEntry, maxShootout+1)
		for index, entry := range shootout {
			team.ShootoutThemes[index] = entry
		}
	}
	return team, nil
}

func loadThemeAnswers(ctx context.Context, q dbQueryer, themeID int64) ([5]string, error) {
	var answers [5]string
	rows, err := q.QueryContext(ctx, `
select answer_index, mark from answers
where theme_id = ?
order by answer_index`, themeID)
	if err != nil {
		return answers, err
	}
	defer rows.Close()
	for rows.Next() {
		var index int
		var mark string
		if err := rows.Scan(&index, &mark); err != nil {
			return answers, err
		}
		if index >= 0 && index < len(answers) {
			answers[index] = normalizeMark(mark)
		}
	}
	return answers, rows.Err()
}

func matchViewFromDBState(match dbMatchState) MatchView {
	view := buildView(match.State)
	view.Code = match.Code
	view.StageCode = match.StageCode
	view.StageTitle = match.StageTitle
	view.Venue = match.Venue
	return view
}

func (s *server) applyMatchUpdate(festID int64, code string, req updateRequest) (MatchView, []byte, error) {
	if s.db == nil {
		return s.applyLegacyUpdate(req)
	}
	return s.applyMatchUpdateUsing(festID, req,
		func(ctx context.Context, q dbQueryer) (dbMatchState, error) {
			return loadDBMatchState(ctx, q, festID, code)
		},
		func() (MatchView, error) {
			return s.loadMatchViewLocked(festID, code)
		})
}

func (s *server) applyScopedMatchUpdate(scope matchScope, req updateRequest) (MatchView, []byte, error) {
	if s.db == nil {
		return s.applyLegacyUpdate(req)
	}
	return s.applyMatchUpdateUsing(scope.FestID, req,
		func(ctx context.Context, q dbQueryer) (dbMatchState, error) {
			return loadDBMatchStateByScope(ctx, q, scope)
		},
		func() (MatchView, error) {
			return s.loadScopedMatchViewLocked(scope)
		})
}

func (s *server) applyMatchUpdateUsing(
	festID int64,
	req updateRequest,
	loadMatch func(context.Context, dbQueryer) (dbMatchState, error),
	loadView func() (MatchView, error),
) (MatchView, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MatchView{}, nil, err
	}
	defer tx.Rollback()

	match, err := loadMatch(ctx, tx)
	if err != nil {
		return MatchView{}, nil, err
	}

	if req.Finished != nil {
		if hasMatchEdit(req) {
			return MatchView{}, nil, errors.New("finished update must be standalone")
		}
		status := "active"
		if *req.Finished {
			status = "finished"
		}
		if _, err := tx.ExecContext(ctx, `update matches set status = ? where id = ?`, status, match.MatchID); err != nil {
			return MatchView{}, nil, err
		}
	} else {
		if match.State.Finished {
			return MatchView{}, nil, errors.New("match is finished")
		}
		if err := applyMatchEditTx(ctx, tx, match, req); err != nil {
			return MatchView{}, nil, err
		}
	}

	if err := recalculateMatchResultsForStateTx(ctx, tx, match); err != nil {
		return MatchView{}, nil, err
	}
	revision, err := bumpMatchRevisionTx(ctx, tx, festID, match.MatchID, "match:update", mustJSON(req))
	if err != nil {
		return MatchView{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return MatchView{}, nil, err
	}

	view, err := loadView()
	if err != nil {
		return MatchView{}, nil, err
	}
	if revision > 0 {
		view.Revision = maxInt64(view.Revision, revision)
	}
	data, err := json.Marshal(view)
	return view, data, err
}

func applyMatchEditTx(ctx context.Context, tx *sql.Tx, match dbMatchState, req updateRequest) error {
	if req.Action != "" {
		if hasTeamEdit(req) {
			return errors.New("action update must be standalone")
		}
		switch req.Action {
		case actionAddShootoutTheme:
			return addShootoutThemeTx(ctx, tx, match.MatchID, match.TeamIDs)
		case actionRemoveShootoutTheme:
			return removeShootoutThemeTx(ctx, tx, match.MatchID)
		default:
			return errors.New("bad action")
		}
	}

	if req.Team < 0 || req.Team >= len(match.TeamIDs) || match.TeamIDs[req.Team] == 0 {
		return errors.New("bad team index")
	}
	teamID := match.TeamIDs[req.Team]

	if req.Tiebreak != nil {
		return errors.New("shootout total is calculated")
	}
	if req.Place != nil {
		if *req.Place < 0 {
			return errors.New("bad place")
		}
		if _, err := tx.ExecContext(ctx, `
insert into match_results(match_id, team_id, place)
values(?, ?, ?)
on conflict(match_id, team_id) do update set place = excluded.place`, match.MatchID, teamID, *req.Place); err != nil {
			return err
		}
	}

	if req.Theme != nil || req.Player != nil || req.Answer != nil || req.Mark != nil || req.Shootout != nil {
		isShootout := req.Shootout != nil && *req.Shootout
		kind := "regular"
		if isShootout {
			kind = "shootout"
		}
		if req.Theme == nil || *req.Theme < 0 {
			return errors.New("bad theme index")
		}
		themeID, err := lookupThemeID(ctx, tx, match.MatchID, teamID, kind, *req.Theme)
		if err != nil {
			return err
		}

		if req.Player != nil {
			player := strings.TrimSpace(*req.Player)
			var playerID any
			if player != "" {
				id, err := lookupRosterPlayerID(ctx, tx, teamID, player)
				if err != nil {
					return err
				}
				playerID = id
			}
			if _, err := tx.ExecContext(ctx, `update themes set player_id = ? where id = ?`, playerID, themeID); err != nil {
				return err
			}
		}

		if req.Answer != nil || req.Mark != nil {
			if req.Answer == nil || *req.Answer < 0 || *req.Answer >= len(questionValues) {
				return errors.New("bad answer index")
			}
			if req.Mark == nil {
				return errors.New("missing mark")
			}
			if _, err := tx.ExecContext(ctx, `
insert into answers(theme_id, answer_index, mark)
values(?, ?, ?)
on conflict(theme_id, answer_index) do update set mark = excluded.mark`, themeID, *req.Answer, normalizeMark(*req.Mark)); err != nil {
				return err
			}
		}
	}

	return nil
}

func addShootoutThemeTx(ctx context.Context, tx *sql.Tx, matchID int64, teamIDs []int64) error {
	var next sql.NullInt64
	if err := tx.QueryRowContext(ctx, `
select max(theme_index) + 1 from themes
where match_id = ? and kind = 'shootout'`, matchID).Scan(&next); err != nil {
		return err
	}
	themeIndex := 0
	if next.Valid {
		themeIndex = int(next.Int64)
	}
	for _, teamID := range teamIDs {
		if teamID == 0 {
			continue
		}
		if err := insertTheme(ctx, tx, matchID, teamID, "shootout", themeIndex, 0, [5]string{}); err != nil {
			return err
		}
	}
	return nil
}

func removeShootoutThemeTx(ctx context.Context, tx *sql.Tx, matchID int64) error {
	var themeIndex sql.NullInt64
	if err := tx.QueryRowContext(ctx, `
select max(theme_index) from themes
where match_id = ? and kind = 'shootout'`, matchID).Scan(&themeIndex); err != nil {
		return err
	}
	if !themeIndex.Valid {
		return errors.New("no shootout themes to remove")
	}
	if _, err := tx.ExecContext(ctx, `
delete from answers
where theme_id in (
  select id from themes where match_id = ? and kind = 'shootout' and theme_index = ?
)`, matchID, themeIndex.Int64); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `
delete from themes
where match_id = ? and kind = 'shootout' and theme_index = ?`, matchID, themeIndex.Int64)
	return err
}

func lookupThemeID(ctx context.Context, q dbQueryer, matchID, teamID int64, kind string, themeIndex int) (int64, error) {
	var id int64
	err := q.QueryRowContext(ctx, `
select id from themes
where match_id = ? and team_id = ? and kind = ? and theme_index = ?`, matchID, teamID, kind, themeIndex).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, errors.New("bad theme index")
	}
	return id, err
}

func lookupRosterPlayerID(ctx context.Context, q dbQueryer, teamID int64, player string) (int64, error) {
	rows, err := q.QueryContext(ctx, `
select p.id, p.first_name, p.last_name
from team_players tp
join players p on p.id = tp.player_id
where tp.team_id = ?`, teamID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var firstName, lastName string
		if err := rows.Scan(&id, &firstName, &lastName); err != nil {
			return 0, err
		}
		if joinPlayerName(firstName, lastName) == player {
			return id, nil
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	return 0, errors.New("player is not in roster")
}

func recalculateMatchResultsTx(ctx context.Context, tx *sql.Tx, festID int64, code string) error {
	match, err := loadDBMatchState(ctx, tx, festID, code)
	if err != nil {
		return err
	}
	return recalculateMatchResultsForStateTx(ctx, tx, match)
}

func recalculateMatchResultsForStateTx(ctx context.Context, tx *sql.Tx, match dbMatchState) error {
	view := buildView(match.State)
	for index, team := range view.Teams {
		if index >= len(match.TeamIDs) || match.TeamIDs[index] == 0 {
			continue
		}
		metrics := matchMetricsJSON(team)
		if _, err := tx.ExecContext(ctx, `
insert into match_results(match_id, team_id, place, total, plus, tiebreak, metrics_json)
values(?, ?, ?, ?, ?, ?, ?)
on conflict(match_id, team_id) do update set
  place = excluded.place,
  total = excluded.total,
  plus = excluded.plus,
  tiebreak = excluded.tiebreak,
  metrics_json = excluded.metrics_json`, match.MatchID, match.TeamIDs[index], team.Place, team.Total, team.Plus, team.Tiebreak, metrics); err != nil {
			return err
		}
	}
	return nil
}

func bumpMatchRevisionTx(ctx context.Context, tx *sql.Tx, festID, matchID int64, eventType, payload string) (int64, error) {
	now := utcNow()
	if _, err := tx.ExecContext(ctx, `update matches set revision = revision + 1 where id = ?`, matchID); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `update fests set revision = revision + 1, updated_at = ? where id = ?`, now, festID); err != nil {
		return 0, err
	}
	var revision int64
	if err := tx.QueryRowContext(ctx, `select revision from fests where id = ?`, festID).Scan(&revision); err != nil {
		return 0, err
	}
	_, err := tx.ExecContext(ctx, `
insert into events(fest_id, revision, type, payload_json, created_at)
values(?, ?, ?, ?, ?)`, festID, revision, eventType, payload, now)
	return revision, err
}

func (s *server) updateMatchVenue(festID int64, code string, number int) (MatchView, []byte, error) {
	return s.updateMatchVenueUsing(festID, number,
		func(ctx context.Context, q dbQueryer) (dbMatchState, error) {
			return loadDBMatchState(ctx, q, festID, code)
		},
		func() (MatchView, error) {
			return s.loadMatchViewLocked(festID, code)
		})
}

func (s *server) updateScopedMatchVenue(scope matchScope, number int) (MatchView, []byte, error) {
	return s.updateMatchVenueUsing(scope.FestID, number,
		func(ctx context.Context, q dbQueryer) (dbMatchState, error) {
			return loadDBMatchStateByScope(ctx, q, scope)
		},
		func() (MatchView, error) {
			return s.loadScopedMatchViewLocked(scope)
		})
}

func (s *server) updateMatchVenueUsing(
	festID int64,
	number int,
	loadMatch func(context.Context, dbQueryer) (dbMatchState, error),
	loadView func() (MatchView, error),
) (MatchView, []byte, error) {
	if number <= 0 {
		return MatchView{}, nil, errors.New("bad venue number")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MatchView{}, nil, err
	}
	defer tx.Rollback()

	match, err := loadMatch(ctx, tx)
	if err != nil {
		return MatchView{}, nil, err
	}
	var venueID int64
	if err := tx.QueryRowContext(ctx, `
select id from venues where fest_id = ? and number = ?`, festID, number).Scan(&venueID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return MatchView{}, nil, errors.New("unknown venue")
		}
		return MatchView{}, nil, err
	}
	if _, err := tx.ExecContext(ctx, `update matches set venue_id = ? where id = ?`, venueID, match.MatchID); err != nil {
		return MatchView{}, nil, err
	}
	revision, err := bumpMatchRevisionTx(ctx, tx, festID, match.MatchID, "match:venue", mustJSON(map[string]any{"code": match.Code, "venue": number}))
	if err != nil {
		return MatchView{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return MatchView{}, nil, err
	}
	view, err := loadView()
	if err != nil {
		return MatchView{}, nil, err
	}
	view.Revision = maxInt64(view.Revision, revision)
	data, err := json.Marshal(view)
	return view, data, err
}

func (s *server) updateVenue(festID int64, number int, title string) ([]VenueView, int64, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, 0, errors.New("empty venue title")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
update venues set title = ?, updated_at = ?
where fest_id = ? and number = ?`, title, utcNow(), festID, number)
	if err != nil {
		return nil, 0, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, 0, err
	}
	if affected == 0 {
		return nil, 0, errors.New("unknown venue")
	}
	revision, err := bumpFestRevisionTx(ctx, tx, festID, "venues:update", mustJSON(map[string]any{"number": number, "title": title}))
	if err != nil {
		return nil, 0, err
	}
	venues, err := loadVenues(ctx, tx, festID)
	if err != nil {
		return nil, 0, err
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	return venues, revision, nil
}

func (s *server) bumpFestRevisionStandalone(ctx context.Context, festID int64, eventType, payload string) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	revision, err := bumpFestRevisionTx(ctx, tx, festID, eventType, payload)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return revision, nil
}

func bumpFestRevisionTx(ctx context.Context, tx *sql.Tx, festID int64, eventType, payload string) (int64, error) {
	now := utcNow()
	if _, err := tx.ExecContext(ctx, `update fests set revision = revision + 1, updated_at = ? where id = ?`, now, festID); err != nil {
		return 0, err
	}
	var revision int64
	if err := tx.QueryRowContext(ctx, `select revision from fests where id = ?`, festID).Scan(&revision); err != nil {
		return 0, err
	}
	_, err := tx.ExecContext(ctx, `
insert into events(fest_id, revision, type, payload_json, created_at)
values(?, ?, ?, ?, ?)`, festID, revision, eventType, payload, now)
	return revision, err
}

func (s *server) broadcastState(festID int64, scope string, revision int64, payload []byte) {
	if s.db != nil {
		payload = eventEnvelopeJSON(scope, revision, payload)
	}
	s.broadcast(event{festID: festID, revision: revision, data: payload})
}

func eventEnvelopeJSON(scope string, revision int64, payload []byte) []byte {
	data, err := json.Marshal(eventEnvelope{
		Scope:    scope,
		Revision: revision,
		Data:     json.RawMessage(payload),
	})
	if err != nil {
		return payload
	}
	return data
}

func writeJSONValue(w http.ResponseWriter, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, data)
}

func matchMetricsJSON(team TeamView) string {
	metrics := map[string]any{
		"correctCounts": team.CorrectCounts,
		"wrongCounts":   team.WrongCounts,
	}
	for index, value := range questionValues {
		metrics[fmt.Sprintf("correct_%d", value)] = team.CorrectCounts[index]
		metrics[fmt.Sprintf("wrong_%d", value)] = team.WrongCounts[index]
	}
	return mustJSON(metrics)
}

func mustJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func splitPlayerName(fullName string) (string, string) {
	fullName = strings.TrimSpace(fullName)
	if fullName == "" {
		return "", ""
	}
	parts := strings.Fields(fullName)
	if len(parts) == 1 {
		return parts[0], ""
	}
	return parts[0], strings.Join(parts[1:], " ")
}

func joinPlayerName(firstName, lastName string) string {
	return strings.TrimSpace(strings.TrimSpace(firstName) + " " + strings.TrimSpace(lastName))
}

func placeholderName(sourceRef string) string {
	var ref map[string]any
	if err := json.Unmarshal([]byte(sourceRef), &ref); err != nil {
		return "Ожидает команды"
	}
	if value, ok := ref["placeholder"].(string); ok && value != "" {
		return value
	}
	if value, ok := ref["name"].(string); ok && value != "" {
		return value
	}
	return "Ожидает команды"
}

func slotSourceLabel(sourceType, sourceRef string) string {
	var ref map[string]any
	_ = json.Unmarshal([]byte(sourceRef), &ref)
	if label, ok := ref["label"].(string); ok && label != "" {
		return label
	}
	switch sourceType {
	case "seed":
		number := intFromMap(ref, "number")
		if number == 0 {
			number = intFromMap(ref, "position")
		}
		return fmt.Sprintf("К%d-%d", intFromMap(ref, "basket"), number)
	case "from_match":
		return fmt.Sprintf("%s%d", stringFromMap(ref, "match"), intFromMap(ref, "place"))
	case "reseed":
		return fmt.Sprintf("Пересев-%d", intFromMap(ref, "rank"))
	case "placeholder":
		if placeholder := stringFromMap(ref, "placeholder"); placeholder != "" {
			return placeholder
		}
	}
	return "Ожидает команды"
}

func stringFromMap(values map[string]any, key string) string {
	if value, ok := values[key].(string); ok {
		return value
	}
	return ""
}

func intFromMap(values map[string]any, key string) int {
	switch value := values[key].(type) {
	case float64:
		return int(value)
	case int:
		return value
	case json.Number:
		number, _ := value.Int64()
		return int(number)
	default:
		return 0
	}
}

func parseDBTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Now()
	}
	return parsed
}

func utcNow() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func nullableInt64(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}

// Auth helpers. Codes must be unique enough not to collide between concurrent
// users; we get that from crypto/rand.

const (
	inviteCodeBytes      = 12
	telegramAuthBytes    = 12
	telegramLoginCodeLen = 5
	sessionTokenBytes    = 32
	telegramAuthLifetime = time.Minute
	inviteLifetime       = 7 * 24 * time.Hour
	sessionLifetime      = 30 * 24 * time.Hour
)

const telegramLoginCodeAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func randomBase32(bytesLen int) (string, error) {
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return strings.ToUpper(strings.TrimRight(base32.StdEncoding.EncodeToString(buf), "=")), nil
}

func newInviteCode() (string, error) {
	return randomBase32(inviteCodeBytes)
}

func newTelegramAuthCode() (string, error) {
	return randomBase32(telegramAuthBytes)
}

func newTelegramLoginCode() (string, error) {
	buf := make([]byte, telegramLoginCodeLen)
	max := big.NewInt(int64(len(telegramLoginCodeAlphabet)))
	for i := range buf {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		buf[i] = telegramLoginCodeAlphabet[n.Int64()]
	}
	return string(buf), nil
}

func newSessionToken() (string, error) {
	buf := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func hashSessionToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
