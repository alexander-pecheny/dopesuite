// Playing the fixture: every Protocol's document written to the same
// arithmetic pattern, then scored and resolved the way a live edit is, so the
// standings, the reseeds and the later rounds are dope's own work rather than
// numbers typed into rows.
package fixture

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"

	"dope/dope/domain/flatgame"
	"dope/dope/domain/gamebuild"
	"dope/dope/domain/games"
	"dope/dope/domain/protocol"
	"dope/dope/domain/resolver"
	"dope/dope/domain/scoring"
	"dope/dope/storage/store"
)

//go:embed data/multi.txt
var multiSpec string

// playBracket fills a bracket game round by round: a round's matches are
// played and scored, and the resolver seats whoever that sends on — which is
// the only way the later rounds get anybody in them.
func playBracket(ctx context.Context, tx *sql.Tx, festID, gameID int64, gameType string) error {
	for pass := 0; ; pass++ {
		if _, err := resolver.ResolveGameSlotsAndReseedsTx(ctx, tx, gameID); err != nil {
			return err
		}
		matches, err := store.LoadMatchStates(ctx, tx, store.MatchSelector{FestID: festID, GameID: gameID})
		if err != nil {
			return err
		}
		played := 0
		for _, match := range matches {
			if match.Status == "finished" || !seated(match) {
				continue
			}
			if err := playMatch(ctx, tx, festID, match); err != nil {
				return fmt.Errorf("%s: %w", match.Code, err)
			}
			played++
		}
		if played == 0 {
			return nil
		}
		// A bracket is finite; the guard is against a scheme whose slots never
		// resolve, which would otherwise spin here forever.
		if pass > len(matches) {
			return fmt.Errorf("fixture: %s never ran out of matches to play", gameType)
		}
	}
}

// seated reports whether every slot of a match has somebody in it — an
// unresolved later round is played on the next pass, not now.
func seated(match store.DBMatchState) bool {
	if len(match.ParticipantIDs) == 0 {
		return false
	}
	for _, id := range match.ParticipantIDs {
		if id == 0 {
			return false
		}
	}
	return true
}

