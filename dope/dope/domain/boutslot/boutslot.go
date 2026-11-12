// Package boutslot moves a bout to another bout's slot of its Round: the wave
// it is played in, its venue, its time and its place in the order
// (CONTEXT.md, Bout slot). A bout keeps its code, its name and its seats, and so
// whom it feeds: the bracket follows bouts by code, never by their place, so a
// move changes when and where a bout is played and nothing else.
//
// A Game stores each moved bout's slot as the code of the compiled bout that
// slot was dealt to (matches.slot_of). The compiler deals the slots by the
// order it emits bouts in; ApplyMoves deals them again after a recompile.
package boutslot

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	corei18n "code.pecheny.me/pecheny/dopesuite/dopecore/i18nstrings"
	"dope/dope/domain/core"
	"dope/dope/domain/games"
	"dope/dope/storage/store"
	dopestrings "dope/i18nstrings"
)

// ApplyMoves puts each moved bout of a freshly compiled scheme into the slot it
// was moved to, moves[code] being the code of the bout the compiler dealt that
// slot. The bout takes the slot's place in its stage, its venue and its wave.
// It returns the moves it kept. A stage keeps its moves only while they still
// make a permutation of its slots within each Round: a scheme edit that took
// a slot away, or moved it to another Round, deals that stage afresh.
func ApplyMoves(scheme *store.FestScheme, moves map[string]string) map[string]string {
	kept := map[string]string{}
	if len(moves) == 0 {
		return kept
	}
	for si := range scheme.Stages {
		stage := &scheme.Stages[si]
		index := map[string]int{}
		for i, match := range stage.Matches {
			index[match.Code] = i
		}
		target := make([]int, len(stage.Matches))
		taken := make([]bool, len(stage.Matches))
		valid, moved := true, false
		for i, match := range stage.Matches {
			target[i] = i
			if slot, ok := moves[match.Code]; ok && slot != match.Code {
				j, ok := index[slot]
				if !ok || stage.Matches[j].BlockRound != match.BlockRound {
					valid = false
					break
				}
				target[i], moved = j, true
			}
			if taken[target[i]] {
				valid = false
				break
			}
			taken[target[i]] = true
		}
		if !valid || !moved {
			continue
		}
		dealt := make([]store.SchemeMatch, len(stage.Matches))
		for i, match := range stage.Matches {
			slot := stage.Matches[target[i]]
			match.Venue, match.Wave = slot.Venue, slot.Wave
			dealt[target[i]] = match
			if target[i] != i {
				kept[match.Code] = slot.Code
			}
		}
		stage.Matches = dealt
	}
	return kept
}

// swapScheme swaps two bouts of one stage in a stored scheme: their places in
// the stage, their venues and their waves. It reports whether it found both.
func swapScheme(scheme *store.FestScheme, a, b string) bool {
	for si := range scheme.Stages {
		matches := scheme.Stages[si].Matches
		ia, ib := -1, -1
		for i, match := range matches {
			switch match.Code {
			case a:
				ia = i
			case b:
				ib = i
			}
		}
		if ia < 0 || ib < 0 {
			continue
		}
		ma, mb := matches[ia], matches[ib]
		ma.Venue, mb.Venue = mb.Venue, ma.Venue
		ma.Wave, mb.Wave = mb.Wave, ma.Wave
		matches[ia], matches[ib] = mb, ma
		return true
	}
	return false
}

// bout is one bout as a swap reads it.
type bout struct {
	id       int64
	code     string
	label    string
	stageID  int64
	block    string
	round    int
	wave     int
	position int
	venueID  sql.NullInt64
	startsAt sql.NullString
	status   string
	state    string
	slotOf   string
}

func (b bout) slot() string {
	if b.slotOf != "" {
		return b.slotOf
	}
	return b.code
}

func (b bout) name() string {
	if b.label != "" {
		return b.label
	}
	return b.code
}

