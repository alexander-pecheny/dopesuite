package structure

import (
	"strconv"

	dopestrings "dope/i18nstrings"

	"dope/dope/storage/store"
)

// A single elimination whose rounds after the opening the regulations print
// bout by bout (`bouts.rN`), as the double elimination's may (de_written.go).
// The Voronenok's erudite quartet seats its 1/2 e = a1 b2 c1 d2, where the
// bracket's own order would seat a1 a2 b1 b2. A single elimination is a
// bracket with one life, so the planner and the reading are the double
// elimination's; only what the block hands on differs: a final's every place,
// or each last bout as a Group sending its winning places.
func seWritten(b Block, participants, winning int, sizeFor func(blockRound, entering int) int, written [][]string) (Outputs, error) {
	s := dopestrings.Default
	for _, key := range []string{"reseed", "draw", "bronze", "best_of", "pairing"} {
		if hasStr(b, key) {
			return Outputs{}, Keyf(key, "%s", s.Structure.Se.BoutsWith(key))
		}
	}
	opening := snakeChunks
	if reseeded, _ := b.Reseed(); !b.First() && !reseeded {
		opening = straightChunks
	}
	plan, err := planLivesUpTo(participants, 0, 1, winning, winning, 1, sizeFor, opening)
	if err != nil {
		return Outputs{}, err
	}
	w, err := nameOpening(plan, nil, nil)
	if err != nil {
		return Outputs{}, err
	}
	proceeding, declared := b.Proceeding()
	if err := w.extend(written, proceeding, declared); err != nil {
		return Outputs{}, err
	}
	names := make([]string, len(plan.blockRounds))
	for i := range names {
		names[i] = "r" + strconv.Itoa(i+1)
	}
	if err := b.BlockRounds(names); err != nil {
		return Outputs{}, err
	}
	lanes, err := b.Venues(names...)
	if err != nil {
		return Outputs{}, err
	}
	entrants, err := b.Entrants(1, participants)
	if err != nil {
		return Outputs{}, err
	}
	codes, stages, err := emitLivesBracket(b, 1, 1, plan, entrants[0], lanes, false)
	if err != nil {
		return Outputs{}, err
	}
	last := plan.blockRounds[len(plan.blockRounds)-1]
	if len(last) == 1 {
		final := codes[last[0]]
		return Outputs{Groups: []Feed{{
			Stage: stages[len(stages)-1],
			Label: s.Structure.Titles.Final(),
			Place: func(p int) store.SchemeSlot { return FromMatch(final, p) },
		}}}, nil
	}
	out := Outputs{Proceeding: winning}
	for _, index := range last {
		code := codes[index]
		out.Groups = append(out.Groups, Feed{
			Stage: code,
			Label: s.Structure.Titles.Bout(plan.titles[index]),
			Place: func(p int) store.SchemeSlot { return FromMatch(code, p) },
		})
	}
	return out, nil
}
