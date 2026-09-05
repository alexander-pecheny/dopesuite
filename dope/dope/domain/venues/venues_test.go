package venues

import (
	"testing"
	"time"

	"dope/dope/domain/protocol"
)

// A flag the filer gave stands; one they did not is derived from the base
// roster, which is what a roster stored before the flag was a field carries.
func TestFlagsAreGivenOrDerived(t *testing.T) {
	given := []RosterPlayer{
		{PlayerID: 1, Surname: "Печеный", Name: "Александр", Flag: FlagCaptain},
		{PlayerID: 3, Surname: "Биткин", Name: "Игорь", Flag: FlagBase},
	}
	base := map[int64]bool{1: true, 2: true}
	// 3 is not in the base roster, and is still Б because someone said so.
	if got := Flags(given, 62868, base); got[0] != FlagCaptain || got[1] != FlagBase {
		t.Fatalf("given flags %v", got)
	}

	players := []RosterPlayer{
		{PlayerID: 1, Surname: "Печеный", Name: "Александр", Flag: FlagCaptain},
		{PlayerID: 2, Surname: "Плотников", Name: "Дмитрий"},
		{PlayerID: 3, Surname: "Биткин", Name: "Игорь"},
	}
	got := Flags(players, 62868, base)
	if got[0] != FlagCaptain || got[1] != FlagBase || got[2] != FlagLegion {
		t.Fatalf("flags %v", got)
	}
	// A team with no rating id has no base roster to be outside of.
	if got := Flags(players, 0, nil); got[0] != FlagCaptain || got[1] != FlagBase || got[2] != FlagBase {
		t.Fatalf("id-0 flags %v", got)
	}
	// A team the mirror has not caught up with reads as legionnaires.
	if got := Flags(players, 62868, nil); got[1] != FlagLegion || got[2] != FlagLegion {
		t.Fatalf("unmirrored flags %v", got)
	}
	if s := FlagSummary(got); s != "1К 1Б 1Л" {
		t.Fatalf("summary %q", s)
	}
	if DefaultFlag(1, 62868, base) != FlagBase || DefaultFlag(3, 62868, base) != FlagLegion {
		t.Fatal("the default is the base roster")
	}
	if DefaultFlag(3, 0, nil) != FlagBase {
		t.Fatal("a team with no rating id has no legionnaires")
	}
}

func TestParseRosterKeepsOneCaptain(t *testing.T) {
	got := ParseRoster(`[
      {"player_id":1,"surname":"А","name":"Б","captain":true},
      {"player_id":0,"surname":" ","name":" "},
      {"player_id":2,"surname":"В","name":"Г","flag":"К"}]`)
	if len(got) != 2 {
		t.Fatalf("roster %+v", got)
	}
	// The stored captain becomes a К, and the second К is not a second captain.
	if got[0].Flag != FlagCaptain || got[1].Flag != "" {
		t.Fatalf("captains %+v", got)
	}
	if got[0].Captain {
		t.Fatal("the legacy field is read, never written again")
	}
	// A flag nobody defined is dropped rather than stored as itself.
	if one := ParseRoster(`[{"surname":"А","flag":"Ж"}]`); one[0].Flag != "" {
		t.Fatalf("unknown flag %+v", one)
	}
	if got[0].FullName() != "А Б" {
		t.Fatalf("name %q", got[0].FullName())
	}
	if ParseRoster("") != nil && len(ParseRoster("")) != 0 {
		t.Fatal("empty roster")
	}
	if MarshalRoster(nil) != "[]" {
		t.Fatalf("marshal nil %q", MarshalRoster(nil))
	}
}

func TestRegistrationState(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	if Registration("", "", false, now) != RegOpen {
		t.Fatal("a registration with no window is open")
	}
	if Registration("2026-09-03 10:00", "", false, now) != RegScheduled {
		t.Fatal("a future opens_at is scheduled")
	}
	if Registration("2026-09-01 10:00", "", false, now) != RegOpen {
		t.Fatal("a past opens_at is open")
	}
	if Registration("", "2026-09-03 10:00", false, now) != RegOpen {
		t.Fatal("a future closes_at is still open")
	}
	if Registration("", "2026-09-01 10:00", false, now) != RegClosed {
		t.Fatal("a past closes_at closes it")
	}
	if Registration("", "", true, now) != RegClosed {
		t.Fatal("a registration shut by hand takes nothing")
	}
}

func TestTimeHelpers(t *testing.T) {
	if got := FormatTime("2026-09-04T15:00"); got != "2026-09-04 15:00" {
		t.Fatalf("format %q", got)
	}
	if got := FormatTime("2026-09-04"); got != "2026-09-04 00:00" {
		t.Fatalf("date only %q", got)
	}
	if got := FormatTime("завтра"); got != "завтра" {
		t.Fatalf("unparseable %q", got)
	}
	if got := Shift("2026-09-04 15:00", 7*24*time.Hour); got != "2026-09-11 15:00" {
		t.Fatalf("shift %q", got)
	}
	if got := Shift("", time.Hour); got != "" {
		t.Fatalf("shift empty %q", got)
	}
}

func TestAssignNumbersFillsTheGaps(t *testing.T) {
	apps := []Application{{Number: 2}, {}, {Number: 4}, {}}
	assignNumbers(apps, nil)
	if apps[0].Number != 2 || apps[1].Number != 1 || apps[2].Number != 4 || apps[3].Number != 3 {
		t.Fatalf("numbers %v %v %v %v", apps[0].Number, apps[1].Number, apps[2].Number, apps[3].Number)
	}
	// A team the host seated by hand holds its number against a new application.
	hand := map[int64]protocol.RosterTeam{1: {Number: 1}}
	fresh := []Application{{}}
	assignNumbers(fresh, hand)
	if fresh[0].Number != 2 {
		t.Fatalf("number %d, want the first free one past the hand-seated team", fresh[0].Number)
	}
}

func TestNewTokenIsUnguessable(t *testing.T) {
	a, b := NewToken(), NewToken()
	if a == b || len(a) < 20 {
		t.Fatalf("tokens %q %q", a, b)
	}
}
