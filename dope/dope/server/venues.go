package dopeserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"dope/dope/domain/core"
	"dope/dope/domain/rooms"
	"dope/dope/storage/festaccess"
	"dope/dope/storage/store"
	"dope/dope/web/route"

	"code.pecheny.me/pecheny/dopesuite/dopecore/idstr"
)

// A fest's rooms and each game's venue map (ADR-0037). The fest's venues
// page lists the rooms and, under each, the bouts the games' maps put there;
// a game's venues tab maps its venues to rooms and gives them hosts.

// roomRequest is the body of POST and PUT …/rooms.
type roomRequest struct {
	Title string `json:"title"`
}

// roomsPage is the fest's venues page: its rooms, every bout played in
// each in order, the games that have venues (a game's tab copies another's
// rooms), and whether the caller may edit the rooms.
type roomsPage struct {
	Rooms    []rooms.Room     `json:"rooms"`
	Bouts    []rooms.RoomBout `json:"bouts"`
	Games    []gameRef        `json:"games"`
	Editable bool             `json:"editable"`
}

// gameRef is a game as a page links to it: its URL ref and its title.
type gameRef struct {
	Ref   string `json:"ref"`
	Title string `json:"title"`
}

// venueMapRequest is the body of PUT …/games/{game}/venues: lines of the map,
// each naming a venue by number and the fields it changes.
type venueMapRequest struct {
	Venues []rooms.VenueEdit `json:"venues"`
}

// venueLineRequest is the body of PUT …/games/{game}/venues/{n}.
type venueLineRequest struct {
	Room  *string `json:"room"`
	Hosts *string `json:"hosts"`
}

func (s *server) scopedRooms(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	page, err := s.loadRoomsPage(r.Context(), sc)
	if err != nil {
		return err
	}
	return route.JSON(w, page)
}

func (s *server) loadRoomsPage(ctx context.Context, sc route.Scope) (roomsPage, error) {
	list, err := rooms.Load(ctx, s.eng.DB, sc.FestID)
	if err != nil {
		return roomsPage{}, err
	}
	bouts, err := rooms.LoadSchedule(ctx, s.eng.DB, sc.FestID)
	if err != nil {
		return roomsPage{}, err
	}
	games, err := store.CollectRows(ctx, s.eng.DB, `
select coalesce(nullif(g.slug, ''), g.code), g.title from games g
where g.fest_id = ? and exists(select 1 from matches m where m.game_id = g.id and m.venue is not null)
order by g.position, g.id`, []any{sc.FestID}, func(rows *sql.Rows) (gameRef, error) {
		var g gameRef
		return g, rows.Scan(&g.Ref, &g.Title)
	})
	if err != nil {
		return roomsPage{}, err
	}
	// A public fest's read does not look the role up; the page asks.
	role := sc.Role
	if role == "" && sc.HasUser {
		if role, err = festaccess.FestUserRoleFromQuery(ctx, s.eng.DB, sc.FestID, sc.User.UserID); err != nil {
			return roomsPage{}, err
		}
	}
	// Empty lists, not nulls: the page draws a host the empty table with its
	// add row.
	if list == nil {
		list = []rooms.Room{}
	}
	if bouts == nil {
		bouts = []rooms.RoomBout{}
	}
	return roomsPage{Rooms: list, Bouts: bouts, Games: games, Editable: role != ""}, nil
}

func (s *server) scopedRoomCreate(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	var req roomRequest
	if err := route.DecodeJSON(r, &req); err != nil {
		return err
	}
	if err := s.writeRooms(r.Context(), sc.FestID, "room-create", map[string]any{"action": "create", "title": req.Title},
		func(ctx context.Context, tx *sql.Tx) error {
			_, err := rooms.CreateTx(ctx, tx, sc.FestID, req.Title)
			return err
		}); err != nil {
		return route.BadUser(err)
	}
	return s.scopedRooms(w, r, sc)
}

func (s *server) scopedRoomPut(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	roomID, err := roomIDOf(r)
	if err != nil {
		return err
	}
	var req roomRequest
	if err := route.DecodeJSON(r, &req); err != nil {
		return err
	}
	if err := s.writeRooms(r.Context(), sc.FestID, "room-rename", map[string]any{"action": "rename", "room": roomID, "title": req.Title},
		func(ctx context.Context, tx *sql.Tx) error {
			return rooms.RenameTx(ctx, tx, sc.FestID, roomID, req.Title)
		}); err != nil {
		return route.BadUser(err)
	}
	return s.scopedRooms(w, r, sc)
}

