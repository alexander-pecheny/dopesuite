// Package entrants is a buzzer Game's entrants tab (CONTEXT.md, Entrant
// list): who the Game seats, where that list comes from, and the host's hand
// edits to it. EK, ES, Brain, Troika, Hamsa and individual SI share it. The list
// itself — how it is stored and how it fills the Structure's seats — is
// imports'; a Game whose Structure is built for its entrants is recompiled by
// gamebuild. This package decides what the host may do and says why not.
package entrants

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"

	"dope/dope/domain/core"
	"dope/dope/domain/gamebuild"
	"dope/dope/domain/games"
	"dope/dope/domain/imports"
	"dope/dope/domain/roster"
	"dope/dope/platform/util"
	"dope/dope/storage/store"
	dopestrings "dope/i18nstrings"

	corei18n "pecheny.me/dopecore/i18nstrings"
)

// What an entrant is in a format: a team, a troika or a player.
const (
	KindTeam   = "team"
	KindTroika = "troika"
	KindPlayer = "player"
)

// kindOf is what the format seats.
func kindOf(gameType string) string {
	switch {
	case gameType == games.Troika:
		return KindTroika
	case games.IsIndividual(gameType):
		return KindPlayer
	}
	return KindTeam
}

// Formats reports whether a Game type keeps an Entrant list: the buzzer
// formats, where a few entrants meet in each bout.
func Formats(gameType string) bool {
	switch gameType {
	case games.EK, games.ES, games.SI, games.Brain, games.Troika, games.Hamsa:
		return true
	}
	return false
}

// Source is where a list comes from: a Game's table (Game is its code), the
// fest's own roster, the fest's troikas, a lot, an uploaded sheet or the
// by-players seeding a scheme declares. Division keeps it to one division.
type Source struct {
	// Fresh takes the source's list alone, dropping the host's hand edits
	// that an import otherwise applies again (ADR-0025).
	Fresh    bool   `json:"fresh,omitempty"`
	Kind     string `json:"kind"`
	Game     string `json:"game,omitempty"`
	Division string `json:"division,omitempty"`
}

// The source kinds.
const (
	SourceGame    = "game"
	SourceFest    = "fest"
	SourceTroikas = "troikas"
	SourceRandom  = "random"
	SourceXLSX    = "xlsx"
	SourcePlayers = "players"
)

// SourceOption is one choice of the tab's source picker. Divided says the
// division filter applies to it.
type SourceOption struct {
	Source
	Label   string `json:"label"`
	Divided bool   `json:"divided,omitempty"`
}

// Candidate is one entrant the host may add: a fest team, a troika or a fest
// player, under a key the add request sends back.
type Candidate struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// View is the entrants tab: the list, the source picker with its preselected
// choice, the divisions to filter by, who can be added, and whether the Game's
// Structure still follows the list (Resizes: nothing entered in a Game built
// for its entrants).
type View struct {
	imports.SeedImportView
	Kind       string         `json:"kind"`
	Sources    []SourceOption `json:"sources"`
	Preselect  Source         `json:"preselect"`
	Divisions  []string       `json:"divisions"`
	Candidates []Candidate    `json:"candidates"`
	OneOffs    bool           `json:"oneOffs"`
	Entered    bool           `json:"entered"`
	Resizes    bool           `json:"resizes"`
	// MovesDropped counts the hand moves an import from another source did not
	// apply again: a place in one source's order means nothing in another's.
	MovesDropped int `json:"movesDropped,omitempty"`
	// Unranked names the entrants the last import seeded last because it had
	// nothing to rank them by: nobody in them is on the fest roster.
	Unranked []string `json:"unranked,omitempty"`
	// Kept is why the last write left the Structure as it was, when the scheme
	// turned the new number of entrants down.
	Kept string `json:"kept,omitempty"`
}

// Result is what a write answers: the tab afresh, the revision and document it
// recorded, and whether the Structure was rebuilt, which an open page must
// reload to draw.
type Result struct {
	View      View
	Revision  int64
	StateJSON []byte
	Rebuilt   bool
}

// Load reads the tab.
func Load(ctx context.Context, q store.Queryer, scope core.FestScope) (View, error) {
	list, err := imports.LoadListTx(ctx, q, scope)
	if err != nil {
		return View{}, err
	}
	base, err := imports.LoadListView(ctx, q, scope)
	if err != nil {
		return View{}, err
	}
	return describe(ctx, q, scope, list, base)
}

