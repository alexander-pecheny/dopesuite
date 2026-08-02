package schemedsl

import (
	"encoding/json"
	"fmt"
	"strings"

	"dope/dope/domain/structure"
	"dope/dope/platform/util"
	"dope/dope/storage/store"
)

// Input is what the DSL cannot know by itself: the game's identity and,
// optionally, the seeded entrant list in seed-rank order (roster-derived at
// creation; absent entrants compile to positional Посев placeholders).
type Input struct {
	Slug     string
	Title    string
	GameType string
	Entrants []store.SchemeSlot
}

// Compile macroexpands a parsed DSL document into the detailed scheme the
// runtime speaks: rr stages per Group, matches-kind stages for eliminations,
// reseed stages for reseed Edges (docs/scheme-dsl.md).
func Compile(doc *Doc, in Input) (store.FestScheme, error) {
	c := &compiler{doc: doc, in: in}
	if err := c.run(); err != nil {
		return store.FestScheme{}, err
	}
	return c.scheme, nil
}

const (
	kindRR = "roundrobin"
	kindSE = "single_elimination"
	kindDE = "double_elimination"
)

var canonOrder = []string{"points", "h2h", "taken", "diff"}

var rrMetricAliases = map[string]string{
	"points": "points", "head2head": "h2h", "h2h": "h2h", "taken": "taken", "diff": "diff",
}

type compiler struct {
	doc    *Doc
	in     Input
	scheme store.FestScheme

	venueCount int
	position   int
	prev       *blockOutputs
}

// blockOutputs is what one expanded block offers the next block's Edge: per
// Group, a way to reference each place.
type blockOutputs struct {
	groups     []groupOut
	proceeding int
}

type groupOut struct {
	stageCode string
	label     string
	place     func(p int) store.SchemeSlot
}

func (c *compiler) run() error {
	if len(c.doc.Blocks) == 0 {
		return errAt(0, "в [scheme] нет ни одного блока")
	}
	if err := c.checkKeys(); err != nil {
		return err
	}
	if err := c.readInit(); err != nil {
		return err
	}
	if err := c.readVenues(); err != nil {
		return err
	}
	c.scheme.SchemaVersion = 2
	c.scheme.Slug = c.in.Slug
	c.scheme.Title = c.in.Title
	c.scheme.GameType = c.in.GameType
	if questions, ok := c.doc.Defaults.Int("questions"); ok {
		c.scheme.Questions = questions
	}
	for i := range c.doc.Blocks {
		if err := c.expandBlock(i); err != nil {
			return err
		}
	}
	return nil
}

// --- vocabulary ------------------------------------------------------------

// protocolParams is the per-game-type allowlist of protocol keys and the
// stage-config names they compile to.
var protocolParams = map[string]map[string]string{
	"brain": {"questions": "questions", "tiebreak_questions": "tiebreakQuestions"},
}

var blockKeys = map[string]bool{
	"type": true, "title": true, "groups": true, "teams_in_group": true, "teams": true,
	"proceeding_teams": true, "reseed": true, "sorting": true, "points": true,
	"venues": true, "bronze": true,
}

var defaultsKeys = map[string]bool{"venues": true, "sorting": true, "points": true}
var initKeys = map[string]bool{"seed": true, "sorting": true}

func (c *compiler) protocolConfigKey(base string) (string, bool) {
	name, ok := protocolParams[c.in.GameType][base]
	return name, ok
}

func (c *compiler) checkKeys() error {
	for key, v := range c.doc.Defaults.Values {
		if _, isParam := c.protocolConfigKey(key); !defaultsKeys[key] && !isParam {
			return errAt(v.Line, "неизвестный ключ %s в [defaults]", key)
		}
	}
	for key, v := range c.doc.Init.Values {
		if !initKeys[key] {
			return errAt(v.Line, "неизвестный ключ %s в [init]", key)
		}
	}
	for _, blk := range c.doc.Blocks {
		for key, v := range blk.Values {
			base, _, dotted := strings.Cut(key, ".")
			if dotted {
				if _, isParam := c.protocolConfigKey(base); !isParam && base != "venues" {
					return errAt(v.Line, "неизвестный ключ %s", key)
				}
				continue // round suffixes are validated by the block's kind
			}
			if _, isParam := c.protocolConfigKey(key); !blockKeys[key] && !isParam {
				return errAt(v.Line, "неизвестный ключ %s", key)
			}
		}
	}
	return nil
}

