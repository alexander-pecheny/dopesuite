package schemedsl

import (
	"encoding/json"
	"strings"
	"testing"

	"dope/dope/storage/store"
)

// Студенческий чемпионат России 2026, все пять игр, записанные на этом языке.
// Каждая схема сверена со своим регламентом (orgkomitet.org/studchr/2026), и
// вместе они — тот самый тест модели «Структура × Протокол»: пять очень разных
// турниров и ни одного нового вида блока под них.

// ЭК: 48 команд, сетка боёв на четверых, из каждого проходят двое. Регламент
// играет четвертьфинал втроём и пересевает перед ним. Столов шесть, поэтому
// 1/16 финала идёт в два захода — как и было на турнире.
const studchrEKSrc = `
[defaults]
venues: 6

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
title.r1: 1/16 финала
title.r2: 1/8 финала
title.r3: 1/4 финала
title.r4: Полуфиналы
title.r5: Финал
`

// Личная СИ: 54 игрока, шесть групп по девять, бой на троих, четыре круга;
// четверо из группы выходят в плей-офф на 24 с двумя жизнями.
const studchrSISrc = `
[defaults]
venues: 6

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
themes.r7: 12
reseed: true
sorting: [place_sum, total, plus, taken50, taken40, taken30, taken20]
title.r1: 1 этап
title.r2: 2 этап
title.r3: 3 этап
title.r4: 4 этап
title.r5: 5 этап
title.r6: Финал нижней сетки
title.r7: Грандфинал
`

// ТПШ: 91 игрок пишет общий письменный отбор, лучшие 24 играют сетку боёв на
// четверых, из каждого проходят двое. Сетка обрывается после второго этапа:
// шестеро оставшихся и есть победители, финала нет.
const studchrTPShSrc = `
[defaults]
venues: 6

[scheme]
title: Письменный отбор
type: flat
teams: 91
themes: 10
proceeding_teams: 24
sorting: [total, plus, taken50, taken40, taken30, taken20, taken10]
---
title: Плей-офф
type: single_elimination
teams: 24
match_size: 4
winning_places: 2
rounds: 2
themes: 9
reseed: every
sorting: [place_sum, total, plus, taken50, taken40, taken30, taken20, taken10]
title.r1: 1 этап
title.r2: 2 этап
`

func TestStudchrTPSh(t *testing.T) {
	scheme := compileSrc(t, studchrTPShSrc, Input{Slug: "tpsh", GameType: "si"})
	stages := matchStages(scheme)
	if len(stages) != 3 {
		t.Fatalf("этапов = %d, want 3 (отбор + два этапа)", len(stages))
	}
	if got := len(stages[0].Matches[0].Slots); got != 91 {
		t.Fatalf("письменный отбор на %d мест, want 91", got)
	}
	for i, want := range []struct {
		title string
		bouts int
	}{{"1 этап", 6}, {"2 этап", 3}} {
		stage := stages[i+1]
		if stage.Title != want.title || len(stage.Matches) != want.bouts {
			t.Errorf("этап %d: %q, %d боёв — want %q, %d", i+1, stage.Title, len(stage.Matches), want.title, want.bouts)
		}
	}
	// Бой A of the sheet seats the отбор's 1st, 12th, 13th and 24th.
	if got := slotLabels(stages[1].Matches[0]); got != "Пересев-1 Пересев-12 Пересев-13 Пересев-24" {
		t.Errorf("первый бой плей-офф: %s", got)
	}
	// The second stage is re-ranked too: бой G seats the survivors 1, 6, 7 and 12,
	// not the winners of бои A and B in бой order.
	if got := slotLabels(stages[2].Matches[0]); got != "Пересев-1 Пересев-6 Пересев-7 Пересев-12" {
		t.Errorf("первый бой второго этапа: %s", got)
	}
}

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
	stages := matchStages(scheme)
	// Шесть столов: двенадцать боёв 1/16 играются в два захода, дальше всё
	// умещается в один. Заход — это отдельный этап, у него своё название.
	want := []struct {
		title               string
		round, bouts, seats int
	}{
		{"1/16 финала, заход 1", 1, 6, 4},
		{"1/16 финала, заход 2", 1, 6, 4},
		{"1/8 финала", 2, 6, 4},
		{"1/4 финала", 3, 4, 3},
		{"Полуфиналы", 4, 2, 4},
		{"Финал", 5, 1, 4},
	}
	if len(stages) != len(want) {
		t.Fatalf("этапов = %d, want %d", len(stages), len(want))
	}
	for i, w := range want {
		stage := stages[i]
		if stage.Title != w.title {
			t.Errorf("этап %d называется %q, want %q", i+1, stage.Title, w.title)
		}
		if len(stage.Matches) != w.bouts {
			t.Fatalf("%s: %d боёв, want %d", w.title, len(stage.Matches), w.bouts)
		}
		for _, match := range stage.Matches {
			if len(match.Slots) != w.seats {
				t.Fatalf("%s, %s: %d мест, want %d", w.title, match.Code, len(match.Slots), w.seats)
			}
			if match.Round != w.round {
				t.Fatalf("%s, %s: круг %d, want %d", w.title, match.Code, match.Round, w.round)
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
	// Бой G of the sheet seats ranks 1, 12, 13 and 24 — the snake every later
	// round of this same bracket is already dealt with. A block fed by a
	// пересев receives a ranking, not a balanced template, so its opening round
	// has to deal that ranking rather than slice it 1-2-3-4.
	if got := slotLabels(playoff[0].Matches[0]); got != "Пересев-1 Пересев-12 Пересев-13 Пересев-24" {
		t.Errorf("первый бой плей-офф: %s", got)
	}
	// «Раунд 6» is not what anyone at the tournament called it. A bracket with
	// lives has no arithmetic name for its rounds, so the scheme says them.
	for i, want := range []string{"1 этап", "2 этап", "3 этап", "4 этап", "5 этап",
		"Финал нижней сетки", "Грандфинал"} {
		if playoff[i].Title != want {
			t.Errorf("раунд %d назван %q, want %q", i+1, playoff[i].Title, want)
		}
	}
	// The grand final is played over twelve themes where the rest of the
	// play-off has eight.
	if got := themeCount(t, playoff[6]); got != 12 {
		t.Errorf("тем в грандфинале = %d, want 12", got)
	}
}

func themeCount(t *testing.T, stage store.SchemeStage) int {
	t.Helper()
	var config struct {
		Themes int `json:"themes"`
	}
	if err := json.Unmarshal(stage.Config, &config); err != nil {
		t.Fatal(err)
	}
	return config.Themes
}

func slotLabels(match store.SchemeMatch) string {
	names := make([]string, len(match.Slots))
	for i, slot := range match.Slots {
		names[i] = slot.Label
	}
	return strings.Join(names, " ")
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
