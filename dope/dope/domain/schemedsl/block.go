package schemedsl

import (
	"errors"
	"fmt"
	"strings"

	"dope/dope/domain/structure"
	"dope/dope/storage/store"
)

// blockHandle is the compiler's adapter at the Kind seam: one Block as its
// Macro sees it (structure.Block). It reads the section, keeps the compiler's
// facts — столы, the previous Block, the Protocol cascade — behind typed
// calls, and pins every complaint to a line.
type blockHandle struct {
	c     *compiler
	index int
	blk   Section
	keys  map[string]structure.Key
	rules *structure.Rules
}

func (b *blockHandle) Code() string { return fmt.Sprintf("s%d", b.index+1) }
func (b *blockHandle) First() bool  { return b.index == 0 }
func (b *blockHandle) Seeded() int  { return len(b.c.in.Entrants) }

func (b *blockHandle) Int(key string) (int, bool) {
	if v, ok := b.blk.Int(key); ok {
		return v, true
	}
	if b.keys[key].Cascade {
		return b.c.doc.Defaults.Int(key)
	}
	return 0, false
}

func (b *blockHandle) Bool(key string) (bool, bool)  { return b.blk.Bool(key) }
func (b *blockHandle) Str(key string) (string, bool) { return b.blk.Str(key) }

func (b *blockHandle) IntList(key string) ([]int, bool, error) {
	sections := []Section{b.blk}
	if b.keys[key].Cascade {
		sections = append(sections, b.c.doc.Defaults)
	}
	for _, section := range sections {
		v, ok, err := section.IntList(key)
		if err != nil || ok {
			return v, ok, err
		}
	}
	return nil, false, nil
}

func (b *blockHandle) NumList(key string) ([]float64, bool, error) {
	sections := []Section{b.blk}
	if b.keys[key].Cascade {
		sections = append(sections, b.c.doc.Defaults)
	}
	for _, section := range sections {
		v, ok, err := section.NumList(key)
		if err != nil || ok {
			return v, ok, err
		}
	}
	return nil, false, nil
}

func (b *blockHandle) Rounds(names []string) error {
	if len(names) == 0 {
		if _, round := blockReseedSpec(b.blk); round != "" {
			return errAt(b.blk.Values["reseed"].Line, "reseed: в этом блоке нет раунда %s — только true/false", round)
		}
	}
	return b.c.rejectRoundKeys(b.blk, names)
}

func (b *blockHandle) Sorting() ([]store.SortRule, bool, error) { return sortRules(b.blk) }
func (b *blockHandle) DefaultSorting() ([]store.SortRule, bool, error) {
	return sortRules(b.c.doc.Defaults)
}

func sortRules(section Section) ([]store.SortRule, bool, error) {
	tokens, ok, err := section.Sorting("sorting")
	if err != nil || !ok {
		return nil, ok, err
	}
	rules := make([]store.SortRule, len(tokens))
	for i, token := range tokens {
		rules[i] = store.SortRule{Metric: token.Metric, Dir: sortDir(token)}
	}
	return rules, true, nil
}

func (b *blockHandle) Rankable(ranker string) map[string]bool { return b.c.rankable(ranker, b.blk) }
func (b *blockHandle) Rules() *structure.Rules                { return b.rules }
func (b *blockHandle) Reseed() (bool, string)                 { return blockReseedSpec(b.blk) }
func (b *blockHandle) Proceeding() (int, bool)                { return b.blk.Int("proceeding_participants") }

func (b *blockHandle) Prev() (structure.Outputs, bool) {
	if b.c.prev == nil {
		return structure.Outputs{}, false
	}
	return *b.c.prev, true
}

func (b *blockHandle) Title(fallback string) string { return blockTitle(b.blk, fallback) }
func (b *blockHandle) GroupTitle(group, groups int) string {
	return b.c.groupTitle(b.blk, group, groups)
}
func (b *blockHandle) RoundTitle(names []string, derived string) string {
	return b.c.roundTitle(b.blk, names, derived)
}

func (b *blockHandle) Venues(names ...string) (structure.Lanes, error) {
	restricted, err := b.c.blockVenues(b.blk, names)
	return structure.Lanes{Restricted: restricted, Total: b.c.venueCount}, err
}

func (b *blockHandle) Entrants(groups, size int) ([][]store.SchemeSlot, error) {
	return b.c.blockEntrants(b.index, b.blk, groups, size)
}

func (b *blockHandle) Seeds(count int) ([]store.SchemeSlot, error) {
	if len(b.c.in.Entrants) > 0 && len(b.c.in.Entrants) != count {
		return nil, errAt(b.blk.Line, "схеме нужно %d участников, а посеяно %d", count, len(b.c.in.Entrants))
	}
	seeds := make([]store.SchemeSlot, count)
	for rank := 1; rank <= count; rank++ {
		seeds[rank-1] = b.c.seedSlot(rank)
	}
	return seeds, nil
}

