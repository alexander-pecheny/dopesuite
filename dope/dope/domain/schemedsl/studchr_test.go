package schemedsl

import (
	"testing"

	"dope/dope/storage/store"
)

// Студенческий чемпионат России 2026, все пять игр, записанные на этом языке.
// Каждая схема сверена со своим регламентом (orgkomitet.org/studchr/2026), и
// вместе они — тот самый тест модели «Структура × Протокол»: пять очень разных
// турниров и ни одного нового вида блока под них.

// ЭК: 48 команд, сетка боёв на четверых, из каждого проходят двое. Регламент
// играет четвертьфинал втроём и пересевает перед ним.
const studchrEKSrc = `
[defaults]
venues: 12

[init]
seed: xlsx

[scheme]
title: Плей-офф
type: single_elimination
teams: 48
match_size: 4
winning_places: 2
match_size.r3: 3
reseed: r3
sorting: [total, plus]
`

// Личная СИ: 54 игрока, шесть групп по девять, бой на троих, четыре круга;
// четверо из группы выходят в плей-офф на 24 с двумя жизнями.
const studchrSISrc = `
[defaults]
venues: 18

[init]
seed: xlsx

[scheme]
title: Групповой этап
type: roundrobin
groups: 6
teams_in_group: 9
match_size: 3
themes: 6
bout.points: seats + 1 - place
sorting: [points, total, plus]
proceeding_teams: 4
---
title: Плей-офф
type: double_elimination
teams: 24
match_size: 4
winning_places: 2
themes: 8
reseed: true
sorting: [place_sum, total, plus]
`

// ОД: командная викторина, шесть туров по пятнадцать вопросов, все девяносто
// команд играют один общий бой.
const studchrODSrc = `
[scheme]
title: КВРМ
type: flat
teams: 90
tour_comp: [15, 15, 15, 15, 15, 15]
`

// КСИ: командная своя игра, двадцать тем, все команды за одним столом.
const studchrKSISrc = `
[scheme]
title: КСИ
type: flat
teams: 40
themes: 20
`

func TestStudchrEK(t *testing.T) {
	scheme := compileSrc(t, studchrEKSrc, Input{Slug: "ek", GameType: "ek"})
	rounds := matchStages(scheme)
	want := []struct{ bouts, seats int }{{12, 4}, {6, 4}, {4, 3}, {2, 4}, {1, 4}}
	if len(rounds) != len(want) {
		t.Fatalf("раундов = %d, want %d", len(rounds), len(want))
	}
	for i, w := range want {
		if len(rounds[i].Matches) != w.bouts {
			t.Fatalf("раунд %d: %d боёв, want %d", i+1, len(rounds[i].Matches), w.bouts)
		}
		for _, match := range rounds[i].Matches {
			if len(match.Slots) != w.seats {
				t.Fatalf("раунд %d, %s: %d мест, want %d", i+1, match.Code, len(match.Slots), w.seats)
			}
		}
	}
	if !hasStageType(scheme, "reseed") {
		t.Error("перед четвертьфиналом должен быть пересев")
	}
}

func TestStudchrSI(t *testing.T) {
	scheme := compileSrc(t, studchrSISrc, Input{Slug: "si", GameType: "si"})
	stages := matchStages(scheme)
	// Шесть групп, потом семь раундов плей-офф.
	if len(stages) != 6+7 {
		t.Fatalf("этапов = %d, want 13", len(stages))
	}
	for g := 0; g < 6; g++ {
		if len(stages[g].Matches) != 12 {
			t.Fatalf("группа %d: %d боёв, want 12", g+1, len(stages[g].Matches))
		}
		for _, match := range stages[g].Matches {
			if len(match.Slots) != 3 {
				t.Fatalf("группа %d, %s: %d мест, want 3", g+1, match.Code, len(match.Slots))
			}
		}
	}
	playoff := stages[6:]
	wantBouts := []int{6, 6, 5, 3, 2, 1, 1}
	for i, want := range wantBouts {
		if len(playoff[i].Matches) != want {
			t.Fatalf("ПО-%d: %d боёв, want %d", i+1, len(playoff[i].Matches), want)
		}
	}
	if got := len(playoff[6].Matches[0].Slots); got != 4 {
		t.Fatalf("грандфинал на %d мест, want 4", got)
	}
}

func TestStudchrFlatGames(t *testing.T) {
	for _, c := range []struct {
		name, src, gameType string
		teams               int
	}{
		{"ОД", studchrODSrc, "od", 90},
		{"КСИ", studchrKSISrc, "ksi", 40},
	} {
		scheme := compileSrc(t, c.src, Input{GameType: c.gameType})
		stages := matchStages(scheme)
		if len(stages) != 1 || len(stages[0].Matches) != 1 {
			t.Fatalf("%s: %d этапов — flat-игра это один бой", c.name, len(stages))
		}
		if got := len(stages[0].Matches[0].Slots); got != c.teams {
			t.Fatalf("%s: за столом %d, want %d", c.name, got, c.teams)
		}
	}
}

// КИнСБФ уже сверен в TestCompileKinsbf; здесь только то, что все пять схем
// компилируются одним и тем же компилятором без единого нового вида блока.
func TestStudchrAllFiveCompile(t *testing.T) {
	for _, c := range []struct {
		name, src, gameType string
	}{
		{"КИнСБФ", kinsbfSrc, "brain"},
		{"ЭК", studchrEKSrc, "ek"},
		{"личная СИ", studchrSISrc, "si"},
		{"ОД", studchrODSrc, "od"},
		{"КСИ", studchrKSISrc, "ksi"},
	} {
		doc, err := Parse(c.src)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if _, err := Compile(doc, Input{GameType: c.gameType}); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
	}
}

func matchStages(scheme store.FestScheme) []store.SchemeStage {
	var out []store.SchemeStage
	for _, stage := range scheme.Stages {
		if stage.StageType == "matches" {
			out = append(out, stage)
		}
	}
	return out
}

func hasStageType(scheme store.FestScheme, kind string) bool {
	for _, stage := range scheme.Stages {
		if stage.StageType == kind {
			return true
		}
	}
	return false
}
