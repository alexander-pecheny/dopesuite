package imports

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"sort"
	"strings"

	"dope/dope/domain/core"
	"dope/dope/domain/games"
	rosterpkg "dope/dope/domain/roster"
	"dope/dope/domain/schemedsl"
	"dope/dope/storage/store"
	dopestrings "dope/i18nstrings"
	corei18n "pecheny.me/dopecore/i18nstrings"
)

// A Game's Entrant list (CONTEXT.md) is who a buzzer Game seats, in seed
// order: EK, ES, Brain, Troika, Hamsa and individual SI all keep one. It is stored
// under the seedImport key of games.state_json, and the Structure reads it
// through game_assignments: the list's active entrants take the Structure's
// seed numbers in order. A bout that has begun keeps whoever sits in it, so an
// entrant seated there holds its number however the list changes (a pinned
// seat), and the others fill the numbers that are left. An entrant with no
// number left is on the waiting list.

// ListState and ListRow are the stored list and one entrant of it.
type (
	ListState = seedImportState
	ListRow   = seedImportStateRow
)

// List is a Game's Entrant list as it was loaded: the Game's format, the state
// blob it lives in, and the list itself. A Game that has not stored one yet
// reads the entrants its Structure seats (Synthesized).
type List struct {
	GameType    string
	Raw         string
	State       ListState
	Synthesized bool
	// Unranked names who the import that made the list seeded last for want
	// of anything to rank them by. The import keeps it in State as well, for
	// the tab to say again when it is read back.
	Unranked []string
}

func (l List) with(state ListState) List {
	return List{GameType: l.GameType, Raw: l.Raw, State: state}
}

// With is the list holding state instead, in the same Game.
func (l List) With(state ListState) List { return l.with(state) }

// Index is where the Participant stands in the list, or -1.
func (l List) Index(participantID int64) int {
	for i, row := range l.State.Rows {
		if row.TeamID == participantID {
			return i
		}
	}
	return -1
}

// Active is the Participants that play, in list order: all but the declined.
func (l List) Active() []int64 {
	var out []int64
	for _, row := range l.State.Rows {
		if !row.Declined {
			out = append(out, row.TeamID)
		}
	}
	return out
}

// Decline marks the entrant as having refused to play, or takes that back.
func (l List) Decline(participantID int64, declined bool) (List, error) {
	if len(l.State.Rows) == 0 {
		return List{}, corei18n.User(dopestrings.Default.Imports.Seed.NothingImported())
	}
	i := l.Index(participantID)
	if i < 0 {
		return List{}, corei18n.User(dopestrings.Default.Imports.Seed.TeamNotFound())
	}
	state := l.clone()
	state.Rows[i].Declined = declined
	return l.with(state), nil
}

func (l List) clone() ListState {
	state := l.State
	state.Rows = slices.Clone(l.State.Rows)
	return state
}

// Edit is the list after a hand edit: its rows as given, marked Edited, with
// the edits that make it logged so a re-import can apply them again
// (ADR-0025).
func (l List) Edit(rows []ListRow, edits ...ListEdit) List {
	state := l.clone()
	state.Rows = rows
	state.Edited = true
	for _, edit := range edits {
		state.Edits = logListEdit(state.Edits, edit)
	}
	return l.with(state)
}

// ListEdit is one hand edit of an entrant list: an entrant added (at the end),
// taken out, or moved to a place (1 is the top). Name and City say who an
// added entrant is, for a list the source rebuilt without them.
type ListEdit struct {
	Op       string `json:"op"`
	TeamID   int64  `json:"teamID"`
	Name     string `json:"name,omitempty"`
	City     string `json:"city,omitempty"`
	Position int    `json:"position,omitempty"`
}

const (
	ListEditAdd    = "add"
	ListEditRemove = "remove"
	ListEditMove   = "move"
)