func describe(ctx context.Context, q store.Queryer, scope core.FestScope, list imports.List, base imports.SeedImportView) (View, error) {
	s := dopestrings.Default
	view := View{SeedImportView: base, Kind: kindOf(list.GameType)}
	view.Unranked, view.MovesDropped = list.State.Unranked, list.State.MovesDropped
	view.OneOffs = view.Kind != KindTroika
	var dsl string
	if err := q.QueryRowContext(ctx, `select coalesce(scheme_dsl, '') from games where id = ?`, scope.GameID).Scan(&dsl); err != nil {
		return View{}, err
	}
	entered, err := imports.GameEntered(ctx, q, scope.GameID, list.GameType)
	if err != nil {
		return View{}, err
	}
	view.Entered = entered
	view.Resizes = !entered && imports.EntrantSized(dsl)

	// The picker: what this format can be seeded from.
	type other struct {
		code, title, gameType string
	}
	others, err := store.CollectRows(ctx, q, `
select code, title, game_type from games where fest_id = ? and id != ? order by position, id`,
		[]any{scope.FestID, scope.GameID}, func(rows *sql.Rows) (other, error) {
			var o other
			return o, rows.Scan(&o.code, &o.title, &o.gameType)
		})
	if err != nil {
		return View{}, err
	}
	declared := view.Declared
	switch view.Kind {
	case KindTroika:
		view.Sources = append(view.Sources, SourceOption{Source: Source{Kind: SourceTroikas}, Label: s.Entrants.Source.Troikas(), Divided: true})
	case KindPlayer:
		view.Sources = append(view.Sources, SourceOption{Source: Source{Kind: SourceFest}, Label: s.Entrants.Source.FestPlayers()})
		for _, o := range others {
			if games.IsIndividual(o.gameType) {
				view.Sources = append(view.Sources, SourceOption{Source: Source{Kind: SourceGame, Game: o.code}, Label: s.Entrants.Source.Game(o.title)})
			}
		}
	default:
		view.Sources = append(view.Sources, SourceOption{Source: Source{Kind: SourceFest}, Label: s.Entrants.Source.FestTeams(), Divided: true})
		for _, o := range others {
			if !games.IsIndividual(o.gameType) && o.gameType != games.Troika {
				view.Sources = append(view.Sources, SourceOption{Source: Source{Kind: SourceGame, Game: o.code}, Label: s.Entrants.Source.Game(o.title), Divided: true})
			}
		}
		view.Sources = append(view.Sources,
			SourceOption{Source: Source{Kind: SourceRandom}, Label: s.Entrants.Source.Random()},
			SourceOption{Source: Source{Kind: SourceXLSX}, Label: s.Entrants.Source.Xlsx()})
	}
	if declared == SourcePlayers {
		view.Sources = append(view.Sources, SourceOption{Source: Source{Kind: SourcePlayers}, Label: s.Entrants.Source.Players()})
	}

	// Preselected: what the list was last imported from, else what the scheme
	// declares, else the format's own roster.
	view.Preselect = sourceOfStored(ctx, q, scope.FestID, list.State.Source, list.State.Division)
	if view.Preselect.Kind == "" {
		view.Preselect = sourceOfStored(ctx, q, scope.FestID, declared, "")
		if seeding, err := schemeDivision(ctx, q, scope.GameID); err == nil && view.Preselect.Kind != "" {
			view.Preselect.Division = seeding
		}
	}
	if view.Preselect.Kind == "" {
		if division, ok := gamebuild.EntrantDivision(dsl); ok && view.Kind == KindTroika {
			view.Preselect = Source{Kind: SourceTroikas, Division: division}
		} else {
			view.Preselect = view.Sources[0].Source
		}
	}

	if view.Divisions, err = festDivisions(ctx, q, scope.FestID); err != nil {
		return View{}, err
	}
	if view.Candidates, err = candidates(ctx, q, scope.FestID, view.Kind, list); err != nil {
		return View{}, err
	}
	return view, nil
}

