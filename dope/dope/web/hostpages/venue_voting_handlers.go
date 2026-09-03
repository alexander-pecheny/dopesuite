package hostpages

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"dope/dope/domain/venues"
	"dope/dope/web/pages"
	"dope/dope/web/route"
)

func (s *Server) loadVotingView(r *http.Request, slot venues.Slot) (VotingView, error) {
	view := VotingView{}
	voting, err := venues.SlotVoting(r.Context(), s.h.Engine().DB, slot.ID)
	if errors.Is(err, sql.ErrNoRows) {
		view.Candidates = s.playableCandidates(r.Context(), slot)
		return view, nil
	}
	if err != nil {
		return view, err
	}
	view.Voting = voting
	view.URL = publicURL(r, "/vote/"+voting.Token)
	view.KindLabel = KindLabel(voting.Kind)
	if view.Ballots, err = venues.VotingBallots(r.Context(), s.h.Engine().DB, voting.ID); err != nil {
		return view, err
	}
	view.Tally = venues.Tally(voting, view.Ballots)
	return view, nil
}

// playableCandidates are the tournaments buff knows to be playable at the
// Slot's time, sync tournaments first (buffdb orders them so).
func (s *Server) playableCandidates(ctx context.Context, slot venues.Slot) []venues.Candidate {
	at, ok := venues.ParseTime(slot.StartsAt)
	if !ok {
		return nil
	}
	found := s.h.Engine().BuffMirror().PlayableTournaments(ctx, at)
	out := make([]venues.Candidate, 0, len(found))
	for _, t := range found {
		out = append(out, venues.Candidate{ID: t.ID, Name: t.Name, Type: t.Type})
	}
	return out
}

func (s *Server) handleVotingSave(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	festID := sc.FestID
	_, slot, err := s.slotOf(r, sc)
	if err != nil {
		return err
	}
	if err := r.ParseForm(); err != nil {
		return route.BadRequest("bad form")
	}
	offered := map[int64]venues.Candidate{}
	for _, c := range s.playableCandidates(r.Context(), slot) {
		offered[c.ID] = c
	}
	if existing, err := venues.SlotVoting(r.Context(), s.h.Engine().DB, slot.ID); err == nil {
		for _, c := range existing.Candidates {
			offered[c.ID] = c
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var candidates []venues.Candidate
	for _, raw := range r.Form["candidate"] {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			continue
		}
		if c, ok := offered[id]; ok {
			candidates = append(candidates, c)
		}
	}
	if extra := formInt64(r.Form, "extra_candidate"); extra > 0 {
		candidates = append(candidates, s.candidateByID(r.Context(), extra))
	}
	err = s.h.Engine().WithWriteTx(r.Context(), festID, "slot-voting", func(ctx context.Context, tx *sql.Tx) error {
		return venues.SaveVotingTx(ctx, tx, slot.ID, r.Form.Get("kind"), r.Form.Get("per_team") == "1",
			r.Form.Get("opens_at"), r.Form.Get("closes_at"), candidates)
	})
	if err != nil {
		return s.renderSlotPage(w, r, sc, err.Error(), "")
	}
	return s.redirectToSlot(w, r, festID, slot.ID)
}

func (s *Server) candidateByID(ctx context.Context, id int64) venues.Candidate {
	if t, ok := s.h.Engine().BuffMirror().Tournament(ctx, id); ok {
		return venues.Candidate{ID: t.ID, Name: t.Name, Type: t.Type}
	}
	return venues.Candidate{ID: id, Name: strs.Venues.Voting.TournamentN(strconv.FormatInt(id, 10))}
}

func (s *Server) handleVotingBallot(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	festID := sc.FestID
	_, slot, err := s.slotOf(r, sc)
	if err != nil {
		return err
	}
	if err := r.ParseForm(); err != nil {
		return route.BadRequest("bad form")
	}
	voting, err := venues.SlotVoting(r.Context(), s.h.Engine().DB, slot.ID)
	if err != nil {
		return route.NotFound
	}
	ballotID, err := strconv.ParseInt(r.PathValue("ballot"), 10, 64)
	if err != nil {
		return route.NotFound
	}
	if err := s.h.Engine().WithWriteTx(r.Context(), festID, "slot-ballot-discard", func(ctx context.Context, tx *sql.Tx) error {
		return venues.DiscardBallotTx(ctx, tx, voting.ID, ballotID, r.Form.Get("discarded") == "1")
	}); err != nil {
		return err
	}
	return s.redirectToSlot(w, r, festID, slot.ID)
}

func (s *Server) renderVotePage(w http.ResponseWriter, r *http.Request, token, errMsg string) error {
	voting, err := venues.VotingByToken(r.Context(), s.h.Engine().DB, token)
	if err != nil {
		return route.NotFound
	}
	slot, err := venues.LoadSlot(r.Context(), s.h.Engine().DB, voting.SlotID)
	if err != nil {
		return route.NotFound
	}
	venue, err := venues.LoadVenue(r.Context(), s.h.Engine().DB, slot.FestID)
	if err != nil {
		return route.NotFound
	}
	now := time.Now().UTC()
	page := VotePage{
		Voting: voting, VenueTitle: venue.Title, VenueRef: venue.Ref(), SlotDate: slot.StartsAt,
		State:     votingState(voting, now),
		LoginHref: "/login?next=" + url.QueryEscape("/vote/"+voting.Token),
		Error:     errMsg,
	}
	if page.State == venues.RegClosed {
		ballots, err := venues.VotingBallots(r.Context(), s.h.Engine().DB, voting.ID)
		if err != nil {
			return err
		}
		page.Tally = venues.Tally(voting, ballots)
	}
	if user, ok := s.h.Engine().LookupSession(r); ok {
		page.LoggedIn = true
		if ballot, err := venues.UserBallot(r.Context(), s.h.Engine().DB, voting.ID, user.UserID); err == nil {
			page.Ballot = &ballot
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	pages.RenderDoc(w, s.h.Engine().AssetETags, VoteDoc(page))
	return nil
}

func votingState(v venues.Voting, now time.Time) venues.RegState {
	switch {
	case v.Closed(now):
		return venues.RegClosed
	case !v.Open(now):
		return venues.RegScheduled
	default:
		return venues.RegOpen
	}
}

func (s *Server) handleVoteSubmit(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	token := r.PathValue("token")
	voting, err := venues.VotingByToken(r.Context(), s.h.Engine().DB, token)
	if err != nil {
		return route.NotFound
	}
	slot, err := venues.LoadSlot(r.Context(), s.h.Engine().DB, voting.SlotID)
	if err != nil {
		return route.NotFound
	}
	if err := r.ParseForm(); err != nil {
		return route.BadRequest("bad form")
	}
	if !voting.Open(time.Now().UTC()) {
		return s.renderVotePage(w, r, token, strs.Venues.Voting.PageClosed())
	}
	var choice []int64
	for _, raw := range r.Form["choice"] {
		if id, err := strconv.ParseInt(raw, 10, 64); err == nil {
			choice = append(choice, id)
		}
	}
	if len(choice) == 0 {
		return s.renderVotePage(w, r, token, strs.Venues.Voting.ErrorPickOne())
	}
	if err := s.h.Engine().WithWriteTx(r.Context(), slot.FestID, "vote", func(ctx context.Context, tx *sql.Tx) error {
		return venues.CastBallotTx(ctx, tx, voting, sc.User.UserID, r.Form.Get("team_name"), choice)
	}); err != nil {
		return s.renderVotePage(w, r, token, err.Error())
	}
	http.Redirect(w, r, "/vote/"+token, http.StatusSeeOther)
	return nil
}
