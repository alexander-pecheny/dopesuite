package imports

import (
	"context"
	"database/sql"

	"github.com/xuri/excelize/v2"

	"dope/dope/domain/core"
	"dope/dope/domain/overrides"
	"dope/dope/domain/protocol"
	rosterpkg "dope/dope/domain/roster"
	"dope/dope/domain/structure"
	"dope/dope/platform/util"
	"dope/dope/storage/festwrite"
	"dope/dope/storage/store"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"sort"
	"strconv"
	"strings"
)

const (
	seedImportStateKey = "seedImport"
	seedSourceKSI      = "ksi"
)

type seedImportState struct {
	Source       string               `json:"source,omitempty"`
	SourceGameID int64                `json:"sourceGameID,omitempty"`
	Rows         []seedImportStateRow `json:"rows,omitempty"`
}

type seedImportStateRow struct {
	SourceRank int    `json:"sourceRank"`
	TeamID     int64  `json:"teamID"`
	Name       string `json:"name"`
	City       string `json:"city,omitempty"`
	Declined   bool   `json:"declined,omitempty"`
}

type SeedImportView struct {
	Source       string              `json:"source,omitempty"`
	SourceGameID int64               `json:"sourceGameID,omitempty"`
	DrawSize     int                 `json:"drawSize"`
	ActiveCount  int                 `json:"activeCount"`
	Rows         []SeedImportViewRow `json:"rows"`
}

type SeedImportViewRow struct {
	SourceRank int    `json:"sourceRank"`
	SeedNumber int    `json:"seedNumber,omitempty"`
	TeamID     int64  `json:"teamID"`
	Name       string `json:"name"`
	City       string `json:"city,omitempty"`
	Declined   bool   `json:"declined"`
	Waitlist   bool   `json:"waitlist"`
}

type SeedDeclineRequest struct {
	TeamID   int64 `json:"teamID"`
	Declined bool  `json:"declined"`
}

type seedRosterTeam struct {
	Number  int64
	Name    string
	City    string
	Players []rosterpkg.SeedRosterPlayer
}

func LoadSeedImportView(eng *core.Engine, ctx context.Context, scope core.FestScope) (SeedImportView, error) {
	_, rawState, err := loadSeedImportGame(ctx, eng.DB, scope)
	if err != nil {
		return SeedImportView{}, err
	}
	state, err := seedImportStateFromRaw(rawState)
	if err != nil {
		return SeedImportView{}, err
	}
	drawSize, err := maxSeedNumber(ctx, eng.DB, scope.GameID)
	if err != nil {
		return SeedImportView{}, err
	}
	return buildSeedImportView(state, drawSize), nil
}

// seedCandidate is one row of a seed source's current standings, whatever the
// source game type (or lot) was.
type seedCandidate struct {
	SourceRank int
	Name       string
	Number     int
	Declined   bool
}

// A SeedSource ranks the фест's Participants for a Game's seeding: the
// фест's first КСИ (the ЭК page's button), whatever the Game's [init]
// declares — a lot for random, else the named Game's current table — or an
// uploaded sheet. Each is resolved inside the import's transaction.
type SeedSource interface {
	resolve(ctx context.Context, tx *sql.Tx, scope core.FestScope) (seeding, error)
}

// seeding is what a source resolved to: the source's stored name and its
// label for messages, the Game it read (0 for a lot or a sheet), and the
// candidates in seed order.
type seeding struct {
	source, label string
	sourceGameID  int64
	candidates    []seedCandidate
}

// FromKSI is the ЭК page's «Импорт из КСИ»: the фест's first КСИ, ranked.
func FromKSI() SeedSource { return fromKSI{} }

// FromScheme is what the Game's [init] declares.
func FromScheme() SeedSource { return fromScheme{} }

// FromXLSX is an uploaded seeding sheet (docs/scheme-dsl.md): column A a team
// number or name, optional column B a basket. With baskets, teams band by
// basket and draw a deterministic lot within each; without, the row order IS
// the seeding.
func FromXLSX(file io.Reader) SeedSource { return fromXLSX{file} }

type fromKSI struct{}