// sourceOfStored reads a source as the list stores it: a keyword, the legacy
// "ksi" (the fest's first KSI), or a Game's code.
func sourceOfStored(ctx context.Context, q store.Queryer, festID int64, stored, division string) Source {
	switch stored {
	case "":
		return Source{}
	case SourceFest, SourceTroikas, SourceRandom, SourceXLSX, SourcePlayers:
		return Source{Kind: stored, Division: division}
	case "ksi":
		var code string
		if err := q.QueryRowContext(ctx, `
select code from games where fest_id = ? and game_type = 'ksi' order by position, id limit 1`, festID).Scan(&code); err != nil {
			return Source{}
		}
		return Source{Kind: SourceGame, Game: code}
	}
	return Source{Kind: SourceGame, Game: stored, Division: division}
}

// schemeDivision is the division the Game's [init] keeps its seeding to.
func schemeDivision(ctx context.Context, q store.Queryer, gameID int64) (string, error) {
	var division string
	err := q.QueryRowContext(ctx, `
select coalesce(json_extract(scheme_json, '$.seeding.division'), '') from games where id = ?`, gameID).Scan(&division)
	return division, err
}

// festDivisions is every Flag the fest's teams carry, in the order first seen.
func festDivisions(ctx context.Context, q store.Queryer, festID int64) ([]string, error) {
	flags, err := store.CollectRows(ctx, q, `
select f.short from fest_team_flags f join fest_teams t on t.id = f.team_id
where t.fest_id = ? and t.deleted = 0 order by t.position, t.id, f.position`, []any{festID},
		func(rows *sql.Rows) (string, error) {
			var short string
			return short, rows.Scan(&short)
		})
	if err != nil {
		return nil, err
	}
	var out []string
	for _, flag := range flags {
		if flag = strings.TrimSpace(flag); flag != "" && !slices.Contains(out, flag) {
			out = append(out, flag)
		}
	}
	return out, nil
}

// candidates is who the host may add to the list: the fest's teams, troikas or
// players that are not in it yet.
func candidates(ctx context.Context, q store.Queryer, festID int64, kind string, list imports.List) ([]Candidate, error) {
	in := map[int64]bool{}
	for _, row := range list.State.Rows {
		in[row.TeamID] = true
	}
	switch kind {
	case KindTroika:
		troikas, err := roster.LoadAssembled(ctx, q, festID)
		if err != nil {
			return nil, err
		}
		var out []Candidate
		for _, t := range troikas {
			if !in[t.ID] {
				out = append(out, Candidate{Key: keyOf(KindTroika, t.ID), Label: t.Name})
			}
		}
		return out, nil
	case KindPlayer:
		players, err := imports.FestPlayerChoices(ctx, q, festID)
		if err != nil {
			return nil, err
		}
		taken, err := playersIn(ctx, q, in)
		if err != nil {
			return nil, err
		}
		var out []Candidate
		for _, p := range players {
			if !taken[p.ID] {
				out = append(out, Candidate{Key: keyOf(KindPlayer, p.ID), Label: p.Name})
			}
		}
		return out, nil
	}
	type team struct {
		id, number int64
		name, city string
	}
	teams, err := store.CollectRows(ctx, q, `
select id, coalesce(number, 0), name, city from fest_teams
where fest_id = ? and deleted = 0 order by coalesce(nullif(number, 0), 1 << 30), name, id`, []any{festID},
		func(rows *sql.Rows) (team, error) {
			var t team
			return t, rows.Scan(&t.id, &t.number, &t.name, &t.city)
		})
	if err != nil {
		return nil, err
	}
	numbers, names, err := teamsIn(ctx, q, in)
	if err != nil {
		return nil, err
	}
	var out []Candidate
	for _, t := range teams {
		if (t.number > 0 && numbers[t.number]) || (t.number <= 0 && names[t.name]) {
			continue
		}
		label := t.name
		if t.city != "" {
			label += " (" + t.city + ")"
		}
		out = append(out, Candidate{Key: keyOf(KindTeam, t.id), Label: label})
	}
	return out, nil
}

// teamsIn is the fest numbers and names of the team Participants in the list.
func teamsIn(ctx context.Context, q store.Queryer, in map[int64]bool) (map[int64]bool, map[string]bool, error) {
	numbers, names := map[int64]bool{}, map[string]bool{}
	for id := range in {
		var number int64
		var name string
		if err := q.QueryRowContext(ctx, `select coalesce(number, 0), name from participants where id = ?`, id).Scan(&number, &name); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return nil, nil, err
		}
		if number > 0 {
			numbers[number] = true
		} else {
			names[name] = true
		}
	}
	return numbers, names, nil
}

