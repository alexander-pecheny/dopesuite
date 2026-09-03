package tests

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"dope/dope/domain/venues"
)

func votingFixture(t *testing.T) (*sql.DB, venues.Slot, venues.Voting) {
	t.Helper()
	db := venueTestDB(t)
	_, slot := newVenueSlot(t, db, []int{2})
	candidates := []venues.Candidate{{ID: 1, Name: "Синхрон А"}, {ID: 2, Name: "Синхрон Б"}}
	if err := inTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return venues.SaveVotingTx(ctx, tx, slot.ID, venues.KindOne, true, "", "2026-09-04 18:00", candidates)
	}); err != nil {
		t.Fatal(err)
	}
	voting, err := venues.SlotVoting(t.Context(), db, slot.ID)
	if err != nil {
		t.Fatal(err)
	}
	return db, slot, voting
}

func TestSavingAFrozenVotingKeepsItsCandidates(t *testing.T) {
	db, slot, voting := votingFixture(t)
	voter := newVenueUser(t, db, "voter")
	if err := inTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return venues.CastBallotTx(ctx, tx, voting, voter, "Мантисса", []int64{1})
	}); err != nil {
		t.Fatal(err)
	}
	// The form of a frozen poll posts no candidates; the window still moves.
	if err := inTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return venues.SaveVotingTx(ctx, tx, slot.ID, venues.KindOne, true, "", "2026-09-05 12:00", nil)
	}); err != nil {
		t.Fatalf("saving a frozen voting: %v", err)
	}
	saved, err := venues.SlotVoting(t.Context(), db, slot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !saved.Frozen || len(saved.Candidates) != 2 {
		t.Fatalf("candidates %+v frozen=%v", saved.Candidates, saved.Frozen)
	}
	if saved.ClosesAt != "2026-09-05 12:00" {
		t.Fatalf("saved %+v", saved)
	}
	// A ballot means something different under another kind, so neither the
	// kind nor per-team mode may move once one has been cast.
	for _, change := range []struct {
		kind    string
		perTeam bool
	}{{venues.KindAny, true}, {venues.KindOne, false}} {
		err := inTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
			return venues.SaveVotingTx(ctx, tx, slot.ID, change.kind, change.perTeam, "", "", nil)
		})
		if !errors.Is(err, venues.ErrVotingFrozen) {
			t.Fatalf("%+v: err = %v, want ErrVotingFrozen", change, err)
		}
	}
	// A poll that never took a ballot still needs candidates.
	if err := inTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
		_, fresh := newVenueSlot(t, db, []int{2})
		return venues.SaveVotingTx(ctx, tx, fresh.ID, venues.KindOne, false, "", "", nil)
	}); err == nil {
		t.Fatal("a new voting with no candidates must be refused")
	}
}

// Per-team mode counts a team once, and the ballot it counts is the latest —
// a re-vote after someone else voted, not the one filed first.
func TestPerTeamTallyFollowsTheLatestBallot(t *testing.T) {
	db, _, voting := votingFixture(t)
	alice := newVenueUser(t, db, "alice")
	bob := newVenueUser(t, db, "bob")
	cast := func(user int64, team string, choice int64) {
		t.Helper()
		if err := inTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
			return venues.CastBallotTx(ctx, tx, voting, user, team, []int64{choice})
		}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	cast(alice, "Мантисса", 1)
	cast(bob, "Мантисса", 2)
	cast(alice, "Мантисса", 1)

	ballots, err := venues.VotingBallots(t.Context(), db, voting.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ballots) != 2 || ballots[len(ballots)-1].UserID != alice {
		t.Fatalf("ballots %+v — the re-vote must sort last", ballots)
	}
	tally := venues.Tally(voting, ballots)
	scores := map[int64]int{}
	for _, row := range tally {
		scores[row.Candidate.ID] = row.Score
	}
	if scores[1] != 1 || scores[2] != 0 {
		t.Fatalf("tally %v — the team's latest ballot counts", scores)
	}
}

// "Decline" is final for that voter: re-voting does not bring the ballot back.
func TestADiscardedBallotStaysDiscardedAcrossARevote(t *testing.T) {
	db, _, voting := votingFixture(t)
	alice := newVenueUser(t, db, "alice")
	cast := func(choice int64) {
		t.Helper()
		if err := inTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
			return venues.CastBallotTx(ctx, tx, voting, alice, "Мантисса", []int64{choice})
		}); err != nil {
			t.Fatal(err)
		}
	}
	cast(1)
	ballots, err := venues.VotingBallots(t.Context(), db, voting.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := inTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return venues.DiscardBallotTx(ctx, tx, voting.ID, ballots[0].ID, true)
	}); err != nil {
		t.Fatal(err)
	}
	cast(2)
	ballots, err = venues.VotingBallots(t.Context(), db, voting.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ballots) != 1 || !ballots[0].Discarded {
		t.Fatalf("ballots %+v, want the discard to survive the re-vote", ballots)
	}
	for _, row := range venues.Tally(voting, ballots) {
		if row.Score != 0 {
			t.Fatalf("tally %+v, want a discarded ballot to count for nothing", row)
		}
	}
}
