// Package gamebuild is where a Game comes to exist: it takes a Spec — which
// fest, which format, its label, its scheme DSL and, when the fest's Games
// differ, its entrants — and materialises the Structure the DSL compiles to,
// numbering the entrants from 1 (ADR-0009), seating the seeds, and writing
// the stage, match and slot rows every other module reads. Recompile plays an
// edited DSL onto a live Game the same way, refusing to touch a Match that
// has begun. The formats that predate the DSL — OD's tours, KSI's themes, EK's
// pasted JSON — enter through the same Spec, so a caller never learns which
// is which.
package gamebuild

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"dope/dope/domain/games"
	"dope/dope/domain/imports"
	"dope/dope/domain/resolver"
	"dope/dope/domain/schemedsl"
	"dope/dope/platform/util"
	"dope/dope/storage/festwrite"
	"dope/dope/storage/store"
	"dope/dope/storage/storeutil"
	dopestrings "dope/i18nstrings"
	corei18n "pecheny.me/dopecore/i18nstrings"
)

type gameIdentity struct {
	Code     string
	Title    string
	Position int
}

func nextGameIdentityTx(ctx context.Context, tx *sql.Tx, festID int64, gameType, titleBase string) (gameIdentity, error) {
	var position int
	if err := tx.QueryRowContext(ctx, `select coalesce(max(position), 0) + 1 from games where fest_id = ?`, festID).Scan(&position); err != nil {
		return gameIdentity{}, err
	}
	// Suffix only to break a collision. A fest may hold two games of one type
	// under names of their own — Studchr played individual SI and TPSh, both
	// `si` — and numbering the second «TPSh 2» renames a tournament that had
	// a name.
	title := titleBase
	for n := 2; ; n++ {
		var taken int
		if err := tx.QueryRowContext(ctx, `select count(*) from games where fest_id = ? and title = ?`, festID, title).Scan(&taken); err != nil {
			return gameIdentity{}, err
		}
		if taken == 0 {
			break
		}
		title = fmt.Sprintf("%s %d", titleBase, n)
	}
	for suffix := position; ; suffix++ {
		code := fmt.Sprintf("%s-%d", gameType, suffix)
		var existing int
		if err := tx.QueryRowContext(ctx, `select count(*) from games where fest_id = ? and code = ?`, festID, code).Scan(&existing); err != nil {
			return gameIdentity{}, err
		}
		if existing == 0 {
			return gameIdentity{Code: code, Title: title, Position: position}, nil
		}
	}
}

// Spec is everything a Game is created from. Type is the format; Label the
// title the Game is offered under (a collision gets a numeric suffix); DSL
// its scheme, which every format is described in now — except the three
// that predate it, whose knobs ride below and are used only when DSL is
// empty. Entrants are which of the fest's Participants play, in seed order;
// absent, the whole fest plays, which is what a one-game fest wants.
type Spec struct {
	FestID   int64
	Type     string
	Label    string
	DSL      string
	Entrants []int64
	// The pre-DSL formats. OD: tours of so many questions; KSI: themes and an
	// optional stickers block; EK: a pasted detailed scheme — the ADR-0006
	// escape hatch, and the one road to the manual Kind.
	ODTours, ODQuestions int
	// KDTables is how many tables a friendship cup seats (a prime); its
	// tours and questions ride ODTours and ODQuestions.
	KDTables    int
	KSIThemes   int
	KSIStickers json.RawMessage
	// Multi: the minigames as the host wrote them, and the comparators that
	// break a tie on the total (empty: equal totals share a place).
	Minigames    []games.MultiGame
	MultiSorting []string
	Pasted       *store.FestScheme
}

// Create makes the Game the Spec describes and returns its id. A fest's Games
// rarely share an entrant list (ADR-0009): Studchr-2026 registered 65 teams,
// its OD seated all of them, its EK seated 48 and its Brain a different 48.
// Numbers are dealt from 1 inside the Game, so the same team is «2» in one
// and «4» in another.
func Create(ctx context.Context, tx *sql.Tx, spec Spec) (int64, error) {
	def, known := games.Lookup(spec.Type)
	if strings.TrimSpace(spec.DSL) != "" {
		// Multi is one sitting whose shape is its minigames, and the DSL
		// has no way to say what they are — so a scheme for one would compile
		// to a game that scores nothing. Refuse it rather than build it.
		if known && def.DSL == games.DSLRefused {
			return 0, corei18n.User(dopestrings.Default.Gamebuild.Create.MultiFromScheme())
		}
		return createSchemeGame(ctx, tx, spec.FestID, spec.Type, spec.Label, spec.DSL, spec.Entrants)
	}
	if spec.Pasted != nil {
		scheme := *spec.Pasted
		if scheme.GameType == "" {
			scheme.GameType = spec.Type
		}
		if scheme.GameType != spec.Type {
			return 0, corei18n.User(dopestrings.Default.Gamebuild.Create.JsonTypeMismatch(games.Label(scheme.GameType), games.Label(spec.Type)))
		}
		return Materialise(ctx, tx, spec.FestID, scheme)
	}
	switch {
	case known && def.Flat:
		// A flat format is one Match seating the whole fest roster under the
		// fest's own numbers, and who did not play is marked on its refusals
		// tab. It has no way to seat a chosen few, so a chosen list is refused
		// here rather than dropped on the floor.
		if len(spec.Entrants) > 0 {
			return 0, corei18n.User(dopestrings.Default.Gamebuild.Create.WholeRoster(games.Label(spec.Type)))
		}
		return createFlatGameTx(ctx, tx, spec.FestID, def, spec.Label, specShape(spec))
	case known && def.PastedScheme:
		return 0, corei18n.User(dopestrings.Default.Gamebuild.Create.EkNoScheme())
	}
	return 0, corei18n.User(dopestrings.Default.Gamebuild.Create.SchemeRequired())
}

