package resolver

import (
	"context"
	"database/sql"
	"strings"

	"dope/dope/domain/structure"
	"dope/dope/storage/store"
)

// Sources answers what advancement asks of the rest of the game: who took a
// place in a finished бой, who holds a rank in a source stage's table. The DB
// is one adapter; the tests' map is the other — the rules below never see SQL.
type Sources interface {
	TeamAtMatchPlace(matchCode string, place int) (int64, error)
	TeamAtReseedRank(stageCode string, rank int) (int64, error)
}

// desiredOccupant is the team a from_match/reseed slot should hold now, or 0
// while its source is not final.
func desiredOccupant(src Sources, ref store.SlotRef) (int64, error) {
	switch ref.Type {
	case store.SlotFromMatch:
		return src.TeamAtMatchPlace(ref.Match, ref.Place)
	case store.SlotReseed:
		return src.TeamAtReseedRank(ref.Stage, ref.Rank)
	}
	return 0, nil
}

// slotTransition is the non-destructive rule for a resolved slot. desired 0
// means the source is temporarily un-final (a finished бой unticked for
// editing): HOLD the occupant, so untick→edit→retick loses nothing. A
// different team moves in and, if someone sat there, the бой reopens so its
// standings are re-reviewed — the previous occupant's protocol rows stay.
func slotTransition(current, desired int64) (move, reopen bool) {
	if desired == 0 || desired == current {
		return false, false
	}
	return true, current != 0
}

// eligibleTeam resolves one advancing-team selector — a from_match place or a
// source stage's rank — returning the source's code so the caller can name what
// still blocks it. 0 while the upstream is not final; "" when the slot has no
// selector at all.
func eligibleTeam(src Sources, slot store.SchemeSlot) (int64, string, error) {
	switch {
	case slot.FromMatch != nil:
		teamID, err := src.TeamAtMatchPlace(slot.FromMatch.Match, slot.FromMatch.Place)
		return teamID, slot.FromMatch.Match, err
	case slot.Reseed != nil:
		teamID, err := src.TeamAtReseedRank(slot.Reseed.Stage, slot.Reseed.Rank)
		return teamID, slot.Reseed.Stage, err
	}
	return 0, "", nil
}

// Bout is a source бой as the readiness rule sees it.
type Bout struct {
	ID     int64
	Code   string
	Status string
}

type reseedPrerequisiteState struct {
	Ready          bool
	SourceMatchIDs []int64
	PendingMatches []string
}

// prerequisites is the readiness rule: a reseed can be calculated once every
// source бой is finished and every advancing selector resolves. A stage with
// no advancing selector is never ready.
func prerequisites(src Sources, cfg store.StageConfig, bouts []Bout) (reseedPrerequisiteState, error) {
	var state reseedPrerequisiteState
	for _, b := range bouts {
		state.SourceMatchIDs = append(state.SourceMatchIDs, b.ID)
		if b.Status != "finished" {
			state.addPending(b.Code)
		}
	}
	advancing := 0
	for _, slot := range cfg.Teams {
		teamID, source, err := eligibleTeam(src, slot)
		if err != nil {
			return state, err
		}
		if source == "" {
			continue
		}
		if teamID == 0 {
			state.addPending(source)
		}
		advancing++
	}
	if advancing == 0 {
		return state, nil
	}
	state.Ready = len(state.SourceMatchIDs) > 0 && len(state.PendingMatches) == 0
	return state, nil
}

func (state *reseedPrerequisiteState) addPending(code string) {
	code = strings.TrimSpace(code)
	if code == "" {
		return
	}
	for _, existing := range state.PendingMatches {
		if existing == code {
			return
		}
	}
	state.PendingMatches = append(state.PendingMatches, code)
}

// contenders names who a reseed ranks — each advancing selector's team with
// its band — or ok=false while some selector's source is not final.
func contenders(src Sources, cfg store.StageConfig) (_ []structure.Contender, ok bool, err error) {
	out := make([]structure.Contender, 0, len(cfg.Teams))
	for index, slot := range cfg.Teams {
		teamID, source, err := eligibleTeam(src, slot)
		if err != nil {
			return nil, false, err
		}
		if source == "" {
			continue
		}
		if teamID == 0 {
			return nil, false, nil
		}
		band := 0
		if index < len(cfg.Bands) {
			band = cfg.Bands[index]
		}
		out = append(out, structure.Contender{Participant: teamID, Band: band})
	}
	return out, len(out) > 0, nil
}

// dbSources is Sources over the game's tables.
type dbSources struct {
	ctx    context.Context
	q      store.Queryer
	gameID int64
}

// TeamAtMatchPlace returns the team that took the given place in a bout, but
// only once that bout is finished — provisional standings must not leak
// downstream. Returns 0 when unresolved.
func (d dbSources) TeamAtMatchPlace(matchCode string, place int) (int64, error) {
	if matchCode == "" || place <= 0 {
		return 0, nil
	}
	var teamID int64
	err := d.q.QueryRowContext(d.ctx, `
select mr.participant_id
from match_results mr
join matches m on m.id = mr.match_id
where m.game_id = ? and m.code = ? and m.status = 'finished' and mr.place = ?`,
		d.gameID, matchCode, float64(place)).Scan(&teamID)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return teamID, err
}

// TeamAtReseedRank returns the team at a source stage's rank, or 0 when the
// table is not there yet. Kind-ranked stages (rr groups, brackets) recompute
// standings live, so a rank ref must not leak provisional order downstream:
// it resolves only once every bout of the source stage is finished. Reseed
// stages keep their explicit-calculate gate (standings exist only when
// calculated).
func (d dbSources) TeamAtReseedRank(stageCode string, rank int) (int64, error) {
	if stageCode == "" || rank <= 0 {
		return 0, nil
	}
	var stageID int64
	var kind string
	err := d.q.QueryRowContext(d.ctx, `
select id, kind from stages where game_id = ? and code = ?`, d.gameID, stageCode).Scan(&stageID, &kind)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if isRankedKind(kind) {
		var unfinished int
		if err := d.q.QueryRowContext(d.ctx, `
select count(*) from matches where stage_id = ? and status != 'finished'`, stageID).Scan(&unfinished); err != nil {
			return 0, err
		}
		if unfinished > 0 {
			return 0, nil
		}
	}
	var teamID int64
	err = d.q.QueryRowContext(d.ctx, `
select participant_id from stage_standings where stage_id = ? and rank = ?`,
		stageID, rank).Scan(&teamID)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return teamID, err
}
