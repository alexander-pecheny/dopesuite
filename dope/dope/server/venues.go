package dopeserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"dope/dope/domain/core"
	"dope/dope/platform/util"
	"dope/dope/storage/store"
	dopestrings "dope/i18nstrings"

	corei18n "code.pecheny.me/pecheny/dopesuite/dopecore/i18nstrings"
)

// The fest's venues: the host lists, names, adds and deletes them on the
// venues tab. They are shared by the fest's Games. A scheme that only counts
// its tables (`venues: 3`, or none) writes no venue rows and leaves its bouts
// at none; adding venue N later seats every such bout the scheme puts at
// table N there.

// venueCreateRequest is the body of POST /api/fest/{fest}/venues. A zero
// Number takes the next free one.
type venueCreateRequest struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Hosts  string `json:"hosts,omitempty"`
}

// venueChange is what a venue write did: the fest's venues after it, the
// fest revision it reached, and the bouts it seated at a new venue.
type venueChange struct {
	Venues   []store.VenueView
	Revision int64
	Seated   []matchScope
}

// writeVenues runs one venue write in the fest's write transaction and
// journals it under venues:update.
func (s *server) writeVenues(ctx context.Context, festID int64, label string, payload map[string]any,
	write func(ctx context.Context, tx *sql.Tx) ([]matchScope, error)) (venueChange, error) {
	var change venueChange
	revision, err := s.eng.CommitFestWrite(ctx, festID, label, func(ctx context.Context, tx *sql.Tx) (core.FestWrite, error) {
		var err error
		if change.Seated, err = write(ctx, tx); err != nil {
			return core.FestWrite{}, err
		}
		if change.Venues, err = store.LoadVenues(ctx, tx, festID); err != nil {
			return core.FestWrite{}, err
		}
		return core.FestWrite{Event: "venues:update", Payload: payload}, nil
	})
	change.Revision = revision
	return change, err
}

func venueUserError(msg string) error { return corei18n.User(msg) }

func (s *server) updateVenue(reqCtx context.Context, festID int64, number int, req venueUpdateRequest) ([]store.VenueView, int64, error) {
	sets, args := []string{}, []any{}
	payload := map[string]any{"number": number}
	if req.Title != nil {
		title := strings.TrimSpace(*req.Title)
		if title == "" {
			return nil, 0, venueUserError(dopestrings.Default.Server.Venue.TitleEmpty())
		}
		sets, args, payload["title"] = append(sets, "title = ?"), append(args, title), title
	}
	if req.Hosts != nil {
		hosts := strings.TrimSpace(*req.Hosts)
		sets, args, payload["hosts"] = append(sets, "hosts = ?"), append(args, hosts), hosts
	}
	if len(sets) == 0 {
		return nil, 0, errors.New("venue update names no field")
	}
	change, err := s.writeVenues(reqCtx, festID, "venue-rename", payload,
		func(ctx context.Context, tx *sql.Tx) ([]matchScope, error) {
			result, err := tx.ExecContext(ctx, `
update venues set `+strings.Join(sets, ", ")+`, updated_at = ?
where fest_id = ? and number = ?`, append(args, util.UtcNow(), festID, number)...)
			if err != nil {
				return nil, err
			}
			affected, err := result.RowsAffected()
			if err != nil {
				return nil, err
			}
			if affected == 0 {
				return nil, venueUserError(dopestrings.Default.Server.Venue.Unknown(strconv.Itoa(number)))
			}
			return nil, nil
		})
	return change.Venues, change.Revision, err
}

// createVenue adds venue number (the next free one when number is 0) to the
// fest, and seats there every bout a Game's scheme puts at that table and
// that sits at no venue yet.
// A gameID other than 0 makes the venue that Game's own: no other Game lists
// it, and only that Game's bouts are seated there.
func (s *server) createVenue(reqCtx context.Context, festID, gameID int64, req venueCreateRequest) (venueChange, error) {
	number, title := req.Number, strings.TrimSpace(req.Title)
	hosts := strings.TrimSpace(req.Hosts)
	if title == "" {
		return venueChange{}, venueUserError(dopestrings.Default.Server.Venue.TitleEmpty())
	}
	if number < 0 {
		return venueChange{}, errors.New("bad venue number")
	}
	payload := map[string]any{"action": "create", "number": number, "title": title}
	return s.writeVenues(reqCtx, festID, "venue-create", payload, func(ctx context.Context, tx *sql.Tx) ([]matchScope, error) {
		if number == 0 {
			if err := tx.QueryRowContext(ctx, `select coalesce(max(number), 0) + 1 from venues where fest_id = ?`, festID).Scan(&number); err != nil {
				return nil, err
			}
			payload["number"] = number
		} else {
			var taken bool
			if err := tx.QueryRowContext(ctx, `select exists(select 1 from venues where fest_id = ? and number = ?)`, festID, number).Scan(&taken); err != nil {
				return nil, err
			}
			if taken {
				return nil, venueUserError(dopestrings.Default.Server.Venue.NumberTaken(strconv.Itoa(number)))
			}
		}
		now := util.UtcNow()
		venueID, err := store.InsertReturningID(ctx, tx, `
insert into venues(fest_id, number, title, hosts, game_id, created_at, updated_at) values(?, ?, ?, ?, ?, ?, ?)`,
			festID, number, title, hosts, util.NullableInt64(gameID), now, now)
		if err != nil {
			return nil, err
		}
		return seatSchemeBoutsTx(ctx, tx, festID, gameID, number, venueID)
	})
}