// Materialise makes a Game from a pasted detailed scheme, in the fest given:
// the scheme row, the game row, its venues, and the Structure — stages with
// their Kind, matches with their letter (dealt here when the JSON brought none),
// slots left for a seed import to fill. It returns the game id. Teams travel
// by that import, never inside the JSON.
func Materialise(ctx context.Context, tx *sql.Tx, festID int64, scheme store.FestScheme) (int64, error) {
	if scheme.GameType == "" {
		scheme.GameType = games.Default
	}
	if err := storeutil.ValidateScheme(scheme); err != nil {
		return 0, err
	}
	if len(scheme.Teams) > 0 {
		return 0, corei18n.User(dopestrings.Default.Gamebuild.Create.PastedTeams())
	}
	title := strings.TrimSpace(scheme.Title)
	if title == "" {
		title = games.Label(scheme.GameType)
	}
	identity, err := nextGameIdentityTx(ctx, tx, festID, scheme.GameType, title)
	if err != nil {
		return 0, err
	}
	dealLetters(&scheme)
	schemaJSON, err := json.Marshal(scheme)
	if err != nil {
		return 0, err
	}
	now := util.UtcNow()
	schemeID, err := store.InsertReturningID(ctx, tx, `
insert into schemes(slug, title, version, schema_json, created_at)
values(?, ?, ?, ?, ?)`, uniqueSchemeSlug(scheme.Slug), title, util.MaxInt(scheme.SchemaVersion, 2), string(schemaJSON), now)
	if err != nil {
		return 0, err
	}
	gameID, err := store.InsertReturningID(ctx, tx, `
insert into games(fest_id, code, title, game_type, position, scheme_id, scheme_json, state_json, status, team_list_source, roster_source, revision, created_at, updated_at)
values(?, ?, ?, ?, ?, ?, ?, '{}', 'pending', 'fest', 'fest', 1, ?, ?)`,
		festID, identity.Code, title, scheme.GameType, identity.Position, schemeID, string(schemaJSON), now, now)
	if err != nil {
		return 0, err
	}
	return gameID, writePastedStructureTx(ctx, tx, festID, gameID, scheme)
}

// writePastedStructureTx writes a pasted scheme's venues and Structure with no
// seat resolved: its Participants arrive by seed import, and the resolver
// seats them then.
func writePastedStructureTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, scheme store.FestScheme) error {
	venues, err := upsertVenuesTx(ctx, tx, festID, scheme.Venues)
	if err != nil {
		return err
	}
	return writeStructureTx(ctx, tx, festID, gameID, scheme.GameType, scheme, venues, unseated, nil)
}

func unseated(store.SchemeSlot) any { return nil }

// dealLetters gives a pasted scheme's matches their letters the way the sheets
// do — A.. in stage order, then match order — when the JSON brought none. A
// sitting whose title does not match boutTitle (the written qualifier) is
// skipped, as the compiler skips a Block that declined letters.
func dealLetters(scheme *store.FestScheme) {
	for _, stage := range scheme.Stages {
		for _, match := range stage.Matches {
			if match.Letter != "" {
				return
			}
		}
	}
	dealt := 0
	for i := range scheme.Stages {
		for m := range scheme.Stages[i].Matches {
			match := &scheme.Stages[i].Matches[m]
			if !boutTitle.MatchString(match.Title) {
				continue
			}
			match.Letter = schemedsl.BoutLetter(dealt)
			dealt++
		}
	}
}

var boutTitle = regexp.MustCompile(`Бой\s+\d+`)

// createSchemeGame creates a game of any type from a scheme DSL: compile,
// store the scheme with its source, seat the entrants and materialise the
// structure. Every format reaches the same plumbing — the DSL is the way a
// bracket is described, not a Brain feature.
func createSchemeGame(ctx context.Context, tx *sql.Tx, festID int64, gameType, label, dsl string, entrants []int64) (int64, error) {
	identity, err := nextGameIdentityTx(ctx, tx, festID, gameType, label)
	if err != nil {
		return 0, err
	}
	doc, declared, err := parseSchemeDSL(gameType, dsl)
	if err != nil {
		return 0, err
	}
	entrants, placeholders, err := createEntrantsTx(ctx, tx, festID, gameType, declared, doc, entrants)
	if err != nil {
		return 0, err
	}
	input := schemedsl.Input{Slug: identity.Code, Title: identity.Title, GameType: gameType}
	var scheme store.FestScheme
	if placeholders > 0 {
		scheme, err = schemedsl.Compile(doc, withEmptySeats(input, placeholders))
	} else {
		scheme, err = compileForEntrantsTx(ctx, tx, festID, doc, declared, input, entrants, nil)
	}
	if err != nil {
		return 0, err
	}
	schemeJSON, err := json.Marshal(scheme)
	if err != nil {
		return 0, err
	}
	now := util.UtcNow()
	schemeID, err := store.InsertReturningID(ctx, tx, `
insert into schemes(slug, title, version, schema_json, created_at)
values(?, ?, 2, ?, ?)`, uniqueSchemeSlug(identity.Code), identity.Title, string(schemeJSON), now)
	if err != nil {
		return 0, err
	}
	gameID, err := store.InsertReturningID(ctx, tx, `
insert into games(fest_id, code, title, game_type, position, scheme_id, scheme_json, scheme_dsl, state_json, status, team_list_source, roster_source, revision, created_at, updated_at)
values(?, ?, ?, ?, ?, ?, ?, ?, '{}', 'active', 'fest', 'fest', 1, ?, ?)`,
		festID, identity.Code, identity.Title, gameType, identity.Position, schemeID, string(schemeJSON), dsl, now, now)
	if err != nil {
		return 0, err
	}
	if len(entrants) > 0 {
		if err := seatChosenTx(ctx, tx, gameID, entrants); err != nil {
			return 0, err
		}
	}
	if placeholders > 0 {
		// Empty seats stay empty: not the fest's teams, which a Game that named
		// no entrants would otherwise seat by their numbers.
		venues, err := schemeVenuesTx(ctx, tx, festID, gameID, scheme.Venues, nil)
		if err != nil {
			return 0, err
		}
		if err := writeStructureTx(ctx, tx, festID, gameID, gameType, scheme, venues, unseated, nil); err != nil {
			return 0, err
		}
		return gameID, nil
	}
	if err := writeCompiledStructureTx(ctx, tx, festID, gameID, gameType, scheme, nil); err != nil {
		return 0, err
	}
	if err := recordGameEntrantsTx(ctx, tx, gameID); err != nil {
		return 0, err
	}
	return gameID, nil
}

