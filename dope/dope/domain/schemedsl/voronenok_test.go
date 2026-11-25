package schemedsl

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"dope/dope/domain/games"
)

// Both age groups of the Интеллектуальный воронёнок's Вопросики быстрые
// compile for an odd field: Ш ends in a final of three бои between the
// upper final's winner and IV.1's, М in one бой of nine questions.
func TestVoronenokSchemesCompile(t *testing.T) {
	for file, final := range map[string]string{"vb-sh.dsl": "s3-final", "vb-m.dsl": "s2-r5"} {
		src, err := os.ReadFile("../../../scripts/voronenok/" + file)
		if err != nil {
			t.Fatal(err)
		}
		scheme := compileSrc(t, string(src), Input{Slug: "vb", Title: "Вопросики быстрые", GameType: games.Brain, Entrants: troikaEntrants(23)})
		round7 := 0
		for _, stage := range scheme.Stages {
			if stage.Code == "s1-r7" || strings.HasPrefix(stage.Code, "s1-r7-w") {
				round7 += len(stage.Matches)
			}
		}
		if got := round7; got != 12 {
			t.Errorf("%s: round 7 has %d бои, want 11 and the empty table", file, got)
		}
		stage := stageByCode(t, scheme, final)
		wantBouts, wantQuestions := 3, 5.0
		if file == "vb-m.dsl" {
			wantBouts, wantQuestions = 1, 9
		}
		if len(stage.Matches) != wantBouts {
			t.Errorf("%s: final has %d бои, want %d", file, len(stage.Matches), wantBouts)
		}
		if q := stageConfig(t, stage)["questions"]; q != wantQuestions {
			t.Errorf("%s: final plays %v questions, want %v", file, q, wantQuestions)
		}
		if file == "vb-m.dsl" && stage.Title != "Плей-офф. Финал" {
			t.Errorf("%s: the final is titled %q, want «Плей-офф. Финал»", file, stage.Title)
		}
	}
}

