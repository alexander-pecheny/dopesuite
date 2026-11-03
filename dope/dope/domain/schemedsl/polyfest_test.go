package schemedsl

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"dope/dope/domain/structure"
	"dope/dope/storage/store"
)

// Polyfest 2026 (scripts/polyfest): the play-offs of personal SI and КИнСбФ,
// bout by bout as their regulations' appendices print them, the upper and
// lower brackets as the appendices place them, and КИнСбФ's group order.

func polyfestScheme(t *testing.T, file, gameType string, entrants int) store.FestScheme {
	t.Helper()
	src, err := os.ReadFile("../../../scripts/polyfest/" + file)
	if err != nil {
		t.Fatal(err)
	}
	return compileSrc(t, string(src), Input{Slug: "x", GameType: gameType, Entrants: seedEntrants(entrants)})
}

func seedEntrants(n int) []store.SchemeSlot {
	out := make([]store.SchemeSlot, n)
	for i := range out {
		out[i] = store.SchemeSlot{Seed: &store.SchemeSeedRef{Basket: 1, Number: i + 1}, Label: fmt.Sprint("P", i+1)}
	}
	return out
}

// playoffBouts renders the play-off's бои as «round bracket name: seats», a
// seat as the appendix writes it: a group's place («A1») or a бой's place
// («E-1»). The grand final has no bracket and shows «-».
func playoffBouts(t *testing.T, scheme store.FestScheme) []string {
	t.Helper()
	name := map[string]string{}
	var out []string
	for _, stage := range matchStages(scheme) {
		if stage.Grain.Block != "s2" {
			continue // the group stage
		}
		for _, match := range stage.Matches {
			name[match.Code] = strings.TrimPrefix(match.Title, "Бой ")
			if match.Letter != name[match.Code] {
				t.Errorf("бой %s: буква %q", name[match.Code], match.Letter)
			}
			var seats []string
			for _, slot := range match.Slots {
				seats = append(seats, appendixSeat(t, slot, name))
			}
			bracket := match.Bracket
			if bracket == "" {
				bracket = "-"
			}
			out = append(out, fmt.Sprintf("r%d %s %s: %s", match.BlockRound, bracket, name[match.Code], strings.Join(seats, " ")))
		}
	}
	return out
}

func appendixSeat(t *testing.T, slot store.SchemeSlot, name map[string]string) string {
	t.Helper()
	switch {
	case slot.FromMatch != nil:
		return fmt.Sprintf("%s-%d", name[slot.FromMatch.Match], slot.FromMatch.Place)
	case slot.Reseed != nil:
		var group int
		if _, err := fmt.Sscanf(slot.Reseed.Stage, "s1-g%d", &group); err != nil {
			t.Fatalf("место из %q, want из группы", slot.Reseed.Stage)
		}
		return fmt.Sprintf("%c%d", 'A'+group-1, slot.Reseed.Rank)
	}
	t.Fatalf("место %+v ни из группы, ни из боя", slot)
	return ""
}

func assertBouts(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("боёв %d, want %d:\n%s", len(got), len(want), strings.Join(got, "\n"))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("бой %d: %q, want %q", i+1, got[i], want[i])
		}
	}
}

func TestPolyfestSIStudentsFollowsAppendix1(t *testing.T) {
	scheme := polyfestScheme(t, "si-students.dsl", "si", 64)
	assertBouts(t, playoffBouts(t, scheme), []string{
		"r1 upper E: A1 B2 C3 D4", "r1 upper F: B1 C2 D3 A4", "r1 upper G: C1 D2 A3 B4", "r1 upper H: D1 A2 B3 C4",
		"r1 lower I: A5 B6 C7 D8", "r1 lower J: B5 C6 D7 A8", "r1 lower K: C5 D6 A7 B8", "r1 lower L: D5 A6 B7 C8",
		"r2 upper M: E-1 H-1 F-2 G-2", "r2 upper N: F-1 G-1 E-2 H-2",
		"r2 lower O: E-3 F-4 K-1 L-2", "r2 lower P: F-3 E-4 L-1 K-2", "r2 lower Q: G-3 H-4 I-1 J-2", "r2 lower R: H-3 G-4 J-1 I-2",
		"r3 upper S: M-1 M-2 N-1 N-2",
		"r3 lower T: M-3 P-1 Q-1 O-2", "r3 lower U: N-3 O-1 R-1 P-2", "r3 lower V: M-4 N-4 R-2 Q-2",
		"r4 lower W: S-3 T-1 V-2 U-2", "r4 lower X: S-4 U-1 V-1 T-2",
		"r5 lower SF: W-1 X-1 W-2 X-2",
		"r6 - GF: S-1 S-2 SF-1 SF-2",
	})
}

