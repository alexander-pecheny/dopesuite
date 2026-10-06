// Who sits where: a Game's entrants, numbered from 1 (ADR-0009), and the
// seater that turns a scheme's seed refs into participant ids when the
// Structure is written.
package gamebuild

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"

	"dope/dope/domain/imports"
	"dope/dope/domain/schemedsl"
	"dope/dope/storage/store"
	dopestrings "dope/i18nstrings"

	corei18n "pecheny.me/dopecore/i18nstrings"
	"pecheny.me/dopecore/idstr"
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
// basketEntry is a Participant seated in the first basket, with its number.
type basketEntry struct {
	id     int64
	number int
}

// firstBasketTx lists the Game's first-basket Participants by number.
func firstBasketTx(ctx context.Context, tx *sql.Tx, gameID int64) ([]basketEntry, error) {
	rows, err := tx.QueryContext(ctx, `
select participant_id, number from game_assignments
where game_id = ? and basket = 1 and participant_id is not null order by number`, gameID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var seated []basketEntry
	for rows.Next() {
		var e basketEntry
		if err := rows.Scan(&e.id, &e.number); err != nil {
			return nil, err
		}
		seated = append(seated, e)
	}
	return seated, rows.Err()
}

func recordGameEntrantsTx(ctx context.Context, tx *sql.Tx, gameID int64) error {
	seated, err := firstBasketTx(ctx, tx, gameID)
	if err != nil {
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
// numbered from 1 in the order given, or under numbers when it is given. Nobody chosen is the format's default
// entrants (imports.DefaultEntrants): the troikas of a format that seats
// them, or empty seats while there are none; otherwise the whole fest plays.
func chosenEntrantsTx(ctx context.Context, tx *sql.Tx, festID int64, gameType string, declared imports.Declared, doc *schemedsl.Doc, chosen []int64, numbers []int) ([]store.SchemeSlot, error) {
	kind := imports.KindOf(gameType)
	if len(chosen) == 0 {
		if kind != imports.KindTroika {
			return seedEntrantsTx(ctx, tx, festID, kind)
		}
		troikas, err := defaultTroikasTx(ctx, tx, festID, gameType, declared)
		if err != nil {
			return nil, err
		}
		if len(troikas) == 0 {
			return emptySeats(placeholderSeats(doc)), nil
		}
		chosen = troikas
	}
	// A team format seats teams and an individual one players, so a chosen
	// Participant of the other kind is a mistake worth naming rather than a
	// seat left empty at the venue.
	want := imports.ParticipantRoster(kind)
	entrants := make([]store.SchemeSlot, len(chosen))
	for i, participantID := range chosen {
		var name, roster string
		if err := tx.QueryRowContext(ctx, `
select name, roster from participants where id = ? and fest_id = ?`, participantID, festID).Scan(&name, &roster); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, corei18n.User(dopestrings.Default.Gamebuild.Seating.UnknownParticipant(idstr.Format(participantID)))
			}
			return nil, err
		}
		if roster != want {
			if want == "player" {
				return nil, corei18n.User(dopestrings.Default.Gamebuild.Seating.KindTeam(name))
			}
			return nil, corei18n.User(dopestrings.Default.Gamebuild.Seating.KindPlayer(name))
		}
		number := i + 1
		if len(numbers) == len(chosen) {
			number = numbers[i]
		}
		entrants[i] = store.SchemeSlot{Seed: &store.SchemeSeedRef{Basket: 1, Number: number}, Label: name}
	}
	return entrants, nil
}

// seatedNumbersTx is the number each entrant sits under in the Game now, in
// the order given, or nil when one of them has no seat or two share one. A
// Game that seated the whole fest numbers its teams by their fest numbers,
// and a fest whose numbers have gaps (1, 2, 5, 7) would otherwise be
// recompiled for 1…4 and lose the teams numbered above that.
func seatedNumbersTx(ctx context.Context, tx *sql.Tx, gameID int64, entrants []int64) ([]int, error) {
	seated, err := firstBasketTx(ctx, tx, gameID)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]int, len(seated))
	for _, e := range seated {
		byID[e.id] = e.number
	}
	numbers := make([]int, len(entrants))
	taken := map[int]bool{}
	for i, id := range entrants {
		number, ok := byID[id]
		if !ok || taken[number] {
			return nil, nil
		}
		numbers[i], taken[number] = number, true
	}
	return numbers, nil
}

