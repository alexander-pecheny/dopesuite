// Package rooms holds a fest's rooms and each game's venue map (ADR-0037).
//
// A room is a physical place of the fest: a title and nothing else. A game's
// venues are the numbers its scheme deals its bouts to; its venue map says,
// for each, which room it is in and who hosts it. A bout sits at one of its
// game's venues by number (matches.venue) and may name hosts of its own
// (matches.hosts).
package rooms

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"dope/dope/platform/util"
	"dope/dope/storage/store"
	dopestrings "dope/i18nstrings"

	corei18n "code.pecheny.me/pecheny/dopesuite/dopecore/i18nstrings"
	"code.pecheny.me/pecheny/dopesuite/dopecore/idstr"
)

// Key is how two room titles are compared: case and spacing aside, and the
// letter yo read as ye.
func Key(title string) string {
	key := strings.ToLower(strings.Join(strings.Fields(title), " "))
	return strings.ReplaceAll(key, "\u0451", "\u0435")
}

// Room is one of the fest's rooms, with the titles of the games whose venue
// maps put a venue in it.
type Room struct {
	ID    int64    `json:"id"`
	Title string   `json:"title"`
	Games []string `json:"games,omitempty"`
}

// Load returns the fest's rooms in their order.
func Load(ctx context.Context, q store.Queryer, festID int64) ([]Room, error) {
	rooms, err := store.CollectRows(ctx, q, `
select id, title from rooms where fest_id = ? order by position, id`, []any{festID}, func(rows *sql.Rows) (Room, error) {
		var r Room
		return r, rows.Scan(&r.ID, &r.Title)
	})
	if err != nil {
		return nil, err
	}
	uses, err := store.CollectRows(ctx, q, `
select distinct gv.room_id, g.title from game_venues gv join games g on g.id = gv.game_id
where g.fest_id = ? and gv.room_id is not null order by g.position, g.id`, []any{festID}, func(rows *sql.Rows) (struct {
		room  int64
		title string
	}, error) {
		var u struct {
			room  int64
			title string
		}
		return u, rows.Scan(&u.room, &u.title)
	})
	if err != nil {
		return nil, err
	}
	for _, u := range uses {
		for i := range rooms {
			if rooms[i].ID == u.room {
				rooms[i].Games = append(rooms[i].Games, u.title)
			}
		}
	}
	return rooms, nil
}

// CreateTx adds a room to the end of the fest's list and returns its id.
func CreateTx(ctx context.Context, tx *sql.Tx, festID int64, title string) (int64, error) {
	title, err := freeTitleTx(ctx, tx, festID, 0, title)
	if err != nil {
		return 0, err
	}
	now := util.UtcNow()
	return store.InsertReturningID(ctx, tx, `
insert into rooms(fest_id, title, position, created_at, updated_at)
values(?, ?, (select coalesce(max(position), 0) + 1 from rooms where fest_id = ?), ?, ?)`, festID, title, festID, now, now)
}

// RenameTx gives a room another title.
func RenameTx(ctx context.Context, tx *sql.Tx, festID, roomID int64, title string) error {
	title, err := freeTitleTx(ctx, tx, festID, roomID, title)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `update rooms set title = ?, updated_at = ? where id = ? and fest_id = ?`, title, util.UtcNow(), roomID, festID)
	if err != nil {
		return err
	}
	return mustAffect(result, roomID)
}

// DeleteTx removes a room no game's venue is in; the refusal names the games
// that use it.
func DeleteTx(ctx context.Context, tx *sql.Tx, festID, roomID int64) error {
	games, err := store.CollectRows(ctx, tx, `
select distinct g.title from game_venues gv join games g on g.id = gv.game_id
where gv.room_id = ? and g.fest_id = ? order by g.position, g.id`, []any{roomID, festID}, func(rows *sql.Rows) (string, error) {
		var title string
		return title, rows.Scan(&title)
	})
	if err != nil {
		return err
	}
	if len(games) > 0 {
		return corei18n.User(dopestrings.Default.Rooms.InUse("«" + strings.Join(games, "», «") + "»"))
	}
	result, err := tx.ExecContext(ctx, `delete from rooms where id = ? and fest_id = ?`, roomID, festID)
	if err != nil {
		return err
	}
	return mustAffect(result, roomID)
}

func mustAffect(result sql.Result, roomID int64) error {
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return corei18n.User(dopestrings.Default.Rooms.Unknown(idstr.Format(roomID)))
	}
	return nil
}

