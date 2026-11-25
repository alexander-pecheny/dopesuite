package games

import (
	"encoding/json"

	dopestrings "dope/i18nstrings"
)

// mpFormat is the minefield: a ChGK sheet where a wrong answer costs points
// (ODMinus). Under the hood it is OD's document and page; to a host it is a
// game of its own, with its own name and its own creation knobs.
var mpFormat = Definition{Code: MP, Label: dopestrings.Default.Games.Mp.Label(), Title: dopestrings.Default.Host.Games.TypeMp(),
	Page: "static/od.html", Flat: true, Divisions: true, OutsideDivisions: true, DSL: DSLAccepted, ToursSeed: true,
	Results: odResults, Sheets: SheetsODRating, Journal: JournalODPatches, Protocol: mp{}}

// DefaultODMinus is a minefield's rules when the host gives none: a wrong
// answer costs one point.
var DefaultODMinus = ODMinus{Wrong: 1}

// mp is the minefield's Protocol: OD's, seated and rostered the same way,
// with the minus block in its scheme.
type mp struct{ od }

func (mp) Code() string { return MP }

func (mp) PristineGame(slug, title string, shape Shape) ([]byte, []byte, error) {
	scheme, state := ODEmptyGameJSON(slug, title, shape.Tours)
	minus := DefaultODMinus
	if shape.Minus != nil {
		minus = *shape.Minus
	}
	obj, err := RawJSONObject(string(scheme))
	if err != nil {
		return nil, nil, err
	}
	if obj["minus"], err = json.Marshal(minus); err != nil {
		return nil, nil, err
	}
	if obj["gameType"], err = json.Marshal(MP); err != nil {
		return nil, nil, err
	}
	scheme, err = json.Marshal(obj)
	return scheme, state, err
}

func (m mp) ShapeOf(schemeJSON string) Shape {
	shape := m.od.ShapeOf(schemeJSON)
	shape.Minus = ParseODMinus(schemeJSON)
	return shape
}
