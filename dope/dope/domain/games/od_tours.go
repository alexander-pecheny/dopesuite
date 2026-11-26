package games

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	dopestrings "dope/i18nstrings"

	corei18n "code.pecheny.me/pecheny/dopesuite/dopecore/i18nstrings"
)

// Retourer is a format whose tours the host may change on a Game already
// under way (OD and the minefield): the organizers moved a question from one
// tour to another, or the tournament site now says another composition.
type Retourer interface {
	// Retour writes tours into the scheme and fits the document to them.
	// The answers stay under their question numbers across the whole Game,
	// so only the tour boundaries move; a question the new tours no longer
	// have must hold nothing.
	Retour(schemeJSON, stateJSON string, tours []int) (scheme, state []byte, err error)
}

// Retour keeps every answer under its question number and refuses tours
// that would drop a question something is entered on.
func (od) Retour(schemeJSON, stateJSON string, tours []int) ([]byte, []byte, error) {
	total := 0
	for _, n := range tours {
		total += n
	}
	var doc ODState
	if strings.TrimSpace(stateJSON) != "" {
		if err := json.Unmarshal([]byte(stateJSON), &doc); err != nil {
			return nil, nil, err
		}
	}
	if held := questionsHeldFrom(doc, total); len(held) > 0 {
		return nil, nil, corei18n.User(dopestrings.Default.Games.Od.ToursDropAnswers(strconv.Itoa(total), questionRuns(held)))
	}
	scheme, err := retourScheme(schemeJSON, tours)
	if err != nil {
		return nil, nil, err
	}
	state, err := retourState(stateJSON, total)
	return scheme, state, err
}

// questionsHeldFrom is every question from index total on that a team's
// answer, a completion or a contested answer sits on, in order.
func questionsHeldFrom(doc ODState, total int) []int {
	held := map[int]bool{}
	mark := func(entries [][]int64, completed []bool) {
		for q := total; q < len(entries); q++ {
			if slices.ContainsFunc(entries[q], func(n int64) bool { return n != 0 }) {
				held[q] = true
			}
		}
		for q := total; q < len(completed); q++ {
			if completed[q] {
				held[q] = true
			}
		}
	}
	mark(doc.Entries, doc.Completed)
	for _, sheet := range doc.Venues {
		mark(sheet.Entries, sheet.Completed)
	}
	for _, c := range doc.Contested {
		if c.Question >= total {
			held[c.Question] = true
		}
	}
	out := make([]int, 0, len(held))
	for q := range held {
		out = append(out, q)
	}
	slices.Sort(out)
	return out
}

// retourScheme writes tours where the scheme keeps them: at its top level,
// and in its one stage's config when the DSL compiler wrote them there.
func retourScheme(raw string, tours []int) ([]byte, error) {
	obj, err := RawJSONObject(raw)
	if err != nil {
		return nil, err
	}
	comp, err := json.Marshal(tours)
	if err != nil {
		return nil, err
	}
	obj["tourComp"] = comp
	if rawStages, ok := obj["stages"]; ok {
		var stages []map[string]json.RawMessage
		if json.Unmarshal(rawStages, &stages) == nil && len(stages) > 0 {
			config, err := RawJSONObject(string(stages[0]["config"]))
			if err != nil {
				return nil, err
			}
			if _, has := config["tourComp"]; has {
				config["tourComp"] = comp
				if stages[0]["config"], err = json.Marshal(config); err != nil {
					return nil, err
				}
				if obj["stages"], err = json.Marshal(stages); err != nil {
					return nil, err
				}
			}
		}
	}
	return json.Marshal(obj)
}

// retourState gives the sheet, and every venue's, a column per question of
// the new tours: the ones past them go (they hold nothing), new ones come
// empty.
func retourState(raw string, total int) ([]byte, error) {
	obj, err := RawJSONObject(raw)
	if err != nil {
		return nil, err
	}
	var entries [][]int64
	if rawEntries, ok := obj["entries"]; ok {
		if err := json.Unmarshal(rawEntries, &entries); err != nil {
			return nil, err
		}
	}
	var completed []bool
	if rawCompleted, ok := obj["completed"]; ok {
		if err := json.Unmarshal(rawCompleted, &completed); err != nil {
			return nil, err
		}
	}
	var teams []json.RawMessage
	if rawTeams, ok := obj["teams"]; ok {
		_ = json.Unmarshal(rawTeams, &teams)
	}
	width := len(teams)
	for _, column := range entries {
		width = max(width, len(column))
	}
	if obj["entries"], err = json.Marshal(resizeSheet(entries, total, width)); err != nil {
		return nil, err
	}
	if obj["completed"], err = json.Marshal(resizeCompleted(completed, total)); err != nil {
		return nil, err
	}
	if err := retourVenues(obj, total); err != nil {
		return nil, err
	}
	return json.Marshal(obj)
}

// retourVenues gives every venue's sheet of a synch fest's Game a column per
// question of the new tours.
func retourVenues(obj map[string]json.RawMessage, total int) error {
	rawVenues, ok := obj["venues"]
	if !ok {
		return nil
	}
	sheets := map[string]ODSheet{}
	if err := json.Unmarshal(rawVenues, &sheets); err != nil {
		return err
	}
	for venue, sheet := range sheets {
		slots := 0
		for _, column := range sheet.Entries {
			slots = max(slots, len(column))
		}
		sheets[venue] = ODSheet{Entries: resizeSheet(sheet.Entries, total, slots), Completed: resizeCompleted(sheet.Completed, total)}
	}
	raw, err := json.Marshal(sheets)
	obj["venues"] = raw
	return err
}

// resizeCompleted is completed cut or padded to questions.
func resizeCompleted(completed []bool, questions int) []bool {
	out := make([]bool, questions)
	copy(out, completed)
	return out
}

// minQuestionRun is the shortest run of questions named by its ends.
const minQuestionRun = 3

// questionRuns names questions by their numbers from 1, a run of three or
// more as its ends: "21–45, 50".
func questionRuns(indexes []int) string {
	var parts []string
	for i := 0; i < len(indexes); {
		j := i
		for j+1 < len(indexes) && indexes[j+1] == indexes[j]+1 {
			j++
		}
		switch first, last := strconv.Itoa(indexes[i]+1), strconv.Itoa(indexes[j]+1); {
		case j-i+1 >= minQuestionRun:
			parts = append(parts, first+"\u2013"+last)
		case j > i:
			parts = append(parts, first, last)
		default:
			parts = append(parts, first)
		}
		i = j + 1
	}
	return strings.Join(parts, ", ")
}
