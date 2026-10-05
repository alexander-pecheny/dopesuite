package replay

import (
	"encoding/json"
	"math"
	"sort"

	"pecheny.me/dopecore/idstr"

	"dope/dope/domain/games"
	"dope/dope/storage/store"
	dopestrings "dope/i18nstrings"
)

// Codec is how one Protocol's Matches read in a transcript and how its
// [statistics] section (statistika) is counted: the seat form, the three stat columns, and the
// aggregate over the finished Matches. One per game type, so neither the parser
// nor a Game adapter switches on a name.
type Codec struct {
	// Individual: the participant is the player — no lineups, no theme
	// players, no team column in the stats.
	Individual bool
	// Questions: the seat's middle field is the Match's questions (who buzzed
	// and how it went), not a grid of themes.
	Questions bool
	// Counts: the seat's middle field counts, per question, how many of the
	// team answered it — Troika's sheet, where all three answer every question
	// and each correct answer pays on its own. Which seat said what the
	// sheet does not record, so the count is all there is to transcribe.
	Counts bool
	// ThemeSize is how many questions a theme holds when a Count grid is read.
	ThemeSize int
	// Bet: the seat's marks may be followed by a bet token, the team
	// round's secret bet — Hamsa's fifth round has no nominal value, so the
	// amount is the only thing a sheet can record.
	Bet bool
	// ShootoutKey names where a shootout's themes sit in the document, and
	// ShootoutValues what its questions are worth there. Empty is EK's blob:
	// `shootoutThemes` on the 10..50 scale.
	ShootoutKey    string
	ShootoutValues func(state string) []int
	// What the sheet prints as the Match's Σ is the Protocol's to say
	// (store.ScoreMetric): brain counts the questions taken.
	Columns [3]string
	// Aggregate folds every finished Match into the sheet's per-player rows.
	Aggregate func(bouts []BoutState) ([]Stat, error)
}

// BoutState is one finished Match as an adapter hands it to a Codec: the
// Protocol document, the seated participants' names in slot order, and the
// names behind the ids the document keys by.
type BoutState struct {
	State   string
	Seated  []string
	Names   map[int64]string // participant id → name
	Players map[int64]string // player id → name, for a team game's theme players
}

var codecs = map[string]Codec{
	"ek": {Columns: [3]string{"Σ", "Σ+", dopestrings.Default.Replay.Codec.StatThemes()}, Aggregate: ekStats},
	// Erudit-Sextet's sheet is EK's: the same columns, the same aggregate —
	// a theme's Σ merely divides among the two or three who sat it.
	"es":    {Columns: [3]string{"Σ", "Σ+", dopestrings.Default.Replay.Codec.StatThemes()}, Aggregate: ekStats},
	"si":    {Individual: true, Columns: [3]string{"Σ", "Σ+", dopestrings.Default.Replay.Codec.StatBouts()}, Aggregate: individualStats},
	"brain": {Questions: true, Columns: [3]string{dopestrings.Default.Replay.Codec.StatAttempts(), dopestrings.Default.Replay.Codec.StatRight(), dopestrings.Default.Replay.Codec.StatWrong()}, Aggregate: brainStats},
	// Troika's sheet keeps no per-player row — it never records which seat
	// answered — so there is no stats section to hold dope to.
	"troika": {Counts: true, ThemeSize: games.TroikaThemeQuestions},
	// Hamsa reads like EK — a grid of themes and the player who sat for each —
	// with the team round's bet after them. Its shootout is another personal
	// round, so its questions are worth what that round paid.
	"hamsa": {Bet: true, ShootoutKey: "shootout", ShootoutValues: hamsaShootoutValues},
}

// hamsaShootoutValues reads the nominal values of a Hamsa bout's last game
// round out of its document — the scale a shootout theme is played on.
func hamsaShootoutValues(state string) []int {
	var doc struct {
		Rounds []struct {
			Values []int `json:"values"`
		} `json:"rounds"`
	}
	if err := json.Unmarshal([]byte(state), &doc); err != nil || len(doc.Rounds) == 0 {
		return nil
	}
	return doc.Rounds[len(doc.Rounds)-1].Values
}

// CodecFor is the codec of a game type; a game with none has no transcript form.
func CodecFor(game string) (Codec, bool) {
	codec, ok := codecs[game]
	return codec, ok
}

var nominals = store.QuestionValues

func statRows(acc map[[2]string]*[3]int) []Stat {
	out := make([]Stat, 0, len(acc))
	for key, values := range acc {
		out = append(out, Stat{Player: key[0], Team: key[1], Values: *values})
	}
	sort.Slice(out, func(a, b int) bool {
		return out[a].Player+"\x1f"+out[a].Team < out[b].Player+"\x1f"+out[b].Team
	})
	return out
}

func entryIn(acc map[[2]string]*[3]int, player, team string) *[3]int {
	key := [2]string{player, team}
	if acc[key] == nil {
		acc[key] = &[3]int{}
	}
	return acc[key]
}

