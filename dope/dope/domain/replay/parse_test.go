package replay

import (
	"strings"
	"testing"
)

const ekSample = `# ЭК СтудЧР-2026 — сверено с листом 2026-08-09
[game]
type: ek
title: ЭК
scheme: ek.dsl

[roster]
1 | Ктулху          | Москва
2 | ВШЭстером       | Санкт-Петербург
3 | Ушки на макушке | Казань
4 | Мыслители       | Новосибирск

[s1/r1/w1/m1] жребий
Ктулху          | ----- ---R- RR--W | 120 | 1
ВШЭстером       | ----- ----- R---- |  10 | 4
Ушки на макушке | -R--- ----- ----- |  20 | 3
Мыслители       | ---R- --R-- ----- |  70 | 2

[s1/r2/w1/m1]
Ктулху          | R---- ----- ----- |  10 | 2
Мыслители       | -R--- -R--- ----- |  60 | 1

# лист сложил только три темы из двенадцати
override [s1/r2/w1/m1] место: у листа Ктулху первый при меньшей сумме
`

func TestParseHeaderAndRoster(t *testing.T) {
	script, err := Parse(ekSample)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if script.Game != "ek" || script.Title != "ЭК" || script.Scheme != "ek.dsl" {
		t.Fatalf("шапка = %+v", script)
	}
	if len(script.Roster) != 4 {
		t.Fatalf("участников = %d, want 4", len(script.Roster))
	}
	third := script.Roster[2]
	if third.Number != 3 || third.Name != "Ушки на макушке" || third.City != "Казань" {
		t.Errorf("третий участник = %+v", third)
	}
}

func TestParseBoutCoordinates(t *testing.T) {
	script, err := Parse(ekSample)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(script.Bouts) != 2 {
		t.Fatalf("боёв = %d, want 2", len(script.Bouts))
	}
	first := script.Bouts[0]
	want := Coord{Block: "s1", Round: 1, Wave: 1, Match: 1}
	if first.At != want {
		t.Errorf("координата = %+v, want %+v", first.At, want)
	}
	if !first.Draw {
		t.Error("первый бой посажен жребием — это вход, а не проверка")
	}
	if script.Bouts[1].Draw {
		t.Error("второй бой сажает резольвер — посадку надо проверять, а не писать")
	}
}

func TestParseSeatMarksAndClaims(t *testing.T) {
	script, err := Parse(ekSample)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	seat := script.Bouts[0].Seats[0]
	if seat.Name != "Ктулху" {
		t.Fatalf("имя = %q", seat.Name)
	}
	if len(seat.Marks) != 3 {
		t.Fatalf("тем = %d, want 3", len(seat.Marks))
	}
	if got := seat.Marks[1]; got != [5]Mark{None, None, None, Right, None} {
		t.Errorf("вторая тема = %v", got)
	}
	if got := seat.Marks[2]; got != [5]Mark{Right, Right, None, None, Wrong} {
		t.Errorf("третья тема = %v", got)
	}
	if seat.Total != 120 || seat.Place != 1 {
		t.Errorf("лист заявил Σ%d место %v", seat.Total, seat.Place)
	}
}

func TestParseOverride(t *testing.T) {
	script, err := Parse(ekSample)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(script.Overrides) != 1 {
		t.Fatalf("расхождений = %d, want 1", len(script.Overrides))
	}
	over := script.Overrides[0]
	if over.At.Round != 2 || over.Field != "место" {
		t.Errorf("расхождение = %+v", over)
	}
	if over.Reason == "" {
		t.Error("расхождение без причины — это не расхождение, а замолчанная ошибка")
	}
}

// A place may be shared: личная СИ pays очки by место, so two equal сумма
// finish 1.5 rather than being split by something the бой did not measure.
func TestParseSharedPlace(t *testing.T) {
	script, err := Parse("[game]\ntype: si\n\n[s1/r1/w1/m1]\nА | R---- | 10 | 1.5\nБ | R---- | 10 | 1.5\n")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for i, seat := range script.Bouts[0].Seats {
		if seat.Place != 1.5 {
			t.Errorf("место %d-го = %v, want 1.5", i+1, seat.Place)
		}
	}
}

