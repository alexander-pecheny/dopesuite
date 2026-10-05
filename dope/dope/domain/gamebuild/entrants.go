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

// FollowListTx makes an entrant-sized Structure (imports.EntrantSized) follow
// the Entrant list's active entrants, the Structure's half of saving the list
// (entrants owns the other half). While nothing has been entered in the Game
// it is recompiled for them. A scheme of a fixed size (a roundrobin of groups
// of four) refuses another count; the Structure then stays, kept says why,
// and the list fills its seats, the rest waiting. Once anything is entered
// the Structure stays as it is, except that a written qualifier grows for a
// troika added after it.
func FollowListTx(ctx context.Context, tx *sql.Tx, scope core.FestScope, dsl, gameType string, active []int64) (rebuilt bool, kept string, err error) {
	entered, err := imports.GameEntered(ctx, tx, scope.GameID, gameType)
	if err != nil {
		return false, "", err
	}
	if !entered {
		return tryReseatTx(ctx, tx, scope, dsl, active)
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
	if len(active) <= len(seated) || !slices.Equal(active[:len(seated)], seated) {
		return false, "", nil
	}
	rebuilt, _, err = tryReseatTx(ctx, tx, scope, dsl, active)
	return rebuilt, "", err
}

// tryReseatTx recompiles the Game for the entrants, or, when its scheme turns
// them down, leaves it as it was and says why.
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

// reseatEntrantsTx recompiles the Game for these entrants, numbered 1… in the
// order given, unless it seats exactly them already.
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