// playersIn is the fest players the list's player Participants are.
func playersIn(ctx context.Context, q store.Queryer, in map[int64]bool) (map[int64]bool, error) {
	out := map[int64]bool{}
	for id := range in {
		var festPlayer sql.NullInt64
		if err := q.QueryRowContext(ctx, `select fest_player_id from participants where id = ?`, id).Scan(&festPlayer); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return nil, err
		}
		if festPlayer.Valid {
			out[festPlayer.Int64] = true
		}
	}
	return out, nil
}

func keyOf(kind string, id int64) string { return kind + ":" + strconv.FormatInt(id, 10) }

// ---- the writes ----

// Host is the write side the package needs: the engine's write transaction.
type Host interface {
	WithWriteTx(ctx context.Context, festID int64, label string, fn func(ctx context.Context, tx *sql.Tx) error) error
}

// edit runs one write to the list: load it, let change say what it becomes,
// save and seat it, and read the tab back. after runs once the list is saved,
// inside the same transaction.
func edit(h Host, reqCtx context.Context, scope core.FestScope, event string,
	change func(ctx context.Context, tx *sql.Tx, list imports.List) (imports.List, error),
	after func(ctx context.Context, tx *sql.Tx) error) (Result, error) {
	var result Result
	err := h.WithWriteTx(reqCtx, scope.FestID, event, func(ctx context.Context, tx *sql.Tx) error {
		list, err := imports.LoadListTx(ctx, tx, scope)
		if err != nil {
			return err
		}
		next, err := change(ctx, tx, list)
		if err != nil {
			return err
		}
		applied, err := gamebuild.ApplyListTx(ctx, tx, scope, list, next, "entrants:"+event)
		if err != nil {
			return err
		}
		base := applied.View
		if after != nil {
			if err := after(ctx, tx); err != nil {
				return err
			}
			if base, err = imports.LoadListView(ctx, tx, scope); err != nil {
				return err
			}
		}
		view, err := describe(ctx, tx, scope, next, base)
		if err != nil {
			return err
		}
		view.Kept = applied.Kept
		result = Result{View: view, Revision: applied.Revision, StateJSON: applied.StateJSON, Rebuilt: applied.Rebuilt}
		return nil
	})
	return result, err
}

// Import replaces the list with what a source makes of it now, and applies
// the host's hand edits to it again (ADR-0025) unless source.Fresh asks for
// the source's list alone. Declines of the list there was survive. file is the
// uploaded sheet of an xlsx source.
func Import(h Host, ctx context.Context, scope core.FestScope, source Source, file io.Reader) (Result, error) {
	src, err := seedSource(source, file)
	if err != nil {
		return Result{}, err
	}
	return importList(h, ctx, scope, src, source.Fresh)
}

// ImportLegacy runs a seed source as the seed tab's old routes name it: the
// fest's first KSI, or what the scheme's [init] declares. The hand edits are
// applied again, as by Import.
func ImportLegacy(h Host, ctx context.Context, scope core.FestScope, src imports.SeedSource) (Result, error) {
	return importList(h, ctx, scope, src, false)
}

// importList replaces the list with the source's, then replays the host's
// edits unless fresh. A one-off the new list leaves out goes, unless a bout
// has seated it.
func importList(h Host, ctx context.Context, scope core.FestScope, src imports.SeedSource, fresh bool) (Result, error) {
	var kept []int64
	dropped := 0
	var unranked []string
	result, err := edit(h, ctx, scope, "import", func(ctx context.Context, tx *sql.Tx, list imports.List) (imports.List, error) {
		next, _, err := imports.ResolveListTx(ctx, tx, scope, list, src)
		if err != nil {
			return imports.List{}, err
		}
		unranked = next.Unranked
		edits := list.State.Edits
		// A move keeps a place in the order the source gave (ADR-0025). Another
		// source orders by something else — a troika game's troikas by application,
		// then by the players' places — and replaying «third» there would undo
		// the new seeding without a word. So only who plays carries over.
		if !fresh && len(edits) > 0 && sourceChanged(list.State, next.State) {
			edits, dropped = withoutMoves(edits)
		}
		if !fresh && len(edits) > 0 {
			state := next.State
			state.Rows = imports.Replay(state.Rows, edits)
			// An entrant the host added keeps its decline, which the source,
			// not knowing it, could not carry.
			for i, row := range state.Rows {
				if j := list.Index(row.TeamID); j >= 0 && list.State.Rows[j].Declined {
					state.Rows[i].Declined = true
				}
			}
			state.Edits, state.Edited = edits, true
			next = next.With(state)
		}
		// What the import has to tell the host stays with the list, so the
		// tab still says it when it is read back: a fest broadcast re-reads it
		// the moment the import lands, and a rebuilt Game reloads the page.
		next.State.Unranked, next.State.MovesDropped = unranked, dropped
		for _, row := range next.State.Rows {
			kept = append(kept, row.TeamID)
		}
		return next, nil
	}, func(ctx context.Context, tx *sql.Tx) error {
		return dropUnlistedOneOffsTx(ctx, tx, scope, kept)
	})
	result.View.Unranked = unranked
	result.View.MovesDropped = dropped
	return result, err
}