// parseSchemeDSL parses a Game's scheme DSL and reads what its [init]
// declares, which decides who the Game is compiled for.
func parseSchemeDSL(gameType, dsl string) (*schemedsl.Doc, imports.Declared, error) {
	if strings.TrimSpace(dsl) == "" {
		return nil, imports.Declared{}, corei18n.User(dopestrings.Default.Gamebuild.Create.SchemeRequired())
	}
	doc, err := schemedsl.Parse(dsl)
	if err != nil {
		return nil, imports.Declared{}, err
	}
	init, err := schemedsl.ReadInit(doc, gameType)
	if err != nil {
		return nil, imports.Declared{}, err
	}
	return doc, imports.DeclaredOfInit(init), nil
}

func schemeForEntrantsTx(ctx context.Context, tx *sql.Tx, festID int64, gameType, slug, title, dsl string, chosen []int64) (store.FestScheme, error) {
	doc, declared, err := parseSchemeDSL(gameType, dsl)
	if err != nil {
		return store.FestScheme{}, err
	}
	return compileForEntrantsTx(ctx, tx, festID, doc, declared, schemedsl.Input{Slug: slug, Title: title, GameType: gameType}, chosen, nil)
}

// compileForEntrantsTx compiles a scheme. Unseeded, it is compiled for the
// chosen entrants, or for the format's default ones when nobody was chosen.
// numbers, when given, is the number each chosen entrant already sits under
// (seatedNumbersTx); otherwise they are numbered 1… in the order given.
// Seeded, its seats are the seed's, and the sources the seed reads must be
// Games of this fest, or the import would fail later, at the host's button,
// rather than here.
func compileForEntrantsTx(ctx context.Context, tx *sql.Tx, festID int64, doc *schemedsl.Doc, declared imports.Declared, input schemedsl.Input, chosen []int64, numbers []int) (store.FestScheme, error) {
	if !declared.Seeded() {
		entrants, err := chosenEntrantsTx(ctx, tx, festID, input.GameType, declared, doc, chosen, numbers)
		if err != nil {
			return store.FestScheme{}, err
		}
		input.Entrants = entrants
		return schemedsl.Compile(doc, input)
	}
	switch source := declared.Source(); source.Kind {
	case imports.SourcePlayers:
		// A seed composed over players (Troika rules §4.4.2) reads the
		// standings of the Games its `games:` names.
		var games []string
		if declared.Players != nil {
			games = declared.Players.Games
		}
		for _, code := range games {
			known, err := festHasGameTx(ctx, tx, festID, code)
			if err != nil {
				return store.FestScheme{}, err
			}
			if !known {
				return store.FestScheme{}, corei18n.User(dopestrings.Default.Imports.Seed.GameMissing(code))
			}
		}
	case imports.SourceGame:
		known, err := festHasGameTx(ctx, tx, festID, source.Game)
		if err != nil {
			return store.FestScheme{}, err
		}
		if !known {
			return store.FestScheme{}, corei18n.User(dopestrings.Default.Gamebuild.Create.SeedUnknown(source.Game))
		}
	}
	return schemedsl.Compile(doc, input)
}

// placeholderSeats is how many empty seats a game is built with before it
// has an entrant: as many as its first stage sends on, the least its scheme
// can take, and two when it does not say.
func placeholderSeats(doc *schemedsl.Doc) int {
	if len(doc.Blocks) == 0 {
		return 2
	}
	if n, ok := doc.Blocks[0].Int("proceeding_participants"); ok && n > 1 {
		return n
	}
	if n, ok := doc.Blocks[0].Int("participants"); ok && n > 1 {
		return n
	}
	return 2
}

// withEmptySeats is the input for seats nobody sits in yet, numbered 1… like
// the seeds an entrant list deals.
func withEmptySeats(input schemedsl.Input, seats int) schemedsl.Input {
	input.Entrants = emptySeats(seats)
	return input
}

