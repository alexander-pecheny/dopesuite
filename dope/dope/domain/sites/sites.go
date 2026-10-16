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
	"sort"

	"dope/dope/platform/util"
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
	venues := make([]Venue, 0, len(venueOf))
	for _, venue := range venueOf {
		venues = append(venues, venue)
	}
	// New sites take their places in the order of their names.
	sort.Slice(venues, func(i, j int) bool { return venues[i].Title < venues[j].Title })
	siteOf := map[int64]int64{}
	for _, venue := range venues {
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
update fest_teams set site_id = ?, site_number = null
where fest_id = ? and rating_id = ? and hand = 0 and site_id is not ?`, siteOf[venue.ID], festID, ratingID, siteOf[venue.ID])
		if err != nil {
			return false, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			moved = true
		}
	}
	numbered, err := numberSitesTx(ctx, tx, festID)
	return moved || numbered, err
}

// numberSitesTx gives each team on a site without a number there the next
// numbers of that site, in the order of the teams' names: a site numbers its
// own teams 1…n.
func numberSitesTx(ctx context.Context, tx *sql.Tx, festID int64) (bool, error) {
	type unnumbered struct {
		id, site   int64
		name, city string
	}
	teams, err := store.CollectRows(ctx, tx, `
select id, site_id, name, city from fest_teams
where fest_id = ? and site_id is not null and site_number is null and deleted = 0`, []any{festID},
		func(rows *sql.Rows) (unnumbered, error) {
			var t unnumbered
			return t, rows.Scan(&t.id, &t.site, &t.name, &t.city)
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
		if _, ok := next[team.site]; !ok {
			var top int64
			if err := tx.QueryRowContext(ctx, `
select coalesce(max(site_number), 0) from fest_teams where site_id = ?`, team.site).Scan(&top); err != nil {
				return false, err
			}
			next[team.site] = top
		}
		next[team.site]++
		if _, err := tx.ExecContext(ctx, `update fest_teams set site_number = ? where id = ?`, next[team.site], team.id); err != nil {
			return false, err
		}
	}
	return true, nil
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

// Place is the site a team is on and its number there.
type Place struct{ Site, Number int64 }

// Placement is where the fest's teams are, by fest team id, rating id and
// fest Number: a roster list names a team by whichever of them it carries.
type Placement struct {
	byLocal, byRating, byNumber map[int64]Place
}

// LoadPlacement reads which site each of the fest's teams is on.
func LoadPlacement(ctx context.Context, q store.Queryer, festID int64) (Placement, error) {
	p := Placement{byLocal: map[int64]Place{}, byRating: map[int64]Place{}, byNumber: map[int64]Place{}}
	rows, err := q.QueryContext(ctx, `
select id, coalesce(rating_id, 0), coalesce(number, 0), site_id, coalesce(site_number, 0) from fest_teams
where fest_id = ? and deleted = 0 and site_id is not null`, festID)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, ratingID, number int64
		var place Place
		if err := rows.Scan(&id, &ratingID, &number, &place.Site, &place.Number); err != nil {
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

// FestNumbers turns the numbers a Game on siteID names its teams by into the
// fest's: the same numbers for a Game on none. ok is false for a number the
// site gives no team.
func FestNumbers(ctx context.Context, q store.Queryer, festID, siteID int64) (func(int64) (int64, bool), error) {
	if siteID == 0 {
		return func(n int64) (int64, bool) { return n, n > 0 }, nil
	}
	teams, err := ByNumber(ctx, q, festID, siteID)
	if err != nil {
		return nil, err
	}
	return func(n int64) (int64, bool) {
		team, ok := teams[n]
		return team.FestNumber, ok && team.FestNumber > 0
	}, nil
}

// ByNumber is the fest's teams under the numbers a Game on siteID gives them:
// that site's own, or the fest's for a Game on none. A Game's document names
// its teams by these numbers, and the fest's Participants by the fest's.
func ByNumber(ctx context.Context, q store.Queryer, festID, siteID int64) (map[int64]Team, error) {
	query, args := `
select number, id, number, coalesce(rating_id, 0) from fest_teams
where fest_id = ? and deleted = 0 and number is not null`, []any{festID}
	if siteID > 0 {
		query, args = `
select site_number, id, coalesce(number, 0), coalesce(rating_id, 0) from fest_teams
where fest_id = ? and site_id = ? and deleted = 0 and site_number is not null`, []any{festID, siteID}
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