// playMatch writes one match's document, marks it finished and scores it. A
// finished match is what a Block ranks and a reseed reads, so a fixture of
// active matches would show empty standings everywhere.
func playMatch(ctx context.Context, tx *sql.Tx, festID int64, match store.DBMatchState) error {
	state, err := matchDocument(match)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
update matches set state_json = ?, status = 'finished', revision = revision + 1 where id = ?`,
		state, match.MatchID); err != nil {
		return err
	}
	fresh, err := store.LoadMatchState(ctx, tx, store.MatchSelector{FestID: festID, MatchID: match.MatchID})
	if err != nil {
		return err
	}
	if match.GameType == games.EK {
		// EK's places are the host's, not the scorer's (protocol/ek.go): without
		// a pin every seat places 0, and a bracket whose next round asks for
		// «place 1 of this bout» never seats anybody.
		if state, err = pinnedPlaces(fresh); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `update matches set state_json = ? where id = ?`, state, match.MatchID); err != nil {
			return err
		}
		if fresh, err = store.LoadMatchState(ctx, tx, store.MatchSelector{FestID: festID, MatchID: match.MatchID}); err != nil {
			return err
		}
	}
	return scoring.RecalculateMatchResultsTx(ctx, tx, fresh)
}

// pinnedPlaces ranks a played EK match by the totals it was just dealt and
// writes them into the blob as the host's places, so the sheet agrees with
// itself and the next round seats the teams that actually won.
func pinnedPlaces(match store.DBMatchState) (string, error) {
	p, ok := protocol.Get(match.GameType)
	if !ok {
		return "", fmt.Errorf("fixture: no protocol %q", match.GameType)
	}
	outcomes, err := p.Score(nil, match.ProtocolState())
	if err != nil {
		return "", err
	}
	type seat struct {
		id          int64
		total, plus float64
	}
	var seats []seat
	for index, id := range match.ParticipantIDs {
		if id == 0 || index >= len(outcomes) {
			continue
		}
		seats = append(seats, seat{id: id, total: outcomes[index].Metrics["total"], plus: outcomes[index].Metrics["plus"]})
	}
	// Ties are broken all the way down to the id: a fixture needs one order,
	// not a shared place the bracket cannot resolve.
	sort.Slice(seats, func(i, j int) bool {
		if seats[i].total != seats[j].total {
			return seats[i].total > seats[j].total
		}
		if seats[i].plus != seats[j].plus {
			return seats[i].plus > seats[j].plus
		}
		return seats[i].id < seats[j].id
	})
	blob := match.Blob
	for index, s := range seats {
		place := float64(index + 1)
		blob.SetPin(s.id, &place)
	}
	return blob.JSON()
}

// matchDocument builds one match's filled Protocol document. Each Protocol
// owns its shape — a brain match is two rows of questions, a troika match two
// sides of themes by chairs, a team-blob match a section per Participant — so
// the pattern is dealt into whichever the match keeps.
// A document is dealt from WHO is in the match, never from where they sit: the
// groups of a scheme are structurally identical, so a pattern keyed on the slot
// gives every group the same winner with the same score — and four winners tied
// on every metric are separated by a random draw (structure), which is exactly
// what a golden cannot survive.
func matchDocument(match store.DBMatchState) (string, error) {
	switch match.GameType {
	case games.Brain:
		return brainDocument(match)
	case games.Troika:
		return troikaDocument(match)
	default:
		return blobDocument(match)
	}
}

// defaultBlobThemes is what a blob match whose stage named no theme count
// plays: EK's four.
const defaultBlobThemes = 4

// blobDocument fills the team-keyed blob (EK and individual SI): a section per
// Participant, themes of five answers each.
func blobDocument(match store.DBMatchState) (string, error) {
	blob, err := store.ParseMatchBlob(match.RawState)
	if err != nil {
		return "", err
	}
	themes := match.Themes
	if themes <= 0 {
		themes = defaultBlobThemes
	}
	for _, participantID := range match.ParticipantIDs {
		if participantID == 0 {
			continue
		}
		for theme := 0; theme < themes; theme++ {
			for answer := range store.QuestionValues {
				if m := mark(int(participantID), int(participantID), theme, answer); m != "" {
					blob.SetAnswer(participantID, "themes", theme, answer, m)
				}
			}
		}
	}
	return blob.JSON()
}

func brainDocument(match store.DBMatchState) (string, error) {
	var state games.BrainState
	if err := json.Unmarshal([]byte(match.RawState), &state); err != nil {
		return "", err
	}
	if len(state.Teams) != 2 || len(match.ParticipantIDs) < 2 {
		return marshal(state)
	}
	seed := int(match.ParticipantIDs[0] + match.ParticipantIDs[1])
	taken := [2]int{}
	for row := range state.Teams[0].Rows {
		// A buzzer question goes to whichever side wants it more on this
		// question, or to neither: both sides cannot answer the same one.
		reach := func(side int) int { return (int(match.ParticipantIDs[side])*37 + row*11) % 23 }
		a, b := reach(0), reach(1)
		switch {
		case a > b+3:
			state.Teams[0].Rows[row].Mark = "right"
			taken[0]++
		case b > a+3:
			state.Teams[1].Rows[row].Mark = "right"
			taken[1]++
		case a == b:
			state.Teams[row%2].Rows[row].Mark = "wrong"
		}
	}
	// A drawn bout sends nobody on, and a bracket whose semifinals are drawn
	// leaves its final empty — so the fixture breaks the tie rather than
	// shipping a page with two unplayed matches on it. The winner is the seed's
	// to pick, and it takes the first question nobody claimed.
	if taken[0] == taken[1] {
		winner := seed % 2
		for row := range state.Teams[winner].Rows {
			if state.Teams[0].Rows[row].Mark == "" && state.Teams[1].Rows[row].Mark == "" {
				state.Teams[winner].Rows[row].Mark = "right"
				taken[winner]++
				break
			}
		}
	}
	if taken[0] == taken[1] {
		// Every question was claimed: the loser gives one back instead.
		loser := 1 - seed%2
		for row := range state.Teams[loser].Rows {
			if state.Teams[loser].Rows[row].Mark == "right" {
				state.Teams[loser].Rows[row].Mark = ""
				break
			}
		}
	}
	return marshal(state)
}

func troikaDocument(match store.DBMatchState) (string, error) {
	var state games.TroikaState
	if err := json.Unmarshal([]byte(match.RawState), &state); err != nil {
		return "", err
	}
	for side := range state.Sides {
		seat := 0
		if side < len(match.ParticipantIDs) {
			seat = int(match.ParticipantIDs[side])
		}
		for theme := range state.Sides[side].Themes {
			answers := state.Sides[side].Themes[theme].Answers
			for question := range answers {
				for chair := range answers[question] {
					answers[question][chair] = mark(seat, seat, theme*3+question, chair)
				}
			}
		}
	}
	// A troika theme is worth the same to both sides, so more correct answers
	// is a higher score — but two sides can still land on the same total, and a
	// drawn bout sends nobody on. The side the seed favours takes one more
	// answer until it leads.
	if len(state.Sides) == 2 && len(match.ParticipantIDs) >= 2 {
		winner := 0
		if match.ParticipantIDs[1] > match.ParticipantIDs[0] {
			winner = 1
		}
		for range troikaNudges {
			ahead, err := troikaLeads(state, winner)
			if err != nil {
				return "", err
			}
			if ahead || !troikaTakeOne(&state, winner) {
				break
			}
		}
	}
	return marshal(state)
}

// troikaNudges bounds the tie-break loop; a bout has far fewer cells than this.
const troikaNudges = 200

// troikaLeads asks the scorer itself whether the side is ahead, rather than
// re-deriving what a theme is worth.
func troikaLeads(state games.TroikaState, side int) (bool, error) {
	raw, err := json.Marshal(state)
	if err != nil {
		return false, err
	}
	results, err := games.ComputeTroikaResults(string(raw))
	if err != nil {
		return false, err
	}
	if len(results) != 2 {
		return true, nil
	}
	return results[side].Total > results[1-side].Total, nil
}

// troikaTakeOne turns one more of a side's cells right, reporting whether it
// found one to turn.
func troikaTakeOne(state *games.TroikaState, side int) bool {
	for theme := range state.Sides[side].Themes {
		answers := state.Sides[side].Themes[theme].Answers
		for question := range answers {
			for chair := range answers[question] {
				if answers[question][chair] != "right" {
					answers[question][chair] = "right"
					return true
				}
			}
		}
	}
	return false
}

func marshal(value any) (string, error) {
	raw, err := json.Marshal(value)
	return string(raw), err
}

// buildOD writes the one OD document: per question, which teams took it.
func buildOD(ctx context.Context, tx *sql.Tx, festID int64) error {
	gameID, err := createFlat(ctx, tx, festID, gamebuild.Spec{
		Type: games.OD, ODTours: odTours, ODQuestions: odQuestions,
	})
	if err != nil {
		return fmt.Errorf("fixture od: %w", err)
	}
	state, err := flatDocument(ctx, tx, gameID)
	if err != nil {
		return err
	}
	numbers, err := odTeamNumbers(state)
	if err != nil {
		return err
	}
	questions := odTours * odQuestions
	entries := make([][]int, questions)
	for q := range entries {
		entries[q] = []int{}
		for index, number := range numbers {
			if mark(number, index, q, 0) == "right" {
				entries[q] = append(entries[q], number)
			}
		}
	}
	completed := make([]bool, questions)
	for i := range completed {
		completed[i] = true
	}
	state["entries"] = entries
	state["completed"] = completed
	return writeFlat(ctx, tx, festID, gameID, state)
}

// odTeamNumbers is the numbers OD's entries key on, in document order.
func odTeamNumbers(state map[string]any) ([]int, error) {
	raw, err := json.Marshal(state["teams"])
	if err != nil {
		return nil, err
	}
	var teams []struct {
		Number int `json:"number"`
	}
	if err := json.Unmarshal(raw, &teams); err != nil {
		return nil, err
	}
	out := make([]int, len(teams))
	for i, team := range teams {
		out[i] = team.Number
	}
	return out, nil
}

func buildKSI(ctx context.Context, tx *sql.Tx, festID int64) error {
	return ksiGame(ctx, tx, festID, ksiThemes, nil)
}

func buildStickerKSI(ctx context.Context, tx *sql.Tx, festID int64) error {
	config, err := json.Marshal(stickerConfig())
	if err != nil {
		return err
	}
	return ksiGame(ctx, tx, festID, stickerThemes, config)
}

// ksiLabel names the two KSI games apart: without it the second takes the
// first's title with a numeric suffix, and a golden called a suffixed title says
// nothing about which variant it shows.

// ksiGame writes one KSI document: the answer grid, two refusals, and — for
// the stickers variant — the sticker each team spent on each theme.
func ksiGame(ctx context.Context, tx *sql.Tx, festID int64, themes int, stickers json.RawMessage) error {
	spec := gamebuild.Spec{Type: games.KSI, KSIThemes: themes, KSIStickers: stickers}
	if len(stickers) > 0 {
		spec.Label = stickerGameLabel()
	}
	gameID, err := createFlat(ctx, tx, festID, spec)
	if err != nil {
		return fmt.Errorf("fixture ksi: %w", err)
	}
	state, err := flatDocument(ctx, tx, gameID)
	if err != nil {
		return err
	}
	seats, err := ksiSeats(state)
	if err != nil {
		return err
	}
	grid := make([]map[string]any, themes)
	stickerGrid := make([][]string, themes)
	for theme := range grid {
		rows := make([][]string, len(seats))
		stickerGrid[theme] = make([]string, len(seats))
		for team := range rows {
			row := make([]string, len(store.QuestionValues))
			for q := range row {
				row[q] = mark(team+1, team, theme, q)
			}
			rows[team] = row
			stickerGrid[theme][team] = sticker(team, theme)
		}
		grid[theme] = map[string]any{"answers": rows}
	}
	// Two refusals, so the tab has something to show and the ranking somebody
	// to leave unplaced.
	declined := map[string]bool{}
	for _, seat := range []int{1, 4} {
		if seat < len(seats) {
			declined[games.KSIDeclinedKey(seats[seat].Number, seats[seat].Name)] = true
		}
	}
	state["themes"] = grid
	state["declined"] = declined
	if len(stickers) > 0 {
		state["stickers"] = stickerGrid
	}
	return writeFlat(ctx, tx, festID, gameID, state)
}

func ksiSeats(state map[string]any) ([]games.KSIParticipant, error) {
	raw, err := json.Marshal(state["participants"])
	if err != nil {
		return nil, err
	}
	return games.ParseKSIParticipants(raw), nil
}

// sticker deals one cell of the sticker grid within the maxima stickerConfig
// declares: every team spends its two ×2 and its two one-offs on different
// themes and takes the plain one for the rest.
func sticker(team, theme int) string {
	switch (theme - team%5 + 60) % 12 {
	case 0, 1:
		return games.KSIStickerX2
	case 2:
		return games.KSIStickerNoWrong
	case 3:
		return games.KSIStickerEmptyWrong
	default:
		return games.KSIStickerNeutral
	}
}

// stickerConfig is the scheme block of the stickers variant, as the creation
// form writes it. Its labels come from the Catalog, like the form's.
func stickerConfig() games.KSIStickerConfig {
	s := stickerLabels()
	max := func(n int) *int { return &n }
	return games.KSIStickerConfig{Types: []games.KSIStickerType{
		{ID: games.KSIStickerNeutral, Label: s.neutral, Color: "#ffffff", Max: max(stickerThemes)},
		{ID: games.KSIStickerX2, Label: "×2", Color: "#fdf66f", Max: max(2)},
		{ID: games.KSIStickerNoWrong, Label: s.noWrong, Color: "#aded87", Max: max(1)},
		{ID: games.KSIStickerEmptyWrong, Label: s.emptyWrong, Color: "#ff7a6b", Max: max(1)},
	}}
}

// buildMulti writes the one multi document: a cell grid per minigame.
func buildMulti(ctx context.Context, tx *sql.Tx, festID int64) error {
	minigames, err := games.ParseMultiGames(multiSpec)
	if err != nil {
		return fmt.Errorf("fixture multi: %w", err)
	}
	gameID, err := createFlat(ctx, tx, festID, gamebuild.Spec{Type: games.Multi, Minigames: minigames})
	if err != nil {
		return fmt.Errorf("fixture multi: %w", err)
	}
	state, err := flatDocument(ctx, tx, gameID)
	if err != nil {
		return err
	}
	seats, err := ksiSeats(state)
	if err != nil {
		return err
	}
	grids := make([]map[string]any, len(minigames))
	for index, game := range minigames {
		cells := make([][]int, len(seats))
		for team := range cells {
			cells[team] = make([]int, len(game.Columns))
			for column := range cells[team] {
				// A minigame task is scored, not marked, so the mark is
				// spent on the task's own scale: its best, its worst, or
				// the middle. The totals then inherit mark's ordering.
				values := game.Columns[column].Values
				if mark(team+1, team, index, column) == "right" {
					cells[team][column] = game.Columns[column].Max()
				} else {
					cells[team][column] = values[0]
				}
			}
		}
		grids[index] = map[string]any{"cells": cells}
	}
	state["games"] = grids
	return writeFlat(ctx, tx, festID, gameID, state)
}

// createFlat builds one of the formats that seat the fest's roster themselves.
func createFlat(ctx context.Context, tx *sql.Tx, festID int64, spec gamebuild.Spec) (int64, error) {
	spec.FestID = festID
	if spec.Label == "" {
		spec.Label = games.Label(spec.Type)
	}
	return createGame(ctx, tx, spec)
}

// flatDocument is the game's one document as it stands, decoded.
func flatDocument(ctx context.Context, tx *sql.Tx, gameID int64) (map[string]any, error) {
	var raw string
	if err := tx.QueryRowContext(ctx, `
select coalesce(m.state_json, '{}') from matches m where m.game_id = ? and m.code = 'main'`,
		gameID).Scan(&raw); err != nil {
		return nil, err
	}
	state := map[string]any{}
	return state, json.Unmarshal([]byte(raw), &state)
}

// writeFlat stores a flat game's document the way an edit does — seats, score,
// ranking — rather than as a row update nothing settles.
func writeFlat(ctx context.Context, tx *sql.Tx, festID, gameID int64, state map[string]any) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return flatgame.SetStateTx(ctx, tx, festID, gameID, string(raw))
}