func (fromKSI) resolve(ctx context.Context, tx *sql.Tx, scope core.FestScope) (seeding, error) {
	var code string
	err := tx.QueryRowContext(ctx, `
select code from games where fest_id = ? and game_type = 'ksi' order by position, id limit 1`, scope.FestID).Scan(&code)
	if errors.Is(err, sql.ErrNoRows) {
		return seeding{}, errors.New("в фесте нет игры КСИ")
	}
	if err != nil {
		return seeding{}, err
	}
	gameID, candidates, err := standingsCandidates(ctx, tx, scope.FestID, code, nil)
	return seeding{source: seedSourceKSI, label: "КСИ", sourceGameID: gameID, candidates: candidates}, err
}

type fromScheme struct{}

func (fromScheme) resolve(ctx context.Context, tx *sql.Tx, scope core.FestScope) (seeding, error) {
	declared, err := loadSchemeSeeding(ctx, tx, scope)
	if err != nil {
		return seeding{}, err
	}
	switch declared.Source {
	case "":
		return seeding{}, errors.New("схема игры не объявляет посев ([init] seed)")
	case "xlsx":
		return seeding{}, errors.New("посев из xlsx: загрузите файл на вкладке посева")
	case "random":
		candidates, err := randomSeedCandidates(ctx, tx, scope)
		return seeding{source: "random", label: "жребий", candidates: candidates}, err
	}
	gameID, candidates, err := standingsCandidates(ctx, tx, scope.FestID, declared.Source, declared.Sort)
	return seeding{source: declared.Source, label: declared.Source, sourceGameID: gameID, candidates: candidates}, err
}

type fromXLSX struct{ file io.Reader }

func (f fromXLSX) resolve(ctx context.Context, tx *sql.Tx, scope core.FestScope) (seeding, error) {
	roster, err := loadSeedRosterTeams(ctx, tx, scope.FestID)
	if err != nil {
		return seeding{}, err
	}
	candidates, err := parseSeedXLSX(f.file, scope.GameID, roster)
	return seeding{source: "xlsx", label: "xlsx", candidates: candidates}, err
}

// ImportSeeds seeds the Game from the source: the source's current order is
// snapshotted into the seed ladder (partial results mid-фест are a normal
// source), previous declines survive, and every seed slot is reseated.
func ImportSeeds(eng *core.Engine, ctx context.Context, scope core.FestScope, src SeedSource) (SeedImportView, int64, []byte, error) {
	var view SeedImportView
	var revision int64
	var stateJSON []byte
	err := eng.WithWriteTx(ctx, scope.FestID, "seed-import", func(ctx context.Context, tx *sql.Tx) error {
		gameType, rawState, err := loadSeedImportGame(ctx, tx, scope)
		if err != nil {
			return err
		}
		resolved, err := src.resolve(ctx, tx, scope)
		if err != nil {
			return err
		}
		view, revision, stateJSON, err = importCandidatesTx(ctx, tx, scope, gameType, rawState, resolved.source, resolved.label, resolved.sourceGameID, resolved.candidates)
		return err
	})
	if err != nil {
		return SeedImportView{}, 0, nil, err
	}
	return view, revision, stateJSON, nil
}