// sourceChanged reports whether an import reads another source than the one
// the list came from: another kind, another Game or another division. A list
// no source ever made (the host's own, or the Structure's seats) has none.
func sourceChanged(was, now imports.ListState) bool {
	if was.Source == "" {
		return false
	}
	return was.Source != now.Source || was.SourceGameID != now.SourceGameID || was.Division != now.Division
}

// withoutMoves is the edits less the moves, and how many moves there were.
func withoutMoves(edits []imports.ListEdit) ([]imports.ListEdit, int) {
	out := make([]imports.ListEdit, 0, len(edits))
	for _, e := range edits {
		if e.Op != imports.ListEditMove {
			out = append(out, e)
		}
	}
	return out, len(edits) - len(out)
}

// dropUnlistedOneOffsTx deletes the Game's one-off entrants that are neither in
// its list nor sitting in any of its bouts.
func dropUnlistedOneOffsTx(ctx context.Context, tx *sql.Tx, scope core.FestScope, listed []int64) error {
	oneOffs, err := store.CollectRows(ctx, tx, `
select p.id from participants p
where p.game_id = ?
  and not exists (select 1 from match_slots ms where ms.participant_id = p.id)
  and not exists (select 1 from game_assignments ga where ga.participant_id = p.id)`, []any{scope.GameID},
		func(rows *sql.Rows) (int64, error) {
			var id int64
			return id, rows.Scan(&id)
		})
	if err != nil {
		return err
	}
	for _, id := range oneOffs {
		if slices.Contains(listed, id) {
			continue
		}
		if _, err := tx.ExecContext(ctx, `delete from participants where id = ? and game_id = ?`, id, scope.GameID); err != nil {
			return err
		}
	}
	return nil
}

func seedSource(source Source, file io.Reader) (imports.SeedSource, error) {
	switch source.Kind {
	case SourceGame:
		if strings.TrimSpace(source.Game) == "" {
			return nil, corei18n.User(dopestrings.Default.Entrants.Error.SourceMissing())
		}
		return imports.FromGame(source.Game, source.Division), nil
	case SourceFest:
		return imports.FromFest(source.Division), nil
	case SourceTroikas:
		return imports.FromTroikas(source.Division), nil
	case SourceRandom:
		return imports.FromRandom(), nil
	case SourcePlayers:
		return imports.FromDeclaredPlayers(), nil
	case SourceXLSX:
		if file == nil {
			return nil, corei18n.User(dopestrings.Default.Server.SeedImport.FileMissing())
		}
		return imports.FromXLSX(file), nil
	}
	return nil, corei18n.User(dopestrings.Default.Entrants.Error.SourceMissing())
}

// AddRequest names who to add: a candidate by its key, or a one-off entrant
// by the name the host typed.
type AddRequest struct {
	Key  string `json:"key,omitempty"`
	Name string `json:"name,omitempty"`
}

