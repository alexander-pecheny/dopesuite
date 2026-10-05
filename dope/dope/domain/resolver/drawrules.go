package resolver

import (
	"context"
	"database/sql"
	"slices"
	"strings"

	"dope/dope/storage/store"
)

// Who a Draw Slot may be filled with (CONTEXT.md, «Draw»). The rules are
// here, over DrawFacts; the database is one adapter of the facts
// (sqlDrawFacts), and a test gives them by hand.

// HeldPlace is a place a finished bout dealt: the bout, the place it goes
// forward from (store.AdvancePlaceSQL), and who holds it.
type HeldPlace struct {
	Match string
	Place float64
	ID    int64
	Name  string
}

// HeldRank is a rank in a stage's table, and who holds it.
type HeldRank struct {
	Stage string
	Rank  int
	ID    int64
	Name  string
}

// DrawFacts is what the Draw rules read of a Game.
type DrawFacts interface {
	// FinishedPlaces is the places the named bouts dealt, finished bouts only.
	FinishedPlaces(matches []string) ([]HeldPlace, error)
	// Ranks is the named stages' tables, and which of those stages are played
	// out: every bout in them finished.
	Ranks(stages []string) ([]HeldRank, map[string]bool, error)
	// Draws is every Draw the Game's Slots declare.
	Draws() ([]*store.SchemeDraw, error)
	// SeatedBesideDraws is who already sits in the bout's stage on a seat that
	// is not a Draw.
	SeatedBesideDraws(matchID int64) (map[int64]bool, error)
}

// DrawOptions is everyone a Draw Slot of the bout may be filled with: its
// candidates and, once they are known, its substitutes.
func DrawOptions(ctx context.Context, q store.Queryer, gameID, matchID int64, draw *store.SchemeDraw) (candidates, substitutes []store.DrawCandidateView, err error) {
	facts := sqlDrawFacts{ctx: ctx, q: q, gameID: gameID}
	if candidates, err = drawCandidates(facts, draw); err != nil || len(candidates) == 0 {
		return candidates, nil, err
	}
	substitutes, err = drawSubstitutes(facts, matchID, draw)
	return candidates, substitutes, err
}

// drawCandidates resolves a Draw's candidate places and ranks to whoever
// holds them now. A place in a bout that is not finished, or a rank in a table
// not yet played out, resolves to nobody: the Slot offers a choice only once
// the Round it draws from is over, the same gate every other advancing seat
// waits on.
func drawCandidates(facts DrawFacts, draw *store.SchemeDraw) ([]store.DrawCandidateView, error) {
	if draw == nil || len(draw.Candidates)+len(draw.Ranks) == 0 {
		return nil, nil
	}
	var out []store.DrawCandidateView
	seen := map[int64]bool{}
	add := func(id int64, name, source string) {
		if !seen[id] {
			seen[id] = true
			out = append(out, store.DrawCandidateView{ID: id, Name: name, Source: source})
		}
	}
	if len(draw.Candidates) > 0 {
		var matches []string
		for _, candidate := range draw.Candidates {
			matches = appendOnce(matches, candidate.Match)
		}
		held, err := facts.FinishedPlaces(matches)
		if err != nil {
			return nil, err
		}
		for _, candidate := range draw.Candidates {
			for _, h := range held {
				if h.Match == candidate.Match && h.Place == float64(candidate.Place) {
					add(h.ID, h.Name, h.Match)
				}
			}
		}
	}
	if len(draw.Ranks) > 0 {
		ranks, played, err := facts.Ranks(drawStages(draw))
		if err != nil {
			return nil, err
		}
		for _, want := range draw.Ranks {
			for _, r := range ranks {
				if played[r.Stage] && r.Stage == want.Stage && r.Rank == want.Rank {
					add(r.ID, r.Name, r.Stage)
				}
			}
		}
	}
	return out, nil
}

// drawSubstitutes is whom an admin may seat in a Draw Slot in place of a team
// that drops out: the rest of the tables the Slot draws ranks from, ranked
// below every rank any Draw of this Game takes from that table (so nobody who
// goes through on their own place), less anyone a seat of the Slot's stage
// already holds without a draw. A Draw by bout places (Hamsa's lot) has none.
func drawSubstitutes(facts DrawFacts, matchID int64, draw *store.SchemeDraw) ([]store.DrawCandidateView, error) {
	if draw == nil || len(draw.Ranks) == 0 {
		return nil, nil
	}
	draws, err := facts.Draws()
	if err != nil {
		return nil, err
	}
	drawn := map[string]int{}
	for _, d := range draws {
		for _, rank := range d.Ranks {
			drawn[rank.Stage] = max(drawn[rank.Stage], rank.Rank)
		}
	}
	stages := drawStages(draw)
	ranks, played, err := facts.Ranks(stages)
	if err != nil {
		return nil, err
	}
	seated, err := facts.SeatedBesideDraws(matchID)
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(ranks, func(a, b HeldRank) int { return a.Rank - b.Rank })
	var out []store.DrawCandidateView
	for _, stage := range stages {
		for _, r := range ranks {
			if r.Stage == stage && played[stage] && r.Rank > drawn[stage] && !seated[r.ID] {
				out = append(out, store.DrawCandidateView{ID: r.ID, Name: r.Name, Source: r.Stage})
			}
		}
	}
	return out, nil
}

