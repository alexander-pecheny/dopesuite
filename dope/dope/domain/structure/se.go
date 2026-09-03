package structure

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	dopestrings "dope/i18nstrings"

	"dope/dope/storage/store"
)

func init() { Register(singleElim{}) }

// singleElim is the knockout kind: entrants (a power of two) are laid out in
// standard bracket order, each round's winners advance by fromMatch place-1
// refs, and an optional bronze Match seats the semifinal losers.
type singleElim struct{}

func (singleElim) Code() string { return "se" }
func (singleElim) Word() string { return "single_elimination" }
func (singleElim) Keys() []Key {
	return []Key{{Name: "participants"}, {Name: "match_size", Round: true}, {Name: "winning_places"}, {Name: "rounds"}, {Name: "bronze"}, {Name: "best_of", Round: true}, {Name: "rollout", Round: true}, {Name: "points", Cascade: true}, {Name: "metric"}}
}

func seRoundTitle(remaining int) string {
	s := dopestrings.Default
	switch remaining {
	case 2:
		return s.Structure.Titles.Final()
	case 4:
		return s.Structure.Se.RoundSemifinals()
	}
	return s.Structure.Se.RoundNth(strconv.Itoa(remaining / 2))
}

// Expand is the knockout: a bracket of Rounds, each round's winning places
// carried forward, with an optional reseed at a Round boundary, a bronze Match
// and a best-of series in the final.
func (singleElim) Expand(b Block) (Outputs, error) {
	s := dopestrings.Default
	participants, ok := b.Int("participants")
	if !ok {
		return Outputs{}, errors.New("single_elimination: нужен participants")
	}
	winning := 1
	if v, ok := b.Int("winning_places"); ok {
		winning = v
	}
	// match_size may differ round by round — EK plays its 1/4 three to a table
	// and everything else four — so the size is asked for per round.
	sizeFor := func(round, entering int) int {
		size := 2
		if v, ok := b.Int("match_size"); ok {
			size = v
		}
		if v, ok := b.Int(fmt.Sprintf("match_size.r%d", round)); ok {
			size = v
		}
		return size
	}
	maxRounds, _ := b.Int("rounds")
	plan, err := planElimRounds(participants, winning, maxRounds, sizeFor)
	if err != nil {
		return Outputs{}, fmt.Errorf("single_elimination: %s", err)
	}
	bronze, _ := b.Bool("bronze")
	directBronze, hasDirect := seDirectBronze(b, participants, bronze)
	rounds := []string{}
	for i, r := range plan {
		rounds = append(rounds, elimRoundNames(r, i, winning)...)
	}
	if bronze && (participants >= 4 || hasDirect) {
		rounds = append(rounds, "bronze")
	}
	if err := b.Rounds(rounds); err != nil {
		return Outputs{}, err
	}
	_, boundary := b.Reseed()
	everyRound := boundary == ReseedEveryRound
	boundaryAt := 0
	if boundary != "" && !everyRound {
		for i, r := range plan {
			for _, name := range elimRoundNames(r, i, winning) {
				if name == boundary {
					boundaryAt = r.entering
				}
			}
		}
		if boundaryAt == 0 {
			return Outputs{}, Keyf("reseed", "reseed: в этом блоке нет раунда %s", boundary)
		}
		if boundaryAt == participants {
			return Outputs{}, Keyf("reseed", "reseed: %s — первый раунд, пишите reseed: true", boundary)
		}
	}

	first, err := seFirstRound(b, plan[0], winning)
	if err != nil {
		return Outputs{}, err
	}
	blockCode := b.Code()
	prevCodes := []string{}
	var prevStages, roundStages []string
	var semifinalCodes []string
	seriesFinal := false
	for roundIndex, round := range plan {
		remaining, size, count := round.entering, round.size, round.bouts
		names := elimRoundNames(round, roundIndex, winning)
		stageCode := fmt.Sprintf("%s-%s", blockCode, names[0])
		bestOf := 0
		if round.terminal {
			v, ok := b.Int("best_of")
			for _, name := range names {
				if dotted, dok := b.Int("best_of." + name); dok {
					v, ok = dotted, true
				}
			}
			if ok {
				if v < 3 || v%2 == 0 {
					return Outputs{}, errors.New("best_of: серия играется до большинства побед — нечётное число боёв от 3")
				}
				bestOf = v
			}
		} else {
			for _, name := range names {
				if _, ok := b.Int("best_of." + name); ok {
					return Outputs{}, Keyf("best_of."+name, "best_of: серия возможна только в финале или матче за 3-е место")
				}
			}
		}
		var reseedCode string
		if (everyRound && roundIndex > 0) || remaining == boundaryAt {
			// Every place that survived the round before, best Match first —
			// the reseed's own sorting decides the rest.
			alive := make([]store.SchemeSlot, 0, len(prevCodes)*winning)
			for _, prev := range prevCodes {
				for place := 1; place <= winning; place++ {
					alive = append(alive, FromMatch(prev, place))
				}
			}
			code, where := blockCode+"-reseed", At{}
			if everyRound {
				code, where = fmt.Sprintf("%s-r%d-reseed", blockCode, roundIndex+1), At{Round: roundIndex + 1}
			}
			sources, err := b.Sources(prevStages, roundStages)
			if err != nil {
				return Outputs{}, err
			}
			if reseedCode, err = b.EmitReseed(code, where, alive, nil, sources); err != nil {
				return Outputs{}, err
			}
		}
		lanes, err := b.Venues(names...)
		if err != nil {
			return Outputs{}, err
		}
		matches := make([]store.SchemeMatch, count)
		codes := make([]string, count)
		// Who meets whom: a reseed re-ranks everyone and deals the Matches by
		// the draw, so the round's best meets its worst; without one the
		// bracket template carries each Match's winners forward in Match order.
		drawn := elimDraw(remaining, count, size, winning)
		template := straightChunks(remaining, count)
		for i := 1; i <= count; i++ {
			code := fmt.Sprintf("%s-m%d", stageCode, i)
			codes[i-1] = code
			var slots []store.SchemeSlot
			switch {
			case reseedCode != "":
				for _, rank := range drawn[i-1] {
					slots = append(slots, ReseedRank(reseedCode, rank))
				}
			case roundIndex == 0:
				slots = first[i-1]
			default:
				for _, rank := range template[i-1] {
					from, place := (rank-1)/winning, (rank-1)%winning+1
					slots = append(slots, LabelledFromMatch(prevCodes[from], s.Structure.Titles.Bout(strconv.Itoa(from+1)), place))
				}
			}
			matches[i-1] = store.SchemeMatch{
				Code:             code,
				Title:            s.Structure.Titles.Bout(strconv.Itoa(i)),
				Venue:            lanes.Pick(i),
				ParticipantCount: len(slots),
				Slots:            slots,
			}
		}
		// The bronze Match is played before the final, so its stage stands
		// before the final's: the grid draws it first, and it deals its letter
		// first.
		if round.terminal && bronze {
			var pair []store.SchemeSlot
			switch {
			case hasDirect:
				pair = directBronze
			case len(semifinalCodes) == 2:
				pair = []store.SchemeSlot{FromMatch(semifinalCodes[0], 2), FromMatch(semifinalCodes[1], 2)}
			}
			if pair != nil {
				if err := appendBronze(b, pair, roundIndex+1); err != nil {
					return Outputs{}, err
				}
			}
		}
		if bestOf > 1 {
			// The series is sequential Matches at one table, so it never
			// wave-splits.
			base := matches[0]
			series := make([]store.SchemeMatch, bestOf)
			for k := 1; k <= bestOf; k++ {
				series[k-1] = store.SchemeMatch{
					Code:             fmt.Sprintf("%s-m%d", stageCode, k),
					Title:            s.Structure.Se.FinalBout(strconv.Itoa(k)),
					Venue:            base.Venue,
					ParticipantCount: 2,
					Slots:            base.Slots,
				}
			}
			stage := Stage{Code: stageCode, Title: b.RoundTitle(names, elimRoundTitle(round, roundIndex, winning)), Kind: "matches",
				Rounds: names, At: At{Round: roundIndex + 1}, Matches: series}
			if !seRolledOut(b, names) {
				cfg, err := seSeriesConfig(b)
				if err != nil {
					return Outputs{}, err
				}
				stage.Kind, stage.Config = "series", cfg
			}
			if _, err := b.Emit(stage); err != nil {
				return Outputs{}, err
			}
			seriesFinal = true
			prevCodes = codes
			continue
		}
		prevStages, err = b.Emit(Stage{Code: stageCode, Title: b.RoundTitle(names, elimRoundTitle(round, roundIndex, winning)), Kind: "matches",
			Rounds: names, At: At{Round: roundIndex + 1}, Matches: matches, Waves: true, Lanes: lanes})
		if err != nil {
			return Outputs{}, err
		}
		roundStages = append(roundStages, prevStages...)
		if remaining == 4 {
			semifinalCodes = codes
		}
		prevCodes = codes
	}
	if len(prevCodes) > 1 {
		// The bracket stopped short of a final, so its last round's Matches
		// are what the block offers on: each is a Group sending its winning
		// places.
		out := Outputs{Proceeding: winning}
		for i, code := range prevCodes {
			code := code
			out.Groups = append(out.Groups, Feed{
				Stage: code,
				Label: s.Structure.Titles.Bout(strconv.Itoa(i + 1)),
				Place: func(p int) store.SchemeSlot { return FromMatch(code, p) },
			})
		}
		return out, nil
	}
	finalCode := prevCodes[0]
	return Outputs{Terminal: seriesFinal, Groups: []Feed{{
		Stage: finalCode,
		Label: s.Structure.Titles.Final(),
		Place: func(p int) store.SchemeSlot {
			return FromMatch(finalCode, p)
		},
	}}}, nil
}

