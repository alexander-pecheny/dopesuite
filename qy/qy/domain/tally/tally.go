// Package tally is the arithmetic of the rules: who makes the shortlist after
// the first stage, and how the final stage ranks it. It is pure; the server
// feeds it rows and shows what it answers.
package tally

import (
	"cmp"
	"slices"
)

// FavouritePoints is what a juror's favourite question gets from them in the
// first stage; any other question they liked gets one point.
const FavouritePoints = 3

// Count is one question's first-stage points from one jury.
type Count struct {
	ID int64
	QY int
	N  int
}

// Cut takes the n questions with the most points, best first. Questions in
// skip are passed over, and a question nobody liked is never taken. When the
// nth question and the next one have the same points, Cut takes only the
// questions above them and returns every question with those points as tied:
// a tie-break round decides which of them fill the places left.
func Cut(counts []Count, n int, skip map[int64]bool) (in, tied []Count) {
	sorted := slices.Clone(counts)
	slices.SortStableFunc(sorted, func(a, b Count) int {
		return cmp.Or(cmp.Compare(b.N, a.N), cmp.Compare(a.QY, b.QY))
	})
	var open []Count
	for _, c := range sorted {
		if !skip[c.ID] && c.N > 0 {
			open = append(open, c)
		}
	}
	if len(open) <= n {
		return open, nil
	}
	if n == 0 || open[n-1].N != open[n].N {
		return open[:n], nil
	}
	edge := open[n-1].N
	for _, c := range open {
		if c.N > edge {
			in = append(in, c)
		} else if c.N == edge {
			tied = append(tied, c)
		}
	}
	return in, tied
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
	// Tiebreak is set on a question that won its place in a tie-break round.
	Tiebreak bool
}

// Tie is a run of questions with equal points at one jury's cut, more of them
// than there are places left. The expert jury marks each of them from 1 to 10,
// and the round is counted like the final: by the sum of ranks, then by the
// sum of marks.
type Tie struct {
	Why    string
	Points int
	// Places is how many of the tied questions make the shortlist.
	Places int
	IDs    []int64
	// Rows is the round's count, best first; Ballots is how many jurors marked
	// every tied question. Only such a ballot counts.
	Rows    []JuryRow
	Ballots int
	// Settled is set once the count puts exactly Places questions above the rest.
	Settled bool
}

// settle counts the tie-break marks for t.
func (t *Tie) settle(marks []Ballot) {
	var ballots []Ballot
	for _, m := range marks {
		b := Ballot{}
		for _, id := range t.IDs {
			if m[id] > 0 {
				b[id] = m[id]
			}
		}
		if len(b) == len(t.IDs) {
			ballots = append(ballots, b)
		}
	}
	t.Ballots = len(ballots)
	if t.Ballots == 0 {
		return
	}
	t.Rows = Jury(t.IDs, ballots)
	t.Settled = t.Rows[t.Places-1].Place.To == t.Places
}

// Outcome is the shortlist as the likes and the tie-break marks make it.
type Outcome struct {
	Picks []Pick
	// Ties are the cuts the likes leave tied, the expert jury's first. While the
	// expert jury's tie is not settled, the people's cut is made as if every
	// question in it were on the shortlist, so it may still change.
	Ties []Tie
}

// Settled reports whether every tie is settled, so the shortlist is final.
func (o Outcome) Settled() bool {
	for _, t := range o.Ties {
		if !t.Settled {
			return false
		}
	}
	return true
}

// Contenders are the shortlist with every tied question: the preliminary
// shortlist the factcheck is about.
func (o Outcome) Contenders() []int64 {
	var out []int64
	seen := map[int64]bool{}
	add := func(id int64) {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, p := range o.Picks {
		add(p.ID)
	}
	for _, t := range o.Ties {
		for _, id := range t.IDs {
			add(id)
		}
	}
	return out
}

// Shortlist is the rule of the first stage: the expert jury's top expertN,
// then the people's jury's top peopleN among the rest, then the pinned
// questions. A tie at either cut goes to a tie-break round, whose marks are
// the expert jurors' ballots in marks.
func Shortlist(cands []Candidate, expertN, peopleN int, marks []Ballot) Outcome {
	skip := map[int64]bool{}
	var expert, people []Count
	for _, c := range cands {
		if c.Excluded {
			skip[c.ID] = true
		}
		expert = append(expert, Count{c.ID, c.QY, c.Expert})
		people = append(people, Count{c.ID, c.QY, c.People})
	}
	var o Outcome
	take := func(id int64, qy int, why string, tiebreak bool) {
		o.Picks = append(o.Picks, Pick{id, qy, why, tiebreak})
		skip[id] = true
	}
	qyOf := map[int64]int{}
	for _, c := range cands {
		qyOf[c.ID] = c.QY
	}
	cut := func(cs []Count, n int, why string) {
		in, tied := Cut(cs, n, skip)
		for _, c := range in {
			take(c.ID, c.QY, why, false)
		}
		if len(tied) == 0 {
			return
		}
		t := Tie{Why: why, Points: tied[0].N, Places: n - len(in)}
		for _, c := range tied {
			t.IDs = append(t.IDs, c.ID)
		}
		t.settle(marks)
		o.Ties = append(o.Ties, t)
		if !t.Settled {
			for _, id := range t.IDs {
				skip[id] = true
			}
			return
		}
		for _, r := range t.Rows[:t.Places] {
			take(r.ID, qyOf[r.ID], why, true)
		}
	}
	cut(expert, expertN, ByExpert)
	cut(people, peopleN, ByPeople)
	for _, c := range cands {
		if c.Pinned && !skip[c.ID] {
			take(c.ID, c.QY, ByPin, false)
		}
	}
	return o
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