func (c *compiler) readInit() error {
	seed, ok := c.doc.Init.Str("seed")
	if !ok {
		return nil
	}
	seeding := &store.SchemeSeeding{Source: seed}
	rules, ok, err := c.doc.Init.Sorting("sorting")
	if err != nil {
		return err
	}
	if ok {
		for _, rule := range rules {
			seeding.Sort = append(seeding.Sort, store.SchemeSortRule{Metric: rule.Metric, Dir: rule.Dir})
		}
	}
	c.scheme.Seeding = seeding
	return nil
}

// readVenues resolves [defaults] venues (count or titled list); absent, the
// count is derived as the widest block's lane need.
func (c *compiler) readVenues() error {
	if count, ok := c.doc.Defaults.Int("venues"); ok {
		if count < 1 {
			return errAt(c.doc.Defaults.Values["venues"].Line, "venues: нужен хотя бы один стол")
		}
		for i := 1; i <= count; i++ {
			c.scheme.Venues = append(c.scheme.Venues, store.SchemeVenue{Number: i, Title: fmt.Sprintf("Стол %d", i)})
		}
		c.venueCount = count
		return nil
	}
	if titles, ok, err := c.doc.Defaults.List("venues"); err != nil {
		return err
	} else if ok {
		for i, title := range titles {
			c.scheme.Venues = append(c.scheme.Venues, store.SchemeVenue{Number: i + 1, Title: title})
		}
		c.venueCount = len(titles)
		return nil
	}
	need := 1
	for _, blk := range c.doc.Blocks {
		if groups, ok := blk.Int("groups"); ok && groups > need {
			need = groups
		}
		if teams, ok := blk.Int("teams"); ok && teams/2 > need {
			need = teams / 2
		}
	}
	for i := 1; i <= need; i++ {
		c.scheme.Venues = append(c.scheme.Venues, store.SchemeVenue{Number: i, Title: fmt.Sprintf("Стол %d", i)})
	}
	c.venueCount = need
	return nil
}

func (c *compiler) venueFor(index int) int {
	return (index-1)%c.venueCount + 1
}

// blockVenues resolves a block's (or, dotted, one round's) `venues` subset to
// venue numbers, by title or number; nil = no restriction.
func (c *compiler) blockVenues(blk Section, rounds []string) ([]int, error) {
	keys := []string{}
	for _, round := range rounds {
		keys = append(keys, "venues."+round)
	}
	keys = append(keys, "venues")
	for _, key := range keys {
		items, ok, err := blk.List(key)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		nums := make([]int, len(items))
		for i, item := range items {
			nums[i] = 0
			for _, venue := range c.scheme.Venues {
				if venue.Title == item || fmt.Sprint(venue.Number) == item {
					nums[i] = venue.Number
					break
				}
			}
			if nums[i] == 0 {
				return nil, errAt(blk.Values[key].Line, "%s: стол %q не объявлен в venues", key, item)
			}
		}
		if len(nums) == 0 {
			return nil, errAt(blk.Values[key].Line, "%s: пустой список столов", key)
		}
		return nums, nil
	}
	return nil, nil
}

// venuePick assigns the i-th lane a venue, cycling the restricted subset when
// one is declared.
func (c *compiler) venuePick(restricted []int, i int) int {
	if len(restricted) == 0 {
		return c.venueFor(i)
	}
	return restricted[(i-1)%len(restricted)]
}

// --- cascade ---------------------------------------------------------------

// paramInt resolves one protocol/config key through defaults < block < round;
// rounds lists the stage's round code plus its aliases (r4 for semifinal).
func paramInt(defaults, blk Section, key string, rounds []string) (int, bool) {
	for _, round := range rounds {
		if v, ok := blk.Int(key + "." + round); ok {
			return v, true
		}
	}
	if v, ok := blk.Int(key); ok {
		return v, true
	}
	if v, ok := defaults.Int(key); ok {
		return v, true
	}
	return 0, false
}

func paramBool(defaults, blk Section, key string, rounds []string) (bool, bool) {
	for _, round := range rounds {
		if v, ok := blk.Bool(key + "." + round); ok {
			return v, true
		}
	}
	if v, ok := blk.Bool(key); ok {
		return v, true
	}
	if v, ok := defaults.Bool(key); ok {
		return v, true
	}
	return false, false
}

