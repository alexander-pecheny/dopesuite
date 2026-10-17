// Package contest is one year's Question of the Year as the rules lay it out: the
// dates of its stages, the portions the first stage is read in, and which
// nominations count.
package contest

import (
	"hash/fnv"
	"math/rand/v2"
	"slices"
	"time"
)

const (
	moscowOffset = 3 * time.Hour
	// A contest moves on by whole weeks, so each stage keeps its weekday.
	weeksPerYear = 52
	daysPerWeek  = 7
	hoursPerDay  = 24
)

// Moscow is the time zone every date of the rules is read in.
var Moscow = time.FixedZone("MSK", int(moscowOffset.Seconds()))

const DateLayout = "2006-01-02"

// Dates are calendar days, each stage inclusive at both ends.
type Dates struct {
	QuestionsFrom, QuestionsTo        string
	NominationStart, NominationEnd    string
	PrelimStart, PrelimEnd            string
	FactcheckStart, FactcheckEnd      string
	FinalStart, FinalEnd, ResultsDate string
}

// Ptrs lists the dates in the order the settings form shows them.
func (d *Dates) Ptrs() []*string {
	return []*string{
		&d.QuestionsFrom, &d.QuestionsTo,
		&d.NominationStart, &d.NominationEnd,
		&d.PrelimStart, &d.PrelimEnd,
		&d.FactcheckStart, &d.FactcheckEnd,
		&d.FinalStart, &d.FinalEnd, &d.ResultsDate,
	}
}

type Contest struct {
	Year int
	Dates
	MainSlots, ReserveSlots    int
	PortionSize, PortionDays   int
	ExpertCutoff, PeopleCutoff int
	// Each moment below is empty until an admin takes that step.
	NumberedAt, ShortlistFrozenAt, ResultsPublishedAt string
}

// Last2025 is the last contest run with the old scripts and Google Sheets, as
// variables2025.yaml in the vopros_goda repo recorded it. The first contest
// set up here is its Next.
var Last2025 = Contest{
	Year: 2025,
	Dates: Dates{
		QuestionsFrom: "2024-11-01", QuestionsTo: "2025-10-31",
		NominationStart: "2025-11-10", NominationEnd: "2025-11-24",
		PrelimStart: "2025-11-25", PrelimEnd: "2025-12-31",
		FactcheckStart: "2026-01-01", FactcheckEnd: "2026-01-18",
		FinalStart: "2026-01-19", FinalEnd: "2026-01-25", ResultsDate: "2026-01-26",
	},
	MainSlots: 5, ReserveSlots: 3,
	PortionSize: 36, PortionDays: 3,
	ExpertCutoff: 10, PeopleCutoff: 3,
}

// Stage is where the contest stands on a given day.
type Stage int

const (
	Before Stage = iota
	Nomination
	// Review is the gap, if any, between nomination and the first stage.
	Review
	Prelim
	Factcheck
	Final
	// Counting is after the final vote and before the results are out.
	Counting
	Done
)

func Day(s string) (time.Time, bool) {
	t, err := time.ParseInLocation(DateLayout, s, Moscow)
	return t, err == nil
}

// within reports whether t falls on a day from first to last, inclusive.
func within(t time.Time, first, last string) bool {
	a, ok1 := Day(first)
	b, ok2 := Day(last)
	return ok1 && ok2 && !t.Before(a) && t.Before(b.AddDate(0, 0, 1))
}

func (c Contest) StageAt(t time.Time) Stage {
	t = t.In(Moscow)
	switch {
	case c.ResultsPublishedAt != "":
		return Done
	case within(t, c.NominationStart, c.NominationEnd):
		return Nomination
	case within(t, c.PrelimStart, c.PrelimEnd):
		return Prelim
	case within(t, c.FactcheckStart, c.FactcheckEnd):
		return Factcheck
	case within(t, c.FinalStart, c.FinalEnd):
		return Final
	}
	if start, ok := Day(c.NominationStart); !ok || t.Before(start) {
		return Before
	}
	if start, ok := Day(c.PrelimStart); ok && t.Before(start) {
		return Review
	}
	return Counting
}

// PortionsOpen is how many portions of the first stage a juror can read on
// day t: one more every PortionDays from the stage's first day, and all of
// them once the stage is over.
func (c Contest) PortionsOpen(t time.Time, total int) int {
	start, ok := Day(c.PrelimStart)
	if !ok || t.Before(start) || c.PortionDays < 1 {
		return 0
	}
	if c.StageAt(t) != Prelim {
		return total
	}
	days := int(t.Sub(start).Hours() / hoursPerDay)
	return min(total, days/c.PortionDays+1)
}

// Portion is the 1-based portion a QY number belongs to.
func (c Contest) Portion(qy int) int {
	if c.PortionSize < 1 || qy < 1 {
		return 1
	}
	return (qy-1)/c.PortionSize + 1
}

// PortionOpens is the first day of portion k.
func (c Contest) PortionOpens(k int) time.Time {
	start, _ := Day(c.PrelimStart)
	return start.AddDate(0, 0, (k-1)*c.PortionDays)
}

// Next is the following year's contest with every date moved 52 weeks on, so
// each stage starts on the same day of the week as it did. The window of
// eligible tournaments moves by a calendar year instead.
func (c Contest) Next() Contest {
	n := c
	n.Year++
	n.NumberedAt, n.ShortlistFrozenAt, n.ResultsPublishedAt = "", "", ""
	ptrs := n.Dates.Ptrs()
	for i, p := range ptrs {
		t, ok := Day(*p)
		if !ok {
			continue
		}
		if i < 2 {
			t = t.AddDate(1, 0, 0)
		} else {
			t = t.AddDate(0, 0, daysPerWeek*weeksPerYear)
		}
		*p = t.Format(DateLayout)
	}
	return n
}

// Shuffle orders ids the way one reader sees them. The order depends only on
// the seed, so a juror who reloads the page finds the questions where they
// left them, and two jurors see different orders.
func Shuffle(ids []int64, seed string) []int64 {
	h := fnv.New64a()
	h.Write([]byte(seed))
	s := h.Sum64()
	out := slices.Clone(ids)
	slices.Sort(out)
	rand.New(rand.NewPCG(s, s>>1|1)).Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

// Slot is one nominator's pick, as the counting rule needs it.
type Slot struct {
	Question int64
	// Skip is a question that cannot count: rejected by an admin.
	Skip bool
}

// Counted applies the nomination rule as the old script did. Nominators are
// taken in the order they first nominated; each walks their slots in order,
// main picks first, passing over any question already counted for someone
// before them or rejected, until main of theirs count. A reserve therefore
// counts only in place of a main pick that did not: a duplicate, a rejected
// question, or a slot left empty.
func Counted(nominators [][]Slot, main int) []int64 {
	seen := map[int64]bool{}
	var out []int64
	for _, slots := range nominators {
		taken := 0
		for _, s := range slots {
			if taken == main {
				break
			}
			if s.Skip || seen[s.Question] {
				continue
			}
			seen[s.Question] = true
			out = append(out, s.Question)
			taken++
		}
	}
	return out
}

// Deal hands out QY numbers 1..n over the counted questions in a random
// order. The result maps question to number.
func Deal(counted []int64, r *rand.Rand) map[int64]int {
	order := slices.Clone(counted)
	r.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
	out := make(map[int64]int, len(order))
	for i, id := range order {
		out[id] = i + 1
	}
	return out
}
