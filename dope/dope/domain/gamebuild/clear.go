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
	"dope/dope/storage/store"
	dopestrings "dope/i18nstrings"
	corei18n "pecheny.me/dopecore/i18nstrings"
)

// Clear resets a Game to its just-created state: every game-scoped derived
// row goes — results, imported seeds, the bracket's resolution — and the
// pristine Structure is written back, while the Game keeps its id, code, slug,
// title and, when it named its entrants, those entrants. Fest-scoped teams,
// players and the audit log stay. Returns the code of the first Match, for
// the host's cursor.
func Clear(ctx context.Context, tx *sql.Tx, festID, gameID int64) (string, error) {
	return clearGame(ctx, tx, festID, gameID, nil, "game:clear")
}

// clearGame is Clear, seating the entrants given instead of the Game's own
// when there are any, and recorded under the event given: a Troika Game that
// takes a division is re-seated this way when its troikas change.
func clearGame(ctx context.Context, tx *sql.Tx, festID, gameID int64, seat []int64, event string) (string, error) {
	var gameType, title, schemeJSON, dsl string
	if err := tx.QueryRowContext(ctx, `
select game_type, title, coalesce(scheme_json, '{}'), coalesce(scheme_dsl, '') from games where id = ? and fest_id = ?`,
		gameID, festID).Scan(&gameType, &title, &schemeJSON, &dsl); err != nil {
		return "", err
	}
	def, known := games.Lookup(gameType)
	// A Game made before its format had a DSL gets the format's scheme
	// re-expressed in the DSL, so a clear moves it onto the one authoring path.
	if known && def.UpgradeDSL != nil && strings.TrimSpace(dsl) == "" {
		var count int
		if err := tx.QueryRowContext(ctx, `select count(*) from fest_teams where fest_id = ?`, festID).Scan(&count); err != nil {
			return "", err
		}
		dsl = def.UpgradeDSL(count, schemeJSON)
		if _, err := tx.ExecContext(ctx, `update games set scheme_dsl = ? where id = ?`, dsl, gameID); err != nil {
			return "", err
		}
	}
	entrants, err := gameEntrantsTx(ctx, tx, gameID)
	if err != nil {
		return "", err
	}
	if len(seat) > 0 {
		entrants = seat
	}
	// A flat Game whose document holds something a clear keeps — a friendship
	// cup's players, a Multi's guest teams — is read before the deletes below
	// take its document away. A load that fails stops the clear: going on
	// would drop what it keeps.
	var oldState string
	flat := known && def.Flat && strings.TrimSpace(dsl) == ""
	keeper, keeps := games.As[games.ClearKeeper](gameType)
	if flat && keeps {
		doc, err := store.LoadGameDoc(ctx, tx, festID, gameID)
		if err != nil {
			return "", fmt.Errorf("load %s document to keep: %w", gameType, err)
		}
		oldState = doc.State
	}
	// The rooms the bouts sit at, before the deletes take the bouts away: a
	// rebuild keeps a room the host renamed (schemeVenuesTx).
	own, err := gameVenueIDsTx(ctx, tx, gameID)
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
		if newScheme, err = rebuildTx(ctx, tx, festID, gameID, gameType, dsl, schemeJSON, entrants, own); err != nil {
			return "", err
		}
	case flat:
		// The same builder a new Game of this shape is made with, the shape
		// read back out of the stored scheme.
		var state []byte
		builder, ok := games.As[games.PristineBuilder](gameType)
		if !ok {
			return "", corei18n.User(dopestrings.Default.Gamebuild.Clear.Unsupported())
		}
		emptyScheme, emptyState, err := builder.PristineGame(meta.Slug, meta.Title, builder.ShapeOf(schemeJSON))
		if err != nil {
			return "", err
		}
		if newScheme, state, err = pristineFlatTx(ctx, tx, festID, gameType, emptyScheme, emptyState); err != nil {
			return "", err
		}
		if keeps {
			if newScheme, state, err = keeper.KeepOnClear(schemeJSON, oldState, newScheme, state); err != nil {
				return "", fmt.Errorf("keep %s document: %w", gameType, err)
			}
		}
		if err := insertFlatMatchTx(ctx, tx, festID, gameID, title, string(state), now); err != nil {
			return "", err
		}
	case known && def.LegacyPasted:
		status = "pending"
		if newScheme, err = rebuildTx(ctx, tx, festID, gameID, gameType, "", schemeJSON, nil, own); err != nil {
			return "", err
		}
	default:
		return "", corei18n.User(dopestrings.Default.Gamebuild.Clear.Unsupported())
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
	_, err = festwrite.BumpFestRevisionTx(ctx, tx, festID, event, util.MustJSON(map[string]any{
		"gameID": gameID,
		"title":  title,
	}))
	return first.String, err
}
