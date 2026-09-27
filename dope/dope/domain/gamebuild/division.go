package gamebuild

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"

	"dope/dope/domain/games"
	"dope/dope/domain/protocol"
	"dope/dope/domain/roster"
	"dope/dope/domain/schemedsl"
	"dope/dope/storage/store"
	dopestrings "dope/i18nstrings"

	corei18n "pecheny.me/dopecore/i18nstrings"
)

// A Troika Game whose scheme declares a division in [init] without a seed seats
// the fest's troikas in that division (store.FestScheme.Division). Its entrant
// list follows the troikas page: a troika added, moved to another division or
// deleted there re-seats the Game, until the Game has anything entered. After
// that the list stays as it is, and only substitutions inside a troika reach
// the Game.

// DivisionGame is one such Game as the troikas page reports it: which division it
// takes, how many troikas are in it, whether its entrants are those troikas,
// and why not.
type DivisionGame struct {
	GameID   int64
	Title    string
	Division string
	// Troikas is who the division holds now, by name.
	Troikas []int64
	// Current says the Game seats exactly them.
	Current bool
	// Frozen says the Game has something entered, so its list no longer moves.
	Frozen bool
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

// TroikaGameIDs lists the fest's Troika Games.
func TroikaGameIDs(ctx context.Context, q store.Queryer, festID int64) ([]int64, error) {
	return store.CollectRows(ctx, q, `
select id from games where fest_id = ? and game_type = ? order by position, id`, []any{festID, games.Troika},
		func(rows *sql.Rows) (int64, error) {
			var id int64
			return id, rows.Scan(&id)
		})
}

// LoadDivisionGames reports the fest's Troika Games that take their entrants
// from a division, in the fest's order.
func LoadDivisionGames(ctx context.Context, q store.Queryer, festID int64) ([]DivisionGame, error) {
	return divisionGames(ctx, q, festID, 0)
}

func divisionGames(ctx context.Context, q store.Queryer, festID, exclude int64) ([]DivisionGame, error) {
	type row struct {
		id         int64
		title, dsl string
	}
	rows, err := store.CollectRows(ctx, q, `
select id, title, scheme_dsl from games
where fest_id = ? and game_type = ? and scheme_dsl is not null
order by position, id`, []any{festID, games.Troika}, func(rows *sql.Rows) (row, error) {
		var r row
		return r, rows.Scan(&r.id, &r.title, &r.dsl)
	})
	if err != nil {
		return nil, err
	}
	var out []DivisionGame
	for _, r := range rows {
		division, ok := entrantDivision(r.dsl)
		if !ok {
			continue
		}
		game := DivisionGame{GameID: r.id, Title: r.title, Division: division}
		if game.Troikas, err = divisionEntrantsTx(ctx, q, festID, division, exclude); err != nil {
			return nil, err
		}
		current, err := store.CollectRows(ctx, q, `
select participant_id from game_participants where game_id = ? order by position`, []any{r.id},
			func(rows *sql.Rows) (int64, error) {
				var id int64
				return id, rows.Scan(&id)
			})
		if err != nil {
			return nil, err
		}
		game.Current = slices.Equal(current, game.Troikas)
		if game.Frozen, err = gameEntered(ctx, q, r.id); err != nil {
			return nil, err
		}
		out = append(out, game)
	}
	return out, nil
}

// gameEntered reports whether anything has been entered in a Game: a finished
// bout or one with marks on it.
func gameEntered(ctx context.Context, q store.Queryer, gameID int64) (bool, error) {
	type match struct {
		status, state string
	}
	matches, err := store.CollectRows(ctx, q, `
select status, coalesce(state_json, '{}') from matches where game_id = ?`, []any{gameID},
		func(rows *sql.Rows) (match, error) {
			var m match
			return m, rows.Scan(&m.status, &m.state)
		})
	if err != nil {
		return false, err
	}
	for _, m := range matches {
		if m.status == "finished" || protocol.Started(games.Troika, m.state) {
			return true, nil
		}
	}
	return false, nil
}

// SyncDivisionEntrantsTx re-seats every Troika Game of the fest that takes a
// division and whose entrants are no longer that division's troikas, as long as
// nothing is entered in it. exclude leaves one troika out, the one a delete is
// about to remove. It returns the Games it looked at; a re-seat the scheme
// refuses (too few troikas for it) is reported on the Game, not returned, so
// the troika edit that caused it still saves.
func SyncDivisionEntrantsTx(ctx context.Context, tx *sql.Tx, festID, exclude int64) ([]DivisionGame, error) {
	found, err := divisionGames(ctx, tx, festID, exclude)
	if err != nil {
		return nil, err
	}
	s := dopestrings.Default
	for i := range found {
		game := &found[i]
		if game.Current || game.Frozen {
			continue
		}
		if len(game.Troikas) == 0 {
			game.Problem = s.Gamebuild.Division.NoTroikas(game.Division)
			continue
		}
		if _, err := tx.ExecContext(ctx, `savepoint division_entrants`); err != nil {
			return nil, err
		}
		_, err := clearGame(ctx, tx, festID, game.GameID, game.Troikas, "game:entrants")
		if err != nil {
			message, user := corei18n.AsUser(err)
			var compile *schemedsl.Error
			if errors.As(err, &compile) {
				message, user = compile.Msg, true
			}
			if !user {
				return nil, err
			}
			if _, err := tx.ExecContext(ctx, `rollback to division_entrants`); err != nil {
				return nil, err
			}
			game.Problem = message
		} else {
			game.Current = true
		}
		if _, err := tx.ExecContext(ctx, `release division_entrants`); err != nil {
			return nil, err
		}
	}
	return found, nil
}
