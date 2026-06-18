package pages

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"dope/dope/numbering"
	"dope/dope/util"
)

// Mass import of team numbers from an external source (e.g. printed answer
// blanks). The host pastes lines in the form `<number>\t<team name>`; the
// server matches each pasted name to a fest team — exact first, otherwise the
// closest by Levenshtein distance — and returns the proposed pairing for the
// host to confirm or correct in a second modal before anything is saved.

// importTeamOption is one team offered in the confirmation modal's dropdown.
type importTeamOption struct {
	ID    int64  `json:"id"`
	Label string `json:"label"`
}

// importMatch is the proposed pairing for one pasted line. TeamID is 0 when no
// team could be matched (more pasted lines than teams, or all teams already
// claimed by closer matches).
type importMatch struct {
	Line     int    `json:"line"`
	Number   int    `json:"number"`
	Raw      string `json:"raw"`
	TeamID   int64  `json:"teamId"`
	Distance int    `json:"distance"`
	Exact    bool   `json:"exact"`
}

type importMatchResponse struct {
	Teams   []importTeamOption `json:"teams"`
	Matches []importMatch      `json:"matches"`
	Errors  []string           `json:"errors"`
}

type importEntry struct {
	Line   int
	Number int
	Raw    string
}

// normalizeTeamName folds a name for fuzzy comparison: lowercase, ё→е, and
// collapsed internal whitespace.
func normalizeTeamName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "ё", "е")
	return strings.Join(strings.Fields(s), " ")
}

// levenshtein returns the edit distance between two strings, counting on runes
// so multibyte (Cyrillic) text is measured per character, not per byte.
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	la, lb := len(ra), len(rb)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			best := del
			if ins < best {
				best = ins
			}
			if sub < best {
				best = sub
			}
			curr[j] = best
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}

// parseNumberImport turns pasted text into entries. Each non-empty line must be
// `<number><tab or whitespace><name>`; the number must be 1..numbering.MaxNumber.
// Malformed lines and duplicate numbers are reported in errs and skipped.
func parseNumberImport(text string) (entries []importEntry, errs []string) {
	seenNumber := make(map[int]int) // number -> first line it appeared on
	lineNo := 0
	for _, raw := range strings.Split(text, "\n") {
		lineNo++
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" {
			continue
		}
		numTxt, name := splitNumberLine(line)
		if numTxt == "" || name == "" {
			errs = append(errs, "Строка "+strconv.Itoa(lineNo)+": ожидается «номер<таб>команда».")
			continue
		}
		n, err := strconv.Atoi(numTxt)
		if err != nil || n <= 0 || n > numbering.MaxNumber {
			errs = append(errs, "Строка "+strconv.Itoa(lineNo)+": номер должен быть целым от 1 до "+strconv.Itoa(numbering.MaxNumber)+".")
			continue
		}
		if prev, ok := seenNumber[n]; ok {
			errs = append(errs, "Строка "+strconv.Itoa(lineNo)+": номер "+strconv.Itoa(n)+" уже указан в строке "+strconv.Itoa(prev)+".")
			continue
		}
		seenNumber[n] = lineNo
		entries = append(entries, importEntry{Line: lineNo, Number: n, Raw: name})
	}
	return entries, errs
}

// splitNumberLine separates the leading number token from the team name. It
// prefers a tab separator (the documented format) but also tolerates the number
// being followed by spaces.
func splitNumberLine(line string) (numTxt, name string) {
	if i := strings.IndexByte(line, '\t'); i >= 0 {
		return strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:])
	}
	if i := strings.IndexFunc(line, func(r rune) bool { return r == ' ' || r == '\t' }); i >= 0 {
		return strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:])
	}
	return "", ""
}