// Add puts an entrant at the end of the list. It takes a free seat if there is
// one, or waits on the waiting list for a decline to free one.
func Add(h Host, ctx context.Context, scope core.FestScope, req AddRequest) (Result, error) {
	return edit(h, ctx, scope, "add", func(ctx context.Context, tx *sql.Tx, list imports.List) (imports.List, error) {
		id, name, city, err := participantFor(ctx, tx, scope, list, req)
		if err != nil {
			return imports.List{}, err
		}
		if list.Index(id) >= 0 {
			return imports.List{}, corei18n.User(dopestrings.Default.Entrants.Error.AlreadyIn(name))
		}
		rows := append(slices.Clone(list.State.Rows), imports.ListRow{TeamID: id, Name: name, City: city})
		return list.Edit(rows, imports.ListEdit{Op: imports.ListEditAdd, TeamID: id, Name: name, City: city}), nil
	}, nil)
}

// participantFor is the Participant an add request means, made when it is new.
func participantFor(ctx context.Context, tx *sql.Tx, scope core.FestScope, list imports.List, req AddRequest) (int64, string, string, error) {
	s := dopestrings.Default
	kind := kindOf(list.GameType)
	if name := strings.TrimSpace(req.Name); name != "" {
		if kind == KindTroika {
			return 0, "", "", corei18n.User(s.Entrants.Error.OneOffTroika())
		}
		if err := nameFree(ctx, tx, list, name, 0); err != nil {
			return 0, "", "", err
		}
		rosterKind := "team"
		if kind == KindPlayer {
			rosterKind = "player"
		}
		id, err := store.InsertReturningID(ctx, tx, `
insert into participants(fest_id, roster, name, city, game_id) values(?, ?, ?, '', ?)`,
			scope.FestID, rosterKind, name, scope.GameID)
		return id, name, "", err
	}
	want, raw, ok := strings.Cut(req.Key, ":")
	id, err := strconv.ParseInt(raw, 10, 64)
	if !ok || err != nil || id <= 0 || want != kind {
		return 0, "", "", corei18n.User(s.Entrants.Error.PickSomebody())
	}
	switch kind {
	case KindTroika:
		var name string
		err := tx.QueryRowContext(ctx, `
select name from participants where id = ? and fest_id = ? and assembled = 1`, id, scope.FestID).Scan(&name)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, "", "", corei18n.User(s.Entrants.Error.PickSomebody())
		}
		return id, name, "", err
	case KindPlayer:
		participant, err := imports.EnsurePlayerParticipantTx(ctx, tx, scope.FestID, id)
		if err != nil {
			return 0, "", "", err
		}
		var name string
		err = tx.QueryRowContext(ctx, `select name from participants where id = ?`, participant).Scan(&name)
		return participant, name, "", err
	}
	var number int64
	var name, city string
	err = tx.QueryRowContext(ctx, `
select coalesce(number, 0), name, city from fest_teams where id = ? and fest_id = ? and deleted = 0`, id, scope.FestID).Scan(&number, &name, &city)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", "", corei18n.User(s.Entrants.Error.PickSomebody())
	}
	if err != nil {
		return 0, "", "", err
	}
	players, err := festTeamPlayers(ctx, tx, id)
	if err != nil {
		return 0, "", "", err
	}
	participant, city, err := imports.EnsureSeedTeamByNumber(ctx, tx, scope.FestID, number, name, city, players)
	return participant, name, city, err
}

func festTeamPlayers(ctx context.Context, q store.Queryer, festTeamID int64) ([]roster.SeedRosterPlayer, error) {
	return store.CollectRows(ctx, q, `
select p.first_name, p.last_name from fest_team_players ftp join fest_players p on p.id = ftp.player_id
where ftp.team_id = ? order by ftp.roster_order, p.id`, []any{festTeamID}, func(rows *sql.Rows) (roster.SeedRosterPlayer, error) {
		var player roster.SeedRosterPlayer
		return player, rows.Scan(&player.FirstName, &player.LastName)
	})
}

// nameFree refuses a one-off name another entrant of the list already goes by.
func nameFree(ctx context.Context, q store.Queryer, list imports.List, name string, except int64) error {
	key := util.AlphaKey(name)
	for _, row := range list.State.Rows {
		if row.TeamID == except {
			continue
		}
		current := row.Name
		_ = q.QueryRowContext(ctx, `select name from participants where id = ?`, row.TeamID).Scan(&current)
		if util.AlphaKey(current) == key {
			return corei18n.User(dopestrings.Default.Entrants.Error.NameTaken(name))
		}
	}
	return nil
}