// protocolConfig collects the game's protocol params for one stage (rounds
// empty means the block default).
func (c *compiler) protocolConfig(blk Section, rounds []string) map[string]any {
	config := map[string]any{}
	for dslKey, configKey := range protocolParams[c.in.GameType] {
		switch dslKey {
		case "tiebreak_questions":
			if v, ok := paramBool(c.doc.Defaults, blk, dslKey, rounds); ok {
				config[configKey] = v
			}
		default:
			if v, ok := paramInt(c.doc.Defaults, blk, dslKey, rounds); ok {
				config[configKey] = v
			}
		}
	}
	return config
}

func (c *compiler) rrOrder(blk Section) ([]string, error) {
	rules, ok, err := blk.Sorting("sorting")
	if err != nil {
		return nil, err
	}
	if !ok {
		if rules, ok, err = c.doc.Defaults.Sorting("sorting"); err != nil {
			return nil, err
		}
	}
	if !ok {
		return canonOrder, nil
	}
	order := make([]string, len(rules))
	for i, rule := range rules {
		metric, known := rrMetricAliases[rule.Metric]
		if !known {
			return nil, errAt(blk.Line, "sorting: %s — не групповой компаратор (есть points, head2head, taken, diff)", rule.Metric)
		}
		order[i] = metric
	}
	return order, nil
}

func (c *compiler) rrPoints(blk Section) ([]int, error) {
	for _, section := range []Section{blk, c.doc.Defaults} {
		points, ok, err := section.IntList("points")
		if err != nil {
			return nil, err
		}
		if ok {
			if len(points) != 3 {
				return nil, errAt(section.Values["points"].Line, "points: жду [победа, ничья, поражение]")
			}
			return points, nil
		}
	}
	return []int{2, 1, 0}, nil
}

// --- entrant supply --------------------------------------------------------

// snakeDeal deals ranks 1..G*K into G groups of K: bands of G, odd bands
// reversed — the reference PF_GROUPS pattern (generate_kinsbf.py).
func snakeDeal(groups, size int) [][]int {
	dealt := make([][]int, groups)
	for band := 0; band < size; band++ {
		for g := 1; g <= groups; g++ {
			rank := band*groups + g
			if band%2 == 1 {
				rank = band*groups + (groups + 1 - g)
			}
			dealt[g-1] = append(dealt[g-1], rank)
		}
	}
	return dealt
}

// seedSlot returns the entrant at seed rank p (1-based): the provided entrant
// list, or a positional Посев placeholder. Basket stays 1 — the seed-import
// ladder keys assignments on (basket 1, rank); bands live in the position.
func (c *compiler) seedSlot(rank int) store.SchemeSlot {
	if len(c.in.Entrants) > 0 {
		return c.in.Entrants[rank-1]
	}
	return store.SchemeSlot{
		Seed:  &store.SchemeSeedRef{Basket: 1, Position: rank},
		Label: fmt.Sprintf("Посев %d", rank),
	}
}

// blockEntrants supplies each group's entrant slots for a block that needs
// G groups of K, resolving the incoming Edge: seeds for block 1, an explicit
// reseed, or a deterministic template from the previous block's groups.
func (c *compiler) blockEntrants(index int, blk Section, groups, size int) ([][]store.SchemeSlot, error) {
	total := groups * size
	if index == 0 {
		if len(c.in.Entrants) > 0 && len(c.in.Entrants) != total {
			return nil, errAt(blk.Line, "схеме нужно %d команд, а посеяно %d", total, len(c.in.Entrants))
		}
		return c.dealSeeds(groups, size), nil
	}
	prev := c.prev
	if prev.proceeding <= 0 {
		return nil, errAt(blk.Line, "предыдущему блоку нужен proceeding_teams, чтобы продолжить схему")
	}
	supply := len(prev.groups) * prev.proceeding
	if supply != total {
		return nil, errAt(blk.Line, "из предыдущего блока выходят %d команд, а блоку нужно %d", supply, total)
	}
	if incoming, _ := blockReseedSpec(blk); incoming {
		return c.dealReseed(index, blk, groups, size)
	}
	return c.dealDeterministic(blk, groups, size)
}

// rejectRoundReseed fails a round-code reseed on kinds with no addressable
// rounds (rr, de).
func rejectRoundReseed(blk Section) error {
	if _, round := blockReseedSpec(blk); round != "" {
		return errAt(blk.Values["reseed"].Line, "reseed: в этом блоке нет раунда %s — только true/false", round)
	}
	return nil
}

