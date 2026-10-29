package hostpages

import (
	"strings"
	"testing"

	"dope/dope/domain/multifest"
	"dope/dope/domain/view"
	dopeui "dope/dope/web/ui"
)

// The dashboard's venues and a venue's page render, each venue linked to its
// page, its Games under it.
func TestVenueSectionsRender(t *testing.T) {
	venue := multifest.Venue{ID: 7, Title: "Москва", Representative: "Колесов Артём"}
	game := PublicFestGame{ID: 3, Title: "Москва", Type: "od", MultiFestVenueID: 7}
	dash := hostFestDashData{MultiFestVenues: []venueRow{{Venue: venue, Teams: 2, Games: []PublicFestGame{game}}}, RosterVenues: []int64{7}}
	for name, section := range map[string]*dopeui.Element{
		"venues": hostDashVenuesSection(dash, "km"),
		"roster": hostDashVenueRosterSection(dash, "km"),
	} {
		html, err := dopeui.Render(&dopeui.Doc{Nodes: []dopeui.Node{dopeui.Page(dopeui.Title("t"), section)}})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.Contains(string(html), `href="/host/fest/km/venue/7"`) {
			t.Fatalf("%s does not link the venue's page: %s", name, html)
		}
	}
	page := hostVenueDoc(hostVenuePageData{Fest: view.HostFest{ID: 1, Title: "Километр"}, Venue: venue, Games: []PublicFestGame{game},
		Teams: []hostFestTeam{{ID: 5, Name: "Альфа", Venue: 7, VenueNumber: 1}}, Synch: true})
	html, err := dopeui.Render(page)
	if err != nil {
		t.Fatalf("venue page: %v", err)
	}
	for _, want := range []string{`&quot;ref&quot;:5`, `data-team-row`, `data-unique`, `href="/host/fest/1/game/3/"`, `data-offline-prepare`} {
		if !strings.Contains(string(html), want) {
			t.Fatalf("the venue page lacks %s: %s", want, html)
		}
	}
}
