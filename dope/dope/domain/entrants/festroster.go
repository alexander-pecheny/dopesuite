package entrants

import (
	"context"
	"database/sql"
	"slices"
	"strings"

	"dope/dope/domain/core"
	"dope/dope/domain/gamebuild"
	"dope/dope/domain/imports"
	"dope/dope/domain/roster"
)

// The writes to the fest roster that may move a troika between divisions: a
// troika added, edited or deleted, a team's Flags typed by hand, a team added,
// edited or taken off, a rating import, and a Game's scheme changed. Each runs
// the write and then the follow (followDivisionsTx) in the caller's
// transaction, and returns a core.FestWrite whose Broadcast names every page
// the write changed, so the commit step tells them. A caller cannot forget the
// follow or the broadcast, because it never makes either itself.

// followTx re-seats the Troika Games that take a division (exclude leaves out
// a troika being deleted) and names every Troika Game for the broadcast: a
// troika's people, or a Game's entrants, may have changed.
func followTx(ctx context.Context, tx *sql.Tx, festID, exclude int64) ([]DivisionGame, []int64, error) {
	followed, err := followDivisionsTx(ctx, tx, festID, exclude)
	if err != nil {
		return nil, nil, err
	}
	views, err := TroikaGameIDs(ctx, tx, festID)
	return followed, views, err
}

// TroikaWrite is what a write on the troikas page did: the fest write to
// commit, the Games that take a division as the follow left them (so the page
// can say which could not follow), and the troikas it added.
type TroikaWrite struct {
	Write core.FestWrite
	Games []DivisionGame
	Added []int64
}

// troikaWrite runs one troika write and the follow.
func troikaWrite(ctx context.Context, tx *sql.Tx, festID int64, label string, exclude int64, write func() error) (TroikaWrite, error) {
	if err := write(); err != nil {
		return TroikaWrite{}, err
	}
	followed, views, err := followTx(ctx, tx, festID, exclude)
	if err != nil {
		return TroikaWrite{}, err
	}
	return TroikaWrite{
		Write: core.FestWrite{Event: "troikas:" + label, Payload: map[string]any{"label": label}, Broadcast: core.Broadcast{Views: views}},
		Games: followed,
	}, nil
}

// AddTroikasTx adds troikas, one per input.
func AddTroikasTx(ctx context.Context, tx *sql.Tx, festID int64, inputs []roster.AssembledInput) (TroikaWrite, error) {
	var added []int64
	out, err := troikaWrite(ctx, tx, festID, "add", 0, func() error {
		for _, in := range inputs {
			id, err := roster.SaveAssembledTx(ctx, tx, festID, 0, in)
			if err != nil {
				return err
			}
			added = append(added, id)
		}
		return nil
	})
	out.Added = added
	return out, err
}

// SaveTroikaTx renames one troika and sets its players, and its head team and
// division when in.Placement says so.
func SaveTroikaTx(ctx context.Context, tx *sql.Tx, festID, troikaID int64, in roster.AssembledInput) (TroikaWrite, error) {
	return troikaWrite(ctx, tx, festID, "edit", 0, func() error {
		_, err := roster.SaveAssembledTx(ctx, tx, festID, troikaID, in)
		return err
	})
}

// DeleteTroikaTx deletes a troika no bout seats. The Games that take a
// division let go of it first, and then every Troika Game that lists it
// without seating it (on a waiting list, say, or in a list the host built by
// hand) takes it off. A troika that sits in a bout keeps its place there, and
// the delete refuses it.
func DeleteTroikaTx(ctx context.Context, tx *sql.Tx, festID, troikaID int64) (TroikaWrite, error) {
	out, err := troikaWrite(ctx, tx, festID, "delete", troikaID, func() error { return nil })
	if err != nil {
		return TroikaWrite{}, err
	}
	for _, gameID := range out.Write.Broadcast.Views {
		if err := unlistTroikaTx(ctx, tx, core.FestScope{FestID: festID, GameID: gameID}, troikaID); err != nil {
			return TroikaWrite{}, err
		}
	}
	return out, roster.DeleteAssembledTx(ctx, tx, festID, troikaID)
}

// unlistTroikaTx takes a troika off a Troika Game's list, unless a bout of
// the Game seats it.
func unlistTroikaTx(ctx context.Context, tx *sql.Tx, scope core.FestScope, troikaID int64) error {
	list, err := imports.LoadListTx(ctx, tx, scope)
	if err != nil {
		return err
	}
	i := list.Index(troikaID)
	if i < 0 {
		return nil
	}
	seated, err := imports.InBoutTx(ctx, tx, scope.GameID, troikaID)
	if err != nil || seated {
		return err
	}
	next := list.Edit(slices.Delete(slices.Clone(list.State.Rows), i, i+1), imports.ListEdit{Op: imports.ListEditRemove, TeamID: troikaID})
	_, err = applyListTx(ctx, tx, scope, list, next, "troikas:delete")
	return err
}