// blockReseedSpec parses the reseed key: `true` re-ranks the incoming Edge,
// a round code re-ranks at that boundary inside the block (se only).
func blockReseedSpec(blk Section) (incoming bool, round string) {
	if v, ok := blk.Bool("reseed"); ok {
		return v, ""
	}
	if v, ok := blk.Str("reseed"); ok {
		return false, v
	}
	return false, ""
}

// reseedSortRules maps the block's sorting tokens onto reseed metrics; absent
// sorting keeps the canonical place_sum-then-taken order.
func (c *compiler) reseedSortRules(blk Section) ([]store.SchemeSortRule, error) {
	tokens, ok, err := blk.Sorting("sorting")
	if err != nil {
		return nil, err
	}
	if !ok {
		return []store.SchemeSortRule{{Metric: "place_sum", Dir: "asc"}, {Metric: "taken", Dir: "desc"}}, nil
	}
	var rules []store.SchemeSortRule
	for _, token := range tokens {
		switch token.Metric {
		case "points":
			rules = append(rules, store.SchemeSortRule{Metric: "place_sum", Dir: "asc"})
		case "taken":
			rules = append(rules, store.SchemeSortRule{Metric: "taken", Dir: "desc"})
		default:
			return nil, errAt(blk.Line, "sorting: %s не считается на пересеве (есть points, taken)", token.Metric)
		}
	}
	return rules, nil
}

func (c *compiler) dealSeeds(groups, size int) [][]store.SchemeSlot {
	dealt := snakeDeal(groups, size)
	out := make([][]store.SchemeSlot, groups)
	for g, ranks := range dealt {
		for _, rank := range ranks {
			out[g] = append(out[g], c.seedSlot(rank))
		}
	}
	return out
}

// reseedStage materialises one reseed Edge over the given source stages and
// returns its code; rank refs against it seat whatever follows.
func (c *compiler) reseedStage(index int, blk Section, sources []string) (string, error) {
	sort, err := c.reseedSortRules(blk)
	if err != nil {
		return "", err
	}
	code := fmt.Sprintf("s%d-reseed", index+1)
	c.position++
	c.scheme.Stages = append(c.scheme.Stages, store.SchemeStage{
		Code:      code,
		Title:     "Пересев",
		StageType: "reseed",
		Kind:      "reseed",
		Position:  c.position,
		Sources:   sources,
		Sort:      json.RawMessage(util.MustJSON(sort)),
	})
	return code, nil
}

func (c *compiler) prevStageCodes() []string {
	codes := make([]string, len(c.prev.groups))
	for i, g := range c.prev.groups {
		codes[i] = g.stageCode
	}
	return codes
}

func reseedRankSlot(stage string, rank int) store.SchemeSlot {
	return store.SchemeSlot{
		Reseed: &store.SchemeReseedRef{Stage: stage, Rank: rank},
		Label:  fmt.Sprintf("Пересев-%d", rank),
	}
}

// dealReseed materialises the block-grain Edge: a reseed stage over every
// previous group, then a snake deal of its ranks.
func (c *compiler) dealReseed(index int, blk Section, groups, size int) ([][]store.SchemeSlot, error) {
	code, err := c.reseedStage(index, blk, c.prevStageCodes())
	if err != nil {
		return nil, err
	}
	out := make([][]store.SchemeSlot, groups)
	for g, ranks := range snakeDeal(groups, size) {
		for _, rank := range ranks {
			out[g] = append(out[g], reseedRankSlot(code, rank))
		}
	}
	return out, nil
}

// dealDeterministic is the reference cross template: source groups arranged in
// rows of G_new; the new group at column i takes place 1 from column i and
// place 2 from its partner column (i xor 1), row by row.
func (c *compiler) dealDeterministic(blk Section, groups, size int) ([][]store.SchemeSlot, error) {
	prev := c.prev
	sourceGroups := len(prev.groups)
	rows := sourceGroups / groups
	needsReseed := func(why string) error {
		return errAt(blk.Line, "%s — добавьте reseed: true", why)
	}
	if prev.proceeding != 2 {
		return nil, needsReseed("детерминированная рассадка определена для proceeding_teams: 2")
	}
	if rows*groups != sourceGroups || groups%2 != 0 {
		return nil, needsReseed("нет шаблона рассадки из этих групп")
	}
	if size != 2*rows {
		return nil, needsReseed("нет шаблона рассадки из этих групп")
	}
	out := make([][]store.SchemeSlot, groups)
	for g := 0; g < groups; g++ {
		partner := g ^ 1
		for row := 0; row < rows; row++ {
			own := prev.groups[row*groups+g]
			other := prev.groups[row*groups+partner]
			out[g] = append(out[g], own.place(1), other.place(2))
		}
	}
	return out, nil
}

