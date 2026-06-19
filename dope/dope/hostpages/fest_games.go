package hostpages

import (
	"context"
	"fmt"

	"dope/dope/store"
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
	ID    int64
	Code  string
	Title string
	Type  string
	Slug  string
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
select id, code, title, game_type, coalesce(slug, '')
from games where fest_id = ?
order by position, id`, festID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FestGameRow
	for rows.Next() {
		var g FestGameRow
		if err := rows.Scan(&g.ID, &g.Code, &g.Title, &g.Type, &g.Slug); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
