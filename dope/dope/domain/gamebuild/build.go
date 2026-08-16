// Package gamebuild is where a Game comes to exist: it takes a Spec — which
// фест, which format, its label, its scheme DSL and, when the фест's Games
// differ, its entrants — and materialises the Structure the DSL compiles to,
// numbering the entrants from 1 (ADR-0009), seating the seeds, and writing
// the stage, match and slot rows every other module reads. Recompile plays an
// edited DSL onto a live Game the same way, refusing to touch a бой that has
// begun. The formats that predate the DSL — ОД's tours, КСИ's themes, ЭК's
// pasted JSON — enter through the same Spec, so a caller never learns which
// is which.
package gamebuild

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
	// Suffix only to break a collision. A фест may hold two games of one type
	// under names of their own — СтудЧР played личная СИ and ТПШ, both `si` —
	// and numbering the second «ТПШ 2» renames a tournament that had a name.
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
// empty. Entrants are which of the фест's Participants play, in seed order;
// absent, the whole фест plays, which is what a one-game фест wants.
type Spec struct {
	FestID   int64
	Type     string
	Label    string
	DSL      string
	Entrants []int64
	// The pre-DSL formats. ОД: tours of so many questions; КСИ: themes and an
	// optional stickers block; ЭК: a pasted detailed scheme.
	ODTours, ODQuestions int
	KSIThemes            int
	KSIStickers          json.RawMessage
	EKScheme             *store.FestScheme
}

// Create makes the Game the Spec describes and returns its id. A фест's Games
// rarely share an entrant list (ADR-0009): СтудЧР-2026 registered 65 teams,
// its ОД seated all of them, its ЭК seated 48 and its брейн a different 48.
// Numbers are dealt from 1 inside the Game, so the same team is «2» in one
// and «4» in another.
func Create(ctx context.Context, tx *sql.Tx, spec Spec) (int64, error) {
	if strings.TrimSpace(spec.DSL) != "" {
		return createSchemeGame(ctx, tx, spec.FestID, spec.Type, spec.Label, spec.DSL, spec.Entrants)
	}
	switch spec.Type {
	case games.OD:
		return createODGameTx(ctx, tx, spec.FestID, spec.ODTours, spec.ODQuestions)
	case games.KSI:
		return createKSIGameTx(ctx, tx, spec.FestID, spec.KSIThemes, spec.KSIStickers)
	case games.EK:
		if spec.EKScheme == nil {
			return 0, errors.New("Вставьте JSON-схему ЭК или опишите её схемой")
		}
		return createEKGameTx(ctx, tx, spec.FestID, *spec.EKScheme)
	}
	return 0, errors.New("опишите схему игры")
}

// createSchemeGame creates a game of any type from a scheme DSL: compile,
// store the scheme with its source, seat the entrants and materialise the
// structure. Every format reaches the same plumbing — the DSL is the way a
// bracket is described, not a брейн feature.
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
	if err := buildSchemeStructureTx(ctx, tx, festID, gameID, gameType, scheme); err != nil {
		return 0, err
	}
	if err := recordGameEntrantsTx(ctx, tx, gameID); err != nil {
		return 0, err
	}
	return gameID, nil
}

func schemeForEntrantsTx(ctx context.Context, tx *sql.Tx, festID int64, gameType, slug, title, dsl string, chosen []int64) (store.FestScheme, error) {
	if strings.TrimSpace(dsl) == "" {
		return store.FestScheme{}, errors.New("опишите схему игры")
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
			return store.FestScheme{}, fmt.Errorf("seed: %s — не random, не xlsx и не код игры этого феста", seed)
		}
	}
	return schemedsl.Compile(doc, input)
}

