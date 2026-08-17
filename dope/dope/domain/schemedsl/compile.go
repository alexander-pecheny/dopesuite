package schemedsl

import (
	"encoding/json"
	"fmt"
	"sort"
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

const (
	kindFlat = "flat"
	kindRR   = "roundrobin"
	kindSE   = "single_elimination"
	kindDE   = "double_elimination"
)

var canonOrder = []string{"points", "h2h", "taken", "diff"}

// Спеллинги, которые DSL принимал раньше и продолжает принимать.
var rrMetricAliases = map[string]string{"head2head": "h2h"}

// На пересеве points исторически значит «сумма мест по возрастанию» — это
// алиас, а не метрика, поэтому он разворачивается в правило целиком.
var reseedMetricAliases = map[string]store.SchemeSortRule{
	"points": {Metric: "place_sum", Dir: "asc"},
}

// Метрики, которые всегда меньше-лучше: место и жребий.
var ascendingMetrics = map[string]bool{"place_sum": true, "draw": true, "place": true, "losses": true}

// rankable — всё, по чему в этой игре можно сортировать этап данного Kind: что
// объявил Протокол, что добавляет Ranker этого Kind, и что определили правила
// подсчёта самой схемы.
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
	prev        *blockOutputs
	blockStages [][]string
}