func parseSeedXLSX(file io.Reader, gameID int64, roster []seedRosterTeam) ([]seedCandidate, error) {
	book, err := excelize.OpenReader(file)
	if err != nil {
		return nil, fmt.Errorf("не удалось открыть xlsx: %w", err)
	}
	defer book.Close()
	sheets := book.GetSheetList()
	if len(sheets) == 0 {
		return nil, errors.New("в xlsx нет листов")
	}
	cells, err := book.GetRows(sheets[0])
	if err != nil {
		return nil, err
	}

	byNumber := make(map[int]seedRosterTeam, len(roster))
	byName := make(map[string]seedRosterTeam, len(roster))
	for _, team := range roster {
		if team.Number > 0 {
			byNumber[int(team.Number)] = team
		}
		byName[rosterpkg.SeedTeamNameKey(team.Name)] = team
	}

	type parsedRow struct {
		team   seedRosterTeam
		basket int
	}
	var parsed []parsedRow
	baskets := false
	for i, row := range cells {
		if len(row) == 0 || strings.TrimSpace(row[0]) == "" {
			continue
		}
		key := strings.TrimSpace(row[0])
		var team seedRosterTeam
		var found bool
		if number, err := strconv.Atoi(key); err == nil {
			team, found = byNumber[number]
		} else {
			team, found = byName[rosterpkg.SeedTeamNameKey(key)]
		}
		if !found {
			headerish := i == 0
			if headerish && len(row) > 1 {
				_, err := strconv.Atoi(strings.TrimSpace(row[1]))
				headerish = err != nil // a numeric basket beside an unknown team is a typo, not a header
			}
			if headerish {
				continue
			}
			return nil, fmt.Errorf("строка %d: команда %q не найдена в фесте", i+1, key)
		}
		basket := 0
		if len(row) > 1 && strings.TrimSpace(row[1]) != "" {
			if basket, err = strconv.Atoi(strings.TrimSpace(row[1])); err != nil {
				return nil, fmt.Errorf("строка %d: корзина %q — не число", i+1, row[1])
			}
			baskets = true
		}
		parsed = append(parsed, parsedRow{team: team, basket: basket})
	}
	if len(parsed) == 0 {
		return nil, errors.New("в xlsx нет команд")
	}
	if baskets {
		for i, row := range parsed {
			if row.basket <= 0 {
				return nil, fmt.Errorf("команда %q без корзины, а лист корзинный (строка %d)", row.team.Name, i+1)
			}
		}
		sort.SliceStable(parsed, func(i, j int) bool {
			if parsed[i].basket != parsed[j].basket {
				return parsed[i].basket < parsed[j].basket
			}
			return seedLot(gameID, parsed[i].team.Number) < seedLot(gameID, parsed[j].team.Number)
		})
	}
	candidates := make([]seedCandidate, len(parsed))
	for i, row := range parsed {
		candidates[i] = seedCandidate{SourceRank: i + 1, Name: row.team.Name, Number: int(row.team.Number)}
	}
	return candidates, nil
}

func seedLot(gameID, number int64) uint64 {
	h := fnv.New64a()
	fmt.Fprintf(h, "seed-lot:%d:%d", gameID, number)
	return h.Sum64()
}

// importCandidatesTx runs the shared ladder update: dedupe candidates against
// the numbered roster, union declines with the previous import, persist the
// rows and reseat every seed slot.
func importCandidatesTx(ctx context.Context, tx *sql.Tx, scope core.FestScope, gameType, rawState, source, sourceLabel string, sourceGameID int64, candidates []seedCandidate) (SeedImportView, int64, []byte, error) {
	previous, err := seedImportStateFromRaw(rawState)
	if err != nil {
		return SeedImportView{}, 0, nil, err
	}
	previousDeclinesByTeam, previousDeclinesByName := previousSeedDeclines(previous)

	roster, err := loadSeedRosterTeams(ctx, tx, scope.FestID)
	if err != nil {
		return SeedImportView{}, 0, nil, err
	}

	// Index the roster by number (the identity) and by name (to recover a number
	// for a legacy number-less KSI state). First entry wins on duplicates.
	rosterByNumber := make(map[int64]seedRosterTeam, len(roster))
	rosterByName := make(map[string]seedRosterTeam, len(roster))
	for _, rt := range roster {
		if rt.Number > 0 {
			if _, ok := rosterByNumber[rt.Number]; !ok {
				rosterByNumber[rt.Number] = rt
			}
		}
		if key := rosterpkg.SeedTeamNameKey(rt.Name); key != "" {
			if _, ok := rosterByName[key]; !ok {
				rosterByName[key] = rt
			}
		}
	}

	rows := make([]seedImportStateRow, 0, len(candidates))
	seenTeams := make(map[int64]string, len(candidates))
	for _, candidate := range candidates {
		// Prefer the source's own number; for a legacy number-less source
		// recover it from the numbered fest roster by name when unambiguous.
		number := int64(candidate.Number)
		rt := rosterByNumber[number]
		if number <= 0 {
			if m, ok := rosterByName[rosterpkg.SeedTeamNameKey(candidate.Name)]; ok {
				rt = m
				number = m.Number
			}
		}
		var teamID int64
		var city string
		if number > 0 {
			teamID, city, err = EnsureSeedTeamByNumber(ctx, tx, scope.FestID, number, candidate.Name, rt.City, rt.Players)
		} else {
			teamID, city, err = rosterpkg.EnsureSeedTeam(ctx, tx, scope.FestID, candidate.Name, rt.City, rt.Players)
		}
		if err != nil {
			return SeedImportView{}, 0, nil, err
		}
		if previous, exists := seenTeams[teamID]; exists {
			return SeedImportView{}, 0, nil, fmt.Errorf("%s содержит команду %q больше одного раза (первое имя: %q)", sourceLabel, candidate.Name, previous)
		}
		seenTeams[teamID] = candidate.Name
		// A team that refused to play at the source lands pre-declined on the
		// import page (visible but skipped in seeding). Union with any prior
		// decline here so a re-import never silently un-declines a team.
		declined := candidate.Declined || previousDeclinesByTeam[teamID]
		if !declined {
			declined = previousDeclinesByName[rosterpkg.SeedTeamNameKey(candidate.Name)]
		}
		rows = append(rows, seedImportStateRow{
			SourceRank: candidate.SourceRank,
			TeamID:     teamID,
			Name:       candidate.Name,
			City:       city,
			Declined:   declined,
		})
	}

	nextState := seedImportState{
		Source:       source,
		SourceGameID: sourceGameID,
		Rows:         rows,
	}
	return saveSeedImportState(ctx, tx, scope, gameType, rawState, nextState, "seed-import:"+source)
}

