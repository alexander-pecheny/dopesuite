package gamebuild

import (
	"context"
	"database/sql"
	"slices"
	"strings"

	"dope/dope/domain/core"
	"dope/dope/domain/games"
	"dope/dope/domain/imports"
	"dope/dope/domain/roster"
	"dope/dope/domain/schemedsl"
	"dope/dope/storage/store"
	dopestrings "dope/i18nstrings"
)

// A Troika Game whose scheme declares a division in [init] without a seed seats
// the fest's troikas in that division (store.FestScheme.Division). Its Entrant
// list follows the troikas page: a troika added, moved to another division or
// deleted there changes the list, and so the Game's written qualifier, until the Game has
// anything entered. After that the list is the host's to edit on the Game's
// entrants tab, and so it is once the host has edited it there or imported it
// from another source.

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

// entrantDivision reads the division a scheme DSL takes its entrants from:
// division in [init] with no seed.
func entrantDivision(dsl string) (string, bool) {
	doc, err := schemedsl.Parse(dsl)
	if err != nil {
		return "", false
	}
	if _, seeded := doc.Init.Str("seed"); seeded {
		return "", false
	}
	division, ok := doc.Init.Str("division")
	division = strings.TrimSpace(division)
	return division, ok && division != ""
}

// declaresSeed reports whether a scheme's [init] names a seed source, which
// then decides the entrants instead of the form.
func declaresSeed(dsl string) bool {
	doc, err := schemedsl.Parse(dsl)
	if err != nil {
		return false
	}
	_, seeded := doc.Init.Str("seed")
	return seeded
}

// festTroikasTx is every troika of the fest, in the order of applications.
func festTroikasTx(ctx context.Context, q store.Queryer, festID int64) ([]int64, error) {
	troikas, err := roster.LoadAssembled(ctx, q, festID)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(troikas))
	for i, t := range troikas {
		ids[i] = t.ID
	}
	return ids, nil
}

// EntrantDivision is the division a Game's scheme takes its troikas from, and
// whether it takes one.
func EntrantDivision(dsl string) (string, bool) { return entrantDivision(dsl) }

// divisionEntrantsTx is who a Game of that division seats, by name; empty is a
// message for the host, since a Game without entrants would seat the whole
// fest roster instead.
func divisionEntrantsTx(ctx context.Context, q store.Queryer, festID int64, division string, exclude int64) ([]int64, error) {
	troikas, err := roster.AssembledInDivision(ctx, q, festID, division, exclude)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(troikas))
	for i, t := range troikas {
		ids[i] = t.ID
	}
	return ids, nil
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
		id                   int64
		title, dsl, gameType string
	}
	filter, args := troikaFormats(festID)
	rows, err := store.CollectRows(ctx, q, `
select id, title, scheme_dsl, game_type from games
where fest_id = ? and `+filter+` and scheme_dsl is not null
order by position, id`, args, func(rows *sql.Rows) (row, error) {
		var r row
		return r, rows.Scan(&r.id, &r.title, &r.dsl, &r.gameType)
	})
	if err != nil {
		return nil, nil, err
	}
	var out []DivisionGame
	var lists []imports.List
	for _, r := range rows {
		division, ok := entrantDivision(r.dsl)
		if !ok {
			continue
		}
		game := DivisionGame{GameID: r.id, Title: r.title, Division: division}
		if game.Troikas, err = divisionEntrantsTx(ctx, q, festID, division, exclude); err != nil {
			return nil, nil, err
		}
		list, err := imports.LoadListTx(ctx, q, core.FestScope{FestID: festID, GameID: r.id})
		if err != nil {
			return nil, nil, err
		}
		game.Current = slices.Equal(list.Active(), game.Troikas)
		game.Manual = list.State.Edited || (list.State.Source != "" && list.State.Source != "troikas")
		if game.Frozen, err = imports.GameEntered(ctx, q, r.id, r.gameType); err != nil {
			return nil, nil, err
		}
		out = append(out, game)
		lists = append(lists, list)
	}
	return out, lists, nil
}

// SyncDivisionEntrantsTx brings the Entrant list of every Troika Game of the
// fest that takes a division, and still follows it, to that division's troikas
// — as long as nothing is entered in the Game. exclude leaves one troika out,
// the one a delete is about to remove. A troika that declined keeps its mark.
// It returns the Games it looked at; a re-seat the scheme refuses (too few
// troikas for it) is reported on the Game, not returned, so the troika edit
// that caused it still saves.
func SyncDivisionEntrantsTx(ctx context.Context, tx *sql.Tx, festID, exclude int64) ([]DivisionGame, error) {
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
		// played (ApplyListTx). A troika that left the division stays where its
		// results are.
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
		troikas, err := roster.AssembledInDivision(ctx, tx, festID, game.Division, exclude)
		if err != nil {
			return nil, err
		}
		state := imports.ListState{Source: "troikas", Division: game.Division}
		for rank, troika := range troikas {
			state.Rows = append(state.Rows, imports.ListRow{SourceRank: rank + 1, TeamID: troika.ID, Name: troika.Name, Declined: declined[troika.ID]})
		}
		scope := core.FestScope{FestID: festID, GameID: game.GameID}
		applied, err := ApplyListTx(ctx, tx, scope, list, list.With(state), "game:entrants")
		if err != nil {
			return nil, err
		}
		// A scheme that takes no other count keeps its written qualifier; the troikas it
		// has no row for wait on the list, and the page says why.
		game.Current = true
		game.Problem = applied.Kept
	}
	return found, nil
}

// DropTroikaFromListsTx takes a troika about to be deleted off the Entrant list
// of every Troika Game that lists it without seating it — on a waiting list,
// say, or in a list the host built by hand. A troika that sits in a bout keeps
// its place, and the delete refuses it.
func DropTroikaFromListsTx(ctx context.Context, tx *sql.Tx, festID, troikaID int64) error {
	ids, err := TroikaGameIDs(ctx, tx, festID)
	if err != nil {
		return err
	}
	for _, gameID := range ids {
		scope := core.FestScope{FestID: festID, GameID: gameID}
		list, err := imports.LoadListTx(ctx, tx, scope)
		if err != nil {
			return err
		}
		i := list.Index(troikaID)
		if i < 0 {
			continue
		}
		var seated bool
		if err := tx.QueryRowContext(ctx, `
select exists(select 1 from match_slots ms join matches m on m.id = ms.match_id where m.game_id = ? and ms.participant_id = ?)`,
			gameID, troikaID).Scan(&seated); err != nil {
			return err
		}
		if seated {
			continue
		}
		next := list.Edit(slices.Delete(slices.Clone(list.State.Rows), i, i+1), imports.ListEdit{Op: imports.ListEditRemove, TeamID: troikaID})
		if _, err := ApplyListTx(ctx, tx, scope, list, next, "troikas:delete"); err != nil {
			return err
		}
	}
	return nil
}