// appendBronze emits the bronze Match between the pair the block hands it —
// the semifinal losers, or the places below the finalists out of the incoming
// Edge — as one Match or, with best_of.bronze, as a series.
func appendBronze(b Block, pair []store.SchemeSlot, round int) error {
	s := dopestrings.Default
	stageCode := b.Code() + "-bronze"
	lanes, err := b.Venues("bronze")
	if err != nil {
		return err
	}
	bouts := 1
	if v, ok := b.Int("best_of.bronze"); ok {
		if v < 3 || v%2 == 0 {
			return Keyf("best_of.bronze", "best_of: серия играется до большинства побед — нечётное число боёв от 3")
		}
		bouts = v
	}
	title := func(k int) string {
		if bouts == 1 {
			return s.Structure.Se.Bronze()
		}
		return s.Structure.Se.BronzeBout(strconv.Itoa(k))
	}
	matches := make([]store.SchemeMatch, bouts)
	for k := 1; k <= bouts; k++ {
		matches[k-1] = store.SchemeMatch{
			Code:             fmt.Sprintf("%s-m%d", stageCode, k),
			Title:            title(k),
			Venue:            lanes.Pick(1),
			ParticipantCount: 2,
			Slots:            pair,
		}
	}
	stage := Stage{Code: stageCode, Title: b.RoundTitle([]string{"bronze"}, s.Structure.Se.Bronze()), Kind: "matches",
		Rounds: []string{"bronze"}, At: At{Round: round}, Matches: matches}
	if bouts > 1 && !seRolledOut(b, []string{"bronze"}) {
		cfg, err := seSeriesConfig(b)
		if err != nil {
			return err
		}
		stage.Kind, stage.Config = "series", cfg
	}
	_, err = b.Emit(stage)
	return err
}

