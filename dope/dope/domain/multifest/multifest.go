// Package multifest holds a fest's type and the venues of a synch or multi
// fest (ADR-0031), as against the rooms a bout is played in. A synch fest is
// one tournament played at many venues at once, and the rating import puts
// each team on the venue rating.chgk.info has it play at; a Game on a venue
// seats only that venue's teams. A regular fest has no venues, and everything
// here leaves it as it was.
package multifest

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"sort"
	"strings"

	"dope/dope/platform/util"
	"dope/dope/storage/store"
	dopestrings "dope/i18nstrings"

	corei18n "code.pecheny.me/pecheny/dopesuite/dopecore/i18nstrings"
)

// The fest types.
const (
	Regular = "regular"
	Synch   = "synch"
	Multi   = "multi"
)

// ValidKind reports whether kind is one of the three fest types.
func ValidKind(kind string) bool { return kind == Regular || kind == Synch || kind == Multi }

// Venue is one venue of the fest.
type Venue struct {
	ID            int64  `json:"id"`
	Title         string `json:"title"`
	Town          string `json:"town"`
	RatingVenueID int64  `json:"rating_venue_id,omitempty"`
	// Representative is the venue's representative on the rating site,
	// surname first.
	Representative string `json:"representative,omitempty"`
}

// Label names the venue with its representative, unless its title already
// does, as the rating site often names a venue after the person.
func (v Venue) Label() string {
	surname, _, _ := strings.Cut(v.Representative, " ")
	if surname == "" || strings.Contains(v.Title, surname) {
		return v.Title
	}
	return v.Title + " / " + v.Representative
}

// RatingVenue is a venue as rating.chgk.info names it.
type RatingVenue struct {
	ID             int64
	Title          string
	Town           string
	Representative string
}

// FestKind is the fest's type.
func FestKind(ctx context.Context, q store.Queryer, festID int64) (string, error) {
	var kind string
	err := q.QueryRowContext(ctx, `select kind from fests where id = ?`, festID).Scan(&kind)
	return kind, err
}

// List is the fest's venues in their order.
func List(ctx context.Context, q store.Queryer, festID int64) ([]Venue, error) {
	return store.CollectRows(ctx, q, `
select id, title, town, coalesce(rating_venue_id, 0), representative from multifest_venues
where fest_id = ? order by position, id`, []any{festID}, func(rows *sql.Rows) (Venue, error) {
		var s Venue
		return s, rows.Scan(&s.ID, &s.Title, &s.Town, &s.RatingVenueID, &s.Representative)
	})
}

// VenueOf is one venue of the fest; a venue of another fest is an error a
// person can read.
func VenueOf(ctx context.Context, q store.Queryer, festID, venueID int64) (Venue, error) {
	var v Venue
	err := q.QueryRowContext(ctx, `
select id, title, town, coalesce(rating_venue_id, 0), representative from multifest_venues where id = ? and fest_id = ?`, venueID, festID).Scan(&v.ID, &v.Title, &v.Town, &v.RatingVenueID, &v.Representative)
	if errors.Is(err, sql.ErrNoRows) {
		return v, corei18n.User(dopestrings.Default.Host.Multifest.VenueUnknown())
	}
	return v, err
}

// GameVenue is the venue a Game is on, 0 for none.
func GameVenue(ctx context.Context, q store.Queryer, gameID int64) (int64, error) {
	var venue sql.NullInt64
	err := q.QueryRowContext(ctx, `select multifest_venue_id from games where id = ?`, gameID).Scan(&venue)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return venue.Int64, err
}

