package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"dope/dope/domain/core"
	"dope/dope/domain/edit"
	"dope/dope/domain/gamebuild"
	"dope/dope/domain/games"
	"dope/dope/domain/replay"
	"dope/dope/domain/resolver"
	"dope/dope/web/editbatch"
)

// buildTroika plays the transcript through the same writes a host's taps make:
// the seating a Draw declares, the marks as a match patch, the finish, and the
// resolver after each бой — the replay driver without its assertions, which
// server/tests already ran over this very transcript.
func buildTroika(ctx context.Context, db *sql.DB, festID int64, registry map[string]int64,
	script replay.Script, root string) error {
	dsl, err := os.ReadFile(root + "/scripts/troika/troika.dsl")
	if err != nil {
		return err
	}
	entrants := make([]int64, 0, len(script.Roster))
	for _, team := range script.Roster {
		entrants = append(entrants, registry[team.Name])
	}
	gameID, err := createGame(ctx, db, gamebuild.Spec{
		FestID: festID, Type: games.Troika, Label: "Троечка", DSL: string(dsl), Entrants: entrants,
	})
	if err != nil {
		return err
	}
	scope := core.FestScope{FestID: festID, GameID: gameID}
	log.Printf("троечка: игра %d, боёв %d", gameID, len(script.Bouts))

	for _, bout := range script.Bouts {
		matchID, err := matchAt(db, gameID, bout.At)
		if err != nil {
			return fmt.Errorf("%s: %w", bout.At, err)
		}
		if bout.Draw {
			for index, seat := range bout.Seats {
				if _, err := db.Exec(`
update match_slots set source_type = 'seed', participant_id = ?
where match_id = ? and slot_index = ?`, registry[seat.Name], matchID, index); err != nil {
					return err
				}
			}
			if err := resolve(ctx, db, gameID); err != nil {
				return err
			}
		}
		for _, seat := range bout.Seats {
			side, err := slotOf(db, matchID, registry[seat.Name])
			if err != nil {
				return fmt.Errorf("%s, %s: %w", bout.At, seat.Name, err)
			}
			var ops []edit.PatchOp
			for theme, questions := range seat.Counts {
				for question, taken := range questions {
					for chair := 0; chair < taken; chair++ {
						ops = append(ops, edit.PatchOp{
							Path:  pointer("sides", side, "themes", theme, "answers", question, chair),
							Value: json.RawMessage(`"right"`),
						})
					}
				}
			}
			if len(ops) == 0 {
				continue
			}
			if err := inTx(db, func(tx *sql.Tx) error {
				return editbatch.PatchMatchTx(ctx, tx, scope, matchID, ops)
			}); err != nil {
				return fmt.Errorf("%s, %s: %w", bout.At, seat.Name, err)
			}
		}
		if err := inTx(db, func(tx *sql.Tx) error {
			if err := editbatch.FinishMatchTx(ctx, tx, matchID, true); err != nil {
				return err
			}
			_, err := editbatch.RecomputeMatchTx(ctx, tx, scope, matchID, true, "", "")
			return err
		}); err != nil {
			return fmt.Errorf("%s: доигрыш: %w", bout.At, err)
		}
		if err := resolve(ctx, db, gameID); err != nil {
			return err
		}
	}
	log.Printf("троечка: сыграна")
	return nil
}

// pointer writes a patch path the way the page does: each segment its own JSON
// value, so a number stays an array index and a name stays an object key.
func pointer(parts ...any) []json.RawMessage {
	out := make([]json.RawMessage, len(parts))
	for i, part := range parts {
		raw, err := json.Marshal(part)
		if err != nil {
			panic(err)
		}
		out[i] = raw
	}
	return out
}

func matchAt(db *sql.DB, gameID int64, at replay.Coord) (int64, error) {
	var id int64
	err := db.QueryRow(`
select m.id from matches m
join stages s on s.id = m.stage_id
where s.game_id = ? and s.block_code = ? and s.group_code = ?
  and coalesce(nullif(m.wave, 0), s.wave_index) = ? and m.round = ?
order by s.position, m.position
limit 1 offset ?`, gameID, at.Block, at.Group, at.Wave, at.Round, at.Match-1).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("нет боя по координате")
	}
	return id, err
}

func slotOf(db *sql.DB, matchID, participantID int64) (int, error) {
	var side int
	err := db.QueryRow(`
select slot_index from match_slots where match_id = ? and participant_id = ?`,
		matchID, participantID).Scan(&side)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("не сидит в этом бою")
	}
	return side, err
}

func resolve(ctx context.Context, db *sql.DB, gameID int64) error {
	return inTx(db, func(tx *sql.Tx) error {
		_, err := resolver.ResolveGameSlotsAndReseedsTx(ctx, tx, gameID)
		return err
	})
}

func inTx(db *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}
