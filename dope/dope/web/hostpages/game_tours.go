package hostpages

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"dope/dope/domain/core"
	"dope/dope/domain/festops"
	"dope/dope/domain/games"
	"dope/dope/storage/store"
	"dope/dope/web/route"
	dopeui "dope/dope/web/ui"
	dopestrings "dope/i18nstrings"

	corei18n "code.pecheny.me/pecheny/dopesuite/dopecore/i18nstrings"
	"code.pecheny.me/pecheny/dopesuite/dopecore/idstr"
)

// An OD's or a minefield's tours, changed on a Game already under way
// (games.Retourer): typed on its settings page, or loaded from the
// tournament site or from another Game of the fest.

// ToursSource is where a Game's new tours come from: typed (Tours), the
// rating.chgk.info tournament (Tournament), or another Game (Game). The
// first one given wins.
type ToursSource struct {
	Tours      []int `json:"tours,omitempty"`
	Tournament int64 `json:"tournament,omitempty"`
	Game       int64 `json:"game,omitempty"`
}

// gameTours is the settings page's tours block: the Game's tours, the
// fest's tournament, and the fest's other Games that have tours.
type gameTours struct {
	Tours      []int
	Tournament int64
	Others     []gameToursOption
}

type gameToursOption struct {
	ID    int64
	Title string
	Tours []int
}

// loadGameTours reads the tours block; nil for a format whose tours do not
// change after creation.
func loadGameTours(ctx context.Context, db *sql.DB, festID, gameID int64) (*gameTours, error) {
	type row struct {
		option   gameToursOption
		gameType string
	}
	rows, err := store.CollectRows(ctx, db, `
select id, title, game_type, coalesce(scheme_json, '') from games where fest_id = ? order by position, id`, []any{festID}, func(rows *sql.Rows) (row, error) {
		var r row
		var scheme string
		err := rows.Scan(&r.option.ID, &r.option.Title, &r.gameType, &scheme)
		r.option.Tours = games.ParseTourComp(scheme)
		return r, err
	})
	if err != nil {
		return nil, err
	}
	out := &gameTours{}
	own := false
	for _, r := range rows {
		if r.option.ID != gameID {
			if len(r.option.Tours) > 0 {
				out.Others = append(out.Others, r.option)
			}
			continue
		}
		_, own = games.As[games.Retourer](r.gameType)
		out.Tours = r.option.Tours
	}
	if !own {
		return nil, nil
	}
	return out, db.QueryRowContext(ctx, `select coalesce(rating_id, 0) from fests where id = ?`, festID).Scan(&out.Tournament)
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

// toursBlock is the settings page's tours: the field with its own save, and
// the two loaders, each its own form so Enter in one field never presses
// another's button.
func toursBlock(action string, t gameTours) []dopeui.Item {
	tournament := ""
	if t.Tournament > 0 {
		tournament = idstr.Format(t.Tournament)
	}
	items := []dopeui.Item{
		dopeui.Form(dopeui.DirCol, dopeui.Method("post"), dopeui.Action(action), dopeui.Autocomplete("off"),
			dopeui.Field(dopeui.Label(dopestrings.Default.Host.Games.ToursLabel()), dopeui.Textfield(dopeui.Name("tours"), dopeui.Value(formatTours(t.Tours)), dopeui.Required())),
			dopeui.Row(dopeui.Button(dopeui.Submit(), dopeui.Text(dopestrings.Default.Host.Games.ToursSave())))),
		dopeui.Form(dopeui.Method("post"), dopeui.Action(action), dopeui.Autocomplete("off"),
			dopeui.Row(dopeui.SpaceSM,
				dopeui.Field(dopeui.Label(dopestrings.Default.Host.Games.ToursSiteLabel()), dopeui.Textfield(dopeui.Name("tournament"), dopeui.Value(tournament), dopeui.Inputmode("numeric"), dopeui.Required())),
				dopeui.Button(dopeui.Submit(), dopeui.Text(dopestrings.Default.Host.Games.ToursFromSite())))),
	}
	if len(t.Others) > 0 {
		options := []dopeui.Item{dopeui.Name("game")}
		for _, other := range t.Others {
			options = append(options, dopeui.Option(dopeui.Value(idstr.Format(other.ID)), dopeui.Text(other.Title+" · "+formatTours(other.Tours))))
		}
		items = append(items, dopeui.Form(dopeui.Method("post"), dopeui.Action(action), dopeui.Autocomplete("off"),
			dopeui.Row(dopeui.SpaceSM,
				dopeui.Field(dopeui.Label(dopestrings.Default.Host.Games.ToursGameLabel()), dopeui.Selectfield(options...)),
				dopeui.Button(dopeui.Submit(), dopeui.Text(dopestrings.Default.Host.Games.ToursFromGame())))))
	}
	return items
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

// sourceTours is the tours the source names: typed, the tournament site's,
// or another Game's.
func (s *Server) sourceTours(ctx context.Context, festID int64, from ToursSource) ([]int, error) {
	switch {
	case len(from.Tours) > 0:
		return from.Tours, nil
	case from.Tournament > 0:
		raw := idstr.Format(from.Tournament)
		tours, err := s.h.Engine().RatingSite("").Tours(ctx, from.Tournament)
		if err != nil {
			return nil, corei18n.User(dopestrings.Default.Host.Games.ErrorRatingTours(raw, err.Error()))
		}
		if len(tours) == 0 {
			return nil, corei18n.User(dopestrings.Default.Host.Games.ErrorRatingNoTours(raw))
		}
		return tours, nil
	case from.Game > 0:
		doc, err := store.LoadGameDoc(ctx, s.h.Engine().DB, festID, from.Game)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, corei18n.User(dopestrings.Default.Host.Games.ErrorToursGame())
		}
		return games.ParseTourComp(doc.SchemeJSON), err
	}
	return nil, nil
}

func (s *Server) handleHostGameTours(w http.ResponseWriter, r *http.Request, festID, gameID int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	var from ToursSource
	switch {
	case r.Form.Has("tours"):
		// Tours that are no numbers come empty, and festops says what the
		// field takes.
		from.Tours = parseTours(r.Form.Get("tours"))
	case r.Form.Has("tournament"):
		id, err := idstr.Parse(strings.TrimSpace(r.Form.Get("tournament")))
		if err != nil || id <= 0 {
			s.renderHostGameSettings(w, r, festID, gameID, dopestrings.Default.Host.Games.ErrorRatingNoTours(r.Form.Get("tournament")))
			return
		}
		from.Tournament = id
	default:
		id, err := idstr.Parse(r.Form.Get("game"))
		if err != nil || id <= 0 {
			s.renderHostGameSettings(w, r, festID, gameID, dopestrings.Default.Host.Games.ErrorToursGame())
			return
		}
		from.Game = id
	}
	err := s.SetGameTours(r.Context(), festID, gameID, from)
	s.settingsWritten(w, r, festID, gameID, s.gameRefOrID(r.Context(), gameID), err)
}

// apiGameTours is handleHostGameTours' twin: POST {tours}, {tournament} or
// {game}; it answers the Game's settings.
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
