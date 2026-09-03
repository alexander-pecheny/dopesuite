package gamebuild

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"dope/dope/domain/games"
	"dope/dope/platform/util"
	"dope/dope/storage/festwrite"
)

// Clear resets a Game to its just-created state: every game-scoped derived
// row goes — results, imported seeds, the bracket's resolution — and the
// pristine Structure is written back, while the Game keeps its id, code, slug,
// title and, when it named its entrants, those entrants. Fest-scoped teams,
// players and the audit log stay. Returns the code of the first Match, for
// the host's cursor.
func Clear(ctx context.Context, tx *sql.Tx, festID, gameID int64) (string, error) {
	var gameType, title, schemeJSON, dsl string
	if err := tx.QueryRowContext(ctx, `
select game_type, title, coalesce(scheme_json, '{}'), coalesce(scheme_dsl, '') from games where id = ? and fest_id = ?`,
		gameID, festID).Scan(&gameType, &title, &schemeJSON, &dsl); err != nil {
		return "", err
	}
	// A pre-DSL Brain gets its shortcut scheme re-expressed in the DSL, so a
	// clear upgrades it onto the one authoring path.
	if gameType == games.Brain && strings.TrimSpace(dsl) == "" {
		var count int
		if err := tx.QueryRowContext(ctx, `select count(*) from fest_teams where fest_id = ?`, festID).Scan(&count); err != nil {
			return "", err
		}
		dsl = DefaultBrainDSL(count, games.BrainQuestions(schemeJSON))
		if _, err := tx.ExecContext(ctx, `update games set scheme_dsl = ? where id = ?`, dsl, gameID); err != nil {
			return "", err
		}
	}
	entrants, err := gameEntrantsTx(ctx, tx, gameID)
	if err != nil {
		return "", err
	}
	// matches/stages cascade to their slots, results and standings (FKs are on).
	for _, q := range []string{
		`delete from matches where game_id = ?`,
		`delete from stages where game_id = ?`,
		`delete from game_assignments where game_id = ?`,
		`delete from game_participants where game_id = ?`,
		`delete from game_team_players where game_id = ?`,
		`delete from game_player_team_overrides where game_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, gameID); err != nil {
			return "", err
		}
	}

	var meta struct {
		Slug  string `json:"slug"`
		Title string `json:"title"`
	}
	_ = json.Unmarshal([]byte(schemeJSON), &meta)
	if strings.TrimSpace(meta.Title) == "" {
		meta.Title = title
	}
	now := util.UtcNow()
	status := "active"
	var newScheme []byte
	switch {
	case strings.TrimSpace(dsl) != "":
		if newScheme, err = rebuildTx(ctx, tx, festID, gameID, gameType, dsl, schemeJSON, entrants); err != nil {
			return "", err
		}
	case gameType == games.OD:
		tourComp := games.ParseTourComp(schemeJSON)
		if len(tourComp) == 0 {
			tourComp = []int{15}
		}
		var state []byte
		emptyScheme, emptyState := games.ODEmptyGameJSON(meta.Slug, meta.Title, tourComp)
		if newScheme, state, err = pristineFlatTx(ctx, tx, festID, games.OD, emptyScheme, emptyState); err != nil {
			return "", err
		}
		if err := insertFlatMatchTx(ctx, tx, festID, gameID, title, string(state), now); err != nil {
			return "", err
		}
	case gameType == games.KSI:
		var sc struct {
			Themes   int             `json:"themes"`
			Stickers json.RawMessage `json:"stickers"`
		}
		_ = json.Unmarshal([]byte(schemeJSON), &sc)
		if sc.Themes <= 0 {
			sc.Themes = 20
		}
		// The sticker configuration survives, so a stickers game stays one.
		var state []byte
		emptyScheme, emptyState := games.KSIStickersEmptyGameJSON(meta.Slug, meta.Title, sc.Themes, sc.Stickers)
		if newScheme, state, err = pristineFlatTx(ctx, tx, festID, games.KSI, emptyScheme, emptyState); err != nil {
			return "", err
		}
		if err := insertFlatMatchTx(ctx, tx, festID, gameID, title, string(state), now); err != nil {
			return "", err
		}
	case gameType == games.Multi:
		// The minigames and the fest's tiebreak survive: clearing a game wipes
		// what was played, not what it is.
		var sc games.MultiScheme
		_ = json.Unmarshal([]byte(schemeJSON), &sc)
		var state []byte
		emptyScheme, emptyState := games.MultiEmptyGameJSON(meta.Slug, meta.Title, sc.Minigames, sc.Sorting)
		if newScheme, state, err = pristineFlatTx(ctx, tx, festID, games.Multi, emptyScheme, emptyState); err != nil {
			return "", err
		}
		if err := insertFlatMatchTx(ctx, tx, festID, gameID, title, string(state), now); err != nil {
			return "", err
		}
	case gameType == games.EK:
		status = "pending"
		if newScheme, err = rebuildTx(ctx, tx, festID, gameID, gameType, "", schemeJSON, nil); err != nil {
			return "", err
		}
	default:
		return "", errors.New("очистка не поддерживается для этого типа игры")
	}
	if _, err := tx.ExecContext(ctx, `
update games set scheme_json = ?, state_json = '{}', status = ?,
  team_list_source = 'fest', roster_source = 'fest', revision = revision + 1, updated_at = ?
where id = ? and fest_id = ?`, string(newScheme), status, now, gameID, festID); err != nil {
		return "", err
	}
	var first sql.NullString
	if err := tx.QueryRowContext(ctx, `
select code from matches where game_id = ? order by position, id limit 1`, gameID).Scan(&first); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	_, err = festwrite.BumpFestRevisionTx(ctx, tx, festID, "game:clear", util.MustJSON(map[string]any{
		"gameID": gameID,
		"title":  title,
	}))
	return first.String, err
}

// DefaultBrainDSL is a Brain at its plainest: one round-robin of everybody,
// so many questions a Match. The creation form offers it and Clear upgrades
// a pre-DSL Brain onto it.
func DefaultBrainDSL(participants, questions int) string {
	if participants < 2 {
		participants = 4
	}
	if questions <= 0 {
		questions = 5
	}
	return fmt.Sprintf("[defaults]\nquestions: %d\n\n[scheme]\nkind: roundrobin\ngroup_size: %d\n", questions, participants)
}
