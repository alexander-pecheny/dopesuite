package schemedsl

import (
	"fmt"
	"testing"

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
	in := Input{Slug: "si", GameType: "si"}
	for i := 0; i < 58; i++ {
		in.Entrants = append(in.Entrants, store.SchemeSlot{Seed: &store.SchemeSeedRef{Basket: 1, Number: i + 1}, Label: fmt.Sprint("P", i+1)})
	}
	scheme := compileSrc(t, octobearfestSISrc, in)
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
	// Верхняя сетка открывается пересевом 1–12, нижняя — 13–24: 1-е и 2-е места
	// групп против 3-х и 4-х не встречаются до второго круга.
	if got := slotLabels(de[0].Matches[0]); got != "Пересев-1 Пересев-6 Пересев-7 Пересев-12" {
		t.Errorf("первый бой верхней сетки: %s", got)
	}
	if got := slotLabels(de[0].Matches[3]); got != "Пересев-13 Пересев-18 Пересев-19 Пересев-24" {
		t.Errorf("первый бой нижней сетки: %s", got)
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
