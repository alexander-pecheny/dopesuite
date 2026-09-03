// Who sits where: a Game's entrants, numbered from 1 (ADR-0009), and the
// seater that turns a scheme's seed refs into participant ids when the
// Structure is written.
package gamebuild

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strconv"

	"dope/dope/domain/games"
	"dope/dope/domain/imports"
	"dope/dope/domain/roster"
	"dope/dope/storage/store"
	dopestrings "dope/i18nstrings"
	corei18n "pecheny.me/dopecore/i18nstrings"
)

// hasAssignmentsTx reports whether the Game's seats are already claimed.
func hasAssignmentsTx(ctx context.Context, tx *sql.Tx, gameID int64) (bool, error) {
	var count int
	if err := tx.QueryRowContext(ctx, `
select count(*) from game_assignments where game_id = ?`, gameID).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

// seatChosenTx numbers the Game's chosen Participants from 1, in the order
// given. It runs before the Structure is built, so the Slots resolve against
// these numbers rather than against the fest's.
func seatChosenTx(ctx context.Context, tx *sql.Tx, gameID int64, entrants []int64) error {
	for i, participantID := range entrants {
		if _, err := tx.ExecContext(ctx, `
insert into game_assignments(game_id, basket, number, participant_id) values(?, 1, ?, ?)
on conflict(game_id, basket, number) do update set participant_id = excluded.participant_id`,
			gameID, i+1, participantID); err != nil {
			return err
		}
	}
	return nil
}

// recordGameEntrantsTx writes who plays this Game, in seed order and under the
// number the Game deals them. It reads back the seating rather than the list it
// was given, so the entrant list can never claim somebody the Structure did not
// seat. A team knocked out before its first Match is still visibly an entrant,
// which is the point of keeping the list at all.
func recordGameEntrantsTx(ctx context.Context, tx *sql.Tx, gameID int64) error {
	rows, err := tx.QueryContext(ctx, `
select participant_id, number from game_assignments
where game_id = ? and basket = 1 and participant_id is not null order by number`, gameID)
	if err != nil {
		return err
	}
	type entry struct {
		id     int64
		number int
	}
	var seated []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.id, &e.number); err != nil {
			rows.Close()
			return err
		}
		seated = append(seated, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for position, e := range seated {
		if _, err := tx.ExecContext(ctx, `
insert into game_participants(game_id, participant_id, position, number) values(?, ?, ?, ?)
on conflict(game_id, participant_id) do update set position = excluded.position, number = excluded.number`,
			gameID, e.id, position+1, e.number); err != nil {
			return err
		}
	}
	return nil
}

// gameEntrantsTx is who this Game seats, in its own seed order — empty for a
// Game created before Games could name their entrants, which then reads the
// fest's registry as it always did.
func gameEntrantsTx(ctx context.Context, tx *sql.Tx, gameID int64) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, `
select participant_id from game_participants where game_id = ? order by position`, gameID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// chosenEntrantsTx turns the Game's chosen Participants into scheme entrants,
// numbered from 1 in the order given. Without a choice the whole fest plays.
func chosenEntrantsTx(ctx context.Context, tx *sql.Tx, festID int64, gameType string, chosen []int64) ([]store.SchemeSlot, error) {
	if len(chosen) == 0 {
		return seedEntrantsTx(ctx, tx, festID, gameType)
	}
	// A team format seats teams and an individual one players, so a chosen
	// Participant of the other kind is a mistake worth naming rather than a
	// seat left empty at the venue.
	want := "team"
	if games.IsIndividual(gameType) {
		want = "player"
	}
	entrants := make([]store.SchemeSlot, len(chosen))
	for i, participantID := range chosen {
		var name, roster string
		if err := tx.QueryRowContext(ctx, `
select name, roster from participants where id = ? and fest_id = ?`, participantID, festID).Scan(&name, &roster); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, corei18n.User(dopestrings.Default.Gamebuild.Seating.UnknownParticipant(strconv.FormatInt(participantID, 10)))
			}
			return nil, err
		}
		if roster != want {
			if want == "player" {
				return nil, corei18n.User(dopestrings.Default.Gamebuild.Seating.KindTeam(name))
			}
			return nil, corei18n.User(dopestrings.Default.Gamebuild.Seating.KindPlayer(name))
		}
		entrants[i] = store.SchemeSlot{Seed: &store.SchemeSeedRef{Basket: 1, Number: i + 1}, Label: name}
	}
	return entrants, nil
}

