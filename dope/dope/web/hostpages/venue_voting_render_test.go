package hostpages

import (
	"strings"
	"testing"

	"dope/dope/domain/venues"
)

func samplePoll() venues.Voting {
	return venues.Voting{
		ID: 3, SlotID: 7, Token: "vtok", Kind: venues.KindRanked, ClosesAt: "2026-09-04 18:00",
		Candidates: []venues.Candidate{
			{ID: 1, Name: "Синхрон А", Type: "Синхрон"},
			{ID: 2, Name: "Асинхрон Б", Type: "Асинхрон"},
		},
	}
}

func TestSlotVotingSectionOffersTheCandidatesThenTheTally(t *testing.T) {
	venue := venues.Venue{ID: 1, Slug: "tbilisi", Title: "Площадка"}
	slot := venues.Slot{ID: 7, FestID: 1, StartsAt: "2026-09-04 19:00", RegToken: "tok"}

	// No poll yet: the form offers what buff knows about the Слот's date.
	body := renderPublic(t, slotPageDoc(slotPageData{
		Venue: venue, Slot: slot, CanManage: true,
		Voting: VotingView{Candidates: []venues.Candidate{{ID: 1, Name: "Синхрон А", Type: "Синхрон"}}},
	}))
	for _, want := range []string{"Создать голосование", `name="candidate"`, "Синхрон А · Синхрон", `name="extra_candidate"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}

	// Once created: the link, the tally, the ballots.
	poll := samplePoll()
	poll.Frozen = true
	ballots := []venues.Ballot{{ID: 5, Voter: "tester", Choice: []int64{2, 1}, CreatedAt: "2026-09-02T13:34:00Z"}}
	body = renderPublic(t, slotPageDoc(slotPageData{
		Venue: venue, Slot: slot, CanManage: true,
		Voting: VotingView{Voting: poll, URL: "https://dope.test/vote/vtok",
			Tally: venues.Tally(poll, ballots), Ballots: ballots},
	}))
	for _, want := range []string{
		`value="https://dope.test/vote/vtok"`,
		`data-copy-target="voteLink"`,
		"Вид: три по порядку · до 2026-09-04 18:00",
		`/host/fest/tbilisi/slot/7/voting/ballot/5`,
		"Отклонить",
		"Список турниров заморожен",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	// A frozen poll no longer offers to change its candidates.
	if strings.Contains(body, `name="extra_candidate"`) {
		t.Error("a frozen poll must not take new candidates")
	}
}

func TestVoteDocSaysWhatEachStateAllows(t *testing.T) {
	base := VotePage{Voting: samplePoll(), VenueTitle: "Площадка", VenueRef: "tbilisi", SlotDate: "2026-09-04 19:00"}

	early := base
	early.State = venues.RegScheduled
	early.Voting.OpensAt = "2026-09-04 10:00"
	if body := renderPublic(t, VoteDoc(early)); !strings.Contains(body, "Голосование откроется 2026-09-04 10:00.") {
		t.Error("a scheduled poll says when it opens")
	}

	anon := base
	anon.LoginHref = "/login?next=%2Fvote%2Fvtok"
	if body := renderPublic(t, VoteDoc(anon)); !strings.Contains(body, `href="/login?next=%2Fvote%2Fvtok"`) {
		t.Error("an anonymous voter is sent to the handshake")
	}

	// Ranked: three ordered selects, prefilled from the voter's own ballot.
	open := base
	open.LoggedIn = true
	open.Ballot = &venues.Ballot{Choice: []int64{2, 1}}
	body := renderPublic(t, VoteDoc(open))
	for _, want := range []string{"Первое место", "Второе место", "Третье место", "Изменить голос"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Count(body, "<select") != 3 {
		t.Errorf("ranked wants three selects, got %d", strings.Count(body, "<select"))
	}

	// One: radios, and a team name field in per-team mode.
	single := open
	single.Voting.Kind = venues.KindOne
	single.Voting.PerTeam = true
	body = renderPublic(t, VoteDoc(single))
	if !strings.Contains(body, `type="radio"`) || !strings.Contains(body, `name="team_name"`) {
		t.Error("a per-team single-choice ballot wants radios and a team name")
	}

	// Any: checkboxes.
	multi := open
	multi.Voting.Kind = venues.KindAny
	if body := renderPublic(t, VoteDoc(multi)); !strings.Contains(body, `type="checkbox"`) {
		t.Error("an any-choice ballot wants checkboxes")
	}

	// Closed: the tally, to anyone holding the link.
	closed := base
	closed.State = venues.RegClosed
	closed.Tally = venues.Tally(base.Voting, []venues.Ballot{{Choice: []int64{1}}})
	body = renderPublic(t, VoteDoc(closed))
	if !strings.Contains(body, "Итог") || strings.Contains(body, "Проголосовать") {
		t.Error("a closed poll shows the tally, not the ballot")
	}
}
