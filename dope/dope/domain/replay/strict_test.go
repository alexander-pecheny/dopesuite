package replay

import (
	"strings"
	"testing"
)

// Everything here is a way a wrong transcript used to replay green. A harness
// that accepts a broken oracle is worse than none: it reports success over work
// it never did.

func TestParseRejectsSilentlyWrongTranscripts(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{
			"бой без мест",
			"[game]\ntype: ek\n\n[s1/r1/w1/m1] жребий\n\n[s1/r2/w1/m1]\nА | R---- | 10 | 1\n",
			"без единого места",
		},
		{
			"одна координата дважды",
			"[game]\ntype: ek\n\n[s1/r1/w1/m1]\nА | R---- | 10 | 1\n\n[s1/r1/w1/m1]\nБ | R---- | 10 | 1\n",
			"уже записан",
		},
		{
			"участник не из состава",
			"[game]\ntype: ek\n\n[roster]\n1 | Ктулху\n\n[s1/r1/w1/m1]\nКтулхо | R---- | 10 | 1\n",
			"нет в [roster]",
		},
		{
			"номер занят дважды",
			"[game]\ntype: ek\n\n[roster]\n1 | Ктулху\n1 | Мыслители\n",
			"занят",
		},
	} {
		_, err := Parse(c.src)
		if err == nil {
			t.Errorf("%s: разобралось без ошибки", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: ошибка %q не говорит про %q", c.name, err, c.want)
		}
	}
}

// A '#' inside a name is not a comment. Cutting there renamed the team and
// turned every бой it played into an unexplained seating disagreement.
func TestParseKeepsHashInsideNames(t *testing.T) {
	script, err := Parse("[game]\ntype: ek\n\n[roster]\n1 | Решётка #1\n\n[s1/r1/w1/m1]\nРешётка #1 | R---- | 10 | 1\n")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := script.Roster[0].Name; got != "Решётка #1" {
		t.Errorf("название = %q, want «Решётка #1»", got)
	}
	if got := script.Bouts[0].Seats[0].Name; got != "Решётка #1" {
		t.Errorf("в бою сидит %q", got)
	}
}

// A группа is part of the coordinate. Without it личная СИ's six групп all
// answer to s1/r1/w1/m1 and five of them are never replayed.
func TestParseGroupCoordinate(t *testing.T) {
	script, err := Parse("[game]\ntype: si\n\n[s1/g3/r2/w1/m4]\nА | R---- | 10 | 1\nБ | ----- | 0 | 2\n")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	at := script.Bouts[0].At
	if at.Group != "3" || at.Round != 2 || at.Match != 4 {
		t.Fatalf("координата = %+v", at)
	}
	if got := at.String(); got != "s1/g3/r2/w1/m4" {
		t.Errorf("печатается как %q", got)
	}
}

// Groups make two бои distinct that would otherwise collide.
func TestParseGroupsDoNotCollide(t *testing.T) {
	if _, err := Parse("[game]\ntype: si\n\n[s1/g1/r1/w1/m1]\nА | R---- | 10 | 1\nБ | ----- | 0 | 2\n\n" +
		"[s1/g2/r1/w1/m1]\nВ | R---- | 10 | 1\nГ | ----- | 0 | 2\n"); err != nil {
		t.Fatalf("две группы должны быть разными боями: %v", err)
	}
}

const twoSeats = `[game]
type: ek

[s1/r1/w1/m1] жребий
Ктулху    | ---R- | 40 | 1
ВШЭстером | R---- | 10 | 2
`

// Dope scoring somebody the sheet never seated is a defect, and used to pass
// because Run only ever looked up the names the sheet listed.
func TestRunReportsAParticipantWeInvented(t *testing.T) {
	script, err := Parse(twoSeats)
	if err != nil {
		t.Fatal(err)
	}
	game := newFakeGame()
	game.bendOutcome = func(_ Coord, out map[string]Result) map[string]Result {
		bent := map[string]Result{"Мыслители": {Place: 3, Total: 20}}
		for name, r := range out {
			bent[name] = r
		}
		return bent
	}
	findings, err := Run(script, game)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(findings) != 1 || findings[0].Participant != "Мыслители" {
		t.Fatalf("расхождения = %v, want одно про Мыслителей", findings)
	}
}

// An override about one participant must not cover the rest of the table.
func TestOverrideScopedToOneParticipant(t *testing.T) {
	script, err := Parse(twoSeats + "override [s1/r1/w1/m1] место Ктулху: лист сложил не все темы\n")
	if err != nil {
		t.Fatal(err)
	}
	game := newFakeGame()
	game.bendOutcome = func(_ Coord, out map[string]Result) map[string]Result {
		bent := map[string]Result{}
		for name, r := range out {
			r.Place++
			bent[name] = r
		}
		return bent
	}
	findings, err := Run(script, game)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("расхождений = %d (%v), want одно — про ВШЭстером", len(findings), findings)
	}
	if findings[0].Participant != "ВШЭстером" {
		t.Errorf("закрыто не то место: %+v", findings[0])
	}
}

// An override that silences nothing claims a defect that is not there, and on
// the discrepancies page it reads as a reviewed deviation.
func TestRunReportsAnOverrideThatMatchedNothing(t *testing.T) {
	script, err := Parse(twoSeats + "override [s1/r9/w1/m1] место: лист врёт про бой, которого нет\n")
	if err != nil {
		t.Fatal(err)
	}
	findings, err := Run(script, newFakeGame())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(findings) != 1 || findings[0].Field != "лишнее расхождение" {
		t.Fatalf("расхождения = %v, want жалобу на неиспользованный override", findings)
	}
}