// PlaceTeamsTx puts each rating team on the fest venue of its rating venue,
// making the venues it has not seen yet, and reports whether any team moved.
// A team the host made by hand keeps its venue.
func PlaceTeamsTx(ctx context.Context, tx *sql.Tx, festID int64, venueOf map[int64]RatingVenue) (bool, error) {
	venues := make([]RatingVenue, 0, len(venueOf))
	for _, venue := range venueOf {
		venues = append(venues, venue)
	}
	festVenue, err := AddVenuesTx(ctx, tx, festID, venues)
	if err != nil {
		return false, err
	}
	moved := false
	for ratingID, venue := range venueOf {
		res, err := tx.ExecContext(ctx, `
update fest_teams set multifest_venue_id = ?, venue_number = null
where fest_id = ? and rating_id = ? and hand = 0 and multifest_venue_id is not ?`, festVenue[venue.ID], festID, ratingID, festVenue[venue.ID])
		if err != nil {
			return false, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			moved = true
		}
	}
	numbered, err := NumberVenuesTx(ctx, tx, festID)
	return moved || numbered, err
}

// NumberVenuesTx gives each team on a venue without a number there the next
// numbers of that venue, in the order of the teams' names: a venue numbers its
// own teams 1…n.
func NumberVenuesTx(ctx context.Context, tx *sql.Tx, festID int64) (bool, error) {
	type unnumbered struct {
		id, venue  int64
		name, city string
	}
	teams, err := store.CollectRows(ctx, tx, `
select id, multifest_venue_id, name, city from fest_teams
where fest_id = ? and multifest_venue_id is not null and venue_number is null and deleted = 0`, []any{festID},
		func(rows *sql.Rows) (unnumbered, error) {
			var t unnumbered
			return t, rows.Scan(&t.id, &t.venue, &t.name, &t.city)
		})
	if err != nil || len(teams) == 0 {
		return false, err
	}
	sort.SliceStable(teams, func(i, j int) bool {
		if cmp := util.CompareAlpha(teams[i].name, teams[j].name); cmp != 0 {
			return cmp < 0
		}
		return util.CompareAlpha(teams[i].city, teams[j].city) < 0
	})
	next := map[int64]int64{}
	for _, team := range teams {
		if _, ok := next[team.venue]; !ok {
			var top int64
			if err := tx.QueryRowContext(ctx, `
select coalesce(max(venue_number), 0) from fest_teams where multifest_venue_id = ?`, team.venue).Scan(&top); err != nil {
				return false, err
			}
			next[team.venue] = top
		}
		next[team.venue]++
		if _, err := tx.ExecContext(ctx, `update fest_teams set venue_number = ? where id = ?`, next[team.venue], team.id); err != nil {
			return false, err
		}
	}
	return true, nil
}

// AddVenuesTx makes the fest venues of the rating venues it has not seen yet,
// in the order of their names, and answers the fest venue of each.
func AddVenuesTx(ctx context.Context, tx *sql.Tx, festID int64, venues []RatingVenue) (map[int64]int64, error) {
	sorted := slices.Clone(venues)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Title < sorted[j].Title })
	festVenue := map[int64]int64{}
	for _, venue := range sorted {
		if _, done := festVenue[venue.ID]; done {
			continue
		}
		id, err := ratingVenueTx(ctx, tx, festID, venue)
		if err != nil {
			return nil, err
		}
		festVenue[venue.ID] = id
	}
	return festVenue, nil
}

// NewVenues is the rating venues the fest does not have yet.
func NewVenues(ctx context.Context, q store.Queryer, festID int64, venues []RatingVenue) ([]RatingVenue, error) {
	have, err := List(ctx, q, festID)
	if err != nil {
		return nil, err
	}
	known := map[int64]bool{}
	for _, v := range have {
		known[v.RatingVenueID] = true
	}
	var fresh []RatingVenue
	for _, v := range venues {
		if !known[v.ID] {
			fresh = append(fresh, v)
		}
	}
	return fresh, nil
}

func ratingVenueTx(ctx context.Context, tx *sql.Tx, festID int64, venue RatingVenue) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `select id from multifest_venues where fest_id = ? and rating_venue_id = ?`, festID, venue.ID).Scan(&id)
	if err == nil && venue.Town != "" {
		_, err = tx.ExecContext(ctx, `update multifest_venues set town = ? where id = ? and town = ''`, venue.Town, id)
	}
	if err == nil && venue.Representative != "" {
		_, err = tx.ExecContext(ctx, `update multifest_venues set representative = ? where id = ?`, venue.Representative, id)
	}
	if err == nil || !errors.Is(err, sql.ErrNoRows) {
		return id, err
	}
	res, err := tx.ExecContext(ctx, `
insert into multifest_venues(fest_id, title, town, rating_venue_id, representative, position)
values(?, ?, ?, ?, ?, (select coalesce(max(position), 0) + 1 from multifest_venues where fest_id = ?))`,
		festID, venue.Title, venue.Town, venue.ID, venue.Representative, festID)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Place is the venue a team is on and its number there.