func emptySeats(seats int) []store.SchemeSlot {
	out := make([]store.SchemeSlot, 0, seats)
	for i := 1; i <= seats; i++ {
		out = append(out, store.SchemeSlot{Seed: &store.SchemeSeedRef{Basket: 1, Number: i}})
	}
	return out
}

func festHasGameTx(ctx context.Context, tx *sql.Tx, festID int64, code string) (bool, error) {
	var known int
	err := tx.QueryRowContext(ctx, `select count(*) from games where fest_id = ? and code = ?`, festID, code).Scan(&known)
	return known > 0, err
}

// stageEmptyState builds a match's pristine Protocol document for a stage of a
// compiled scheme. The Protocol owns the shape — a Brain match is a row of
// questions, an SI match a grid of themes — so the builder asks it rather
// than knowing. Falls back to Brain's for schemes compiled before Protocols
// carried their own config.
func stageEmptyState(gameType string, stage store.SchemeStage, seats, fallbackQuestions int) string {
	// A blob-shaped Protocol (EK, individual SI) stores an empty document: its
	// seats come from the Slots and its marks arrive as edits. Seeding it with
	// the Protocol's own state shape would write an array where the blob keys
	// a map, and the first edit would fail to parse it.
	if store.TeamBlobShaped(gameType) {
		return "{}"
	}
	p, ok := games.ProtocolOf(gameType)
	if !ok {
		return string(games.BrainEmptyStateJSON(stageQuestions(stage, fallbackQuestions)))
	}
	config := map[string]any{}
	if len(stage.Config) > 0 {
		_ = json.Unmarshal(stage.Config, &config)
	}
	config["participants"] = seats
	cfgJSON, err := json.Marshal(config)
	if err != nil {
		return "{}"
	}
	state, err := p.EmptyState(cfgJSON)
	if err != nil || len(state) == 0 {
		return "{}"
	}
	return string(state)
}

// writeCompiledStructureTx writes a compiled scheme's Structure, seating the
// fest's registry first unless a seed source owns the seats — «Import seed»
// writes game_assignments by seed rank, so creation must not pre-fill them by
// number — or the Game already named its entrants: seating the registry on
// top would add the teams this Game does not play.
// own is the venues this Game's bouts sat at before a clear deleted them, nil
// for a Game being created.
func writeCompiledStructureTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, gameType string, scheme store.FestScheme, own map[int64]bool) error {
	seated, err := hasAssignmentsTx(ctx, tx, gameID)
	if err != nil {
		return err
	}
	if scheme.Seeding == nil && !seated {
		if err := seatRosterTx(ctx, tx, festID, gameID, gameType); err != nil {
			return err
		}
	}
	seat, err := seedSeaterTx(ctx, tx, festID, gameID, gameType)
	if err != nil {
		return err
	}
	venues, err := schemeVenuesTx(ctx, tx, festID, gameID, scheme.Venues, own)
	if err != nil {
		return err
	}
	return writeStructureTx(ctx, tx, festID, gameID, gameType, scheme, venues, seat, nil)
}

// schemeVenuesTx gives the fest the venues a compiled scheme titles — Hamsa's
// `venues: [А, Б, В]` are the regulations' venues — and returns, by the
// scheme's own number, the fest venue each bout is seated at. The fest's
// venues are shared by its Games and the host retitles them, so a titled
// venue is found by its title wherever the fest numbers it: Octobearfest's
// Троечка and Своячок list the same rooms in different orders, and matching
// by number seated the Своячок's «Актовый зал» bouts in the Троечка's «Фойе».
// A title the fest does not have yet takes the scheme's number when no other
// Game seats a bout there — free, or this Game's own room the host retitled
// («Малый зал» for the scheme's «В»), which stays — and the next free number
// when another Game plays there. A table the compiler only
// numbered (`venues: 3`, or none given) carries the default title, which
// says nothing the number does not: it is the fest's venue of that number,
// and writes no row.
//
// A clear deletes the Game's bouts before it writes them again, so it hands
// over the rooms they sat at (own): a room there that the host renamed is
// still the Game's, even when another Game plays there too.
func schemeVenuesTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, venues []store.SchemeVenue, own map[int64]bool) (map[int]int64, error) {
	type festVenue struct {
		id     int64
		number int
		title  string
	}
	existing, err := store.CollectRows(ctx, tx, `select id, number, title from venues where fest_id = ? order by number`,
		[]any{festID}, func(rows *sql.Rows) (festVenue, error) {
			var v festVenue
			return v, rows.Scan(&v.id, &v.number, &v.title)
		})
	if err != nil {
		return nil, err
	}
	byTitle := map[string]int64{}
	byNumber := map[int]int64{}
	maxNumber := 0
	for _, v := range existing {
		if key := venueKey(v.title); key != "" {
			if _, seen := byTitle[key]; !seen {
				byTitle[key] = v.id
			}
		}
		byNumber[v.number] = v.id
		maxNumber = max(maxNumber, v.number)
	}
	now := util.UtcNow()
	ids := map[int]int64{}
	// claimed is what this scheme already seats a room at: a venue it named
	// one line up is not free for the next title.
	claimed := map[int64]bool{}
	for _, venue := range venues {
		title := strings.TrimSpace(venue.Title)
		if title == "" || title == dopestrings.Default.Scheme.Titles.Venue(fmt.Sprint(venue.Number)) {
			if id, ok := byNumber[venue.Number]; ok {
				ids[venue.Number] = id
			}
			continue
		}
		if id, ok := byTitle[venueKey(title)]; ok {
			ids[venue.Number], claimed[id] = id, true
			continue
		}
		number := venue.Number
		if id, taken := byNumber[number]; taken && number > 0 && !claimed[id] {
			var elsewhere bool
			if err := tx.QueryRowContext(ctx, `
select exists(select 1 from matches where venue_id = ? and game_id != ?)`, id, gameID).Scan(&elsewhere); err != nil {
				return nil, err
			}
			if !elsewhere || own[id] {
				ids[venue.Number], claimed[id] = id, true
				continue
			}
		}
		if _, taken := byNumber[number]; taken || number <= 0 {
			maxNumber++
			number = maxNumber
		}
		id, err := store.InsertReturningID(ctx, tx, `
insert into venues(fest_id, number, title, created_at, updated_at) values(?, ?, ?, ?, ?)`, festID, number, title, now, now)
		if err != nil {
			return nil, err
		}
		byNumber[number] = id
		byTitle[venueKey(title)] = id
		maxNumber = max(maxNumber, number)
		ids[venue.Number], claimed[id] = id, true
	}
	return ids, nil
}