// freeTitleTx checks a room title: not empty, and no other room of the fest
// with the same Key.
func freeTitleTx(ctx context.Context, tx *sql.Tx, festID, roomID int64, title string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return "", corei18n.User(dopestrings.Default.Rooms.TitleEmpty())
	}
	existing, err := Load(ctx, tx, festID)
	if err != nil {
		return "", err
	}
	for _, r := range existing {
		if r.ID != roomID && Key(r.Title) == Key(title) {
			return "", corei18n.User(dopestrings.Default.Rooms.TitleTaken(r.Title))
		}
	}
	return title, nil
}

// ResolveTx finds the room a venue map names: by its id, or by its title as
// Key compares them. An empty ref is no room (0). A title the fest has no room
// for is refused, so a typo makes no room.
func ResolveTx(ctx context.Context, q store.Queryer, festID int64, ref string) (int64, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return 0, nil
	}
	existing, err := Load(ctx, q, festID)
	if err != nil {
		return 0, err
	}
	if id, err := idstr.Parse(ref); err == nil {
		for _, r := range existing {
			if r.ID == id {
				return id, nil
			}
		}
	}
	for _, r := range existing {
		if Key(r.Title) == Key(ref) {
			return r.ID, nil
		}
	}
	return 0, corei18n.User(dopestrings.Default.Rooms.Unknown(ref))
}

// DealtNumbers are the venue numbers a compiled scheme deals its bouts to,
// in order.
func DealtNumbers(schemeJSON string) []int {
	var scheme struct {
		Stages []struct {
			Matches []struct {
				Venue int `json:"venue"`
			} `json:"matches"`
		} `json:"stages"`
	}
	if json.Unmarshal([]byte(schemeJSON), &scheme) != nil {
		return nil
	}
	var out []int
	for _, stage := range scheme.Stages {
		for _, match := range stage.Matches {
			if match.Venue > 0 && !slices.Contains(out, match.Venue) {
				out = append(out, match.Venue)
			}
		}
	}
	slices.Sort(out)
	return out
}

// GameNumbers are a game's venues: the numbers its scheme deals to and those
// its bouts sit at.
func GameNumbers(ctx context.Context, q store.Queryer, gameID int64) ([]int, error) {
	var scheme string
	if err := q.QueryRowContext(ctx, `select coalesce(scheme_json, '') from games where id = ?`, gameID).Scan(&scheme); err != nil {
		return nil, err
	}
	numbers := DealtNumbers(scheme)
	seated, err := store.CollectRows(ctx, q, `select distinct venue from matches where game_id = ? and venue is not null`, []any{gameID},
		func(rows *sql.Rows) (int, error) {
			var n int
			return n, rows.Scan(&n)
		})
	if err != nil {
		return nil, err
	}
	for _, n := range seated {
		if n > 0 && !slices.Contains(numbers, n) {
			numbers = append(numbers, n)
		}
	}
	slices.Sort(numbers)
	return numbers, nil
}

// LoadGameVenues returns a game's venues with its map.
func LoadGameVenues(ctx context.Context, q store.Queryer, gameID int64) ([]store.VenueView, error) {
	numbers, err := GameNumbers(ctx, q, gameID)
	if err != nil || len(numbers) == 0 {
		return nil, err
	}
	mapped, err := store.CollectRows(ctx, q, `
select gv.number, coalesce(gv.room_id, 0), coalesce(r.title, ''), gv.hosts
from game_venues gv left join rooms r on r.id = gv.room_id
where gv.game_id = ?`, []any{gameID}, func(rows *sql.Rows) (store.VenueView, error) {
		var v store.VenueView
		return v, rows.Scan(&v.Number, &v.Room, &v.Title, &v.Hosts)
	})
	if err != nil {
		return nil, err
	}
	type seated struct{ venue, bouts int }
	bouts, err := store.CollectRows(ctx, q, `select venue, count(*) from matches where game_id = ? and venue is not null group by venue`, []any{gameID},
		func(rows *sql.Rows) (seated, error) {
			var b seated
			return b, rows.Scan(&b.venue, &b.bouts)
		})
	if err != nil {
		return nil, err
	}
	out := make([]store.VenueView, len(numbers))
	for i, n := range numbers {
		out[i] = store.VenueView{Number: n}
		for _, m := range mapped {
			if m.Number == n {
				out[i] = m
			}
		}
		for _, b := range bouts {
			if b.venue == n {
				out[i].Bouts = b.bouts
			}
		}
	}
	return out, nil
}

