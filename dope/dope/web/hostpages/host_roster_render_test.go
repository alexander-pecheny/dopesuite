package hostpages

import (
	"net/url"
	"strings"
	"testing"

	"dope/dope/domain/imports"
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
		`/static/dist/roster.js`, `/static/dist/pageforms.js`,
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

// TestHostRatingImportConflictDocRenders builds the roster-import page with the
// reconcile dialog and confirms it validates and carries the form contract
// parseRosterChoice reads back: one radio group per conflicted team, a merge
// value per incoming candidate, and the drop value.
func TestHostRatingImportConflictDocRenders(t *testing.T) {
	data := hostFestImportData{
		Fest:     view.HostFest{ID: 5, Title: "Кубок"},
		RatingID: 14149,
		Conflict: &imports.RosterConflict{
			Dropped: []imports.DroppedTeam{{
				TeamID: 42, RatingID: 3, Number: 18, Name: "Эта-ноль", City: "Базель",
				Games: []string{"ЧГК", "КСИ"},
			}},
			Added: []imports.AddedTeam{
				{RatingID: 93587, Name: "Эта-ноль", City: "Базель"},
				{RatingID: 77, Name: "Новички", City: "Берн"},
			},
		},
	}
	html, err := dopeui.Render(hostRatingImportDoc(data))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	body := string(html)
	for _, want := range []string{
		`id="rosterConflictDialog"`, `data-dialog-auto`, `data-dialog-close`,
		`/static/dist/pageforms.js`,
		`name="team_42" value="merge:93587"`,
		`name="team_42" value="merge:77"`,
		`name="team_42" value="drop"`,
		"Эта-ноль", "ЧГК, КСИ",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("conflict dialog missing %q", want)
		}
	}
	// Only the incoming team of the same name is preselected.
	if !strings.Contains(body, `value="merge:93587" checked`) {
		t.Errorf("the same-name candidate should be preselected:\n%s", body)
	}
	if strings.Contains(body, `value="merge:77" checked`) || strings.Contains(body, `value="drop" checked`) {
		t.Errorf("nothing else may be preselected:\n%s", body)
	}
}

// TestParseRosterChoiceReadsTheForm is the other half of that contract.
func TestParseRosterChoiceReadsTheForm(t *testing.T) {
	choice := parseRosterChoice(url.Values{
		"team_42": {"merge:93587"},
		"team_43": {"drop"},
		"team_44": {""},
		"other":   {"drop"},
	})
	if len(choice.Merge) != 1 || choice.Merge[42] != 93587 {
		t.Errorf("merge = %v, want team 42 onto 93587", choice.Merge)
	}
	if len(choice.Drop) != 1 || !choice.Drop[43] {
		t.Errorf("drop = %v, want team 43 only", choice.Drop)
	}
}
