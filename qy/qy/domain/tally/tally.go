// Package tally is the arithmetic of the rules: who makes the shortlist after
// the first stage, and how the final stage ranks it. It is pure; the server
// feeds it rows and shows what it answers.
package tally

import (
	"cmp"
	"slices"
)

// Count is one question's first-stage likes from one jury.
type Count struct {
	ID int64
	QY int
	N  int
}

// AtLeast takes the questions with the most likes, best first, until it has at
// least n, and then every question tied with the last one taken. Questions in
// skip are passed over, and a question nobody liked is never taken.
func AtLeast(counts []Count, n int, skip map[int64]bool) []Count {
	sorted := slices.Clone(counts)
	slices.SortStableFunc(sorted, func(a, b Count) int {
		return cmp.Or(cmp.Compare(b.N, a.N), cmp.Compare(a.QY, b.QY))
	})
	var out []Count
	for _, c := range sorted {
		if skip[c.ID] || c.N == 0 {
			continue
		}
		if len(out) >= n && c.N < out[len(out)-1].N {
			break
		}
		out = append(out, c)
	}
	return out
}

// Candidate is a question as the shortlist sees it.
type Candidate struct {
	ID     int64
	QY     int
	Expert int
	People int
	// Excluded questions are taken out of the ordered lists before the cut: the
	// author declined, or an admin removed it.
	Excluded bool
	// Pinned questions join the shortlist whatever their likes.
	Pinned bool
}

// Why a question is on the shortlist.
const (
	ByExpert = "expert"
	ByPeople = "people"
	ByPin    = "pin"
)

type Pick struct {
	ID  int64
	QY  int
	Why string
}

// Shortlist is the rule of the first stage: the expert jury's top expertN with
// ties, then the people's jury's top peopleN with ties among the rest, then
// the pinned questions.
func Shortlist(cands []Candidate, expertN, peopleN int) []Pick {
	skip := map[int64]bool{}
	var expert, people []Count
	for _, c := range cands {
		if c.Excluded {
			skip[c.ID] = true
		}
		expert = append(expert, Count{c.ID, c.QY, c.Expert})
		people = append(people, Count{c.ID, c.QY, c.People})
	}
	var out []Pick
	take := func(cs []Count, why string) {
		for _, c := range cs {
			out = append(out, Pick{c.ID, c.QY, why})
			skip[c.ID] = true
		}
	}
	take(AtLeast(expert, expertN, skip), ByExpert)
	take(AtLeast(people, peopleN, skip), ByPeople)
	for _, c := range cands {
		if c.Pinned && !skip[c.ID] {
			out = append(out, Pick{c.ID, c.QY, ByPin})
			skip[c.ID] = true
		}
	}
	return out
}

// Places is a place that two or more questions may share: From == To unless
// they tie.
type Places struct{ From, To int }

func (p Places) Mid() float64 { return float64(p.From+p.To) / 2 }

// placesBy hands out places along a sorted list, giving every run of equal
// entries the range it spans.
func placesBy[T any](sorted []T, same func(a, b T) bool) []Places {
	out := make([]Places, len(sorted))
	for i := 0; i < len(sorted); {
		j := i + 1
		for j < len(sorted) && same(sorted[i], sorted[j]) {
			j++
		}
		for k := i; k < j; k++ {
			out[k] = Places{i + 1, j}
		}
		i = j
	}
	return out
}

// Ballot is one expert juror's complete set of marks, question to 1–10.
type Ballot map[int64]int

// Ranks turns a ballot into ranks, 1 for the best mark. Equal marks share the
// mean of the ranks they span.
func (b Ballot) Ranks() map[int64]float64 {
	ids := make([]int64, 0, len(b))
	for id := range b {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(x, y int64) int { return cmp.Or(cmp.Compare(b[y], b[x]), cmp.Compare(x, y)) })
	places := placesBy(ids, func(x, y int64) bool { return b[x] == b[y] })
	out := make(map[int64]float64, len(ids))
	for i, id := range ids {
		out[id] = places[i].Mid()
	}
	return out
}

// JuryRow is one question's standing with the expert jury.
type JuryRow struct {
	ID      int64
	RankSum float64
	Points  int
	Place   Places
}

// Jury ranks the shortlist by the sum of the jurors' ranks, lower first (the
// Borda count), and breaks a tie on the plain sum of marks.
func Jury(ids []int64, ballots []Ballot) []JuryRow {
	ranks := make([]map[int64]float64, len(ballots))
	for i, b := range ballots {
		ranks[i] = b.Ranks()
	}
	rows := make([]JuryRow, len(ids))
	for i, id := range ids {
		rows[i].ID = id
		for k, b := range ballots {
			rows[i].RankSum += ranks[k][id]
			rows[i].Points += b[id]
		}
	}
	slices.SortFunc(rows, func(a, b JuryRow) int {
		return cmp.Or(cmp.Compare(a.RankSum, b.RankSum), cmp.Compare(b.Points, a.Points), cmp.Compare(a.ID, b.ID))
	})
	places := placesBy(rows, func(a, b JuryRow) bool { return a.RankSum == b.RankSum && a.Points == b.Points })
	for i := range rows {
		rows[i].Place = places[i]
	}
	return rows
}

// PopularVote is one voter's three picks, best first.
type PopularVote [3]int64

// PopularPoints are what the picks are worth, first to third.
var PopularPoints = [3]int{5, 4, 3}

type PopularRow struct {
	ID     int64
	Points int
	Place  Places
}

// Popular sums the points of the open vote. Equal points share a place, so
// several questions can win it at once.
func Popular(ids []int64, votes []PopularVote) []PopularRow {
	points := map[int64]int{}
	for _, v := range votes {
		for i, id := range v {
			points[id] += PopularPoints[i]
		}
	}
	rows := make([]PopularRow, len(ids))
	for i, id := range ids {
		rows[i] = PopularRow{ID: id, Points: points[id]}
	}
	slices.SortFunc(rows, func(a, b PopularRow) int { return cmp.Or(cmp.Compare(b.Points, a.Points), cmp.Compare(a.ID, b.ID)) })
	places := placesBy(rows, func(a, b PopularRow) bool { return a.Points == b.Points })
	for i := range rows {
		rows[i].Place = places[i]
	}
	return rows
}

// Result is a shortlisted question's final standing in both votes.
type Result struct {
	ID      int64
	QY      int
	Jury    JuryRow
	Popular PopularRow
}

// Results joins the two votes and orders the shortlist the way the results
// page lists it: by the better of the two places, then by the jury's place,
// then by number.
func Results(qy map[int64]int, ballots []Ballot, votes []PopularVote) []Result {
	ids := make([]int64, 0, len(qy))
	for id := range qy {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	pop := map[int64]PopularRow{}
	for _, r := range Popular(ids, votes) {
		pop[r.ID] = r
	}
	out := make([]Result, 0, len(ids))
	for _, j := range Jury(ids, ballots) {
		out = append(out, Result{ID: j.ID, QY: qy[j.ID], Jury: j, Popular: pop[j.ID]})
	}
	slices.SortStableFunc(out, func(a, b Result) int {
		return cmp.Or(
			cmp.Compare(min(a.Jury.Place.Mid(), a.Popular.Place.Mid()), min(b.Jury.Place.Mid(), b.Popular.Place.Mid())),
			cmp.Compare(a.Jury.Place.Mid(), b.Jury.Place.Mid()),
			cmp.Compare(a.QY, b.QY),
		)
	})
	return out
}