func (b *blockHandle) Reseeded(groups, size int) ([][]store.SchemeSlot, error) {
	return b.c.dealReseed(b.index, b.blk, groups, size)
}

func (b *blockHandle) Sources(otherwise, self []string) ([]string, error) {
	return b.c.reseedSources(b.index, b.blk, otherwise, self)
}

func (b *blockHandle) Emit(s structure.Stage) ([]string, error) {
	if s.Code != b.Code() && !strings.HasPrefix(s.Code, b.Code()+"-") {
		return nil, fmt.Errorf("stage %s outside block %s", s.Code, b.Code())
	}
	for _, r := range s.Slug {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return nil, errAt(b.blk.Line, "slug — латиница, цифры и дефис, а не %q", s.Slug)
		}
	}
	where := b.at(s.At)
	if s.Matches == nil {
		expander, ok := structure.ExpanderFor(s.Kind)
		if !ok {
			return nil, errAt(0, "%s не зарегистрирован в реестре видов", s.Kind)
		}
		configJSON, err := b.c.stageConfig(s.Config, b.blk, s.Rounds)
		if err != nil {
			return nil, err
		}
		matches, err := expander.Schedule(configJSON)
		if err != nil {
			return nil, errAt(b.blk.Line, "%s", err.Error())
		}
		b.c.position++
		b.c.scheme.Stages = append(b.c.scheme.Stages, store.SchemeStage{
			Code:      s.Code,
			Title:     s.Title,
			StageType: "matches",
			Kind:      s.Kind,
			Slug:      s.Slug,
			Position:  b.c.position,
			Grain:     where.grain(),
			Matches:   matches,
			Config:    configJSON,
		})
		return []string{s.Code}, nil
	}
	if s.Waves {
		return b.c.appendSERound(b.blk, s.Code, s.Title, s.Rounds, s.Lanes.Restricted, where, s.Matches), nil
	}
	b.c.appendDrawnStage(s.Kind, s.Config, b.blk, s.Code, s.Title, s.Rounds, where, s.Matches)
	return []string{s.Code}, nil
}

func (b *blockHandle) EmitReseed(code string, where structure.At, contenders []store.SchemeSlot, bands []int, sources []string) (string, error) {
	if !strings.HasPrefix(code, b.Code()+"-") {
		return "", fmt.Errorf("reseed %s outside block %s", code, b.Code())
	}
	return b.c.reseedStageBanded(code, b.at(where), b.blk, sources, contenders, bands)
}

func (b *blockHandle) at(where structure.At) at {
	return at{block: b.Code(), round: where.Round, group: where.Group}
}

// pin turns a Kind's complaint into a DSL error at the right line: a KeyError
// at its key's line (the block's, else [defaults]' for a cascading key), a
// compiler error as it is, anything else at the Block's line.
func (b *blockHandle) pin(err error) error {
	var dslErr *Error
	if errors.As(err, &dslErr) {
		return err
	}
	var keyErr structure.KeyError
	if errors.As(err, &keyErr) {
		if v, ok := b.blk.Values[keyErr.Key]; ok {
			return errAt(v.Line, "%s", keyErr.Msg)
		}
		if v, ok := b.c.doc.Defaults.Values[keyErr.Key]; ok && b.keys[keyErr.Key].Cascade {
			return errAt(v.Line, "%s", keyErr.Msg)
		}
	}
	return errAt(b.blk.Line, "%s", err.Error())
}

// appendSERound emits one round as ⌈matches/venues⌉ Wave stages — one stage
// when everything fits, `-w{k}` codes when the venue count forces turns — and
// returns the stage codes it created (a following reseed sources them).
func (c *compiler) appendSERound(blk Section, stageCode, title string, rounds []string, venues []int, where at, matches []store.SchemeMatch) []string {
	perWave := len(venues)
	if perWave == 0 {
		perWave = c.venueCount
	}
	if len(matches) <= perWave {
		c.appendManualStage(blk, stageCode, title, rounds, where, matches)
		return []string{stageCode}
	}
	var codes []string
	for wave := 0; wave*perWave < len(matches); wave++ {
		end := (wave + 1) * perWave
		if end > len(matches) {
			end = len(matches)
		}
		code := fmt.Sprintf("%s-w%d", stageCode, wave+1)
		waveAt := where
		waveAt.wave = wave + 1
		c.appendManualStage(blk, code,
			fmt.Sprintf("%s, заход %d", title, wave+1),
			rounds, waveAt, matches[wave*perWave:end])
		codes = append(codes, code)
	}
	return codes
}
