package structure

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"dope/dope/storage/store"
)

func init() { Register(pod{}) }

// pod is a Group of a double elimination — a table of four playing until two
// have lost twice — as a Ranker only: the DSL expands its бои, since a pod is
// hand-drawn into rounds. It ranks the never-eliminated first (fewest Losses),
// then the eliminated by how late their second Loss came; a tie the бои did
// not settle shares its place, and survivors of an unfinished pod stay
// unplaced — their места are still being played. Only an outright lost бой
// counts: a shared place is a tie, not a Loss.
type pod struct{}

func (pod) Code() string { return "de" }
func (pod) Word() string { return "double_elimination" }
func (pod) Keys() []Key {
	return []Key{{Name: "groups"}, {Name: "group_size"}, {Name: "participants"}, {Name: "match_size"}, {Name: "winning_places"}}
}

// Expand is an elimination where two Losses end a tournament —
// КИнСБФ's pods of four and личная СИ's whole play-off are the same Kind, told
// apart only by their size and by how many Participants leave the block.
//
// Pods (groups > 1) stay one stage each: a pod's five бои run in sequence at
// its own стол, so the pod is the unit a host works with. A single bracket
// emits one stage per round, because a round is what plays at once across every
// стол — and, when the block reseeds, what gets re-ranked between rounds.
func (pod) Expand(b Block) (Outputs, error) {
	winning := 1
	if v, ok := b.Int("winning_places"); ok {
		winning = v
	}
	size := 2
	if v, ok := b.Int("match_size"); ok {
		size = v
	}
	groups, hasGroups := b.Int("groups")
	perGroup, hasSize := b.Int("group_size")
	teams, hasTeams := b.Int("participants")
	switch {
	case hasGroups && hasSize:
	case hasGroups && hasTeams:
		perGroup = teams / groups
	case hasSize && hasTeams:
		groups = teams / perGroup
	case hasTeams && size == 2 && winning == 1:
		// The classic pod: four to a group unless the scheme says otherwise.
		if teams%4 != 0 {
			return Outputs{}, errors.New("double_elimination: нужен groups (или participants, кратный 4)")
		}
		groups, perGroup = teams/4, 4
	case hasGroups && !hasTeams && !hasSize && size == 2 && winning == 1:
		perGroup = 4
	case hasTeams:
		groups, perGroup = 1, teams
	default:
		return Outputs{}, errors.New("double_elimination: нужен participants (или groups и group_size)")
	}
	if groups < 1 || perGroup < 2 {
		return Outputs{}, fmt.Errorf("double_elimination: %d групп по %d — так не бывает", groups, perGroup)
	}
	proceeding := 1
	if v, ok := b.Int("proceeding_participants"); ok {
		proceeding = v
	}
	// A пересев hands the block a ranking, so its opening round deals that
	// ranking as a snake like every later round does. Only the deterministic
	// template arrives pre-balanced, and slicing it in order is the point.
	opening := snakeChunks
	if reseeded, _ := b.Reseed(); !b.First() && !reseeded {
		opening = straightChunks
	}
	plan, err := planLivesDrawn(perGroup, 2, winning, proceeding,
		func(round, members int) int { return size }, opening)
	if err != nil {
		return Outputs{}, fmt.Errorf("double_elimination: %s", err)
	}
	roundNames := make([]string, len(plan.rounds))
	for i := range plan.rounds {
		roundNames[i] = fmt.Sprintf("r%d", i+1)
	}
	if err := b.Rounds(roundNames); err != nil {
		return Outputs{}, err
	}
	if _, round := b.Reseed(); round != "" {
		return Outputs{}, Keyf("reseed", "reseed: в этом блоке нет раунда %s — только true/false", round)
	}
	lanes, err := b.Venues(roundNames...)
	if err != nil {
		return Outputs{}, err
	}
	entrants, err := b.Entrants(groups, perGroup)
	if err != nil {
		return Outputs{}, err
	}
	reranked, _ := b.Bool("reseed")

	out := Outputs{Proceeding: proceeding}
	for g := 1; g <= groups; g++ {
		codes, stages, err := emitLivesBracket(b, g, groups, plan, entrants[g-1], lanes, reranked)
		if err != nil {
			return Outputs{}, err
		}
		label := fmt.Sprintf("DE %d", g)
		if groups == 1 {
			label = b.Title("Плей-офф")
		}
		place := func(p int) store.SchemeSlot {
			if p < 1 || p > len(plan.survivor) {
				return store.SchemeSlot{}
			}
			source := plan.survivor[p-1]
			return FromMatch(codes[source.bout], source.place)
		}
		out.Groups = append(out.Groups, Feed{Stage: stages[len(stages)-1], Label: label, Place: place})
	}
	return out, nil
}

