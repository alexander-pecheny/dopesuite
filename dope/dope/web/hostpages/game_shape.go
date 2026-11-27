package hostpages

import (
	"context"
	"database/sql"
	"net/http"
	"strings"

	"dope/dope/domain/core"
	"dope/dope/domain/festops"
	"dope/dope/domain/games"
	"dope/dope/web/route"
	dopeui "dope/dope/web/ui"
	dopestrings "dope/i18nstrings"
)

// A Multi game's minigames, written again on a Game already created
// (games.Reshaper): the organizers drop a minigame, or split one into its
// tasks. The settings page shows the spec as the creation form took it.

// GameShape is a Game's shape: its minigames' spec and its sorting.
type GameShape struct {
	Spec    string `json:"spec"`
	Sorting string `json:"sorting"`
}

// loadGameShape reads the shape block; nil for a format that has no shape
// to write again.
func loadGameShape(ctx context.Context, db *sql.DB, festID, gameID int64) (*GameShape, error) {
	var gameType, scheme string
	err := db.QueryRowContext(ctx, `
select game_type, coalesce(scheme_json, '') from games where id = ? and fest_id = ?`, gameID, festID).Scan(&gameType, &scheme)
	if err != nil {
		return nil, err
	}
	reshaper, ok := games.As[games.Reshaper](gameType)
	if !ok {
		return nil, nil
	}
	spec, sorting := reshaper.Shape(scheme)
	return &GameShape{Spec: spec, Sorting: sorting}, nil
}

// settingsShape is the shape block the settings page draws: the Game's, or,
// after a refused save, the one the host typed.
func (s *Server) settingsShape(r *http.Request, festID, gameID int64, errMsg string) (*GameShape, error) {
	shape, err := loadGameShape(r.Context(), s.h.Engine().DB, festID, gameID)
	if err != nil || shape == nil || errMsg == "" || !r.Form.Has("multi_games") {
		return shape, err
	}
	return &GameShape{Spec: r.Form.Get("multi_games"), Sorting: r.Form.Get("multi_sorting")}, nil
}

// shapeFields are the settings form's minigames: the spec and the sorting,
// saved by the form's one button with the title and the rest.
func shapeFields(shape GameShape) []dopeui.Item {
	return []dopeui.Item{
		dopeui.Field(dopeui.Label(dopestrings.Default.Host.Games.MinigamesLabel()),
			dopeui.Editor(dopeui.Name("multi_games"), dopeui.Rows("8"), dopeui.Spellcheck("false"), dopeui.Text(shape.Spec))),
		dopeui.Field(dopeui.Label(dopestrings.Default.Host.Games.MultiSortingLabel()),
			dopeui.Textfield(dopeui.Name("multi_sorting"), dopeui.Value(shape.Sorting))),
	}
}

// SetGameShape gives a Game a new shape (festops.SetShapeTx).
func (s *Server) SetGameShape(ctx context.Context, festID, gameID int64, shape GameShape) error {
	_, err := s.commit(ctx, festID, "game-shape", []int64{gameID}, func(ctx context.Context, tx *sql.Tx) (core.FestWrite, error) {
		return festops.SetShapeTx(ctx, tx, festID, gameID, shape.Spec, shape.Sorting)
	})
	return err
}

// formShape writes the shape the settings form carries, when it differs
// from the Game's; a form without the minigames, or with them as they are,
// writes nothing.
func (s *Server) formShape(r *http.Request, festID, gameID int64) error {
	if !r.Form.Has("multi_games") {
		return nil
	}
	shape := GameShape{Spec: r.Form.Get("multi_games"), Sorting: r.Form.Get("multi_sorting")}
	current, err := loadGameShape(r.Context(), s.h.Engine().DB, festID, gameID)
	if err != nil || current == nil || normalizedShape(*current) == normalizedShape(shape) {
		return err
	}
	return s.SetGameShape(r.Context(), festID, gameID, shape)
}

// normalizedShape compares two shapes as typed: line ends and the space
// around them do not count.
func normalizedShape(shape GameShape) GameShape {
	lines := strings.Split(strings.ReplaceAll(shape.Spec, "\r\n", "\n"), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}
	return GameShape{Spec: strings.TrimSpace(strings.Join(lines, "\n")), Sorting: strings.TrimSpace(shape.Sorting)}
}

// apiGameShape is the settings form's minigames as an API call: POST {spec,
// sorting}; it answers the Game's settings.
func (s *Server) apiGameShape(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	var shape GameShape
	if err := route.DecodeJSON(r, &shape); err != nil {
		return err
	}
	if err := s.SetGameShape(r.Context(), sc.FestID, sc.GameID, shape); err != nil {
		return err
	}
	return s.apiGameSettings(w, r, sc)
}