func SetSeedImportDeclined(eng *core.Engine, ctx context.Context, scope core.FestScope, req SeedDeclineRequest) (SeedImportView, int64, []byte, error) {
	if req.TeamID <= 0 {
		return SeedImportView{}, 0, nil, errors.New("bad team id")
	}

	eng.Mu.Lock()
	defer eng.Mu.Unlock()

	tx, err := eng.BeginWriteTx(ctx)
	if err != nil {
		return SeedImportView{}, 0, nil, err
	}
	defer tx.Rollback()

	gameType, rawState, err := loadSeedImportGame(ctx, tx, scope)
	if err != nil {
		return SeedImportView{}, 0, nil, err
	}
	state, err := seedImportStateFromRaw(rawState)
	if err != nil {
		return SeedImportView{}, 0, nil, err
	}
	if len(state.Rows) == 0 {
		return SeedImportView{}, 0, nil, errors.New("сначала импортируйте посев")
	}
	found := false
	for i := range state.Rows {
		if state.Rows[i].TeamID != req.TeamID {
			continue
		}
		state.Rows[i].Declined = req.Declined
		found = true
		break
	}
	if !found {
		return SeedImportView{}, 0, nil, errors.New("команда не найдена в импорте посева")
	}

	view, revision, stateJSON, err := saveSeedImportState(ctx, tx, scope, gameType, rawState, state, "seed-import:decline")
	if err != nil {
		return SeedImportView{}, 0, nil, err
	}
	if err := tx.Commit(); err != nil {
		return SeedImportView{}, 0, nil, err
	}
	return view, revision, stateJSON, nil
}

// loadSeedImportGame reads any game's type and state blob — the seed ladder
// lives under state_json's seedImport key for every game type.
func loadSeedImportGame(ctx context.Context, q store.Queryer, scope core.FestScope) (string, string, error) {
	var gameType, rawState string
	err := q.QueryRowContext(ctx, `
select game_type, coalesce(state_json, '{}')
from games
where fest_id = ? and id = ?`, scope.FestID, scope.GameID).Scan(&gameType, &rawState)
	return gameType, rawState, err
}

func loadSchemeSeeding(ctx context.Context, q store.Queryer, scope core.FestScope) (store.SchemeSeeding, error) {
	var schemeJSON string
	if err := q.QueryRowContext(ctx, `
select coalesce(scheme_json, '{}') from games where fest_id = ? and id = ?`,
		scope.FestID, scope.GameID).Scan(&schemeJSON); err != nil {
		return store.SchemeSeeding{}, err
	}
	var scheme struct {
		Seeding *store.SchemeSeeding `json:"seeding"`
	}
	if err := json.Unmarshal([]byte(schemeJSON), &scheme); err != nil {
		return store.SchemeSeeding{}, err
	}
	if scheme.Seeding == nil {
		return store.SchemeSeeding{}, nil
	}
	return *scheme.Seeding, nil
}

