// Package sites holds a fest's type and its sites, the venues of a synch or
// multi fest (ADR-0031). A synch fest is one tournament played at many venues
// at once, and the rating import puts each team on the venue rating.chgk.info
// has it play at; a Game on a site seats only that site's teams. A regular
// fest has no sites, and everything here leaves it as it was.
package sites

import (
	"context"
	"database/sql"
	"errors"

	"dope/dope/storage/store"
)

// The fest types.
const (
	Regular = "regular"
	Synch   = "synch"
	Multi   = "multi"
)

// ValidKind reports whether kind is one of the three fest types.
func ValidKind(kind string) bool { return kind == Regular || kind == Synch || kind == Multi }

// Site is one venue of the fest.
type Site struct {
	ID            int64  `json:"id"`
	Title         string `json:"title"`
	Town          string `json:"town"`
	RatingVenueID int64  `json:"rating_venue_id,omitempty"`
}

// Venue is a venue as rating.chgk.info names it.
type Venue struct {
	ID    int64
	Title string
	Town  string
}

// FestKind is the fest's type.
func FestKind(ctx context.Context, q store.Queryer, festID int64) (string, error) {
	var kind string
	err := q.QueryRowContext(ctx, `select kind from fests where id = ?`, festID).Scan(&kind)
	return kind, err
}

// List is the fest's sites in their order.
func List(ctx context.Context, q store.Queryer, festID int64) ([]Site, error) {
	return store.CollectRows(ctx, q, `
select id, title, town, coalesce(rating_venue_id, 0) from fest_sites
where fest_id = ? order by position, id`, []any{festID}, func(rows *sql.Rows) (Site, error) {
		var s Site
		return s, rows.Scan(&s.ID, &s.Title, &s.Town, &s.RatingVenueID)
	})
}

// GameSite is the site a Game is on, 0 for none.
func GameSite(ctx context.Context, q store.Queryer, gameID int64) (int64, error) {
	var site sql.NullInt64
	err := q.QueryRowContext(ctx, `select site_id from games where id = ?`, gameID).Scan(&site)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return site.Int64, err
}

// PlaceTeamsTx puts each rating team on the site of its rating venue,
// making the sites it has not seen yet, and reports whether any team moved.
// A team the host made by hand keeps its site.
func PlaceTeamsTx(ctx context.Context, tx *sql.Tx, festID int64, venueOf map[int64]Venue) (bool, error) {
	siteOf := map[int64]int64{}
	for _, venue := range venueOf {
		if _, done := siteOf[venue.ID]; done {
			continue
		}
		id, err := venueSiteTx(ctx, tx, festID, venue)
		if err != nil {
			return false, err
		}
		siteOf[venue.ID] = id
	}
	moved := false
	for ratingID, venue := range venueOf {
		res, err := tx.ExecContext(ctx, `
update fest_teams set site_id = ?
where fest_id = ? and rating_id = ? and hand = 0 and site_id is not ?`, siteOf[venue.ID], festID, ratingID, siteOf[venue.ID])
		if err != nil {
			return false, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			moved = true
		}
	}
	return moved, nil
}

func venueSiteTx(ctx context.Context, tx *sql.Tx, festID int64, venue Venue) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `select id from fest_sites where fest_id = ? and rating_venue_id = ?`, festID, venue.ID).Scan(&id)
	if err == nil || !errors.Is(err, sql.ErrNoRows) {
		return id, err
	}
	res, err := tx.ExecContext(ctx, `
insert into fest_sites(fest_id, title, town, rating_venue_id, position)
values(?, ?, ?, ?, (select coalesce(max(position), 0) + 1 from fest_sites where fest_id = ?))`,
		festID, venue.Title, venue.Town, venue.ID, festID)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Placement is where the fest's teams are, by fest team id, rating id and
// Number: a roster list names a team by whichever of them it carries.
type Placement struct {
	byLocal, byRating, byNumber map[int64]int64
}

// LoadPlacement reads which site each of the fest's teams is on.
func LoadPlacement(ctx context.Context, q store.Queryer, festID int64) (Placement, error) {
	p := Placement{byLocal: map[int64]int64{}, byRating: map[int64]int64{}, byNumber: map[int64]int64{}}
	rows, err := q.QueryContext(ctx, `
select id, coalesce(rating_id, 0), coalesce(number, 0), site_id from fest_teams
where fest_id = ? and deleted = 0 and site_id is not null`, festID)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, ratingID, number, site int64
		if err := rows.Scan(&id, &ratingID, &number, &site); err != nil {
			return p, err
		}
		p.byLocal[id] = site
		if ratingID > 0 {
			p.byRating[ratingID] = site
		}
		if number > 0 {
			p.byNumber[number] = site
		}
	}
	return p, rows.Err()
}

// Of is the site of a team known by its row, its rating id or its Number.
func (p Placement) Of(localID, ratingID, number int64) int64 {
	switch {
	case localID > 0:
		return p.byLocal[localID]
	case ratingID > 0:
		return p.byRating[ratingID]
	}
	return p.byNumber[number]
}