func (s *server) scopedRoomDelete(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	roomID, err := roomIDOf(r)
	if err != nil {
		return err
	}
	if err := s.writeRooms(r.Context(), sc.FestID, "room-delete", map[string]any{"action": "delete", "room": roomID},
		func(ctx context.Context, tx *sql.Tx) error { return rooms.DeleteTx(ctx, tx, sc.FestID, roomID) }); err != nil {
		return route.BadUser(err)
	}
	return s.scopedRooms(w, r, sc)
}

func roomIDOf(r *http.Request) (int64, error) {
	id, err := idstr.Parse(r.PathValue("room"))
	if err != nil || id <= 0 {
		return 0, route.BadRequest("bad room id")
	}
	return id, nil
}

// writeRooms runs a write of the fest's rooms, journaled under venues:update,
// and tells the open pages of every game whose venues are in a room.
func (s *server) writeRooms(ctx context.Context, festID int64, label string, payload map[string]any, write func(context.Context, *sql.Tx) error) error {
	var games []int64
	revision, err := s.eng.CommitFestWrite(ctx, festID, label, func(ctx context.Context, tx *sql.Tx) (core.FestWrite, error) {
		if err := write(ctx, tx); err != nil {
			return core.FestWrite{}, err
		}
		var err error
		games, err = store.CollectRows(ctx, tx, `select distinct gv.game_id from game_venues gv join games g on g.id = gv.game_id where g.fest_id = ? and gv.room_id is not null`,
			[]any{festID}, func(rows *sql.Rows) (int64, error) {
				var id int64
				return id, rows.Scan(&id)
			})
		return core.FestWrite{Event: "venues:update", Payload: payload}, err
	})
	if err == nil {
		s.broadcastVenues(festID, revision, games)
	}
	return err
}

func (s *server) scopedGameVenues(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	venues, err := rooms.LoadGameVenues(r.Context(), s.eng.DB, sc.GameID)
	if err != nil {
		return err
	}
	if venues == nil {
		venues = []store.VenueView{}
	}
	return route.JSON(w, venues)
}

// scopedGameVenueMap writes lines of the game's venue map and answers its
// venues.
func (s *server) scopedGameVenueMap(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	var req venueMapRequest
	if err := route.DecodeJSON(r, &req); err != nil {
		return err
	}
	if err := s.setVenueMap(r.Context(), sc.FestID, sc.GameID, req.Venues); err != nil {
		return route.BadUser(err)
	}
	return s.scopedGameVenues(w, r, sc)
}

// scopedGameVenuePut writes one line of the game's venue map.
func (s *server) scopedGameVenuePut(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	number, err := strconv.Atoi(r.PathValue("n"))
	if err != nil || number <= 0 {
		return route.BadRequest("bad venue number")
	}
	var req venueLineRequest
	if err := route.DecodeJSON(r, &req); err != nil {
		return err
	}
	if err := s.setVenueMap(r.Context(), sc.FestID, sc.GameID, []rooms.VenueEdit{{Number: number, Room: req.Room, Hosts: req.Hosts}}); err != nil {
		return route.BadUser(err)
	}
	return s.scopedGameVenues(w, r, sc)
}

func (s *server) setVenueMap(ctx context.Context, festID, gameID int64, edits []rooms.VenueEdit) error {
	revision, err := s.eng.CommitFestWrite(ctx, festID, "game-venues", func(ctx context.Context, tx *sql.Tx) (core.FestWrite, error) {
		if err := rooms.SetMapTx(ctx, tx, festID, gameID, edits); err != nil {
			return core.FestWrite{}, err
		}
		return core.FestWrite{Event: "venues:update", Payload: map[string]any{"action": "map", "game": gameID, "venues": edits}}, nil
	})
	if err == nil {
		s.broadcastVenues(festID, revision, []int64{gameID})
	}
	return err
}

// broadcastVenues tells the open pages what a rooms or map write changed:
// every bout of the games named, which reads its venue afresh, each game's
// grid, then the venues signal the game pages refetch their venues on.
func (s *server) broadcastVenues(festID, revision int64, games []int64) {
	for _, gameID := range games {
		bouts, err := store.CollectRows(context.Background(), s.eng.DB, `select id, code from matches where game_id = ?`, []any{gameID},
			func(rows *sql.Rows) (matchScope, error) {
				m := matchScope{festScope: festScope{FestID: festID, GameID: gameID}}
				return m, rows.Scan(&m.MatchID, &m.Code)
			})
		if err != nil {
			continue
		}
		for _, bout := range bouts {
			view, err := s.loadScopedMatchViewSnapshot(bout)
			if err != nil {
				continue
			}
			if data, err := json.Marshal(view); err == nil {
				s.eng.BroadcastState(festID, matchScopeKey(bout), revision, data)
			}
		}
		s.broadcastFestView(festScope{FestID: festID, GameID: gameID}, revision)
	}
	s.eng.BroadcastState(festID, fmt.Sprintf("venues:%d", festID), revision, []byte(`{}`))
}