type Place struct{ Venue, Number int64 }

// Placement is where the fest's teams are, by fest team id, rating id and
// fest Number: a roster list names a team by whichever of them it carries.
type Placement struct {
	byLocal, byRating, byNumber map[int64]Place
}

// LoadPlacement reads which venue each of the fest's teams is on.
func LoadPlacement(ctx context.Context, q store.Queryer, festID int64) (Placement, error) {
	p := Placement{byLocal: map[int64]Place{}, byRating: map[int64]Place{}, byNumber: map[int64]Place{}}
	rows, err := q.QueryContext(ctx, `
select id, coalesce(rating_id, 0), coalesce(number, 0), multifest_venue_id, coalesce(venue_number, 0) from fest_teams
where fest_id = ? and deleted = 0 and multifest_venue_id is not null`, festID)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, ratingID, number int64
		var place Place
		if err := rows.Scan(&id, &ratingID, &number, &place.Venue, &place.Number); err != nil {
			return p, err
		}
		p.byLocal[id] = place
		if ratingID > 0 {
			p.byRating[ratingID] = place
		}
		if number > 0 {
			p.byNumber[number] = place
		}
	}
	return p, rows.Err()
}

// Of is the place of a team known by its row, its rating id or its fest Number.
func (p Placement) Of(localID, ratingID, number int64) Place {
	switch {
	case localID > 0:
		return p.byLocal[localID]
	case ratingID > 0:
		return p.byRating[ratingID]
	}
	return p.byNumber[number]
}

// Team is a fest team as a Game names it.
type Team struct {
	ID, FestNumber, RatingID int64
}

// FestNumbers turns the numbers a Game on venueID names its teams by into the
// fest's: the same numbers for a Game on none. ok is false for a number the
// venue gives no team.
func FestNumbers(ctx context.Context, q store.Queryer, festID, venueID int64) (func(int64) (int64, bool), error) {
	if venueID == 0 {
		return func(n int64) (int64, bool) { return n, n > 0 }, nil
	}
	teams, err := ByNumber(ctx, q, festID, venueID)
	if err != nil {
		return nil, err
	}
	return func(n int64) (int64, bool) {
		team, ok := teams[n]
		return team.FestNumber, ok && team.FestNumber > 0
	}, nil
}

// ByNumber is the fest's teams under the numbers a Game on venueID gives them:
// that venue's own, or the fest's for a Game on none. A Game's document names
// its teams by these numbers, and the fest's Participants by the fest's.
func ByNumber(ctx context.Context, q store.Queryer, festID, venueID int64) (map[int64]Team, error) {
	query, args := `
select number, id, number, coalesce(rating_id, 0) from fest_teams
where fest_id = ? and deleted = 0 and number is not null`, []any{festID}
	if venueID > 0 {
		query, args = `
select venue_number, id, coalesce(number, 0), coalesce(rating_id, 0) from fest_teams
where fest_id = ? and multifest_venue_id = ? and deleted = 0 and venue_number is not null`, []any{festID, venueID}
	}
	type numbered struct {
		number int64
		team   Team
	}
	rows, err := store.CollectRows(ctx, q, query, args, func(rows *sql.Rows) (numbered, error) {
		var n numbered
		return n, rows.Scan(&n.number, &n.team.ID, &n.team.FestNumber, &n.team.RatingID)
	})
	if err != nil {
		return nil, err
	}
	out := make(map[int64]Team, len(rows))
	for _, row := range rows {
		out[row.number] = row.team
	}
	return out, nil
}
