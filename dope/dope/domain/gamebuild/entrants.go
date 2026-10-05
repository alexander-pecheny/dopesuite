package gamebuild

import (
	"context"
	"database/sql"
	"errors"
	"slices"

	"dope/dope/domain/core"
	"dope/dope/domain/games"
	"dope/dope/domain/imports"
	"dope/dope/domain/schemedsl"
	dopestrings "dope/i18nstrings"

	corei18n "pecheny.me/dopecore/i18nstrings"
)

// Applied is what saving an Entrant list did: the tab as it reads now, the
// revision and game document it recorded, whether the Structure was rebuilt
// for the list, and, when a rebuild was tried and the scheme turned it down,
// why (Kept) — the list then fills the Structure's seats as they are.
type Applied struct {
	View      imports.SeedImportView
	Revision  int64
	StateJSON []byte
	Rebuilt   bool
	Kept      string
}

// ApplyListTx saves a Game's Entrant list (CONTEXT.md) and seats it. A Game
// whose Structure is compiled against its entrants (imports.EntrantSized) is
// recompiled for the list's active entrants first, as long as nothing has been
// entered in it: then a troika added to a Troika gets a row in the written qualifier. A
// scheme of a fixed size (a roundrobin of groups of four) refuses another
// count; the Structure then stays, and the list fills its seats, the rest
// waiting. Once anything is entered the Structure stays as it is, except that
// a written qualifier grows for a troika added after it, and the list only
// moves entrants between the seats of bouts nobody has started.
func ApplyListTx(ctx context.Context, tx *sql.Tx, scope core.FestScope, current, next imports.List, event string) (Applied, error) {
	var applied Applied
	var dsl string
	if err := tx.QueryRowContext(ctx, `
select coalesce(scheme_dsl, '') from games where id = ? and fest_id = ?`, scope.GameID, scope.FestID).Scan(&dsl); err != nil {
		return applied, err
	}
	if imports.EntrantSized(dsl) {
		var err error
		if applied.Rebuilt, applied.Kept, err = reseatForListTx(ctx, tx, scope, dsl, current.GameType, next); err != nil {
			return applied, err
		}
	}
	var err error
	applied.View, applied.Revision, applied.StateJSON, err = imports.SaveListTx(ctx, tx, scope, current, next, event)
	return applied, err
}

// tryReseatTx recompiles the Game for the entrants, or, when its scheme turns
// them down, leaves it as it was and says why.
// reseatForListTx recompiles an entrant-sized Structure for the list's
// active entrants, when the Game's state still allows it.
func reseatForListTx(ctx context.Context, tx *sql.Tx, scope core.FestScope, dsl, gameType string, next imports.List) (bool, string, error) {
	entered, err := imports.GameEntered(ctx, tx, scope.GameID, gameType)
	if err != nil {
		return false, "", err
	}
	if !entered {
		return tryReseatTx(ctx, tx, scope, dsl, next.Active())
	}
	// Once something is entered, the Structure changes only by growing: a
	// late troika takes a row of a written qualifier that has results, as
	// long as nothing after it is played (Recompile says when). Anything
	// else leaves the Structure as it is, and the list waits, quietly: the
	// tab already says results fix the seats.
	if _, grows := games.As[games.Grower](gameType); !grows {
		return false, "", nil
	}
	seated, err := gameEntrantsTx(ctx, tx, scope.GameID)
	if err != nil {
		return false, "", err
	}
	active := next.Active()
	if len(active) <= len(seated) || !slices.Equal(active[:len(seated)], seated) {
		return false, "", nil
	}
	rebuilt, _, err := tryReseatTx(ctx, tx, scope, dsl, active)
	return rebuilt, "", err
}

func tryReseatTx(ctx context.Context, tx *sql.Tx, scope core.FestScope, dsl string, entrants []int64) (bool, string, error) {
	if _, err := tx.ExecContext(ctx, `savepoint entrant_reseat`); err != nil {
		return false, "", err
	}
	rebuilt, err := reseatEntrantsTx(ctx, tx, scope, dsl, entrants)
	if err != nil {
		message, user := corei18n.AsUser(err)
		if !user {
			return false, "", err
		}
		if _, err := tx.ExecContext(ctx, `rollback to entrant_reseat`); err != nil {
			return false, "", err
		}
		if _, err := tx.ExecContext(ctx, `release entrant_reseat`); err != nil {
			return false, "", err
		}
		return false, message, nil
	}
	_, err = tx.ExecContext(ctx, `release entrant_reseat`)
	return rebuilt, "", err
}

// reseatEntrantsTx recompiles the Game for these entrants, numbered 1… in the
// order given, unless it seats exactly them already.
// seatEntrantsTx replaces the Game's seating with entrants, in order.
func seatEntrantsTx(ctx context.Context, tx *sql.Tx, gameID int64, entrants []int64) error {
	for _, q := range []string{
		`delete from game_assignments where game_id = ?`,
		`delete from game_participants where game_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, gameID); err != nil {
			return err
		}
	}
	if err := seatChosenTx(ctx, tx, gameID, entrants); err != nil {
		return err
	}
	for i, participantID := range entrants {
		if _, err := tx.ExecContext(ctx, `
insert into game_participants(game_id, participant_id, position, number) values(?, ?, ?, ?)`,
			gameID, participantID, i+1, i+1); err != nil {
			return err
		}
	}
	return nil
}

func reseatEntrantsTx(ctx context.Context, tx *sql.Tx, scope core.FestScope, dsl string, entrants []int64) (bool, error) {
	seated, err := gameEntrantsTx(ctx, tx, scope.GameID)
	if err != nil {
		return false, err
	}
	if slices.Equal(seated, entrants) {
		return false, nil
	}
	if len(entrants) == 0 {
		return false, corei18n.User(dopestrings.Default.Gamebuild.Entrants.NoneLeft())
	}
	if err := seatEntrantsTx(ctx, tx, scope.GameID, entrants); err != nil {
		return false, err
	}
	if err := Recompile(ctx, tx, scope.FestID, scope.GameID, dsl); err != nil {
		var compile *schemedsl.Error
		if errors.As(err, &compile) {
			return false, corei18n.User(dopestrings.Default.Gamebuild.Entrants.SchemeRefuses(len(entrants), compile.Msg))
		}
		return false, err
	}
	return true, nil
}