// venueKey is a venue title as two spellings of one room compare: case and
// spacing aside, ё as е.
func venueKey(title string) string {
	key := strings.ToLower(strings.Join(strings.Fields(title), " "))
	return strings.ReplaceAll(key, "ё", "е")
}

// writeStructureTx is the one writer of a Game's stages, matches and slots —
// compiled or pasted, created, rebuilt, recompiled or imported. A stage
// carries its Kind (its stage_type when the scheme names none), a match its
// letter, its venue when the caller resolved the scheme's venues to rows, and
// the pristine Protocol document its Protocol asks for; seat says who sits in
// a seed slot.
//
// live is the Structure the Game has now, nil for a Game that has none. A
// stage or bout the scheme still names is then rewritten in place rather
// than inserted, and what the scheme no longer names is deleted: see
// liveStructure for what each bout keeps.
func writeStructureTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, gameType string, scheme store.FestScheme, venues map[int]int64, seat func(store.SchemeSlot) any, live *liveStructure) error {
	if live == nil {
		live = &liveStructure{}
	}
	for stageIndex, stage := range scheme.Stages {
		stageID, err := live.writeStageTx(ctx, tx, festID, gameID, stageIndex, stage)
		if err != nil {
			return err
		}
		for matchIndex, match := range stage.Matches {
			row := matchRow{stageID: stageID, position: matchIndex + 1, match: match,
				emptyState: stageEmptyState(gameType, stage, len(match.Slots), scheme.Questions)}
			if id, ok := venues[match.Venue]; ok {
				row.venueID = id
			}
			if err := live.writeMatchTx(ctx, tx, festID, gameID, gameType, row, seat); err != nil {
				return err
			}
		}
	}
	return live.dropLeftTx(ctx, tx)
}

// liveStructure is a Game's stages and bouts as they stand, by code, for
// writeStructureTx to write the new scheme over. A bout that has begun keeps
// its seats and its document and only moves (title, letter, position); one in
// grown keeps them too and takes the extra seats after them; any other is
// reseated from scratch.
type liveStructure struct {
	stages  map[string]int64
	matches map[string]liveMatch
	// grown is the bouts that grow, with the document their Protocol grew.
	grown map[string]json.RawMessage
}

type liveMatch struct {
	id     int64
	status string
	state  string
}

func (m liveMatch) begun(gameType string) bool {
	return m.status == "finished" || games.Started(gameType, m.state)
}

// matchRow is one bout of the scheme as writeStructureTx writes it.
type matchRow struct {
	stageID    int64
	position   int
	match      store.SchemeMatch
	emptyState string
	venueID    any
}

func loadLiveStructureTx(ctx context.Context, tx *sql.Tx, gameID int64) (*liveStructure, error) {
	live := &liveStructure{stages: map[string]int64{}, matches: map[string]liveMatch{}, grown: map[string]json.RawMessage{}}
	type stageRow struct {
		id   int64
		code string
	}
	stages, err := store.CollectRows(ctx, tx, `select id, code from stages where game_id = ?`, []any{gameID},
		func(rows *sql.Rows) (stageRow, error) {
			var r stageRow
			return r, rows.Scan(&r.id, &r.code)
		})
	if err != nil {
		return nil, err
	}
	for _, r := range stages {
		live.stages[r.code] = r.id
	}
	type liveRow struct {
		code string
		m    liveMatch
	}
	matches, err := store.CollectRows(ctx, tx, `
select id, code, status, coalesce(state_json, '{}') from matches where game_id = ?`, []any{gameID},
		func(rows *sql.Rows) (liveRow, error) {
			var r liveRow
			return r, rows.Scan(&r.m.id, &r.code, &r.m.status, &r.m.state)
		})
	if err != nil {
		return nil, err
	}
	for _, r := range matches {
		live.matches[r.code] = r.m
	}
	return live, nil
}

