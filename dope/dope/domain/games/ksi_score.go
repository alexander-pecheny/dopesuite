package games

import (
	"encoding/json"
	"fmt"
	"sort"
)

// Server-side KSI scoring, mirroring the si.js results tab and the xlsx «Итог»
// sheet: totals under the per-(team,theme) sticker rules, ranked by total,
// then plus, then correct counts from the highest value down. Declined teams
// keep their participant index but are excluded from the ranking.

// KSIState is the persisted KSI state JSON: the participants list, the
// declined map (see KSIDeclinedKey), the per-theme × per-team sticker grid
// (stickers games only) and the per-theme answer grids (team × question marks).
type KSIState struct {
	Participants []KSIParticipant `json:"participants"`
	Declined     map[string]bool  `json:"declined"`
	Stickers     [][]string       `json:"stickers"`
	Themes       []struct {
		Answers [][]string `json:"answers"`
	} `json:"themes"`
}

// KSIResultsTeam is one ranked team: its participant index, shared numeric
// place (1-based; ties share), and the ranking metrics.
type KSIResultsTeam struct {
	Index   int
	Place   float64
	Total   int
	Plus    int
	Correct map[int]int // correct answers per question value
}

// ComputeKSIResults scores a KSI game from its scheme and state JSON. values
// is the question-value scale (store.QuestionValues in production). The scheme
// is consulted only for the stickers config: in a stickers game a theme is
// unscored for a team until its sticker is chosen; a plain game scores every
// theme under neutral rules.
func ComputeKSIResults(schemeJSON, stateJSON string, values []int) ([]KSIResultsTeam, error) {
	var state KSIState
	if stateJSON != "" {
		if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
			return nil, fmt.Errorf("parse KSI state: %w", err)
		}
	}
	stickersMode := false
	if schemeJSON != "" {
		var scheme struct {
			Stickers KSIStickerConfig `json:"stickers"`
		}
		if err := json.Unmarshal([]byte(schemeJSON), &scheme); err == nil {
			stickersMode = len(scheme.Stickers.Types) > 0
		}
	}

	mark := func(theme, player, answer int) string {
		if theme >= len(state.Themes) {
			return ""
		}
		answers := state.Themes[theme].Answers
		if player >= len(answers) || answer >= len(answers[player]) {
			return ""
		}
		return answers[player][answer]
	}
	themeSticker := func(theme, player int) (string, bool) {
		if !stickersMode {
			return KSIStickerNeutral, true
		}
		if theme < len(state.Stickers) && player < len(state.Stickers[theme]) {
			if id := state.Stickers[theme][player]; id != "" {
				return id, true
			}
		}
		return "", false
	}

	ranked := make([]KSIResultsTeam, 0, len(state.Participants))
	for p := range state.Participants {
		if KSIParticipantDeclined(state.Declined, state.Participants[p]) {
			continue
		}
		team := KSIResultsTeam{Index: p, Correct: map[int]int{}}
		for t := range state.Themes {
			sticker, scored := themeSticker(t, p)
			if !scored {
				continue
			}
			for a, value := range values {
				m := mark(t, p, a)
				cv := KSIStickerMarkValue(sticker, m, value)
				team.Total += cv
				if cv > 0 {
					team.Plus += cv
				}
				if m == "right" {
					team.Correct[value]++
				}
			}
		}
		ranked = append(ranked, team)
	}

	// Ranking values high to low, matching the «Итог» sheet.
	resultValues := make([]int, len(values))
	for i, v := range values {
		resultValues[len(values)-1-i] = v
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		a, b := ranked[i], ranked[j]
		if a.Total != b.Total {
			return a.Total > b.Total
		}
		if a.Plus != b.Plus {
			return a.Plus > b.Plus
		}
		for _, v := range resultValues {
			if a.Correct[v] != b.Correct[v] {
				return a.Correct[v] > b.Correct[v]
			}
		}
		return a.Index < b.Index
	})
	sameMetrics := func(a, b KSIResultsTeam) bool {
		if a.Total != b.Total || a.Plus != b.Plus {
			return false
		}
		for _, v := range resultValues {
			if a.Correct[v] != b.Correct[v] {
				return false
			}
		}
		return true
	}
	for i := range ranked {
		if i > 0 && sameMetrics(ranked[i], ranked[i-1]) {
			ranked[i].Place = ranked[i-1].Place
		} else {
			ranked[i].Place = float64(i + 1)
		}
	}
	return ranked, nil
}