// played refuses a change to an entrant that has results in this Game.
func played(ctx context.Context, q store.Queryer, scope core.FestScope, gameType string, participantID int64, name string) error {
	sat, err := imports.PlayedParticipants(ctx, q, scope.GameID, gameType)
	if err != nil {
		return err
	}
	if _, ok := sat[participantID]; ok {
		return corei18n.User(dopestrings.Default.Entrants.Error.Played(name))
	}
	return nil
}

func rowOf(list imports.List, participantID int64) (int, error) {
	i := list.Index(participantID)
	if i < 0 {
		return 0, corei18n.User(dopestrings.Default.Imports.Seed.TeamNotFound())
	}
	return i, nil
}

// Remove takes an entrant out of the list — refused once it has results here.
// A one-off entrant goes altogether, since no other Game knows it.
func Remove(h Host, ctx context.Context, scope core.FestScope, participantID int64) (Result, error) {
	var oneOff bool
	return edit(h, ctx, scope, "remove", func(ctx context.Context, tx *sql.Tx, list imports.List) (imports.List, error) {
		i, err := rowOf(list, participantID)
		if err != nil {
			return imports.List{}, err
		}
		if err := played(ctx, tx, scope, list.GameType, participantID, list.State.Rows[i].Name); err != nil {
			return imports.List{}, err
		}
		if err := tx.QueryRowContext(ctx, `
select game_id is not null from participants where id = ?`, participantID).Scan(&oneOff); err != nil {
			return imports.List{}, err
		}
		return list.Edit(slices.Delete(slices.Clone(list.State.Rows), i, i+1), imports.ListEdit{Op: imports.ListEditRemove, TeamID: participantID}), nil
	}, func(ctx context.Context, tx *sql.Tx) error {
		if !oneOff {
			return nil
		}
		_, err := tx.ExecContext(ctx, `delete from participants where id = ? and game_id = ?`, participantID, scope.GameID)
		return err
	})
}

// Move puts an entrant at a place in the list (1 is the top). One that sits
// in a bout that has begun keeps its seat, so it cannot be moved.
func Move(h Host, ctx context.Context, scope core.FestScope, participantID int64, position int) (Result, error) {
	return edit(h, ctx, scope, "move", func(ctx context.Context, tx *sql.Tx, list imports.List) (imports.List, error) {
		i, err := rowOf(list, participantID)
		if err != nil {
			return imports.List{}, err
		}
		if err := played(ctx, tx, scope, list.GameType, participantID, list.State.Rows[i].Name); err != nil {
			return imports.List{}, err
		}
		rows := slices.Clone(list.State.Rows)
		row := rows[i]
		rows = slices.Delete(rows, i, i+1)
		to := min(max(position, 1), len(rows)+1) - 1
		rows = slices.Insert(rows, to, row)
		return list.Edit(rows, imports.ListEdit{Op: imports.ListEditMove, TeamID: participantID, Position: to + 1}), nil
	}, nil)
}

// Replace puts another entrant in an entrant's place: it takes that place in
// the list, and with it the seed number and every seat nobody has started,
// so nobody else moves. That is what a host wants when a team pulls out and
// another plays instead, where a decline would move everybody below up a
// seat. One already in the list swaps places with the entrant it replaces,
// so one on the waiting list simply changes places with it; one from outside
// takes its row, and the entrant replaced leaves the list. Refused when
// either of them already sits in a bout that has begun.
func Replace(h Host, ctx context.Context, scope core.FestScope, participantID int64, req AddRequest) (Result, error) {
	var oneOff bool
	return edit(h, ctx, scope, "replace", func(ctx context.Context, tx *sql.Tx, list imports.List) (imports.List, error) {
		i, err := rowOf(list, participantID)
		if err != nil {
			return imports.List{}, err
		}
		out := list.State.Rows[i]
		if err := played(ctx, tx, scope, list.GameType, participantID, out.Name); err != nil {
			return imports.List{}, err
		}
		id, name, city, err := replacementFor(ctx, tx, scope, list, req)
		if err != nil {
			return imports.List{}, err
		}
		if id == participantID {
			return imports.List{}, corei18n.User(dopestrings.Default.Entrants.Error.ReplaceSelf())
		}
		rows := slices.Clone(list.State.Rows)
		if j := list.Index(id); j >= 0 {
			if err := played(ctx, tx, scope, list.GameType, id, rows[j].Name); err != nil {
				return imports.List{}, err
			}
			// The two swap places. Each keeps its own decline, except that the
			// one coming in plays: that is why it was picked.
			rows[i], rows[j] = rows[j], rows[i]
			rows[i].Declined = false
			return list.Edit(rows,
				imports.ListEdit{Op: imports.ListEditMove, TeamID: id, Position: i + 1},
				imports.ListEdit{Op: imports.ListEditMove, TeamID: participantID, Position: j + 1}), nil
		}
		if err := tx.QueryRowContext(ctx, `
select game_id is not null from participants where id = ?`, participantID).Scan(&oneOff); err != nil {
			return imports.List{}, err
		}
		rows[i] = imports.ListRow{TeamID: id, Name: name, City: city}
		return list.Edit(rows,
			imports.ListEdit{Op: imports.ListEditAdd, TeamID: id, Name: name, City: city},
			imports.ListEdit{Op: imports.ListEditMove, TeamID: id, Position: i + 1},
			imports.ListEdit{Op: imports.ListEditRemove, TeamID: participantID}), nil
	}, func(ctx context.Context, tx *sql.Tx) error {
		// A one-off replaced from outside the list goes, as a removed one does.
		if !oneOff {
			return nil
		}
		_, err := tx.ExecContext(ctx, `
delete from participants where id = ? and game_id = ?
  and not exists (select 1 from match_slots where participant_id = ?)`, participantID, scope.GameID, participantID)
		return err
	})
}

