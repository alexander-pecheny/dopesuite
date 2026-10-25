package hostpages

import (
	"context"
	"fmt"

	"dope/dope/domain/multifest"
	"dope/dope/storage/store"

	"code.pecheny.me/pecheny/dopesuite/dopecore/idstr"
)

// PublicFestGame is the per-game view model rendered in the public fest detail
// page and the host fest dashboard (its slug/id Ref and a prebuilt URL).
type PublicFestGame struct {
	ID    int64
	Slug  string
	Code  string
	Title string
	Type  string
	URL   string
	// MultiFestVenueID is the venue the game is on, 0 for none.
	MultiFestVenueID int64
	// VenueSheets: a game of the whole fest that each venue enters a sheet
	// of, and so shows a view per venue (ADR-0033).
	VenueSheets bool
}

// PublicVenue is a venue of a multi-site fest on its public page: its view of
// each game of the whole fest that keeps a sheet per venue, then its own games.
type PublicVenue struct {
	Title string
	Games []PublicFestGame
}

// SplitByVenue parts a fest's games into the games of the whole fest and the
// games each of its venues shows.
func SplitByVenue(all []PublicFestGame, venues []multifest.Venue) ([]PublicFestGame, []PublicVenue) {
	var fest []PublicFestGame
	for _, g := range all {
		if g.MultiFestVenueID == 0 {
			fest = append(fest, g)
		}
	}
	out := make([]PublicVenue, 0, len(venues))
	for _, v := range venues {
		venue := PublicVenue{Title: v.Title}
		for _, g := range all {
			switch {
			case g.MultiFestVenueID == 0 && g.VenueSheets:
				g.URL += "?venue=" + idstr.Format(v.ID)
			case g.MultiFestVenueID != v.ID:
				continue
			}
			venue.Games = append(venue.Games, g)
		}
		out = append(out, venue)
	}
	return fest, out
}

// Ref returns the game's slug if set, otherwise the stringified id.
func (g PublicFestGame) Ref() string {
	if g.Slug != "" {
		return g.Slug
	}
	return fmt.Sprintf("%d", g.ID)
}

// FestGameRow is a raw game row (id/code/title/type/slug) loaded for a fest,
// before it is shaped into a PublicFestGame with a URL.
type FestGameRow struct {
	ID               int64
	Code             string
	Title            string
	Type             string
	Slug             string
	MultiFestVenueID int64
}

// Ref returns the game's slug if set, otherwise the stringified id. Use for
// building public URLs.
func (g FestGameRow) Ref() string {
	if g.Slug != "" {
		return g.Slug
	}
	return fmt.Sprintf("%d", g.ID)
}

// LoadFestGames returns every game of a fest in display order.
func LoadFestGames(ctx context.Context, q store.Queryer, festID int64) ([]FestGameRow, error) {
	rows, err := q.QueryContext(ctx, `
select id, code, title, game_type, coalesce(slug, ''), coalesce(multifest_venue_id, 0)
from games where fest_id = ?
order by position, id`, festID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FestGameRow
	for rows.Next() {
		var g FestGameRow
		if err := rows.Scan(&g.ID, &g.Code, &g.Title, &g.Type, &g.Slug, &g.MultiFestVenueID); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
