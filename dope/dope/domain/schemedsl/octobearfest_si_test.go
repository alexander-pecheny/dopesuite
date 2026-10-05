package schemedsl

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"dope/dope/domain/structure"
	"dope/dope/storage/store"
)

// Octobearfest IX, личная СИ. Письменный отбор всех заявившихся, 54 лучших в
// шесть групп по девять (бой на троих, четыре круга), четверо из группы в
// Double elimination на 24 — причём 1-е и 2-е места в верхнюю сетку, а 3-и и
// 4-е сразу в нижнюю (регламент, Приложение 3) — и гранд-финал на 12 тем.
const octobearfestSISrc = `
[defaults]
venues: 6
sorting: [points, h2h]

[scheme]
title: Письменный отбор
kind: flat
letters: false
themes: 12
proceeding_participants: 54
sorting: [total, plus, taken50, taken40, taken30, taken20, taken10]
---
title: Групповой этап
kind: roundrobin
slug: group-stage
groups: 6
group_size: 9
match_size: 3
themes: 6
bout.points: seats + 1 - place + total / 1000
reseed: true
sorting: [place_sum]
proceeding_participants: 4
---
title: Double elimination
kind: double_elimination
participants: 24
match_size: 4
winning_places: 2
lower_entrants: 12
opening: [A1 A2 D1 D2, B1 B2 E1 E2, C1 C2 F1 F2, A3 A4 D3 D4, B3 B4 E3 E4, C3 C4 F3 F4]
themes: 8
themes.r6: 12
reseed: true
sorting: [place_sum, total, plus, taken50, taken40, taken30, taken20, taken10]
title.r1: 1 круг
title.r2: 2 круг
title.r3: 3 круг
title.r4: Полуфинал нижней сетки
title.r5: Финал нижней сетки
title.r6: Гранд-финал
`

func TestOctobearfestSIFollowsAppendix3(t *testing.T) {
	scheme := compileSrc(t, octobearfestSISrc, octobearfestSIInput())
	stages := matchStages(scheme)
	// Письменный отбор, шесть групп, шесть кругов DE.
	if len(stages) != 1+6+6 {
		t.Fatalf("этапов = %d, want 13", len(stages))
	}
	de := stages[7:]
	// Приложение 3 бой за боем: 1-й круг WA WB WC LA LB LC, 2-й WD WE LD LE LF,
	// 3-й WF LG LH, полуфинал L — LI LJ, финал L — LK, гранд-финал WG.
	want := [][]int{{4, 4, 4, 4, 4, 4}, {3, 3, 4, 4, 4}, {4, 4, 4}, {3, 3}, {4}, {4}}
	for r, sizes := range want {
		got := make([]int, len(de[r].Matches))
		for i, match := range de[r].Matches {
			got[i] = len(match.Slots)
		}
		if fmt.Sprint(got) != fmt.Sprint(sizes) {
			t.Errorf("круг %d (%s): мест в боях %v, want %v", r+1, de[r].Title, got, sizes)
		}
	}
	if got := themeCount(t, de[5]); got != 12 {
		t.Errorf("тем в гранд-финале = %d, want 12", got)
	}
	// 1-й круг — таблица Приложения 3, а не пересев всех 24: WA = WA1-WA2-WD1-WD2
	// (1-е и 2-е места групп A и D), LA = LA3-LA4-LD3-LD4.
	wantOpening := [][][2]int{
		{{1, 1}, {1, 2}, {4, 1}, {4, 2}}, {{2, 1}, {2, 2}, {5, 1}, {5, 2}}, {{3, 1}, {3, 2}, {6, 1}, {6, 2}},
		{{1, 3}, {1, 4}, {4, 3}, {4, 4}}, {{2, 3}, {2, 4}, {5, 3}, {5, 4}}, {{3, 3}, {3, 4}, {6, 3}, {6, 4}},
	}
	for i, seats := range wantOpening {
		if got, want := groupPlaces(t, scheme, de[0].Matches[i]), fmt.Sprint(seats); got != want {
			t.Errorf("1-й круг, бой %d: %s, want %s", i+1, got, want)
		}
	}
	// Входного пересева больше нет, а между кругами он остаётся: 2-й круг — это
	// W1-W4-W5 и W2-W3-W6 по итогам 1-го.
	for _, stage := range scheme.Stages {
		if stage.Code == "s3-reseed" {
			t.Error("1-й круг по таблице, а входной пересев всё ещё строится")
		}
	}
	if got := slotLabels(de[1].Matches[0]); got != "Пересев-1 Пересев-4 Пересев-5" {
		t.Errorf("первый бой 2-го круга: %s", got)
	}
}

