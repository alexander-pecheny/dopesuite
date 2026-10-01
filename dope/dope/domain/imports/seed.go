package imports

import (
	"context"
	"database/sql"

	"github.com/xuri/excelize/v2"

	"dope/dope/domain/core"
	"dope/dope/domain/games"
	"dope/dope/domain/overrides"
	"dope/dope/domain/protocol"
	rosterpkg "dope/dope/domain/roster"
	"dope/dope/domain/structure"
	"dope/dope/platform/util"
	"dope/dope/storage/festwrite"
	"dope/dope/storage/store"
	dopestrings "dope/i18nstrings"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	corei18n "pecheny.me/dopecore/i18nstrings"
	"sort"
	"strconv"
	"strings"
)

const (
	seedImportStateKey = "seedImport"
	seedSourceKSI      = "ksi"
)

// seedImportState is a Game's Entrant list (CONTEXT.md) as it is stored: under
// the seedImport key of games.state_json, whatever the format. Its name is the
// ladder's from before the list could be edited by hand.
type seedImportState struct {
	Source       string `json:"source,omitempty"`
	SourceGameID int64  `json:"sourceGameID,omitempty"`
	// Division keeps the import to one division (ADR-0020); a re-import repeats it.
	Division string `json:"division,omitempty"`
	// Edited says the host has added, removed, moved or renamed an entrant
	// since the last import, which a re-import would throw away.
	Edited bool                 `json:"edited,omitempty"`
	Rows   []seedImportStateRow `json:"rows,omitempty"`
	// Edits are the host's hand edits since the last import, in order, which
	// a re-import from the source applies again (ADR-0025).
	Edits []ListEdit `json:"edits,omitempty"`
	// Unranked and MovesDropped are what the last import has to tell the host:
	// whom it seeded last for want of anything to rank them by, and how many
	// hand moves it did not apply because it read another source. They stand
	// until the next import, so the tab can say them however often it is read.
	Unranked     []string `json:"unranked,omitempty"`
	MovesDropped int      `json:"movesDropped,omitempty"`
}

// seedImportStateRow is one entrant. TeamID is the Participant, whatever it
// is in this format: a team, a troika or a player.
type seedImportStateRow struct {
	SourceRank int    `json:"sourceRank"`
	TeamID     int64  `json:"teamID"`
	Name       string `json:"name"`
	City       string `json:"city,omitempty"`
	Declined   bool   `json:"declined,omitempty"`
}

type SeedImportView struct {
	// Declared is the source the Game's [init] names ("" when it names none):
	// random, players, xlsx or a Game's code. DeclaredTitle is that Game's
	// title, for the page's import button.
	Declared      string `json:"declared,omitempty"`
	DeclaredTitle string `json:"declaredTitle,omitempty"`
	Source        string `json:"source,omitempty"`
	SourceGameID  int64  `json:"sourceGameID,omitempty"`
	Division      string `json:"division,omitempty"`
	Edited        bool   `json:"edited,omitempty"`
	// Edits counts the hand edits a re-import would apply again.
	Edits       int                 `json:"edits,omitempty"`
	DrawSize    int                 `json:"drawSize"`
	ActiveCount int                 `json:"activeCount"`
	Rows        []SeedImportViewRow `json:"rows"`
}

// SeedImportViewRow is one entrant as the entrants tab shows it. SeedNumber
// is the seat it holds in the Structure (0: none — declined, or on the waiting
// list). Played says it sits in a bout that has begun, so it can be neither
// removed nor renamed and its seat no longer moves; OneOff says the host typed
// it in for this Game alone.
type SeedImportViewRow struct {
	SourceRank int    `json:"sourceRank"`
	SeedNumber int    `json:"seedNumber,omitempty"`
	TeamID     int64  `json:"teamID"`
	Name       string `json:"name"`
	City       string `json:"city,omitempty"`
	Declined   bool   `json:"declined"`
	Waitlist   bool   `json:"waitlist"`
	Played     bool   `json:"played,omitempty"`
	OneOff     bool   `json:"oneOff,omitempty"`
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
	// Participant is set for an assembled team (a troika): it has no fest
	// number and no fest_teams row, so it is named by its Participant id.
	Participant int64
}