func (live *liveStructure) writeStageTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, stageIndex int, stage store.SchemeStage) (int64, error) {
	position := stage.Position
	if position == 0 {
		position = stageIndex + 1
	}
	stageType := stage.StageType
	if stageType == "" {
		stageType = "matches"
	}
	kind := stage.Kind
	if kind == "" {
		kind = stageType
	}
	grain := stage.Grain.Normalized()
	if stageID, ok := live.stages[stage.Code]; ok {
		delete(live.stages, stage.Code)
		// The grain is refreshed here too: a recompile is how a game whose
		// stages predate the coordinates acquires them, and a block that
		// moved needs its new ones.
		_, err := tx.ExecContext(ctx, `
update stages set title = ?, stage_type = ?, kind = ?, position = ?, config_json = ?,
  block_code = ?, wave_index = ?, group_code = ? where id = ?`,
			stage.Title, stageType, kind, position, store.StageConfigOf(stage).JSON(),
			grain.Block, grain.Wave, grain.Group, stageID)
		return stageID, err
	}
	return store.InsertReturningID(ctx, tx, `
insert into stages(fest_id, game_id, code, title, stage_type, kind, position, status, config_json, block_code, wave_index, group_code)
values(?, ?, ?, ?, ?, ?, ?, 'active', ?, ?, ?, ?)`,
		festID, gameID, stage.Code, stage.Title, stageType, kind, position, store.StageConfigOf(stage).JSON(),
		grain.Block, grain.Wave, grain.Group)
}

func (live *liveStructure) writeMatchTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, gameType string, row matchRow, seat func(store.SchemeSlot) any) error {
	match := row.match
	seats := match.ParticipantCount
	if seats == 0 {
		seats = len(match.Slots)
	}
	existing, ok := live.matches[match.Code]
	if !ok {
		matchID, err := store.InsertReturningID(ctx, tx, `
insert into matches(fest_id, game_id, stage_id, code, title, letter, position, round, wave, participant_count, venue_id, status, revision, state_json)
values(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'active', 1, ?)`,
			festID, gameID, row.stageID, match.Code, match.Title, match.Letter, row.position, match.BlockRound, match.Wave, seats, row.venueID, row.emptyState)
		if err != nil {
			return err
		}
		return insertMatchSlots(ctx, tx, matchID, match.Slots, seat)
	}
	delete(live.matches, match.Code)
	if state, grows := live.grown[match.Code]; grows {
		// The seats it had stay where they are, results and all; the new ones
		// are added after them.
		if _, err := tx.ExecContext(ctx, `
update matches set stage_id = ?, title = ?, letter = ?, position = ?, round = ?, wave = ?, participant_count = ?, state_json = ? where id = ?`,
			row.stageID, match.Title, match.Letter, row.position, match.BlockRound, match.Wave, seats, string(state), existing.id); err != nil {
			return err
		}
		var have int
		if err := tx.QueryRowContext(ctx, `select count(*) from match_slots where match_id = ?`, existing.id).Scan(&have); err != nil {
			return err
		}
		return insertMatchSlotsFrom(ctx, tx, existing.id, match.Slots, have, seat)
	}
	if existing.begun(gameType) {
		_, err := tx.ExecContext(ctx, `
update matches set stage_id = ?, title = ?, letter = ?, position = ?, round = ?, wave = ? where id = ?`,
			row.stageID, match.Title, match.Letter, row.position, match.BlockRound, match.Wave, existing.id)
		return err
	}
	// A team-blob бой keys its marks by Participant, and its Protocol calls it
	// unstarted until it is finished. Its marks stay through the reseat: those
	// of an entrant still seated show again, the others wait unseen. A late
	// entrant added to a written qualifier mid-entry used to wipe it.
	state := row.emptyState
	if store.TeamBlobShaped(gameType) {
		state = existing.state
	}
	if _, err := tx.ExecContext(ctx, `
update matches set stage_id = ?, title = ?, letter = ?, position = ?, round = ?, wave = ?, participant_count = ?, status = 'active', state_json = ? where id = ?`,
		row.stageID, match.Title, match.Letter, row.position, match.BlockRound, match.Wave, seats, state, existing.id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `delete from match_slots where match_id = ?`, existing.id); err != nil {
		return err
	}
	return insertMatchSlots(ctx, tx, existing.id, match.Slots, seat)
}

// dropLeftTx deletes the stages and bouts the new scheme no longer names.
func (live *liveStructure) dropLeftTx(ctx context.Context, tx *sql.Tx) error {
	for _, m := range live.matches {
		if _, err := tx.ExecContext(ctx, `delete from matches where id = ?`, m.id); err != nil {
			return err
		}
	}
	for _, stageID := range live.stages {
		if _, err := tx.ExecContext(ctx, `delete from stages where id = ?`, stageID); err != nil {
			return err
		}
	}
	return nil
}

