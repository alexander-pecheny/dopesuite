package hostpages

import (
	"reflect"
	"testing"

	"dope/dope/domain/multifest"
)

// A synch fest's OD is one game of the whole fest, and each venue shows its
// view of it beside the venue's own games.
func TestEachVenueShowsItsViewOfTheFestsGames(t *testing.T) {
	all := []PublicFestGame{
		{ID: 1, Title: "ОД", URL: "/fest/f/game/1/", VenueSheets: true},
		{ID: 2, Title: "КСИ", URL: "/fest/f/game/2/", MultiFestVenueID: 7},
	}
	fest, venues := SplitByVenue(all, []multifest.Venue{{ID: 7, Title: "Москва"}, {ID: 8, Title: "Казань"}})
	if len(fest) != 1 || fest[0].ID != 1 {
		t.Fatalf("the fest's games: %+v", fest)
	}
	urls := func(v PublicVenue) []string {
		var out []string
		for _, g := range v.Games {
			out = append(out, g.URL)
		}
		return out
	}
	if got := urls(venues[0]); !reflect.DeepEqual(got, []string{"/fest/f/game/1/?venue=7", "/fest/f/game/2/"}) {
		t.Fatalf("Москва shows %v", got)
	}
	if got := urls(venues[1]); !reflect.DeepEqual(got, []string{"/fest/f/game/1/?venue=8"}) {
		t.Fatalf("Казань shows %v", got)
	}
	if all[0].URL != "/fest/f/game/1/" {
		t.Fatalf("the split changed the fest's own link: %s", all[0].URL)
	}
}