// seedEntrantsTx is the fest's own roster as scheme entrants: teams by their
// fest numbers in a team format, players numbered in the order they
// registered in an individual one — that order IS the seeding, the way a
// fest's registration list is.
func seedEntrantsTx(ctx context.Context, tx *sql.Tx, festID int64, kind string) ([]store.SchemeSlot, error) {
	roster, err := imports.DefaultEntrants(ctx, tx, festID, kind, "", 0)
	if err != nil {
		return nil, err
	}
	if kind == imports.KindPlayer && len(roster) < minPlayers {
		return nil, corei18n.User(dopestrings.Default.Gamebuild.Seating.NeedPlayers())
	}
	if kind != imports.KindPlayer && len(roster) < minTeams {
		return nil, corei18n.User(dopestrings.Default.Gamebuild.Seating.NeedTwo())
	}
	entrants := make([]store.SchemeSlot, len(roster))
	for i, e := range roster {
		number := int(e.Number)
		if kind == imports.KindPlayer {
			number = i + 1
		} else if e.Number <= 0 {
			return nil, corei18n.User(dopestrings.Default.Gamebuild.Seating.Unnumbered())
		}
		entrants[i] = store.SchemeSlot{Seed: &store.SchemeSeedRef{Basket: 1, Number: number}, Label: e.Name}
	}
	return entrants, nil
}

// The fewest entrants a Game seating the whole fest is built for.
const (
	minTeams   = 2
	minPlayers = 3
)

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
	if imports.KindOf(gameType) == imports.KindTeam {
		// A team Game that never named its entrants seats the fest's registry by
		// its registration numbers, as every game did before Games could differ.
		// A Game of troikas never seats the fest's teams.
		teams, err := imports.DefaultEntrants(ctx, tx, festID, imports.KindTeam, "", 0)
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
	return insertMatchSlotsFrom(ctx, tx, matchID, slots, 0, seat)
}

// insertMatchSlotsFrom writes the slots from index from on: a bout that grows
// keeps the seats it has and takes the rest.
func insertMatchSlotsFrom(ctx context.Context, tx *sql.Tx, matchID int64, slots []store.SchemeSlot, from int, seat func(store.SchemeSlot) any) error {
	for slotIndex := from; slotIndex < len(slots); slotIndex++ {
		slot := slots[slotIndex]
		ref := store.SlotRefOf(slot)
		if _, err := tx.ExecContext(ctx, `
insert into match_slots(match_id, slot_index, source_type, source_ref_json, participant_id, locked)
values(?, ?, ?, ?, ?, 0)`, matchID, slotIndex, ref.Type, ref.JSON(), seat(slot)); err != nil {
			return err
		}
	}
	return nil
}

// seatRosterTx pre-fills a game's seed assignments from the fest roster —
// teams in a team format, players in an individual one, each becoming a
// Participant of the matching kind. A format that seats troikas is seated by
// its chosen entrants (seatChosenTx), never from the fest's teams.
func seatRosterTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, gameType string) error {
	assign := func(number, participantID int64) error {
		_, err := tx.ExecContext(ctx, `
insert into game_assignments(game_id, basket, number, participant_id) values(?, 1, ?, ?)
on conflict(game_id, basket, number) do update set participant_id = excluded.participant_id`,
			gameID, number, participantID)
		return err
	}
	kind := imports.KindOf(gameType)
	if kind == imports.KindTroika {
		return nil
	}
	roster, err := imports.DefaultEntrants(ctx, tx, festID, kind, "", 0)
	if err != nil {
		return err
	}
	for i, e := range roster {
		var participantID int64
		number := e.Number
		if kind == imports.KindPlayer {
			number = int64(i + 1)
			participantID, err = imports.EnsureSeedPlayerByNumber(ctx, tx, festID, number, e.Name, e.FestPlayerID)
		} else {
			participantID, _, err = imports.EnsureSeedTeamByNumber(ctx, tx, festID, e.Number, e.Name, e.City, nil)
		}
		if err != nil {
			return err
		}
		if err := assign(number, participantID); err != nil {
			return err
		}
	}
	return nil
}

// festPlayerRefPrefix marks an entrant ref naming a rating player rather than
// a Participant: the picker offers people no individual Game has seated yet.
const festPlayerRefPrefix = "fp"

// FestPlayerEntrantRef is the picker value for a rating player who is not a
// Participant yet.
func FestPlayerEntrantRef(festPlayerID int64) string {
	return festPlayerRefPrefix + idstr.Format(festPlayerID)
}

// ResolveEntrantRefsTx turns the picker's refs into Participant ids, in the
// order given. A Participant id passes through; "fp<id>" is a rating player,
// whose player Participant is found or minted here — under the number the
// whole-roster seating would give them (their rank among the fest's players),
// so a later Game seating everyone finds the same Participant, or under the
// next free number when that one belongs to someone else. Unparseable refs
// are skipped, as before.
func ResolveEntrantRefsTx(ctx context.Context, tx *sql.Tx, festID int64, refs []string) ([]int64, error) {
	var out []int64
	for _, ref := range refs {
		if raw, ok := strings.CutPrefix(ref, festPlayerRefPrefix); ok {
			festPlayerID, err := idstr.Parse(raw)
			if err != nil || festPlayerID <= 0 {
				continue
			}
			id, err := festPlayerParticipantTx(ctx, tx, festID, festPlayerID)
			if err != nil {
				return nil, err
			}
			out = append(out, id)
			continue
		}
		if id, err := idstr.Parse(ref); err == nil && id > 0 {
			out = append(out, id)
		}
	}
	return out, nil
}