// SaveTeamFlagsTx sets the Flags the host typed for the named teams; teams
// left out keep theirs. Flags the host typed are the host's: an import leaves
// them alone (ADR-0024). The roster snapshots an undo would go back to are
// forgotten, the roster is folded into every flat Protocol's document, and a
// troika follows its head team's division, so the Troika Games that take one
// follow too.
func SaveTeamFlagsTx(ctx context.Context, tx *sql.Tx, festID int64, flagsByTeam map[int64][]roster.FestRosterFlag) (core.FestWrite, error) {
	before, err := roster.LoadFestTeamFlags(ctx, tx, festID)
	if err != nil {
		return core.FestWrite{}, err
	}
	for teamID, flags := range flagsByTeam {
		// Only a team whose Flags this save changes is marked as typed by
		// hand, since the teams page posts every team's.
		if strings.Join(roster.FlagShortNames(before[teamID]), ",") == strings.Join(roster.FlagShortNames(flags), ",") {
			continue
		}
		if err := roster.SetHandFlagsTx(ctx, tx, festID, teamID, flags); err != nil {
			return core.FestWrite{}, err
		}
	}
	if err := imports.ForgetRosterSnapshotsTx(ctx, tx, festID); err != nil {
		return core.FestWrite{}, err
	}
	teams, err := roster.LoadFestRosterImportTeamsTx(ctx, tx, festID)
	if err != nil {
		return core.FestWrite{}, err
	}
	updates, err := roster.PropagateRosterTx(ctx, tx, festID, teams, nil)
	if err != nil {
		return core.FestWrite{}, err
	}
	_, views, err := followTx(ctx, tx, festID, 0)
	if err != nil {
		return core.FestWrite{}, err
	}
	return core.FestWrite{
		Event:     "fest:team-flags",
		Payload:   map[string]any{"teams": len(flagsByTeam)},
		Broadcast: core.Broadcast{States: gameStates(updates), Views: views},
	}, nil
}

// EditRosterTx runs one of the host's edits to the fest roster (a team added,
// edited or taken off by hand, a roster sheet, the undo of an import) and the
// follow, and records it under event.
func EditRosterTx(ctx context.Context, tx *sql.Tx, festID int64, event string, edit func(ctx context.Context, tx *sql.Tx) (imports.RosterWrite, error)) (core.FestWrite, error) {
	written, err := edit(ctx, tx)
	if err != nil {
		return core.FestWrite{}, err
	}
	_, views, err := followTx(ctx, tx, festID, 0)
	if err != nil {
		return core.FestWrite{}, err
	}
	return core.FestWrite{
		Event:     event,
		Broadcast: core.Broadcast{States: gameStates(written.Updates), Rosters: written.EKGameIDs, Views: views},
	}, nil
}

// RecompileTx compiles a Game's changed scheme (gamebuild.Recompile). A
// Troika Game that now takes a division, or another one, seats its troikas,
// so the follow runs too. It returns the Games whose fest view changed.
func RecompileTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, dsl string) ([]int64, error) {
	if err := gamebuild.Recompile(ctx, tx, festID, gameID, dsl); err != nil {
		return nil, err
	}
	_, views, err := followTx(ctx, tx, festID, 0)
	return views, err
}

// ImportFestRoster imports the fest roster from rating.chgk.info
// (imports.ImportFestRoster). The import rewrites the teams' Flags, and a
// troika follows its head team's division, so the Troika Games that take one
// follow in the import's own transaction. The result's Views names them for
// the caller to tell.
func ImportFestRoster(eng *core.Engine, ctx context.Context, festID, ratingID int64, teams []roster.FestRosterImportTeam, choice imports.RosterChoice) (imports.RatingRosterImportResult, error) {
	var views []int64
	choice.Within = func(ctx context.Context, tx *sql.Tx) error {
		var err error
		_, views, err = followTx(ctx, tx, festID, 0)
		return err
	}
	result, err := imports.ImportFestRoster(eng, ctx, festID, ratingID, teams, choice)
	if err != nil || result.Revision == 0 {
		return result, err
	}
	result.Views = views
	return result, nil
}

func gameStates(updates []roster.GameStateBroadcast) []core.GameState {
	out := make([]core.GameState, 0, len(updates))
	for _, u := range updates {
		out = append(out, core.GameState{GameID: u.GameID, StateJSON: u.StateJSON})
	}
	return out
}
