package hostpages

import (
	"strings"
	"testing"

	"dope/dope/domain/roster"
	"dope/dope/domain/view"
	dopeui "dope/dope/web/ui"
)

// The troikas page renders its table, a dialog per troika with four player
// fields on the fest's player list, and the bulk form; a troika a Game seats
// has no delete button.
func TestHostTroikasDocRenders(t *testing.T) {
	data := hostTroikasData{
		Fest: view.HostFest{ID: 5, Title: "Bug Major"},
		Troikas: []roster.Assembled{
			{ID: 11, Name: "Бобры", Players: []string{"Иван Петров", "Анна Сидорова"}, Team: "Альфа"},
			{ID: 12, Name: "Ежи", Players: []string{"Олег Кузнецов", "Ия Ли", "Ян Ким"}, Seated: true},
		},
		Players: []roster.FestPlayerChoice{{Name: "Иван Петров", Team: "Альфа"}},
	}
	html, err := dopeui.Render(hostTroikasDoc(data))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	body := string(html)
	for _, want := range []string{
		`id="troikaPlayers"`, `value="Иван Петров (Альфа)"`,
		`id="troika-11"`, `id="troika-12"`, `list="troikaPlayers"`,
		`name="mode" value="lines"`, `name="lines"`, "Альфа",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("troikas page missing %q", want)
		}
	}
	if got := strings.Count(body, `name="player"`); got != 8 {
		t.Errorf("player fields = %d, want four per troika", got)
	}
	if got := strings.Count(body, `name="delete"`); got != 1 {
		t.Errorf("delete buttons = %d, want one: the seated troika has none", got)
	}
}