func TestPolyfestSIYouthFollowsAppendix2(t *testing.T) {
	scheme := polyfestScheme(t, "si-youth.dsl", "si", 32)
	assertBouts(t, playoffBouts(t, scheme), []string{
		"r1 upper E: A1 B2 B3 A4", "r1 upper F: B1 A2 A3 B4",
		"r1 lower G: A5 B6 B7 A8", "r1 lower H: B5 A6 A7 B8",
		"r2 upper I: E-1 F-1 E-2 F-2",
		"r2 lower J: E-3 F-4 H-1 G-2", "r2 lower K: F-3 E-4 G-1 H-2",
		"r3 lower L: I-3 J-1 K-2", "r3 lower M: I-4 K-1 J-2",
		"r4 lower SF: L-1 M-1 L-2 M-2",
		"r5 - GF: I-1 I-2 SF-1 SF-2",
	})
}

func TestPolyfestKINSBFStudentsFollowsAppendix1(t *testing.T) {
	scheme := polyfestScheme(t, "kinsbf-students.dsl", "brain", 32)
	assertBouts(t, playoffBouts(t, scheme), []string{
		"r1 upper N: A1 B1", "r1 upper O: C1 D1", "r1 upper P: E1 F1", "r1 upper Q: G1 H1",
		"r1 lower R: A2 B2", "r1 lower S: C2 D2", "r1 lower T: E2 F2", "r1 lower U: G2 H2",
		"r2 upper V: N-1 O-1", "r2 upper W: P-1 Q-1",
		"r2 lower X: N-2 S-1", "r2 lower Y: O-2 R-1", "r2 lower Z: P-2 U-1", "r2 lower AA: Q-2 T-1",
		"r3 upper AB: V-1 W-1",
		"r3 lower AC: V-2 AA-1", "r3 lower AD: W-2 Z-1", "r3 lower AE: X-1 Y-1",
		"r4 lower AF: AB-2 AE-1", "r4 lower AG: AC-1 AD-1",
		"r5 lower AH: AF-1 AG-1",
		"r6 - Финал: AB-1 AH-1",
	})
	assertKINSBFGroups(t, scheme, 8)
}

// The eight-team bracket lets the upper final's loser wait a round: L is the
// lower bracket's two winners, and only M seats I's loser.
func TestPolyfestKINSBFYouthFollowsAppendix1(t *testing.T) {
	scheme := polyfestScheme(t, "kinsbf-youth.dsl", "brain", 16)
	assertBouts(t, playoffBouts(t, scheme), []string{
		"r1 upper E: A1 B1", "r1 upper F: C1 D1",
		"r1 lower G: A2 B2", "r1 lower H: C2 D2",
		"r2 upper I: E-1 F-1",
		"r2 lower J: E-2 H-1", "r2 lower K: F-2 G-1",
		"r3 lower L: J-1 K-1",
		"r4 lower M: I-2 L-1",
		"r5 - Финал: I-1 M-1",
	})
	assertKINSBFGroups(t, scheme, 4)
}

