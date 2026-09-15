package schemedsl

import "testing"

// Эрудит-Секстет: how many players a team sends to a theme is a Protocol param
// like `themes`, so it cascades defaults < Block < Round and lands in the
// stage's config for the loader to read back.
const sextetSrc = `
[defaults]
venues: 4

[scheme]
title: Групповой этап
kind: roundrobin
groups: 1
group_size: 8
themes: 12
proceeding_participants: 8
---
title: Плей-офф
kind: single_elimination
participants: 8
match_size: 4
winning_places: 2
themes: 12
reseed: true
players: 2
players.r2: 1
`

func TestSextetSeatsCascade(t *testing.T) {
	scheme := compileSrc(t, sextetSrc, Input{Slug: "es", Title: "ЭС", GameType: "es"})
	stages := matchStages(scheme)
	if len(stages) != 3 {
		t.Fatalf("этапов = %d, want 3 (группы + два раунда плей-офф)", len(stages))
	}
	// The group stage says nothing, so it plays at the format's own default.
	if got := stageConfig(t, stages[0])["players"]; got != float64(3) {
		t.Fatalf("группы: players = %v, want 3", got)
	}
	if got := stageConfig(t, stages[1])["players"]; got != float64(2) {
		t.Fatalf("плей-офф r1: players = %v, want 2 (block override)", got)
	}
	if got := stageConfig(t, stages[2])["players"]; got != float64(1) {
		t.Fatalf("плей-офф r2: players = %v, want 1 (round override)", got)
	}
}

// ЭК knows the same key and seats one player by default: a квартет sends one
// representative to a theme, and the stage config says so rather than leaving
// the cap to be guessed downstream.
func TestQuartetSeatsOne(t *testing.T) {
	scheme := compileSrc(t, `
[scheme]
kind: roundrobin
groups: 1
group_size: 4
themes: 12
`, Input{Slug: "ek", Title: "ЭК", GameType: "ek"})
	stages := matchStages(scheme)
	if got := stageConfig(t, stages[0])["players"]; got != float64(1) {
		t.Fatalf("ЭК: players = %v, want 1", got)
	}
}

// A format with no seating — ЧГК — must not learn the key: an unknown key is
// a scheme error, which is how a typo in `players` is caught in ЭК too.
func TestSeatsKeyIsPerFormat(t *testing.T) {
	doc, err := Parse(`
[scheme]
kind: flat
participants: 4
questions: 36
players: 2
`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Compile(doc, Input{Slug: "od", Title: "ОД", GameType: "od"}); err == nil {
		t.Fatal("ЧГК accepted players:, which it does not seat")
	}
}
