package entrants

import (
	"context"
	"database/sql"
	"slices"
	"strings"

	"dope/dope/domain/core"
	"dope/dope/domain/gamebuild"
	"dope/dope/domain/games"
	"dope/dope/domain/imports"
	"dope/dope/domain/roster"
	"dope/dope/storage/store"
	dopestrings "dope/i18nstrings"
)

// This file is where every write that changes who a Game seats ends up. A
// host's edit on the entrants tab, an import into it, a troika added, moved to
// another division or deleted, a team's Flags changed by hand or by a rating
// import: each saves the Entrant list through applyListTx, in the transaction
// of the write that caused it.

// applied is what saving an Entrant list did: the tab as it reads now, the
// revision and game document it recorded, whether the Structure was rebuilt
// for the list, and, when a rebuild was tried and the scheme turned it down,
// why (kept). The list then fills the Structure's seats as they are.
type applied struct {
	view      imports.SeedImportView
	revision  int64
	stateJSON []byte
	rebuilt   bool
	kept      string
}

// applyListTx saves a Game's Entrant list (CONTEXT.md) and seats it. An
// entrant-sized Structure follows the list first (gamebuild.FollowListTx);
// then the list's active entrants take the seed numbers, and every seat in a
// bout nobody has started is dealt again.
func applyListTx(ctx context.Context, tx *sql.Tx, scope core.FestScope, current, next imports.List, event string) (applied, error) {
	var out applied
	var dsl, schemeJSON string
	if err := tx.QueryRowContext(ctx, `
select coalesce(scheme_dsl, ''), coalesce(scheme_json, '{}') from games where id = ? and fest_id = ?`, scope.GameID, scope.FestID).Scan(&dsl, &schemeJSON); err != nil {
		return out, err
	}
	declared, err := imports.DeclaredOfJSON(schemeJSON, dsl)
	if err != nil {
		return out, err
	}
	if declared.EntrantSized() {
		var err error
		if out.rebuilt, out.kept, err = gamebuild.FollowListTx(ctx, tx, scope, dsl, current.GameType, next.Active()); err != nil {
			return out, err
		}
	}
	out.view, out.revision, out.stateJSON, err = imports.SaveListTx(ctx, tx, scope, current, next, event)
	return out, err
}

// A Troika Game whose scheme declares a division in [init] without a seed
// seats the fest's troikas in that division (imports.Declared.EntrantDivision). Its
// Entrant list follows them: a troika added, moved to another division or
// deleted changes the list, and so the Game's written qualifier, until the
// Game has anything entered. After that the list is the host's to edit on the
// Game's entrants tab, and so it is once the host has edited it there or
// imported it from another source.

// DivisionGame is one such Game as the troikas page reports it: which division it
// takes, how many troikas are in it, whether its entrants are those troikas,
// and why not.
type DivisionGame struct {
	GameID   int64
	Title    string
	Division string
	// Troikas is who the division holds now, by name.
	Troikas []int64
	// Current says the Game's list is exactly them.
	Current bool
	// Frozen says the Game has something entered, so its list no longer follows.
	Frozen bool
	// Manual says the host has edited the list on the Game's entrants tab or
	// imported it from somewhere else, so it no longer follows.
	Manual bool
	// Problem is why a re-seat was refused, for the host to read.
	Problem string
}

// troikaFormats is the game_type filter for the formats that seat troikas
// (games.Definition.Troikas), with its arguments after the fest's id.
func troikaFormats(festID int64) (string, []any) {
	codes := games.Codes(func(d games.Definition) bool { return d.Troikas })
	args := []any{festID}
	marks := make([]string, len(codes))
	for i, code := range codes {
		marks[i] = "?"
		args = append(args, code)
	}
	return "game_type in (" + strings.Join(marks, ", ") + ")", args
}

// TroikaGameIDs lists the fest's Games that seat troikas.
func TroikaGameIDs(ctx context.Context, q store.Queryer, festID int64) ([]int64, error) {
	filter, args := troikaFormats(festID)
	return store.CollectRows(ctx, q, `
select id from games where fest_id = ? and `+filter+` order by position, id`, args,
		func(rows *sql.Rows) (int64, error) {
			var id int64
			return id, rows.Scan(&id)
		})
}

// LoadDivisionGames reports the fest's Troika Games that take their entrants
// from a division, in the fest's order.
func LoadDivisionGames(ctx context.Context, q store.Queryer, festID int64) ([]DivisionGame, error) {
	found, _, err := divisionGames(ctx, q, festID, 0)
	return found, err
}

func divisionGames(ctx context.Context, q store.Queryer, festID, exclude int64) ([]DivisionGame, []imports.List, error) {
	type row struct {
		id                               int64
		title, dsl, schemeJSON, gameType string
	}
	filter, args := troikaFormats(festID)
	rows, err := store.CollectRows(ctx, q, `
select id, title, scheme_dsl, coalesce(scheme_json, '{}'), game_type from games
where fest_id = ? and `+filter+` and scheme_dsl is not null
order by position, id`, args, func(rows *sql.Rows) (row, error) {
		var r row
		return r, rows.Scan(&r.id, &r.title, &r.dsl, &r.schemeJSON, &r.gameType)
	})
	if err != nil {
		return nil, nil, err
	}
	var out []DivisionGame
	var lists []imports.List
	for _, r := range rows {
		declared, err := imports.DeclaredOfJSON(r.schemeJSON, r.dsl)
		if err != nil {
			return nil, nil, err
		}
		division, ok := declared.EntrantDivision()
		if !ok {
			continue
		}
		game := DivisionGame{GameID: r.id, Title: r.title, Division: division}
		troikas, err := imports.DefaultEntrants(ctx, q, festID, KindTroika, division, exclude)
		if err != nil {
			return nil, nil, err
		}
		for _, t := range troikas {
			game.Troikas = append(game.Troikas, t.ParticipantID)
		}
		list, err := imports.LoadListTx(ctx, q, core.FestScope{FestID: festID, GameID: r.id})
		if err != nil {
			return nil, nil, err
		}
		game.Current = slices.Equal(list.Active(), game.Troikas)
		game.Manual = list.State.Edited || (list.State.Source != "" && list.State.Source != SourceTroikas)
		if game.Frozen, err = imports.GameEntered(ctx, q, r.id, r.gameType); err != nil {
			return nil, nil, err
		}
		out = append(out, game)
		lists = append(lists, list)
	}
	return out, lists, nil
}

