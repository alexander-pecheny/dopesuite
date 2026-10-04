package games

import (
	"encoding/json"
	"testing"
)

type multiTestDoc struct {
	Participants []KSIParticipant `json:"participants"`
	Declined     map[string]bool  `json:"declined"`
	Games        []struct {
		Cells [][]int `json:"cells"`
	} `json:"games"`
}

func readMultiDoc(t *testing.T, raw []byte) multiTestDoc {
	t.Helper()
	var doc multiTestDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("state: %v (%s)", err, raw)
	}
	return doc
}

// cellOf is the first cell of the only мини-игра on the row of the team
// called name.
func cellOf(t *testing.T, doc multiTestDoc, name string) int {
	t.Helper()
	for i, p := range doc.Participants {
		if p.Name == name {
			return doc.Games[0].Cells[i][0]
		}
	}
	t.Fatalf("no team %q among %+v", name, doc.Participants)
	return 0
}

const guestTestScheme = `{"gameType":"multi","minigames":[{"name":"Песни","columns":[{"values":[0,1,2]},{"values":[0,1,2]}]}]}`

// A guest team is added after the fest's teams under a Number below zero,
// takes an empty row, and cannot take a name another team of the game has.
func TestAddMultiGuest(t *testing.T) {
	state := `{"participants":[{"number":1,"name":"Альфа"},{"number":2,"name":"Бета"}],"games":[{"cells":[[1,0],[2,2]]}]}`
	scheme, next, err := AddMultiGuest(guestTestScheme, state, "  Гости   из Пинска ")
	if err != nil {
		t.Fatal(err)
	}
	scheme, next, err = AddMultiGuest(string(scheme), string(next), "Жюри")
	if err != nil {
		t.Fatal(err)
	}
	doc := readMultiDoc(t, next)
	want := []KSIParticipant{{Number: 1, Name: "Альфа"}, {Number: 2, Name: "Бета"}, {Number: -1, Name: "Гости из Пинска"}, {Number: -2, Name: "Жюри"}}
	if len(doc.Participants) != len(want) {
		t.Fatalf("participants = %+v", doc.Participants)
	}
	for i := range want {
		if doc.Participants[i].Number != want[i].Number || doc.Participants[i].Name != want[i].Name {
			t.Fatalf("participants = %+v, want %+v", doc.Participants, want)
		}
	}
	if cellOf(t, doc, "Бета") != 2 || cellOf(t, doc, "Жюри") != 0 || len(doc.Games[0].Cells[3]) != 2 {
		t.Fatalf("cells = %v", doc.Games[0].Cells)
	}
	var sc struct {
		Participants []KSIParticipant `json:"participants"`
	}
	if err := json.Unmarshal(scheme, &sc); err != nil || len(sc.Participants) != 4 {
		t.Fatalf("scheme participants = %s", scheme)
	}
	for _, taken := range []string{"альфа", "ЖЮРИ", " "} {
		if _, _, err := AddMultiGuest(string(scheme), string(next), taken); err == nil {
			t.Errorf("AddMultiGuest(%q) was accepted", taken)
		}
	}
}

// A guest team keeps its cells and its Отказ through a roster fold that
// drops, adds and reorders the fest's teams, and stays after them.
func TestMultiFoldRosterKeepsGuests(t *testing.T) {
	state := `{"participants":[{"number":1,"name":"Альфа"},{"number":2,"name":"Бета"},{"number":-1,"name":"Жюри"},{"number":-2,"name":"Гости"}],` +
		`"declined":{"n-2":true},"games":[{"cells":[[1,0],[2,2],[1,1],[0,0]]}]}`
	_, next, err := (multi{}).FoldRoster(guestTestScheme, state, []RosterTeam{{Number: 3, Name: "Вега"}, {Number: 2, Name: "Бета"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	doc := readMultiDoc(t, next)
	names := []string{}
	for _, p := range doc.Participants {
		names = append(names, p.Name)
	}
	if got := len(names); got != 4 || names[0] != "Вега" || names[1] != "Бета" || names[2] != "Жюри" || names[3] != "Гости" {
		t.Fatalf("participants after fold = %v", names)
	}
	if cellOf(t, doc, "Жюри") != 1 || cellOf(t, doc, "Бета") != 2 || cellOf(t, doc, "Вега") != 0 {
		t.Fatalf("cells after fold = %v", doc.Games[0].Cells)
	}
	if !KSIParticipantDeclined(doc.Declined, doc.Participants[3]) || KSIParticipantDeclined(doc.Declined, doc.Participants[2]) {
		t.Fatalf("declined after fold = %v", doc.Declined)
	}
	seats := (multi{}).Seats(next)
	if seats[3].Number != -2 || !seats[3].Declined {
		t.Fatalf("seats = %+v", seats)
	}
}

// Renaming a guest team keeps its row; removing one is refused while it has
// a point or an Отказ, and otherwise takes its row away.
func TestRenameAndRemoveMultiGuest(t *testing.T) {
	state := `{"participants":[{"number":1,"name":"Альфа"},{"number":-1,"name":"Жюри"},{"number":-2,"name":"Гости"},{"number":-3,"name":"Отказники"}],` +
		`"declined":{"n-3":true},"games":[{"cells":[[1,0],[0,2],[0,0],[0,0]]}]}`
	scheme, next, err := RenameMultiGuest(guestTestScheme, state, -1, "Жюри турнира")
	if err != nil {
		t.Fatal(err)
	}
	if cellOf(t, readMultiDoc(t, next), "Жюри турнира") != 0 || readMultiDoc(t, next).Games[0].Cells[1][1] != 2 {
		t.Fatalf("cells after rename = %s", next)
	}
	if _, _, err := RenameMultiGuest(string(scheme), string(next), 1, "Бета"); err == nil {
		t.Error("renaming a fest team was accepted")
	}
	if _, _, err := RenameMultiGuest(string(scheme), string(next), -2, "альфа"); err == nil {
		t.Error("a rename onto a taken name was accepted")
	}
	if _, _, err := RemoveMultiGuest(string(scheme), string(next), -1); err == nil {
		t.Error("removing a team with points was accepted")
	}
	if _, _, err := RemoveMultiGuest(string(scheme), string(next), -3); err == nil {
		t.Error("removing a team with an Отказ was accepted")
	}
	_, next, err = RemoveMultiGuest(string(scheme), string(next), -2)
	if err != nil {
		t.Fatal(err)
	}
	doc := readMultiDoc(t, next)
	if len(doc.Participants) != 3 || doc.Participants[2].Name != "Отказники" || len(doc.Games[0].Cells) != 3 || doc.Games[0].Cells[1][1] != 2 {
		t.Fatalf("after remove = %s", next)
	}
}

// Clearing a Multi game keeps its guest teams, with nothing entered.
func TestKeepMultiGuests(t *testing.T) {
	old := `{"gameType":"multi","participants":[{"number":1,"name":"Альфа"},{"number":-1,"name":"Жюри"}],"minigames":[{"name":"Песни","columns":[{"values":[0,1,2]}]}]}`
	empty := `{"participants":[{"number":1,"name":"Альфа"}],"games":[{"cells":[[0]]}]}`
	_, next, err := KeepMultiGuests(old, []byte(old), []byte(empty))
	if err != nil {
		t.Fatal(err)
	}
	doc := readMultiDoc(t, next)
	if len(doc.Participants) != 2 || doc.Participants[1].Number != -1 || len(doc.Games[0].Cells) != 2 {
		t.Fatalf("after clear = %s", next)
	}
}