// --- block expansion -------------------------------------------------------

func (c *compiler) expandBlock(index int) error {
	blk := c.doc.Blocks[index]
	kind, _ := blk.Str("type")
	var out *blockOutputs
	var err error
	switch kind {
	case kindRR:
		out, err = c.expandRoundRobin(index, blk)
	case kindSE:
		out, err = c.expandSingleElim(index, blk)
	case kindDE:
		out, err = c.expandDoubleElim(index, blk)
	case "swiss":
		return errAt(blk.Line, "swiss ещё не реализован")
	case "":
		return errAt(blk.Line, "блоку нужен type")
	default:
		return errAt(blk.Line, "неизвестный type %s", kind)
	}
	if err != nil {
		return err
	}
	if proceeding, ok := blk.Int("proceeding_teams"); ok {
		out.proceeding = proceeding
	}
	c.prev = out
	return nil
}

func (c *compiler) groupTitle(blk Section, group, groups int) string {
	if title, ok := blk.Str("title"); ok && groups == 1 {
		return title
	}
	if groups == 1 && len(c.doc.Blocks) == 1 {
		return "Группа"
	}
	return fmt.Sprintf("Группа %d", group)
}

func (c *compiler) expandRoundRobin(index int, blk Section) (*blockOutputs, error) {
	size, ok := blk.Int("teams_in_group")
	if !ok {
		return nil, errAt(blk.Line, "roundrobin: нужен teams_in_group")
	}
	groups, ok := blk.Int("groups")
	if !ok {
		groups = 1
	}
	if groups < 1 || size < 2 {
		return nil, errAt(blk.Line, "roundrobin: groups ≥ 1, teams_in_group ≥ 2")
	}
	if err := c.rejectRoundKeys(blk, nil); err != nil {
		return nil, err
	}
	if err := rejectRoundReseed(blk); err != nil {
		return nil, err
	}
	venues, err := c.blockVenues(blk, nil)
	if err != nil {
		return nil, err
	}
	entrants, err := c.blockEntrants(index, blk, groups, size)
	if err != nil {
		return nil, err
	}
	order, err := c.rrOrder(blk)
	if err != nil {
		return nil, err
	}
	points, err := c.rrPoints(blk)
	if err != nil {
		return nil, err
	}
	rr, ok := structure.Kind("rr")
	if !ok {
		return nil, errAt(0, "rr не зарегистрирован в реестре видов")
	}
	out := &blockOutputs{}
	for g := 1; g <= groups; g++ {
		code := fmt.Sprintf("s%d-g%d", index+1, g)
		config := c.protocolConfig(blk, nil)
		config["code"] = code
		config["entrants"] = entrants[g-1]
		config["order"] = order
		config["points"] = map[string]int{"win": points[0], "draw": points[1], "loss": points[2]}
		config["venue"] = c.venuePick(venues, g)
		configJSON, err := json.Marshal(config)
		if err != nil {
			return nil, err
		}
		matches, err := rr.Schedule(configJSON, nil)
		if err != nil {
			return nil, errAt(blk.Line, "%s", err.Error())
		}
		c.position++
		c.scheme.Stages = append(c.scheme.Stages, store.SchemeStage{
			Code:      code,
			Title:     c.groupTitle(blk, g, groups),
			StageType: "matches",
			Kind:      "rr",
			Position:  c.position,
			Matches:   matches,
			Config:    configJSON,
		})
		stageCode := code
		label := fmt.Sprintf("Гр. %d", g)
		out.groups = append(out.groups, groupOut{
			stageCode: stageCode,
			label:     label,
			place: func(p int) store.SchemeSlot {
				return store.SchemeSlot{
					Reseed: &store.SchemeReseedRef{Stage: stageCode, Rank: p},
					Label:  fmt.Sprintf("%s-%d", label, p),
				}
			},
		})
	}
	return out, nil
}

func seRoundCode(remaining int) string {
	switch remaining {
	case 2:
		return "final"
	case 4:
		return "semifinal"
	}
	return fmt.Sprintf("r%d", remaining)
}

