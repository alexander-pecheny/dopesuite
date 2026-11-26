package games

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// ODTeam and ODState mirror the persisted OD game state JSON (see static/od.js).
// They are shared by the xlsx export and the server-side results view so the two
// scoring paths can't drift.
type ODTeam struct {
	Name   string `json:"name"`
	City   string `json:"city"`
	Number int64  `json:"number"`
	// Venue and VenueNumber are where a team of a synch fest plays and its
	// number there, under which its venue's sheet records it (ADR-0033).
	Venue       int64 `json:"venue,omitempty"`
	VenueNumber int64 `json:"venueNumber,omitempty"`
	// Flags are the team's Divisions by short name, propagated with the roster
	// (ADR-0020). The page filters and re-ranks on them; nothing here does.
	Flags []string `json:"flags,omitempty"`
}

type ODState struct {
	Teams     []ODTeam  `json:"teams"`
	Entries   [][]int64 `json:"entries"`
	Completed []bool    `json:"completed"`
	// Venues are the sheets of a synch fest's one OD, by venue id: each venue
	// enters its own teams under its own numbers and completes its own
	// questions. Entries and Completed then say nothing until flattened.
	Venues map[string]ODSheet `json:"venues,omitempty"`
	// Contested are the contested answers, which the server writes here from its own
	// tables (domain/contested) without their text.
	Contested []ODContested `json:"contested,omitempty"`
	// Ticks are a minus game's (ODMinus) finished tours, by the team's Number
	// and then by tour: a team's answers on a tour count once the host ticked
	// it. Completed says nothing there.
	Ticks map[string][]bool `json:"ticks,omitempty"`
}

// ODContested is one team's contested answer on one question, under the team's Number.
type ODContested struct {
	Question int    `json:"question"`
	Number   int64  `json:"number"`
	Status   string `json:"status"`
}

// ContestedKey is the document's key for its contested answers.
const ContestedKey = "contested"

// The jury's ruling on a contested answer, as ODContested.Status says it.
const (
	ContestedPending  = "pending"
	ContestedAccepted = "accepted"
	ContestedRejected = "rejected"
)

// Scored is the document as the standings read it: the venue sheets folded
// into one, and every contested answer the jury accepted entered as taken.
func (s *ODState) Scored() {
	s.FlattenVenues()
	for _, c := range s.Contested {
		if c.Status != ContestedAccepted || c.Question < 0 || c.Question >= max(len(s.Entries), len(s.Completed)) {
			continue
		}
		for len(s.Entries) <= c.Question {
			s.Entries = append(s.Entries, []int64{})
		}
		if !slices.Contains(s.Entries[c.Question], c.Number) {
			s.Entries[c.Question] = append(s.Entries[c.Question], c.Number)
		}
	}
}

// Contestable is a Protocol whose Games take contested answers.
type Contestable interface{ Contestable() }

func (od) Contestable() {}

// ODResultsTeam is one ranked row of an OD standings view.
type ODResultsTeam struct {
	Place      string `json:"place"`      // tie-grouped label ("1", "2–4"); "" before any question is completed
	Index      int    `json:"index"`      // roster index in state.teams
	Number     int64  `json:"number"`     // printed team number (0 if unnumbered)
	Name       string `json:"name"`       //
	City       string `json:"city"`       //
	Total      int    `json:"total"`      // questions taken across all tours (Σ on the page)
	TourTotals []int  `json:"tourTotals"` // questions taken per tour, aligned with tourComp
	Rating     int    `json:"rating"`     // Buchholz-style R: sum over taken questions of (teamCount − takers + 1)
	Mask       string `json:"mask"`       // per-question "1"/"0" (took/not), rating.chgk.info style, length = sum(tourComp)
	// In a minus game (ODMinus) Total and TourTotals are points, and these
	// are the answers behind them: right and wrong, overall and per tour.
	Right     int   `json:"right,omitempty"`
	Wrong     int   `json:"wrong,omitempty"`
	TourRight []int `json:"tourRight,omitempty"`
	TourWrong []int `json:"tourWrong,omitempty"`
	// rank is what places the team: the total, then a minus game's
	// tiebreakers. Teams with equal keys share a place.
	rank []int
}

// ODResults is the computed OD standings: the same totals and per-tour standings
// the #results page renders client-side, computed server-side so callers (bots,
// exports, integrations) don't have to replicate the scoring.
type ODResults struct {
	TourComp []int           `json:"tourComp"`
	Teams    []ODResultsTeam `json:"teams"` // ranked: total desc, then roster index
}

// emptyEntries is a blank entry list for every question of the tours, and
// how many questions that is.
func emptyEntries(tourComp []int) ([][]int, int) {
	totalQuestions := 0
	for _, n := range tourComp {
		totalQuestions += n
	}
	entries := make([][]int, totalQuestions)
	for i := range entries {
		entries[i] = []int{}
	}
	return entries, totalQuestions
}

