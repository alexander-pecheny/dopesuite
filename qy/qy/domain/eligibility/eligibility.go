// Package eligibility checks a nominated question against rule 2 of the
// regulations: its tournament started inside the contest's window, it can no
// longer be played for rating when the shortlist is published, and nobody on
// the contest's jury or committee wrote it or edited its tournament. The
// checks are automatic and can be wrong (two people share a name), so they
// warn and do not refuse; a nominator who disagrees appeals to the admins.
package eligibility

import (
	"regexp"
	"strings"
)

// Kind names the rule a question fails.
type Kind string

const (
	DatesOff      Kind = "dates_off"      // the tournament started outside the window
	StillPlayable Kind = "still_playable" // it can still be played when the shortlist is out
	AuthorBarred  Kind = "author_barred"  // an author is on the jury or the committee
	EditorBarred  Kind = "editor_barred"  // an editor of the tournament is
)

// Problem is one failed rule: Who is the person it is about, Date the day.
type Problem struct {
	Kind Kind
	Who  string
	Date string
}

// Person is someone barred from authoring or editing: a juror or an admin.
// RatingID is 0 when unknown.
type Person struct {
	Name     string
	RatingID int64
}

// Window is the contest's dates, as calendar days YYYY-MM-DD.
type Window struct {
	From, To  string // the tournament must have started in [From, To]
	Shortlist string // the day the shortlist is published
}

// Tournament is what the rating site says; an empty Start means it is
// unknown and the date rules are not checked.
type Tournament struct {
	Start, End string
	Editors    []Person
}

// Check lists the rules the question fails.
func Check(w Window, t Tournament, authors string, barred []Person) []Problem {
	var out []Problem
	if t.Start != "" && (t.Start < w.From || t.Start > w.To) {
		out = append(out, Problem{Kind: DatesOff, Date: t.Start})
	}
	if t.End != "" && w.Shortlist != "" && t.End >= w.Shortlist {
		out = append(out, Problem{Kind: StillPlayable, Date: t.End})
	}
	chunks := authorChunks(authors)
	for _, p := range barred {
		if matchesAny(p.Name, chunks) {
			out = append(out, Problem{Kind: AuthorBarred, Who: p.Name})
		}
	}
	for _, e := range t.Editors {
		for _, p := range barred {
			if p.RatingID != 0 && p.RatingID == e.RatingID || sameName(p.Name, e.Name) {
				out = append(out, Problem{Kind: EditorBarred, Who: e.Name})
				break
			}
		}
	}
	return out
}

var (
	parensRe = regexp.MustCompile(`\([^)]*\)`)
	splitRe  = regexp.MustCompile(`[,;]|\s+и\s+`)
	wordRe   = regexp.MustCompile(`[\p{L}]+`)
)

// authorChunks splits an author line into one set of words per person,
// leaving out the towns in brackets.
func authorChunks(s string) []map[string]bool {
	var out []map[string]bool
	for _, chunk := range splitRe.Split(parensRe.ReplaceAllString(s, " "), -1) {
		if ws := words(chunk); len(ws) > 0 {
			set := map[string]bool{}
			for _, w := range ws {
				set[w] = true
			}
			out = append(out, set)
		}
	}
	return out
}

// words is a name as lowercase words, ё read as е.
func words(s string) []string {
	s = strings.ReplaceAll(strings.ToLower(s), "ё", "е")
	return wordRe.FindAllString(s, -1)
}

// matchesAny says some author is this person: every word of the name, which
// needs at least a name and a surname, is among that author's words.
func matchesAny(name string, chunks []map[string]bool) bool {
	ws := words(name)
	if len(ws) < 2 {
		return false
	}
	for _, set := range chunks {
		all := true
		for _, w := range ws {
			all = all && set[w]
		}
		if all {
			return true
		}
	}
	return false
}

func sameName(a, b string) bool {
	return matchesAny(a, authorChunks(b))
}