// seRolledOut reports whether this Round's series is drawn as its Matches
// rather than ranked as one. A series is a ranking scope by default — that is
// what makes best-of and Troika's summed rating score the same mechanism — but
// a tournament that reads its final off the Matches themselves, as StudChR's
// brain does, says so and gets three boxes instead of a table.
func seRolledOut(b Block, names []string) bool {
	if v, ok := b.Bool("rollout"); ok && v {
		return true
	}
	for _, name := range names {
		if v, ok := b.Bool("rollout." + name); ok && v {
			return true
		}
	}
	return false
}

// seSeriesConfig is how a series is ranked: the block's own points, score
// metric, comparators and scoring rules, exactly as a Group's table reads them.
func seSeriesConfig(b Block) (SeriesConfig, error) {
	points, err := rrPoints(b)
	if err != nil {
		return SeriesConfig{}, err
	}
	cfg := SeriesConfig{Duel: Duel{
		Points: &RRPoints{Win: points[0], Draw: points[1], Loss: points[2]},
		Rules:  b.Rules(),
	}}
	cfg.Metric, _ = b.Str("metric")
	order, ok, err := b.Sorting()
	if err != nil {
		return SeriesConfig{}, err
	}
	if !ok {
		if order, ok, err = b.DefaultSorting(); err != nil {
			return SeriesConfig{}, err
		}
	}
	if ok {
		known := b.Rankable("series")
		for _, rule := range order {
			if !known[rule.Metric] {
				return SeriesConfig{}, UnrankableMetric(rule.Metric, known)
			}
			cfg.Order = append(cfg.Order, rule.Metric)
		}
	}
	return cfg, nil
}

