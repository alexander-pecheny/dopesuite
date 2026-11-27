package roster

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"dope/dope/storage/store"
	dopestrings "dope/i18nstrings"

	corei18n "code.pecheny.me/pecheny/dopesuite/dopecore/i18nstrings"
)

// The writes of a tethered Game's overrides (ADR-0038): add one, take one
// back, and the number typed in the table, which is a number override.

// tetheredGame reads a Game that follows a source, about to change its
// overrides, with the teams the source and its overrides give it now.
func tetheredGame(ctx context.Context, tx *sql.Tx, festID, gameID int64) (changed, error) {
	doc, src, err := changingGame(ctx, tx, festID, gameID)
	if err != nil {
		return changed{}, err
	}
	if src.own {
		return changed{}, corei18n.User(dopestrings.Default.Entrants.Own.HasList())
	}
	// A bout game's first override takes its hand rosters along.
	if BoutTeamGame(doc.GameType) && src.plain() {
		if src, err = handRostersToOverrides(ctx, tx, festID, doc, src); err != nil {
			return changed{}, err
		}
	}
	teams, _, err := resolveTeams(ctx, tx, festID, src, map[int64]bool{})
	return changed{doc: doc, src: src, teams: teams}, err
}

type changed struct {
	doc   store.GameDoc
	src   teamsSource
	teams []OwnTeam
}

// save stores the overrides and refolds the Game and its followers.
func (c changed) save(ctx context.Context, tx *sql.Tx, festID int64) ([]GameStateBroadcast, error) {
	if err := storeSource(ctx, tx, c.src); err != nil {
		return nil, err
	}
	return refoldWithFollowersTx(ctx, tx, festID, c.doc)
}

// AddOverrideTx adds an override to a tethered Game. It refuses one that
// does not apply to the teams as they are, and one that would take away a
// team the Game holds answers for.
func AddOverrideTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, o Override) ([]GameStateBroadcast, error) {
	c, err := tetheredGame(ctx, tx, festID, gameID)
	if err != nil {
		return nil, err
	}
	o.ID = nextOverrideID(c.src.overrides)
	if BoutTeamGame(c.doc.GameType) && !boutOverride(o.Kind) {
		return nil, corei18n.User(dopestrings.Default.Entrants.Own.OverrideFailed())
	}
	if err := c.prepare(&o); err != nil {
		return nil, err
	}
	if _, ok := applyOverride(append([]OwnTeam(nil), c.teams...), o); !ok {
		return nil, corei18n.User(dopestrings.Default.Entrants.Own.OverrideFailed())
	}
	c.src.overrides = append(c.src.overrides, o)
	return c.save(ctx, tx, festID)
}

// boutOverride says a bout game takes this kind of override: who a team is,
// never who sits where (its entrant list says that).
func boutOverride(kind string) bool {
	switch kind {
	case OverrideRename, OverrideCity, OverrideAddPlayer, OverrideRemovePlayer, OverrideMovePlayer:
		return true
	}
	return false
}

func nextOverrideID(overrides []Override) int64 {
	var top int64
	for _, o := range overrides {
		top = max(top, o.ID)
	}
	return top + 1
}

// prepare tidies an override and gives it its labels: the team names it
// shows when its team is gone.
func (c changed) prepare(o *Override) error {
	o.Name, o.City = strings.TrimSpace(o.Name), strings.TrimSpace(o.City)
	if o.Kind == OverrideAddTeam {
		return c.prepareAdd(o)
	}
	at := teamAt(c.teams, o.Team)
	if at < 0 {
		return corei18n.User(dopestrings.Default.Entrants.Own.OverrideFailed())
	}
	o.Label = c.teams[at].Name
	if to := teamAt(c.teams, o.To); to >= 0 {
		o.ToLabel = c.teams[to].Name
	}
	switch o.Kind {
	case OverrideRename:
		if o.Name == "" {
			return corei18n.User(dopestrings.Default.Entrants.Own.NameMissing("1"))
		}
	case OverrideFlags:
		o.Flags = FlagShortNames(ParseFlags(strings.Join(o.Flags, ",")))
	case OverrideRemoveTeam:
		for _, seat := range enteredSeats(c.doc) {
			if seat.Number == c.teams[at].Number {
				return corei18n.User(dopestrings.Default.Entrants.Own.Entered(seat.Name))
			}
		}
	}
	return nil
}

// prepareAdd gives an added team its key and, without one, the next number.
func (c changed) prepareAdd(o *Override) error {
	if o.Add == nil || strings.TrimSpace(o.Add.Name) == "" {
		return corei18n.User(dopestrings.Default.Entrants.Own.NameMissing("1"))
	}
	add := o.Add.stored()
	add.Name, add.City = strings.TrimSpace(add.Name), strings.TrimSpace(add.City)
	if add.Number <= 0 {
		add.Number = topNumber(c.teams) + 1
	}
	o.Add, o.Team, o.Label = &add, fmt.Sprintf("a%d", o.ID), add.Name
	return nil
}

// DeleteOverrideTx takes an override back.
func DeleteOverrideTx(ctx context.Context, tx *sql.Tx, festID, gameID, overrideID int64) ([]GameStateBroadcast, error) {
	c, err := tetheredGame(ctx, tx, festID, gameID)
	if err != nil {
		return nil, err
	}
	kept := c.src.overrides[:0:0]
	for _, o := range c.src.overrides {
		if o.ID != overrideID {
			kept = append(kept, o)
		}
	}
	// A team that leaves with its override but has answers stays for them
	// (planSeats).
	c.src.overrides = kept
	return c.save(ctx, tx, festID)
}

// SetTeamNumberTx gives a team of a tethered Game a number of its own: a
// number override, which replaces the team's earlier one. The source's own
// number takes the override away.
func SetTeamNumberTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, key string, number int64) ([]GameStateBroadcast, error) {
	c, err := tetheredGame(ctx, tx, festID, gameID)
	if err != nil {
		return nil, err
	}
	kept := c.src.overrides[:0:0]
	for _, o := range c.src.overrides {
		if o.Kind != OverrideNumber || o.Team != key {
			kept = append(kept, o)
		}
	}
	c.src.overrides = kept
	before, _, err := resolveTeams(ctx, tx, festID, c.src, map[int64]bool{})
	if err != nil {
		return nil, err
	}
	at := teamAt(before, key)
	if at < 0 || number <= 0 {
		return nil, corei18n.User(dopestrings.Default.Entrants.Own.NumberMissing(key))
	}
	if before[at].Number != number {
		o := Override{ID: nextOverrideID(c.src.overrides), Kind: OverrideNumber, Team: key, Label: before[at].Name, Number: number}
		if _, ok := applyOverride(append([]OwnTeam(nil), before...), o); !ok {
			return nil, corei18n.User(dopestrings.Default.Entrants.Own.NumberTaken(fmt.Sprint(number), before[at].Name, numberOwner(before, number)))
		}
		c.src.overrides = append(c.src.overrides, o)
	}
	return c.save(ctx, tx, festID)
}

func numberOwner(teams []OwnTeam, number int64) string {
	for _, team := range teams {
		if team.Number == number {
			return team.Name
		}
	}
	return ""
}