// stageEmptyState builds a match's pristine Protocol document for a stage of a
// compiled scheme. The Protocol owns the shape — a брейн бой is a row of
// questions, a СИ бой a grid of themes — so the builder asks it rather than
// knowing. Falls back to брейн's for schemes compiled before Protocols carried
// their own config.
func stageEmptyState(gameType string, stage store.SchemeStage, seats, fallbackQuestions int) string {
	// A blob-shaped Protocol (ЭК, личная СИ) stores an empty document: its
	// seats come from the Slots and its marks arrive as edits. Seeding it with
	// the Protocol's own state shape would write an array where the blob keys
	// a map, and the first edit would fail to parse it.
	if (store.DBMatchState{GameType: gameType}).IsEKShaped() {
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

// buildSchemeStructureTx materialises a compiled scheme into stages, matches
// and slots for any game type — the plumbing used to be брейн's alone, and the
// only thing that was ever брейн-specific about it was the empty state.
func buildSchemeStructureTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, gameType string, scheme store.FestScheme) error {
	// A declared seed source owns game_assignments — «Import seed» writes them
	// by seed rank, so creation must not pre-fill them by number. Nor may it when
	// the Game already named its entrants: seating the фест's registry on top
	// would add the teams this Game does not play.
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
	for stageIndex, stage := range scheme.Stages {
		position := stage.Position
		if position == 0 {
			position = stageIndex + 1
		}
		grain := stage.Grain.Normalized()
		stageID, err := store.InsertReturningID(ctx, tx, `
insert into stages(fest_id, game_id, code, title, stage_type, kind, position, status, config_json, block_code, wave_index, group_code)
values(?, ?, ?, ?, ?, ?, ?, 'active', ?, ?, ?, ?)`,
			festID, gameID, stage.Code, stage.Title, stage.StageType, stage.Kind, position, storeutil.StageConfigJSON(stage),
			grain.Block, grain.Wave, grain.Group)
		if err != nil {
			return err
		}
		for matchIndex, match := range stage.Matches {
			emptyState := stageEmptyState(gameType, stage, len(match.Slots), scheme.Questions)
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
	return nil
}

// Recompile re-expands an edited DSL onto a live game: stages and unstarted
// бои follow the new scheme (questions changes included), started бои must
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
	// to the фест's registry here would recompile a game of 48 against a roster
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
		if m.Status != "finished" && !games.BrainStateStarted(m.State) {
			continue
		}
		match, survives := planned[code]
		if !survives || !sameSlotIdentities(ctx, tx, m.ID, match.Slots) {
			blocked = append(blocked, code)
		}
	}
	if len(blocked) > 0 {
		sort.Strings(blocked)
		return fmt.Errorf("нельзя менять начатые бои: %s — уберите их изменения или снимите отметку «Закончен» и очистите протокол", strings.Join(blocked, ", "))
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
				stage.Title, stage.StageType, stage.Kind, position, storeutil.StageConfigJSON(stage),
				grain.Block, grain.Wave, grain.Group, stageID); err != nil {
				return err
			}
			delete(existingStages, stage.Code)
		} else {
			if stageID, err = store.InsertReturningID(ctx, tx, `
insert into stages(fest_id, game_id, code, title, stage_type, kind, position, status, config_json, block_code, wave_index, group_code)
values(?, ?, ?, ?, ?, ?, ?, 'active', ?, ?, ?, ?)`,
				festID, gameID, stage.Code, stage.Title, stage.StageType, stage.Kind, position, storeutil.StageConfigJSON(stage),
				grain.Block, grain.Wave, grain.Group); err != nil {
				return err
			}
		}
		emptyState := string(games.BrainEmptyStateJSON(stageQuestions(stage, scheme.Questions)))
		for matchIndex, match := range stage.Matches {
			existing, ok := existingMatches[match.Code]
			if ok {
				delete(existingMatches, match.Code)
				started := existing.Status == "finished" || games.BrainStateStarted(existing.State)
				if started {
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
		current = append(current, slotIdentity(sourceType, refJSON))
	}
	if rows.Err() != nil || len(current) != len(planned) {
		return false
	}
	for i, slot := range planned {
		sourceType, refJSON := storeutil.SlotSource(slot)
		if slotIdentity(sourceType, refJSON) != current[i] {
			return false
		}
	}
	return true
}

func slotIdentity(sourceType, refJSON string) string {
	var ref map[string]any
	_ = json.Unmarshal([]byte(refJSON), &ref)
	switch sourceType {
	case "seed":
		return fmt.Sprintf("seed:%d:%d", store.IntFromMap(ref, "basket"), store.IntFromMap(ref, "number"))
	case "from_match":
		return fmt.Sprintf("from_match:%v:%d", ref["match"], store.IntFromMap(ref, "place"))
	case "reseed":
		return fmt.Sprintf("reseed:%v:%d", ref["stage"], store.IntFromMap(ref, "rank"))
	}
	return sourceType
}

// stageQuestions reads a stage's questions override from its kind config,
// falling back to the scheme-wide count.
func stageQuestions(stage store.SchemeStage, fallback int) int {
	var config struct {
		Questions int `json:"questions"`
	}
	if err := json.Unmarshal(stage.Config, &config); err == nil && config.Questions > 0 {
		return config.Questions
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

// Rebuild materialises a Game's Structure afresh from what the Game already
// holds — its DSL (compiled against its own entrants) or, for a pasted ЭК
// scheme, that JSON with its seeded teams dropped — and returns the scheme it
// built. The «Очистить» action deletes the old rows first and calls this.
func Rebuild(ctx context.Context, tx *sql.Tx, festID, gameID int64) ([]byte, error) {
	var gameType, dsl, schemeJSON string
	if err := tx.QueryRowContext(ctx, `
select game_type, coalesce(scheme_dsl, ''), coalesce(scheme_json, '{}') from games where id = ? and fest_id = ?`,
		gameID, festID).Scan(&gameType, &dsl, &schemeJSON); err != nil {
		return nil, err
	}
	if strings.TrimSpace(dsl) != "" {
		var meta struct {
			Slug  string `json:"slug"`
			Title string `json:"title"`
		}
		_ = json.Unmarshal([]byte(schemeJSON), &meta)
		entrants, err := gameEntrantsTx(ctx, tx, gameID)
		if err != nil {
			return nil, err
		}
		scheme, err := schemeForEntrantsTx(ctx, tx, festID, gameType, meta.Slug, meta.Title, dsl, entrants)
		if err != nil {
			return nil, err
		}
		if err := buildSchemeStructureTx(ctx, tx, festID, gameID, gameType, scheme); err != nil {
			return nil, err
		}
		return json.Marshal(scheme)
	}
	var scheme store.FestScheme
	if err := json.Unmarshal([]byte(schemeJSON), &scheme); err != nil {
		return nil, fmt.Errorf("не удалось разобрать схему ЭК: %w", err)
	}
	scheme.Teams = nil // seeded teams come from a fresh import, not the scheme
	if err := buildEKStructureTx(ctx, tx, festID, gameID, scheme, util.UtcNow()); err != nil {
		return nil, err
	}
	return json.Marshal(scheme)
}