// A transcript is read by people, so its errors must say where to look.
func TestParseErrorsCarryLineNumbers(t *testing.T) {
	for _, c := range []struct{ name, src string }{
		{"кривая координата", "[game]\ntype: ek\n\n[s1/r1/w1]\nА | R---- | 10 | 1\n"},
		{"метка не из алфавита", "[game]\ntype: ek\n\n[s1/r1/w1/m1]\nА | R-X-- | 10 | 1\n"},
		{"место не число", "[game]\ntype: ek\n\n[s1/r1/w1/m1]\nА | R---- | 10 | первое\n"},
		{"расхождение без причины", "[game]\ntype: ek\n\n[s1/r1/w1/m1]\nА | R---- | 10 | 1\noverride [s1/r1/w1/m1] место\n"},
		{"место в бою без координаты", "[game]\ntype: ek\nА | R---- | 10 | 1\n"},
	} {
		if _, err := Parse(c.src); err == nil {
			t.Errorf("%s: разобралось без ошибки", c.name)
		} else if !hasLine(err.Error()) {
			t.Errorf("%s: ошибка без номера строки: %v", c.name, err)
		}
	}
}

func hasLine(msg string) bool {
	return strings.Contains(msg, "строка ")
}

// Брейн's бой is not a grid of themes: it is a duel over buzzer questions, and
// what the protocol records for each is who took it. So a брейн seat line lists
// its questions instead — comma-separated, because a player has a space in the
// middle of their name and a бой has none to spare.
func TestParseBrainSeatLine(t *testing.T) {
	script, err := Parse("[game]\ntype: brain\n\n[s1/g1/r1/w1/m1]\n" +
		"Рыб'ending | R Виктория Корнеева, -, R Санжи Сундуев, W Тимофей Маркин, R | 3 | 1\n" +
		"Постпопс   | -, W Нина Андреева, -, -, - | 0 | 2\n")
	if err != nil {
		t.Fatal(err)
	}
	seat := script.Bouts[0].Seats[0]
	if len(seat.Questions) != 5 {
		t.Fatalf("вопросов = %d, want 5", len(seat.Questions))
	}
	if seat.Questions[0].Mark != Right || seat.Questions[0].Player != "Виктория Корнеева" {
		t.Errorf("первый вопрос = %+v", seat.Questions[0])
	}
	if seat.Questions[1].Mark != None || seat.Questions[1].Player != "" {
		t.Errorf("нетронутый вопрос = %+v", seat.Questions[1])
	}
	if seat.Questions[3].Mark != Wrong {
		t.Errorf("снятие = %+v", seat.Questions[3])
	}
	// A question taken with nobody named is still a taken question: the sheets
	// do not always record who buzzed.
	if seat.Questions[4].Mark != Right || seat.Questions[4].Player != "" {
		t.Errorf("взятие без игрока = %+v", seat.Questions[4])
	}
	if len(seat.Marks) != 0 {
		t.Errorf("у брейна нет тем, а прочитано %d", len(seat.Marks))
	}
}

// The two forms are not interchangeable: a брейн бой written as a theme grid,
// or an ЭК бой written as questions, is a transcript of a game nobody played.
func TestParseRefusesTheWrongSeatForm(t *testing.T) {
	if _, err := Parse("[game]\ntype: brain\n\n[s1/r1/w1/m1]\nА | ---R- | 40 | 1\n"); err == nil {
		t.Error("брейн принял сетку тем")
	}
	if _, err := Parse("[game]\ntype: ek\n\n[s1/r1/w1/m1]\nА | R Иван Петров, - | 10 | 1\n"); err == nil {
		t.Error("ЭК принял список вопросов")
	}
}