// seDirectBronze is the bronze pair of a bracket with no semifinal to lose:
// the block is seeded straight into its final, so the bronze Match takes the
// place below the finalists out of every source Group. Troika's "group winners
// go to the final, and the teams that placed second go to the bronze Match".
func seDirectBronze(b Block, participants int, bronze bool) ([]store.SchemeSlot, bool) {
	if !bronze || participants != 2 || b.First() {
		return nil, false
	}
	prev, ok := b.Prev()
	if !ok || prev.Proceeding != 2 || len(prev.Groups) != 2 {
		return nil, false
	}
	return []store.SchemeSlot{prev.Groups[0].Place(2), prev.Groups[1].Place(2)}, true
}

// seFirstRound seats the opening round: bracket order over seeds, or the
// winner-meets-runner-up template over the previous block's paired groups.
func seFirstRound(b Block, opening elimRound, winning int) ([][]store.SchemeSlot, error) {
	participants, count := opening.entering, opening.bouts
	if b.First() {
		seeds, err := b.Seeds(participants)
		if err != nil {
			return nil, err
		}
		draw := elimDraw(participants, count, opening.size, winning)
		first := make([][]store.SchemeSlot, count)
		for i := 0; i < count; i++ {
			for _, rank := range draw[i] {
				first[i] = append(first[i], seeds[rank-1])
			}
		}
		return first, nil
	}
	prev, _ := b.Prev()
	if prev.Proceeding <= 0 {
		return nil, errors.New("предыдущему блоку нужен proceeding_participants, чтобы продолжить схему")
	}
	// A reseed makes the Match's size irrelevant: it hands over a ranking, and
	// the snake deals that ranking into Matches of any size — TPSH opens on
	// four seats. Only the template below needs Matches of two.
	if incoming, _ := b.Reseed(); incoming {
		dealt, err := b.Reseeded(count, opening.size)
		if err != nil {
			return nil, err
		}
		return dealt, nil
	}
	if bronze, _ := b.Bool("bronze"); bronze && participants == 2 && prev.Proceeding == 2 && len(prev.Groups) == 2 {
		// Seeded straight into the final: the Group winners meet, and
		// seDirectBronze takes the places below them.
		return [][]store.SchemeSlot{{prev.Groups[0].Place(1), prev.Groups[1].Place(1)}}, nil
	}
	if opening.size != 2 || winning != 1 {
		return nil, fmt.Errorf("нет шаблона рассадки в бои по %d из предыдущего блока — добавьте reseed: true", opening.size)
	}
	if prev.Proceeding != 2 || len(prev.Groups)%2 != 0 || len(prev.Groups)*2 != participants {
		return nil, errors.New("нет шаблона рассадки из этих групп — добавьте reseed: true")
	}
	// Pods (paired groups) fill opposite bracket halves: winners' matches
	// first, runners-up-led rematches in the second half, so pod survivors
	// can only meet again in the final rounds.
	first := make([][]store.SchemeSlot, count)
	half := len(prev.Groups) / 2
	for p := 0; p < half; p++ {
		a, b := prev.Groups[2*p], prev.Groups[2*p+1]
		first[p] = []store.SchemeSlot{a.Place(1), b.Place(2)}
		first[half+p] = []store.SchemeSlot{b.Place(1), a.Place(2)}
	}
	return first, nil
}

