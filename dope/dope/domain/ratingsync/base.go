// Package ratingsync keeps what a fest and rating.chgk.info last agreed on,
// fact by fact for each team, so the export can tell which side changed
// since (ADR-0036).
package ratingsync

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strings"

	"dope/dope/domain/roster"
	"dope/dope/storage/store"
)

// Fact is what of a team the export compares with the site.
type Fact string

const (
	Roster  Fact = "roster"
	Flags   Fact = "flags"
	Answers Fact = "answers"
)

// Key is a fact of a team, the team by its site id.
type Key struct {
	Team int64
	Fact Fact
}

// Base is the last version of a fact both sides held: written when an
// import reads it from the site, and when the export sends it there.
type Base struct {
	Key
	Value string
}

// RosterBase is a team's roster as the base keeps it: its players' site ids,
// without order or repeats.
func RosterBase(team int64, players []int64) Base {
	ids := append([]int64{}, players...)
	slices.Sort(ids)
	return Base{Key{team, Roster}, mustJSON(slices.Compact(ids))}
}

// FlagsBase is a team's зачёты as the base keeps them: their short names,
// without order or repeats.
func FlagsBase(team int64, shorts []string) Base {
	names := make([]string, 0, len(shorts))
	for _, s := range shorts {
		if s = strings.TrimSpace(s); s != "" {
			names = append(names, s)
		}
	}
	slices.Sort(names)
	return Base{Key{team, Flags}, mustJSON(slices.Compact(names))}
}

// AnswersBase is a team's answers, a character per question.
func AnswersBase(team int64, mask string) Base {
	return Base{Key{team, Answers}, mask}
}

func mustJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

// FromImport is the base an import of these site teams sets: each team's
// roster and зачёты as the site gave them.
func FromImport(teams []roster.FestRosterImportTeam) []Base {
	var out []Base
	for _, t := range teams {
		if t.RatingID <= 0 {
			continue
		}
		var ids []int64
		for _, p := range t.Players {
			if p.RatingID > 0 {
				ids = append(ids, p.RatingID)
			}
		}
		out = append(out, RosterBase(t.RatingID, ids), FlagsBase(t.RatingID, roster.FlagShortNames(t.Flags)))
	}
	return out
}

// Load is the fest's base.
func Load(ctx context.Context, q store.Queryer, festID int64) (map[Key]string, error) {
	rows, err := store.CollectRows(ctx, q, `select rating_team, fact, value from rating_sync_base where fest_id = ?`, []any{festID},
		func(rows *sql.Rows) (Base, error) {
			var b Base
			return b, rows.Scan(&b.Team, &b.Fact, &b.Value)
		})
	out := make(map[Key]string, len(rows))
	for _, b := range rows {
		out[b.Key] = b.Value
	}
	return out, err
}

// Stale reports whether any of these differs from the fest's base.
func Stale(ctx context.Context, q store.Queryer, festID int64, bases []Base) (bool, error) {
	have, err := Load(ctx, q, festID)
	if err != nil {
		return false, err
	}
	for _, b := range bases {
		if value, ok := have[b.Key]; !ok || value != b.Value {
			return true, nil
		}
	}
	return false, nil
}

// SaveTx sets these facts of the fest's base.
func SaveTx(ctx context.Context, tx *sql.Tx, festID int64, bases []Base) error {
	for _, b := range bases {
		if _, err := tx.ExecContext(ctx, `
insert into rating_sync_base(fest_id, rating_team, fact, value) values(?, ?, ?, ?)
on conflict(fest_id, rating_team, fact) do update set value = excluded.value`, festID, b.Team, b.Fact, b.Value); err != nil {
			return err
		}
	}
	return nil
}