func saveSeedImportState(ctx context.Context, tx *sql.Tx, scope core.FestScope, gameType, previousRaw string, state seedImportState, eventType string) (SeedImportView, int64, []byte, error) {
	stateJSON, err := putSeedImportState(previousRaw, state)
	if err != nil {
		return SeedImportView{}, 0, nil, err
	}
	if _, err := tx.ExecContext(ctx, `
update games set state_json = ?, updated_at = ?
where fest_id = ? and id = ?`, string(stateJSON), util.UtcNow(), scope.FestID, scope.GameID); err != nil {
		return SeedImportView{}, 0, nil, err
	}
	assignments, err := replaceSeedAssignments(ctx, tx, scope.GameID, state.Rows)
	if err != nil {
		return SeedImportView{}, 0, nil, err
	}
	if err := resolveSeedSlots(ctx, tx, scope.GameID, gameType, assignments); err != nil {
		return SeedImportView{}, 0, nil, err
	}
	hasRosterOverrides, err := overrides.GameHasPlayerOverridesTx(ctx, tx, scope.FestID, scope.GameID)
	if err != nil {
		return SeedImportView{}, 0, nil, err
	}
	if hasRosterOverrides {
		if err := overrides.MaterializeGameRosterOverridesTx(ctx, tx, scope.FestID, scope.GameID); err != nil {
			return SeedImportView{}, 0, nil, err
		}
	}
	drawSize, err := maxSeedNumber(ctx, tx, scope.GameID)
	if err != nil {
		return SeedImportView{}, 0, nil, err
	}
	revision, err := festwrite.BumpFestRevisionTx(ctx, tx, scope.FestID, eventType, util.MustJSON(map[string]any{
		"gameID":       scope.GameID,
		"source":       state.Source,
		"sourceGameID": state.SourceGameID,
		"rows":         len(state.Rows),
	}))
	if err != nil {
		return SeedImportView{}, 0, nil, err
	}
	return buildSeedImportView(state, drawSize), revision, stateJSON, nil
}

func seedImportStateFromRaw(raw string) (seedImportState, error) {
	obj, err := protocol.RawJSONObject(raw)
	if err != nil {
		return seedImportState{}, err
	}
	rawState, ok := obj[seedImportStateKey]
	if !ok || len(rawState) == 0 {
		return seedImportState{}, nil
	}
	var state seedImportState
	if err := json.Unmarshal(rawState, &state); err != nil {
		return seedImportState{}, err
	}
	return state, nil
}

func putSeedImportState(raw string, state seedImportState) ([]byte, error) {
	obj, err := protocol.RawJSONObject(raw)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	obj[seedImportStateKey] = data
	return json.Marshal(obj)
}

func previousSeedDeclines(state seedImportState) (map[int64]bool, map[string]bool) {
	byTeam := make(map[int64]bool, len(state.Rows))
	byName := make(map[string]bool, len(state.Rows))
	for _, row := range state.Rows {
		if !row.Declined {
			continue
		}
		if row.TeamID > 0 {
			byTeam[row.TeamID] = true
		}
		if row.Name != "" {
			byName[rosterpkg.SeedTeamNameKey(row.Name)] = true
		}
	}
	return byTeam, byName
}

func buildSeedImportView(state seedImportState, drawSize int) SeedImportView {
	view := SeedImportView{
		Source:       state.Source,
		SourceGameID: state.SourceGameID,
		DrawSize:     drawSize,
		Rows:         make([]SeedImportViewRow, 0, len(state.Rows)),
	}
	active := 0
	for _, row := range state.Rows {
		waitlist := drawSize > 0 && active >= drawSize
		seedNumber := 0
		if !row.Declined {
			active++
			seedNumber = active
			waitlist = drawSize > 0 && seedNumber > drawSize
		}
		view.Rows = append(view.Rows, SeedImportViewRow{
			SourceRank: row.SourceRank,
			SeedNumber: seedNumber,
			TeamID:     row.TeamID,
			Name:       row.Name,
			City:       row.City,
			Declined:   row.Declined,
			Waitlist:   waitlist,
		})
	}
	view.ActiveCount = active
	return view
}

func replaceSeedAssignments(ctx context.Context, tx *sql.Tx, gameID int64, rows []seedImportStateRow) (map[[2]int]int64, error) {
	if _, err := tx.ExecContext(ctx, `delete from game_assignments where game_id = ?`, gameID); err != nil {
		return nil, err
	}
	assignments := make(map[[2]int]int64, len(rows))
	seedNumber := 0
	for _, row := range rows {
		if row.Declined || row.TeamID <= 0 {
			continue
		}
		seedNumber++
		if _, err := tx.ExecContext(ctx, `
insert into game_assignments(game_id, basket, number, participant_id)
values(?, 1, ?, ?)`, gameID, seedNumber, row.TeamID); err != nil {
			return nil, err
		}
		assignments[[2]int{1, seedNumber}] = row.TeamID
	}
	return assignments, nil
}