// seRoundNames is the round's canonical code plus its r{N} alias — both are
// accepted in every round-addressing key (questions.r4 ≡ questions.semifinal).
func seRoundNames(remaining int) []string {
	code := seRoundCode(remaining)
	if remaining == 2 || remaining == 4 {
		return []string{code, fmt.Sprintf("r%d", remaining)}
	}
	return []string{code}
}

func seRoundTitle(remaining int) string {
	switch remaining {
	case 2:
		return "Финал"
	case 4:
		return "Полуфиналы"
	}
	return fmt.Sprintf("1/%d финала", remaining/2)
}

func (c *compiler) expandSingleElim(index int, blk Section) (*blockOutputs, error) {
	teams, ok := blk.Int("teams")
	if !ok {
		return nil, errAt(blk.Line, "single_elimination: нужен teams")
	}
	if teams < 2 || teams&(teams-1) != 0 {
		return nil, errAt(blk.Line, "single_elimination: teams должен быть степенью двойки")
	}
	bronze, _ := blk.Bool("bronze")
	rounds := []string{}
	for remaining := teams; remaining >= 2; remaining /= 2 {
		rounds = append(rounds, seRoundNames(remaining)...)
	}
	if bronze && teams >= 4 {
		rounds = append(rounds, "bronze")
	}
	if err := c.rejectRoundKeys(blk, rounds); err != nil {
		return nil, err
	}
	_, boundary := blockReseedSpec(blk)
	boundaryAt := 0
	if boundary != "" {
		for remaining := teams; remaining >= 2; remaining /= 2 {
			for _, name := range seRoundNames(remaining) {
				if name == boundary {
					boundaryAt = remaining
				}
			}
		}
		if boundaryAt == 0 {
			return nil, errAt(blk.Values["reseed"].Line, "reseed: в этом блоке нет раунда %s", boundary)
		}
		if boundaryAt == teams {
			return nil, errAt(blk.Values["reseed"].Line, "reseed: %s — первый раунд, пишите reseed: true", boundary)
		}
	}

	first, err := c.seFirstRound(index, blk, teams)
	if err != nil {
		return nil, err
	}
	blockCode := fmt.Sprintf("s%d", index+1)
	prevCodes := []string{}
	var prevStages []string
	var semifinalCodes []string
	for remaining := teams; remaining >= 2; remaining /= 2 {
		names := seRoundNames(remaining)
		stageCode := fmt.Sprintf("%s-%s", blockCode, names[0])
		count := remaining / 2
		var reseedCode string
		if remaining == boundaryAt {
			if reseedCode, err = c.reseedStage(index, blk, prevStages); err != nil {
				return nil, err
			}
		}
		venues, err := c.blockVenues(blk, names)
		if err != nil {
			return nil, err
		}
		matches := make([]store.SchemeMatch, count)
		codes := make([]string, count)
		rankOrder := structure.BracketOrder(remaining)
		for i := 1; i <= count; i++ {
			code := fmt.Sprintf("%s-m%d", stageCode, i)
			codes[i-1] = code
			var slots []store.SchemeSlot
			switch {
			case reseedCode != "":
				slots = []store.SchemeSlot{
					reseedRankSlot(reseedCode, rankOrder[2*i-2]),
					reseedRankSlot(reseedCode, rankOrder[2*i-1]),
				}
			case remaining == teams:
				slots = first[i-1]
			default:
				slots = []store.SchemeSlot{
					fromMatchSlot(prevCodes[2*i-2], 1),
					fromMatchSlot(prevCodes[2*i-1], 1),
				}
			}
			matches[i-1] = store.SchemeMatch{
				Code:             code,
				Title:            fmt.Sprintf("Бой %d", i),
				Venue:            c.venuePick(venues, i),
				ParticipantCount: 2,
				Slots:            slots,
			}
		}
		prevStages = c.appendSERound(blk, stageCode, seRoundTitle(remaining), names, venues, matches)
		if remaining == 4 {
			semifinalCodes = codes
		}
		prevCodes = codes
	}
	finalCode := prevCodes[0]
	out := &blockOutputs{groups: []groupOut{{
		stageCode: finalCode,
		label:     "Финал",
		place: func(p int) store.SchemeSlot {
			return fromMatchSlot(finalCode, p)
		},
	}}}
	if bronze && teams >= 4 {
		stageCode := blockCode + "-bronze"
		venues, err := c.blockVenues(blk, []string{"bronze"})
		if err != nil {
			return nil, err
		}
		matches := []store.SchemeMatch{{
			Code:             stageCode + "-m1",
			Title:            "Матч за 3-е место",
			Venue:            c.venuePick(venues, 1),
			ParticipantCount: 2,
			Slots: []store.SchemeSlot{
				fromMatchSlot(semifinalCodes[0], 2),
				fromMatchSlot(semifinalCodes[1], 2),
			},
		}}
		c.appendManualStage(blk, stageCode, "Матч за 3-е место", []string{"bronze"}, matches)
	}
	return out, nil
}