// Составы: a team game's transcript names each team's players, so the replay
// can register them and the theme players below can be held to a real roster.
func TestParseLineups(t *testing.T) {
	script, err := Parse(`[game]
type: ek

[roster]
1 | Ктулху
2 | ВШЭстером

[составы]
Ктулху    | Иван Петров, Анна Ким
ВШЭстером | Юлия Лапшина

[s1/r1/w1/m1] жребий
Ктулху    | R---- | 10 | 1
ВШЭстером | ----- |  0 | 2
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(script.Lineups) != 2 {
		t.Fatalf("составов = %d, want 2", len(script.Lineups))
	}
	first := script.Lineups[0]
	if first.Team != "Ктулху" || len(first.Players) != 2 || first.Players[1] != "Анна Ким" {
		t.Errorf("первый состав = %+v", first)
	}
}

// An ЭК seat line may carry a fifth field: who played each theme, comma-
// separated and aligned with the marks, `-` where the sheet named nobody.
func TestParseEKThemePlayers(t *testing.T) {
	script, err := Parse(`[game]
type: ek

[s1/r1/w1/m1] жребий
Ктулху | R---- ---R- ----- | 50 | 1 | Иван Петров, -, Анна Ким
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	seat := script.Bouts[0].Seats[0]
	want := []string{"Иван Петров", "", "Анна Ким"}
	if len(seat.Players) != len(want) {
		t.Fatalf("игроков = %d, want %d", len(seat.Players), len(want))
	}
	for i, name := range want {
		if seat.Players[i] != name {
			t.Errorf("игрок темы %d = %q, want %q", i+1, seat.Players[i], name)
		}
	}
}

func TestParseEKThemePlayersMustAlignWithThemes(t *testing.T) {
	if _, err := Parse("[game]\ntype: ek\n\n[s1/r1/w1/m1]\nА | R---- ----- | 10 | 1 | Иван Петров\n"); err == nil {
		t.Error("две темы и один игрок разобрались без ошибки")
	}
}

// Статистика: the sheet's own per-player aggregates, asserted after the last
// бой the way Σ and место are asserted after each one.
func TestParseStats(t *testing.T) {
	script, err := Parse(`[game]
type: ek

[roster]
1 | Ктулху

[составы]
Ктулху | Иван Петров

[s1/r1/w1/m1] жребий
Ктулху | R---- | 10 | 1 | Иван Петров

[статистика]
Иван Петров | Ктулху | 10 | 1 | 1
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(script.Stats) != 1 {
		t.Fatalf("строк статистики = %d, want 1", len(script.Stats))
	}
	row := script.Stats[0]
	if row.Player != "Иван Петров" || row.Team != "Ктулху" || row.Values != [3]int{10, 1, 1} {
		t.Errorf("статистика = %+v", row)
	}
}

// In an individual game the participant is the player, so a stats line carries
// no team and a составы section has nothing to say.
func TestParseStatsIndividual(t *testing.T) {
	script, err := Parse(`[game]
type: si

[roster]
1 | Виктор Вега

[s1/r1/w1/m1] жребий
Виктор Вега | R---- | 10 | 1

[статистика]
Виктор Вега | 10 | 10 | 1
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	row := script.Stats[0]
	if row.Player != "Виктор Вега" || row.Team != "" || row.Values != [3]int{10, 10, 1} {
		t.Errorf("статистика = %+v", row)
	}
	if _, err := Parse("[game]\ntype: si\n\n[составы]\nКтулху | Иван Петров\n"); err == nil {
		t.Error("личная игра приняла составы")
	}
}

// A stats disagreement the author has ruled on is silenced by name, with the
// section itself standing in for the бой coordinate.
func TestParseStatsOverride(t *testing.T) {
	script, err := Parse(`[game]
type: si

[roster]
1 | Виктор Вега

[s1/r1/w1/m1] жребий
Виктор Вега | R---- | 10 | 1

[статистика]
Виктор Вега | 10 | 10 | 1

override [статистика] Σ+ Виктор Вега: лист сам с собой не сходится
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	over := script.Overrides[0]
	if over.At != StatsCoord || over.Field != "Σ+" || over.Participant != "Виктор Вега" {
		t.Errorf("расхождение = %+v", over)
	}
	if StatsCoord.String() != "статистика" {
		t.Errorf("координата статистики печатается как %q", StatsCoord.String())
	}
}

// Составы and статистика are held to the transcript's own data: an unknown
// team, an unknown player, or a theme player outside his team's состав is a
// parse error, not data.
func TestParseLineupAndStatsStrictness(t *testing.T) {
	for _, c := range []struct{ name, src string }{
		{"состав команды не из ростера", "[game]\ntype: ek\n\n[roster]\n1 | Ктулху\n\n[составы]\nПришельцы | Иван Петров\n\n[s1/r1/w1/m1] жребий\nКтулху | R---- | 10 | 1\n"},
		{"игрок темы не из состава", "[game]\ntype: ek\n\n[roster]\n1 | Ктулху\n\n[составы]\nКтулху | Анна Ким\n\n[s1/r1/w1/m1] жребий\nКтулху | R---- | 10 | 1 | Иван Петров\n"},
		{"статистика игрока не из состава", "[game]\ntype: ek\n\n[roster]\n1 | Ктулху\n\n[составы]\nКтулху | Анна Ким\n\n[s1/r1/w1/m1] жребий\nКтулху | R---- | 10 | 1\n\n[статистика]\nИван Петров | Ктулху | 10 | 1 | 1\n"},
		{"статистика команды не из ростера", "[game]\ntype: ek\n\n[roster]\n1 | Ктулху\n\n[s1/r1/w1/m1] жребий\nКтулху | R---- | 10 | 1\n\n[статистика]\nИван Петров | Пришельцы | 10 | 1 | 1\n"},
		{"личная статистика игрока не из ростера", "[game]\ntype: si\n\n[roster]\n1 | Виктор Вега\n\n[s1/r1/w1/m1] жребий\nВиктор Вега | R---- | 10 | 1\n\n[статистика]\nНикто Такой | 10 | 10 | 1\n"},
		{"игроки у брейна", "[game]\ntype: brain\n\n[s1/r1/w1/m1]\nА | R Иван, - | 1 | 1 | Иван\nБ | -, - | 0 | 2\n"},
		{"игроки у личной игры", "[game]\ntype: si\n\n[s1/r1/w1/m1]\nА | R---- | 10 | 1 | Иван Петров\n"},
		{"состав записан дважды", "[game]\ntype: ek\n\n[roster]\n1 | Ктулху\n\n[составы]\nКтулху | Иван Петров\nКтулху | Анна Ким\n\n[s1/r1/w1/m1] жребий\nКтулху | R---- | 10 | 1\n"},
	} {
		if _, err := Parse(c.src); err == nil {
			t.Errorf("%s: разобралось без ошибки", c.name)
		} else if !hasLine(err.Error()) && !strings.Contains(err.Error(), "запис") {
			t.Errorf("%s: ошибка без адреса: %v", c.name, err)
		}
	}
}

// A перестрелка is extra material the protocol grid never records — the sheet
// keeps only its net points per player. The line rides inside the бой it broke,
// and the seat carries the value, so ranking can use it instead of a pin.
func TestParseShootout(t *testing.T) {
	script, err := Parse(`[game]
type: si

[s1/r1/w1/m1]
А | R---R | 60 | 1
Б | -R--R | 60 | 2
перестрелка А: 60
перестрелка Б: -50
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	seats := script.Bouts[0].Seats
	if seats[0].Shootout != 60 || seats[1].Shootout != -50 {
		t.Errorf("перестрелка = %d и %d, want 60 и -50", seats[0].Shootout, seats[1].Shootout)
	}
}

func TestParseShootoutStrictness(t *testing.T) {
	for _, c := range []struct{ name, src string }{
		{"не сидящий в бою", "[game]\ntype: si\n\n[s1/r1/w1/m1]\nА | R---- | 10 | 1\nперестрелка Б: 20\n"},
		{"записана дважды", "[game]\ntype: si\n\n[s1/r1/w1/m1]\nА | R---- | 10 | 1\nперестрелка А: 20\nперестрелка А: 30\n"},
		{"не число", "[game]\ntype: si\n\n[s1/r1/w1/m1]\nА | R---- | 10 | 1\nперестрелка А: много\n"},
		{"вне боя", "[game]\ntype: si\n\nперестрелка А: 20\n"},
	} {
		if _, err := Parse(c.src); err == nil {
			t.Errorf("%s: разобралось без ошибки", c.name)
		} else if !hasLine(err.Error()) {
			t.Errorf("%s: ошибка без номера строки: %v", c.name, err)
		}
	}
}