// emitLivesBracket writes one bracket's stages and returns each planned бой's
// match code plus the stage codes, newest last.
func emitLivesBracket(b Block, group, groups int, plan *dePlan, entrants []store.SchemeSlot, lanes Lanes, reranked bool) ([]string, []string, error) {
	blockCode := b.Code()
	stageCode := fmt.Sprintf("%s-g%d", blockCode, group)
	if groups == 1 {
		stageCode = blockCode
	}
	codes := make([]string, len(plan.bouts))
	var stages []string
	seq := 0
	var prevStages []string
	for r, round := range plan.rounds {
		var reseedCode string
		if reranked && r > 0 {
			sources, bands := roundEntrantBands(plan, r)
			alive := make([]store.SchemeSlot, 0, len(sources))
			for _, source := range sources {
				alive = append(alive, FromMatch(codes[source.bout], source.place))
			}
			var err error
			// The code is per bracket, not per block: several groups re-ranking
			// the same round would otherwise all claim `s1-r2-reseed` and the
			// insert would die on unique(game_id, code).
			roundReseed := fmt.Sprintf("%s-r%d-reseed", stageCode, r+1)
			if reseedCode, err = b.EmitReseed(roundReseed, At{Group: GroupCode(groups, group)}, alive, bands, prevStages); err != nil {
				return nil, nil, err
			}
		}
		var matches []store.SchemeMatch
		roundStage := stageCode
		if groups == 1 {
			roundStage = fmt.Sprintf("%s-r%d", blockCode, r+1)
		}
		for i, boutIndex := range round {
			seq++
			code := fmt.Sprintf("%s-m%d", roundStage, i+1)
			if groups > 1 {
				code = fmt.Sprintf("%s-m%d", stageCode, seq)
			}
			codes[boutIndex] = code
			bout := plan.bouts[boutIndex]
			slots := make([]store.SchemeSlot, 0, len(bout.sources))
			for _, source := range bout.sources {
				switch {
				case reseedCode != "":
					slots = append(slots, ReseedRank(reseedCode, source.rank))
				case source.entrant != 0:
					slots = append(slots, entrants[source.entrant-1])
				default:
					slots = append(slots, FromMatch(codes[source.bout], source.place))
				}
			}
			title := fmt.Sprintf("Бой %d", seq)
			if groups == 1 {
				title = fmt.Sprintf("Бой %d", i+1)
			}
			matches = append(matches, store.SchemeMatch{
				Code:             code,
				Title:            title,
				Venue:            lanes.Pick(boutIndex + 1),
				ParticipantCount: len(slots),
				Slots:            slots,
			})
		}
		if groups > 1 {
			if r < len(plan.rounds)-1 {
				continue // pods emit once, below
			}
			// A pod plays all its rounds at one стол, so its stage spans them:
			// each бой carries the round it belongs to, the stage carries none.
			roundOf := boutRounds(plan)
			all := make([]store.SchemeMatch, 0, len(plan.bouts))
			for _, boutIndex := range flatBouts(plan) {
				match := podMatch(plan, codes, boutIndex, entrants, lanes.Pick(group))
				match.Round = roundOf[boutIndex]
				all = append(all, match)
			}
			if _, err := b.Emit(Stage{Code: stageCode, Title: fmt.Sprintf("DE %d", group), Kind: "de",
				Config: PodConfig{Lives: plan.lives, WinningPlaces: plan.winning}, At: At{Group: GroupCode(groups, group)}, Matches: all}); err != nil {
				return nil, nil, err
			}
			return codes, []string{stageCode}, nil
		}
		names := []string{fmt.Sprintf("r%d", r+1)}
		if _, err := b.Emit(Stage{Code: roundStage, Title: b.RoundTitle(names, fmt.Sprintf("Раунд %d", r+1)), Kind: "matches",
			Rounds: names, At: At{Round: r + 1}, Matches: matches}); err != nil {
			return nil, nil, err
		}
		prevStages = []string{roundStage}
		stages = append(stages, roundStage)
	}
	return codes, stages, nil
}

// roundEntrantBands lists a round's entrants and, alongside, the band each is
// ranked in. The reseed ranks inside a band and never across, so a band is
// everything the ordering settles before a single metric is read.
//
// Two things settle it, and both are the model's convention rather than
// anything the players did (planLives). Fewer Losses first: a Participant on one
// Loss never outranks one on none. Then, inside a bracket, whoever has just
// dropped into it outranks whoever was already there — they arrive with the
// better record, having lost later.
func roundEntrantBands(plan *dePlan, round int) ([]deSource, []int) {
	// Everyone still in, not only everyone who plays. A bracket already down to
	// its winning places sits the round out, and it keeps its ranks while it
	// waits — the бои of this round are numbered around them, so a reseed that
	// skipped them would hand every later rank to the wrong person.
	return plan.alive[round], denseBands(plan.aliveBands[round])
}