// appendSERound emits one round as ⌈matches/venues⌉ Wave stages — one stage
// when everything fits, `-w{k}` codes when the venue count forces turns — and
// returns the stage codes it created (a following reseed sources them).
func (c *compiler) appendSERound(blk Section, stageCode, title string, rounds []string, venues []int, matches []store.SchemeMatch) []string {
	perWave := len(venues)
	if perWave == 0 {
		perWave = c.venueCount
	}
	if len(matches) <= perWave {
		c.appendManualStage(blk, stageCode, title, rounds, matches)
		return []string{stageCode}
	}
	var codes []string
	for wave := 0; wave*perWave < len(matches); wave++ {
		end := (wave + 1) * perWave
		if end > len(matches) {
			end = len(matches)
		}
		code := fmt.Sprintf("%s-w%d", stageCode, wave+1)
		c.appendManualStage(blk, code,
			fmt.Sprintf("%s, заход %d", title, wave+1),
			rounds, matches[wave*perWave:end])
		codes = append(codes, code)
	}
	return codes
}

// seFirstRound seats the opening round: bracket order over seeds, or the
// winner-meets-runner-up template over the previous block's paired groups.
func (c *compiler) seFirstRound(index int, blk Section, teams int) ([][]store.SchemeSlot, error) {
	count := teams / 2
	if index == 0 {
		if len(c.in.Entrants) > 0 && len(c.in.Entrants) != teams {
			return nil, errAt(blk.Line, "схеме нужно %d команд, а посеяно %d", teams, len(c.in.Entrants))
		}
		order := structure.BracketOrder(teams)
		first := make([][]store.SchemeSlot, count)
		for i := 0; i < count; i++ {
			first[i] = []store.SchemeSlot{
				c.seedSlot(order[2*i]),
				c.seedSlot(order[2*i+1]),
			}
		}
		return first, nil
	}
	prev := c.prev
	if prev.proceeding <= 0 {
		return nil, errAt(blk.Line, "предыдущему блоку нужен proceeding_teams, чтобы продолжить схему")
	}
	if incoming, _ := blockReseedSpec(blk); incoming {
		dealt, err := c.dealReseed(index, blk, count, 2)
		if err != nil {
			return nil, err
		}
		return dealt, nil
	}
	if prev.proceeding != 2 || len(prev.groups)%2 != 0 || len(prev.groups)*2 != teams {
		return nil, errAt(blk.Line, "нет шаблона рассадки из этих групп — добавьте reseed: true")
	}
	// Pods (paired groups) fill opposite bracket halves: winners' matches
	// first, runners-up-led rematches in the second half, so pod survivors
	// can only meet again in the final rounds.
	first := make([][]store.SchemeSlot, count)
	half := len(prev.groups) / 2
	for p := 0; p < half; p++ {
		a, b := prev.groups[2*p], prev.groups[2*p+1]
		first[p] = []store.SchemeSlot{a.place(1), b.place(2)}
		first[half+p] = []store.SchemeSlot{b.place(1), a.place(2)}
	}
	return first, nil
}

// deMatchPlan is the canonical 4-team double-elimination group
// (studchr_tables_common add_de): openers, winners, losers, cross.
var deMatchPlan = []struct {
	title string
	slots func(entrants []store.SchemeSlot, code func(m int) string) []store.SchemeSlot
}{
	{"Бой 1", func(e []store.SchemeSlot, code func(int) string) []store.SchemeSlot {
		return []store.SchemeSlot{e[0], e[1]}
	}},
	{"Бой 2", func(e []store.SchemeSlot, code func(int) string) []store.SchemeSlot {
		return []store.SchemeSlot{e[2], e[3]}
	}},
	{"Бой 3", func(e []store.SchemeSlot, code func(int) string) []store.SchemeSlot {
		return []store.SchemeSlot{fromMatchSlot(code(1), 1), fromMatchSlot(code(2), 1)}
	}},
	{"Бой 4", func(e []store.SchemeSlot, code func(int) string) []store.SchemeSlot {
		return []store.SchemeSlot{fromMatchSlot(code(1), 2), fromMatchSlot(code(2), 2)}
	}},
	{"Бой 5", func(e []store.SchemeSlot, code func(int) string) []store.SchemeSlot {
		return []store.SchemeSlot{fromMatchSlot(code(3), 2), fromMatchSlot(code(4), 1)}
	}},
}

