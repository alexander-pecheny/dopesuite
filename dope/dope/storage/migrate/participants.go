package migrate

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"dope/dope/storage/journal"
)

// Participant rename (ADR-0007): the game-side registry stops calling itself
// `teams`, because личная СИ seats players in the same rows and a column whose
// name lies about its contents is the expensive kind of mistake. Runs once, as
// schema version 20 (server/db.go).
//
// Every step is DDL over small tables (the registry is hundreds of rows, not
// millions), so this never materialises a result set — the one backfill that
// reads data streams a cursor per fest.

// RunParticipantRename renames the registry and everything keyed on it. The
// guard is the schema_versions row, written only once every step has succeeded,
// so a crash between the DDL and the backfills resumes instead of stranding a
// half-migrated database behind an "already renamed" check.
func RunParticipantRename(db *sql.DB) error {
	if _, err := db.Exec(`create table if not exists schema_versions(
  version integer primary key,
  applied_at text not null
)`); err != nil {
		return err
	}
	var done int
	if err := db.QueryRow(`select count(*) from schema_versions where version = 20`).Scan(&done); err != nil {
		return err
	}
	if done > 0 {
		return nil
	}
	// A fresh database has none of these tables yet — server/db.go creates them
	// in the new shape right after, so there is nothing to migrate and the
	// version row simply records that.
	if legacy, err := tableExists(db, "teams"); err != nil {
		return err
	} else if legacy {
		if err := renameRegistry(db); err != nil {
			return err
		}
	}
	if matches, err := tableExists(db, "matches"); err != nil {
		return err
	} else if matches {
		if err := renameBlobSections(db); err != nil {
			return err
		}
	}
	if registry, err := tableExists(db, "participants"); err != nil {
		return err
	} else if registry {
		if err := backfillRosterLinks(db); err != nil {
			return err
		}
	}
	_, err := db.Exec(`insert or ignore into schema_versions(version, applied_at)
values(20, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`)
	return err
}

// renameRegistry is the DDL half: one transaction, all or nothing.
func renameRegistry(db *sql.DB) error {

	// Row-op triggers name their table and columns; drop them so the rename's
	// own DDL churn is never journaled, and let EnsureTriggers reinstall against
	// the new shape (its fingerprint covers column names, so it will).
	if err := journal.DropTriggers(db); err != nil {
		return err
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

	// legacy_alter_table stays OFF: we *want* RENAME to rewrite the foreign-key
	// references other tables hold, so they point at `participants` afterwards.
	for _, stmt := range []string{
		`alter table teams rename to participants`,
		`alter table participants add column roster text not null default 'team'`,
		`alter table participants add column fest_team_id integer references fest_teams(id)`,
		`alter table participants add column fest_player_id integer references fest_players(id)`,
		`drop index if exists teams_fest_number_idx`,
		`create unique index if not exists participants_fest_number_idx
		   on participants(fest_id, number) where number is not null`,

		`alter table match_results rename column team_id to participant_id`,
		`alter table game_team_players rename column team_id to participant_id`,

		`alter table game_teams rename to game_participants`,
		`alter table game_participants rename column team_id to participant_id`,
		`alter table team_players rename to participant_players`,
		`alter table participant_players rename column team_id to participant_id`,
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("participant rename %q: %w", stmt, err)
		}
	}

	// match_slots and game_assignments carry a `player_id` that was declared for
	// individual games and never written. A Participant now covers that case, so
	// the column goes — but SQLite refuses DROP COLUMN on a column bound by a
	// foreign key or a CHECK, so both tables are rebuilt instead. They are the
	// two smallest tables in the schema.
	if err := rebuildTable(ctx, tx,
		"match_slots", `
create table match_slots_participants_migration(
  id integer primary key,
  match_id integer not null references matches(id) on delete cascade,
  slot_index integer not null,
  source_type text not null check (source_type in ('seed','from_match','reseed','placeholder')),
  source_ref_json text not null default '{}',
  participant_id integer references participants(id),
  locked integer not null default 0,
  unique(match_id, slot_index)
)`, `
insert into match_slots_participants_migration(id, match_id, slot_index, source_type, source_ref_json, participant_id, locked)
select id, match_id, slot_index, source_type, source_ref_json, team_id, locked from match_slots`); err != nil {
		return err
	}
	if err := rebuildTable(ctx, tx,
		"game_assignments", `
create table game_assignments_participants_migration(
  game_id integer not null references games(id) on delete cascade,
  basket integer not null,
  number integer not null,
  participant_id integer references participants(id),
  primary key(game_id, basket, number)
)`, `
insert into game_assignments_participants_migration(game_id, basket, number, participant_id)
select game_id, basket, number, team_id from game_assignments`); err != nil {
		return err
	}

	// game_players listed an individual game's entrants. game_participants is
	// that list now, for both rosters.
	if _, err := tx.ExecContext(ctx, `drop table if exists game_players`); err != nil {
		return err
	}

	// RENAME TO rewrites a trigger's body but keeps its name, so the roster-limit
	// trigger would survive under the old name and be created a second time under
	// the new one. Drop it; server/db.go recreates it as participant_players_max_9.
	if _, err := tx.ExecContext(ctx, `drop trigger if exists team_players_max_9`); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		return err
	}
	fkOff = false
	return verifyForeignKeys(db)
}