// matchNumberImport pairs each pasted entry with the closest unused team.
// Entries are assigned in order of how confident their best candidate is (the
// smallest distance first), so strong matches claim their team before weaker
// entries can steal it. Each team is used at most once.
func matchNumberImport(entries []importEntry, teams []numbering.Team) []importMatch {
	type cand struct {
		teamID   int64
		distance int
	}
	// Precompute, for each entry, its team candidates sorted by distance.
	normTeams := make([]string, len(teams))
	for i, t := range teams {
		// Match against both the bare name and the "name (city)" display so
		// blanks that include the city still line up.
		normTeams[i] = normalizeTeamName(t.Name)
	}
	cands := make([][]cand, len(entries))
	bestDist := make([]int, len(entries))
	for ei, e := range entries {
		norm := normalizeTeamName(e.Raw)
		list := make([]cand, len(teams))
		for ti, t := range teams {
			d := levenshtein(norm, normTeams[ti])
			if dc := levenshtein(norm, normalizeTeamName(numbering.DisplayName(t))); dc < d {
				d = dc
			}
			list[ti] = cand{teamID: t.ID, distance: d}
		}
		sort.SliceStable(list, func(a, b int) bool { return list[a].distance < list[b].distance })
		cands[ei] = list
		if len(list) > 0 {
			bestDist[ei] = list[0].distance
		} else {
			bestDist[ei] = 1 << 30
		}
	}

	order := make([]int, len(entries))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return bestDist[order[a]] < bestDist[order[b]] })

	used := make(map[int64]bool, len(teams))
	matches := make([]importMatch, len(entries))
	for _, ei := range order {
		e := entries[ei]
		m := importMatch{Line: e.Line, Number: e.Number, Raw: e.Raw}
		for _, c := range cands[ei] {
			if used[c.teamID] {
				continue
			}
			used[c.teamID] = true
			m.TeamID = c.teamID
			m.Distance = c.distance
			m.Exact = c.distance == 0
			break
		}
		matches[ei] = m
	}
	return matches
}

func (s *Server) HandleHostFestNumbersImportMatch(w http.ResponseWriter, r *http.Request, festID int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	teams, err := numbering.LoadFestTeams(r.Context(), s.h.DB(), festID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	entries, errs := parseNumberImport(r.Form.Get("text"))
	matches := matchNumberImport(entries, teams)

	options := make([]importTeamOption, 0, len(teams))
	sorted := append([]numbering.Team(nil), teams...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if cmp := util.CompareAlpha(sorted[i].Name, sorted[j].Name); cmp != 0 {
			return cmp < 0
		}
		return sorted[i].ID < sorted[j].ID
	})
	for _, t := range sorted {
		options = append(options, importTeamOption{ID: t.ID, Label: numbering.DisplayName(t)})
	}
	if matches == nil {
		matches = []importMatch{}
	}
	if errs == nil {
		errs = []string{}
	}
	s.h.WriteJSONValue(w, importMatchResponse{Teams: options, Matches: matches, Errors: errs})
}

type importApplyRequest struct {
	Assignments []struct {
		TeamID int64 `json:"teamId"`
		Number int   `json:"number"`
	} `json:"assignments"`
}

type importApplyResponse struct {
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
	Assigned int    `json:"assigned,omitempty"`
}

func (s *Server) HandleHostFestNumbersImportApply(w http.ResponseWriter, r *http.Request, festID int64) {
	var req importApplyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.h.WriteJSONValue(w, importApplyResponse{Error: "Не удалось прочитать данные."})
		return
	}
	teams, err := numbering.LoadFestTeams(r.Context(), s.h.DB(), festID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Start from the teams' current numbers so a partial import keeps the rest
	// intact, then let each confirmed assignment overwrite. Import is the
	// authoritative source (printed blanks), so a number being moved onto a
	// team is stripped from whoever else currently holds it.
	final := make(map[int64]int, len(teams))
	validIDs := make(map[int64]bool, len(teams))
	for _, t := range teams {
		validIDs[t.ID] = true
		if t.Number > 0 {
			final[t.ID] = t.Number
		}
	}
	seenTeam := make(map[int64]bool, len(req.Assignments))
	for _, a := range req.Assignments {
		if !validIDs[a.TeamID] {
			s.h.WriteJSONValue(w, importApplyResponse{Error: "Команда не из этого феста."})
			return
		}
		if a.Number <= 0 || a.Number > numbering.MaxNumber {
			s.h.WriteJSONValue(w, importApplyResponse{Error: "Номер должен быть целым от 1 до " + strconv.Itoa(numbering.MaxNumber) + "."})
			return
		}
		if seenTeam[a.TeamID] {
			s.h.WriteJSONValue(w, importApplyResponse{Error: "Команда выбрана несколько раз."})
			return
		}
		seenTeam[a.TeamID] = true
	}
	for _, a := range req.Assignments {
		for tid, n := range final {
			if n == a.Number && tid != a.TeamID {
				delete(final, tid)
			}
		}
		final[a.TeamID] = a.Number
	}
	if err := s.SaveFestNumbers(r.Context(), festID, final); err != nil {
		s.h.WriteJSONValue(w, importApplyResponse{Error: err.Error()})
		return
	}
	s.h.WriteJSONValue(w, importApplyResponse{OK: true, Assigned: len(req.Assignments)})
}
