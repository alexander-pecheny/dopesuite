package dopeserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"dope/dope/platform/util"
	"dope/dope/storage/festwrite"
	"dope/dope/storage/store"
	dopestrings "dope/i18nstrings"

	corei18n "pecheny.me/dopecore/i18nstrings"
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
func (s *server) writeVenues(reqCtx context.Context, festID int64, label string, payload map[string]any,
	write func(ctx context.Context, tx *sql.Tx) ([]matchScope, error)) (venueChange, error) {
	ctx, cancel := festwrite.AuditDetachedContext(reqCtx, festID)
	defer cancel()
	conn, err := s.eng.AcquireWriteConn(ctx, label)
	if err != nil {
		return venueChange{}, err
	}
	defer conn.Close()

	defer s.eng.LockWrite(label)()

	tx, err := s.eng.BeginWriteTxConn(ctx, conn)
	if err != nil {
		return venueChange{}, err
	}
	defer tx.Rollback()

	seated, err := write(ctx, tx)
	if err != nil {
		return venueChange{}, err
	}
	revision, err := festwrite.BumpFestRevisionTx(ctx, tx, festID, "venues:update", util.MustJSON(payload))
	if err != nil {
		return venueChange{}, err
	}
	venues, err := store.LoadVenues(ctx, tx, festID)
	if err != nil {
		return venueChange{}, err
	}
	if err := tx.Commit(); err != nil {
		return venueChange{}, err
	}
	return venueChange{Venues: venues, Revision: revision, Seated: seated}, nil
}

func venueUserError(msg string) error { return corei18n.User(msg) }

func (s *server) updateVenue(reqCtx context.Context, festID int64, number int, title string) ([]store.VenueView, int64, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, 0, venueUserError(dopestrings.Default.Server.Venue.TitleEmpty())
	}
	change, err := s.writeVenues(reqCtx, festID, "venue-rename", map[string]any{"number": number, "title": title},
		func(ctx context.Context, tx *sql.Tx) ([]matchScope, error) {
			result, err := tx.ExecContext(ctx, `
update venues set title = ?, updated_at = ?
where fest_id = ? and number = ?`, title, util.UtcNow(), festID, number)
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
func (s *server) createVenue(reqCtx context.Context, festID int64, number int, title string) (venueChange, error) {
	title = strings.TrimSpace(title)
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
insert into venues(fest_id, number, title, created_at, updated_at) values(?, ?, ?, ?, ?)`, festID, number, title, now, now)
		if err != nil {
			return nil, err
		}
		return seatSchemeBoutsTx(ctx, tx, festID, number, venueID)
	})
}

// seatSchemeBoutsTx seats at venueID every bout of the fest whose Game's
// scheme puts it at table number and that sits at no venue, and returns them.
func seatSchemeBoutsTx(ctx context.Context, tx *sql.Tx, festID int64, number int, venueID int64) ([]matchScope, error) {
	type game struct {
		id     int64
		scheme string
	}
	games, err := store.CollectRows(ctx, tx, `select id, coalesce(scheme_json, '{}') from games where fest_id = ? order by id`,
		[]any{festID}, func(rows *sql.Rows) (game, error) {
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

// deleteVenue removes a venue no bout plays at.
func (s *server) deleteVenue(reqCtx context.Context, festID int64, number int) (venueChange, error) {
	return s.writeVenues(reqCtx, festID, "venue-delete", map[string]any{"action": "delete", "number": number},
		func(ctx context.Context, tx *sql.Tx) ([]matchScope, error) {
			var venueID int64
			err := tx.QueryRowContext(ctx, `select id from venues where fest_id = ? and number = ?`, festID, number).Scan(&venueID)
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