// seatSchemeBoutsTx seats at venueID every bout of the fest whose Game's
// scheme puts it at table number and that sits at no venue, and returns them.
// A gameID other than 0 seats that Game's bouts alone.
func seatSchemeBoutsTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, number int, venueID int64) ([]matchScope, error) {
	type game struct {
		id     int64
		scheme string
	}
	games, err := store.CollectRows(ctx, tx, `select id, coalesce(scheme_json, '{}') from games where fest_id = ? and (? = 0 or id = ?) order by id`,
		[]any{festID, gameID, gameID}, func(rows *sql.Rows) (game, error) {
			var g game
			return g, rows.Scan(&g.id, &g.scheme)
		})
	if err != nil {
		return nil, err
	}
	var seated []matchScope
	for _, g := range games {
		var scheme struct {
			Stages []struct {
				Matches []struct {
					Code  string `json:"code"`
					Venue int    `json:"venue"`
				} `json:"matches"`
			} `json:"stages"`
		}
		if err := json.Unmarshal([]byte(g.scheme), &scheme); err != nil {
			continue // a Game with no scheme of its own seats nothing
		}
		for _, stage := range scheme.Stages {
			for _, match := range stage.Matches {
				if match.Venue != number || match.Code == "" {
					continue
				}
				var matchID int64
				err := tx.QueryRowContext(ctx, `
update matches set venue_id = ?, revision = revision + 1
where game_id = ? and code = ? and venue_id is null
returning id`, venueID, g.id, match.Code).Scan(&matchID)
				if errors.Is(err, sql.ErrNoRows) {
					continue
				}
				if err != nil {
					return nil, err
				}
				seated = append(seated, matchScope{festScope: festScope{FestID: festID, GameID: g.id}, MatchID: matchID, Code: match.Code})
			}
		}
	}
	return seated, nil
}

// deleteVenue removes a venue no bout plays at. A gameID other than 0
// removes only that Game's own venue.
func (s *server) deleteVenue(reqCtx context.Context, festID, gameID int64, number int) (venueChange, error) {
	return s.writeVenues(reqCtx, festID, "venue-delete", map[string]any{"action": "delete", "number": number, "game": gameID},
		func(ctx context.Context, tx *sql.Tx) ([]matchScope, error) {
			var venueID int64
			err := tx.QueryRowContext(ctx, `select id from venues where fest_id = ? and number = ? and (? = 0 or game_id = ?)`,
				festID, number, gameID, gameID).Scan(&venueID)
			if errors.Is(err, sql.ErrNoRows) {
				return nil, venueUserError(dopestrings.Default.Server.Venue.Unknown(strconv.Itoa(number)))
			}
			if err != nil {
				return nil, err
			}
			var used bool
			if err := tx.QueryRowContext(ctx, `select exists(select 1 from matches where venue_id = ?)`, venueID).Scan(&used); err != nil {
				return nil, err
			}
			if used {
				return nil, venueUserError(dopestrings.Default.Server.Venue.InUse(strconv.Itoa(number)))
			}
			_, err = tx.ExecContext(ctx, `delete from venues where id = ?`, venueID)
			return nil, err
		})
}