func festPlayerParticipantTx(ctx context.Context, tx *sql.Tx, festID, festPlayerID int64) (int64, error) {
	var known int
	if err := tx.QueryRowContext(ctx, `select count(*) from fest_players where fest_id = ? and id = ?`, festID, festPlayerID).Scan(&known); err != nil {
		return 0, err
	}
	if known == 0 {
		return 0, corei18n.User(dopestrings.Default.Gamebuild.Seating.UnknownParticipant(FestPlayerEntrantRef(festPlayerID)))
	}
	// The same Participant a whole-roster seating finds or makes: the player's
	// own if they have one, else under their rank, never another person's.
	return imports.EnsurePlayerParticipantTx(ctx, tx, festID, festPlayerID)
}

// defaultTroikasTx is who a Game that seats troikas seats when nobody chose
// anyone (imports.DefaultEntrants): the troikas of its division, or every
// troika of the fest, in the order of applications. Another format, or a
// seeded Game, has none: its seats come from the roster or the seed.
func defaultTroikasTx(ctx context.Context, q store.Queryer, festID int64, gameType string, declared imports.Declared) ([]int64, error) {
	if imports.KindOf(gameType) != imports.KindTroika || declared.Seeded() {
		return nil, nil
	}
	division, _ := declared.EntrantDivision()
	troikas, err := imports.DefaultEntrants(ctx, q, festID, imports.KindTroika, division, 0)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(troikas))
	for i, t := range troikas {
		ids[i] = t.ParticipantID
	}
	return ids, nil
}

// createEntrantsTx is who a Game created from a DSL seats, given the
// entrants ticked on the form, and how many empty seats it is built with
// instead when there is nobody to seat yet.
//
// A Troika game that takes a division seats that division's troikas,
// whatever was ticked on the form. A division with none yet still gets its
// game: the Structure is built for as many empty seats as its first stage
// sends on, and the troikas fill it as they are entered
// (entrants.AddTroikasTx and the follow). A Troika game created with none ticked and no
// seed declared takes every troika of the fest, not the fest's teams.
func createEntrantsTx(ctx context.Context, tx *sql.Tx, festID int64, gameType string, declared imports.Declared, doc *schemedsl.Doc, ticked []int64) ([]int64, int, error) {
	_, divided := declared.EntrantDivision()
	if imports.KindOf(gameType) != imports.KindTroika || (!divided && len(ticked) > 0) {
		return ticked, 0, nil
	}
	troikas, err := defaultTroikasTx(ctx, tx, festID, gameType, declared)
	if err != nil {
		return nil, 0, err
	}
	switch {
	case declared.Seeded():
		return ticked, 0, nil
	case divided && len(troikas) == 0:
		return nil, placeholderSeats(doc), nil
	case !divided && len(troikas) < minTeams:
		return nil, 0, corei18n.User(dopestrings.Default.Gamebuild.Seating.NeedTroikas())
	}
	return troikas, 0, nil
}

// unrecordedEntrantsTx is who a recompile seats in a Game with no recorded
// Entrant list: the ones Clear would seat. A Game that seats troikas keeps the
// troikas its seats hold, in their order, and takes its default troikas after
// them (a Troika Game from before Games recorded their lists, which troikas joined
// since). seatRoster says a team or individual Game has no seats yet and takes
// the fest's roster, as on creation. A seeded Game takes neither: the seed
// owns its seats.
func unrecordedEntrantsTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, gameType string, declared imports.Declared) (entrants []int64, seatRoster bool, err error) {
	if declared.Seeded() {
		return nil, false, nil
	}
	if imports.KindOf(gameType) != imports.KindTroika {
		seated, err := hasAssignmentsTx(ctx, tx, gameID)
		return nil, !seated, err
	}
	seated, err := firstBasketTx(ctx, tx, gameID)
	if err != nil {
		return nil, false, err
	}
	defaults, err := defaultTroikasTx(ctx, tx, festID, gameType, declared)
	if err != nil {
		return nil, false, err
	}
	for _, e := range seated {
		entrants = append(entrants, e.id)
	}
	for _, id := range defaults {
		if !slices.Contains(entrants, id) {
			entrants = append(entrants, id)
		}
	}
	return entrants, false, nil
}
