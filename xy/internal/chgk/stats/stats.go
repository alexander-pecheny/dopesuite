// Package stats is a Go port of chgksuite's `compose add_stats`
// (chgksuite/composer/stats.py plus the question-table readers in
// common.py). It appends the per-question stats line — label: taken/total
// (percent) — to each question's comment from a tournament's results, taken
// either from rating.chgk.info or from a local csv/xlsx export in the same
// shape.
package stats

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	corei18n "pecheny.me/dopecore/i18nstrings"
	xystrings "xy/i18nstrings"

	"xy/internal/chgk/fsource"
)

// Result is one team's row of a results table: the mask is one character per
// question, "1" for taken.
type Result struct {
	TeamID int
	Name   string
	Mask   string
}

// Options are the switches `compose add_stats` takes.
type Options struct {
	// Label heads the line; the default is the catalog's stats.options.label.
	Label string
	// QuestionRange narrows the mask to a slice of it, "25-36" style. Empty
	// means the whole of it.
	QuestionRange string
	// TeamNamingThreshold: a question this few teams took gets them named.
	TeamNamingThreshold int
}

// DefaultOptions are chgksuite's, with its label from the catalog.
func DefaultOptions() Options {
	return Options{Label: xystrings.Default.Stats.Options.Label(), TeamNamingThreshold: 2}
}

// Add appends the stats line to every non-warm-up question of the package, in
// order: the results' masks are positional, so question N of the structure is
// bit N of the mask.
func Add(doc fsource.Doc, results []Result, o Options) error {
	s := xystrings.Default
	start, end, err := parseRange(o.QuestionRange)
	if err != nil {
		return err
	}
	taken := map[int]int{}
	byQuestion := map[int]map[string]bool{}
	total := 0
	for _, r := range results {
		if r.Mask == "" {
			continue
		}
		total++
		qnum := 1
		for i, c := range r.Mask {
			if i+1 < start || i+1 > end {
				continue
			}
			if c == '1' {
				taken[qnum]++
				if byQuestion[qnum] == nil {
					byQuestion[qnum] = map[string]bool{}
				}
				byQuestion[qnum][r.Name] = true
			}
			qnum++
		}
	}
	if total == 0 {
		return corei18n.User(s.Stats.Results.Empty())
	}

	qnumber := 1
	for _, p := range doc {
		q, ok := p.Content.(*fsource.Question)
		if p.Type != "Question" || !ok || strings.HasPrefix(fmt.Sprintf("%v", q.Get("number")), "0") {
			continue
		}
		scored := taken[qnumber]
		// Python's round() is half-to-even, and a package where exactly an
		// eighth of the field took a question would round the other way here.
		percent := int(math.RoundToEven(float64(scored) / float64(total) * 100))
		message := s.Stats.Question.Line(o.Label, strconv.Itoa(scored), strconv.Itoa(total),
			strconv.Itoa(percent))
		if scored > 0 && scored <= o.TeamNamingThreshold {
			names := make([]string, 0, len(byQuestion[qnumber]))
			for name := range byQuestion[qnumber] {
				names = append(names, name)
			}
			sort.Strings(names)
			message += s.Stats.Question.Takers(strings.Join(names, ", "))
		}
		patchQuestion(q, message)
		qnumber++
	}
	return nil
}

// HasStats is telegram.py's structure_has_stats, which `stop_if_no_stats` uses
// to refuse to publish a package nobody has played yet.
func HasStats(doc fsource.Doc) bool {
	for _, p := range doc {
		q, ok := p.Content.(*fsource.Question)
		if p.Type != "Question" || !ok {
			continue
		}
		if c, ok := q.Get("comment").(string); ok && strings.Contains(c, "Взятия:") {
			return true
		}
	}
	return false
}

// patchQuestion adds the line to whatever shape the comment already has: a new
// comment, another paragraph of a plain one, or another item of a list.
func patchQuestion(q *fsource.Question, message string) {
	switch c := q.Get("comment").(type) {
	case nil:
		q.Set("comment", message)
	case string:
		q.Set("comment", c+"\n"+message)
	case []any:
		if len(c) > 1 {
			if items, ok := c[1].([]any); ok {
				c[1] = append(items, message)
				q.Set("comment", c)
				return
			}
		}
		q.Set("comment", append(c, message))
	}
}

func parseRange(s string) (int, int, error) {
	if s == "" {
		return 1, 9999, nil
	}
	from, to, ok := strings.Cut(s, "-")
	if !ok {
		return 0, 0, corei18n.User(xystrings.Default.Stats.Range.Bad(s))
	}
	start, err1 := strconv.Atoi(strings.TrimSpace(from))
	end, err2 := strconv.Atoi(strings.TrimSpace(to))
	if err1 != nil || err2 != nil {
		return 0, 0, corei18n.User(xystrings.Default.Stats.Range.Bad(s))
	}
	return start, end, nil
}