func resolveSeedSlots(ctx context.Context, tx *sql.Tx, gameID int64, gameType string, assignments map[[2]int]int64) error {
	type slotRecord struct {
		ID        int64
		MatchID   int64
		SourceRef string
		Status    string
		State     string
	}
	slots, err := store.CollectRows(ctx, tx, `
select ms.id, ms.match_id, ms.source_ref_json, m.status, coalesce(m.state_json, '{}')
from match_slots ms
join matches m on m.id = ms.match_id
where m.game_id = ? and ms.source_type = 'seed' and ms.locked = 0
order by ms.id`, []any{gameID}, func(rows *sql.Rows) (slotRecord, error) {
		var slot slotRecord
		if err := rows.Scan(&slot.ID, &slot.MatchID, &slot.SourceRef, &slot.Status, &slot.State); err != nil {
			return slot, err
		}
		return slot, nil
	})
	if err != nil {
		return err
	}
	touchedMatches := make(map[int64]struct{})

	for _, slot := range slots {
		// A played бой keeps its participants: a decline shifts the ladder
		// only through matches nobody has started — results already earned
		// stand, the vacancy propagates to the unplayed part of the scheme.
		if slot.Status == "finished" || protocol.Started(gameType, slot.State) {
			continue
		}
		basket, number := seedRefKey(slot.SourceRef)
		teamID := assignments[[2]int{basket, number}]
		touchedMatches[slot.MatchID] = struct{}{}
		if _, err := tx.ExecContext(ctx, `update match_slots set participant_id = ? where id = ?`, util.NullableInt64(teamID), slot.ID); err != nil {
			return err
		}
	}
	for matchID := range touchedMatches {
		if err := pruneMatchStateToSlots(ctx, tx, matchID, gameType); err != nil {
			return err
		}
	}
	return nil
}

func pruneMatchStateToSlots(ctx context.Context, tx *sql.Tx, matchID int64, gameType string) error {
	if _, err := tx.ExecContext(ctx, `
delete from match_results
where match_id = ?
  and not exists (
    select 1
    from match_slots ms
    where ms.match_id = match_results.match_id
      and ms.participant_id = match_results.participant_id
  )`, matchID); err != nil {
		return err
	}
	if !store.TeamBlobShaped(gameType) {
		// A document Protocol keys state by side, so a reseated slot needs no
		// state surgery; the team-keyed blob drops the departed team's part.
		return nil
	}
	seated, err := store.CollectRows(ctx, tx, `
select participant_id from match_slots where match_id = ? and participant_id is not null`,
		[]any{matchID}, func(rows *sql.Rows) (int64, error) {
			var id int64
			return id, rows.Scan(&id)
		})
	if err != nil {
		return err
	}
	return festwrite.MutateMatchBlobTx(ctx, tx, matchID, func(blob *store.MatchBlob) error {
		keep := map[string]bool{}
		for _, id := range seated {
			keep[strconv.FormatInt(id, 10)] = true
		}
		for key := range blob.Participants {
			if !keep[key] {
				blob.RemoveParticipant(key)
			}
		}
		return nil
	})
}