func LoadSeedImportView(eng *core.Engine, ctx context.Context, scope core.FestScope) (SeedImportView, error) {
	return LoadListView(ctx, eng.DB, scope)
}

// declareSeeding tells the view where the Game's [init] says its seeding comes
// from, with the source Game's title when it names one.
func declareSeeding(ctx context.Context, q store.Queryer, scope core.FestScope, view *SeedImportView) error {
	declared, err := loadSchemeSeeding(ctx, q, scope)
	if err != nil {
		return err
	}
	view.Declared, view.DeclaredTitle = declared.Source, ""
	switch declared.Source {
	case "", "random", "players", "xlsx":
		return nil
	}
	err = q.QueryRowContext(ctx, `
select title from games where fest_id = ? and code = ?`, scope.FestID, declared.Source).Scan(&view.DeclaredTitle)
	if errors.Is(err, sql.ErrNoRows) {
		view.DeclaredTitle = declared.Source
		return nil
	}
	return err
}

// seedCandidate is one row of a seed source's current standings, whatever the
// source game type (or lot) was.
type seedCandidate struct {
	SourceRank int
	Name       string
	Number     int
	Declined   bool
	// ParticipantID names the Participant itself when the source knows it
	// and a fest number would not find it: a troika, a player, a one-off.
	ParticipantID int64
}

// A SeedSource ranks the fest's Participants for a Game's seeding: the
// fest's first KSI (the EK page's button), whatever the Game's [init]
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
	division      string
	candidates    []seedCandidate
	// unranked names the candidates the source had nothing to rank by and put
	// last: a troika whose people the fest roster does not know.
	unranked []string
}

// FromKSI is the EK page's "Import from KSI" button: the fest's first KSI, ranked.
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
		return seeding{}, corei18n.User(dopestrings.Default.Imports.Seed.KsiMissing())
	}
	if err != nil {
		return seeding{}, err
	}
	gameID, candidates, err := standingsCandidates(ctx, tx, scope.FestID, code, nil)
	return seeding{source: seedSourceKSI, label: dopestrings.Default.Imports.SeedSource.Ksi(), sourceGameID: gameID, candidates: candidates}, err
}

type fromScheme struct{}

func (fromScheme) resolve(ctx context.Context, tx *sql.Tx, scope core.FestScope) (seeding, error) {
	declared, err := loadSchemeSeeding(ctx, tx, scope)
	if err != nil {
		return seeding{}, err
	}
	switch declared.Source {
	case "":
		return seeding{}, corei18n.User(dopestrings.Default.Imports.Seed.SchemeMissing())
	case "xlsx":
		return seeding{}, corei18n.User(dopestrings.Default.Imports.Seed.SchemeXlsx())
	case "random":
		candidates, err := randomSeedCandidates(ctx, tx, scope)
		return seeding{source: "random", label: dopestrings.Default.Imports.SeedSource.Random(), candidates: candidates}, err
	case "players":
		return FromPlayers(declared.Players, declared.Sort).resolve(ctx, tx, scope)
	}
	return fromGame{code: declared.Source, division: declared.Division, sort: declared.Sort}.resolve(ctx, tx, scope)
}