// FollowDivisionsTx brings the Entrant list of every Troika Game of the fest
// that takes a division, and still follows it, to that division's troikas — as
// long as nothing is entered in the Game. Every write that may move a troika
// between divisions calls it in its own transaction: a troika's own edit, and
// a change to its head team's Flags, by hand or by an import. exclude leaves
// one troika out, the one a delete is about to remove. A troika that declined
// keeps its mark. It returns the Games it looked at; a re-seat the scheme
// refuses (too few troikas for it) is reported on the Game, not returned, so
// the write that caused it still saves.
func FollowDivisionsTx(ctx context.Context, tx *sql.Tx, festID, exclude int64) ([]DivisionGame, error) {
	found, lists, err := divisionGames(ctx, tx, festID, exclude)
	if err != nil {
		return nil, err
	}
	s := dopestrings.Default
	for i := range found {
		game, list := &found[i], lists[i]
		if game.Current || game.Manual {
			continue
		}
		// A Game with results takes only troikas added after the ones it has:
		// its written qualifier grows a row for them while nothing after it is
		// played (gamebuild.FollowListTx). A troika that left the division
		// stays where its results are.
		if game.Frozen {
			active := list.Active()
			if len(game.Troikas) <= len(active) || !slices.Equal(game.Troikas[:len(active)], active) {
				continue
			}
		}
		if len(game.Troikas) == 0 {
			game.Problem = s.Gamebuild.Division.NoTroikas(game.Division)
			continue
		}
		declined := map[int64]bool{}
		for _, row := range list.State.Rows {
			declined[row.TeamID] = row.Declined
		}
		troikas, err := imports.DefaultEntrants(ctx, tx, festID, KindTroika, game.Division, exclude)
		if err != nil {
			return nil, err
		}
		state := imports.ListState{Source: SourceTroikas, Division: game.Division}
		for rank, troika := range troikas {
			state.Rows = append(state.Rows, imports.ListRow{SourceRank: rank + 1, TeamID: troika.ParticipantID, Name: troika.Name, Declined: declined[troika.ParticipantID]})
		}
		scope := core.FestScope{FestID: festID, GameID: game.GameID}
		saved, err := applyListTx(ctx, tx, scope, list, list.With(state), "game:entrants")
		if err != nil {
			return nil, err
		}
		// A scheme that takes no other count keeps its written qualifier; the
		// troikas it has no row for wait on the list, and the page says why.
		game.Current = true
		game.Problem = saved.kept
	}
	return found, nil
}

// DeleteTroikaTx deletes a troika no bout seats. The Games that take a
// division let go of it first, and then every Troika Game that lists it
// without seating it (on a waiting list, say, or in a list the host built by
// hand) takes it off. A troika that sits in a bout keeps its place there, and
// the delete refuses it.
func DeleteTroikaTx(ctx context.Context, tx *sql.Tx, festID, troikaID int64) ([]DivisionGame, error) {
	followed, err := FollowDivisionsTx(ctx, tx, festID, troikaID)
	if err != nil {
		return nil, err
	}
	ids, err := TroikaGameIDs(ctx, tx, festID)
	if err != nil {
		return nil, err
	}
	for _, gameID := range ids {
		scope := core.FestScope{FestID: festID, GameID: gameID}
		list, err := imports.LoadListTx(ctx, tx, scope)
		if err != nil {
			return nil, err
		}
		i := list.Index(troikaID)
		if i < 0 {
			continue
		}
		seated, err := imports.InBoutTx(ctx, tx, gameID, troikaID)
		if err != nil {
			return nil, err
		}
		if seated {
			continue
		}
		next := list.Edit(slices.Delete(slices.Clone(list.State.Rows), i, i+1), imports.ListEdit{Op: imports.ListEditRemove, TeamID: troikaID})
		if _, err := applyListTx(ctx, tx, scope, list, next, "troikas:delete"); err != nil {
			return nil, err
		}
	}
	return followed, roster.DeleteAssembledTx(ctx, tx, festID, troikaID)
}

// ImportFestRoster imports the fest roster from rating.chgk.info
// (imports.ImportFestRoster). The import rewrites the teams' Flags, and a
// troika follows its head team's division, so the Troika Games that take one
// follow in the import's own transaction.
func ImportFestRoster(eng *core.Engine, ctx context.Context, festID, ratingID int64, teams []roster.FestRosterImportTeam, choice imports.RosterChoice) (imports.RatingRosterImportResult, error) {
	choice.Within = func(ctx context.Context, tx *sql.Tx) error {
		_, err := FollowDivisionsTx(ctx, tx, festID, 0)
		return err
	}
	return imports.ImportFestRoster(eng, ctx, festID, ratingID, teams, choice)
}
