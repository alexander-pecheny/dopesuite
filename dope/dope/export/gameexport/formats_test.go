package gameexport

import (
	"testing"

	"dope/dope/domain/games"
)

// Every registered format's export layout has a builder, so no format's
// download answers that it is not supported.
func TestEveryFormatHasAnExport(t *testing.T) {
	for _, d := range games.All() {
		if _, ok := sheetBuilders[d.Sheets]; !ok {
			t.Errorf("%s: no xlsx builder for its layout %d", d.Code, d.Sheets)
		}
	}
}

// Only OD and the friendship cup answer the results view.
func TestResultsViewFormats(t *testing.T) {
	got := games.Codes(func(d games.Definition) bool { return d.Results != nil })
	if len(got) != 2 || got[0] != games.OD || got[1] != games.KD {
		t.Errorf("results view: %v", got)
	}
}