func maxSeedNumber(ctx context.Context, q store.Queryer, gameID int64) (int, error) {
	rows, err := q.QueryContext(ctx, `
select ms.source_ref_json
from match_slots ms
join matches m on m.id = ms.match_id
where m.game_id = ? and ms.source_type = 'seed'`, gameID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	maxNumber := 0
	for rows.Next() {
		var sourceRef string
		if err := rows.Scan(&sourceRef); err != nil {
			return 0, err
		}
		_, number := seedRefKey(sourceRef)
		if number > maxNumber {
			maxNumber = number
		}
	}
	return maxNumber, rows.Err()
}

func seedRefKey(sourceRef string) (int, int) {
	ref := store.ParseSlotRef(store.SlotSeed, sourceRef)
	return ref.Basket, ref.Number
}

// standingsCandidates reads a source Game's table — the one Block's
// stage_standings the server ranked (ADR-0011) — as seed candidates: in
// rank order, ties within a shared place broken by the Protocol's own
// Metrics, or re-sorted by the [init] sorting rules when the scheme names
// some. A team the source document marks as declined comes pre-declined. A
// Game of more than one table is refused: which of them seeds is not defined.
func standingsCandidates(ctx context.Context, q store.Queryer, festID int64, gameCode string, rules []store.SchemeSortRule) (int64, []seedCandidate, error) {
	doc, err := store.LoadGameDocByCode(ctx, q, festID, gameCode)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil, fmt.Errorf("в фесте нет игры с кодом %s", gameCode)
	}
	if err != nil {
		return 0, nil, err
	}
	gameID, gameType, document := doc.GameID, doc.GameType, doc.State
	stages, err := store.CollectRows(ctx, q, `
select distinct stage_id from stage_standings st join stages s on s.id = st.stage_id where s.game_id = ?`, []any{gameID},
		func(rows *sql.Rows) (int64, error) {
			var id int64
			err := rows.Scan(&id)
			return id, err
		})
	if err != nil {
		return 0, nil, err
	}
	switch {
	case len(stages) == 0:
		return 0, nil, fmt.Errorf("у игры %s ещё нет таблицы", gameCode)
	case len(stages) > 1:
		return 0, nil, fmt.Errorf("у игры %s %d таблиц — посев берётся из игры с одной", gameCode, len(stages))
	}
	type row struct {
		name   string
		number int
		entry  structure.RankedEntry
	}
	rows, err := store.CollectRows(ctx, q, `
select st.rank, p.name, coalesce(p.number, 0), st.metrics_json
from stage_standings st join participants p on p.id = st.participant_id
where st.stage_id = ? order by st.rank`, []any{stages[0]}, func(rs *sql.Rows) (row, error) {
		var r row
		var metrics string
		if err := rs.Scan(&r.entry.Rank, &r.name, &r.number, &metrics); err != nil {
			return r, err
		}
		_ = json.Unmarshal([]byte(metrics), &r.entry.Metrics)
		return r, nil
	})
	if err != nil {
		return 0, nil, err
	}
	if len(rows) == 0 {
		return 0, nil, errors.New("в игре-источнике нет команд")
	}
	// The table's own rank comes first (the Structure ranked it, ADR-0011);
	// the [init] sorting rules, when a scheme names some, re-sort within it
	// by the Metrics the Protocol measured. Rows keep their table order as
	// the final tie-break.
	for i := range rows {
		rows[i].entry.Participant = int64(i)
		if rows[i].entry.Metrics == nil {
			rows[i].entry.Metrics = map[string]float64{}
		}
		rows[i].entry.Metrics["rank"] = float64(rows[i].entry.Rank)
	}
	order := []store.SchemeSortRule{{Metric: "rank", Dir: "asc"}}
	if len(rules) > 0 {
		order = rules
	}
	for _, rule := range rules {
		known := false
		for _, r := range rows {
			_, known = r.entry.Metrics[rule.Metric]
			if known {
				break
			}
		}
		if !known {
			return 0, nil, fmt.Errorf("sorting: %s — игра %s такой метрики не считает", rule.Metric, gameCode)
		}
	}
	less := structure.LessBy(order)
	sort.SliceStable(rows, func(i, j int) bool { return less(rows[i].entry, rows[j].entry) })
	declined := map[int64]bool{}
	if seats, ok := protocol.Seats(gameType, json.RawMessage(document)); ok {
		for _, seat := range seats {
			if seat.Declined {
				declined[seat.Number] = true
			}
		}
	}
	candidates := make([]seedCandidate, len(rows))
	for i, r := range rows {
		candidates[i] = seedCandidate{SourceRank: i + 1, Name: r.name, Number: r.number, Declined: declined[int64(r.number)]}
	}
	return gameID, candidates, nil
}

// randomSeedCandidates orders the fest's numbered teams by a deterministic
// per-game lot, so re-pressing the import re-draws nothing.
func randomSeedCandidates(ctx context.Context, q store.Queryer, scope core.FestScope) ([]seedCandidate, error) {
	roster, err := loadSeedRosterTeams(ctx, q, scope.FestID)
	if err != nil {
		return nil, err
	}
	type lotted struct {
		team seedRosterTeam
		lot  uint64
	}
	lots := make([]lotted, 0, len(roster))
	for _, team := range roster {
		if team.Number <= 0 {
			continue
		}
		lots = append(lots, lotted{team: team, lot: seedLot(scope.GameID, team.Number)})
	}
	if len(lots) == 0 {
		return nil, errors.New("в фесте нет пронумерованных команд")
	}
	sort.Slice(lots, func(i, j int) bool { return lots[i].lot < lots[j].lot })
	candidates := make([]seedCandidate, len(lots))
	for i, entry := range lots {
		candidates[i] = seedCandidate{SourceRank: i + 1, Name: entry.team.Name, Number: int(entry.team.Number)}
	}
	return candidates, nil
}

