package hostpages

import (
	"strings"
	"testing"

	"dope/dope/domain/overrides"
	"dope/dope/domain/view"
	dopeui "dope/dope/web/ui"
)

// TestHostPlayersDocRenders builds the players page (add-override dialog, the
// overrides table with per-row edit dialogs, players table) through the typed UI
// builder and confirms it validates and preserves the JS-contract data-* hooks
// and form field names.
func TestHostPlayersDocRenders(t *testing.T) {
	data := hostFestRosterData{
		Fest:    view.HostFest{ID: 5, Title: "Кубок"},
		Players: []hostFestPlayer{{RatingID: 3, Name: "Иван Петров", Team: "Альфа"}},
		OverridePlayers: []overrides.HostPlayerOverrideOption{
			{ID: 3, Label: "Иван Петров (Альфа)"},
		},
		OverrideTeams: []overrides.HostTeamOverrideOption{{ID: 9, Label: "Бета"}},
		OverrideGames: []overrides.HostGameOverrideOption{{ID: 1, Label: "КСИ 1"}},
		Overrides: []overrides.HostPlayerOverrideRow{
			{PlayerID: 3, SourceTeamID: 4, OverrideTeamID: 9, Player: "Иван Петров", SourceTeam: "Альфа", OverrideTeam: "Бета", Games: "КСИ 1", GameIDs: []int64{1}},
		},
	}
	html, err := dopeui.Render(hostPlayersDoc(data))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	body := string(html)
	for _, want := range []string{
		`id="playerOverrideDialog"`, `data-player-override-form`,
		`name="player_id"`, `data-player-override-player-id`,
		`name="player_label"`, `list="playerOverridePlayers"`, `data-player-override-player`,
		`id="playerOverridePlayers"`, `data-id="3"`,
		`data-dialog-open="playerOverrideDialog"`, `data-dialog-close`,
		`name="game_id"`, `data-dialog-open="playerOverrideEdit-3-4-9"`,
		`id="playerOverrideEdit-3-4-9"`, `name="mode" value="edit"`,
		`name="delete" value="1"`, `data-confirm="Удалить оверрайд?"`,
		`/static/roster.js`, `/static/pageforms.js`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("players page missing %q", want)
		}
	}
	// The edit dialog's checkbox for game 1 must be pre-checked (HasGame).
	if !strings.Contains(body, `value="1" checked`) {
		t.Errorf("edit dialog game checkbox should be checked:\n%s", body)
	}
}