// inDivision keeps the candidates of one Division, ranked afresh inside it: the
// teams carrying the Flag, or with a leading minus the teams not carrying it.
// A candidate is a fest team by its number, which is where its Flags are.
func inDivision(ctx context.Context, q store.Queryer, festID int64, division string, candidates []seedCandidate) ([]seedCandidate, error) {
	flag, exclude := strings.CutPrefix(division, "-")
	flag = strings.TrimSpace(flag)
	carriers, err := store.CollectRows(ctx, q, `
select t.number from fest_teams t join fest_team_flags f on f.team_id = t.id
where t.fest_id = ? and t.deleted = 0 and t.number is not null and f.short = ?`, []any{festID, flag},
		func(rows *sql.Rows) (int, error) {
			var n int
			return n, rows.Scan(&n)
		})
	if err != nil {
		return nil, err
	}
	carries := make(map[int]bool, len(carriers))
	for _, n := range carriers {
		carries[n] = true
	}
	var out []seedCandidate
	for _, c := range candidates {
		if carries[c.Number] != exclude {
			c.SourceRank = len(out) + 1
			out = append(out, c)
		}
	}
	return out, nil
}

type fromXLSX struct{ file io.Reader }

func (f fromXLSX) resolve(ctx context.Context, tx *sql.Tx, scope core.FestScope) (seeding, error) {
	roster, err := seedRosterForGame(ctx, tx, scope)
	if err != nil {
		return seeding{}, err
	}
	candidates, err := parseSeedXLSX(f.file, scope.GameID, roster)
	return seeding{source: "xlsx", label: "xlsx", candidates: candidates}, err
}

// ImportSeeds seeds the Game from the source: the source's current order is
// snapshotted into the Entrant list (partial results mid-fest are a normal
// source), previous declines survive, and every seed slot nobody has started
// is reseated. A Game whose Structure is sized by its entrants is reshaped by
// the entrants package, which calls ResolveListTx and SaveListTx itself.
func ImportSeeds(eng *core.Engine, ctx context.Context, scope core.FestScope, src SeedSource) (SeedImportView, int64, []byte, error) {
	var view SeedImportView
	var revision int64
	var stateJSON []byte
	err := eng.WithWriteTx(ctx, scope.FestID, "seed-import", func(ctx context.Context, tx *sql.Tx) error {
		list, err := LoadListTx(ctx, tx, scope)
		if err != nil {
			return err
		}
		next, event, err := ResolveListTx(ctx, tx, scope, list, src)
		if err != nil {
			return err
		}
		view, revision, stateJSON, err = SaveListTx(ctx, tx, scope, list, next, event)
		return err
	})
	if err != nil {
		return SeedImportView{}, 0, nil, err
	}
	return view, revision, stateJSON, nil
}

// ResolveListTx runs a source and returns the Entrant list it makes, merged
// with the list there is (its declines survive), and the event to record it
// under.
func ResolveListTx(ctx context.Context, tx *sql.Tx, scope core.FestScope, current List, src SeedSource) (List, string, error) {
	resolved, err := src.resolve(ctx, tx, scope)
	if err != nil {
		return List{}, "", err
	}
	next, err := listFromSeedingTx(ctx, tx, scope, current.State, resolved)
	if err != nil {
		return List{}, "", err
	}
	list := current.with(next)
	list.Unranked = resolved.unranked
	return list, "seed-import:" + resolved.source, nil
}