var dePlaces = map[int]struct {
	match int
	place int
}{1: {3, 1}, 2: {5, 1}, 3: {5, 2}, 4: {4, 2}}

func (c *compiler) expandDoubleElim(index int, blk Section) (*blockOutputs, error) {
	groups, ok := blk.Int("groups")
	if !ok {
		if teams, hasTeams := blk.Int("teams"); hasTeams && teams > 0 && teams%4 == 0 {
			groups = teams / 4
		} else {
			return nil, errAt(blk.Line, "double_elimination: нужен groups (или teams, кратный 4)")
		}
	}
	if size, ok := blk.Int("teams_in_group"); ok && size != 4 {
		return nil, errAt(blk.Line, "double_elimination: группа всегда из 4 команд")
	}
	if err := c.rejectRoundKeys(blk, nil); err != nil {
		return nil, err
	}
	if err := rejectRoundReseed(blk); err != nil {
		return nil, err
	}
	venues, err := c.blockVenues(blk, nil)
	if err != nil {
		return nil, err
	}
	entrants, err := c.blockEntrants(index, blk, groups, 4)
	if err != nil {
		return nil, err
	}
	if index == 0 {
		// Seed-dealt groups open bracket-style: best meets worst.
		for g := range entrants {
			e := entrants[g]
			entrants[g] = []store.SchemeSlot{e[0], e[3], e[1], e[2]}
		}
	}
	out := &blockOutputs{}
	for g := 1; g <= groups; g++ {
		stageCode := fmt.Sprintf("s%d-g%d", index+1, g)
		matchCode := func(m int) string { return fmt.Sprintf("%s-m%d", stageCode, m) }
		var matches []store.SchemeMatch
		for i, plan := range deMatchPlan {
			matches = append(matches, store.SchemeMatch{
				Code:             matchCode(i + 1),
				Title:            plan.title,
				Venue:            c.venuePick(venues, g),
				ParticipantCount: 2,
				Slots:            plan.slots(entrants[g-1], matchCode),
			})
		}
		c.appendManualStage(blk, stageCode, fmt.Sprintf("DE %d", g), nil, matches)
		label := fmt.Sprintf("DE %d", g)
		out.groups = append(out.groups, groupOut{
			stageCode: stageCode,
			label:     label,
			place: func(p int) store.SchemeSlot {
				spec := dePlaces[p]
				return fromMatchSlot(matchCode(spec.match), spec.place)
			},
		})
	}
	return out, nil
}

func fromMatchSlot(matchCode string, place int) store.SchemeSlot {
	return store.SchemeSlot{FromMatch: &store.SchemeFromMatchRef{Match: matchCode, Place: place}}
}

func (c *compiler) appendManualStage(blk Section, code, title string, rounds []string, matches []store.SchemeMatch) {
	config := c.protocolConfig(blk, rounds)
	configJSON, _ := json.Marshal(config)
	c.position++
	c.scheme.Stages = append(c.scheme.Stages, store.SchemeStage{
		Code:      code,
		Title:     title,
		StageType: "matches",
		Kind:      "matches",
		Position:  c.position,
		Matches:   matches,
		Config:    configJSON,
	})
}

// rejectRoundKeys fails on dotted overrides whose suffix names no round of
// this block (rounds nil = the kind has no addressable rounds).
func (c *compiler) rejectRoundKeys(blk Section, rounds []string) error {
	known := map[string]bool{}
	for _, code := range rounds {
		known[code] = true
	}
	for key, v := range blk.Values {
		_, suffix, dotted := strings.Cut(key, ".")
		if !dotted {
			continue
		}
		if !known[suffix] {
			return errAt(v.Line, "%s: в этом блоке нет раунда %s", key, suffix)
		}
	}
	return nil
}