// logListEdit appends an edit, folding what it undoes: taking out an entrant
// the host had added forgets both, and a move replaces an earlier move.
func logListEdit(log []ListEdit, edit ListEdit) []ListEdit {
	out := make([]ListEdit, 0, len(log)+1)
	added := false
	for _, e := range log {
		if e.TeamID != edit.TeamID {
			out = append(out, e)
			continue
		}
		switch {
		case e.Op == ListEditAdd && edit.Op == ListEditRemove:
			added = true
		case e.Op == ListEditMove && edit.Op != ListEditAdd:
		case e.Op == ListEditRemove && edit.Op == ListEditAdd:
			added = true
		default:
			out = append(out, e)
		}
	}
	// An add that cancels a remove, or a remove that cancels an add, leaves
	// nothing to replay: the entrant is where the source put it.
	if added && edit.Op != ListEditMove {
		return out
	}
	return append(out, edit)
}

// Replay applies the host's edits to rows a source has just made: the
// entrants added come back at the end, those taken out stay out, and the ones
// moved go to their places. An edit about an entrant the rows no longer have,
// or already have, changes nothing.
func Replay(rows []ListRow, edits []ListEdit) []ListRow {
	rows = slices.Clone(rows)
	index := func(id int64) int {
		for i, row := range rows {
			if row.TeamID == id {
				return i
			}
		}
		return -1
	}
	for _, e := range edits {
		i := index(e.TeamID)
		switch e.Op {
		case ListEditAdd:
			if i < 0 {
				rows = append(rows, ListRow{TeamID: e.TeamID, Name: e.Name, City: e.City})
			}
		case ListEditRemove:
			if i >= 0 {
				rows = slices.Delete(rows, i, i+1)
			}
		case ListEditMove:
			if i >= 0 {
				row := rows[i]
				rows = slices.Delete(rows, i, i+1)
				to := min(max(e.Position, 1), len(rows)+1) - 1
				rows = slices.Insert(rows, to, row)
			}
		}
	}
	return rows
}

// LoadListTx reads the Game's Entrant list. A Game that has none stored reads
// the Participants its Structure seats, in its own order; a row whose
// Participant has gone (a troika deleted since) is dropped.
func LoadListTx(ctx context.Context, q store.Queryer, scope core.FestScope) (List, error) {
	gameType, raw, err := loadSeedImportGame(ctx, q, scope)
	if err != nil {
		return List{}, err
	}
	state, err := seedImportStateFromRaw(raw)
	if err != nil {
		return List{}, err
	}
	list := List{GameType: gameType, Raw: raw, State: state}
	if len(state.Rows) == 0 {
		rows, err := seatedParticipantsTx(ctx, q, scope)
		if err != nil {
			return List{}, err
		}
		list.State.Rows = rows
		list.Synthesized = len(rows) > 0
		return list, nil
	}
	known, err := festParticipantIDs(ctx, q, scope.FestID)
	if err != nil {
		return List{}, err
	}
	list.State.Rows = knownRows(list.State.Rows, known)
	return list, nil
}

// knownRows drops the rows whose Participant the fest no longer has.
func knownRows(rows []seedImportStateRow, known map[int64]bool) []seedImportStateRow {
	out := rows[:0:0]
	for _, row := range rows {
		if known[row.TeamID] {
			out = append(out, row)
		}
	}
	return out
}

// seatedParticipantsTx is who the Game seats before it keeps a list of its own:
// the entrants it named, else whoever holds its seed numbers.
func seatedParticipantsTx(ctx context.Context, q store.Queryer, scope core.FestScope) ([]ListRow, error) {
	scan := func(rows *sql.Rows) (ListRow, error) {
		var row ListRow
		return row, rows.Scan(&row.TeamID, &row.Name, &row.City)
	}
	rows, err := store.CollectRows(ctx, q, `
select p.id, p.name, p.city from game_participants gp join participants p on p.id = gp.participant_id
where gp.game_id = ? order by gp.position`, []any{scope.GameID}, scan)
	if err != nil || len(rows) > 0 {
		return rows, err
	}
	return store.CollectRows(ctx, q, `
select p.id, p.name, p.city from game_assignments ga join participants p on p.id = ga.participant_id
where ga.game_id = ? and ga.basket = 1 order by ga.number`, []any{scope.GameID}, scan)
}