// Recompile re-expands an edited DSL onto a live game: stages and unstarted
// matches follow the new scheme (questions changes included), started matches
// survive with identical slot sources — else the whole edit is refused,
// naming them. A bout the new scheme adds sits at its venue, as on creation.
// A Game with no recorded Entrant list is seated and recorded the way Clear
// does it (unrecordedEntrantsTx), so the bouts, the entrants tab and the next
// recompile agree on who plays.
func Recompile(ctx context.Context, tx *sql.Tx, festID, gameID int64, dsl string) error {
	var oldSchemeJSON, gameType string
	if err := tx.QueryRowContext(ctx, `
select coalesce(scheme_json, '{}'), game_type from games where id = ? and fest_id = ? and scheme_dsl is not null`,
		gameID, festID).Scan(&oldSchemeJSON, &gameType); err != nil {
		return err
	}
	var meta struct {
		Slug  string `json:"slug"`
		Title string `json:"title"`
	}
	_ = json.Unmarshal([]byte(oldSchemeJSON), &meta)
	// A Game that named its entrants keeps them across a recompile. Falling back
	// to the fest's registry here would recompile a game of 48 against a roster
	// of 65 and refuse the scheme it was created from.
	entrants, err := gameEntrantsTx(ctx, tx, gameID)
	if err != nil {
		return err
	}
	doc, declared, err := parseSchemeDSL(gameType, dsl)
	if err != nil {
		return err
	}
	unrecorded := len(entrants) == 0
	var seatRoster bool
	var numbers []int
	if unrecorded {
		if entrants, seatRoster, err = unrecordedEntrantsTx(ctx, tx, festID, gameID, gameType, declared); err != nil {
			return err
		}
	} else if numbers, err = seatedNumbersTx(ctx, tx, gameID, entrants); err != nil {
		return err
	}
	input := schemedsl.Input{Slug: meta.Slug, Title: meta.Title, GameType: gameType}
	scheme, err := compileForEntrantsTx(ctx, tx, festID, doc, declared, input, entrants, numbers)
	if err != nil {
		return err
	}
	live, err := loadLiveStructureTx(ctx, tx, gameID)
	if err != nil {
		return err
	}
	if err := planGrowthTx(ctx, tx, gameType, scheme, live); err != nil {
		return err
	}
	if err := refuseLosingEntries(gameType, oldSchemeJSON, scheme, live); err != nil {
		return err
	}
	if unrecorded && len(entrants) > 0 {
		if err := seatChosenTx(ctx, tx, gameID, entrants); err != nil {
			return err
		}
	}
	if seatRoster {
		if err := seatRosterTx(ctx, tx, festID, gameID, gameType); err != nil {
			return err
		}
	}
	seat, err := seedSeaterTx(ctx, tx, festID, gameID, gameType)
	if err != nil {
		return err
	}
	own, err := gameVenueIDsTx(ctx, tx, gameID)
	if err != nil {
		return err
	}
	venues, err := schemeVenuesTx(ctx, tx, festID, gameID, scheme.Venues, own)
	if err != nil {
		return err
	}
	if err := writeStructureTx(ctx, tx, festID, gameID, gameType, scheme, venues, seat, live); err != nil {
		return err
	}
	if unrecorded {
		if err := recordGameEntrantsTx(ctx, tx, gameID); err != nil {
			return err
		}
	}
	schemeJSON, err := json.Marshal(scheme)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
update games set scheme_json = ?, scheme_dsl = ?, revision = revision + 1, updated_at = ? where id = ?`,
		string(schemeJSON), dsl, util.UtcNow(), gameID); err != nil {
		return err
	}
	if _, err := resolver.ResolveGameSlotsTx(ctx, tx, gameID); err != nil {
		return err
	}
	_, err = festwrite.BumpFestRevisionTx(ctx, tx, festID, "game:recompile", util.MustJSON(map[string]any{
		"gameID": gameID,
		"stages": len(scheme.Stages),
	}))
	return err
}

// planGrowthTx says which of the Game's started bouts the new scheme may
// keep, and refuses it, naming the others. A started bout keeps its seats.
// The one exception is a bout that only grows: the same seats first, more
// after them, a document that can take them (a troika game's written
// qualifier), and nothing else in the Game started — a late troika then gets a
// row, and no result that decides a later seat has been played yet.
func planGrowthTx(ctx context.Context, tx *sql.Tx, gameType string, scheme store.FestScheme, live *liveStructure) error {
	planned := map[string]store.SchemeMatch{}
	for _, stage := range scheme.Stages {
		for _, match := range stage.Matches {
			planned[match.Code] = match
		}
	}
	var blocked []string
	othersStarted := false
	for code, m := range live.matches {
		if !m.begun(gameType) {
			continue
		}
		match, survives := planned[code]
		if survives && sameSlotIdentities(ctx, tx, m.id, match.Slots) {
			othersStarted = true
			continue
		}
		if survives && slotIdentitiesExtend(ctx, tx, m.id, match.Slots) {
			if grower, ok := games.As[games.Grower](gameType); ok {
				state, grew, err := grower.GrowSeats(json.RawMessage(m.state), len(match.Slots))
				if err != nil {
					return err
				}
				if grew {
					live.grown[code] = state
					continue
				}
			}
		}
		blocked = append(blocked, code)
	}
	if othersStarted {
		for code := range live.grown {
			blocked = append(blocked, code)
		}
	}
	if len(blocked) > 0 {
		sort.Strings(blocked)
		return corei18n.User(dopestrings.Default.Gamebuild.Recompile.StartedBouts(strings.Join(blocked, ", ")))
	}
	return nil
}

// refuseLosingEntries refuses a recompile that would throw away something a
// host entered. A bout nobody has started is dealt again from its pristine
// document, or deleted when the new scheme drops it, and its Protocol may
// call it unstarted while it already holds a seat, a pin or a рассадка. Such
// a bout is told apart by its document: it is no longer the one its old
// scheme wrote. A team-blob bout the new scheme keeps is not at risk, since
// it keeps its marks through the reseat.
func refuseLosingEntries(gameType, oldSchemeJSON string, scheme store.FestScheme, live *liveStructure) error {
	var old store.FestScheme
	_ = json.Unmarshal([]byte(oldSchemeJSON), &old)
	pristine := map[string]string{}
	for _, stage := range old.Stages {
		for _, match := range stage.Matches {
			pristine[match.Code] = stageEmptyState(gameType, stage, len(match.Slots), old.Questions)
		}
	}
	kept := map[string]bool{}
	for _, stage := range scheme.Stages {
		for _, match := range stage.Matches {
			kept[match.Code] = true
		}
	}
	var entered []string
	for code, m := range live.matches {
		if m.begun(gameType) || live.grown[code] != nil {
			continue
		}
		if kept[code] && store.TeamBlobShaped(gameType) {
			continue
		}
		empty, known := pristine[code]
		if !known {
			empty = "{}"
		}
		if !sameDocument(m.state, empty) {
			entered = append(entered, code)
		}
	}
	if len(entered) == 0 {
		return nil
	}
	sort.Strings(entered)
	return corei18n.User(dopestrings.Default.Gamebuild.Recompile.EnteredBouts(strings.Join(entered, ", ")))
}

// sameDocument compares two JSON documents by what they hold, not by how
// they are spelled.
func sameDocument(a, b string) bool {
	if a == b {
		return true
	}
	var x, y any
	if json.Unmarshal([]byte(a), &x) != nil || json.Unmarshal([]byte(b), &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

// sameSlotIdentities compares a live match's slot sources with the planned
// ones, ignoring cosmetic labels.
func sameSlotIdentities(ctx context.Context, tx *sql.Tx, matchID int64, planned []store.SchemeSlot) bool {
	rows, err := tx.QueryContext(ctx, `
select source_type, source_ref_json from match_slots where match_id = ? order by slot_index`, matchID)
	if err != nil {
		return false
	}
	defer rows.Close()
	var current []string
	for rows.Next() {
		var sourceType, refJSON string
		if err := rows.Scan(&sourceType, &refJSON); err != nil {
			return false
		}
		current = append(current, store.ParseSlotRef(sourceType, refJSON).Identity())
	}
	if rows.Err() != nil || len(current) != len(planned) {
		return false
	}
	for i, slot := range planned {
		if store.SlotRefOf(slot).Identity() != current[i] {
			return false
		}
	}
	return true
}

// slotIdentitiesExtend reports whether a match's seats are the first of the
// planned ones and the plan only adds seats after them.
func slotIdentitiesExtend(ctx context.Context, tx *sql.Tx, matchID int64, planned []store.SchemeSlot) bool {
	current, err := store.CollectRows(ctx, tx, `
select source_type, source_ref_json from match_slots where match_id = ? order by slot_index`, []any{matchID},
		func(rows *sql.Rows) (string, error) {
			var sourceType, refJSON string
			if err := rows.Scan(&sourceType, &refJSON); err != nil {
				return "", err
			}
			return store.ParseSlotRef(sourceType, refJSON).Identity(), nil
		})
	if err != nil || len(current) >= len(planned) {
		return false
	}
	for i, identity := range current {
		if store.SlotRefOf(planned[i]).Identity() != identity {
			return false
		}
	}
	return true
}

// stageQuestions is a stage's questions override, else the scheme-wide count.
func stageQuestions(stage store.SchemeStage, fallback int) int {
	if q := store.StageConfigOf(stage).Questions(); q > 0 {
		return q
	}
	return fallback
}

func uniqueSchemeSlug(base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "game"
	}
	return fmt.Sprintf("%s-%d", base, time.Now().UnixNano())
}

// rebuildTx materialises a Game's Structure afresh from what the Game already
// holds — its DSL, compiled against its own entrants and seating them again,
// or its pasted scheme with its venues — and returns the scheme it built.
// Clear deletes the old rows first and calls this.
func rebuildTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, gameType, dsl, schemeJSON string, entrants []int64, own map[int64]bool) ([]byte, error) {
	if strings.TrimSpace(dsl) != "" {
		var meta struct {
			Slug  string `json:"slug"`
			Title string `json:"title"`
		}
		_ = json.Unmarshal([]byte(schemeJSON), &meta)
		if len(entrants) == 0 {
			// Nobody recorded: a Game that seats troikas seats the
			// default ones, never the fest's teams.
			_, declared, err := parseSchemeDSL(gameType, dsl)
			if err != nil {
				return nil, err
			}
			if entrants, err = defaultTroikasTx(ctx, tx, festID, gameType, declared); err != nil {
				return nil, err
			}
		}
		scheme, err := schemeForEntrantsTx(ctx, tx, festID, gameType, meta.Slug, meta.Title, dsl, entrants)
		if err != nil {
			return nil, err
		}
		if len(entrants) > 0 {
			if err := seatChosenTx(ctx, tx, gameID, entrants); err != nil {
				return nil, err
			}
		}
		if err := writeCompiledStructureTx(ctx, tx, festID, gameID, gameType, scheme, own); err != nil {
			return nil, err
		}
		if err := recordGameEntrantsTx(ctx, tx, gameID); err != nil {
			return nil, err
		}
		return json.Marshal(scheme)
	}
	var scheme store.FestScheme
	if err := json.Unmarshal([]byte(schemeJSON), &scheme); err != nil {
		return nil, corei18n.User(dopestrings.Default.Gamebuild.Clear.ParsePasted(err.Error()))
	}
	scheme.Teams = nil // seeded teams come from a fresh import, not the scheme
	if scheme.GameType == "" {
		scheme.GameType = gameType
	}
	dealLetters(&scheme)
	if err := writePastedStructureTx(ctx, tx, festID, gameID, scheme); err != nil {
		return nil, err
	}
	return json.Marshal(scheme)
}

// gameVenueIDsTx is the venues a Game's bouts sit at now.
func gameVenueIDsTx(ctx context.Context, tx *sql.Tx, gameID int64) (map[int64]bool, error) {
	ids, err := store.CollectRows(ctx, tx, `select distinct venue_id from matches where game_id = ? and venue_id is not null`,
		[]any{gameID}, func(rows *sql.Rows) (int64, error) {
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
