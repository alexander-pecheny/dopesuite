package store

import (
	"context"
	"database/sql"
)

// GameDoc is a Game's document as every reader wants it: the flat document on
// the 'main' match when the Game has one (ChGK, KSI), else the game-level blob
// (EK's seed ladder, a DSL flat game before it has a match), never empty.
// MatchID says which: valid means the document lives on that match.
type GameDoc struct {
	GameID     int64
	GameType   string
	Slug       sql.NullString
	FestSlug   sql.NullString
	SchemeJSON string
	State      string
	Screen     string
	MatchID    sql.NullInt64
}

const gameDocSelect = `
select g.id, g.game_type, g.slug, f.slug, coalesce(g.scheme_json, ''), m.id,
       coalesce(m.state_json, coalesce(g.state_json, '')), coalesce(g.screen_settings_json, '')
from games g join fests f on f.id = g.fest_id
left join matches m on m.game_id = g.id and m.code = 'main'
where g.fest_id = ? and `

// LoadGameDoc reads one Game's document by id; sql.ErrNoRows when absent.
func LoadGameDoc(ctx context.Context, q Queryer, festID, gameID int64) (GameDoc, error) {
	return scanGameDoc(q.QueryRowContext(ctx, gameDocSelect+`g.id = ?`, festID, gameID))
}

// LoadGameDocByCode is LoadGameDoc for a Game named by its code.
func LoadGameDocByCode(ctx context.Context, q Queryer, festID int64, code string) (GameDoc, error) {
	return scanGameDoc(q.QueryRowContext(ctx, gameDocSelect+`g.code = ?`, festID, code))
}

// LoadGameDocs reads every Game of a fest in position order.
func LoadGameDocs(ctx context.Context, q Queryer, festID int64) ([]GameDoc, error) {
	return CollectRows(ctx, q, gameDocSelect+`1 order by g.position, g.id`, []any{festID}, func(rows *sql.Rows) (GameDoc, error) {
		return scanGameDoc(rows)
	})
}

type scanner interface{ Scan(dest ...any) error }

func scanGameDoc(row scanner) (GameDoc, error) {
	var d GameDoc
	if err := row.Scan(&d.GameID, &d.GameType, &d.Slug, &d.FestSlug, &d.SchemeJSON, &d.MatchID, &d.State, &d.Screen); err != nil {
		return d, err
	}
	if d.SchemeJSON == "" {
		d.SchemeJSON = "{}"
	}
	if d.State == "" {
		d.State = "{}"
	}
	return d, nil
}