// blockOutputs is what one expanded block offers the next block's Edge: per
// Group, a way to reference each place. terminal marks a block nothing can
// follow (its final is a series, so places have no single source bout).
type blockOutputs struct {
	groups     []groupOut
	proceeding int
	terminal   bool
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

var blockKeys = map[string]bool{
	"type": true, "title": true, "slug": true, "groups": true, "teams_in_group": true, "teams": true,
	"proceeding_teams": true, "reseed": true, "sorting": true, "points": true,
	"venues": true, "bronze": true, "stats_from": true, "best_of": true,
	"match_size": true, "winning_places": true, "rounds": true, "letters": true,
}

var defaultsKeys = map[string]bool{"venues": true, "sorting": true, "points": true}
var initKeys = map[string]bool{"seed": true, "sorting": true}

func (c *compiler) protocolConfigKey(base string) (string, bool) {
	for _, param := range protocol.Params(c.in.GameType) {
		if param.Key == base {
			return param.Config, true
		}
	}
	return "", false
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
				if _, isParam := c.protocolConfigKey(base); !isParam && base != "venues" && base != "best_of" &&
					base != "match_size" && base != "title" && base != "bout" && base != "standings" {
					return errAt(v.Line, "неизвестный ключ %s", key)
				}
				continue // round suffixes are validated by the block's kind
			}
			if _, isParam := c.protocolConfigKey(key); !blockKeys[key] && !isParam {
				return errAt(v.Line, "неизвестный ключ %s", key)
			}
			// Only roundrobin reads it; anywhere else it would be silently
			// dropped and the author would meet the s2-… URLs in production.
			if key == "slug" {
				if kind, _ := blk.Str("type"); kind != kindRR {
					return errAt(v.Line, "slug умеет только roundrobin — у блока %s его вкладки кода не поменяют", kind)
				}
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
		if param.Bool {
			if v, ok := paramBool(c.doc.Defaults, blk, param.Key, rounds); ok {
				config[param.Config] = v
			}
		} else if v, ok := paramInt(c.doc.Defaults, blk, param.Key, rounds); ok {
			config[param.Config] = v
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

// rrOrder resolves the groups' comparator order. On a block with an incoming
// reseed the sorting key describes the Edge (reseedSortRules), so the groups
// fall back to [defaults] or the canon.
func (c *compiler) rrOrder(blk Section) ([]string, error) {
	var rules []SortRule
	var ok bool
	var err error
	if incoming, _ := blockReseedSpec(blk); !incoming {
		if rules, ok, err = blk.Sorting("sorting"); err != nil {
			return nil, err
		}
	}
	if !ok {
		if rules, ok, err = c.doc.Defaults.Sorting("sorting"); err != nil {
			return nil, err
		}
	}
	if !ok {
		return canonOrder, nil
	}
	known := c.rankable("rr")
	order := make([]string, len(rules))
	for i, rule := range rules {
		metric := rule.Metric
		if alias, ok := rrMetricAliases[metric]; ok {
			metric = alias
		}
		if !known[metric] {
			return nil, errAt(blk.Line, "sorting: %s не считается — ни протокол, ни правила подсчёта такой метрики не дают (есть %s)",
				rule.Metric, strings.Join(sortedNames(known), ", "))
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
// rounds (rr, de). The same kinds have no финал for a best_of series.
func rejectRoundReseed(blk Section) error {
	if _, round := blockReseedSpec(blk); round != "" {
		return errAt(blk.Values["reseed"].Line, "reseed: в этом блоке нет раунда %s — только true/false", round)
	}
	if v, ok := blk.Values["best_of"]; ok {
		return errAt(v.Line, "best_of: серия возможна только в финале single_elimination")
	}
	return nil
}

// blockReseedSpec parses the reseed key: `true` re-ranks the incoming Edge, a
// round code re-ranks at that boundary inside the block (se only), and `every`
// does both — the incoming Edge and every round after it, which is what ТПШ
// does and what `true` already means on a bracket with lives.
const reseedEveryRound = "every"

func blockReseedSpec(blk Section) (incoming bool, round string) {
	if v, ok := blk.Bool("reseed"); ok {
		return v, ""
	}
	if v, ok := blk.Str("reseed"); ok {
		if v == reseedEveryRound {
			return true, reseedEveryRound
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
		if alias, ok := reseedMetricAliases[token.Metric]; ok {
			rules = append(rules, alias)
			continue
		}
		if !known[token.Metric] {
			return nil, errAt(blk.Line, "sorting: %s не считается на пересеве — ни протокол, ни правила подсчёта такой метрики не дают (есть %s)",
				token.Metric, strings.Join(sortedNames(known), ", "))
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

// reseedStage materialises one reseed Edge: teams is who is re-ranked (place
// selectors into the feeding round), sources is whose bouts the stats are
// summed over. Returns the stage code; rank refs against it seat what follows.
func (c *compiler) reseedStage(index int, blk Section, sources []string, teams []store.SchemeSlot) (string, error) {
	blockCode := fmt.Sprintf("s%d", index+1)
	return c.reseedStageCoded(blockCode+"-reseed", at{block: blockCode}, blk, sources, teams)
}

func (c *compiler) reseedStageCoded(code string, where at, blk Section, sources []string, teams []store.SchemeSlot) (string, error) {
	return c.reseedStageBanded(code, where, blk, sources, teams, nil)
}

// reseedStageBanded is reseedStageCoded for a bracket with lives: bands says how
// many Losses each team carries, and the ranking runs inside a band.
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
	codes := make([]string, len(c.prev.groups))
	for i, g := range c.prev.groups {
		codes[i] = g.stageCode
	}
	return codes
}

// reseedSources resolves what a block-grain reseed sums its stats over: the
// previous block's stages, or — with stats_from — every stage of the listed
// blocks (регламент КИНСБФ 3.3.5 counts both the groups and the DE).
func (c *compiler) reseedSources(index int, blk Section) ([]string, error) {
	tokens, ok, err := blk.List("stats_from")
	if err != nil {
		return nil, err
	}
	if !ok {
		return c.prevStageCodes(), nil
	}
	var sources []string
	for _, token := range tokens {
		var n int
		if _, err := fmt.Sscanf(token, "s%d", &n); err != nil || n < 1 || n > index {
			return nil, errAt(blk.Values["stats_from"].Line, "stats_from: %s — доступны блоки s1..s%d", token, index)
		}
		sources = append(sources, c.blockStages[n-1]...)
	}
	return sources, nil
}

// prevPlaceSlots is the reseed's eligibility set: the previous block's
// proceeding places.
func (c *compiler) prevPlaceSlots() []store.SchemeSlot {
	var teams []store.SchemeSlot
	for _, g := range c.prev.groups {
		for p := 1; p <= c.prev.proceeding; p++ {
			teams = append(teams, g.place(p))
		}
	}
	return teams
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
	if supply := len(c.prev.groups) * c.prev.proceeding; supply != groups*size {
		return nil, errAt(blk.Line, "из предыдущего блока выходят %d команд, а блоку нужно %d", supply, groups*size)
	}
	sources, err := c.reseedSources(index, blk)
	if err != nil {
		return nil, err
	}
	code, err := c.reseedStage(index, blk, sources, c.prevPlaceSlots())
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
				out[g] = append(out[g], prev.groups[row*groups+column].place(place))
			}
		}
	}
	return out, nil
}

// --- block expansion -------------------------------------------------------

func (c *compiler) expandBlock(index int) error {
	blk := c.doc.Blocks[index]
	if index > 0 && c.prev.terminal {
		return errAt(blk.Line, "предыдущий блок кончается серией боёв — за ним нельзя продолжить схему")
	}
	if v, present := blk.Values["stats_from"]; present {
		if incoming, _ := blockReseedSpec(blk); !incoming {
			return errAt(v.Line, "stats_from работает только вместе с reseed: true")
		}
	}
	kind, _ := blk.Str("type")
	firstStage := len(c.scheme.Stages)
	var out *blockOutputs
	var err error
	switch kind {
	case kindFlat:
		out, err = c.expandFlat(index, blk)
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
	var emitted []string
	for _, stage := range c.scheme.Stages[firstStage:] {
		if stage.StageType == "matches" {
			emitted = append(emitted, stage.Code)
		}
	}
	c.blockStages = append(c.blockStages, emitted)
	if proceeding, ok := blk.Int("proceeding_teams"); ok {
		out.proceeding = proceeding
	}
	c.prev = out
	return nil
}

// expandFlat is the whole bracket of a flat game: one Match seating everyone.
// ОД and КСИ have always been this shape in the database; the Kind only lets a
// scheme say so.
func (c *compiler) expandFlat(index int, blk Section) (*blockOutputs, error) {
	teams, ok := blk.Int("teams")
	if !ok {
		if len(c.in.Entrants) == 0 {
			return nil, errAt(blk.Line, "flat: нужен teams")
		}
		teams = len(c.in.Entrants)
	}
	if err := c.rejectRoundKeys(blk, nil); err != nil {
		return nil, err
	}
	if err := rejectRoundReseed(blk); err != nil {
		return nil, err
	}
	proceeding, _ := blk.Int("proceeding_teams")
	venues, err := c.blockVenues(blk, nil)
	if err != nil {
		return nil, err
	}
	entrants, err := c.blockEntrants(index, blk, 1, teams)
	if err != nil {
		return nil, err
	}
	kind, ok := structure.ExpanderFor("flat")
	if !ok {
		return nil, errAt(0, "flat не зарегистрирован в реестре видов")
	}
	code := fmt.Sprintf("s%d", index+1)
	rules, err := c.blockRules(blk)
	if err != nil {
		return nil, err
	}
	cfg := structure.FlatConfig{Code: code, Entrants: entrants[0], Title: blockTitle(blk, "Игра"), Venue: c.venuePick(venues, 1), Rules: rules}
	if order, ok, err := blk.Sorting("sorting"); err != nil {
		return nil, err
	} else if ok {
		known := c.rankable("flat")
		for _, rule := range order {
			if !known[rule.Metric] {
				return nil, errAt(blk.Line, "sorting: %s не считается — ни протокол, ни правила подсчёта такой метрики не дают (есть %s)",
					rule.Metric, strings.Join(sortedNames(known), ", "))
			}
			cfg.Order = append(cfg.Order, rule.Metric)
		}
	}
	configJSON, err := c.stageConfig(cfg, blk, nil)
	if err != nil {
		return nil, err
	}
	matches, err := kind.Schedule(configJSON)
	if err != nil {
		return nil, errAt(blk.Line, "%s", err.Error())
	}
	c.position++
	c.scheme.Stages = append(c.scheme.Stages, store.SchemeStage{
		Code:      code,
		Title:     blockTitle(blk, "Игра"),
		StageType: "matches",
		Kind:      "flat",
		Position:  c.position,
		Grain:     at{block: code}.grain(),
		Matches:   matches,
		Config:    configJSON,
	})
	label := blockTitle(blk, "Игра")
	return &blockOutputs{proceeding: proceeding, groups: []groupOut{{
		stageCode: code,
		label:     label,
		// The block's standings rank, not the бой's место. A бой shares a place
		// between seats that tie, and a shared place names nobody — ТПШ's отбор
		// has ties inside its top 24, and «место 10.5» cannot seat anyone. The
		// standings apply the block's whole sorting chain and rank distinctly.
		place: func(p int) store.SchemeSlot {
			return store.SchemeSlot{
				Reseed: &store.SchemeReseedRef{Stage: code, Rank: p},
				Label:  fmt.Sprintf("%s-%d", label, p),
			}
		},
	}}}, nil
}

// blockSlug reads a block's `slug:` — the readable URL handle its synthetic
// tabs use as their stage code. It lands in a URL path, so only what survives
// one unescaped is allowed.
func (c *compiler) blockSlug(blk Section) (string, error) {
	slug, ok := blk.Str("slug")
	if !ok {
		return "", nil
	}
	for _, r := range slug {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return "", errAt(blk.Line, "slug — латиница, цифры и дефис, а не %q", slug)
		}
	}
	return slug, nil
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
	rules, err := c.blockRules(blk)
	if err != nil {
		return nil, err
	}
	matchSize := 2
	if v, ok := blk.Int("match_size"); ok {
		matchSize = v
	}
	order, err := c.rrOrder(blk)
	if err != nil {
		return nil, err
	}
	points, err := c.rrPoints(blk)
	if err != nil {
		return nil, err
	}
	rr, ok := structure.ExpanderFor("rr")
	if !ok {
		return nil, errAt(0, "rr не зарегистрирован в реестре видов")
	}
	out := &blockOutputs{}
	blockCode := fmt.Sprintf("s%d", index+1)
	for g := 1; g <= groups; g++ {
		code := fmt.Sprintf("%s-g%d", blockCode, g)
		cfg := structure.RRConfig{
			Code:     code,
			Entrants: entrants[g-1],
			Order:    order,
			Points:   &structure.RRPoints{Win: float64(points[0]), Draw: float64(points[1]), Loss: float64(points[2])},
			Venue:    c.venuePick(venues, g),
			Rules:    rules,
		}
		if matchSize > 2 {
			cfg.MatchSize = matchSize
		}
		cfg.Rounds, _ = blk.Int("rounds")
		configJSON, err := c.stageConfig(cfg, blk, nil)
		if err != nil {
			return nil, err
		}
		matches, err := rr.Schedule(configJSON)
		if err != nil {
			return nil, errAt(blk.Line, "%s", err.Error())
		}
		slug, err := c.blockSlug(blk)
		if err != nil {
			return nil, err
		}
		c.position++
		c.scheme.Stages = append(c.scheme.Stages, store.SchemeStage{
			Code:      code,
			Title:     c.groupTitle(blk, g, groups),
			StageType: "matches",
			Kind:      "rr",
			Slug:      slug,
			Position:  c.position,
			Grain:     at{block: blockCode, group: groupCode(groups, g)}.grain(),
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
	winning := 1
	if v, ok := blk.Int("winning_places"); ok {
		winning = v
	}
	// match_size may differ round by round — ЭК plays its 1/4 three to a table
	// and everything else four — so the size is asked for per round.
	sizeFor := func(round, entering int) int {
		size := 2
		if v, ok := blk.Int("match_size"); ok {
			size = v
		}
		if v, ok := blk.Int(fmt.Sprintf("match_size.r%d", round)); ok {
			size = v
		}
		return size
	}
	maxRounds, _ := blk.Int("rounds")
	plan, err := planElimRounds(teams, winning, maxRounds, sizeFor)
	if err != nil {
		return nil, errAt(blk.Line, "single_elimination: %s", err)
	}
	bronze, _ := blk.Bool("bronze")
	rounds := []string{}
	for i, r := range plan {
		rounds = append(rounds, elimRoundNames(r, i, winning)...)
	}
	if bronze && teams >= 4 {
		rounds = append(rounds, "bronze")
	}
	if err := c.rejectRoundKeys(blk, rounds); err != nil {
		return nil, err
	}
	_, boundary := blockReseedSpec(blk)
	everyRound := boundary == reseedEveryRound
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
			return nil, errAt(blk.Values["reseed"].Line, "reseed: в этом блоке нет раунда %s", boundary)
		}
		if boundaryAt == teams {
			return nil, errAt(blk.Values["reseed"].Line, "reseed: %s — первый раунд, пишите reseed: true", boundary)
		}
	}

	first, err := c.seFirstRound(index, blk, plan[0], winning)
	if err != nil {
		return nil, err
	}
	blockCode := fmt.Sprintf("s%d", index+1)
	prevCodes := []string{}
	var prevStages []string
	var semifinalCodes []string
	seriesFinal := false
	for roundIndex, round := range plan {
		remaining, size, count := round.entering, round.size, round.bouts
		names := elimRoundNames(round, roundIndex, winning)
		stageCode := fmt.Sprintf("%s-%s", blockCode, names[0])
		bestOf := 0
		if round.terminal {
			v, ok := blk.Int("best_of")
			for _, name := range names {
				if dotted, dok := blk.Int("best_of." + name); dok {
					v, ok = dotted, true
				}
			}
			if ok {
				if v < 3 || v%2 == 0 {
					return nil, errAt(blk.Line, "best_of: серия играется до большинства побед — нечётное число боёв от 3")
				}
				bestOf = v
			}
		} else {
			for _, name := range names {
				if _, ok := blk.Int("best_of." + name); ok {
					return nil, errAt(blk.Values["best_of."+name].Line, "best_of: серия возможна только в финале")
				}
			}
		}
		var reseedCode string
		switch {
		case everyRound && roundIndex > 0:
			// Every place that survived the round before, best бой first — the
			// reseed's own sorting decides the rest.
			alive := make([]store.SchemeSlot, 0, len(prevCodes)*winning)
			for _, prev := range prevCodes {
				for place := 1; place <= winning; place++ {
					alive = append(alive, fromMatchSlot(prev, place))
				}
			}
			code := fmt.Sprintf("%s-r%d-reseed", blockCode, roundIndex+1)
			if reseedCode, err = c.reseedStageCoded(code, at{block: blockCode, round: roundIndex + 1}, blk, prevStages, alive); err != nil {
				return nil, err
			}
		case remaining == boundaryAt:
			winners := make([]store.SchemeSlot, len(prevCodes))
			for i, prev := range prevCodes {
				winners[i] = fromMatchSlot(prev, 1)
			}
			if reseedCode, err = c.reseedStage(index, blk, prevStages, winners); err != nil {
				return nil, err
			}
		}
		venues, err := c.blockVenues(blk, names)
		if err != nil {
			return nil, err
		}
		matches := make([]store.SchemeMatch, count)
		codes := make([]string, count)
		// Who meets whom: a reseed re-ranks everyone and deals the бои by the
		// draw, so the round's best meets its worst; without one the bracket
		// template carries each бой's winners forward in бой order.
		drawn := elimDraw(remaining, count, size, winning)
		template := straightChunks(remaining, count)
		for i := 1; i <= count; i++ {
			code := fmt.Sprintf("%s-m%d", stageCode, i)
			codes[i-1] = code
			var slots []store.SchemeSlot
			switch {
			case reseedCode != "":
				for _, rank := range drawn[i-1] {
					slots = append(slots, reseedRankSlot(reseedCode, rank))
				}
			case roundIndex == 0:
				slots = first[i-1]
			default:
				for _, rank := range template[i-1] {
					from, place := (rank-1)/winning, (rank-1)%winning+1
					slots = append(slots, labelledFromMatch(prevCodes[from], fmt.Sprintf("Бой %d", from+1), place))
				}
			}
			matches[i-1] = store.SchemeMatch{
				Code:             code,
				Title:            fmt.Sprintf("Бой %d", i),
				Venue:            c.venuePick(venues, i),
				ParticipantCount: len(slots),
				Slots:            slots,
			}
		}
		// The bronze бой is played before the final, so its stage stands before
		// the final's: the Сетка draws it first, and it deals its буква first.
		if round.terminal && bronze && len(semifinalCodes) == 2 {
			if err := c.appendBronze(blk, blockCode, semifinalCodes, roundIndex+1); err != nil {
				return nil, err
			}
		}
		if bestOf > 1 {
			// The series is sequential бои at one стол, so it never wave-splits.
			base := matches[0]
			series := make([]store.SchemeMatch, bestOf)
			for k := 1; k <= bestOf; k++ {
				series[k-1] = store.SchemeMatch{
					Code:             fmt.Sprintf("%s-m%d", stageCode, k),
					Title:            fmt.Sprintf("Финал. Бой %d", k),
					Venue:            base.Venue,
					ParticipantCount: 2,
					Slots:            base.Slots,
				}
			}
			c.appendManualStage(blk, stageCode, c.roundTitle(blk, names, elimRoundTitle(round, roundIndex, winning)), names,
				at{block: blockCode, round: roundIndex + 1}, series)
			seriesFinal = true
			prevCodes = codes
			continue
		}
		prevStages = c.appendSERound(blk, stageCode, c.roundTitle(blk, names, elimRoundTitle(round, roundIndex, winning)), names, venues,
			at{block: blockCode, round: roundIndex + 1}, matches)
		if remaining == 4 {
			semifinalCodes = codes
		}
		prevCodes = codes
	}
	if len(prevCodes) > 1 {
		// The bracket stopped short of a final, so its last round's бои are what
		// the block offers on: each is a Group sending its winning places.
		out := &blockOutputs{proceeding: winning}
		for i, code := range prevCodes {
			code := code
			out.groups = append(out.groups, groupOut{
				stageCode: code,
				label:     fmt.Sprintf("Бой %d", i+1),
				place:     func(p int) store.SchemeSlot { return fromMatchSlot(code, p) },
			})
		}
		return out, nil
	}
	finalCode := prevCodes[0]
	out := &blockOutputs{terminal: seriesFinal, groups: []groupOut{{
		stageCode: finalCode,
		label:     "Финал",
		place: func(p int) store.SchemeSlot {
			return fromMatchSlot(finalCode, p)
		},
	}}}
	return out, nil
}

// appendBronze emits the third-place бой between the two semifinal losers.
func (c *compiler) appendBronze(blk Section, blockCode string, semifinalCodes []string, round int) error {
	stageCode := blockCode + "-bronze"
	venues, err := c.blockVenues(blk, []string{"bronze"})
	if err != nil {
		return err
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
	c.appendManualStage(blk, stageCode, c.roundTitle(blk, []string{"bronze"}, "Матч за 3-е место"), []string{"bronze"},
		at{block: blockCode, round: round}, matches)
	return nil
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

// seFirstRound seats the opening round: bracket order over seeds, or the
// winner-meets-runner-up template over the previous block's paired groups.
func (c *compiler) seFirstRound(index int, blk Section, opening elimRound, winning int) ([][]store.SchemeSlot, error) {
	teams, count := opening.entering, opening.bouts
	if index == 0 {
		if len(c.in.Entrants) > 0 && len(c.in.Entrants) != teams {
			return nil, errAt(blk.Line, "схеме нужно %d команд, а посеяно %d", teams, len(c.in.Entrants))
		}
		draw := elimDraw(teams, count, opening.size, winning)
		first := make([][]store.SchemeSlot, count)
		for i := 0; i < count; i++ {
			for _, rank := range draw[i] {
				first[i] = append(first[i], c.seedSlot(rank))
			}
		}
		return first, nil
	}
	prev := c.prev
	if prev.proceeding <= 0 {
		return nil, errAt(blk.Line, "предыдущему блоку нужен proceeding_teams, чтобы продолжить схему")
	}
	// A пересев makes the бой's size irrelevant: it hands over a ranking, and the
	// snake deals that ranking into бои of any size — ТПШ opens on four seats.
	// Only the template below needs бои of two.
	if incoming, _ := blockReseedSpec(blk); incoming {
		dealt, err := c.dealReseed(index, blk, count, opening.size)
		if err != nil {
			return nil, err
		}
		return dealt, nil
	}
	if opening.size != 2 || winning != 1 {
		return nil, errAt(blk.Line, "нет шаблона рассадки в бои по %d из предыдущего блока — добавьте reseed: true", opening.size)
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

// expandDoubleElim expands an elimination where two Losses end a tournament —
// КИнСБФ's pods of four and личная СИ's whole play-off are the same Kind, told
// apart only by their size and by how many Participants leave the block.
//
// Pods (groups > 1) stay one stage each: a pod's five бои run in sequence at
// its own стол, so the pod is the unit a host works with. A single bracket
// emits one stage per round, because a round is what plays at once across every
// стол — and, when the block reseeds, what gets re-ranked between rounds.
func (c *compiler) expandDoubleElim(index int, blk Section) (*blockOutputs, error) {
	winning := 1
	if v, ok := blk.Int("winning_places"); ok {
		winning = v
	}
	size := 2
	if v, ok := blk.Int("match_size"); ok {
		size = v
	}
	groups, hasGroups := blk.Int("groups")
	perGroup, hasSize := blk.Int("teams_in_group")
	teams, hasTeams := blk.Int("teams")
	switch {
	case hasGroups && hasSize:
	case hasGroups && hasTeams:
		perGroup = teams / groups
	case hasSize && hasTeams:
		groups = teams / perGroup
	case hasTeams && size == 2 && winning == 1:
		// The classic pod: four to a group unless the scheme says otherwise.
		if teams%4 != 0 {
			return nil, errAt(blk.Line, "double_elimination: нужен groups (или teams, кратный 4)")
		}
		groups, perGroup = teams/4, 4
	case hasGroups && !hasTeams && !hasSize && size == 2 && winning == 1:
		perGroup = 4
	case hasTeams:
		groups, perGroup = 1, teams
	default:
		return nil, errAt(blk.Line, "double_elimination: нужен teams (или groups и teams_in_group)")
	}
	if groups < 1 || perGroup < 2 {
		return nil, errAt(blk.Line, "double_elimination: %d групп по %d — так не бывает", groups, perGroup)
	}
	proceeding := 1
	if v, ok := blk.Int("proceeding_teams"); ok {
		proceeding = v
	}
	// A пересев hands the block a ranking, so its opening round deals that
	// ranking as a snake like every later round does. Only the deterministic
	// template arrives pre-balanced, and slicing it in order is the point.
	opening := snakeChunks
	if reseeded, _ := blockReseedSpec(blk); index > 0 && !reseeded {
		opening = straightChunks
	}
	plan, err := planLivesDrawn(perGroup, 2, winning, proceeding,
		func(round, members int) int { return size }, opening)
	if err != nil {
		return nil, errAt(blk.Line, "double_elimination: %s", err)
	}
	roundNames := make([]string, len(plan.rounds))
	for i := range plan.rounds {
		roundNames[i] = fmt.Sprintf("r%d", i+1)
	}
	if err := c.rejectRoundKeys(blk, roundNames); err != nil {
		return nil, err
	}
	if err := rejectRoundReseed(blk); err != nil {
		return nil, err
	}
	venues, err := c.blockVenues(blk, roundNames)
	if err != nil {
		return nil, err
	}
	entrants, err := c.blockEntrants(index, blk, groups, perGroup)
	if err != nil {
		return nil, err
	}
	reranked, _ := blk.Bool("reseed")

	out := &blockOutputs{proceeding: proceeding}
	for g := 1; g <= groups; g++ {
		codes, stages, err := c.emitLivesBracket(index, blk, g, groups, plan, entrants[g-1], venues, reranked)
		if err != nil {
			return nil, err
		}
		label := fmt.Sprintf("DE %d", g)
		if groups == 1 {
			label = blockTitle(blk, "Плей-офф")
		}
		place := func(p int) store.SchemeSlot {
			if p < 1 || p > len(plan.survivor) {
				return store.SchemeSlot{}
			}
			source := plan.survivor[p-1]
			return fromMatchSlot(codes[source.bout], source.place)
		}
		out.groups = append(out.groups, groupOut{stageCode: stages[len(stages)-1], label: label, place: place})
	}
	return out, nil
}

// emitLivesBracket writes one bracket's stages and returns each planned бой's
// match code plus the stage codes, newest last.
func (c *compiler) emitLivesBracket(index int, blk Section, group, groups int, plan *dePlan, entrants []store.SchemeSlot, venues []int, reranked bool) ([]string, []string, error) {
	blockCode := fmt.Sprintf("s%d", index+1)
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
				alive = append(alive, fromMatchSlot(codes[source.bout], source.place))
			}
			var err error
			// The code is per bracket, not per block: several groups re-ranking
			// the same round would otherwise all claim `s1-r2-reseed` and the
			// insert would die on unique(game_id, code).
			roundReseed := fmt.Sprintf("%s-r%d-reseed", stageCode, r+1)
			if reseedCode, err = c.reseedStageBanded(roundReseed, at{block: blockCode, group: groupCode(groups, group)}, blk, prevStages, alive, bands); err != nil {
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
					slots = append(slots, reseedRankSlot(reseedCode, source.rank))
				case source.entrant != 0:
					slots = append(slots, entrants[source.entrant-1])
				default:
					slots = append(slots, fromMatchSlot(codes[source.bout], source.place))
				}
			}
			title := fmt.Sprintf("Бой %d", seq)
			if groups == 1 {
				title = fmt.Sprintf("Бой %d", i+1)
			}
			matches = append(matches, store.SchemeMatch{
				Code:             code,
				Title:            title,
				Venue:            c.venuePick(venues, boutIndex+1),
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
				match := podMatch(plan, codes, boutIndex, entrants, c.venuePick(venues, group))
				match.Round = roundOf[boutIndex]
				all = append(all, match)
			}
			c.appendPodStage(blk, stageCode, fmt.Sprintf("DE %d", group), plan,
				at{block: blockCode, group: groupCode(groups, group)}, all)
			return codes, []string{stageCode}, nil
		}
		names := []string{fmt.Sprintf("r%d", r+1)}
		c.appendManualStage(blk, roundStage, c.roundTitle(blk, names, fmt.Sprintf("Раунд %d", r+1)), names,
			at{block: blockCode, round: r + 1}, matches)
		prevStages = []string{roundStage}
		stages = append(stages, roundStage)
	}
	return codes, stages, nil
}

// roundEntrants lists everyone a re-ranked round seats, in the ranking order
// the model defines: bracket first, place second.
func roundEntrants(plan *dePlan, round int) []deSource {
	sources, _ := roundEntrantBands(plan, round)
	return sources
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
		slots = append(slots, fromMatchSlot(codes[source.bout], source.place))
	}
	return store.SchemeMatch{
		Code:             codes[boutIndex],
		Title:            fmt.Sprintf("Бой %d", boutIndex+1),
		Venue:            venue,
		ParticipantCount: len(slots),
		Slots:            slots,
	}
}

func blockTitle(blk Section, fallback string) string {
	if title, ok := blk.Str("title"); ok {
		return title
	}
	return fallback
}

func fromMatchSlot(matchCode string, place int) store.SchemeSlot {
	return store.SchemeSlot{FromMatch: &store.SchemeFromMatchRef{Match: matchCode, Place: place}}
}

// labelledFromMatch is fromMatchSlot with the label a host reads while the seat
// is still empty — «Бой 3, место 2», not the internal бой code.
func labelledFromMatch(matchCode, boutLabel string, place int) store.SchemeSlot {
	slot := fromMatchSlot(matchCode, place)
	slot.Label = fmt.Sprintf("%s, м. %d", boutLabel, place)
	return slot
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

// appendPodStage is a hand-drawn stage that ranks itself: Kind "de", whose
// config says what the Ranker counts a Loss by.
func (c *compiler) appendPodStage(blk Section, code, title string, plan *dePlan, where at, matches []store.SchemeMatch) {
	c.appendDrawnStage("de", structure.PodConfig{Lives: plan.lives, WinningPlaces: plan.winning}, blk, code, title, nil, where, matches)
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
			return errAt(v.Line, "%s: в этом блоке нет раунда %s", key, suffix)
		}
	}
	return nil
}

// sortedNames renders a name set for an error message, so a typo is answered
// with the list the author could have meant.
func sortedNames(set map[string]bool) []string {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// groupCode names a Group only where a Block has more than one: a single-group
// block is the Block, and labelling it «группа 1» invents a distinction the
// tournament does not make.
func groupCode(groups, group int) string {
	if groups <= 1 {
		return ""
	}
	return fmt.Sprint(group)
}
