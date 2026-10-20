package contest

import (
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"
	"time"
)

var c2025 = Last2025

func at(day string, hour int) time.Time {
	t, _ := Day(day)
	return t.Add(time.Duration(hour) * time.Hour)
}

func TestNextKeepsTheDayOfTheWeek(t *testing.T) {
	n := c2025.Next()
	if n.Year != 2026 || n.NominationStart != "2026-11-09" || n.ResultsDate != "2027-01-25" {
		t.Errorf("got %+v", n.Dates)
	}
	if n.QuestionsFrom != "2025-11-01" || n.QuestionsTo != "2026-10-31" {
		t.Errorf("the tournament window should move a calendar year: %+v", n.Dates)
	}
	for i, p := range c2025.Dates.Ptrs() {
		if i < 2 {
			continue
		}
		a, _ := Day(*p)
		b, _ := Day(*n.Dates.Ptrs()[i])
		if a.Weekday() != b.Weekday() {
			t.Errorf("%s fell on %s, %s falls on %s", *p, a.Weekday(), *n.Dates.Ptrs()[i], b.Weekday())
		}
	}
}

func TestStageAt(t *testing.T) {
	cases := []struct {
		day  string
		hour int
		want Stage
	}{
		{"2025-11-09", 23, Before},
		{"2025-11-10", 0, Nomination},
		{"2025-11-24", 23, Nomination},
		{"2025-11-25", 0, Prelim},
		{"2026-01-01", 12, Factcheck},
		{"2026-01-25", 23, Final},
		{"2026-01-26", 1, Counting},
	}
	for _, tc := range cases {
		if got := c2025.StageAt(at(tc.day, tc.hour)); got != tc.want {
			t.Errorf("%s %02d:00: got %d, want %d", tc.day, tc.hour, got, tc.want)
		}
	}
	done := c2025
	done.ResultsPublishedAt = "2026-01-26T10:00:00Z"
	if done.StageAt(at("2026-01-26", 12)) != Done {
		t.Error("published results should end the contest")
	}
}

func TestPortionsOpenOneEveryThreeDays(t *testing.T) {
	const total = 11
	for _, tc := range []struct {
		day  string
		want int
	}{{"2025-11-24", 0}, {"2025-11-25", 1}, {"2025-11-27", 1}, {"2025-11-28", 2}, {"2025-12-31", 11}, {"2026-01-02", total}} {
		if got := c2025.PortionsOpen(at(tc.day, 12), total); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.day, got, tc.want)
		}
	}
	if c2025.Portion(36) != 1 || c2025.Portion(37) != 2 {
		t.Error("portion boundaries are off")
	}
	if got := c2025.PortionOpens(2).Format(DateLayout); got != "2025-11-28" {
		t.Errorf("portion 2 opens %s", got)
	}
}

func TestShuffleIsStablePerSeed(t *testing.T) {
	ids := []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	a, b := Shuffle(ids, "2026/1/7"), Shuffle(ids, "2026/1/7")
	if !reflect.DeepEqual(a, b) {
		t.Error("the same seed gave two orders")
	}
	if reflect.DeepEqual(a, Shuffle(ids, "2026/1/8")) {
		t.Error("two seeds gave the same order")
	}
	slices.Sort(a)
	if !reflect.DeepEqual(a, ids) {
		t.Error("shuffle lost or added a question")
	}
}

func TestCountedUsesAReserveOnlyForADuplicate(t *testing.T) {
	// Main picks first, then reserves; two main picks per nominator.
	alice := []Slot{{Question: 1}, {Question: 2}, {Question: 10}}
	bob := []Slot{{Question: 2}, {Question: 3}, {Question: 11}}
	carol := []Slot{{Question: 4, Skip: true}, {Question: 5}, {Question: 12}}
	got := Counted([][]Slot{alice, bob, carol}, 2)
	if want := []int64{1, 2, 3, 11, 5, 12}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDealNumbersEveryQuestionOnce(t *testing.T) {
	got := Deal([]int64{5, 6, 7}, rand.New(rand.NewPCG(1, 2)))
	var nums []int
	for _, n := range got {
		nums = append(nums, n)
	}
	slices.Sort(nums)
	if !reflect.DeepEqual(nums, []int{1, 2, 3}) || len(got) != 3 {
		t.Errorf("got %v", got)
	}
}