func parseSeedXLSX(file io.Reader, gameID int64, roster []seedRosterTeam) ([]seedCandidate, error) {
	book, err := excelize.OpenReader(file)
	if err != nil {
		return nil, corei18n.User(dopestrings.Default.Imports.Seed.XlsxOpen(err.Error()))
	}
	defer book.Close()
	sheets := book.GetSheetList()
	if len(sheets) == 0 {
		return nil, corei18n.User(dopestrings.Default.Imports.Seed.XlsxNoSheets())
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
			return nil, corei18n.User(dopestrings.Default.Imports.Seed.XlsxTeamUnknown(strconv.Itoa(i+1), key))
		}
		basket := 0
		if len(row) > 1 && strings.TrimSpace(row[1]) != "" {
			if basket, err = strconv.Atoi(strings.TrimSpace(row[1])); err != nil {
				return nil, corei18n.User(dopestrings.Default.Imports.Seed.XlsxBasketNotNumber(strconv.Itoa(i+1), row[1]))
			}
			baskets = true
		}
		parsed = append(parsed, parsedRow{team: team, basket: basket})
	}
	if len(parsed) == 0 {
		return nil, corei18n.User(dopestrings.Default.Imports.Seed.XlsxNoTeams())
	}
	if baskets {
		for i, row := range parsed {
			if row.basket <= 0 {
				return nil, corei18n.User(dopestrings.Default.Imports.Seed.XlsxBasketMissing(row.team.Name, strconv.Itoa(i+1)))
			}
		}
		sort.SliceStable(parsed, func(i, j int) bool {
			if parsed[i].basket != parsed[j].basket {
				return parsed[i].basket < parsed[j].basket
			}
			return seedLot(gameID, parsed[i].team.lotKey()) < seedLot(gameID, parsed[j].team.lotKey())
		})
	}
	candidates := make([]seedCandidate, len(parsed))
	for i, row := range parsed {
		candidates[i] = seedCandidate{SourceRank: i + 1, Name: row.team.Name, Number: int(row.team.Number), ParticipantID: row.team.Participant}
	}
	return candidates, nil
}

// lotKey is what a team's lot is drawn on: its fest number, or for a troika,
// which has none, its Participant id.
func (t seedRosterTeam) lotKey() int64 {
	if t.Number > 0 {
		return t.Number
	}
	return t.Participant
}

func seedLot(gameID, number int64) uint64 {
	h := fnv.New64a()
	fmt.Fprintf(h, "seed-lot:%d:%d", gameID, number)
	return h.Sum64()
}

