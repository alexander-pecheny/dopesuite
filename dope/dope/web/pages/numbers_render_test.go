package pages

import (
	"strings"
	"testing"

	"dope/dope/domain/view"
	ui "dope/dope/web/ui"
)

// TestHostNumbersDocRenders builds the fest-numbers page through the typed UI
// builder and confirms it validates + renders, and that the JS-contract ids and
// the number-row field names survive the port.
func TestHostNumbersDocRenders(t *testing.T) {
	data := hostFestNumbersData{
		Fest:       view.HostFest{ID: 7, Title: "Кубок"},
		HasNumbers: true,
		Notice:     "Номера сохранены.",
		Rows: []hostFestNumberRow{
			{Index: 1, Number: "1", TeamID: 11, TeamLabel: "Альфа (Москва)"},
			{Index: 2, Number: "", TeamID: 0, TeamLabel: "Бета"},
		},
	}
	html, err := ui.Render(hostNumbersDoc(data))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	body := string(html)
	for _, want := range []string{
		`id="numbers-form"`, `data-has-numbers="1"`, `id="numbers-clear-btn"`,
		`formaction="/host/fest/7/numbers/clear"`, `formnovalidate`,
		`id="numbers-auto-btn"`, `id="numbers-import-btn"`, `id="numbers-edit-btn"`,
		`id="numbers-help"`, `id="numbers-save"`, `id="numbers-cancel-btn"`,
		`class="number-row-num"`, `name="num_1"`, `name="team_label_1"`, `name="team_id_1"`, `value="11"`,
		`/static/numbers.js`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("numbers page missing %q", want)
		}
	}
	// Row 2 has no team id -> empty hidden value (matches the old {{if .TeamID}}).
	if !strings.Contains(body, `name="team_id_2" value=""`) {
		t.Errorf("row 2 team_id should be empty:\n%s", body)
	}
}