// The Эрудит-квартет brackets seat exactly the regulations' appendices: the
// opening a snake of the seed, every later бой the places it prints, the
// бои called by the letters the appendices give them.
func TestVoronenokEKBrackets(t *testing.T) {
	for file, want := range map[string][]string{
		"ek-m.dsl": {
			"A: 1 8 9 16", "B: 2 7 10 15", "C: 3 6 11 14", "D: 4 5 12 13",
			"E: A1 B2 C1 D2", "F: A2 B1 C2 D1",
			"Финал: E1 F1 E2 F2",
		},
		"ek-sh.dsl": {
			"A: 1 16 17 32", "B: 2 15 18 31", "C: 3 14 19 30", "D: 4 13 20 29",
			"E: 5 12 21 28", "F: 6 11 22 27", "G: 7 10 23 26", "H: 8 9 24 25",
			"I: A1 D2 E2 H1", "J: B1 C2 F2 G1", "K: C1 B2 G2 F1", "L: D1 A2 H2 E1",
			"M: I1 J2 K2 L1", "N: J1 I2 L2 K1",
			"Финал: M1 M2 N1 N2",
		},
	} {
		src, err := os.ReadFile("../../../scripts/voronenok/" + file)
		if err != nil {
			t.Fatal(err)
		}
		teams := 16
		if file == "ek-sh.dsl" {
			teams = 32
		}
		scheme := compileSrc(t, string(src), Input{Slug: "ek", Title: "Эрудит-квартет", GameType: games.EK, Entrants: troikaEntrants(teams)})
		letters := map[string]string{}
		var got []string
		for _, stage := range scheme.Stages {
			for _, match := range stage.Matches {
				letters[match.Code] = match.Letter
				var seats []string
				for _, slot := range match.Slots {
					switch {
					case slot.Seed != nil:
						seats = append(seats, strconv.Itoa(slot.Seed.Number))
					case slot.FromMatch != nil:
						seats = append(seats, letters[slot.FromMatch.Match]+strconv.Itoa(slot.FromMatch.Place))
					default:
						seats = append(seats, "?")
					}
				}
				got = append(got, match.Letter+": "+strings.Join(seats, " "))
			}
		}
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("%s:\n%s\nwant\n%s", file, strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	}
}

// A written single elimination refuses what would seat its rounds a second
// time, and a winning place no later бой takes.
func TestWrittenSingleEliminationRefuses(t *testing.T) {
	base := "[scheme]\nkind: single_elimination\nparticipants: 8\nmatch_size: 4\nwinning_places: 2\n"
	for name, src := range map[string]string{
		"bronze": base + "bronze: true\nbouts.r2: [Финал: 1-1 2-2 1-2 2-1]\n",
		"unused": base + "bouts.r2: [Финал: 1-1 2-2 1-2]\n",
	} {
		doc, err := Parse(src)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Compile(doc, Input{Slug: "ek", Title: "ЭК", GameType: games.EK, Entrants: troikaEntrants(8)}); err == nil {
			t.Errorf("%s: compiled", name)
		} else {
			t.Logf("%s: %v", name, err)
		}
	}
}

// rotate_venues moves the venue a short last wave leaves empty one venue back
// each round, the empty table's bout just before it: 37 teams at ten venues
// play a round as ten bouts and nine, and the organizers' table leaves venue
// 10 idle in round 1, venue 9 in round 2, and so on round the venues.
func TestSwissRotatesTheShortWavesEmptyVenue(t *testing.T) {
	src := `
[defaults]
venues: 10
questions: 5

[scheme]
kind: swiss_classic
rounds: 11
rotate_venues: true
`
	scheme := compileSrc(t, src, Input{Slug: "vb", Title: "Вопросики быстрые", GameType: games.Brain, Entrants: troikaEntrants(37)})
	for r := 1; r <= 11; r++ {
		first := stageByCode(t, scheme, "s1-r"+strconv.Itoa(r)+"-w1")
		for i, match := range first.Matches {
			if match.Venue != i+1 {
				t.Errorf("round %d, wave 1: %s at venue %d, want %d", r, match.Code, match.Venue, i+1)
			}
		}
		second := stageByCode(t, scheme, "s1-r"+strconv.Itoa(r)+"-w2")
		if len(second.Matches) != 9 {
			t.Fatalf("round %d, wave 2 has %d бои, want 9", r, len(second.Matches))
		}
		empty := 10 - (r-1)%10
		used := map[int]bool{}
		for _, match := range second.Matches {
			used[match.Venue] = true
		}
		if used[empty] || len(used) != 9 {
			t.Errorf("round %d, wave 2 plays at %v, want every venue but %d", r, used, empty)
		}
		table := second.Matches[len(second.Matches)-1]
		if want := (empty+8)%10 + 1; table.Venue != want {
			t.Errorf("round %d: the empty table at venue %d, want %d, just before the empty venue %d", r, table.Venue, want, empty)
		}
	}
}

// Without rotate_venues the short wave keeps its venues every round: the
// wave's bouts from venue 1, the venues after them empty.
func TestSwissKeepsTheShortWaveInPlace(t *testing.T) {
	src := `
[defaults]
venues: 10

[scheme]
kind: swiss_classic
rounds: 3
`
	scheme := compileSrc(t, src, Input{Slug: "vb", Title: "Вопросики быстрые", GameType: games.Brain, Entrants: troikaEntrants(37)})
	for r := 1; r <= 3; r++ {
		second := stageByCode(t, scheme, "s1-r"+strconv.Itoa(r)+"-w2")
		for i, match := range second.Matches {
			if match.Venue != i+1 {
				t.Errorf("round %d, wave 2: %s at venue %d, want %d", r, match.Code, match.Venue, i+1)
			}
		}
	}
}

// The Воронёнок's schemes switch on the formats' own keys: ЭК reads its
// shootout themes after the first from 50 down (2.1.11), and the Вопросики
// быстрые draw their bouts on the grid with the winner in bold, not places.
func TestVoronenokFormatKeys(t *testing.T) {
	for file, want := range map[string]struct {
		gameType, key string
		teams         int
	}{
		"ek-m.dsl":  {games.EK, "reverseShootout", 16},
		"ek-sh.dsl": {games.EK, "reverseShootout", 32},
		"vb-m.dsl":  {games.Brain, "boldWinner", 23},
		"vb-sh.dsl": {games.Brain, "boldWinner", 23},
	} {
		src, err := os.ReadFile("../../../scripts/voronenok/" + file)
		if err != nil {
			t.Fatal(err)
		}
		scheme := compileSrc(t, string(src), Input{Slug: "v", Title: "Воронёнок", GameType: want.gameType, Entrants: troikaEntrants(want.teams)})
		for _, stage := range scheme.Stages {
			if len(stage.Matches) == 0 {
				continue
			}
			if got := stageConfig(t, stage)[want.key]; got != true {
				t.Errorf("%s: %s has %s = %v, want true", file, stage.Code, want.key, got)
			}
		}
	}
}