// ODEmptyGameJSON builds the pristine scheme/state for an OD game (no teams, no
// entries). Shared by game creation and the clear-to-pristine path so the two
// can't drift.
func ODEmptyGameJSON(slug, title string, tourComp []int) ([]byte, []byte) {
	entries, totalQuestions := emptyEntries(tourComp)
	schemeJSON := []byte(mustJSON(map[string]any{
		"schemaVersion": 2,
		"slug":          slug,
		"title":         title,
		"gameType":      OD,
		"tourComp":      tourComp,
		"nTeams":        0,
		"teams":         []any{},
	}))
	stateJSON := []byte(mustJSON(map[string]any{
		"teams":          []any{},
		"entries":        entries,
		"completed":      make([]bool, totalQuestions),
		"shootoutRounds": []any{},
	}))
	return schemeJSON, stateJSON
}

// ParseTourComp reads scheme.tourComp — either a JSON array of ints or a
// comma-separated string with "count*repeat" segments (mirrors od.js) — from
// the OD document's top level, or from its one stage's config, where the DSL
// compiler writes the Protocol's params.
func ParseTourComp(schemeJSON string) []int {
	if schemeJSON == "" {
		return nil
	}
	var probe struct {
		TourComp json.RawMessage `json:"tourComp"`
		Stages   []struct {
			Config struct {
				TourComp json.RawMessage `json:"tourComp"`
			} `json:"config"`
		} `json:"stages"`
	}
	if err := json.Unmarshal([]byte(schemeJSON), &probe); err != nil {
		return nil
	}
	if len(probe.TourComp) == 0 && len(probe.Stages) > 0 {
		probe.TourComp = probe.Stages[0].Config.TourComp
	}
	if len(probe.TourComp) == 0 {
		return nil
	}
	var nums []int
	if err := json.Unmarshal(probe.TourComp, &nums); err == nil {
		return filterPositive(nums)
	}
	var s string
	if err := json.Unmarshal(probe.TourComp, &s); err != nil {
		return nil
	}
	var out []int
	for _, seg := range strings.Split(s, ",") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		if i := strings.IndexByte(seg, '*'); i >= 0 {
			count, _ := strconv.Atoi(strings.TrimSpace(seg[:i]))
			repeat, _ := strconv.Atoi(strings.TrimSpace(seg[i+1:]))
			for j := 0; j < repeat; j++ {
				if count > 0 {
					out = append(out, count)
				}
			}
			continue
		}
		if n, err := strconv.Atoi(seg); err == nil && n > 0 {
			out = append(out, n)
		}
	}
	return out
}

func filterPositive(in []int) []int {
	out := in[:0:0]
	for _, n := range in {
		if n > 0 {
			out = append(out, n)
		}
	}
	return out
}

// ODMinus is the scheme's `minus` block: a ChGK where a wrong answer costs
// points (Tminnoe pole). The input tab enters a wrong answer as the team's number
// with a minus («-5»); an empty cell is no answer. A tour scores
// right − Wrong·wrong, plus AllBonus when every question of it was taken.
// With EmptyTour, a team that answered nothing in a finished tour (but did
// answer somewhere in the game) is charged size/3 wrong answers for it.
// Places go by points, then right answers, then the best tour's right
// answers, then the worst tour's.
type ODMinus struct {
	Wrong     int  `json:"wrong"`
	AllBonus  int  `json:"allBonus,omitempty"`
	EmptyTour bool `json:"emptyTour,omitempty"`
}

// odEmptyTourDivisor: an empty tour is charged a third of its questions.
const odEmptyTourDivisor = 3

// ParseODMinus reads the minus block from the scheme's top level, or from
// its one stage's config; nil is a plain ChGK.
func ParseODMinus(schemeJSON string) *ODMinus {
	if schemeJSON == "" {
		return nil
	}
	var probe struct {
		Minus  *ODMinus `json:"minus"`
		Stages []struct {
			Config struct {
				Minus *ODMinus `json:"minus"`
			} `json:"config"`
		} `json:"stages"`
	}
	if err := json.Unmarshal([]byte(schemeJSON), &probe); err != nil {
		return nil
	}
	if probe.Minus == nil && len(probe.Stages) > 0 {
		probe.Minus = probe.Stages[0].Config.Minus
	}
	if probe.Minus == nil || probe.Minus.Wrong < 0 || probe.Minus.AllBonus < 0 {
		return nil
	}
	return probe.Minus
}

