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
	"regexp"
	"sort"
	"strings"
	"time"

	"dope/dope/domain/games"
	"dope/dope/domain/protocol"
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
	KSIThemes            int
	KSIStickers          json.RawMessage
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
	if strings.TrimSpace(spec.DSL) != "" {
		// Multi is one sitting whose shape is its minigames, and the DSL
		// has no way to say what they are — so a scheme for one would compile
		// to a game that scores nothing. Refuse it rather than build it.
		if spec.Type == games.Multi {
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
	switch spec.Type {
	case games.OD:
		return createODGameTx(ctx, tx, spec.FestID, spec.ODTours, spec.ODQuestions)
	case games.KSI:
		return createKSIGameTx(ctx, tx, spec.FestID, spec.KSIThemes, spec.KSIStickers)
	case games.Multi:
		return createMultiGameTx(ctx, tx, spec.FestID, spec.Minigames, spec.MultiSorting)
	case games.EK:
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
	return writeStructureTx(ctx, tx, festID, gameID, scheme.GameType, scheme, venues, unseated)
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
	scheme, err := schemeForEntrantsTx(ctx, tx, festID, gameType, identity.Code, identity.Title, dsl, entrants)
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
	if err := writeCompiledStructureTx(ctx, tx, festID, gameID, gameType, scheme); err != nil {
		return 0, err
	}
	if err := recordGameEntrantsTx(ctx, tx, gameID); err != nil {
		return 0, err
	}
	return gameID, nil
}

func schemeForEntrantsTx(ctx context.Context, tx *sql.Tx, festID int64, gameType, slug, title, dsl string, chosen []int64) (store.FestScheme, error) {
	if strings.TrimSpace(dsl) == "" {
		return store.FestScheme{}, corei18n.User(dopestrings.Default.Gamebuild.Create.SchemeRequired())
	}
	doc, err := schemedsl.Parse(dsl)
	if err != nil {
		return store.FestScheme{}, err
	}
	input := schemedsl.Input{Slug: slug, Title: title, GameType: gameType}
	if seed, hasSeed := doc.Init.Str("seed"); !hasSeed {
		entrants, err := chosenEntrantsTx(ctx, tx, festID, gameType, chosen)
		if err != nil {
			return store.FestScheme{}, err
		}
		input.Entrants = entrants
	} else if seed != "random" && seed != "xlsx" {
		var known int
		if err := tx.QueryRowContext(ctx, `select count(*) from games where fest_id = ? and code = ?`, festID, seed).Scan(&known); err != nil {
			return store.FestScheme{}, err
		}
		if known == 0 {
			return store.FestScheme{}, corei18n.User(dopestrings.Default.Gamebuild.Create.SeedUnknown(seed))
		}
	}
	return schemedsl.Compile(doc, input)
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
	p, ok := protocol.Get(gameType)
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
func writeCompiledStructureTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, gameType string, scheme store.FestScheme) error {
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
	return writeStructureTx(ctx, tx, festID, gameID, gameType, scheme, nil, seat)
}

// writeStructureTx is the one writer of a Game's stages, matches and slots —
// compiled or pasted, created, rebuilt or imported. A stage carries its Kind
// (its stage_type when the scheme names none), a match its letter, its venue
// when the caller resolved the scheme's venues to rows, and the pristine
// Protocol document its Protocol asks for; seat says who sits in a seed slot.
func writeStructureTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, gameType string, scheme store.FestScheme, venues map[int]int64, seat func(store.SchemeSlot) any) error {
	for stageIndex, stage := range scheme.Stages {
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
		stageID, err := store.InsertReturningID(ctx, tx, `
insert into stages(fest_id, game_id, code, title, stage_type, kind, position, status, config_json, block_code, wave_index, group_code)
values(?, ?, ?, ?, ?, ?, ?, 'active', ?, ?, ?, ?)`,
			festID, gameID, stage.Code, stage.Title, stageType, kind, position, store.StageConfigOf(stage).JSON(),
			grain.Block, grain.Wave, grain.Group)
		if err != nil {
			return err
		}
		for matchIndex, match := range stage.Matches {
			seats := match.ParticipantCount
			if seats == 0 {
				seats = len(match.Slots)
			}
			var venueID any
			if id, ok := venues[match.Venue]; ok {
				venueID = id
			}
			emptyState := stageEmptyState(gameType, stage, len(match.Slots), scheme.Questions)
			matchID, err := store.InsertReturningID(ctx, tx, `
insert into matches(fest_id, game_id, stage_id, code, title, letter, position, round, wave, participant_count, venue_id, status, revision, state_json)
values(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'active', 1, ?)`,
				festID, gameID, stageID, match.Code, match.Title, match.Letter, matchIndex+1, match.Round, match.Wave, seats, venueID, emptyState)
			if err != nil {
				return err
			}
			if err := insertMatchSlots(ctx, tx, matchID, match.Slots, seat); err != nil {
				return err
			}
		}
	}
	return nil
}

// Recompile re-expands an edited DSL onto a live game: stages and unstarted
// matches follow the new scheme (questions changes included), started matches
// survive with identical slot sources — else the whole edit is refused,
// naming them.
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
	scheme, err := schemeForEntrantsTx(ctx, tx, festID, gameType, meta.Slug, meta.Title, dsl, entrants)
	if err != nil {
		return err
	}

	type dbMatch struct {
		ID      int64
		StageID int64
		Status  string
		State   string
	}
	existingMatches := map[string]dbMatch{}
	rows, err := tx.QueryContext(ctx, `
select id, stage_id, code, status, coalesce(state_json, '{}') from matches where game_id = ?`, gameID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var m dbMatch
		var code string
		if err := rows.Scan(&m.ID, &m.StageID, &code, &m.Status, &m.State); err != nil {
			rows.Close()
			return err
		}
		existingMatches[code] = m
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	existingStages := map[string]int64{}
	stageRows, err := tx.QueryContext(ctx, `select id, code from stages where game_id = ?`, gameID)
	if err != nil {
		return err
	}
	for stageRows.Next() {
		var id int64
		var code string
		if err := stageRows.Scan(&id, &code); err != nil {
			stageRows.Close()
			return err
		}
		existingStages[code] = id
	}
	if err := stageRows.Err(); err != nil {
		stageRows.Close()
		return err
	}
	stageRows.Close()

	planned := map[string]store.SchemeMatch{}
	for _, stage := range scheme.Stages {
		for _, match := range stage.Matches {
			planned[match.Code] = match
		}
	}
	var blocked []string
	for code, m := range existingMatches {
		if m.Status != "finished" && !protocol.Started(gameType, m.State) {
			continue
		}
		match, survives := planned[code]
		if !survives || !sameSlotIdentities(ctx, tx, m.ID, match.Slots) {
			blocked = append(blocked, code)
		}
	}
	if len(blocked) > 0 {
		sort.Strings(blocked)
		return corei18n.User(dopestrings.Default.Gamebuild.Recompile.StartedBouts(strings.Join(blocked, ", ")))
	}

	seat, err := seedSeaterTx(ctx, tx, festID, gameID, gameType)
	if err != nil {
		return err
	}
	for stageIndex, stage := range scheme.Stages {
		position := stage.Position
		if position == 0 {
			position = stageIndex + 1
		}
		grain := stage.Grain.Normalized()
		stageID, exists := existingStages[stage.Code]
		if exists {
			// The grain is refreshed here, not only on insert: a recompile is how
			// a game whose stages predate the coordinates acquires them, and a
			// block that moved needs its new ones.
			if _, err := tx.ExecContext(ctx, `
update stages set title = ?, stage_type = ?, kind = ?, position = ?, config_json = ?,
  block_code = ?, wave_index = ?, group_code = ? where id = ?`,
				stage.Title, stage.StageType, stage.Kind, position, store.StageConfigOf(stage).JSON(),
				grain.Block, grain.Wave, grain.Group, stageID); err != nil {
				return err
			}
			delete(existingStages, stage.Code)
		} else {
			if stageID, err = store.InsertReturningID(ctx, tx, `
insert into stages(fest_id, game_id, code, title, stage_type, kind, position, status, config_json, block_code, wave_index, group_code)
values(?, ?, ?, ?, ?, ?, ?, 'active', ?, ?, ?, ?)`,
				festID, gameID, stage.Code, stage.Title, stage.StageType, stage.Kind, position, store.StageConfigOf(stage).JSON(),
				grain.Block, grain.Wave, grain.Group); err != nil {
				return err
			}
		}
		emptyState := string(games.BrainEmptyStateJSON(stageQuestions(stage, scheme.Questions)))
		for matchIndex, match := range stage.Matches {
			existing, ok := existingMatches[match.Code]
			if ok {
				delete(existingMatches, match.Code)
				if existing.Status == "finished" || protocol.Started(gameType, existing.State) {
					if _, err := tx.ExecContext(ctx, `
update matches set stage_id = ?, title = ?, letter = ?, position = ?, round = ?, wave = ? where id = ?`,
						stageID, match.Title, match.Letter, matchIndex+1, match.Round, match.Wave, existing.ID); err != nil {
						return err
					}
					continue
				}
				if _, err := tx.ExecContext(ctx, `
update matches set stage_id = ?, title = ?, letter = ?, position = ?, round = ?, wave = ?, participant_count = ?, status = 'active', state_json = ? where id = ?`,
					stageID, match.Title, match.Letter, matchIndex+1, match.Round, match.Wave, len(match.Slots), emptyState, existing.ID); err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `delete from match_slots where match_id = ?`, existing.ID); err != nil {
					return err
				}
				if err := insertMatchSlots(ctx, tx, existing.ID, match.Slots, seat); err != nil {
					return err
				}
				continue
			}
			matchID, err := store.InsertReturningID(ctx, tx, `
insert into matches(fest_id, game_id, stage_id, code, title, letter, position, round, wave, participant_count, status, revision, state_json)
values(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'active', 1, ?)`,
				festID, gameID, stageID, match.Code, match.Title, match.Letter, matchIndex+1, match.Round, match.Wave, len(match.Slots), emptyState)
			if err != nil {
				return err
			}
			if err := insertMatchSlots(ctx, tx, matchID, match.Slots, seat); err != nil {
				return err
			}
		}
	}
	for _, m := range existingMatches {
		if _, err := tx.ExecContext(ctx, `delete from matches where id = ?`, m.ID); err != nil {
			return err
		}
	}
	for _, stageID := range existingStages {
		if _, err := tx.ExecContext(ctx, `delete from stages where id = ?`, stageID); err != nil {
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
func rebuildTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, gameType, dsl, schemeJSON string, entrants []int64) ([]byte, error) {
	if strings.TrimSpace(dsl) != "" {
		var meta struct {
			Slug  string `json:"slug"`
			Title string `json:"title"`
		}
		_ = json.Unmarshal([]byte(schemeJSON), &meta)
		scheme, err := schemeForEntrantsTx(ctx, tx, festID, gameType, meta.Slug, meta.Title, dsl, entrants)
		if err != nil {
			return nil, err
		}
		if len(entrants) > 0 {
			if err := seatChosenTx(ctx, tx, gameID, entrants); err != nil {
				return nil, err
			}
		}
		if err := writeCompiledStructureTx(ctx, tx, festID, gameID, gameType, scheme); err != nil {
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