// ekStats: per theme player, Σ, the themes he took positive, and the themes
// he played.
func ekStats(bouts []BoutState) ([]Stat, error) {
	acc := map[[2]string]*[3]int{}
	// A theme's Σ divides among whoever sat it — one player in EK, up to three
	// in Erudit-Sextet — so it is summed as a fraction and rounded once, at
	// the end; the themes taken and played are whole for each of them.
	sums := map[[2]string]float64{}
	for _, bout := range bouts {
		if err := ekBoutStats(bout, acc, sums); err != nil {
			return nil, err
		}
	}
	for key, sum := range sums {
		entryIn(acc, key[0], key[1])[0] = int(math.Round(sum))
	}
	return statRows(acc), nil
}

// ekBoutStats adds one bout's themes to acc, and each theme's share of its Σ
// to sums.
func ekBoutStats(bout BoutState, acc map[[2]string]*[3]int, sums map[[2]string]float64) error {
	var blob struct {
		Participants map[string]struct {
			Themes []store.BlobTheme `json:"themes"`
		} `json:"participants"`
	}
	if err := json.Unmarshal([]byte(bout.State), &blob); err != nil {
		return err
	}
	for pid, section := range blob.Participants {
		id, err := idstr.Parse(pid)
		if err != nil {
			return err
		}
		for _, theme := range section.Themes {
			addEKTheme(bout, bout.Names[id], theme, acc, sums)
		}
	}
	return nil
}

// addEKTheme counts one theme for each player who sat it.
func addEKTheme(bout BoutState, team string, theme store.BlobTheme, acc map[[2]string]*[3]int, sums map[[2]string]float64) {
	if len(theme.Players) == 0 {
		return
	}
	sum := themeSum(theme.Answers)
	share := float64(sum) / float64(len(theme.Players))
	for _, playerID := range theme.Players {
		key := [2]string{bout.Players[playerID], team}
		entry := entryIn(acc, key[0], key[1])
		sums[key] += share
		if sum > 0 {
			entry[1]++
		}
		entry[2]++
	}
}

// individualStats: per player, Σ, Σ+ and the Matches he sat — counted from the
// seating, since a player who took nothing has no state section and the sheet
// still counts the Match.
func individualStats(bouts []BoutState) ([]Stat, error) {
	acc := map[[2]string]*[3]int{}
	for _, bout := range bouts {
		for _, name := range bout.Seated {
			if name != "" {
				entryIn(acc, name, "")[2]++
			}
		}
		if err := individualBoutStats(bout, acc); err != nil {
			return nil, err
		}
	}
	return statRows(acc), nil
}

// individualBoutStats adds one bout's answers to each player's Σ and Σ+.
func individualBoutStats(bout BoutState, acc map[[2]string]*[3]int) error {
	var blob struct {
		Participants map[string]struct {
			Themes []struct {
				Answers [store.QuestionCount]string `json:"answers"`
			} `json:"themes"`
		} `json:"participants"`
	}
	if err := json.Unmarshal([]byte(bout.State), &blob); err != nil {
		return err
	}
	for pid, section := range blob.Participants {
		id, err := idstr.Parse(pid)
		if err != nil {
			return err
		}
		entry := entryIn(acc, bout.Names[id], "")
		for _, theme := range section.Themes {
			for i, mark := range theme.Answers {
				if mark == "right" {
					entry[0] += nominals[i]
					entry[1] += nominals[i]
				} else if mark == "wrong" {
					entry[0] -= nominals[i]
				}
			}
		}
	}
	return nil
}

// brainStats: per player and team, the regular questions he buzzed on, and
// how many were right and wrong.
func brainStats(bouts []BoutState) ([]Stat, error) {
	acc := map[[2]string]*[3]int{}
	for _, bout := range bouts {
		if err := brainBoutStats(bout, acc); err != nil {
			return nil, err
		}
	}
	return statRows(acc), nil
}

// brainBoutStats adds one bout's regular questions to each player's counts.
func brainBoutStats(bout BoutState, acc map[[2]string]*[3]int) error {
	var blob struct {
		Teams []struct {
			Rows []brainRow `json:"rows"`
		} `json:"teams"`
		Tiebreaks int `json:"tiebreaks"`
	}
	if err := json.Unmarshal([]byte(bout.State), &blob); err != nil {
		return err
	}
	for side, team := range blob.Teams {
		if side >= len(bout.Seated) {
			break
		}
		regular := len(team.Rows) - blob.Tiebreaks
		addBrainRows(acc, team.Rows[:min(max(regular, 0), len(team.Rows))], bout.Seated[side])
	}
	return nil
}

// brainRow is one question of a Brain side: who buzzed, and how it went.
type brainRow struct {
	Player string `json:"player"`
	Mark   string `json:"mark"`
}

// addBrainRows counts a side's regular questions for whoever buzzed on them.
func addBrainRows(acc map[[2]string]*[3]int, rows []brainRow, team string) {
	for _, row := range rows {
		if row.Player == "" || row.Mark == "" {
			continue
		}
		entry := entryIn(acc, row.Player, team)
		entry[0]++
		if row.Mark == "right" {
			entry[1]++
		} else {
			entry[2]++
		}
	}
}

func themeSum(answers [5]string) int {
	sum := 0
	for i, mark := range answers {
		if mark == "right" {
			sum += nominals[i]
		} else if mark == "wrong" {
			sum -= nominals[i]
		}
	}
	return sum
}