// SwapTx swaps the slots of two bouts of one Round of a Game: each is played
// where and when the other was. Neither may have begun, and when the two are
// in different waves nobody may end up playing twice in one wave. It
// returns the ids of the two bouts.
func SwapTx(ctx context.Context, tx *sql.Tx, scope core.FestScope, gameType, codeA, codeB string) ([]int64, error) {
	if codeA == codeB {
		return nil, corei18n.User(dopestrings.Default.Boutslot.Same())
	}
	load := func(code string) (bout, error) {
		var b bout
		err := tx.QueryRowContext(ctx, `
select m.id, m.code, m.letter, m.stage_id, coalesce(s.block_code, ''), m.round, m.wave, m.position,
       m.venue_id, m.starts_at, m.status, m.state_json, m.slot_of
from matches m join stages s on s.id = m.stage_id
where m.game_id = ? and m.code = ?`, scope.GameID, code).Scan(&b.id, &b.code, &b.label, &b.stageID, &b.block, &b.round,
			&b.wave, &b.position, &b.venueID, &b.startsAt, &b.status, &b.state, &b.slotOf)
		if err == sql.ErrNoRows {
			return b, corei18n.User(dopestrings.Default.Boutslot.Unknown(code))
		}
		return b, err
	}
	a, err := load(codeA)
	if err != nil {
		return nil, err
	}
	b, err := load(codeB)
	if err != nil {
		return nil, err
	}
	// A Round-robin Group's Rounds share its stage, so the Round tells them
	// apart; any other Kind's stage is one Round.
	if a.stageID != b.stageID || a.round != b.round {
		return nil, corei18n.User(dopestrings.Default.Boutslot.OtherRound(a.name(), b.name()))
	}
	for _, x := range []bout{a, b} {
		if x.status == "finished" || games.Started(gameType, x.state) {
			return nil, corei18n.User(dopestrings.Default.Boutslot.Begun(x.name()))
		}
	}
	if a.wave != b.wave {
		if err := refuseTwiceInAWave(ctx, tx, scope.GameID, a, b); err != nil {
			return nil, err
		}
		if err := refuseTwiceInAWave(ctx, tx, scope.GameID, b, a); err != nil {
			return nil, err
		}
	}
	slotOf := func(x bout, slot string) string {
		if slot == x.code {
			return ""
		}
		return slot
	}
	update := func(x, into bout) error {
		_, err := tx.ExecContext(ctx, `
update matches set slot_of = ?, position = ?, wave = ?, venue_id = ?, starts_at = ?, revision = revision + 1 where id = ?`,
			slotOf(x, into.slot()), into.position, into.wave, into.venueID, into.startsAt, x.id)
		return err
	}
	if err := update(a, b); err != nil {
		return nil, err
	}
	if err := update(b, a); err != nil {
		return nil, err
	}
	var schemeJSON string
	if err := tx.QueryRowContext(ctx, `select coalesce(scheme_json, '') from games where id = ?`, scope.GameID).Scan(&schemeJSON); err != nil {
		return nil, err
	}
	if strings.TrimSpace(schemeJSON) != "" {
		var scheme store.FestScheme
		if err := json.Unmarshal([]byte(schemeJSON), &scheme); err != nil {
			return nil, fmt.Errorf("game %d scheme: %w", scope.GameID, err)
		}
		if swapScheme(&scheme, a.code, b.code) {
			data, err := json.Marshal(scheme)
			if err != nil {
				return nil, err
			}
			if _, err := tx.ExecContext(ctx, `update games set scheme_json = ?, revision = revision + 1 where id = ?`, string(data), scope.GameID); err != nil {
				return nil, err
			}
		}
	}
	return []int64{a.id, b.id}, nil
}

// refuseTwiceInAWave refuses moving x into into's wave when someone seated
// in x already plays another bout of that wave.
func refuseTwiceInAWave(ctx context.Context, tx *sql.Tx, gameID int64, x, into bout) error {
	var player, other string
	err := tx.QueryRowContext(ctx, `
select p.name, case when m.letter <> '' then m.letter else m.code end
from match_slots mine
join participants p on p.id = mine.participant_id
join match_slots theirs on theirs.participant_id = mine.participant_id
join matches m on m.id = theirs.match_id
join stages s on s.id = m.stage_id
where mine.match_id = ? and m.game_id = ? and m.id not in (?, ?)
  and coalesce(s.block_code, '') = ? and m.round = ? and m.wave = ?
limit 1`, x.id, gameID, x.id, into.id, into.block, into.round, into.wave).Scan(&player, &other)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	return corei18n.User(dopestrings.Default.Boutslot.Twice(player, other, x.name()))
}