func loadSeedRosterTeams(ctx context.Context, q store.Queryer, festID int64) ([]seedRosterTeam, error) {
	rows, err := q.QueryContext(ctx, `
select id, coalesce(number, 0), name, city
from fest_teams
where fest_id = ? and deleted = 0
order by position, id`, festID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type teamRow struct {
		ID     int64
		Number int64
		Name   string
		City   string
	}
	var teamRows []teamRow
	for rows.Next() {
		var row teamRow
		if err := rows.Scan(&row.ID, &row.Number, &row.Name, &row.City); err != nil {
			return nil, err
		}
		teamRows = append(teamRows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	out := make([]seedRosterTeam, 0, len(teamRows))
	for _, row := range teamRows {
		players, err := loadSeedRosterPlayers(ctx, q, row.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, seedRosterTeam{Number: row.Number, Name: row.Name, City: row.City, Players: players})
	}
	return out, nil
}

func loadSeedRosterPlayers(ctx context.Context, q store.Queryer, festTeamID int64) ([]rosterpkg.SeedRosterPlayer, error) {
	return store.CollectRows(ctx, q, `
select p.first_name, p.last_name
from fest_team_players ftp
join fest_players p on p.id = ftp.player_id
where ftp.team_id = ?
order by ftp.roster_order, p.id`, []any{festTeamID}, func(rows *sql.Rows) (rosterpkg.SeedRosterPlayer, error) {
		var player rosterpkg.SeedRosterPlayer
		if err := rows.Scan(&player.FirstName, &player.LastName); err != nil {
			return player, err
		}
		return player, nil
	})
}

// EnsureSeedTeamByNumber finds or creates the game-scoped team identified by its
// universal NUMBER (not name), updating name/city to the current rosterpkg. Because
// number is the identity, two same-named teams stay distinct and re-seeding
// follows a team across name changes — the EK side of the team-number
// unification. Falls back to name-keyed rosterpkg.EnsureSeedTeam when no number is known.
func EnsureSeedTeamByNumber(ctx context.Context, tx *sql.Tx, festID, number int64, name, city string, players []rosterpkg.SeedRosterPlayer) (int64, string, error) {
	name = strings.TrimSpace(name)
	city = strings.TrimSpace(city)
	if number <= 0 {
		return rosterpkg.EnsureSeedTeam(ctx, tx, festID, name, city, players)
	}
	if name == "" {
		return 0, "", errors.New("empty team name")
	}
	teamID, err := store.EnsureParticipantByNumber(ctx, tx, festID, "team", number, name, city)
	if err != nil {
		return 0, "", err
	}
	if err := tx.QueryRowContext(ctx, `select city from participants where id = ?`, teamID).Scan(&city); err != nil {
		return 0, "", err
	}
	if len(players) > 0 {
		if err := rosterpkg.ReplaceSeedTeamRoster(ctx, tx, festID, teamID, players); err != nil {
			return 0, "", err
		}
	}
	return teamID, city, nil
}

// EnsureSeedPlayerByNumber is EnsureSeedTeamByNumber for an individual format:
// the Participant it ensures is one player of the fest roster, carrying
// roster='player' and a link back to the fest_players row it was drawn from
// (ADR-0007). Личная СИ seats these.
func EnsureSeedPlayerByNumber(ctx context.Context, tx *sql.Tx, festID, number int64, name string, festPlayerID int64) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, errors.New("empty player name")
	}
	var participantID int64
	err := tx.QueryRowContext(ctx, `
select id from participants
where fest_id = ? and roster = 'player' and number = ? limit 1`, festID, number).Scan(&participantID)
	if errors.Is(err, sql.ErrNoRows) {
		return store.InsertReturningID(ctx, tx, `
insert into participants(fest_id, roster, name, city, number, fest_player_id)
values(?, 'player', ?, '', ?, ?)`, festID, name, number, festPlayerID)
	}
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `
update participants set name = ?, fest_player_id = ? where id = ?`,
		name, festPlayerID, participantID); err != nil {
		return 0, err
	}
	return participantID, nil
}
