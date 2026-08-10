package replay

import (
	"fmt"
	"strings"
	"testing"
)

// fakeGame is a Structure that seats a бой from whoever the previous бой sent
// on, and a Protocol that scores 10 per taken answer. It exists so the
// replayer's own logic can be tested without a database — what it must catch
// is a disagreement, and a disagreement is easiest to stage by hand.
type fakeGame struct {
	seated   map[string][]string
	outcomes map[string]map[string]Result
	finished []string
	pinned   []string
	lineups  []string
	stats    []Stat
	// bend rewrites what the game reports, so a test can make dope "wrong".
	bendSeats   func(Coord, []string) []string
	bendOutcome func(Coord, map[string]Result) map[string]Result
}

func newFakeGame() *fakeGame {
	return &fakeGame{seated: map[string][]string{}, outcomes: map[string]map[string]Result{}}
}

func (f *fakeGame) Seat(at Coord, names []string) error {
	f.seated[at.String()] = append([]string(nil), names...)
	return nil
}

func (f *fakeGame) Seats(at Coord) ([]string, error) {
	names, ok := f.seated[at.String()]
	if !ok {
		return nil, fmt.Errorf("бой %s не найден", at)
	}
	if f.bendSeats != nil {
		return f.bendSeats(at, names), nil
	}
	return names, nil
}

func (f *fakeGame) Play(at Coord, name string, play Play) error {
	total := 0
	for _, question := range play.Questions {
		if question.Mark == Right {
			total += 10
		}
	}
	for _, theme := range play.Themes {
		for i, mark := range theme {
			switch mark {
			case Right:
				total += (i + 1) * 10
			case Wrong:
				total -= (i + 1) * 10
			}
		}
	}
	key := at.String()
	if f.outcomes[key] == nil {
		f.outcomes[key] = map[string]Result{}
	}
	f.outcomes[key][name] = Result{Total: total}
	if _, ok := f.seated[key]; !ok {
		f.seated[key] = nil
	}
	if !contains(f.seated[key], name) {
		f.seated[key] = append(f.seated[key], name)
	}
	return nil
}

func (f *fakeGame) Finish(at Coord) error {
	f.finished = append(f.finished, at.String())
	results := f.outcomes[at.String()]
	type row struct {
		name  string
		total int
	}
	var rows []row
	for name, r := range results {
		rows = append(rows, row{name, r.Total})
	}
	for i := 1; i < len(rows); i++ {
		for k := i; k > 0 && rows[k].total > rows[k-1].total; k-- {
			rows[k], rows[k-1] = rows[k-1], rows[k]
		}
	}
	for i, r := range rows {
		results[r.name] = Result{Place: float64(i + 1), Total: r.total}
	}
	return nil
}

func (f *fakeGame) Outcome(at Coord) (map[string]Result, error) {
	out := f.outcomes[at.String()]
	if f.bendOutcome != nil {
		return f.bendOutcome(at, out), nil
	}
	return out, nil
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

const runSample = `[game]
type: ek

[s1/r1/w1/m1] жребий
Ктулху    | ---R- | 40 | 1
ВШЭстером | R---- | 10 | 2
`

func TestRunAcceptsAnAgreeingSheet(t *testing.T) {
	script, err := Parse(runSample)
	if err != nil {
		t.Fatal(err)
	}
	findings, err := Run(script, newFakeGame())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("расхождения на сходящемся листе: %v", findings)
	}
}

// The claims are asserted, not applied: if dope scores the marks differently
// from what the sheet printed, the replay says so rather than believing either.
func TestRunReportsAPlaceDisagreement(t *testing.T) {
	script, err := Parse(runSample)
	if err != nil {
		t.Fatal(err)
	}
	game := newFakeGame()
	game.bendOutcome = func(_ Coord, out map[string]Result) map[string]Result {
		bent := map[string]Result{}
		for name, r := range out {
			if name == "Ктулху" {
				r.Place = 2
			}
			bent[name] = r
		}
		return bent
	}
	findings, err := Run(script, game)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("расхождений = %d, want 1 (%v)", len(findings), findings)
	}
	if got := findings[0]; got.Field != "место" || got.Participant != "Ктулху" {
		t.Errorf("расхождение = %+v", got)
	}
}

// A seating the Structure derived is checked, never written. This is the
// assertion the whole coordinate scheme exists for.
func TestRunReportsASeatingDisagreement(t *testing.T) {
	script, err := Parse(runSample + "\n[s1/r2/w1/m1]\nКтулху | R---- | 10 | 1\n")
	if err != nil {
		t.Fatal(err)
	}
	game := newFakeGame()
	game.seated["s1/r2/w1/m1"] = []string{"ВШЭстером"}
	findings, err := Run(script, game)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(findings) == 0 {
		t.Fatal("резольвер посадил не того, а замены никто не заметил")
	}
	first := findings[0]
	if first.Field != "посадка" {
		t.Fatalf("первое расхождение = %+v, want посадку", first)
	}
	if !strings.Contains(first.Ours, "ВШЭстером") || !strings.Contains(first.Sheet, "Ктулху") {
		t.Errorf("расхождение не показывает обе стороны: %+v", first)
	}
}

// An override is the tournament author's ruling that the sheet is wrong here.
// It silences that one disagreement and nothing else.
func TestOverrideSilencesOnlyItsOwn(t *testing.T) {
	script, err := Parse(runSample + "override [s1/r1/w1/m1] место: лист сложил не все темы\n")
	if err != nil {
		t.Fatal(err)
	}
	game := newFakeGame()
	game.bendOutcome = func(_ Coord, out map[string]Result) map[string]Result {
		bent := map[string]Result{}
		for name, r := range out {
			r.Place, r.Total = r.Place+1, r.Total+5
			bent[name] = r
		}
		return bent
	}
	findings, err := Run(script, game)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, finding := range findings {
		if finding.Field == "место" {
			t.Errorf("место закрыто расхождением, но всё равно сообщено: %+v", finding)
		}
	}
	if len(findings) == 0 {
		t.Error("расхождение по Σ никто не закрывал — оно должно остаться")
	}
}

