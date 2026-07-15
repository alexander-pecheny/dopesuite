package games

import (
	"fmt"

	"dope/dope/storage/store"
)

// Round-robin group machinery, shared by any game type that opens with groups
// where every team plays every other in a head-to-head бой (брейн today, more
// later). It carries no game-type knowledge: a caller asks for a round-robin
// stage and supplies the head-to-head scoring rule separately.

// CrossTableLayout is the stages.config layout marker that flags a stage as a
// round-robin group rendered as a cross-table (rows × opponents grid + О/+/−/М).
// It drives per-question theme provisioning and cross-table scoring off the
// stage config rather than the game code, so the machinery stays reusable.
const CrossTableLayout = "crosstable"

// GroupMatches is the round-robin schedule per group size: a list of rounds,
// each round a list of position pairs (1-based) that play in that round. Retained
// verbatim from the KINSBF generator so a group's бои cover every pair exactly
// once. Positions index into the group's teams; a пара [a,b] means "position a
// plays position b".
var GroupMatches = map[int][][][2]int{
	4: {{{1, 2}, {3, 4}}, {{1, 4}, {2, 3}}, {{1, 3}, {2, 4}}},
	3: {{{1, 2}}, {{1, 3}}, {{2, 3}}},
	2: {{{1, 2}}},
}

// GroupLabel maps a 1-based group index to its letter label (1→A, 2→B, …),
// wrapping past Z with a numeric suffix so it never collapses.
func GroupLabel(index int) string {
	if index >= 1 && index <= 26 {
		return string(rune('A' + index - 1))
	}
	return fmt.Sprintf("G%d", index)
}

// RoundRobinStage builds one group's scheme stage: a round-robin of head-to-head
// бои whose slots are flat seed references, so the fest-wide seed draw (which
// numbers teams sequentially in basket 1) fills them directly. groupIndex is
// 1-based (drives the label and venue default); teamCount is the group size (2–4);
// nGroups is how many groups share the draw; venueNumber is the площадка; questions
// is the бой length. A group's position p takes seed number (p-1)*nGroups+groupIndex
// — i.e. the p-th seed tier is distributed one-per-group, balancing groups the way
// the KINSBF basket draw does.
func RoundRobinStage(groupIndex, teamCount, nGroups, venueNumber, questions int) store.SchemeStage {
	label := GroupLabel(groupIndex)
	seedAt := func(position int) *store.SchemeSeedRef {
		return &store.SchemeSeedRef{Number: (position-1)*nGroups + groupIndex}
	}
	schedule := GroupMatches[teamCount]
	var matches []store.SchemeMatch
	boutSeq := 0
	for _, round := range schedule {
		for _, pair := range round {
			boutSeq++
			a, b := pair[0], pair[1]
			matches = append(matches, store.SchemeMatch{
				Code:             fmt.Sprintf("g%s-%d", label, boutSeq),
				Title:            fmt.Sprintf("Бой %s%d", label, boutSeq),
				Venue:            venueNumber,
				ParticipantCount: 2,
				Slots: []store.SchemeSlot{
					{Seed: seedAt(a), Label: fmt.Sprintf("%s%d", label, a)},
					{Seed: seedAt(b), Label: fmt.Sprintf("%s%d", label, b)},
				},
			})
		}
	}
	return store.SchemeStage{
		Code:      "g" + label,
		Title:     fmt.Sprintf("Группа %s (пл. %d)", label, venueNumber),
		StageType: "matches",
		Position:  groupIndex,
		Matches:   matches,
		Config:    []byte(mustJSON(map[string]any{"layout": CrossTableLayout, "questions": questions})),
	}
}

// HeadToHead is one finished-or-pending бой between two group positions, carrying
// the questions each side took. Positions are 0-based indices into the group.
type HeadToHead struct {
	A, B     int
	TakenA   int
	TakenB   int
	Finished bool
}

// StandingRow is one team's row in a group cross-table standings: О (points),
// + (taken), − (conceded), +/− (diff) and М (place, ties sharing a rank).
type StandingRow struct {
	Team     int
	Points   int
	Taken    int
	Conceded int
	Diff     int
	Place    int
}

// CrossTableStandings aggregates a group's бои into ranked standings, parameterised
// by the head-to-head points rule (брейн passes BrainMatchPoints). Only finished
// бои contribute points; sort is О desc, then +/− desc, then + desc, then team
// index. Ties share a place. Mirrors the KINSBF add_group group_stats.
func CrossTableStandings(teamCount int, bouts []HeadToHead, points func(takenA, takenB int) (int, int)) []StandingRow {
	rows := make([]StandingRow, teamCount)
	for i := range rows {
		rows[i].Team = i
	}
	for _, bout := range bouts {
		if bout.A < 0 || bout.A >= teamCount || bout.B < 0 || bout.B >= teamCount {
			continue
		}
		rows[bout.A].Taken += bout.TakenA
		rows[bout.A].Conceded += bout.TakenB
		rows[bout.B].Taken += bout.TakenB
		rows[bout.B].Conceded += bout.TakenA
		if bout.Finished {
			pa, pb := points(bout.TakenA, bout.TakenB)
			rows[bout.A].Points += pa
			rows[bout.B].Points += pb
		}
	}
	for i := range rows {
		rows[i].Diff = rows[i].Taken - rows[i].Conceded
	}
	order := make([]int, teamCount)
	for i := range order {
		order[i] = i
	}
	less := func(x, y StandingRow) bool {
		if x.Points != y.Points {
			return x.Points > y.Points
		}
		if x.Diff != y.Diff {
			return x.Diff > y.Diff
		}
		if x.Taken != y.Taken {
			return x.Taken > y.Taken
		}
		return x.Team < y.Team
	}
	sortStandings(rows, order, less)
	ranked := make([]StandingRow, teamCount)
	for rank, idx := range order {
		ranked[rank] = rows[idx]
	}
	for i := 0; i < len(ranked); {
		j := i
		for j+1 < len(ranked) && ranked[j+1].Points == ranked[i].Points &&
			ranked[j+1].Diff == ranked[i].Diff && ranked[j+1].Taken == ranked[i].Taken {
			j++
		}
		for k := i; k <= j; k++ {
			ranked[k].Place = i + 1
		}
		i = j + 1
	}
	return ranked
}

func sortStandings(rows []StandingRow, order []int, less func(x, y StandingRow) bool) {
	for i := 1; i < len(order); i++ {
		for j := i; j > 0 && less(rows[order[j]], rows[order[j-1]]); j-- {
			order[j], order[j-1] = order[j-1], order[j]
		}
	}
}