// renameBlobSections moves the EK match blob's `teams` container to
// `participants`, matching store.MatchBlob and the journal's patch paths. Only
// EK's blob is keyed this way; a flat game's document owns its own `teams` and
// is left alone.
//
// Paged by id, so the whole matches table is never in memory at once.
func renameBlobSections(db *sql.DB) error {
	const page = 200
	var after int64
	for {
		rows, err := db.Query(`
select m.id, m.state_json from matches m
left join games g on g.id = m.game_id
where m.id > ? and coalesce(g.game_type, '') in ('', 'ek')
  and m.state_json like '%"teams"%'
order by m.id limit ?`, after, page)
		if err != nil {
			return err
		}
		type blobRow struct {
			id  int64
			raw string
		}
		var batch []blobRow
		for rows.Next() {
			var row blobRow
			if err := rows.Scan(&row.id, &row.raw); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, row)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(batch) == 0 {
			return nil
		}
		for _, row := range batch {
			after = row.id
			moved, ok, err := moveBlobTeamsKey(row.raw)
			if err != nil {
				return fmt.Errorf("match %d blob: %w", row.id, err)
			}
			if !ok {
				continue
			}
			if _, err := db.Exec(`update matches set state_json = ? where id = ?`, moved, row.id); err != nil {
				return err
			}
		}
	}
}

// moveBlobTeamsKey renames the document's top-level `teams` key, reporting
// whether anything changed. A blob that already speaks the new name, or whose
// `teams` is not an object, is left exactly as it was.
func moveBlobTeamsKey(raw string) (string, bool, error) {
	if raw == "" || raw == "{}" {
		return raw, false, nil
	}
	var doc map[string]any
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return "", false, err
	}
	section, ok := doc["teams"].(map[string]any)
	if !ok {
		return raw, false, nil
	}
	if _, taken := doc["participants"]; taken {
		return raw, false, nil
	}
	delete(doc, "teams")
	doc["participants"] = section
	encoded, err := json.Marshal(doc)
	if err != nil {
		return "", false, err
	}
	return string(encoded), true, nil
}

// rebuildTable swaps a table for a new shape: create under a scratch name, copy,
// drop the original, rename in. Callers hold the transaction.
func rebuildTable(ctx context.Context, tx *sql.Tx, name, create, copy string) error {
	scratch := name + "_participants_migration"
	for _, stmt := range []string{create, copy,
		`drop table ` + name,
		`alter table ` + scratch + ` rename to ` + name,
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("rebuild %s: %w", name, err)
		}
	}
	return nil
}

// backfillRosterLinks points each migrated Participant at the fest_teams row it
// was drawn from — by number where the fest numbers its teams, else by name
// where that name is unique in the fest. Ambiguous rows keep a null link: the
// code paths that need the roster still resolve it the way they always have,
// and a wrong link would be worse than none.
//
// One statement per fest, driven by a cursor over fest ids, so no query ever
// spans the whole table.
func backfillRosterLinks(db *sql.DB) error {
	rows, err := db.Query(`select distinct fest_id from participants where fest_team_id is null`)
	if err != nil {
		return err
	}
	var fests []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		fests = append(fests, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, fest := range fests {
		if _, err := db.Exec(`
update participants set fest_team_id = (
  select ft.id from fest_teams ft
  where ft.fest_id = participants.fest_id and ft.deleted = 0
    and ft.number is not null and ft.number = participants.number)
where fest_id = ? and fest_team_id is null and number is not null`, fest); err != nil {
			return err
		}
		// An aggregate select is one group, so HAVING decides whether the name is
		// unique in the fest; min(ft.id) is then the only id it could be.
		if _, err := db.Exec(`
update participants set fest_team_id = (
  select min(ft.id) from fest_teams ft
  where ft.fest_id = participants.fest_id and ft.deleted = 0
    and lower(ft.name) = lower(participants.name)
  having count(*) = 1)
where fest_id = ? and fest_team_id is null`, fest); err != nil {
			return err
		}
	}
	return nil
}

// verifyForeignKeys fails loudly if the rebuilds left a dangling reference —
// the one way this migration could quietly corrupt a bracket.
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
		return fmt.Errorf("participant rename left a dangling reference: table=%s rowid=%v parent=%v fkid=%v", table, rowID, parent, fkID)
	}
	return rows.Err()
}

func tableExists(db *sql.DB, name string) (bool, error) {
	var count int
	err := db.QueryRow(`select count(*) from sqlite_master where type = 'table' and name = ?`, name).Scan(&count)
	return count > 0, err
}
