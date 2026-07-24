package store

import (
	"sort"
	"time"
)

// The scorer turns a persisted MatchState into the scored, client-facing
// MatchView: per-answer marks become signed point values on the QuestionValues
// scale, totals/plus and correct/wrong tallies accumulate, and manual places
// produce the standings. It is pure (no DB/server), shared by the live view,
// the SSE broadcasts and the xlsx export.

// BuildView scores a whole match state into a MatchView.
func BuildView(state MatchState) MatchView {
	teams := make([]TeamView, len(state.Teams))
	for i, team := range state.Teams {
		teams[i] = ScoreTeam(team)
	}

	standings := ManualStandings(teams)
	for i := range standings {
		standing := standings[i]
		for teamIndex := range teams {
			if teams[teamIndex].Name == standing.Name {
				teams[teamIndex].Place = standing.Place
				break
			}
		}
	}

	return MatchView{
		Title:          state.Title,
		Finished:       state.Finished,
		Revision:       state.Revision,
		UpdatedAt:      state.UpdatedAt.Format(time.RFC3339),
		QuestionValues: QuestionValues,
		Teams:          teams,
		Standings:      standings,
	}
}

// ScoreTeam scores one team's themes and shootout themes into a TeamView.
func ScoreTeam(team TeamState) TeamView {
	view := TeamView{
		ID:             team.ID,
		Name:           team.Name,
		Roster:         append([]RosterMember(nil), team.Roster...),
		Themes:         make([]ThemeView, len(team.Themes)),
		ShootoutThemes: make([]ThemeView, len(team.ShootoutThemes)),
		Place:          team.Place,
	}

	for i, theme := range team.Themes {
		tv := ThemeView{
			Player:  theme.Player,
			Answers: theme.Answers,
		}
		for answerIndex, mark := range theme.Answers {
			value := QuestionValues[answerIndex]
			switch NormalizeMark(mark) {
			case "right":
				tv.Score += value
				view.Total += value
				view.Plus += value
				view.CorrectCounts[answerIndex]++
			case "wrong":
				tv.Score -= value
				view.Total -= value
				view.WrongCounts[answerIndex]++
			}
		}
		view.Themes[i] = tv
	}
	for i, theme := range team.ShootoutThemes {
		tv := ScoreTheme(theme)
		view.ShootoutThemes[i] = tv
		view.ShootoutTotal += tv.Score
	}
	view.Tiebreak = view.ShootoutTotal
	return view
}

// ScoreTheme scores one theme's answer marks into a ThemeView.
func ScoreTheme(theme ThemeEntry) ThemeView {
	view := ThemeView{
		Player:  theme.Player,
		Answers: theme.Answers,
	}
	for answerIndex, mark := range theme.Answers {
		value := QuestionValues[answerIndex]
		switch NormalizeMark(mark) {
		case "right":
			view.Score += value
		case "wrong":
			view.Score -= value
		}
	}
	return view
}

// AssignComputedPlaces ranks the teams by score and writes 1..n into their
// places. Finishing a match runs it, turning the live grid into a result; a
// pinned place overrides it again at scoring time.
func AssignComputedPlaces(state *MatchState) {
	ranked := make([]int, len(state.Teams))
	views := make([]TeamView, len(state.Teams))
	for index, team := range state.Teams {
		ranked[index], views[index] = index, ScoreTeam(team)
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		return teamRanksHigher(views[ranked[i]], views[ranked[j]])
	})
	for place, index := range ranked {
		state.Teams[index].Place = float64(place + 1)
	}
}

func teamRanksHigher(a, b TeamView) bool {
	if a.Total != b.Total {
		return a.Total > b.Total
	}
	if a.ShootoutTotal != b.ShootoutTotal {
		return a.ShootoutTotal > b.ShootoutTotal
	}
	if a.Plus != b.Plus {
		return a.Plus > b.Plus
	}
	for i := len(a.CorrectCounts) - 1; i >= 0; i-- {
		if a.CorrectCounts[i] != b.CorrectCounts[i] {
			return a.CorrectCounts[i] > b.CorrectCounts[i]
		}
	}
	return false
}

// ManualStandings orders teams by their manual place (placed first, sorted),
// then unplaced in input order, projecting each to a StandingView.
func ManualStandings(teams []TeamView) []StandingView {
	placed := make([]TeamView, 0, len(teams))
	unplaced := make([]TeamView, 0)
	for _, team := range teams {
		if team.Place > 0 {
			placed = append(placed, team)
		} else {
			unplaced = append(unplaced, team)
		}
	}
	for i := 1; i < len(placed); i++ {
		for j := i; j > 0 && placed[j-1].Place > placed[j].Place; j-- {
			placed[j-1], placed[j] = placed[j], placed[j-1]
		}
	}

	result := make([]StandingView, 0, len(teams))
	for _, team := range append(placed, unplaced...) {
		result = append(result, StandingView{
			Name:     team.Name,
			Place:    team.Place,
			Total:    team.Total,
			Plus:     team.Plus,
			Tiebreak: team.Tiebreak,
		})
	}
	return result
}