// assertKINSBFGroups checks the groups take the snake of the ОД table (group
// A is ОД #1, #2G, #2G+1, #4G) and play «1–3, 2–4, 1–2, 3–4, 4–1, 2–3».
func assertKINSBFGroups(t *testing.T, scheme store.FestScheme, groups int) {
	t.Helper()
	group := stageByCode(t, scheme, "s1-g1")
	var cfg structure.RRConfig
	if err := json.Unmarshal(group.Config, &cfg); err != nil {
		t.Fatal(err)
	}
	wantSeeds := []int{1, 2 * groups, 2*groups + 1, 4 * groups}
	for i, slot := range cfg.Entrants {
		if slot.Seed == nil || slot.Seed.Number != wantSeeds[i] {
			t.Errorf("группа A, команда %d: %+v, want ОД #%d", i+1, slot.Seed, wantSeeds[i])
		}
	}
	rr, _ := structure.ExpanderFor("rr")
	matches, err := rr.Schedule(group.Config)
	if err != nil {
		t.Fatal(err)
	}
	want := [][2]int{{1, 3}, {2, 4}, {1, 2}, {3, 4}, {4, 1}, {2, 3}}
	if len(matches) != len(want) {
		t.Fatalf("боёв в группе %d, want %d", len(matches), len(want))
	}
	for i, match := range matches {
		got := [2]int{teamIndex(cfg.Entrants, match.Slots[0]), teamIndex(cfg.Entrants, match.Slots[1])}
		if got != want[i] {
			t.Errorf("бой %d группы A: %v, want %v", i+1, got, want[i])
		}
	}
}

func teamIndex(teams []store.SchemeSlot, slot store.SchemeSlot) int {
	for i, team := range teams {
		if team.Seed != nil && slot.Seed != nil && team.Seed.Number == slot.Seed.Number {
			return i + 1
		}
	}
	return 0
}

// A written round must account for everyone still in, once, and only for them.
func TestDEWrittenRoundsRefuseABadTable(t *testing.T) {
	raw, err := os.ReadFile("../../../scripts/polyfest/kinsbf-youth.dsl")
	if err != nil {
		t.Fatal(err)
	}
	good := string(raw)
	for name, edit := range map[string][2]string{
		"место дважды":         {"K: F-2 G-1", "K: F-2 H-1"},
		"нет такого боя":       {"L: J-1 K-1", "L: J-1 Q-1"},
		"нет такого места":     {"L: J-1 K-1", "L: J-1 K-3"},
		"выбывший играет":      {"L: J-1 K-1", "L: J-2 K-1"},
		"место никуда не идёт": {"bouts.r4: [M: I-2 L-1]\nbouts.r5: [Финал: I-1 M-1]", "bouts.r4: [M: I-2 L-1]"},
		"после финала":         {"questions.r5: 9", "bouts.r6: [X: Финал-1 Финал-2]\nquestions.r5: 9"},
		"бой без места":        {"L: J-1 K-1", "L: J-1"},
		"имя занято":           {"L: J-1 K-1", "K: J-1 K-1"},
		"пропущен раунд":       {"bouts.r3: [L: J-1 K-1]\n", ""},
		"первый раунд в bouts": {"bouts.r2:", "bouts.r1: [Z: A1 B1]\nbouts.r2:"},
		"место без номера":     {"L: J-1 K-1", "L: J K-1"},
		"вместе с пересевом":   {"lower_entrants: 4", "lower_entrants: 4\nreseed: true"},
	} {
		src := strings.Replace(good, edit[0], edit[1], 1)
		if src == good {
			t.Fatalf("%s: правка не нашлась", name)
		}
		doc, err := Parse(src)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err := Compile(doc, Input{Slug: "x", GameType: "brain", Entrants: seedEntrants(16)}); err == nil {
			t.Errorf("%s: схема принята", name)
		} else {
			t.Logf("%s: %v", name, err)
		}
	}
}

// The groups' бои are lettered around the names the play-off claims: СИ's
// 80 group бои skip E…X, SF and GF, and no letter is dealt twice.
func TestPolyfestLettersAreUnique(t *testing.T) {
	scheme := polyfestScheme(t, "si-students.dsl", "si", 64)
	seen := map[string]string{}
	for _, stage := range scheme.Stages {
		for _, match := range stage.Matches {
			if match.Letter == "" {
				t.Errorf("%s без буквы", match.Code)
			}
			if other, dup := seen[match.Letter]; dup {
				t.Errorf("буква %s у %s и %s", match.Letter, other, match.Code)
			}
			seen[match.Letter] = match.Code
		}
	}
	if got := len(seen); got != 80+22 {
		t.Errorf("букв %d, want 102", got)
	}
}