// Every бой is finished, and in the order written: a round that closes late
// seats the next one from stale results.
func TestRunFinishesEveryBoutInOrder(t *testing.T) {
	script, err := Parse(runSample + "\n[s1/r2/w1/m1] жребий\nКтулху | R---- | 10 | 1\n")
	if err != nil {
		t.Fatal(err)
	}
	game := newFakeGame()
	if _, err := Run(script, game); err != nil {
		t.Fatalf("run: %v", err)
	}
	want := []string{"s1/r1/w1/m1", "s1/r2/w1/m1"}
	if len(game.finished) != len(want) {
		t.Fatalf("закрыто боёв = %v, want %v", game.finished, want)
	}
	for i := range want {
		if game.finished[i] != want[i] {
			t.Fatalf("порядок закрытия = %v, want %v", game.finished, want)
		}
	}
}

func (f *fakeGame) Pin(at Coord, name string, place float64) error {
	key := at.String()
	if f.outcomes[key] == nil {
		f.outcomes[key] = map[string]Result{}
	}
	r := f.outcomes[key][name]
	r.Place = place
	f.outcomes[key][name] = r
	f.pinned = append(f.pinned, key+"|"+name)
	return nil
}

// A pinned place is written, not checked: a перестрелка settles a tie with
// material the grid never recorded, so the marks cannot imply it.
func TestRunWritesPinnedPlacesInsteadOfAsserting(t *testing.T) {
	script, err := Parse("[game]\ntype: ek\n\n[s1/r1/w1/m1] жребий\nА | R---- | 10 | 2!\nБ | R---- | 10 | 1!\n")
	if err != nil {
		t.Fatal(err)
	}
	game := newFakeGame()
	findings, err := Run(script, game)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("расставленное вручную место не проверяют: %v", findings)
	}
	if len(game.pinned) != 2 {
		t.Errorf("проставлено мест = %d, want 2", len(game.pinned))
	}
}

func (f *fakeGame) Lineups(lineups []Lineup) error {
	for _, lineup := range lineups {
		f.lineups = append(f.lineups, lineup.Team)
	}
	return nil
}

func (f *fakeGame) PlayerStats() ([]Stat, error) {
	return f.stats, nil
}

const statsSample = `[game]
type: ek

[roster]
1 | Ктулху
2 | ВШЭстером

[составы]
Ктулху    | Иван Петров
ВШЭстером | Анна Ким

[s1/r1/w1/m1] жребий
Ктулху    | ---R- | 40 | 1 | Иван Петров
ВШЭстером | R---- | 10 | 2 | Анна Ким

[статистика]
Иван Петров | Ктулху    | 40 | 1 | 1
Анна Ким    | ВШЭстером | 10 | 1 | 1
`

// Составы are input: the replay registers them before the first бой, so the
// theme players have somebody to be.
func TestRunWritesLineups(t *testing.T) {
	script, err := Parse(statsSample)
	if err != nil {
		t.Fatal(err)
	}
	game := newFakeGame()
	game.stats = []Stat{
		{Player: "Иван Петров", Team: "Ктулху", Values: [3]int{40, 1, 1}},
		{Player: "Анна Ким", Team: "ВШЭстером", Values: [3]int{10, 1, 1}},
	}
	findings, err := Run(script, game)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("расхождения на сходящемся листе: %v", findings)
	}
	if len(game.lineups) != 2 {
		t.Errorf("составов записано = %d, want 2", len(game.lineups))
	}
}

// Статистика is asserted like Σ and место: dope aggregates the бои itself and
// has to agree with what the sheet printed, player by player, both ways.
func TestRunReportsStatsDisagreements(t *testing.T) {
	script, err := Parse(statsSample)
	if err != nil {
		t.Fatal(err)
	}
	game := newFakeGame()
	game.stats = []Stat{
		{Player: "Иван Петров", Team: "Ктулху", Values: [3]int{40, 2, 1}},
		{Player: "Лишний Игрок", Team: "Ктулху", Values: [3]int{10, 1, 1}},
	}
	findings, err := Run(script, game)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(findings) != 3 {
		t.Fatalf("расхождений = %d (%v), want 3: Σ+ Петрова, лишний игрок, пропавшая Ким", len(findings), findings)
	}
	fields := map[string]bool{}
	for _, f := range findings {
		if f.At != StatsCoord {
			t.Errorf("расхождение статистики с координатой боя: %+v", f)
		}
		fields[f.Field+"|"+f.Participant] = true
	}
	for _, want := range []string{"Σ+|Иван Петров", "статистика|Лишний Игрок", "статистика|Анна Ким"} {
		if !fields[want] {
			t.Errorf("нет расхождения %q среди %v", want, findings)
		}
	}
}

// A stats override silences exactly one player's column, the way a бой
// override silences one field of one seat.
func TestStatsOverrideSilencesOnlyItsOwn(t *testing.T) {
	script, err := Parse(statsSample + "override [статистика] Σ+ Иван Петров: лист сам с собой не сходится\n")
	if err != nil {
		t.Fatal(err)
	}
	game := newFakeGame()
	game.stats = []Stat{
		{Player: "Иван Петров", Team: "Ктулху", Values: [3]int{40, 2, 1}},
		{Player: "Анна Ким", Team: "ВШЭстером", Values: [3]int{10, 1, 1}},
	}
	findings, err := Run(script, game)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("закрытое расхождение всё равно сообщено: %v", findings)
	}
}
