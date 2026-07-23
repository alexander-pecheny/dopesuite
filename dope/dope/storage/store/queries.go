package store

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"dope/dope/platform/util"
)

// ResolveFestID accepts either a positive integer (the fest id) or a slug and
// returns the numeric fest id. Returns sql.ErrNoRows if no fest matches.
func ResolveFestID(ctx context.Context, q Queryer, ref string) (int64, error) {
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

// FlatMatchID returns the id of the single match (code 'main') hosting a flat
// (ЧГК-family) game's state under the unified model.
func FlatMatchID(ctx context.Context, q Queryer, gameID int64) (int64, error) {
	var id int64
	err := q.QueryRowContext(ctx,
		`select id from matches where game_id = ? and code = 'main'`, gameID).Scan(&id)
	return id, err
}

// FlatGameStateJSON reads a flat game's state document from its match.
func FlatGameStateJSON(ctx context.Context, q Queryer, gameID int64) (string, error) {
	var state string
	err := q.QueryRowContext(ctx,
		`select state_json from matches where game_id = ? and code = 'main'`, gameID).Scan(&state)
	if state == "" {
		state = "{}"
	}
	return state, err
}

// RecalculateMatchResultsForStateTx recomputes and upserts the match_results
// rows (place/total/plus/tiebreak/metrics) for every occupied slot of a match
// from its in-memory state.
func RecalculateMatchResultsForStateTx(ctx context.Context, tx *sql.Tx, match DBMatchState) error {
	view := BuildView(match.State)
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

func matchMetricsJSON(team TeamView) string {
	metrics := map[string]any{
		"correctCounts": team.CorrectCounts,
		"wrongCounts":   team.WrongCounts,
	}
	for index, value := range QuestionValues {
		metrics[fmt.Sprintf("correct_%d", value)] = team.CorrectCounts[index]
		metrics[fmt.Sprintf("wrong_%d", value)] = team.WrongCounts[index]
	}
	return util.MustJSON(metrics)
}
