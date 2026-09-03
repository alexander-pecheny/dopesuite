package replay

import (
	"encoding/json"
	"sort"
	"strconv"

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
	// ScoreMetric names the Protocol metric the sheet prints as the Match's Σ
	// when it is not the total column (brain counts the questions taken).
	ScoreMetric string
	Columns     [3]string
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
	"ek":    {Columns: [3]string{"Σ", "Σ+", dopestrings.Default.Replay.Codec.StatThemes()}, Aggregate: ekStats},
	"si":    {Individual: true, Columns: [3]string{"Σ", "Σ+", dopestrings.Default.Replay.Codec.StatBouts()}, Aggregate: individualStats},
	"brain": {Questions: true, ScoreMetric: "taken", Columns: [3]string{dopestrings.Default.Replay.Codec.StatAttempts(), dopestrings.Default.Replay.Codec.StatRight(), dopestrings.Default.Replay.Codec.StatWrong()}, Aggregate: brainStats},
	// Troika's sheet keeps no per-player row — it never records which seat
	// answered — so there is no stats section to hold dope to.
	"troika": {Counts: true, ThemeSize: 3, ScoreMetric: "total"},
}

// CodecFor is the codec of a game type; a game with none has no transcript form.
func CodecFor(game string) (Codec, bool) {
	codec, ok := codecs[game]
	return codec, ok
}

var nominals = [5]int{10, 20, 30, 40, 50}

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
	for _, bout := range bouts {
		var blob struct {
			Participants map[string]struct {
				Themes []struct {
					Player  int64     `json:"player"`
					Answers [5]string `json:"answers"`
				} `json:"themes"`
			} `json:"participants"`
		}
		if err := json.Unmarshal([]byte(bout.State), &blob); err != nil {
			return nil, err
		}
		for pid, section := range blob.Participants {
			id, err := strconv.ParseInt(pid, 10, 64)
			if err != nil {
				return nil, err
			}
			for _, theme := range section.Themes {
				if theme.Player == 0 {
					continue
				}
				entry := entryIn(acc, bout.Players[theme.Player], bout.Names[id])
				sum := themeSum(theme.Answers)
				entry[0] += sum
				if sum > 0 {
					entry[1]++
				}
				entry[2]++
			}
		}
	}
	return statRows(acc), nil
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
		var blob struct {
			Participants map[string]struct {
				Themes []struct {
					Answers [5]string `json:"answers"`
				} `json:"themes"`
			} `json:"participants"`
		}
		if err := json.Unmarshal([]byte(bout.State), &blob); err != nil {
			return nil, err
		}
		for pid, section := range blob.Participants {
			id, err := strconv.ParseInt(pid, 10, 64)
			if err != nil {
				return nil, err
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
	}
	return statRows(acc), nil
}

// brainStats: per player and team, the regular questions he buzzed on, and
// how many were right and wrong.
func brainStats(bouts []BoutState) ([]Stat, error) {
	acc := map[[2]string]*[3]int{}
	for _, bout := range bouts {
		var blob struct {
			Teams []struct {
				Rows []struct {
					Player string `json:"player"`
					Mark   string `json:"mark"`
				} `json:"rows"`
			} `json:"teams"`
			Tiebreaks int `json:"tiebreaks"`
		}
		if err := json.Unmarshal([]byte(bout.State), &blob); err != nil {
			return nil, err
		}
		for side, team := range blob.Teams {
			if side >= len(bout.Seated) {
				break
			}
			regular := len(team.Rows) - blob.Tiebreaks
			for index, row := range team.Rows {
				if index >= regular || row.Player == "" || row.Mark == "" {
					continue
				}
				entry := entryIn(acc, row.Player, bout.Seated[side])
				entry[0]++
				if row.Mark == "right" {
					entry[1]++
				} else {
					entry[2]++
				}
			}
		}
	}
	return statRows(acc), nil
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
