package hostpages

import (
	"testing"

	"code.pecheny.me/pecheny/dopesuite/dopecore/invitelink"
)

// The copied list has one line per venue with a working link, its newest
// one, and leaves out the links to Games.
func TestVenueInviteLinesTakeEachVenuesNewestWorkingLink(t *testing.T) {
	link := func(venue, game int64, opens, url string, state invitelink.State) festInvite {
		inv := festInvite{MultiFestVenueID: venue, GameID: game, Opens: opens, URL: url}
		inv.State = string(state)
		return inv
	}
	got := venueInviteLines([]festInvite{
		link(1, 0, "Москва / Колесов Артём", "https://x/join/NEW", invitelink.Active),
		link(1, 0, "Москва / Колесов Артём", "https://x/join/OLD", invitelink.Active),
		link(2, 0, "Санкт-Петербург / Лепичев Сергей", "https://x/join/GONE", invitelink.Revoked),
		link(2, 0, "Санкт-Петербург / Лепичев Сергей", "https://x/join/SPB", invitelink.Active),
		link(0, 9, "Финал", "https://x/join/GAME", invitelink.Active),
	})
	want := "Москва / Колесов Артём https://x/join/NEW\nСанкт-Петербург / Лепичев Сергей https://x/join/SPB"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}