// replacementFor is the entrant a replace request names: one of the list by
// its key (entrant:<id>), or anybody an add could bring in.
func replacementFor(ctx context.Context, tx *sql.Tx, scope core.FestScope, list imports.List, req AddRequest) (int64, string, string, error) {
	if raw, ok := strings.CutPrefix(req.Key, keyEntrant+":"); ok {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || list.Index(id) < 0 {
			return 0, "", "", corei18n.User(dopestrings.Default.Entrants.Error.PickSomebody())
		}
		row := list.State.Rows[list.Index(id)]
		return id, row.Name, row.City, nil
	}
	return participantFor(ctx, tx, scope, list, req)
}

// keyEntrant is the key kind of an entrant already in the list.
const keyEntrant = "entrant"

// Rename changes a one-off entrant's name — a fest team is renamed on the
// fest's roster page, a troika on the troikas page — and is refused once it
// has results here.
func Rename(h Host, ctx context.Context, scope core.FestScope, participantID int64, name string) (Result, error) {
	name = strings.TrimSpace(name)
	return edit(h, ctx, scope, "rename", func(ctx context.Context, tx *sql.Tx, list imports.List) (imports.List, error) {
		s := dopestrings.Default
		i, err := rowOf(list, participantID)
		if err != nil {
			return imports.List{}, err
		}
		if name == "" {
			return imports.List{}, corei18n.User(s.Entrants.Error.NameMissing())
		}
		var oneOff bool
		if err := tx.QueryRowContext(ctx, `
select game_id is not null from participants where id = ?`, participantID).Scan(&oneOff); err != nil {
			return imports.List{}, err
		}
		if !oneOff {
			return imports.List{}, corei18n.User(s.Entrants.Error.RenameFest())
		}
		if err := played(ctx, tx, scope, list.GameType, participantID, list.State.Rows[i].Name); err != nil {
			return imports.List{}, err
		}
		if err := nameFree(ctx, tx, list, name, participantID); err != nil {
			return imports.List{}, err
		}
		if _, err := tx.ExecContext(ctx, `update participants set name = ? where id = ? and game_id = ?`, name, participantID, scope.GameID); err != nil {
			return imports.List{}, err
		}
		rows := slices.Clone(list.State.Rows)
		rows[i].Name = name
		// The one-off itself is renamed, so there is nothing to replay: the
		// list only shows the new name.
		state := list.State
		state.Rows = rows
		return list.With(state), nil
	}, nil)
}

// Decline marks an entrant as having refused to play, or takes that back. The
// next entrant moves up into its seat in every bout nobody has started.
func Decline(h Host, ctx context.Context, scope core.FestScope, participantID int64, declined bool) (Result, error) {
	return edit(h, ctx, scope, "decline", func(ctx context.Context, tx *sql.Tx, list imports.List) (imports.List, error) {
		return list.Decline(participantID, declined)
	}, nil)
}