// octobearfestSIInput seeds 58 заявившихся into the письменный отбор.
func octobearfestSIInput() Input {
	in := Input{Slug: "si", GameType: "si"}
	for i := 0; i < 58; i++ {
		in.Entrants = append(in.Entrants, store.SchemeSlot{Seed: &store.SchemeSeedRef{Basket: 1, Number: i + 1}, Label: fmt.Sprint("P", i+1)})
	}
	return in
}

// groupPlaces is who a бой seats as [group, place] pairs of the stage before:
// group n is the n-th group stage (s2-gn).
func groupPlaces(t *testing.T, scheme store.FestScheme, match store.SchemeMatch) string {
	t.Helper()
	var out [][2]int
	for _, slot := range match.Slots {
		ref := slot.Reseed
		if ref == nil {
			t.Fatalf("место %+v не из группы", slot)
		}
		var group int
		if _, err := fmt.Sscanf(ref.Stage, "s2-g%d", &group); err != nil {
			t.Fatalf("место из %q, want из группы: %+v", ref.Stage, ref)
		}
		out = append(out, [2]int{group, ref.Rank})
	}
	return fmt.Sprint(out)
}

// The table must name every place the groups send on, once, and only those.
func TestDEOpeningRefusesABadTable(t *testing.T) {
	good := "[A1 A2 D1 D2, B1 B2 E1 E2, C1 C2 F1 F2, A3 A4 D3 D4, B3 B4 E3 E4, C3 C4 F3 F4]"
	for name, table := range map[string]string{
		"место дважды":         strings.Replace(good, "F4", "F3", 1),
		"нет такой группы":     strings.Replace(good, "F4", "G4", 1),
		"место не проходит":    strings.Replace(good, "F4", "F5", 1),
		"не хватает места":     strings.Replace(good, " F4", "", 1),
		"не буква и не место":  strings.Replace(good, "F4", "4F", 1),
		"то же место иначе":    strings.Replace(good, "F4", "A01", 1),
		"граница боя сдвинута": strings.Replace(good, "D1 D2, B1", "D1, D2 B1", 1),
		"один бой на всех":     strings.ReplaceAll(good, ",", ""),
	} {
		src := strings.Replace(octobearfestSISrc, "opening: "+good, "opening: "+table, 1)
		if src == octobearfestSISrc {
			t.Fatal("таблица в схеме не найдена")
		}
		doc, err := Parse(src)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Compile(doc, octobearfestSIInput()); err == nil || !strings.Contains(err.Error(), "opening") {
			t.Errorf("%s: %v, want ошибку про opening", name, err)
		}
	}
}

// Без lower_entrants тот же блок — прежняя сетка СтудЧР: все 24 начинают с
// двумя жизнями.
func TestDELowerEntrantsDefaultsToNone(t *testing.T) {
	scheme := compileSrc(t, studchrSISrc, Input{Slug: "si", GameType: "si"})
	playoff := matchStages(scheme)[6:]
	if len(playoff) != 7 || len(playoff[0].Matches) != 6 || len(playoff[1].Matches) != 6 {
		t.Fatalf("сетка СтудЧР изменилась: %d кругов", len(playoff))
	}
}

func TestDELowerEntrantsRefusesTheWholeField(t *testing.T) {
	src := "[scheme]\nkind: double_elimination\nparticipants: 8\nmatch_size: 4\nwinning_places: 2\nlower_entrants: 8\n"
	doc, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	in := Input{Slug: "si", GameType: "si"}
	for i := 0; i < 8; i++ {
		in.Entrants = append(in.Entrants, store.SchemeSlot{Seed: &store.SchemeSeedRef{Basket: 1, Number: i + 1}})
	}
	if _, err := Compile(doc, in); err == nil {
		t.Fatal("lower_entrants: 8 из 8 скомпилировался; want ошибку")
	}
}