// drawStages is the tables a Draw takes ranks from, in the order it names
// them.
func drawStages(draw *store.SchemeDraw) []string {
	var stages []string
	for _, rank := range draw.Ranks {
		stages = appendOnce(stages, rank.Stage)
	}
	return stages
}

func appendOnce(list []string, value string) []string {
	if slices.Contains(list, value) {
		return list
	}
	return append(list, value)
}

// sqlDrawFacts reads DrawFacts from the Game's rows.
type sqlDrawFacts struct {
	ctx    context.Context
	q      store.Queryer
	gameID int64
}

func (f sqlDrawFacts) FinishedPlaces(matches []string) ([]HeldPlace, error) {
	args := []any{f.gameID}
	for _, m := range matches {
		args = append(args, m)
	}
	return store.CollectRows(f.ctx, f.q, `
select m.code, `+store.AdvancePlaceSQL+`, mr.participant_id, coalesce(p.name, '')
from match_results mr
join matches m on m.id = mr.match_id
join participants p on p.id = mr.participant_id
where m.game_id = ? and m.status = 'finished' and m.code in (`+marks(len(matches))+`)`,
		args, func(rows *sql.Rows) (HeldPlace, error) {
			var h HeldPlace
			return h, rows.Scan(&h.Match, &h.Place, &h.ID, &h.Name)
		})
}

func (f sqlDrawFacts) Ranks(stages []string) ([]HeldRank, map[string]bool, error) {
	args := []any{f.gameID}
	for _, s := range stages {
		args = append(args, s)
	}
	ranks, err := store.CollectRows(f.ctx, f.q, `
select s.code, ss.rank, ss.participant_id, coalesce(p.name, '')
from stage_standings ss
join stages s on s.id = ss.stage_id
join participants p on p.id = ss.participant_id
where s.game_id = ? and s.code in (`+marks(len(stages))+`)`,
		args, func(rows *sql.Rows) (HeldRank, error) {
			var r HeldRank
			return r, rows.Scan(&r.Stage, &r.Rank, &r.ID, &r.Name)
		})
	if err != nil {
		return nil, nil, err
	}
	playedOut, err := store.CollectRows(f.ctx, f.q, `
select s.code from stages s
where s.game_id = ? and s.code in (`+marks(len(stages))+`)
  and exists (select 1 from matches m where m.stage_id = s.id)
  and not exists (select 1 from matches m where m.stage_id = s.id and m.status != 'finished')`,
		args, func(rows *sql.Rows) (string, error) {
			var code string
			return code, rows.Scan(&code)
		})
	if err != nil {
		return nil, nil, err
	}
	played := map[string]bool{}
	for _, code := range playedOut {
		played[code] = true
	}
	return ranks, played, nil
}

func (f sqlDrawFacts) Draws() ([]*store.SchemeDraw, error) {
	refs, err := store.CollectRows(f.ctx, f.q, `
select ms.source_ref_json from match_slots ms join matches m on m.id = ms.match_id
where m.game_id = ? and ms.source_type = ?`, []any{f.gameID, store.SlotPlaceholder}, func(rows *sql.Rows) (string, error) {
		var ref string
		return ref, rows.Scan(&ref)
	})
	if err != nil {
		return nil, err
	}
	var out []*store.SchemeDraw
	for _, ref := range refs {
		if draw := store.ParseSlotRef(store.SlotPlaceholder, ref).Draw; draw != nil {
			out = append(out, draw)
		}
	}
	return out, nil
}

func (f sqlDrawFacts) SeatedBesideDraws(matchID int64) (map[int64]bool, error) {
	ids, err := store.CollectRows(f.ctx, f.q, `
select ms.participant_id from match_slots ms join matches m on m.id = ms.match_id
where m.stage_id = (select stage_id from matches where id = ?)
  and ms.participant_id is not null and ms.source_type != ?`, []any{matchID, store.SlotPlaceholder},
		func(rows *sql.Rows) (int64, error) {
			var id int64
			return id, rows.Scan(&id)
		})
	if err != nil {
		return nil, err
	}
	out := make(map[int64]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

func marks(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}
