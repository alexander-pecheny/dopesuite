package hostpages

import (
	"dope/dope/web/pages"
	dopeui "dope/dope/web/ui"
	dopestrings "dope/i18nstrings"
)

// FestVenuesLink is the button to the fest's venues page, under its games on
// the fest page and the host dashboard.
func FestVenuesLink(ref string) dopeui.Item {
	return dopeui.Row(dopeui.Button(dopeui.Href("/fest/"+ref+"/venues"), dopeui.Text(dopestrings.Default.Host.FestVenues.Link())))
}

// FestVenuesDoc is the fest's venues page, /fest/{ref}/venues: the shell
// fest-venues.ts fills with the venues and each one's bouts, which it reads
// from the API the mount names. The venues are the fest's, shared by its
// Games, so they are listed here once rather than on every Game's page.
func FestVenuesDoc(ref, title string) *dopeui.Doc {
	s := dopestrings.Default
	festCrumb := dopeui.Crumb(dopeui.Href("/fest/"+ref), dopeui.Text(title))
	return &dopeui.Doc{Nodes: []dopeui.Node{dopeui.Page(
		dopeui.Title(s.Host.FestVenues.Title(title)), dopeui.PagePublic,
		dopeui.Classicscripts("dist/fest-venues.js"),
		dopeui.Publictopbar(pages.Trail([]dopeui.Item{pages.HomeCrumb(), festCrumb}, s.Host.FestVenues.Crumb())),
		dopeui.Col(dopeui.ID("festVenues"),
			dopeui.Data("api", "/api/fest/"+ref), dopeui.Data("games", "/fest/"+ref+"/game/")),
	)}}
}