// Приложение 1 deals the письменный отбор's places into the groups by a snake
// that starts again after places 25–30, which are drawn by lot. No rule makes
// that table, so the scheme writes it out in deal:, and the group stage seats
// the reseed's ranks exactly as written.
func TestOctobearfestSIGroupsFollowAppendix1(t *testing.T) {
	appendix1 := "deal: [1 12 13 24 28 31 42 43 54, 2 11 14 23 25 32 41 44 53, 3 10 15 22 26 33 40 45 52, " +
		"4 9 16 21 30 34 39 46 51, 5 8 17 20 27 35 38 47 50, 6 7 18 19 29 36 37 48 49]\n"
	src := strings.Replace(octobearfestSISrc, "slug: group-stage\n", "slug: group-stage\n"+appendix1, 1)
	in := Input{Slug: "si", GameType: "si"}
	for i := 0; i < 61; i++ {
		in.Entrants = append(in.Entrants, store.SchemeSlot{Seed: &store.SchemeSeedRef{Basket: 1, Number: i + 1}, Label: fmt.Sprint("P", i+1)})
	}
	scheme := compileSrc(t, src, in)
	var groupA []int
	for _, stage := range scheme.Stages {
		if stage.Code != "s2-g1" {
			continue
		}
		var cfg struct {
			Entrants []store.SchemeSlot `json:"entrants"`
		}
		if err := json.Unmarshal(stage.Config, &cfg); err != nil {
			t.Fatal(err)
		}
		for _, slot := range cfg.Entrants {
			groupA = append(groupA, slot.Reseed.Rank)
		}
	}
	if fmt.Sprint(groupA) != "[1 12 13 24 28 31 42 43 54]" {
		t.Fatalf("группа A = %v, want Приложение 1's 1 12 13 24 28 31 42 43 54", groupA)
	}

	for _, bad := range []string{
		"deal: [1 12 13 24 28 31 42 43 54]\n",                         // one group of six
		"deal: [1 12 13, 2 11 14, 3 10 15, 4 9 16, 5 8 17, 6 7 18]\n", // groups of three
		strings.Replace(appendix1, "54,", "53,", 1),                   // 53 twice, 54 never
	} {
		doc, err := Parse(strings.Replace(octobearfestSISrc, "slug: group-stage\n", "slug: group-stage\n"+bad, 1))
		if err == nil {
			_, err = Compile(doc, in)
		}
		if err == nil {
			t.Errorf("deal %q compiled", strings.TrimSpace(bad))
		}
	}
}

// opening: seats a block from the previous block's groups. On a first block
// there is no previous block, and the table would have been dropped without a
// word.
func TestDEOpeningNeedsAPreviousBlock(t *testing.T) {
	src := "[scheme]\nkind: double_elimination\nparticipants: 8\nmatch_size: 4\nwinning_places: 2\nopening: [A1 A2 A3 A4, A5 A6 A7 A8]\n"
	doc, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Compile(doc, Input{Slug: "si", GameType: "si"}); err == nil || !strings.Contains(err.Error(), "opening") {
		t.Fatalf("opening на первом блоке: %v, want ошибку про opening", err)
	}
}

// deal: names places of one ranking. A later block without reseed: true
// deals from the previous block's groups, where no such ranking exists, and
// the table would have been dropped without a word.
func TestRRDealNeedsARanking(t *testing.T) {
	src := "[scheme]\nkind: roundrobin\ngroups: 2\ngroup_size: 4\nproceeding_participants: 2\n---\nkind: roundrobin\ngroups: 2\ngroup_size: 2\ndeal: [1 4, 2 3]\n"
	doc, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	in := Input{Slug: "rr", GameType: "brain"}
	if _, err := Compile(doc, in); err == nil || !strings.Contains(err.Error(), "deal") {
		t.Fatalf("deal на блоке без reseed: %v, want ошибку про deal", err)
	}
}

// A double elimination's bouts say which bracket they are played in, for the
// Сетка to colour, as appendix 3 letters them (W upper, L lower); the grand
// final seats both brackets, so it is in neither.
func TestDEBoutsKnowTheirBracket(t *testing.T) {
	de := matchStages(compileSrc(t, octobearfestSISrc, octobearfestSIInput()))[7:]
	u, l := structure.BracketUpper, structure.BracketLower
	want := [][]string{{u, u, u, l, l, l}, {u, u, l, l, l}, {u, l, l}, {l, l}, {l}, {""}}
	for r, brackets := range want {
		got := make([]string, len(de[r].Matches))
		for i, match := range de[r].Matches {
			got[i] = match.Bracket
		}
		if fmt.Sprint(got) != fmt.Sprint(brackets) {
			t.Errorf("round %d (%s): brackets %q, want %q", r+1, de[r].Title, got, brackets)
		}
	}
}