// seedEntrantsTx is the fest's own roster as scheme entrants: teams in a team
// format, players in an individual one. A Participant is whoever the format
// seats, and the seeding is where that first shows up.
func seedEntrantsTx(ctx context.Context, tx *sql.Tx, festID int64, gameType string) ([]store.SchemeSlot, error) {
	if games.IsIndividual(gameType) {
		return seedPlayerEntrantsTx(ctx, tx, festID)
	}
	teams, err := roster.LoadFestRosterImportTeamsTx(ctx, tx, festID)
	if err != nil {
		return nil, err
	}
	if len(teams) < 2 {
		return nil, corei18n.User(dopestrings.Default.Gamebuild.Seating.NeedTwo())
	}
	sort.Slice(teams, func(i, j int) bool { return teams[i].Number < teams[j].Number })
	entrants := make([]store.SchemeSlot, len(teams))
	for i, team := range teams {
		if team.Number <= 0 {
			return nil, corei18n.User(dopestrings.Default.Gamebuild.Seating.Unnumbered())
		}
		entrants[i] = store.SchemeSlot{Seed: &store.SchemeSeedRef{Basket: 1, Number: int(team.Number)}, Label: team.Name}
	}
	return entrants, nil
}

// seedSeaterTx builds the seat lookup a recompile reuses: fest teams by
// number (roster-seeded games) plus the seed-import ladder's assignments
// (declared-seed games).
func seedSeaterTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, gameType string) (func(slot store.SchemeSlot) any, error) {
	// A Game numbers the Participants it seats, and the assignment rows carry
	// that numbering (ADR-0009). Reading them first is what lets one fest hold
	// an EK of 48 and a Brain of a different 48.
	byNumber := map[int]int64{}
	rows, err := tx.QueryContext(ctx, `
select number, participant_id from game_assignments
where game_id = ? and basket = 1 and participant_id is not null`, gameID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var number int
		var participantID int64
		if err := rows.Scan(&number, &participantID); err != nil {
			rows.Close()
			return nil, err
		}
		byNumber[number] = participantID
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if !games.IsIndividual(gameType) {
		// A team Game that never named its entrants seats the fest's registry by
		// its registration numbers, as every game did before Games could differ.
		teams, err := roster.LoadFestRosterImportTeamsTx(ctx, tx, festID)
		if err != nil {
			return nil, err
		}
		for _, team := range teams {
			if team.Number <= 0 {
				continue
			}
			if _, taken := byNumber[int(team.Number)]; taken {
				continue
			}
			teamID, _, err := imports.EnsureSeedTeamByNumber(ctx, tx, festID, team.Number, team.Name, team.City, nil)
			if err != nil {
				return nil, err
			}
			byNumber[int(team.Number)] = teamID
		}
	}
	assignments := map[[2]int]int64{}
	rows, err = tx.QueryContext(ctx, `select basket, number, participant_id from game_assignments where game_id = ?`, gameID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var basket, number int
		var teamID int64
		if err := rows.Scan(&basket, &number, &teamID); err != nil {
			return nil, err
		}
		assignments[[2]int{basket, number}] = teamID
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return func(slot store.SchemeSlot) any {
		if slot.Seed == nil {
			return nil
		}
		// Number-refs seat by team number only; Position-refs by the seed
		// ladder only — a number missing from the roster must NOT fall through
		// to the rank-keyed assignments (15 the team ≠ 15 the seed rank).
		if slot.Seed.Number > 0 {
			if id, ok := byNumber[slot.Seed.Number]; ok {
				return id
			}
			return nil
		}
		if slot.Seed.Position > 0 {
			basket := slot.Seed.Basket
			if basket <= 0 {
				basket = 1
			}
			if id, ok := assignments[[2]int{basket, slot.Seed.Position}]; ok {
				return id
			}
		}
		return nil
	}, nil
}

func insertMatchSlots(ctx context.Context, tx *sql.Tx, matchID int64, slots []store.SchemeSlot, seat func(store.SchemeSlot) any) error {
	for slotIndex, slot := range slots {
		ref := store.SlotRefOf(slot)
		if _, err := tx.ExecContext(ctx, `
insert into match_slots(match_id, slot_index, source_type, source_ref_json, participant_id, locked)
values(?, ?, ?, ?, ?, 0)`, matchID, slotIndex, ref.Type, ref.JSON(), seat(slot)); err != nil {
			return err
		}
	}
	return nil
}

// seedPlayerEntrantsTx lists the fest's players as entrants, in the order they
// were registered — that order IS the seeding, the way a fest's registration
// list is. They are numbered here rather than in the roster because a fest
// numbers its teams; an individual game numbers the players it seats.
func seedPlayerEntrantsTx(ctx context.Context, tx *sql.Tx, festID int64) ([]store.SchemeSlot, error) {
	rows, err := tx.QueryContext(ctx, `
select p.id, trim(p.first_name || ' ' || p.last_name)
from fest_players p where p.fest_id = ?
order by p.id`, festID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entrants []store.SchemeSlot
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		entrants = append(entrants, store.SchemeSlot{
			Seed:  &store.SchemeSeedRef{Basket: 1, Number: len(entrants) + 1},
			Label: name,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(entrants) < 3 {
		return nil, corei18n.User(dopestrings.Default.Gamebuild.Seating.NeedPlayers())
	}
	return entrants, nil
}

// seatRosterTx pre-fills a game's seed assignments from the fest roster —
// teams in a team format, players in an individual one, each becoming a
// Participant of the matching kind.
func seatRosterTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, gameType string) error {
	assign := func(number, participantID int64) error {
		_, err := tx.ExecContext(ctx, `
insert into game_assignments(game_id, basket, number, participant_id) values(?, 1, ?, ?)
on conflict(game_id, basket, number) do update set participant_id = excluded.participant_id`,
			gameID, number, participantID)
		return err
	}
	if games.IsIndividual(gameType) {
		rows, err := tx.QueryContext(ctx, `
select p.id, trim(p.first_name || ' ' || p.last_name)
from fest_players p where p.fest_id = ?
order by p.id`, festID)
		if err != nil {
			return err
		}
		type entry struct {
			id   int64
			name string
		}
		var players []entry
		for rows.Next() {
			var e entry
			if err := rows.Scan(&e.id, &e.name); err != nil {
				rows.Close()
				return err
			}
			players = append(players, e)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for i, player := range players {
			number := int64(i + 1)
			participantID, err := imports.EnsureSeedPlayerByNumber(ctx, tx, festID, number, player.name, player.id)
			if err != nil {
				return err
			}
			if err := assign(number, participantID); err != nil {
				return err
			}
		}
		return nil
	}
	teams, err := roster.LoadFestRosterImportTeamsTx(ctx, tx, festID)
	if err != nil {
		return err
	}
	for _, team := range teams {
		teamID, _, err := imports.EnsureSeedTeamByNumber(ctx, tx, festID, team.Number, team.Name, team.City, nil)
		if err != nil {
			return err
		}
		if err := assign(team.Number, teamID); err != nil {
			return err
		}
	}
	return nil
}