func festParticipantIDs(ctx context.Context, q store.Queryer, festID int64) (map[int64]bool, error) {
	ids, err := store.CollectRows(ctx, q, `select id from participants where fest_id = ?`, []any{festID},
		func(rows *sql.Rows) (int64, error) {
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

// seatNumbersTx is the seed numbers the Game's Structure has seats for, in
// order. A pasted scheme numbers them 1…N; a Game that seated the fest roster
// by the fest's own numbers has those.
func seatNumbersTx(ctx context.Context, q store.Queryer, gameID int64) ([]int, error) {
	refs, err := store.CollectRows(ctx, q, `
select ms.source_ref_json from match_slots ms join matches m on m.id = ms.match_id
where m.game_id = ? and ms.source_type = 'seed'`, []any{gameID}, func(rows *sql.Rows) (string, error) {
		var ref string
		return ref, rows.Scan(&ref)
	})
	if err != nil {
		return nil, err
	}
	seen := map[int]bool{}
	var out []int
	for _, ref := range refs {
		basket, number := seedRefKey(ref)
		if basket == 1 && number > 0 && !seen[number] {
			seen[number] = true
			out = append(out, number)
		}
	}
	sort.Ints(out)
	return out, nil
}

// startedMatch is one of the Game's bouts as the seating reads it.
type startedMatch struct {
	id     int64
	status string
	state  string
}

func (m startedMatch) begun(gameType string) bool {
	return m.status == "finished" || games.Started(gameType, m.state)
}

func gameMatchesTx(ctx context.Context, q store.Queryer, gameID int64) ([]startedMatch, error) {
	return store.CollectRows(ctx, q, `
select id, status, coalesce(state_json, '{}') from matches where game_id = ?`, []any{gameID},
		func(rows *sql.Rows) (startedMatch, error) {
			var m startedMatch
			return m, rows.Scan(&m.id, &m.status, &m.state)
		})
}

// GameEntered reports whether anything has been entered in a Game: a bout
// finished, or one with marks on it.
func GameEntered(ctx context.Context, q store.Queryer, gameID int64, gameType string) (bool, error) {
	matches, err := gameMatchesTx(ctx, q, gameID)
	if err != nil {
		return false, err
	}
	for _, m := range matches {
		if m.begun(gameType) {
			return true, nil
		}
	}
	return false, nil
}

// PlayedParticipants is who sits in a bout of the Game that has begun, with the
// seed number such a seat was dealt under (0 for a seat filled from a bout or a
// reseed). They have results, so they can be neither removed nor renamed, and
// a seed seat among them no longer moves.
func PlayedParticipants(ctx context.Context, q store.Queryer, gameID int64, gameType string) (map[int64]int, error) {
	matches, err := gameMatchesTx(ctx, q, gameID)
	if err != nil {
		return nil, err
	}
	out := map[int64]int{}
	for _, m := range matches {
		if !m.begun(gameType) {
			continue
		}
		type seat struct {
			participant int64
			sourceType  string
			ref         string
		}
		seats, err := store.CollectRows(ctx, q, `
select participant_id, source_type, source_ref_json from match_slots
where match_id = ? and participant_id is not null`, []any{m.id}, func(rows *sql.Rows) (seat, error) {
			var s seat
			return s, rows.Scan(&s.participant, &s.sourceType, &s.ref)
		})
		if err != nil {
			return nil, err
		}
		for _, s := range seats {
			number := 0
			if s.sourceType == store.SlotSeed {
				if basket, n := seedRefKey(s.ref); basket == 1 {
					number = n
				}
			}
			if prev, ok := out[s.participant]; !ok || prev == 0 {
				out[s.participant] = number
			}
		}
	}
	return out, nil
}

// EntrantSized reports whether a scheme DSL compiles against its Game's
// entrants, so the Structure's size follows the list: a DSL with no seed in
// [init]. A Game seeded from a source has a Structure of fixed size, and the
// list fills its seats.
func EntrantSized(dsl string) bool {
	if strings.TrimSpace(dsl) == "" {
		return false
	}
	doc, err := schemedsl.Parse(dsl)
	if err != nil {
		return false
	}
	_, seeded := doc.Init.Str("seed")
	return !seeded
}

// seatListTx gives the list's entrants their seed numbers and reseats every
// seed slot nobody has started. An entrant seated in a bout that has begun
// keeps its number, unless it has declined; the other active entrants take
// the numbers left, in list order, and those beyond the Structure's seats are
// numbered on past them — the waiting list. The Game's entrant record (game_participants) follows
// when the Game keeps one.
func seatListTx(ctx context.Context, tx *sql.Tx, scope core.FestScope, gameType string, rows []ListRow) error {
	numbers, err := seatNumbersTx(ctx, tx, scope.GameID)
	if err != nil {
		return err
	}
	played, err := PlayedParticipants(ctx, tx, scope.GameID, gameType)
	if err != nil {
		return err
	}
	seats := make(map[int]bool, len(numbers))
	for _, n := range numbers {
		seats[n] = true
	}
	// A declined entrant keeps its place in the bouts it has played, which the
	// seating never touches, and gives its seat up in the rest.
	declined := map[int64]bool{}
	for _, row := range rows {
		if row.Declined {
			declined[row.TeamID] = true
		}
	}
	byNumber := map[int]int64{}
	pinned := map[int64]bool{}
	for participant, number := range played {
		if seats[number] && !declined[participant] {
			byNumber[number] = participant
			pinned[participant] = true
		}
	}
	next := 0
	overflow := 0
	if len(numbers) > 0 {
		overflow = numbers[len(numbers)-1]
	}
	for _, row := range rows {
		if row.Declined || pinned[row.TeamID] || row.TeamID <= 0 {
			continue
		}
		for next < len(numbers) && byNumber[numbers[next]] != 0 {
			next++
		}
		if next < len(numbers) {
			byNumber[numbers[next]] = row.TeamID
			next++
			continue
		}
		overflow++
		byNumber[overflow] = row.TeamID
	}

	if _, err := tx.ExecContext(ctx, `delete from game_assignments where game_id = ?`, scope.GameID); err != nil {
		return err
	}
	assignments := make(map[[2]int]int64, len(byNumber))
	ordered := make([]int, 0, len(byNumber))
	for number := range byNumber {
		ordered = append(ordered, number)
	}
	sort.Ints(ordered)
	for _, number := range ordered {
		if _, err := tx.ExecContext(ctx, `
insert into game_assignments(game_id, basket, number, participant_id) values(?, 1, ?, ?)`,
			scope.GameID, number, byNumber[number]); err != nil {
			return err
		}
		assignments[[2]int{1, number}] = byNumber[number]
	}
	if err := resolveSeedSlots(ctx, tx, scope.GameID, gameType, assignments); err != nil {
		return err
	}
	return recordSeatedTx(ctx, tx, scope, seats, ordered, byNumber)
}

// recordSeatedTx rewrites the Game's entrant record — who holds a seat, by
// number — for a Game that keeps one: one that named its entrants, or whose
// Structure is sized by them. An EK seeded from a source keeps none, and its
// Clear throws the seeding away as it always has.
func recordSeatedTx(ctx context.Context, tx *sql.Tx, scope core.FestScope, seats map[int]bool, ordered []int, byNumber map[int]int64) error {
	var named int
	if err := tx.QueryRowContext(ctx, `select count(*) from game_participants where game_id = ?`, scope.GameID).Scan(&named); err != nil {
		return err
	}
	var dsl string
	if err := tx.QueryRowContext(ctx, `select coalesce(scheme_dsl, '') from games where id = ?`, scope.GameID).Scan(&dsl); err != nil {
		return err
	}
	if named == 0 && !EntrantSized(dsl) {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `delete from game_participants where game_id = ?`, scope.GameID); err != nil {
		return err
	}
	position := 0
	for _, number := range ordered {
		if len(seats) > 0 && !seats[number] {
			continue
		}
		position++
		if _, err := tx.ExecContext(ctx, `
insert into game_participants(game_id, participant_id, position, number) values(?, ?, ?, ?)`,
			scope.GameID, byNumber[number], position, number); err != nil {
			return err
		}
	}
	return nil
}

// LoadListView is the Game's Entrant list as the entrants tab shows it: each
// entrant with its seat, whether it is on the waiting list, whether it has
// results and whether it is a one-off, and where the Game's [init] says the
// list comes from.
func LoadListView(ctx context.Context, q store.Queryer, scope core.FestScope) (SeedImportView, error) {
	list, err := LoadListTx(ctx, q, scope)
	if err != nil {
		return SeedImportView{}, err
	}
	numbers, err := seatNumbersTx(ctx, q, scope.GameID)
	if err != nil {
		return SeedImportView{}, err
	}
	played, err := PlayedParticipants(ctx, q, scope.GameID, list.GameType)
	if err != nil {
		return SeedImportView{}, err
	}
	type seated struct {
		participant int64
		number      int
	}
	assigned, err := store.CollectRows(ctx, q, `
select participant_id, number from game_assignments where game_id = ? and basket = 1 and participant_id is not null`,
		[]any{scope.GameID}, func(rows *sql.Rows) (seated, error) {
			var s seated
			return s, rows.Scan(&s.participant, &s.number)
		})
	if err != nil {
		return SeedImportView{}, err
	}
	numberOf := map[int64]int{}
	for _, s := range assigned {
		numberOf[s.participant] = s.number
	}
	type info struct {
		name, city string
		oneOff     bool
	}
	infos, err := store.CollectRows(ctx, q, `
select id, name, city, game_id is not null from participants where fest_id = ?`, []any{scope.FestID},
		func(rows *sql.Rows) (struct {
			id int64
			info
		}, error) {
			var r struct {
				id int64
				info
			}
			return r, rows.Scan(&r.id, &r.name, &r.city, &r.oneOff)
		})
	if err != nil {
		return SeedImportView{}, err
	}
	byID := make(map[int64]info, len(infos))
	for _, r := range infos {
		byID[r.id] = r.info
	}
	seats := make(map[int]bool, len(numbers))
	for _, n := range numbers {
		seats[n] = true
	}

	view := SeedImportView{
		Source:       list.State.Source,
		SourceGameID: list.State.SourceGameID,
		Division:     list.State.Division,
		Edited:       list.State.Edited,
		Edits:        len(list.State.Edits),
		DrawSize:     len(numbers),
		Rows:         make([]SeedImportViewRow, 0, len(list.State.Rows)),
	}
	for _, row := range list.State.Rows {
		current := byID[row.TeamID]
		name, city := row.Name, row.City
		if current.name != "" {
			name, city = current.name, current.city
		}
		_, hasResults := played[row.TeamID]
		out := SeedImportViewRow{
			SourceRank: row.SourceRank,
			TeamID:     row.TeamID,
			Name:       name,
			City:       city,
			Declined:   row.Declined,
			Played:     hasResults,
			OneOff:     current.oneOff,
		}
		if number, ok := numberOf[row.TeamID]; ok {
			out.SeedNumber = number
			out.Waitlist = len(numbers) > 0 && !seats[number]
		}
		if !row.Declined {
			view.ActiveCount++
		}
		view.Rows = append(view.Rows, out)
	}
	return view, declareSeeding(ctx, q, scope, &view)
}

// fromGame is a Game's current table, optionally kept to one Division; sort
// re-ranks within the table when the scheme names rules.
type fromGame struct {
	code, division string
	sort           []store.SchemeSortRule
}

// FromGame is a Game's current table as the seeding, kept to one Division
// when one is given.
func FromGame(code, division string) SeedSource {
	return fromGame{code: strings.TrimSpace(code), division: strings.TrimSpace(division)}
}

func (f fromGame) resolve(ctx context.Context, tx *sql.Tx, scope core.FestScope) (seeding, error) {
	rules := f.sort
	if rules == nil {
		// The scheme's own sorting applies when the host picks the Game it declares.
		if declared, err := loadSchemeSeeding(ctx, tx, scope); err == nil && declared.Source == f.code {
			rules = declared.Sort
		}
	}
	gameID, candidates, err := standingsCandidates(ctx, tx, scope.FestID, f.code, rules)
	if err == nil && f.division != "" {
		candidates, err = inDivision(ctx, tx, scope.FestID, f.division, candidates)
	}
	return seeding{source: f.code, label: f.code, sourceGameID: gameID, division: f.division, candidates: candidates}, err
}

// FromRandom is a lot over the fest's numbered teams, the same every time it
// is drawn for this Game.
func FromRandom() SeedSource { return fromRandom{} }

type fromRandom struct{}

func (fromRandom) resolve(ctx context.Context, tx *sql.Tx, scope core.FestScope) (seeding, error) {
	candidates, err := randomSeedCandidates(ctx, tx, scope)
	return seeding{source: "random", label: dopestrings.Default.Imports.SeedSource.Random(), candidates: candidates}, err
}

// FromDeclaredPlayers is the by-players seeding the Game's [init] declares.
func FromDeclaredPlayers() SeedSource { return fromDeclaredPlayers{} }

type fromDeclaredPlayers struct{}

func (fromDeclaredPlayers) resolve(ctx context.Context, tx *sql.Tx, scope core.FestScope) (seeding, error) {
	declared, err := loadSchemeSeeding(ctx, tx, scope)
	if err != nil {
		return seeding{}, err
	}
	if declared.Source != "players" {
		return seeding{}, corei18n.User(dopestrings.Default.Imports.Seed.PlayersUndeclared())
	}
	return FromPlayers(declared.Players, declared.Sort).resolve(ctx, tx, scope)
}

// FromFest is the fest's whole roster in its own order: the numbered teams by
// number, kept to one Division when one is given, or, in a format that seats
// players, the fest's players in the order they registered.
func FromFest(division string) SeedSource { return fromFest{division: strings.TrimSpace(division)} }

type fromFest struct{ division string }

func (f fromFest) resolve(ctx context.Context, tx *sql.Tx, scope core.FestScope) (seeding, error) {
	gameType, _, err := loadSeedImportGame(ctx, tx, scope)
	if err != nil {
		return seeding{}, err
	}
	label := dopestrings.Default.Imports.SeedSource.Fest()
	if games.IsIndividual(gameType) {
		players, err := FestPlayerChoices(ctx, tx, scope.FestID)
		if err != nil {
			return seeding{}, err
		}
		candidates := make([]seedCandidate, 0, len(players))
		for _, player := range players {
			id, err := EnsurePlayerParticipantTx(ctx, tx, scope.FestID, player.ID)
			if err != nil {
				return seeding{}, err
			}
			candidates = append(candidates, seedCandidate{SourceRank: len(candidates) + 1, Name: player.Name, ParticipantID: id})
		}
		return seeding{source: "fest", label: label, candidates: candidates}, nil
	}
	roster, err := loadSeedRosterTeams(ctx, tx, scope.FestID)
	if err != nil {
		return seeding{}, err
	}
	sort.SliceStable(roster, func(i, j int) bool { return roster[i].Number < roster[j].Number })
	var candidates []seedCandidate
	for _, team := range roster {
		if team.Number > 0 {
			candidates = append(candidates, seedCandidate{SourceRank: len(candidates) + 1, Name: team.Name, Number: int(team.Number)})
		}
	}
	if len(candidates) == 0 {
		return seeding{}, corei18n.User(dopestrings.Default.Imports.Seed.NoNumberedTeams())
	}
	if f.division != "" {
		if candidates, err = inDivision(ctx, tx, scope.FestID, f.division, candidates); err != nil {
			return seeding{}, err
		}
	}
	return seeding{source: "fest", label: label, division: f.division, candidates: candidates}, nil
}

// FromTroikas is the fest's troikas by name, kept to one Division when one is
// given: what a Troika Game that takes a division follows.
func FromTroikas(division string) SeedSource {
	return fromTroikas{division: strings.TrimSpace(division)}
}

type fromTroikas struct{ division string }

func (f fromTroikas) resolve(ctx context.Context, tx *sql.Tx, scope core.FestScope) (seeding, error) {
	troikas, err := TroikasIn(ctx, tx, scope.FestID, f.division, 0)
	if err != nil {
		return seeding{}, err
	}
	if len(troikas) == 0 {
		return seeding{}, corei18n.User(dopestrings.Default.Imports.Seed.NoTroikas())
	}
	candidates := make([]seedCandidate, len(troikas))
	for i, troika := range troikas {
		candidates[i] = seedCandidate{SourceRank: i + 1, Name: troika.Name, ParticipantID: troika.ID}
	}
	return seeding{source: "troikas", label: dopestrings.Default.Imports.SeedSource.Troikas(), division: f.division, candidates: candidates}, nil
}

// TroikasIn is the fest's troikas in a Division, all of them for "", leaving
// out the one excluded (a troika about to be deleted).
func TroikasIn(ctx context.Context, q store.Queryer, festID int64, division string, exclude int64) ([]rosterpkg.Assembled, error) {
	if strings.TrimSpace(division) != "" {
		return rosterpkg.AssembledInDivision(ctx, q, festID, division, exclude)
	}
	all, err := rosterpkg.LoadAssembled(ctx, q, festID)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(all, func(a rosterpkg.Assembled) bool { return a.ID == exclude }), nil
}

// FestPlayerChoice is one fest player a format that seats players may add.
type FestPlayerChoice struct {
	ID   int64
	Name string
}

// FestPlayerChoices is the fest's players in the order they registered.
func FestPlayerChoices(ctx context.Context, q store.Queryer, festID int64) ([]FestPlayerChoice, error) {
	return store.CollectRows(ctx, q, `
select id, trim(first_name || ' ' || last_name) from fest_players where fest_id = ? order by id`, []any{festID},
		func(rows *sql.Rows) (FestPlayerChoice, error) {
			var c FestPlayerChoice
			return c, rows.Scan(&c.ID, &c.Name)
		})
}

// EnsurePlayerParticipantTx is the Participant a fest player plays as in a
// format that seats players. It is numbered by the player's place in the
// registration order, the way a Game that seats the whole fest numbers them,
// so the two find the same Participant.
func EnsurePlayerParticipantTx(ctx context.Context, tx *sql.Tx, festID, festPlayerID int64) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `
select id from participants
where fest_id = ? and roster = 'player' and fest_player_id = ? and game_id is null
order by id limit 1`, festID, festPlayerID).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	var name string
	var number int64
	if err := tx.QueryRowContext(ctx, `
select trim(first_name || ' ' || last_name), (select count(*) from fest_players o where o.fest_id = p.fest_id and o.id <= p.id)
from fest_players p where p.id = ? and p.fest_id = ?`, festPlayerID, festID).Scan(&name, &number); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, corei18n.User(dopestrings.Default.Imports.Seed.TeamNotFound())
		}
		return 0, err
	}
	return EnsureSeedPlayerByNumber(ctx, tx, festID, number, name, festPlayerID)
}