// odAnswers is who answered what on the completed questions: took[q] the
// teams that took question q, and wrong[q] those of a minus game whose
// number was entered with a minus there and who did not also take it (an
// accepted contested answer enters it as taken). An unfinished question has
// nil sets. In a minus game ticked[team][tour] is which tours each team
// finished, and a question holds the answers of those teams alone.
type odAnswers struct {
	took, wrong []map[int]bool
	ticked      [][]bool
}

func odAnswersOf(state ODState, indexByNumber map[int64]int, tours []int, minus bool) (odAnswers, bool) {
	a := odAnswers{took: make([]map[int]bool, len(state.Entries)), wrong: make([]map[int]bool, len(state.Entries))}
	counts, done := odCompleted(state)
	if minus {
		a.ticked = odTicked(state, tours)
		counts, done = odTickedCounts(a.ticked, tours)
	}
	anyCompleted := false
	for q, column := range state.Entries {
		if !done(q) {
			continue
		}
		anyCompleted = true
		a.took[q], a.wrong[q] = map[int]bool{}, map[int]bool{}
		for _, num := range column {
			if idx, ok := indexByNumber[num]; ok && counts(q, idx) {
				a.took[q][idx] = true
			}
		}
		for _, num := range column {
			if idx, ok := indexByNumber[-num]; ok && num < 0 && !a.took[q][idx] && counts(q, idx) {
				a.wrong[q][idx] = true
			}
		}
	}
	return a, anyCompleted
}

// ODTaken is, for each completed question, the team rows that took it, as
// the standings count them (state already Scored); nil for a question not
// completed.
func ODTaken(schemeJSON string, state ODState, tours []int) []map[int]bool {
	indexByNumber := make(map[int64]int, len(state.Teams))
	for i, t := range state.Teams {
		if t.Number != 0 {
			indexByNumber[t.Number] = i
		}
	}
	answers, _ := odAnswersOf(state, indexByNumber, tours, ParseODMinus(schemeJSON) != nil)
	return answers.took
}

// odCompleted is whose answers on a question count, and whether it is done:
// everyone's, once the question is completed.
func odCompleted(state ODState) (counts func(q, idx int) bool, done func(q int) bool) {
	done = func(q int) bool { return q >= len(state.Completed) || state.Completed[q] }
	return func(q, _ int) bool { return done(q) }, done
}

// odTickedCounts is odCompleted for a minus game: a row's answers count
// once it ticked the question's tour, and the question is done once some row
// did.
func odTickedCounts(ticked [][]bool, tours []int) (counts func(q, idx int) bool, done func(q int) bool) {
	tourOf := odTourOf(tours)
	counts = func(q, idx int) bool { return q < len(tourOf) && ticked[idx][tourOf[q]] }
	done = func(q int) bool {
		return slices.ContainsFunc(ticked, func(row []bool) bool { return q < len(tourOf) && row[tourOf[q]] })
	}
	return counts, done
}

// odTicked is each team row's ticked tours.
func odTicked(state ODState, tours []int) [][]bool {
	out := make([][]bool, len(state.Teams))
	for i, team := range state.Teams {
		out[i] = make([]bool, len(tours))
		copy(out[i], state.Ticks[fmt.Sprint(team.Number)])
	}
	return out
}

// odTourOf is the tour of each question.
func odTourOf(tours []int) []int {
	var out []int
	for t, size := range tours {
		for range size {
			out = append(out, t)
		}
	}
	return out
}

// tour counts one team's right and wrong answers over tour t's questions,
// and says whether the tour is finished: every question completed, or in a
// minus game the team's tour ticked.
func (a odAnswers) tour(t, from, size, teamIdx int) (right, wrong int, done bool) {
	done = size > 0
	for q := from; q < from+size; q++ {
		if q >= len(a.took) || a.took[q] == nil {
			done = false
			continue
		}
		if a.took[q][teamIdx] {
			right++
		} else if a.wrong[q][teamIdx] {
			wrong++
		}
	}
	if a.ticked != nil {
		done = size > 0 && a.ticked[teamIdx][t]
	}
	return right, wrong, done
}

// odMinusTours is one team's minus score: points per tour, and the right and
// wrong answers per tour (a charged empty tour counts its charge as wrong).
func odMinusTours(minus ODMinus, tours []int, answers odAnswers, teamIdx int) (points, right, wrongs []int) {
	points, right, wrongs = make([]int, len(tours)), make([]int, len(tours)), make([]int, len(tours))
	done := make([]bool, len(tours))
	from := 0
	for t, size := range tours {
		right[t], wrongs[t], done[t] = answers.tour(t, from, size, teamIdx)
		from += size
	}
	answered := sumInts(right)+sumInts(wrongs) > 0
	for t, size := range tours {
		if minus.EmptyTour && answered && done[t] && right[t]+wrongs[t] == 0 {
			wrongs[t] = size / odEmptyTourDivisor
		}
		points[t] = right[t] - minus.Wrong*wrongs[t]
		if size > 0 && right[t] == size {
			points[t] += minus.AllBonus
		}
	}
	return points, right, wrongs
}

