package replay

import (
	"strings"
	"testing"
)

const tableSample = `[game]
type: si

[roster]
1 | Виктор Вега
2 | Яна Шаркова
3 | Олег Кочко

[s1/g1/r1/w1/m1] жребий
Виктор Вега | ---R- | 40 | 1
Яна Шаркова | R---- | 10 | 2

[таблица s1/g1]
1 | Виктор Вега
2 | Яна Шаркова
`

func TestParseTable(t *testing.T) {
	script, err := Parse(tableSample)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(script.Tables) != 1 {
		t.Fatalf("таблиц = %d, want 1", len(script.Tables))
	}
	table := script.Tables[0]
	if table.At != (Coord{Block: "s1", Group: "1"}) || table.At.String() != "таблица s1/g1" {
		t.Errorf("координата таблицы = %+v (%s)", table.At, table.At)
	}
	if len(table.Rows) != 2 || table.Rows[0] != (TableRow{Place: 1, Name: "Виктор Вега", Line: 14}) || table.Rows[1].Place != 2 {
		t.Errorf("строки = %+v", table.Rows)
	}
}

func TestParseTableOverride(t *testing.T) {
	script, err := Parse(tableSample + "\noverride [таблица s1/g1] место Яна Шаркова: level on every count, the sheet ordered them by hand\n")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	over := script.Overrides[0]
	if over.At != (Coord{Block: "s1", Group: "1"}) || over.Field != "место" || over.Participant != "Яна Шаркова" {
		t.Errorf("расхождение = %+v", over)
	}
}

// A table is held to the door like a бой: no rows, an unrostered name, a name
// twice, or a second table at the same coordinate are all errors with a line.
func TestParseTableStrictness(t *testing.T) {
	head := "[game]\ntype: si\n\n[roster]\n1 | Виктор Вега\n2 | Яна Шаркова\n\n[s1/g1/r1/w1/m1] жребий\nВиктор Вега | ---R- | 40 | 1\n"
	for _, c := range []struct{ name, tail string }{
		{"таблица без строк", "\n[таблица s1/g1]\n\n[таблица s1/g2]\n1 | Виктор Вега\n"},
		{"участник не из ростера", "\n[таблица s1/g1]\n1 | Некто Иной\n"},
		{"участник дважды", "\n[таблица s1/g1]\n1 | Виктор Вега\n2 | Виктор Вега\n"},
		{"две таблицы на одной координате", "\n[таблица s1/g1]\n1 | Виктор Вега\n\n[таблица s1/g1]\n1 | Яна Шаркова\n"},
		{"место не число", "\n[таблица s1/g1]\nпервое | Виктор Вега\n"},
		{"координата боя", "\n[таблица s1/g1/r1/w1/m1]\n1 | Виктор Вега\n"},
	} {
		if _, err := Parse(head + c.tail); err == nil {
			t.Errorf("%s: разобралось без ошибки", c.name)
		} else if !hasLine(err.Error()) {
			t.Errorf("%s: ошибка без адреса: %v", c.name, err)
		}
	}
}

func (f *fakeGame) Standings(at Coord) ([]TableRow, error) {
	return f.standings[at.String()], nil
}

func TestRunAssertsTablesBothWays(t *testing.T) {
	script, err := Parse(tableSample)
	if err != nil {
		t.Fatal(err)
	}
	game := newFakeGame()
	game.standings = map[string][]TableRow{"таблица s1/g1": {
		{Place: 1, Name: "Виктор Вега"},
		{Place: 2, Name: "Олег Кочко"},
	}}
	findings, err := Run(script, game)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	fields := map[string]bool{}
	for _, f := range findings {
		if f.At.String() != "таблица s1/g1" {
			t.Errorf("расхождение таблицы с чужой координатой: %+v", f)
		}
		fields[f.Field+"|"+f.Participant] = true
	}
	if len(findings) != 2 || !fields["таблица|Яна Шаркова"] || !fields["таблица|Олег Кочко"] {
		t.Fatalf("расхождений = %v, want пропавшая Шаркова и лишний Кочко", findings)
	}
}

func TestRunReportsATablePlaceDisagreement(t *testing.T) {
	script, err := Parse(tableSample)
	if err != nil {
		t.Fatal(err)
	}
	game := newFakeGame()
	game.standings = map[string][]TableRow{"таблица s1/g1": {
		{Place: 1, Name: "Виктор Вега"},
		{Place: 1, Name: "Яна Шаркова"},
	}}
	findings, err := Run(script, game)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(findings) != 1 || findings[0].Field != "место" || findings[0].Participant != "Яна Шаркова" || findings[0].Sheet != "2" || findings[0].Ours != "1" {
		t.Fatalf("расхождения = %v, want место Шарковой 2 против 1", findings)
	}
	if !strings.Contains(findings[0].String(), "таблица s1/g1, Яна Шаркова: место") {
		t.Errorf("расхождение читается как %q", findings[0].String())
	}
	script, _ = Parse(tableSample + "\noverride [таблица s1/g1] место Яна Шаркова: level on every count\n")
	if findings, _ = Run(script, game); len(findings) != 0 {
		t.Fatalf("закрытое расхождение всё равно сообщено: %v", findings)
	}
}

// A game that cannot rank a Block fails a transcript that asserts one — the
// same rule as статистика.
func TestRunNeedsAStandingsReaderForTables(t *testing.T) {
	script, err := Parse(tableSample)
	if err != nil {
		t.Fatal(err)
	}
	type bare struct{ Game }
	if _, err := Run(script, bare{newFakeGame()}); err == nil {
		t.Fatal("таблица без Standings прошла молча")
	}
}