// broadcastVenues tells the fest's open pages what a venue write changed:
// each bout it seated, each Game's grid those bouts are in, then the venues
// list itself. It returns the list as the response body.
func (s *server) broadcastVenues(festID int64, change venueChange) []byte {
	games := map[int64]bool{}
	for _, mscope := range change.Seated {
		games[mscope.GameID] = true
		view, err := s.loadScopedMatchViewSnapshot(mscope)
		if err != nil {
			continue
		}
		if data, err := json.Marshal(view); err == nil {
			s.eng.BroadcastState(festID, matchScopeKey(mscope), change.Revision, data)
		}
	}
	for gameID := range games {
		s.broadcastFestView(festScope{FestID: festID, GameID: gameID}, change.Revision)
	}
	data, _ := json.Marshal(change.Venues)
	s.eng.BroadcastState(festID, fmt.Sprintf("venues:%d", festID), change.Revision, data)
	return data
}

// updateGameVenue gives one Game its own title or hosts for a fest venue,
// or hides it from the Game; a field the request names and leaves empty goes
// back to the fest's. On the Game's own venue it changes the venue itself.
// Its Seated are the Game's bouts at the venue, which read the change.
func (s *server) updateGameVenue(reqCtx context.Context, festID, gameID int64, number int, req venueUpdateRequest) (venueChange, error) {
	if req.Title == nil && req.Hosts == nil && req.Hidden == nil {
		return venueChange{}, errors.New("venue update names no field")
	}
	payload := map[string]any{"action": "game", "game": gameID, "number": number}
	own := func(field *string, key string) (sql.NullString, bool) {
		if field == nil {
			return sql.NullString{}, false
		}
		value := strings.TrimSpace(*field)
		payload[key] = value
		return sql.NullString{String: value, Valid: value != ""}, true
	}
	title, setTitle := own(req.Title, "title")
	hosts, setHosts := own(req.Hosts, "hosts")
	hidden := req.Hidden != nil && *req.Hidden
	if req.Hidden != nil {
		payload["hidden"] = hidden
	}
	return s.writeVenues(reqCtx, festID, "game-venue", payload, func(ctx context.Context, tx *sql.Tx) ([]matchScope, error) {
		var venueID int64
		var venueGame sql.NullInt64
		err := tx.QueryRowContext(ctx, `select id, game_id from venues where fest_id = ? and number = ?`, festID, number).Scan(&venueID, &venueGame)
		if errors.Is(err, sql.ErrNoRows) || (venueGame.Valid && venueGame.Int64 != gameID) {
			return nil, venueUserError(dopestrings.Default.Server.Venue.Unknown(strconv.Itoa(number)))
		}
		if err != nil {
			return nil, err
		}
		bouts, err := store.CollectRows(ctx, tx, `select id, code from matches where game_id = ? and venue_id = ?`, []any{gameID, venueID},
			func(rows *sql.Rows) (matchScope, error) {
				m := matchScope{festScope: festScope{FestID: festID, GameID: gameID}}
				return m, rows.Scan(&m.MatchID, &m.Code)
			})
		if err != nil {
			return nil, err
		}
		if hidden && len(bouts) > 0 {
			return nil, venueUserError(dopestrings.Default.Server.Venue.InUse(strconv.Itoa(number)))
		}
		if venueGame.Valid {
			return bouts, updateOwnVenueTx(ctx, tx, venueID, title, setTitle, hosts, setHosts)
		}
		if _, err := tx.ExecContext(ctx, `
insert into game_venues(game_id, venue_id, title, hosts, hidden, updated_at) values(?, ?, ?, ?, ?, ?)
on conflict(game_id, venue_id) do update set
  title = case when ? then excluded.title else title end,
  hosts = case when ? then excluded.hosts else hosts end,
  hidden = case when ? then excluded.hidden else hidden end,
  updated_at = excluded.updated_at`,
			gameID, venueID, title, hosts, util.BoolToInt(hidden), util.UtcNow(), setTitle, setHosts, req.Hidden != nil); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
delete from game_venues where game_id = ? and venue_id = ? and title is null and hosts is null and hidden = 0`, gameID, venueID); err != nil {
			return nil, err
		}
		return bouts, nil
	})
}

// updateOwnVenueTx changes a Game's own venue: its title, which may not be
// emptied, and its hosts.
func updateOwnVenueTx(ctx context.Context, tx *sql.Tx, venueID int64, title sql.NullString, setTitle bool, hosts sql.NullString, setHosts bool) error {
	if setTitle && !title.Valid {
		return venueUserError(dopestrings.Default.Server.Venue.TitleEmpty())
	}
	_, err := tx.ExecContext(ctx, `
update venues set
  title = case when ? then ? else title end,
  hosts = case when ? then ? else hosts end,
  updated_at = ?
where id = ?`, setTitle, title.String, setHosts, hosts.String, util.UtcNow(), venueID)
	return err
}