// ComputeODResults scores an OD game from its scheme and state JSON, mirroring
// the client-side helpers in static/od.js (sumRow, tourSumsForTeam,
// ratingForTeam, computePlaces).
func ComputeODResults(schemeJSON, stateJSON string) (ODResults, error) {
	tours := ParseTourComp(schemeJSON)
	var state ODState
	if stateJSON != "" {
		if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
			return ODResults{}, fmt.Errorf("parse OD state: %w", err)
		}
	}
	state.Scored()
	if len(tours) == 0 {
		// No tour composition recorded: treat every question as one tour, matching
		// the export's fallback so the view is never empty.
		tours = []int{len(state.Entries)}
	}

	teamCount := len(state.Teams)

	// number → team index, mirroring the live grid's number-keyed scoring.
	indexByNumber := make(map[int64]int, teamCount)
	for i, t := range state.Teams {
		if t.Number != 0 {
			indexByNumber[t.Number] = i
		}
	}
	minus := ParseODMinus(schemeJSON)
	answers, anyCompleted := odAnswersOf(state, indexByNumber, tours, minus != nil)
	took := answers.took

	teams := make([]ODResultsTeam, teamCount)
	for teamIdx, team := range state.Teams {
		row := odTeamRow(team, teamIdx, teamCount, tours, took)
		if minus != nil {
			row.scoreMinus(*minus, tours, answers)
		}
		teams[teamIdx] = row
	}

	// Rank by total desc, then roster index (stable), and assign tie-grouped place
	// labels, mirroring computePlaces in od.js. Without any completed question,
	// places stay blank.
	order := make([]int, teamCount)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return slices.Compare(teams[order[a]].rank, teams[order[b]].rank) > 0
	})
	ranked := make([]ODResultsTeam, teamCount)
	for rank, teamIdx := range order {
		ranked[rank] = teams[teamIdx]
	}
	if anyCompleted {
		labelPlaces(ranked)
	}

	return ODResults{TourComp: tours, Teams: ranked}, nil
}

// odTeamRow is a team's row as a plain ChGK scores it: the questions it
// took, per tour and overall, its rating and its mask.
func odTeamRow(team ODTeam, teamIdx, teamCount int, tours []int, took []map[int]bool) ODResultsTeam {
	row := ODResultsTeam{Index: teamIdx, Number: team.Number, Name: team.Name, City: team.City, TourTotals: make([]int, len(tours))}
	var mask strings.Builder
	q := 0
	for tourIdx, tourSize := range tours {
		for range tourSize {
			if q < len(took) && took[q] != nil && took[q][teamIdx] {
				row.Total++
				row.TourTotals[tourIdx]++
				row.Rating += teamCount - len(took[q]) + 1
				mask.WriteByte('1')
			} else {
				mask.WriteByte('0')
			}
			q++
		}
	}
	row.Mask = mask.String()
	row.rank = []int{row.Total}
	return row
}

// labelPlaces gives ranked rows their places, a shared one ("2–4") to rows
// level on everything that places them.
func labelPlaces(ranked []ODResultsTeam) {
	for i := 0; i < len(ranked); {
		j := i
		for j+1 < len(ranked) && ranked[j+1].SharesPlace(ranked[i]) {
			j++
		}
		label := strconv.Itoa(i + 1)
		if j > i {
			label = fmt.Sprintf("%d–%d", i+1, j+1)
		}
		for k := i; k <= j; k++ {
			ranked[k].Place = label
		}
		i = j + 1
	}
}

// scoreMinus rescores a row as a minus game does: points, and the places
// broken on right answers, the best tour's, then the worst tour's.
func (t *ODResultsTeam) scoreMinus(minus ODMinus, tours []int, answers odAnswers) {
	points, right, wrongs := odMinusTours(minus, tours, answers, t.Index)
	t.TourTotals, t.TourRight, t.TourWrong = points, right, wrongs
	t.Total, t.Right, t.Wrong = sumInts(points), sumInts(right), sumInts(wrongs)
	t.rank = []int{t.Total, t.Right, slices.Max(append([]int{0}, right...)), minOr0(right)}
}

// SharesPlace says two ranked rows are level on everything that places them.
func (t ODResultsTeam) SharesPlace(other ODResultsTeam) bool {
	return slices.Equal(t.rank, other.rank)
}

func sumInts(values []int) int {
	total := 0
	for _, v := range values {
		total += v
	}
	return total
}

func minOr0(values []int) int {
	if len(values) == 0 {
		return 0
	}
	return slices.Min(values)
}