// denseBands numbers the distinct (arriving, departing) pairs from best to
// worst, so the resolver only has to know that a lower band ranks first.
func denseBands(pairs [][2]int) []int {
	distinct := map[[2]int]bool{}
	for _, pair := range pairs {
		distinct[pair] = true
	}
	ordered := make([][2]int, 0, len(distinct))
	for pair := range distinct {
		ordered = append(ordered, pair)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i][0] != ordered[j][0] {
			return ordered[i][0] < ordered[j][0]
		}
		return ordered[i][1] < ordered[j][1]
	})
	index := make(map[[2]int]int, len(ordered))
	for i, pair := range ordered {
		index[pair] = i
	}
	bands := make([]int, len(pairs))
	for i, pair := range pairs {
		bands[i] = index[pair]
	}
	return bands
}

func flatBouts(plan *dePlan) []int {
	var out []int
	for _, round := range plan.rounds {
		out = append(out, round...)
	}
	return out
}

func boutRounds(plan *dePlan) map[int]int {
	out := make(map[int]int, len(plan.bouts))
	for r, round := range plan.rounds {
		for _, boutIndex := range round {
			out[boutIndex] = r + 1
		}
	}
	return out
}

func podMatch(plan *dePlan, codes []string, boutIndex int, entrants []store.SchemeSlot, venue int) store.SchemeMatch {
	bout := plan.bouts[boutIndex]
	slots := make([]store.SchemeSlot, 0, len(bout.sources))
	for _, source := range bout.sources {
		if source.entrant != 0 {
			slots = append(slots, entrants[source.entrant-1])
			continue
		}
		slots = append(slots, FromMatch(codes[source.bout], source.place))
	}
	return store.SchemeMatch{
		Code:             codes[boutIndex],
		Title:            fmt.Sprintf("Бой %d", boutIndex+1),
		Venue:            venue,
		ParticipantCount: len(slots),
		Slots:            slots,
	}
}

// A pod's table shows М alone; Losses are how it ranks, not a column.
func (pod) Order(cfg json.RawMessage) []SortRule { return nil }

func (pod) Metrics() []string { return []string{"losses"} }

func (pod) Standings(cfg json.RawMessage, results []MatchOutcome, _ Inputs) ([]RankedEntry, error) {
	var conf PodConfig
	if err := json.Unmarshal(cfg, &conf); err != nil {
		return nil, fmt.Errorf("de config: %w", err)
	}
	if conf.Lives < 1 {
		conf.Lives = 2
	}
	if conf.WinningPlaces < 1 {
		conf.WinningPlaces = 1
	}
	losses := map[int64]int{}
	eliminated := map[int64]int{}
	var order []int64
	seen := map[int64]bool{}
	allFinished := len(results) > 0
	for _, match := range results {
		for _, slot := range match.Slots {
			if slot.Participant != 0 && !seen[slot.Participant] {
				seen[slot.Participant] = true
				order = append(order, slot.Participant)
			}
		}
		if !match.Finished {
			allFinished = false
			continue
		}
		for _, slot := range match.Slots {
			if slot.Participant == 0 {
				continue
			}
			lost := slot.Place > float64(conf.WinningPlaces) && slot.Place == float64(int(slot.Place))
			if !lost {
				continue
			}
			losses[slot.Participant]++
			if losses[slot.Participant] == conf.Lives {
				if _, out := eliminated[slot.Participant]; !out {
					eliminated[slot.Participant] = match.Round
				}
			}
		}
	}
	// Rank key: alive before eliminated, fewer Losses first, later elimination
	// first. Equal keys are ties the pod never split — they share a place.
	key := func(id int64) int {
		if round, out := eliminated[id]; out {
			return 1000 - round
		}
		return losses[id] - 1000
	}
	ranked := make([]int64, len(order))
	copy(ranked, order)
	sort.SliceStable(ranked, func(i, j int) bool { return key(ranked[i]) < key(ranked[j]) })
	out := make([]RankedEntry, 0, len(ranked))
	for i, id := range ranked {
		entry := RankedEntry{Participant: id, Metrics: map[string]float64{"losses": float64(losses[id])}}
		if _, placed := eliminated[id]; placed || allFinished {
			if i > 0 && key(ranked[i-1]) == key(id) {
				entry.Rank = out[i-1].Rank
			} else {
				entry.Rank = i + 1
			}
		}
		out = append(out, entry)
	}
	return out, nil
}
