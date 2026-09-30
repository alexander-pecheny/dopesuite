package schemedsl

import (
	"fmt"
	"strings"
	"testing"

	"dope/dope/storage/store"
)

// Octobearfest's Троечка: 16 groups, a drawn 1/16 of 16 bouts Q1…Q16, then
// the regulations pair R1 = Q1–Q9, R2 = Q2–Q10, … — one bout of either wave —
// and from there on neighbours, S1 = R1–R2.
const troikaPairingSrc = `
[scheme]
title: Групповой этап
kind: roundrobin
groups: 16
group_size: 4
proceeding_participants: 2
themes: 6
metric: total
points: [1, 0.5, 0]
---
title: Плей-офф
kind: single_elimination
participants: 32
draw: true
bronze: true
themes: 8
metric: total
points: [1, 0, 0]
%s
`

func troikaPairingScheme(t *testing.T, pairing string) store.FestScheme {
	t.Helper()
	in := Input{Slug: "troika", GameType: "troika"}
	for i := 0; i < 64; i++ {
		in.Entrants = append(in.Entrants, store.SchemeSlot{Seed: &store.SchemeSeedRef{Basket: 1, Number: i + 1}})
	}
	return compileSrc(t, fmt.Sprintf(troikaPairingSrc, pairing), in)
}

// fedBy is which bouts a бой takes its winners from.
func fedBy(t *testing.T, scheme store.FestScheme, code string) string {
	t.Helper()
	for _, stage := range scheme.Stages {
		for _, match := range stage.Matches {
			if match.Code != code {
				continue
			}
			var from []string
			for _, slot := range match.Slots {
				if slot.FromMatch == nil {
					t.Fatalf("%s: slot %+v is not a winner", code, slot)
				}
				from = append(from, slot.FromMatch.Match)
			}
			return strings.Join(from, " ")
		}
	}
	t.Fatalf("no бой %s", code)
	return ""
}

func TestSingleEliminationPairingHalves(t *testing.T) {
	adjacent := troikaPairingScheme(t, "")
	if got := fedBy(t, adjacent, "s2-r2-m1"); got != "s2-r1-m1 s2-r1-m2" {
		t.Fatalf("by default 1/8 bout 1 takes %s, want bouts 1 and 2", got)
	}
	halves := troikaPairingScheme(t, "pairing.r2: halves")
	for i, want := range map[int]string{1: "s2-r1-m1 s2-r1-m9", 2: "s2-r1-m2 s2-r1-m10", 8: "s2-r1-m8 s2-r1-m16"} {
		if got := fedBy(t, halves, fmt.Sprintf("s2-r2-m%d", i)); got != want {
			t.Errorf("R%d takes %s, want %s", i, got, want)
		}
	}
	// S1 = R1–R2, T1 = S1–S2: the later rounds stay neighbours.
	if got := fedBy(t, halves, "s2-r3-m1"); got != "s2-r2-m1 s2-r2-m2" {
		t.Errorf("1/4 bout 1 takes %s, want R1 and R2", got)
	}
	if got := fedBy(t, halves, "s2-semifinal-m1"); got != "s2-r3-m1 s2-r3-m2" {
		t.Errorf("semifinal 1 takes %s, want S1 and S2", got)
	}
	// A plain pairing: halves pairs every round it can.
	every := troikaPairingScheme(t, "pairing: halves")
	if got := fedBy(t, every, "s2-r3-m1"); got != "s2-r2-m1 s2-r2-m5" {
		t.Errorf("pairing: halves — 1/4 bout 1 takes %s, want R1 and R5", got)
	}

	for _, bad := range []string{"pairing: sideways", "pairing.r1: halves"} {
		doc, err := Parse(fmt.Sprintf(troikaPairingSrc, bad))
		if err != nil {
			t.Fatal(err)
		}
		in := Input{Slug: "troika", GameType: "troika"}
		for i := 0; i < 64; i++ {
			in.Entrants = append(in.Entrants, store.SchemeSlot{Seed: &store.SchemeSeedRef{Basket: 1, Number: i + 1}})
		}
		if _, err := Compile(doc, in); err == nil || !strings.Contains(err.Error(), "pairing") {
			t.Errorf("%s: %v, want a pairing error", bad, err)
		}
	}
}
