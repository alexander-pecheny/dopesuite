package schemedsl

import (
	"encoding/json"
	"fmt"
	"strings"

	"dope/dope/domain/expr"
	"dope/dope/domain/protocol"
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
	if err := uniqueCodes(c.scheme); err != nil {
		return store.FestScheme{}, err
	}
	if err := uniqueSlugs(c.scheme); err != nil {
		return store.FestScheme{}, err
	}
	return c.scheme, nil
}

// uniqueSlugs refuses slugs that collide: a slug is a synthetic stage code on
// the client, so two blocks sharing one — or a slug shadowing a real stage
// code — would key two different tabs the same.
func uniqueSlugs(scheme store.FestScheme) error {
	codes := map[string]bool{}
	for _, stage := range scheme.Stages {
		codes[stage.Code] = true
	}
	owner := map[string]string{}
	for _, stage := range scheme.Stages {
		if stage.Slug == "" {
			continue
		}
		if codes[stage.Slug] {
			return fmt.Errorf("slug %q совпадает с кодом этапа — вкладки перепутаются", stage.Slug)
		}
		block := stage.Grain.Block
		if held, taken := owner[stage.Slug]; taken && held != block {
			return fmt.Errorf("slug %q носят два блока — вкладки перепутаются", stage.Slug)
		}
		owner[stage.Slug] = block
	}
	return nil
}

// uniqueCodes refuses a scheme whose stages or бои collide. The database says
// the same thing — unique(game_id, code) — but it says it as a raw constraint
// error at insert time, half a screen away from the scheme that caused it.
func uniqueCodes(scheme store.FestScheme) error {
	stages := map[string]bool{}
	matches := map[string]string{}
	for _, stage := range scheme.Stages {
		if stages[stage.Code] {
			return fmt.Errorf("этап %q собран дважды — у схемы столкнулись коды", stage.Code)
		}
		stages[stage.Code] = true
		for _, match := range stage.Matches {
			if where, taken := matches[match.Code]; taken {
				return fmt.Errorf("бой %q есть и в этапе %q, и в %q", match.Code, where, stage.Code)
			}
			matches[match.Code] = stage.Code
		}
	}
	return nil
}

// Metrics where less is better: a place, a lot, a loss.
var ascendingMetrics = map[string]bool{"place_sum": true, "draw": true, "place": true, "losses": true}

// rankable is everything a stage of this Kind may sort by in this game: what
// the Protocol declares, what the Kind's Ranker adds, and what the scheme's own
// scoring rules define.
func (c *compiler) rankable(kind string) map[string]bool {
	names := map[string]bool{}
	for _, name := range protocol.Metrics(c.in.GameType) {
		names[name] = true
	}
	for _, name := range structure.RankerMetrics(kind) {
		names[name] = true
	}
	for _, name := range c.ruleMetrics {
		names[name] = true
	}
	return names
}

// sortDir выбирает направление: как написал автор, иначе по смыслу метрики.
func sortDir(rule SortRule) string {
	if rule.Dir != "" {
		return rule.Dir
	}
	if ascendingMetrics[rule.Metric] {
		return "asc"
	}
	return "desc"
}

type compiler struct {
	doc    *Doc
	in     Input
	scheme store.FestScheme

	ruleMetrics []string // метрики, определённые правилами подсчёта схемы

	venueCount  int
	position    int
	prev        *structure.Outputs
	blockStages [][]string
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
	c.assignLetters()
	return nil
}

// assignLetters deals every бой its буква — A..Z, then AA.. — in schedule
// order over the whole Game: Block, then stage, then бой. A block may decline
// (`letters: false`): the письменный отбор is one sitting for everyone and
// is not called a бой.
func (c *compiler) assignLetters() {
	dealt := 0
	for i := range c.scheme.Stages {
		stage := &c.scheme.Stages[i]
		var block int
		if _, err := fmt.Sscanf(stage.Grain.Block, "s%d", &block); err != nil || block < 1 || block > len(c.doc.Blocks) {
			continue
		}
		if letters, ok := c.doc.Blocks[block-1].Bool("letters"); ok && !letters {
			continue
		}
		for m := range stage.Matches {
			stage.Matches[m].Letter = BoutLetter(dealt)
			dealt++
		}
	}
}