// VenueEdit is one venue's line of a map write: a field left nil stays as it
// is. Room names a room by id or title, and an empty one takes the venue out
// of its room.
type VenueEdit struct {
	Number int     `json:"number"`
	Room   *string `json:"room"`
	Hosts  *string `json:"hosts"`
}

// SetMapTx writes edits into a game's venue map. Every number must be one of
// the game's venues, and every room one of the fest's.
func SetMapTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, edits []VenueEdit) error {
	numbers, err := GameNumbers(ctx, tx, gameID)
	if err != nil {
		return err
	}
	for _, e := range edits {
		if !slices.Contains(numbers, e.Number) {
			return corei18n.User(dopestrings.Default.Rooms.NoVenue(strconv.Itoa(e.Number)))
		}
		if err := setVenueTx(ctx, tx, festID, gameID, e); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `delete from game_venues where game_id = ? and room_id is null and hosts = ''`, gameID)
	return err
}

func setVenueTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, e VenueEdit) error {
	var room sql.NullInt64
	if e.Room != nil {
		id, err := ResolveTx(ctx, tx, festID, *e.Room)
		if err != nil {
			return err
		}
		room = sql.NullInt64{Int64: id, Valid: id > 0}
	}
	hosts := ""
	if e.Hosts != nil {
		hosts = strings.TrimSpace(*e.Hosts)
	}
	_, err := tx.ExecContext(ctx, `
insert into game_venues(game_id, number, room_id, hosts, updated_at) values(?, ?, ?, ?, ?)
on conflict(game_id, number) do update set
  room_id = case when ? then excluded.room_id else room_id end,
  hosts = case when ? then excluded.hosts else hosts end,
  updated_at = excluded.updated_at`,
		gameID, e.Number, room, hosts, util.UtcNow(), e.Room != nil, e.Hosts != nil)
	return err
}

// SetBoutVenueTx moves a bout to another of its game's venues, and gives it
// its own hosts when hosts is not nil (empty: the venue's).
func SetBoutVenueTx(ctx context.Context, tx *sql.Tx, gameID, matchID int64, number int, hosts *string) error {
	numbers, err := GameNumbers(ctx, tx, gameID)
	if err != nil {
		return err
	}
	if !slices.Contains(numbers, number) {
		return corei18n.User(dopestrings.Default.Rooms.NoVenue(strconv.Itoa(number)))
	}
	if hosts == nil {
		_, err = tx.ExecContext(ctx, `update matches set venue = ?, revision = revision + 1 where id = ? and game_id = ?`, number, matchID, gameID)
		return err
	}
	_, err = tx.ExecContext(ctx, `update matches set venue = ?, hosts = ?, revision = revision + 1 where id = ? and game_id = ?`,
		number, strings.TrimSpace(*hosts), matchID, gameID)
	return err
}

// RoomBout is one bout of a room's schedule: when it starts, its game (URL
// ref and title), the game's venue it sits at, the bout's letter, code and
// title, and who hosts it.
type RoomBout struct {
	Room      int64  `json:"room"`
	Venue     int    `json:"venue"`
	StartsAt  string `json:"startsAt,omitempty"`
	Game      string `json:"game"`
	GameTitle string `json:"gameTitle"`
	Code      string `json:"code"`
	Letter    string `json:"letter,omitempty"`
	Title     string `json:"title,omitempty"`
	Finished  bool   `json:"finished,omitempty"`
	Hosts     string `json:"hosts,omitempty"`
}

// LoadSchedule returns every bout of the fest whose venue is in a room, room
// by room, in the order they are played: by start time, then the game's order
// and the schedule's.
func LoadSchedule(ctx context.Context, q store.Queryer, festID int64) ([]RoomBout, error) {
	return store.CollectRows(ctx, q, `
select gv.room_id, m.venue, coalesce(m.starts_at, ''), coalesce(nullif(g.slug, ''), g.code), g.title,
       m.code, m.letter, m.title, m.status = 'finished', coalesce(nullif(m.hosts, ''), gv.hosts)
from matches m
join games g on g.id = m.game_id
join game_venues gv on gv.game_id = m.game_id and gv.number = m.venue
join rooms r on r.id = gv.room_id
join stages s on s.id = m.stage_id
where m.fest_id = ?
order by r.position, r.id, coalesce(m.starts_at, '') = '', m.starts_at, g.position, s.position, m.position`, []any{festID},
		func(rows *sql.Rows) (RoomBout, error) {
			var b RoomBout
			return b, rows.Scan(&b.Room, &b.Venue, &b.StartsAt, &b.Game, &b.GameTitle, &b.Code, &b.Letter, &b.Title, &b.Finished, &b.Hosts)
		})
}