// BracketOrder returns 1-based entrant ranks in standard bracket layout: the
// classic recursive fold that keeps top seeds apart until the late rounds
// (for 8: 1,8,4,5,3,6,2,7).
func BracketOrder(n int) []int {
	order := []int{1}
	for len(order) < n {
		grown := make([]int, 0, len(order)*2)
		mirror := len(order)*2 + 1
		for _, rank := range order {
			grown = append(grown, rank, mirror-rank)
		}
		order = grown
	}
	return order
}

func (singleElim) Schedule(cfg json.RawMessage) ([]store.SchemeMatch, error) {
	s := dopestrings.Default
	var conf SEConfig
	if err := json.Unmarshal(cfg, &conf); err != nil {
		return nil, fmt.Errorf("se config: %w", err)
	}
	n := len(conf.Entrants)
	if n < 2 || n&(n-1) != 0 {
		return nil, fmt.Errorf("se: %d entrants, need a power of two", n)
	}

	var matches []store.SchemeMatch
	code := func(round, index int) string { return fmt.Sprintf("%s-r%d-%d", conf.Code, round, index) }
	emit := func(round int, matchCode, title string, slots [2]store.SchemeSlot) {
		matches = append(matches, store.SchemeMatch{
			Code:             matchCode,
			Title:            title,
			Venue:            conf.Venue,
			Round:            round,
			ParticipantCount: 2,
			Slots:            slots[:],
		})
	}
	winnerOf := func(matchCode string) store.SchemeSlot {
		return store.SchemeSlot{FromMatch: &store.SchemeFromMatchRef{Match: matchCode, Place: 1}}
	}
	loserOf := func(matchCode string) store.SchemeSlot {
		return store.SchemeSlot{FromMatch: &store.SchemeFromMatchRef{Match: matchCode, Place: 2}}
	}

	rounds := 0
	for size := n; size > 1; size /= 2 {
		rounds++
	}
	order := BracketOrder(n)
	for i := 0; i < n/2; i++ {
		emit(1, code(1, i+1), roundTitle(rounds, 1, i+1),
			[2]store.SchemeSlot{conf.Entrants[order[2*i]-1], conf.Entrants[order[2*i+1]-1]})
	}
	for round := 2; round <= rounds; round++ {
		count := n >> uint(round)
		for i := 0; i < count; i++ {
			emit(round, code(round, i+1), roundTitle(rounds, round, i+1),
				[2]store.SchemeSlot{winnerOf(code(round-1, 2*i+1)), winnerOf(code(round-1, 2*i+2))})
		}
	}
	if conf.Bronze {
		semi := rounds - 1
		emit(rounds, fmt.Sprintf("%s-r%d-3p", conf.Code, rounds), s.Structure.Se.Bronze(),
			[2]store.SchemeSlot{loserOf(code(semi, 1)), loserOf(code(semi, 2))})
	}
	return matches, nil
}

func roundTitle(rounds, round, index int) string {
	s := dopestrings.Default
	switch rounds - round {
	case 0:
		return s.Structure.Titles.Final()
	case 1:
		return s.Structure.Se.MatchSemifinal(strconv.Itoa(index))
	default:
		return s.Structure.Se.MatchNthRound(strconv.Itoa(1<<uint(rounds-round)), strconv.Itoa(index))
	}
}

// A bracket ranks by progression, which no column shows; its table is the
// place column alone.
func (singleElim) Order(cfg json.RawMessage) []SortRule { return nil }

func (singleElim) Metrics() []string { return nil }

// Standings ranks by Losses, one ending a run (eliminationStandings): the
// champion first, then the eliminated by the round they fell in, the bronze
// splitting the two semifinal losers.
func (singleElim) Standings(cfg json.RawMessage, results []MatchOutcome, _ Inputs) ([]RankedEntry, error) {
	var conf SEConfig
	if err := json.Unmarshal(cfg, &conf); err != nil {
		return nil, fmt.Errorf("se standings config: %w", err)
	}
	return eliminationStandings(1, conf.WinningPlaces, results), nil
}