// BoutLetter is the буква of the n-th бой (0-based): A..Z, then AA, AB.. —
// the sheets' handle, base-26 without a zero.
func BoutLetter(n int) string {
	label := ""
	for k := n + 1; k > 0; k = (k - 1) / 26 {
		label = string(rune('A'+(k-1)%26)) + label
	}
	return label
}

// --- vocabulary ------------------------------------------------------------

// Every block takes these; each Kind adds its own below. A key a Kind does not
// read is refused, so nothing is dropped on the floor.
var commonKeys = []string{"kind", "title", "venues", "sorting", "reseed", "stats_from", "letters", "proceeding_participants"}

// dottedKeys take a round suffix (`title.final`, `venues.r2`) on every Kind;
// a Protocol param does too, and a Kind's Round keys; bout./standings.
// suffixes are metric names (scoring rules).
var dottedKeys = []string{"venues", "title", "bout", "standings"}

var defaultsKeys = map[string]bool{"venues": true, "sorting": true}
var initKeys = map[string]bool{"seed": true, "sorting": true}

func keySet(lists ...[]string) map[string]bool {
	set := map[string]bool{}
	for _, list := range lists {
		for _, key := range list {
			set[key] = true
		}
	}
	return set
}

func (c *compiler) checkKeys() error {
	params := map[string]bool{}
	for _, param := range protocol.Params(c.in.GameType) {
		params[param.Key] = true
	}
	inDefaults := keySet(structure.SortedNames(defaultsKeys), structure.SortedNames(params))
	for _, word := range structure.Words() {
		macro, _ := structure.MacroFor(word)
		for _, key := range macro.Keys() {
			if key.Cascade {
				inDefaults[key.Name] = true
			}
		}
	}
	for key, v := range c.doc.Defaults.Values {
		if !inDefaults[key] {
			return errAt(v.Line, "неизвестный ключ %s в [defaults] (есть %s)", key, strings.Join(structure.SortedNames(inDefaults), ", "))
		}
	}
	for key, v := range c.doc.Init.Values {
		if !initKeys[key] {
			return errAt(v.Line, "неизвестный ключ %s в [init] (есть %s)", key, strings.Join(structure.SortedNames(initKeys), ", "))
		}
	}
	for _, blk := range c.doc.Blocks {
		kind, _ := blk.Str("kind")
		macro, ok := structure.MacroFor(kind)
		if !ok {
			continue // expandBlock names the kind it does not know
		}
		known := keySet(commonKeys, structure.SortedNames(params))
		dotted := keySet(dottedKeys, structure.SortedNames(params))
		for _, key := range macro.Keys() {
			known[key.Name] = true
			if key.Round {
				dotted[key.Name] = true
			}
		}
		for key, v := range blk.Values {
			base, _, isDotted := strings.Cut(key, ".")
			if isDotted {
				if !dotted[base] {
					return errAt(v.Line, "неизвестный ключ %s (раунд дописывают к %s)", key, strings.Join(structure.SortedNames(dotted), ", "))
				}
				continue // round suffixes are validated by the block's kind
			}
			if !known[key] {
				return errAt(v.Line, "неизвестный ключ %s (есть %s)", key, strings.Join(structure.SortedNames(known), ", "))
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
			seeding.Sort = append(seeding.Sort, store.SchemeSortRule{Metric: rule.Metric, Dir: sortDir(rule)})
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
		if teams, ok := blk.Int("participants"); ok && teams/2 > need {
			need = teams / 2
		}
	}
	for i := 1; i <= need; i++ {
		c.scheme.Venues = append(c.scheme.Venues, store.SchemeVenue{Number: i, Title: fmt.Sprintf("Стол %d", i)})
	}
	c.venueCount = need
	return nil
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

func paramIntList(defaults, blk Section, key string, rounds []string) ([]int, bool) {
	for _, section := range append(roundSections(blk, key, rounds), blk, defaults) {
		if v, ok, err := section.IntList(key); ok && err == nil {
			return v, true
		}
	}
	return nil, false
}

// roundSections is a section per round override of key — the same cascade
// paramInt walks by hand.
func roundSections(blk Section, key string, rounds []string) []Section {
	var out []Section
	for _, round := range rounds {
		if v, ok := blk.Values[key+"."+round]; ok {
			out = append(out, Section{Line: v.Line, Values: map[string]Value{key: v}})
		}
	}
	return out
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

// stageConfig is a stage's config on the wire: the Kind's typed config (nil
// for a hand-drawn stage) with the Protocol's params beside it.
func (c *compiler) stageConfig(cfg any, blk Section, rounds []string) (json.RawMessage, error) {
	config := c.protocolConfig(blk, rounds)
	if cfg != nil {
		data, err := json.Marshal(cfg)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &config); err != nil {
			return nil, err
		}
	}
	return json.Marshal(config)
}

// protocolConfig collects the game's protocol params for one stage (rounds
// empty means the block default).
func (c *compiler) protocolConfig(blk Section, rounds []string) map[string]any {
	config := map[string]any{}
	for _, param := range protocol.Params(c.in.GameType) {
		switch {
		case param.Bool:
			if v, ok := paramBool(c.doc.Defaults, blk, param.Key, rounds); ok {
				config[param.Config] = v
			}
		case param.List:
			if v, ok := paramIntList(c.doc.Defaults, blk, param.Key, rounds); ok {
				config[param.Config] = v
			}
		default:
			if v, ok := paramInt(c.doc.Defaults, blk, param.Key, rounds); ok {
				config[param.Config] = v
			}
		}
		if _, ok := config[param.Config]; !ok && param.Default != 0 {
			config[param.Config] = param.Default
		}
	}
	return config
}

// blockRules reads the block's scoring rules — bout.<name> is evaluated per бой
// and summed, standings.<name> once over the sums (ADR-0008). Whatever they
// define becomes rankable, so `sorting:` may name it.
func (c *compiler) blockRules(blk Section) (*structure.Rules, error) {
	rules := &structure.Rules{}
	for key, value := range blk.Values {
		grain, name, dotted := strings.Cut(key, ".")
		if !dotted || name == "" {
			continue
		}
		switch grain {
		case "bout":
			if rules.Bout == nil {
				rules.Bout = map[string]string{}
			}
			rules.Bout[name] = value.Raw
		case "standings":
			if rules.Standings == nil {
				rules.Standings = map[string]string{}
			}
			rules.Standings[name] = value.Raw
		default:
			continue
		}
		if _, err := expr.Parse(value.Raw); err != nil {
			return nil, errAt(value.Line, "%s: %s", key, err)
		}
		c.ruleMetrics = append(c.ruleMetrics, name)
	}
	if rules.Empty() {
		return nil, nil
	}
	return rules, nil
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
			return nil, errAt(blk.Line, "схеме нужно %d участников, а посеяно %d", total, len(c.in.Entrants))
		}
		return c.dealSeeds(groups, size), nil
	}
	prev := c.prev
	if prev.Proceeding <= 0 {
		return nil, errAt(blk.Line, "предыдущему блоку нужен proceeding_participants, чтобы продолжить схему")
	}
	supply := len(prev.Groups) * prev.Proceeding
	if supply != total {
		return nil, errAt(blk.Line, "из предыдущего блока выходят %d участников, а блоку нужно %d", supply, total)
	}
	if incoming, _ := blockReseedSpec(blk); incoming {
		return c.dealReseed(index, blk, groups, size)
	}
	return c.dealDeterministic(blk, groups, size)
}

// blockReseedSpec parses the reseed key: `true` re-ranks the incoming Edge, a
// round code re-ranks at that boundary inside the block (se only), and `every`
// does both — the incoming Edge and every round after it, which is what ТПШ
// does and what `true` already means on a bracket with lives.
func blockReseedSpec(blk Section) (incoming bool, round string) {
	if v, ok := blk.Bool("reseed"); ok {
		return v, ""
	}
	if v, ok := blk.Str("reseed"); ok {
		if v == structure.ReseedEveryRound {
			return true, structure.ReseedEveryRound
		}
		return false, v
	}
	return false, ""
}

// reseedSortRules maps the block's sorting tokens onto reseed metrics; absent
// sorting keeps the canonical place_sum-then-taken order. Жребий closes every
// order — the resolver only lots ties when a draw rule is present to use them.
func (c *compiler) reseedSortRules(blk Section) ([]store.SchemeSortRule, error) {
	tokens, ok, err := blk.Sorting("sorting")
	if err != nil {
		return nil, err
	}
	var rules []store.SchemeSortRule
	if !ok {
		rules = []store.SchemeSortRule{{Metric: "place_sum", Dir: "asc"}, {Metric: "taken", Dir: "desc"}}
	}
	known := c.rankable("reseed")
	for _, token := range tokens {
		if !known[token.Metric] {
			return nil, errAt(blk.Line, "sorting: %s не считается на пересеве — ни протокол, ни правила подсчёта такой метрики не дают (есть %s)",
				token.Metric, strings.Join(structure.SortedNames(known), ", "))
		}
		rules = append(rules, store.SchemeSortRule{Metric: token.Metric, Dir: sortDir(token)})
	}
	return append(rules, store.SchemeSortRule{Metric: "draw", Dir: "asc"}), nil
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

// reseedStageBanded materialises one reseed Edge: teams is who is re-ranked
// (place selectors into the feeding round), sources is whose бои the stats
// are summed over, bands how many Losses each team carries — the ranking
// runs inside a band. Returns the stage code; rank refs against it seat what
// follows.
func (c *compiler) reseedStageBanded(code string, where at, blk Section, sources []string, teams []store.SchemeSlot, bands []int) (string, error) {
	sort, err := c.reseedSortRules(blk)
	if err != nil {
		return "", err
	}
	c.position++
	c.scheme.Stages = append(c.scheme.Stages, store.SchemeStage{
		Code:      code,
		Title:     "Пересев",
		StageType: "reseed",
		Kind:      "reseed",
		Position:  c.position,
		Grain:     where.grain(),
		Teams:     teams,
		Bands:     bands,
		Sources:   sources,
		Sort:      json.RawMessage(util.MustJSON(sort)),
	})
	return code, nil
}

func (c *compiler) prevStageCodes() []string {
	codes := make([]string, len(c.prev.Groups))
	for i, g := range c.prev.Groups {
		codes[i] = g.Stage
	}
	return codes
}

// reseedSources resolves what a reseed sums its stats over: `otherwise` (the
// previous block, or the round before a boundary), or with stats_from every
// stage of the listed blocks (регламент КИНСБФ 3.3.5 counts both the groups
// and the DE) — the block being expanded meaning `self`, its rounds so far.
func (c *compiler) reseedSources(index int, blk Section, otherwise, self []string) ([]string, error) {
	tokens, ok, err := blk.List("stats_from")
	if err != nil {
		return nil, err
	}
	if !ok {
		return otherwise, nil
	}
	var sources []string
	for _, token := range tokens {
		var n int
		last := index
		if self != nil {
			last++
		}
		if _, err := fmt.Sscanf(token, "s%d", &n); err != nil || n < 1 || n > last {
			return nil, errAt(blk.Values["stats_from"].Line, "stats_from: %s — доступны блоки s1..s%d", token, last)
		}
		if n == index+1 {
			sources = append(sources, self...)
			continue
		}
		sources = append(sources, c.blockStages[n-1]...)
	}
	return sources, nil
}

// prevPlaceSlots is the reseed's eligibility set: the previous block's
// proceeding places.
func (c *compiler) prevPlaceSlots() []store.SchemeSlot {
	var teams []store.SchemeSlot
	for _, g := range c.prev.Groups {
		for p := 1; p <= c.prev.Proceeding; p++ {
			teams = append(teams, g.Place(p))
		}
	}
	return teams
}

// dealReseed materialises the block-grain Edge: a reseed stage over every
// previous group, then a snake deal of its ranks.
func (c *compiler) dealReseed(index int, blk Section, groups, size int) ([][]store.SchemeSlot, error) {
	if supply := len(c.prev.Groups) * c.prev.Proceeding; supply != groups*size {
		return nil, errAt(blk.Line, "из предыдущего блока выходят %d участников, а блоку нужно %d", supply, groups*size)
	}
	sources, err := c.reseedSources(index, blk, c.prevStageCodes(), nil)
	if err != nil {
		return nil, err
	}
	blockCode := fmt.Sprintf("s%d", index+1)
	code, err := c.reseedStageBanded(blockCode+"-reseed", at{block: blockCode}, blk, sources, c.prevPlaceSlots(), nil)
	if err != nil {
		return nil, err
	}
	out := make([][]store.SchemeSlot, groups)
	for g, ranks := range snakeDeal(groups, size) {
		for _, rank := range ranks {
			out[g] = append(out[g], structure.ReseedRank(code, rank))
		}
	}
	return out, nil
}

// dealDeterministic is the reference cross template: source groups arranged in
// rows of G_new; the new group at column i takes place 1 from column i and
// place 2 from its partner column (i xor 1), row by row.
func (c *compiler) dealDeterministic(blk Section, groups, size int) ([][]store.SchemeSlot, error) {
	prev := c.prev
	sourceGroups := len(prev.Groups)
	rows := sourceGroups / groups
	needsReseed := func(why string) error {
		return errAt(blk.Line, "%s — добавьте reseed: true", why)
	}
	if prev.Proceeding != 2 {
		return nil, needsReseed("детерминированная рассадка определена для proceeding_participants: 2")
	}
	if rows*groups != sourceGroups || groups%2 != 0 {
		return nil, needsReseed("нет шаблона рассадки из этих групп")
	}
	if size != 2*rows {
		return nil, needsReseed("нет шаблона рассадки из этих групп")
	}
	out := make([][]store.SchemeSlot, groups)
	for g := 0; g < groups; g++ {
		for row := 0; row < rows; row++ {
			// The pair this Group crosses with, taken in source-Group order: the
			// winner out of its own and the runner-up out of its partner. Reading
			// own-Group-first instead seats the same four but pairs them in a
			// different заход, and a бой's turn at the стол is a fact about the
			// tournament like any other.
			for column := g &^ 1; column <= g|1; column++ {
				place := 2
				if column == g {
					place = 1
				}
				out[g] = append(out[g], prev.Groups[row*groups+column].Place(place))
			}
		}
	}
	return out, nil
}

// --- block expansion -------------------------------------------------------

func (c *compiler) expandBlock(index int) error {
	blk := c.doc.Blocks[index]
	if index > 0 && c.prev.Terminal {
		return errAt(blk.Line, "предыдущий блок кончается серией боёв — за ним нельзя продолжить схему")
	}
	if v, present := blk.Values["stats_from"]; present {
		if incoming, boundary := blockReseedSpec(blk); !incoming && boundary == "" {
			return errAt(v.Line, "stats_from работает только вместе с reseed")
		}
	}
	kind, _ := blk.Str("kind")
	if kind == "" {
		return errAt(blk.Line, "блоку нужен kind")
	}
	macro, ok := structure.MacroFor(kind)
	if !ok {
		return errAt(blk.Line, "неизвестный kind %s (есть %s)", kind, strings.Join(structure.Words(), ", "))
	}
	firstStage := len(c.scheme.Stages)
	out, err := c.expandThrough(index, blk, macro)
	if err != nil {
		return err
	}
	var emitted []string
	for _, stage := range c.scheme.Stages[firstStage:] {
		if stage.StageType == "matches" {
			emitted = append(emitted, stage.Code)
		}
	}
	c.blockStages = append(c.blockStages, emitted)
	if proceeding, ok := blk.Int("proceeding_participants"); ok {
		out.Proceeding = proceeding
	}
	c.prev = out
	return nil
}

// expandThrough hands one Block to its Kind's Macro through the compiler's
// adapter and pins whatever it complains about.
func (c *compiler) expandThrough(index int, blk Section, macro structure.Macro) (*structure.Outputs, error) {
	rules, err := c.blockRules(blk)
	if err != nil {
		return nil, err
	}
	keys := map[string]structure.Key{}
	for _, key := range macro.Keys() {
		keys[key.Name] = key
	}
	b := &blockHandle{c: c, index: index, blk: blk, keys: keys, rules: rules}
	out, err := macro.Expand(b)
	if err != nil {
		return nil, b.pin(err)
	}
	return &out, nil
}

// roundTitle lets a scheme name a round itself — `title.r1: 1/16 финала` —
// falling back to the derived name. The traditional 1/N names are arithmetic
// only in a bracket that halves; anywhere else they are the tournament's own
// word for the round, so the scheme says them.
//
// A titled block among other blocks says whose round it is — «Плей-офф.
// 1 этап» — the same way a группа carries its block's title. A scheme of one
// block has nothing to tell apart, so ЭК's «1/16 финала» stays bare.
func (c *compiler) roundTitle(blk Section, names []string, derived string) string {
	title := derived
	for _, name := range names {
		if named, ok := blk.Str("title." + name); ok {
			title = named
			break
		}
	}
	if block, ok := blk.Str("title"); ok && len(c.doc.Blocks) > 1 {
		return block + ". " + title
	}
	return title
}

func (c *compiler) groupTitle(blk Section, group, groups int) string {
	if title, ok := blk.Str("title"); ok {
		if groups == 1 {
			return title
		}
		return fmt.Sprintf("%s. Группа %d", title, group)
	}
	if groups == 1 && len(c.doc.Blocks) == 1 {
		return "Группа"
	}
	return fmt.Sprintf("Группа %d", group)
}

func blockTitle(blk Section, fallback string) string {
	if title, ok := blk.Str("title"); ok {
		return title
	}
	return fallback
}

// at is where a stage sits: which Block it expands, which turn at the столы it
// is, and — for the Kinds whose stage is one Round — which Round its бои play.
// A stage spanning several Rounds (a round-robin Group, a DE pod) leaves round
// zero and lets each бой carry its own.
type at struct {
	block string
	round int
	wave  int
	group string
}

func (a at) grain() store.SchemeGrain {
	wave := a.wave
	if wave == 0 {
		wave = 1
	}
	return store.SchemeGrain{Block: a.block, Wave: wave, Group: a.group}
}

// appendManualStage adds a hand-drawn stage: its бои as the compiler laid
// them out, no Kind config of its own.
func (c *compiler) appendManualStage(blk Section, code, title string, rounds []string, where at, matches []store.SchemeMatch) {
	c.appendDrawnStage("matches", nil, blk, code, title, rounds, where, matches)
}

func (c *compiler) appendDrawnStage(kind string, cfg any, blk Section, code, title string, rounds []string, where at, matches []store.SchemeMatch) {
	configJSON, _ := c.stageConfig(cfg, blk, rounds)
	if where.round > 0 {
		for i := range matches {
			if matches[i].Round == 0 {
				matches[i].Round = where.round
			}
		}
	}
	c.position++
	c.scheme.Stages = append(c.scheme.Stages, store.SchemeStage{
		Code:      code,
		Title:     title,
		StageType: "matches",
		Kind:      kind,
		Position:  c.position,
		Grain:     where.grain(),
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
		prefix, suffix, dotted := strings.Cut(key, ".")
		if !dotted || prefix == "bout" || prefix == "standings" {
			continue // a scoring rule's suffix is a metric name, not a round
		}
		if !known[suffix] {
			return errAt(v.Line, "%s: в этом блоке нет раунда %s (есть %s)", key, suffix, strings.Join(structure.SortedNames(known), ", "))
		}
		if prefix == "match_size" && !strings.HasPrefix(suffix, "r") {
			return errAt(v.Line, "%s: размер боя задаётся по номеру раунда, match_size.r%s", key, "N")
		}
	}
	return nil
}
