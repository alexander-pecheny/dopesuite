package gamebuild

import (
	"context"
	"database/sql"
	"errors"
	"slices"

	"dope/dope/domain/core"
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
// waiting. Once anything is entered the Structure stays as it is, and the list
// only moves entrants between the seats of bouts nobody has started.
func ApplyListTx(ctx context.Context, tx *sql.Tx, scope core.FestScope, current, next imports.List, event string) (Applied, error) {
	var applied Applied
	var dsl string
	if err := tx.QueryRowContext(ctx, `
select coalesce(scheme_dsl, '') from games where id = ? and fest_id = ?`, scope.GameID, scope.FestID).Scan(&dsl); err != nil {
		return applied, err
	}
	if imports.EntrantSized(dsl) {
		entered, err := imports.GameEntered(ctx, tx, scope.GameID, current.GameType)
		if err != nil {
			return applied, err
		}
		if !entered {
			if applied.Rebuilt, applied.Kept, err = tryReseatTx(ctx, tx, scope, dsl, next.Active()); err != nil {
				return applied, err
			}
		}
	}
	var err error
	applied.View, applied.Revision, applied.StateJSON, err = imports.SaveListTx(ctx, tx, scope, current, next, event)
	return applied, err
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
	for _, q := range []string{
		`delete from game_assignments where game_id = ?`,
		`delete from game_participants where game_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, scope.GameID); err != nil {
			return false, err
		}
	}
	if err := seatChosenTx(ctx, tx, scope.GameID, entrants); err != nil {
		return false, err
	}
	for i, participantID := range entrants {
		if _, err := tx.ExecContext(ctx, `
insert into game_participants(game_id, participant_id, position, number) values(?, ?, ?, ?)`,
			scope.GameID, participantID, i+1, i+1); err != nil {
			return false, err
		}
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
