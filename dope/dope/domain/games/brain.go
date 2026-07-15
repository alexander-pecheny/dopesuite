package games

import (
	"fmt"
	"strings"

	"dope/dope/storage/store"
)

// Брейн (brain-ring / КИНСБФ) domain logic. Brain rides the EK relational route
// (venues/stages/matches/slots/reseed); its only game-specific pieces are the
// scheme it generates and the head-to-head 1/0 бой scoring. Everything else —
// the round-robin schedule, the cross-table — is the reusable machinery in
// roundrobin.go.

// BrainGenerateScheme builds a брейн group-stage scheme: one venue and one
// round-robin group per group index, sized teamsPerGroup, with questions-per-бой
// длиной. Teams arrive via the fest-wide seed draw (жеребьёвка) into the seed
// slots the groups reference. Later phases append advancement stages (reseed →
// next group stage → DE → finals).
func BrainGenerateScheme(slug, title string, nGroups, teamsPerGroup, questions int) store.FestScheme {
	venues := make([]store.SchemeVenue, 0, nGroups)
	stages := make([]store.SchemeStage, 0, nGroups)
	for g := 1; g <= nGroups; g++ {
		venues = append(venues, store.SchemeVenue{Number: g, Title: fmt.Sprintf("Площадка %d", g)})
		stages = append(stages, RoundRobinStage(g, teamsPerGroup, nGroups, g, questions))
	}
	return store.FestScheme{
		SchemaVersion: 2,
		Slug:          slug,
		Title:         strings.TrimSpace(title),
		GameType:      BRAIN,
		Venues:        venues,
		Stages:        stages,
	}
}

// BrainMatchPoints is the head-to-head бой scoring: the side taking more questions
// gets 2 group points (О), a tie gives each 1, the loser 0. Delegates to the
// store rule so the standings helper and the DB recompute can't drift. Passed
// into CrossTableStandings.
func BrainMatchPoints(takenA, takenB int) (int, int) {
	return store.BrainMatchPoints(takenA, takenB)
}
