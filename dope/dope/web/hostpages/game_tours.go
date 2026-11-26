package hostpages

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"dope/dope/domain/core"
	"dope/dope/domain/festops"
	"dope/dope/domain/games"
	"dope/dope/web/route"
	dopeui "dope/dope/web/ui"
	dopestrings "dope/i18nstrings"

	corei18n "code.pecheny.me/pecheny/dopesuite/dopecore/i18nstrings"
	"code.pecheny.me/pecheny/dopesuite/dopecore/idstr"
)

// An OD's or a minefield's tours, changed on a Game already under way
// (games.Retourer): typed on its settings page, or loaded from the fest's
// tournament on the tournament site.

// ToursSource is where a Game's new tours come from: typed (Tours), or the
// fest's rating.chgk.info tournament (FromSite).
type ToursSource struct {
	Tours    []int `json:"tours,omitempty"`
	FromSite bool  `json:"from_site,omitempty"`
}

// gameTours is the settings page's tours block: the Game's tours.
type gameTours struct {
	Tours []int
}

// loadGameTours reads the tours block; nil for a format whose tours do not
// change after creation.
func loadGameTours(ctx context.Context, db *sql.DB, festID, gameID int64) (*gameTours, error) {
	var gameType, scheme string
	err := db.QueryRowContext(ctx, `
select game_type, coalesce(scheme_json, '') from games where id = ? and fest_id = ?`, gameID, festID).Scan(&gameType, &scheme)
	if err != nil {
		return nil, err
	}
	if _, ok := games.As[games.Retourer](gameType); !ok {
		return nil, nil
	}
	return &gameTours{Tours: games.ParseTourComp(scheme)}, nil
}

// formatTours writes tours as the field shows them: "15 15 15".
func formatTours(tours []int) string {
	return strings.Trim(fmt.Sprint(tours), "[]")
}

// parseTours reads the field: numbers apart by spaces or commas. A word
// that is no number makes the tours empty, which festops refuses.
func parseTours(text string) []int {
	fields := strings.Fields(strings.ReplaceAll(text, ",", " "))
	out := make([]int, 0, len(fields))
	for _, field := range fields {
		n, err := strconv.Atoi(field)
		if err != nil {
			return nil
		}
		out = append(out, n)
	}
	return out
}

// toursBlock is the settings page's tours: the field, then its save and
// the load from the fest's tournament side by side.
func toursBlock(action string, t gameTours) []dopeui.Item {
	return []dopeui.Item{
		dopeui.Form(dopeui.DirCol, dopeui.Method("post"), dopeui.Action(action), dopeui.Autocomplete("off"),
			dopeui.Field(dopeui.Label(dopestrings.Default.Host.Games.ToursLabel()), dopeui.Textfield(dopeui.Name("tours"), dopeui.Value(formatTours(t.Tours)))),
			dopeui.Row(dopeui.SpaceSM, dopeui.Wrap(),
				dopeui.Button(dopeui.Submit(), dopeui.Text(dopestrings.Default.Host.Games.ToursSave())),
				dopeui.Button(dopeui.Submit(), dopeui.Name("from_site"), dopeui.Value("1"), dopeui.Text(dopestrings.Default.Host.Games.ToursFromSite())))),
	}
}

// SetGameTours gives a Game the tours the source names (festops.SetToursTx).
// The tournament site is asked before the write, which must not wait on the
// network.
func (s *Server) SetGameTours(ctx context.Context, festID, gameID int64, from ToursSource) error {
	tours, err := s.sourceTours(ctx, festID, from)
	if err != nil {
		return err
	}
	_, err = s.commit(ctx, festID, "game-tours", []int64{gameID}, func(ctx context.Context, tx *sql.Tx) (core.FestWrite, error) {
		return festops.SetToursTx(ctx, tx, festID, gameID, tours)
	})
	return err
}

// sourceTours is the tours the source names: typed, or the fest's
// tournament's on the tournament site.
func (s *Server) sourceTours(ctx context.Context, festID int64, from ToursSource) ([]int, error) {
	if !from.FromSite {
		return from.Tours, nil
	}
	var tournament int64
	if err := s.h.Engine().DB.QueryRowContext(ctx, `select coalesce(rating_id, 0) from fests where id = ?`, festID).Scan(&tournament); err != nil {
		return nil, err
	}
	if tournament <= 0 {
		return nil, corei18n.User(dopestrings.Default.Host.RatingExport.NoTournament())
	}
	raw := idstr.Format(tournament)
	tours, err := s.h.Engine().RatingSite("").Tours(ctx, tournament)
	if err != nil {
		return nil, corei18n.User(dopestrings.Default.Host.Games.ErrorRatingTours(raw, err.Error()))
	}
	if len(tours) == 0 {
		return nil, corei18n.User(dopestrings.Default.Host.Games.ErrorRatingNoTours(raw))
	}
	return tours, nil
}

func (s *Server) handleHostGameTours(w http.ResponseWriter, r *http.Request, festID, gameID int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	// Tours that are no numbers come empty, and festops says what the field
	// takes.
	from := ToursSource{FromSite: r.Form.Has("from_site")}
	if !from.FromSite {
		from.Tours = parseTours(r.Form.Get("tours"))
	}
	err := s.SetGameTours(r.Context(), festID, gameID, from)
	s.settingsWritten(w, r, festID, gameID, s.gameRefOrID(r.Context(), gameID), err)
}

// apiGameTours is handleHostGameTours' twin: POST {tours} or {from_site:
// true}; it answers the Game's settings.
func (s *Server) apiGameTours(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	var from ToursSource
	if err := route.DecodeJSON(r, &from); err != nil {
		return err
	}
	if err := s.SetGameTours(r.Context(), sc.FestID, sc.GameID, from); err != nil {
		return err
	}
	return s.apiGameSettings(w, r, sc)
}