// listFromSeedingTx turns what a source resolved to into the Game's new
// Entrant list: each candidate becomes its Participant (a fest team by its
// number, recovered by name for a legacy number-less source; the Participant
// itself when the source names it), a Participant listed twice is refused,
// and every decline of the previous list is kept.
func listFromSeedingTx(ctx context.Context, tx *sql.Tx, scope core.FestScope, previous seedImportState, resolved seeding) (seedImportState, error) {
	previousDeclinesByTeam, previousDeclinesByName := previousSeedDeclines(previous)

	roster, err := loadSeedRosterTeams(ctx, tx, scope.FestID)
	if err != nil {
		return seedImportState{}, err
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

	rows := make([]seedImportStateRow, 0, len(resolved.candidates))
	seenTeams := make(map[int64]string, len(resolved.candidates))
	for _, candidate := range resolved.candidates {
		var teamID int64
		var city string
		if candidate.ParticipantID > 0 {
			teamID = candidate.ParticipantID
			if err := tx.QueryRowContext(ctx, `select coalesce(city, '') from participants where id = ? and fest_id = ?`, teamID, scope.FestID).Scan(&city); err != nil {
				return seedImportState{}, err
			}
		} else {
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
			if number > 0 {
				teamID, city, err = EnsureSeedTeamByNumber(ctx, tx, scope.FestID, number, candidate.Name, rt.City, rt.Players)
			} else {
				teamID, city, err = rosterpkg.EnsureSeedTeam(ctx, tx, scope.FestID, candidate.Name, rt.City, rt.Players)
			}
			if err != nil {
				return seedImportState{}, err
			}
		}
		if previous, exists := seenTeams[teamID]; exists {
			return seedImportState{}, corei18n.User(dopestrings.Default.Imports.Seed.TeamTwice(resolved.label, candidate.Name, previous))
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

	return seedImportState{
		Source:       resolved.source,
		SourceGameID: resolved.sourceGameID,
		Division:     resolved.division,
		Rows:         rows,
	}, nil
}

// SetSeedImportDeclined marks an entrant as having refused to play, or takes
// the mark back. The next entrant moves up into the seat, in every bout nobody
// has started.
func SetSeedImportDeclined(eng *core.Engine, ctx context.Context, scope core.FestScope, req SeedDeclineRequest) (SeedImportView, int64, []byte, error) {
	if req.TeamID <= 0 {
		return SeedImportView{}, 0, nil, errors.New("bad team id")
	}
	var view SeedImportView
	var revision int64
	var stateJSON []byte
	err := eng.WithWriteTx(ctx, scope.FestID, "seed-import-decline", func(ctx context.Context, tx *sql.Tx) error {
		list, err := LoadListTx(ctx, tx, scope)
		if err != nil {
			return err
		}
		next, err := list.Decline(req.TeamID, req.Declined)
		if err != nil {
			return err
		}
		view, revision, stateJSON, err = SaveListTx(ctx, tx, scope, list, next, "seed-import:decline")
		return err
	})
	if err != nil {
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

// SaveListTx writes the Game's Entrant list and seats it (seatListTx), records
// the event and returns the list as the entrants tab shows it, with the
// document the game-state scope broadcasts. current is the list as it was
// loaded, whose state blob the new list is written into.
func SaveListTx(ctx context.Context, tx *sql.Tx, scope core.FestScope, current, next List, eventType string) (SeedImportView, int64, []byte, error) {
	stateJSON, err := putSeedImportState(current.Raw, next.State)
	if err != nil {
		return SeedImportView{}, 0, nil, err
	}
	if _, err := tx.ExecContext(ctx, `
update games set state_json = ?, updated_at = ?
where fest_id = ? and id = ?`, string(stateJSON), util.UtcNow(), scope.FestID, scope.GameID); err != nil {
		return SeedImportView{}, 0, nil, err
	}
	if err := seatListTx(ctx, tx, scope, current.GameType, next.State.Rows); err != nil {
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
	revision, err := festwrite.BumpFestRevisionTx(ctx, tx, scope.FestID, eventType, util.MustJSON(map[string]any{
		"gameID":       scope.GameID,
		"source":       next.State.Source,
		"sourceGameID": next.State.SourceGameID,
		"rows":         len(next.State.Rows),
	}))
	if err != nil {
		return SeedImportView{}, 0, nil, err
	}
	view, err := LoadListView(ctx, tx, scope)
	return view, revision, stateJSON, err
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
		// A played match keeps its participants: a decline shifts the ladder
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
		return 0, nil, corei18n.User(dopestrings.Default.Imports.Seed.GameMissing(gameCode))
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
		return 0, nil, corei18n.User(dopestrings.Default.Imports.Seed.NoStandings(gameCode))
	case len(stages) > 1:
		return 0, nil, corei18n.User(dopestrings.Default.Imports.Seed.MultipleStandings(gameCode, len(stages)))
	}
	type row struct {
		id     int64
		name   string
		number int
		player bool
		entry  structure.RankedEntry
	}
	rows, err := store.CollectRows(ctx, q, `
select st.rank, p.id, p.name, coalesce(p.number, 0), p.roster = 'player', st.metrics_json
from stage_standings st join participants p on p.id = st.participant_id
where st.stage_id = ? order by st.rank`, []any{stages[0]}, func(rs *sql.Rows) (row, error) {
		var r row
		var metrics string
		if err := rs.Scan(&r.entry.Rank, &r.id, &r.name, &r.number, &r.player, &metrics); err != nil {
			return r, err
		}
		_ = json.Unmarshal([]byte(metrics), &r.entry.Metrics)
		return r, nil
	})
	if err != nil {
		return 0, nil, err
	}
	if len(rows) == 0 {
		return 0, nil, corei18n.User(dopestrings.Default.Imports.Seed.SourceNoTeams())
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
			return 0, nil, corei18n.User(dopestrings.Default.Imports.Seed.MetricUnknown(rule.Metric, gameCode))
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
		// A fest team is found again by its number, which also refreshes its
		// people from the roster; a player comes as itself.
		if r.player {
			candidates[i].ParticipantID = r.id
		}
	}
	return gameID, candidates, nil
}

// randomSeedCandidates orders the fest's numbered teams by a deterministic
// per-game lot, so re-pressing the import re-draws nothing.
func randomSeedCandidates(ctx context.Context, q store.Queryer, scope core.FestScope) ([]seedCandidate, error) {
	roster, err := seedRosterForGame(ctx, q, scope)
	if err != nil {
		return nil, err
	}
	type lotted struct {
		team seedRosterTeam
		lot  uint64
	}
	lots := make([]lotted, 0, len(roster))
	for _, team := range roster {
		if team.lotKey() <= 0 {
			continue
		}
		lots = append(lots, lotted{team: team, lot: seedLot(scope.GameID, team.lotKey())})
	}
	if len(lots) == 0 {
		return nil, corei18n.User(dopestrings.Default.Imports.Seed.NoNumberedTeams())
	}
	sort.Slice(lots, func(i, j int) bool { return lots[i].lot < lots[j].lot })
	candidates := make([]seedCandidate, len(lots))
	for i, entry := range lots {
		candidates[i] = seedCandidate{SourceRank: i + 1, Name: entry.team.Name, Number: int(entry.team.Number), ParticipantID: entry.team.Participant}
	}
	return candidates, nil
}

// seedRosterForGame is who a Game's xlsx and random seeds draw from: the
// fest's rating teams, or — for a Troika game, which seats troikas — the fest's
// troikas. A troika is not a fest team: it has no fest number and no
// fest_teams row, so a sheet naming troikas used to find none of them.
func seedRosterForGame(ctx context.Context, q store.Queryer, scope core.FestScope) ([]seedRosterTeam, error) {
	var gameType string
	if err := q.QueryRowContext(ctx, `select game_type from games where fest_id = ? and id = ?`,
		scope.FestID, scope.GameID).Scan(&gameType); err != nil {
		return nil, err
	}
	if gameType != games.Troika {
		return loadSeedRosterTeams(ctx, q, scope.FestID)
	}
	return store.CollectRows(ctx, q, `
select id, name, coalesce(city, '') from participants
where fest_id = ? and assembled = 1
order by id`, []any{scope.FestID}, func(rows *sql.Rows) (seedRosterTeam, error) {
		var team seedRosterTeam
		return team, rows.Scan(&team.Participant, &team.Name, &team.City)
	})
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
// (ADR-0007). Individual SI seats these.
func EnsureSeedPlayerByNumber(ctx context.Context, tx *sql.Tx, festID, number int64, name string, festPlayerID int64) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, errors.New("empty player name")
	}
	var participantID, holder int64
	// A player who already has a Participant keeps it, whatever number the
	// roster gives them now: a re-import can shift the ranks.
	if festPlayerID > 0 {
		err := tx.QueryRowContext(ctx, `
select id from participants
where fest_id = ? and roster = 'player' and fest_player_id = ? and game_id is null
order by id limit 1`, festID, festPlayerID).Scan(&participantID)
		if err == nil {
			_, err = tx.ExecContext(ctx, `update participants set name = ? where id = ?`, name, participantID)
			return participantID, err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
	}
	err := tx.QueryRowContext(ctx, `
select id, coalesce(fest_player_id, 0) from participants
where fest_id = ? and roster = 'player' and number = ? and game_id is null limit 1`, festID, number).Scan(&participantID, &holder)
	if err == nil && holder != 0 && holder != festPlayerID && festPlayerID > 0 {
		// The number belongs to another person, whose results sit on that
		// Participant: taking it over would hand them to this player. This one
		// gets the next free number instead.
		if err := tx.QueryRowContext(ctx, `
select coalesce(max(number), 0) + 1 from participants where fest_id = ? and roster = 'player'`, festID).Scan(&number); err != nil {
			return 0, err
		}
		err = sql.ErrNoRows
	}
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
