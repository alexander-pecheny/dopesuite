package hostpages

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"

	"dope/dope/domain/contested"
	"dope/dope/domain/core"
	"dope/dope/domain/gamebuild"
	"dope/dope/domain/games"
	"dope/dope/domain/roster"
	"dope/dope/domain/view"
	"dope/dope/platform/util"
	"dope/dope/storage/store"
	"dope/dope/web/pages"
	dopeui "dope/dope/web/ui"
	dopestrings "dope/i18nstrings"

	corei18n "code.pecheny.me/pecheny/dopesuite/dopecore/i18nstrings"
	"code.pecheny.me/pecheny/dopesuite/dopecore/idstr"
	"code.pecheny.me/pecheny/dopesuite/dopeuikit/palette"

	"dope/dope/domain/festops"
	"dope/dope/web/route"
)

// Upper bounds the game creation form accepts.
const (
	maxFormTours     = 20
	maxFormQuestions = 100
	maxFormThemes    = 100
	minKDTables      = 2
	maxKDTables      = 199
	maxStickerCount  = 100
)

// Lengths of a "#rgb" and a "#rrggbb" color.
const (
	shortHexColorLen = 4
	longHexColorLen  = 7
)

type hostGameSettingsData struct {
	Fest      view.HostFest
	Game      PublicFestGame
	Slug      string
	Error     string
	SchemeDSL string
	HasDSL    bool
	// Pasted says the Game's format has a scheme but the Game has none: it
	// was pasted as JSON before every bracket Game needed one.
	Pasted bool
	// Divisions is every division the game's teams offer, Hidden the ones it
	// does not show; nil Divisions draws no divisions field.
	Divisions []string
	Hidden    []string
	// Ranks says the Game ranks its teams on a flat table where a Division can
	// stand out of competition (OD), Outside the Divisions that do.
	Ranks   bool
	Outside []string
	// Grid says the Game draws a grid, GridWidth its column width (0 for
	// the default).
	Grid      bool
	GridWidth int
	// Contested is whether the Game takes contested answers, nil for a format
	// that takes none; VenueRooms whether its bouts name their venues by
	// room, nil for a format without bouts.
	Contested  *bool
	VenueRooms *bool
	// Tours is the tours block of a format whose tours change after
	// creation, nil for any other.
	Tours *gameTours
	// Shape is a Multi game's minigames block; nil for other formats.
	Shape *GameShape
}

type hostGameCreateData struct {
	Fest         view.HostFest
	Error        string
	SelectedType string
	// DSL is each scheme format's editor text, by format code: what the host
	// posted, else the format's default scheme (games.Definition.DefaultDSL).
	DSL map[string]string
	// Entrants is the fest's registry offered as this Game's entrant list.
	// A Game numbers whom it seats from 1 (ADR-0009), so a fest of 65 can hold
	// an EK of 48 and a brain of a different 48.
	Entrants []gameEntrantOption
	// Divisions are the fest's Divisions, which a format that has them
	// offers; Synch says the fest takes contested answers.
	Divisions []string
	Synch     bool
	// Form is what the host posted, kept when the page is drawn again after
	// a refusal.
	Form url.Values
}

type gameEntrantOption struct {
	ID    int64
	Label string
	// assembled marks a troika, which the picker lists after the teams.
	assembled bool
	// player marks a person; festPlayer, a person of the rating roster no
	// individual Game has seated yet — no Participant exists for them, so the
	// box posts "fp<fest player id>" and the Participant is minted on create.
	player     bool
	festPlayer int64
}

// value is what the option's box posts.
func (o gameEntrantOption) value() string {
	if o.festPlayer > 0 {
		return gamebuild.FestPlayerEntrantRef(o.festPlayer)
	}
	return idstr.Format(o.ID)
}

// stickerPaletteColors is the fixed set of colours an organizer may assign to a
// sticker. Name is the closed swatch-color enum token the swatchradio primitive
// turns into --sticker-c-<name>; Hex is the value submitted with the form.
//
// It comes from dopeuikit/palette, which also generates the --sticker-c-* block
// in styles.css. The two used to be separate literals with a comment asking
// whoever edited one to remember the other.
var stickerPaletteColors = palette.StickerColors

// stickerPalette builds the swatch radio group for one sticker colour field; the
// swatchradio expansion owns the inline --swatch style.
func stickerPalette(name, selected string) *dopeui.Element {
	swatches := make([]dopeui.Item, 0, len(stickerPaletteColors))
	for _, c := range stickerPaletteColors {
		items := []dopeui.Item{dopeui.Name(name), dopeui.Value(c.Hex), dopeui.Attr{Name: "color", Value: c.Name}, dopeui.Title(c.Hex)}
		if strings.EqualFold(c.Hex, selected) {
			items = append(items, dopeui.Checked())
		}
		swatches = append(swatches, dopeui.Swatchradio(items...))
	}
	return dopeui.Palette(swatches...)
}

// stickerRow builds one sticker-type config row: a max-count field and its colour
// palette under the sticker's name.
func stickerRow(label, maxName, maxVal, colorName, colorSel string) *dopeui.Element {
	return dopeui.Col(dopeui.SpaceSM,
		dopeui.Strong(dopeui.Text(label)),
		dopeui.Row(dopeui.AlignCenter, dopeui.SpaceMD, dopeui.Wrap(),
			dopeui.Field(dopeui.Label(dopestrings.Default.Host.Games.StickerMaxLabel()), dopeui.Textfield(dopeui.Name(maxName), dopeui.Inputmode("numeric"), dopeui.Value(maxVal))),
			stickerPalette(colorName, colorSel),
		),
	)
}

// gameTypeOption is one format in the game-type select, chosen when it is
// the selected type.
func gameTypeOption(value, label, selected string) dopeui.Item {
	items := []dopeui.Item{dopeui.Value(value), dopeui.Text(label)}
	if value == selected {
		items = append(items, dopeui.Selected())
	}
	return dopeui.Option(items...)
}

// gameSettings wraps one game type's settings in a data-game-settings section,
// hidden unless that type is the selected one (gamecreate.js toggles them).
func gameSettings(kind, selected string, kids ...dopeui.Item) *dopeui.Element {
	items := []dopeui.Item{dopeui.SpaceMD, dopeui.Data("game-settings", kind)}
	if kind != selected {
		items = append(items, dopeui.Hidden())
	}
	return dopeui.Col(append(items, kids...)...)
}

// hostGameCreateDoc builds the create-game form: the game-type radio group and
// four conditional settings sections (OD / KSI / KSI-stickers / EK), plus the
// submit cluster. gamecreate.js shows the matching section and the submit button
// once a type is picked (keyed on data-game-create-form / data-game-settings /
// data-game-submit).
func hostGameCreateDoc(data hostGameCreateData) *dopeui.Doc {
	ref := data.Fest.Ref()
	sel := data.SelectedType
	s := dopestrings.Default
	page := []dopeui.Item{
		dopeui.Title(s.Host.Games.CreateTitle(data.Fest.Title)), dopeui.PagePublic, dopeui.Classicscripts("dist/gamecreate.js"),
		dopeui.Publictopbar(pages.Trail(pages.FestCrumbs(ref, data.Fest.Title), s.Host.Games.CreateCrumb())),
	}
	if data.Error != "" {
		page = append(page, dopeui.Empty(dopeui.Text(data.Error)))
	}

	if sel == "" {
		sel = games.All()[0].Code
		data.SelectedType = sel
	}
	form := []dopeui.Item{dopeui.DirCol, dopeui.Method("post"), dopeui.Action("/host/fest/" + ref + "/game/new"),
		dopeui.Autocomplete("off"), dopeui.Data("game-create-form", ""),
		dopeui.Field(dopeui.Label(s.Host.Games.TypeLabel()), dopeui.Selectfield(append([]dopeui.Item{dopeui.Name("game_type")}, typeOptions(sel)...)...)),
		// An empty title is the format's, which the placeholder shows
		// (gamecreate.js follows the select).
		dopeui.Field(dopeui.Label(s.Host.Games.TitleLabel()), dopeui.Textfield(dopeui.Name("title"), dopeui.Value(data.Form.Get("title")), dopeui.Placeholder(typeTitle(sel)))),
		dopeui.Field(dopeui.Label(s.Host.Games.SlugLabel()), dopeui.Textfield(dopeui.Name("slug"), dopeui.Value(data.Form.Get("slug")), dopeui.Pattern("[a-z0-9-]+"))),
	}
	form = append(form, settingsSections(data)...)
	form = append(form, createOptionFields(data)...)
	form = append(form, entrantPicker(data), dopeui.Row(dopeui.Button(dopeui.Submit(), dopeui.Text(s.Host.Games.CreateSubmit()))))
	page = append(page, dopeui.Form(form...))
	return &dopeui.Doc{Nodes: []dopeui.Node{dopeui.Page(page...)}}
}

// formatForm is what the creation form draws and reads for one format beyond
// what its Definition says. A format described by a scheme gets a DSL editor
// (games.Definition.DefaultDSL prefills it) and, when it takes a pasted JSON
// scheme, a JSON editor too; a flat format draws its own knobs and reads
// them into the Spec. A variant is a second radio over the same format.
type formatForm struct {
	// A scheme format's editor height, hint and placeholder.
	rows        string
	hint        func() string
	placeholder string
	// A flat format's knobs, and how they are read.
	knobs   func() []dopeui.Item
	read    func(spec *gamebuild.Spec, form url.Values) error
	variant *formVariant
}

// formVariant is a radio that creates the format it follows with other knobs:
// KSI with stickers is an ordinary KSI whose scheme carries a stickers block.
type formVariant struct {
	value, label string
	knobs        func() []dopeui.Item
	read         func(spec *gamebuild.Spec, form url.Values) error
}

// formatForms is every format's part of the creation form, by code. A format
// registered in domain/games without one fails TestEveryFormatHasACreationForm.
var formatForms = map[string]formatForm{
	games.OD: {
		knobs: func() []dopeui.Item {
			return append(toursKnobs(games.OD), dopeui.Hint(dopeui.Text(dopestrings.Default.Host.Games.WholeRosterHint())))
		},
		read: func(spec *gamebuild.Spec, form url.Values) error { return readTours(spec, form, games.OD) },
	},
	games.MP: {
		knobs: func() []dopeui.Item {
			s := dopestrings.Default
			return append(toursKnobs(games.MP),
				dopeui.Hint(dopeui.Text(s.Host.Games.WholeRosterHint())),
				dopeui.Field(dopeui.Label(s.Host.Games.OdMinusWrongLabel()), dopeui.Textfield(dopeui.Name("mp_wrong"), dopeui.Inputmode("numeric"), dopeui.Value(strconv.Itoa(games.DefaultODMinus.Wrong)))),
				dopeui.Field(dopeui.Label(s.Host.Games.OdMinusBonusLabel()), dopeui.Textfield(dopeui.Name("mp_bonus"), dopeui.Inputmode("numeric"), dopeui.Value("0"))),
			)
		},
		read: func(spec *gamebuild.Spec, form url.Values) (err error) {
			if err = readTours(spec, form, games.MP); err != nil {
				return err
			}
			spec.ODMinus, err = odMinusFromForm(form)
			return err
		},
	},
	games.KSI: {
		knobs: func() []dopeui.Item {
			s := dopestrings.Default
			return []dopeui.Item{
				dopeui.Field(dopeui.Label(s.Host.Games.ThemesLabel()), dopeui.Textfield(dopeui.Name("ksi_themes"), dopeui.Inputmode("numeric"), dopeui.Value("20"))),
				dopeui.Hint(dopeui.Text(s.Host.Games.WholeRosterHint())),
			}
		},
		read: func(spec *gamebuild.Spec, form url.Values) (err error) {
			spec.KSIThemes, err = parsePositiveFormInt(form, "ksi_themes", dopestrings.Default.Host.Games.ThemesLabel(), 1, maxFormThemes)
			return err
		},
		variant: &formVariant{
			value: ksiStickersGameType,
			label: dopestrings.Default.Host.Games.TypeKsiStickers(),
			knobs: func() []dopeui.Item {
				s := dopestrings.Default
				return []dopeui.Item{
					dopeui.Field(dopeui.Label(s.Host.Games.ThemesLabel()), dopeui.Textfield(dopeui.Name("ksis_themes"), dopeui.Inputmode("numeric"), dopeui.Value("20"))),
					dopeui.Hint(dopeui.Text(s.Host.Games.WholeRosterHint())),
					dopeui.Hint(dopeui.Text(s.Host.Games.StickersHint())),
					stickerRow(s.Host.Games.StickerNeutral(), "ksis_neutral_max", "20", "ksis_neutral_color", games.KSIStickerNeutralColor),
					stickerRow(s.Host.Games.StickerX2Row(), "ksis_x2_max", "2", "ksis_x2_color", games.KSIStickerX2Color),
					stickerRow(s.Host.Games.StickerNowrongRow(), "ksis_nowrong_max", "1", "ksis_nowrong_color", games.KSIStickerNoWrongColor),
					stickerRow(s.Host.Games.StickerEmptywrongRow(), "ksis_emptywrong_max", "1", "ksis_emptywrong_color", games.KSIStickerEmptyWrongColor),
					dopeui.Checkbox(dopeui.Name("ksis_plus_nominal"), dopeui.Value("1"), dopeui.Text(s.Host.Games.StickerPlusNominalLabel())),
				}
			},
			read: func(spec *gamebuild.Spec, form url.Values) (err error) {
				if spec.KSIThemes, err = parsePositiveFormInt(form, "ksis_themes", dopestrings.Default.Host.Games.ThemesLabel(), 1, maxFormThemes); err != nil {
					return err
				}
				spec.KSIStickers, err = ksiStickerConfigFromForm(form)
				return err
			},
		},
	},
	games.Brain: {rows: "14", hint: dopestrings.Default.Host.Games.BrainHint},
	games.EK: {rows: "10", hint: dopestrings.Default.Host.Games.EkHint,
		placeholder: "[scheme]\nkind: single_elimination\nparticipants: 48\nmatch_size: 4\nwinning_places: 2"},
	games.ES: {rows: "10", hint: dopestrings.Default.Host.Games.EsHint,
		placeholder: "[scheme]\nkind: single_elimination\nparticipants: 48\nmatch_size: 4\nwinning_places: 2\nplayers: 3"},
	games.SI: {rows: "14", hint: dopestrings.Default.Host.Games.SiHint},
	games.Multi: {
		knobs: func() []dopeui.Item {
			s := dopestrings.Default
			return []dopeui.Item{
				dopeui.Field(dopeui.Label(s.Host.Games.MinigamesLabel()),
					dopeui.Editor(dopeui.Name("multi_games"), dopeui.Rows("8"), dopeui.Spellcheck("false"),
						dopeui.Placeholder(s.Host.Games.MinigamesPlaceholder()))),
				dopeui.Hint(dopeui.Text(s.Host.Games.MinigamesHint())),
				dopeui.Hint(dopeui.Text(s.Host.Games.MinigamesShareHint())),
				dopeui.Hint(dopeui.Text(s.Host.Games.WholeRosterHint())),
				dopeui.Field(dopeui.Label(s.Host.Games.MultiSortingLabel()),
					dopeui.Textfield(dopeui.Name("multi_sorting"), dopeui.Placeholder("total, game2, plus"))),
				dopeui.Hint(dopeui.Text(s.Host.Games.MultiSortingHint())),
			}
		},
		read: func(spec *gamebuild.Spec, form url.Values) (err error) {
			s := dopestrings.Default
			if spec.Minigames, err = games.ParseMultiGames(form.Get("multi_games")); err != nil {
				return corei18n.User(s.Host.Games.ErrorMinigames(err.Error()))
			}
			if spec.MultiSorting, err = games.ParseMultiSorting(spec.Minigames, form.Get("multi_sorting")); err != nil {
				return corei18n.User(s.Host.Games.ErrorMultiSorting(err.Error()))
			}
			return nil
		},
	},
	games.Troika: {rows: "16", hint: dopestrings.Default.Host.Games.TroikaHint},
	games.Hamsa:  {rows: "18", hint: dopestrings.Default.Host.Games.HamsaHint},
	games.KD: {
		knobs: func() []dopeui.Item {
			s := dopestrings.Default
			return []dopeui.Item{
				dopeui.Field(dopeui.Label(s.Host.Games.OdToursLabel()), dopeui.Textfield(dopeui.Name("kd_tours"), dopeui.Inputmode("numeric"), dopeui.Value("9"))),
				dopeui.Field(dopeui.Label(s.Host.Games.OdQuestionsLabel()), dopeui.Textfield(dopeui.Name("kd_questions"), dopeui.Inputmode("numeric"), dopeui.Value("4"))),
				dopeui.Field(dopeui.Label(s.Host.Games.KdTablesLabel()), dopeui.Textfield(dopeui.Name("kd_tables"), dopeui.Inputmode("numeric"), dopeui.Value("11"))),
				dopeui.Hint(dopeui.Text(s.Host.Games.KdHint())),
			}
		},
		read: func(spec *gamebuild.Spec, form url.Values) (err error) {
			s := dopestrings.Default
			if spec.ODTours, err = parsePositiveFormInt(form, "kd_tours", s.Host.Games.OdToursLabel(), 1, maxFormTours); err != nil {
				return err
			}
			if spec.ODQuestions, err = parsePositiveFormInt(form, "kd_questions", s.Host.Games.OdQuestionsLabel(), 1, maxFormQuestions); err != nil {
				return err
			}
			spec.KDTables, err = parsePositiveFormInt(form, "kd_tables", s.Host.Games.KdTablesLabel(), minKDTables, maxKDTables)
			return err
		},
	},
}

// dslFieldOf names a scheme format's DSL editor on the creation form. Every
// section is in the document at once and the page merely hides the ones not
// picked — a hidden field still posts — so one shared name would have handed
// a brain game's prefilled scheme to whatever type the host actually chose. A
// flat format has no scheme on the form and its DSL is empty.
func dslFieldOf(code string) (string, bool) {
	d, ok := games.Lookup(code)
	if !ok || d.Flat {
		return "", false
	}
	return code + "_dsl", true
}

// typeOptions is the game-type select: the registry's first format, the
// most common (OD), then every other format and variant by its title.
func typeOptions(selected string) []dopeui.Item {
	type entry struct{ value, label string }
	all := games.All()
	var rest []entry
	for i, d := range all {
		if i > 0 {
			rest = append(rest, entry{d.Code, d.Title})
		}
		if v := formatForms[d.Code].variant; v != nil {
			rest = append(rest, entry{v.value, v.label})
		}
	}
	slices.SortFunc(rest, func(a, b entry) int { return strings.Compare(strings.ToLower(a.label), strings.ToLower(b.label)) })
	out := []dopeui.Item{gameTypeOption(all[0].Code, all[0].Title, selected)}
	for _, e := range rest {
		out = append(out, gameTypeOption(e.value, e.label, selected))
	}
	return out
}

// typeTitle is the title a Game of the select's value takes by default: its
// format's, or its variant's.
func typeTitle(value string) string {
	for _, d := range games.All() {
		if v := formatForms[d.Code].variant; v != nil && v.value == value {
			return v.label
		}
	}
	return games.Label(value)
}

// typeValues is every select value whose format the predicate holds for,
// its variants with it: the formats a creation field applies to.
func typeValues(pred func(games.Definition) bool) string {
	var out []string
	for _, d := range games.All() {
		if !pred(d) {
			continue
		}
		out = append(out, d.Code)
		if v := formatForms[d.Code].variant; v != nil {
			out = append(out, v.value)
		}
	}
	return strings.Join(out, " ")
}

// forTypes shows a creation field for the formats named alone; gamecreate.js
// hides it and disables its inputs for the rest, as a hidden input still
// posts.
func forTypes(types string, selected string, kids ...dopeui.Item) *dopeui.Element {
	items := []dopeui.Item{dopeui.SpaceSM, dopeui.Data("game-types", types)}
	if !slices.Contains(strings.Fields(types), selected) {
		items = append(items, dopeui.Hidden())
	}
	return dopeui.Col(append(items, kids...)...)
}

// createOptionFields are the settings page's options a Game can be created
// with, each for the formats it applies to: the Divisions it shows and ranks
// outside, its grid's width, its venues by room, and contested answers.
func createOptionFields(data hostGameCreateData) []dopeui.Item {
	s := dopestrings.Default
	form, sel := data.Form, data.SelectedType
	var out []dopeui.Item
	if len(data.Divisions) > 0 {
		hidden := data.Divisions[:0:0]
		if form.Get("divisions_present") != "" {
			for _, d := range data.Divisions {
				if !slices.Contains(form["division_shown"], d) {
					hidden = append(hidden, d)
				}
			}
		}
		out = append(out, forTypes(typeValues(func(d games.Definition) bool { return d.Divisions }), sel, divisionsField(data.Divisions, hidden)...))
		out = append(out, forTypes(typeValues(func(d games.Definition) bool { return d.Divisions && d.OutsideDivisions }), sel,
			outsideDivisionsField(data.Divisions, form["division_outside"])...))
	}
	out = append(out, forTypes(typeValues(func(d games.Definition) bool { return d.DSL == games.DSLEditable }), sel,
		gridWidthField(form.Get("grid_width"))))
	out = append(out, forTypes(typeValues(func(d games.Definition) bool { return !d.Flat }), sel,
		settingsSwitch("venue_rooms", form.Get("venue_rooms") == "1", s.Host.Games.VenueRoomsLabel())))
	if data.Synch {
		contestable := typeValues(func(d games.Definition) bool {
			_, ok := games.As[games.Contestable](d.Code)
			return ok
		})
		on := form.Get("contested_present") == "" || form.Get("contested") == "1"
		out = append(out, forTypes(contestable, sel, settingsSwitch("contested", on, s.Host.Games.ContestedLabel())))
	}
	return out
}

// divisionsField is the Divisions a Game shows: a box per one it offers,
// ticked unless hidden.
func divisionsField(divisions, hidden []string) []dopeui.Item {
	s := dopestrings.Default
	boxes := []dopeui.Item{dopeui.Hiddenfield(dopeui.Name("divisions_present"), dopeui.Value("1"))}
	for _, d := range divisions {
		items := []dopeui.Item{dopeui.Name("division_shown"), dopeui.Value(d), dopeui.Text(d)}
		if !slices.Contains(hidden, d) {
			items = append(items, dopeui.Checked())
		}
		boxes = append(boxes, dopeui.Checkbox(items...))
	}
	return []dopeui.Item{
		dopeui.Field(dopeui.Label(s.Host.Games.DivisionsLabel()), dopeui.Row(append([]dopeui.Item{dopeui.SpaceMD}, boxes...)...)),
		dopeui.Hint(dopeui.Text(s.Host.Games.DivisionsHint())),
	}
}

// gridWidthField is the width of a column of the Game's grid, empty for the
// default.
func gridWidthField(width string) dopeui.Item {
	return dopeui.Field(dopeui.Label(dopestrings.Default.Host.Games.GridWidthLabel()),
		dopeui.Textfield(dopeui.Name("grid_width"), dopeui.Value(width), dopeui.Inputmode("numeric")))
}

// settingsSections is every format's settings section, hidden unless picked.
func settingsSections(data hostGameCreateData) []dopeui.Item {
	s := dopestrings.Default
	var out []dopeui.Item
	for _, d := range games.All() {
		f := formatForms[d.Code]
		var kids []dopeui.Item
		if field, ok := dslFieldOf(d.Code); ok {
			editor := []dopeui.Item{dopeui.Name(field), dopeui.Rows(f.rows), dopeui.Spellcheck("false"), dopeui.Text(data.DSL[d.Code])}
			if f.placeholder != "" {
				editor = append(editor, dopeui.Placeholder(f.placeholder))
			}
			kids = append(kids, dopeui.Field(dopeui.Label(s.Host.Games.SchemeLabel()), dopeui.Editor(editor...)))
			if f.hint != nil {
				kids = append(kids, dopeui.Hint(dopeui.Text(f.hint())))
			}
		} else if f.knobs != nil {
			kids = f.knobs()
		}
		out = append(out, gameSettings(d.Code, data.SelectedType, kids...))
		if v := f.variant; v != nil {
			out = append(out, gameSettings(v.value, data.SelectedType, v.knobs()...))
		}
	}
	return out
}

// SeatsChosenEntrants reports whether a format seats the entrant list a host
// picked, rather than the whole fest roster: every format described by a
// scheme, which keeps an entrant list (games.KeepsEntrantList). A flat format
// (OD, KSI, Multi, the friendship cup) seats the whole fest roster under the
// fest's own numbers and marks who did not play on its refusals tab, so it is
// not offered the picker at all and refuses a chosen list rather than
// dropping it (gamebuild.Create).
func SeatsChosenEntrants(gameType string) bool {
	return games.KeepsEntrantList(gameType)
}

// entrantPicker offers the fest's registry as this Game's entrant list. Ticking
// nothing means everyone, which is what a one-game fest wants and what every
// Game did before Games could differ. gamecreate.js reveals it for the formats
// named in data-game-entrants and disables its boxes for the rest — a hidden
// checkbox still posts, and a format that cannot honour one says so.
func entrantPicker(data hostGameCreateData) dopeui.Item {
	s := dopestrings.Default
	if len(data.Entrants) == 0 {
		return dopeui.Empty()
	}
	boxes := make([]dopeui.Item, 0, len(data.Entrants)+1)
	boxes = append(boxes,
		dopeui.Hint(dopeui.Text(s.Host.Games.EntrantsHint())))
	for _, entrant := range data.Entrants {
		boxes = append(boxes, dopeui.Checkbox(dopeui.Name("entrant_id"),
			dopeui.Value(entrant.value()), dopeui.Text(entrant.Label)))
	}
	items := []dopeui.Item{dopeui.Data("game-entrants", strings.Join(games.Codes(func(d games.Definition) bool { return !d.Flat }), " "))}
	if !SeatsChosenEntrants(data.SelectedType) {
		items = append(items, dopeui.Hidden())
	}
	items = append(items, dopeui.Summary(dopeui.Text(s.Host.Games.EntrantsSummary())), dopeui.Col(boxes...))
	return dopeui.Details(items...)
}

// outsideDivisionsField is the settings page's out-of-competition boxes: one per
// Division the game offers, ticked when its teams stand below everyone else.
func outsideDivisionsField(divisions, outside []string) []dopeui.Item {
	s := dopestrings.Default
	boxes := []dopeui.Item{dopeui.Hiddenfield(dopeui.Name("outside_present"), dopeui.Value("1"))}
	for _, d := range divisions {
		items := []dopeui.Item{dopeui.Name("division_outside"), dopeui.Value(d), dopeui.Text(d)}
		if slices.Contains(outside, d) {
			items = append(items, dopeui.Checked())
		}
		boxes = append(boxes, dopeui.Checkbox(items...))
	}
	return []dopeui.Item{
		dopeui.Field(dopeui.Label(s.Host.Games.OutsideLabel()), dopeui.Row(append([]dopeui.Item{dopeui.SpaceMD}, boxes...)...)),
		dopeui.Hint(dopeui.Text(s.Host.Games.OutsideHint())),
	}
}

// hostGameSettingsDoc builds a game's settings page: a small form to rename the
// game and set its slug (its type is shown read-only).
func hostGameSettingsDoc(data hostGameSettingsData) *dopeui.Doc {
	festRef := data.Fest.Ref()
	s := dopestrings.Default
	page := []dopeui.Item{
		dopeui.Title(data.Game.Title + " · " + data.Fest.Title), dopeui.PagePublic,
		dopeui.Publictopbar(pages.Trail(pages.FestCrumbs(festRef, data.Fest.Title), data.Game.Title)),
	}
	if data.Error != "" {
		page = append(page, dopeui.Empty(dopeui.Text(data.Error)))
	}
	form := []dopeui.Item{dopeui.DirCol, dopeui.Method("post"),
		dopeui.Action("/host/fest/" + festRef + "/game/" + data.Game.Ref() + "/settings"), dopeui.Autocomplete("off"),
		dopeui.Field(dopeui.Label(s.Host.Games.TypeLabel()), dopeui.Textfield(dopeui.Value(data.Game.Type), dopeui.Disabled())),
		dopeui.Field(dopeui.Label(s.Host.Games.TitleLabel()), dopeui.Textfield(dopeui.Name("title"), dopeui.Value(data.Game.Title), dopeui.Required())),
		dopeui.Field(dopeui.Label(s.Host.Games.SlugLabel()), dopeui.Textfield(dopeui.Name("slug"), dopeui.Value(data.Slug), dopeui.Pattern("[a-z0-9-]+"))),
	}
	if len(data.Divisions) > 0 {
		form = append(form, divisionsField(data.Divisions, data.Hidden)...)
		if data.Ranks {
			form = append(form, outsideDivisionsField(data.Divisions, data.Outside)...)
		}
	}
	if data.Grid {
		width := ""
		if data.GridWidth > 0 {
			width = strconv.Itoa(data.GridWidth)
		}
		form = append(form, gridWidthField(width))
	}
	if data.Contested != nil {
		form = append(form, settingsSwitch("contested", *data.Contested, s.Host.Games.ContestedLabel()))
	}
	if data.VenueRooms != nil {
		form = append(form, settingsSwitch("venue_rooms", *data.VenueRooms, s.Host.Games.VenueRoomsLabel()))
	}
	if data.HasDSL {
		form = append(form,
			dopeui.Field(dopeui.Label(s.Host.Games.SchemeLabel()),
				dopeui.Editor(dopeui.Name("scheme_dsl"), dopeui.Rows("14"), dopeui.Spellcheck("false"), dopeui.Text(data.SchemeDSL))),
			dopeui.Hint(dopeui.Text(s.Host.Games.RebuildHint())),
		)
	}
	if data.Pasted {
		form = append(form, dopeui.Note(dopeui.Text(s.Host.Games.SchemePastedNote())))
	}
	if data.Shape != nil {
		form = append(form, shapeFields(*data.Shape)...)
	}
	// One form, one save button; the tours' load from the site saves it too,
	// and sits after the save button, which Enter in a field presses.
	buttons := []dopeui.Item{dopeui.SpaceSM, dopeui.Wrap(), dopeui.Button(dopeui.Submit(), dopeui.Text(s.Host.Games.SaveSubmit()))}
	if data.Tours != nil {
		form = append(form, toursField(*data.Tours))
		buttons = append(buttons, toursFromSiteButton())
	}
	form = append(form, dopeui.Row(buttons...))
	page = append(page, dopeui.Form(form...))
	return &dopeui.Doc{Nodes: []dopeui.Node{dopeui.Page(page...)}}
}

// settingsSwitch is a checkbox of the settings form, with a hidden field that
// tells the box left unticked apart from a form that has no box.
func settingsSwitch(name string, on bool, label string) *dopeui.Element {
	box := []dopeui.Item{dopeui.Name(name), dopeui.Value("1"), dopeui.Text(label)}
	if on {
		box = append(box, dopeui.Checked())
	}
	return dopeui.Col(dopeui.Hiddenfield(dopeui.Name(name+"_present"), dopeui.Value("1")), dopeui.Checkbox(box...))
}

// formSwitch reads a settingsSwitch: nil when the form has no such box.
func formSwitch(form url.Values, name string) *bool {
	if form.Get(name+"_present") != "1" {
		return nil
	}
	on := form.Get(name) == "1"
	return &on
}

func (s *Server) renderHostGameSettings(w http.ResponseWriter, r *http.Request, festID, gameID int64, errMsg string) {
	s.festPage(w, r, festID, func(fest view.HostFest) (*dopeui.Doc, error) {
		var (
			code        string
			title       string
			gameType    string
			slug        sql.NullString
			schemeDSL   string
			hidden      string
			outside     string
			gridWidth   int
			contestedOn bool
			synch       bool
			rooms       bool
		)
		if err := s.h.Engine().DB.QueryRowContext(r.Context(), `
select g.code, g.title, g.game_type, g.slug, coalesce(g.scheme_dsl, ''), coalesce(g.hidden_divisions, ''), coalesce(g.outside_divisions, ''), g.grid_width,
  `+contested.EnabledSQL+`, f.kind = 'synch', g.venue_rooms
from games g join fests f on f.id = g.fest_id where g.id = ? and g.fest_id = ?`, gameID, festID).
			Scan(&code, &title, &gameType, &slug, &schemeDSL, &hidden, &outside, &gridWidth, &contestedOn, &synch, &rooms); err != nil {
			return nil, err
		}
		data := hostGameSettingsData{}
		// Contested answers are a synch fest's alone.
		if _, ok := games.As[games.Contestable](gameType); ok && synch {
			data.Contested = &contestedOn
		}
		if !games.Get(gameType).Flat {
			data.VenueRooms = &rooms
		}
		divisions, err := festops.OfferedDivisions(r.Context(), s.h.Engine().DB, festID, gameType)
		if err != nil {
			return nil, err
		}
		tours, err := loadGameTours(r.Context(), s.h.Engine().DB, festID, gameID)
		if err != nil {
			return nil, err
		}
		shape, err := s.settingsShape(r, festID, gameID, errMsg)
		if err != nil {
			return nil, err
		}
		editable := games.Get(gameType).DSL == games.DSLEditable
		hasDSL := editable && strings.TrimSpace(schemeDSL) != ""
		if submitted := strings.TrimSpace(r.Form.Get("scheme_dsl")); submitted != "" && errMsg != "" {
			schemeDSL = r.Form.Get("scheme_dsl")
		}
		// A refused save shows the title, slug and tours as typed, not as
		// stored.
		if errMsg != "" && r.Form.Has("title") {
			title, slug.String = r.Form.Get("title"), r.Form.Get("slug")
		}
		if errMsg != "" && tours != nil && r.Form.Has("tours") {
			tours = &gameTours{Tours: parseTours(r.Form.Get("tours"))}
		}
		return hostGameSettingsDoc(hostGameSettingsData{
			Contested:  data.Contested,
			VenueRooms: data.VenueRooms,
			Tours:      tours,
			Shape:      shape,
			Fest:       fest,
			Game: PublicFestGame{
				ID:    gameID,
				Slug:  slug.String,
				Code:  code,
				Title: title,
				Type:  games.Label(gameType),
			},
			Slug:      slug.String,
			Error:     errMsg,
			SchemeDSL: schemeDSL,
			HasDSL:    hasDSL,
			Pasted:    editable && !hasDSL,
			Divisions: divisions,
			Hidden:    store.ParseHiddenDivisions(hidden),
			Ranks:     games.Get(gameType).OutsideDivisions,
			Outside:   store.ParseHiddenDivisions(outside),
			Grid:      editable,
			GridWidth: gridWidth,
		}), nil
	})
}

// GameSettings is what a game's settings page edits (festops.Settings).
type GameSettings = festops.Settings

// UpdateGameSettings saves a game's settings (festops.UpdateSettingsTx) and
// tells the game's open pages, and those of the Troika Games a changed scheme
// re-seated.
func (s *Server) UpdateGameSettings(ctx context.Context, festID, gameID int64, g GameSettings) error {
	_, err := s.commit(ctx, festID, "game-settings", []int64{gameID}, func(ctx context.Context, tx *sql.Tx) (core.FestWrite, error) {
		return festops.UpdateSettingsTx(ctx, tx, festID, gameID, g)
	})
	return err
}

func (s *Server) handleHostUpdateGameSettings(w http.ResponseWriter, r *http.Request, festID, gameID int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	g := settingsFromForm(r.Form)
	// Changed tours and shape go first: refused, they keep the rest unsaved,
	// and the page shows what the host typed with the reason.
	err := s.formTours(r, festID, gameID)
	if err == nil {
		err = s.formShape(r, festID, gameID)
	}
	if err == nil {
		err = s.UpdateGameSettings(r.Context(), festID, gameID, g)
	}
	gameRef := strings.TrimSpace(g.Slug)
	if gameRef == "" {
		gameRef = fmt.Sprintf("%d", gameID)
	}
	s.settingsWritten(w, r, festID, gameID, gameRef, err)
}

// settingsFromForm reads the settings a form carries, the settings page's
// or the creation form's; a field the form lacks leaves its setting alone.
func settingsFromForm(form url.Values) GameSettings {
	g := GameSettings{Title: form.Get("title"), Slug: form.Get("slug"), SchemeDSL: form.Get("scheme_dsl")}
	if form.Has("grid_width") {
		width, err := strconv.Atoi(strings.TrimSpace(form.Get("grid_width")))
		if err != nil {
			width = 0
		}
		g.GridWidth = &width
	}
	g.Contested, g.VenueRooms = formSwitch(form, "contested"), formSwitch(form, "venue_rooms")
	if form.Get("divisions_present") != "" {
		// The boxes say which offered Divisions are shown (festops.Settings).
		shown := form["division_shown"]
		g.ShownDivisions = &shown
	}
	if form.Get("outside_present") != "" {
		outside := form["division_outside"]
		g.OutsideDivisions = &outside
	}
	return g
}

// settingsWritten ends a post of the settings page: a Game that left the fest
// after the router found it is not found, a refused write draws the page
// again with the reason, and a saved one goes back to the page at gameRef.
func (s *Server) settingsWritten(w http.ResponseWriter, r *http.Request, festID, gameID int64, gameRef string, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.renderHostGameSettings(w, r, festID, gameID, err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/host/fest/%s/game/%s/settings", s.festRefOrID(r.Context(), festID), gameRef), http.StatusSeeOther)
}

// DeleteGame deletes one game of the fest and moves the active-game pointer
// off it (festops.DeleteGameTx).
func (s *Server) DeleteGame(ctx context.Context, festID, gameID int64) error {
	_, err := s.commit(ctx, festID, "game-delete", nil, func(ctx context.Context, tx *sql.Tx) (core.FestWrite, error) {
		return festops.DeleteGameTx(ctx, tx, festID, gameID)
	})
	return err
}

func (s *Server) handleHostDeleteGame(w http.ResponseWriter, r *http.Request, festID, gameID int64) {
	if err := s.DeleteGame(r.Context(), festID, gameID); err != nil {
		route.WriteError(w, r, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/host/fest/%s", s.festRefOrID(r.Context(), festID)), http.StatusSeeOther)
}

// ClearGame resets a game to its just-created state (festops.ClearGameTx)
// and tells its open pages.
func (s *Server) ClearGame(ctx context.Context, festID, gameID int64) error {
	_, err := s.commit(ctx, festID, "game-clear", []int64{gameID}, func(ctx context.Context, tx *sql.Tx) (core.FestWrite, error) {
		written, err := festops.ClearGameTx(ctx, tx, festID, gameID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			err = route.BadUser(err)
		}
		return written, err
	})
	return err
}

func (s *Server) handleHostClearGame(w http.ResponseWriter, r *http.Request, festID, gameID int64) {
	if err := s.ClearGame(r.Context(), festID, gameID); err != nil {
		route.WriteError(w, r, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/host/fest/%s", s.festRefOrID(r.Context(), festID)), http.StatusSeeOther)
}

func (s *Server) renderHostCreateGamePage(w http.ResponseWriter, r *http.Request, festID int64, errMsg string, selectedType string) {
	s.festPage(w, r, festID, func(fest view.HostFest) (*dopeui.Doc, error) {
		var teamCount int
		_ = s.h.Engine().DB.QueryRowContext(r.Context(), `select count(*) from fest_teams where fest_id = ?`, festID).Scan(&teamCount)
		kept := func(field, fallback string) string {
			if v := strings.TrimSpace(r.Form.Get(field)); v != "" {
				return v
			}
			return fallback
		}
		entrants, err := festEntrantOptions(r.Context(), s.h.Engine().DB, festID)
		if err != nil {
			return nil, err
		}
		divisions, err := roster.FestDivisions(r.Context(), s.h.Engine().DB, festID)
		if err != nil {
			return nil, err
		}
		var synch bool
		if err := s.h.Engine().DB.QueryRowContext(r.Context(), `select kind = 'synch' from fests where id = ?`, festID).Scan(&synch); err != nil {
			return nil, err
		}
		return hostGameCreateDoc(hostGameCreateData{
			Fest: fest, Error: errMsg, SelectedType: selectedType,
			DSL:       createFormDSLs(teamCount, kept),
			Entrants:  entrants,
			Divisions: divisions,
			Synch:     synch,
			Form:      r.Form,
		}), nil
	})
}

// createFormDSLs fills each DSL format's box with what the host already typed,
// or with the format's default for a fest of teamCount teams.
func createFormDSLs(teamCount int, kept func(field, fallback string) string) map[string]string {
	dsl := map[string]string{}
	for _, d := range games.All() {
		field, ok := dslFieldOf(d.Code)
		if !ok {
			continue
		}
		fallback := ""
		if d.DefaultDSL != nil {
			fallback = d.DefaultDSL(teamCount)
		}
		dsl[d.Code] = kept(field, fallback)
	}
	return dsl
}

// festEntrantOptions lists the fest's Participants a Game may seat, teams and
// players alike — which kind a Game wants depends on its format, and the picker
// offers both rather than guessing before the type is chosen.
func festEntrantOptions(ctx context.Context, db *sql.DB, festID int64) ([]gameEntrantOption, error) {
	options, err := store.CollectRows(ctx, db, `
select id, name, coalesce(city, ''), roster, assembled from participants
where fest_id = ? and game_id is null order by roster desc, assembled, coalesce(nullif(number, 0), 1 << 30), name, id`,
		[]any{festID}, func(rows *sql.Rows) (gameEntrantOption, error) {
			var option gameEntrantOption
			var city, roster string
			var assembled bool
			if err := rows.Scan(&option.ID, &option.Label, &city, &roster, &assembled); err != nil {
				return option, err
			}
			option.player = roster == "player"
			if city != "" {
				option.Label += " (" + city + ")"
			}
			// A troika reads apart from the teams it is drawn from: the picker
			// lists both, and a Troika Game seats troikas.
			if assembled {
				option.Label = dopestrings.Default.Host.Games.EntrantTroika(option.Label)
				option.assembled = true
			}
			return option, nil
		})
	if err != nil {
		return nil, err
	}
	// An individual Game seats people, and a person becomes a Participant only
	// once some individual Game seated the whole rating roster — so a fest's
	// first personal SI could not pick its entrants at all. Every rating player
	// with no Participant yet is offered too; creating the Game mints them.
	festPlayers, err := store.CollectRows(ctx, db, `
select fp.id, trim(fp.first_name || ' ' || fp.last_name) from fest_players fp
where fp.fest_id = ? and not exists (
  select 1 from participants p
  where p.fest_id = fp.fest_id and p.roster = 'player' and p.fest_player_id = fp.id)
order by fp.id`, []any{festID}, func(rows *sql.Rows) (gameEntrantOption, error) {
		var option gameEntrantOption
		err := rows.Scan(&option.festPlayer, &option.Label)
		option.player = true
		return option, err
	})
	if err != nil {
		return nil, err
	}
	// Teams by number, then troikas, then people — the last two have no
	// number to order by, so they go by name as a person reads it: 2 before
	// 10. Troikas and people used to share one sort and read interleaved.
	var teams, troikas, people []gameEntrantOption
	for _, option := range options {
		switch {
		case option.assembled:
			troikas = append(troikas, option)
		case option.player:
			people = append(people, option)
		default:
			teams = append(teams, option)
		}
	}
	people = append(people, festPlayers...)
	byName := func(list []gameEntrantOption) {
		sort.SliceStable(list, func(i, j int) bool { return util.CompareNatural(list[i].Label, list[j].Label) < 0 })
	}
	byName(troikas)
	byName(people)
	return append(append(teams, troikas...), people...), nil
}

// chosenEntrantRefs reads the picker: whom this Game seats, in the order
// posted — a Participant id, or "fp<id>" for a rating player not yet one.
// Nothing ticked means everyone, which is what every Game did before Games
// could name their own.
func chosenEntrantRefs(form url.Values) []string {
	var out []string
	for _, raw := range form["entrant_id"] {
		if raw = strings.TrimSpace(raw); raw != "" {
			out = append(out, raw)
		}
	}
	return out
}

// GameCreateRequest is the creation form as JSON: the format, whom it seats,
// and the format's own knobs. It is read into the form's own fields, so both
// ways of creating a game go through one reader and one set of refusals.
type GameCreateRequest struct {
	// GameType is a games.* type, or "ksi_stickers" for KSI with stickers.
	GameType string `json:"game_type"`
	// Title and Slug are the Game's, as its settings take them; an empty
	// title is the format's.
	Title    string  `json:"title"`
	Slug     string  `json:"slug"`
	Entrants []int64 `json:"entrants"`
	// EntrantRefs are more entrants by the ref GET …/entrants gives, which
	// also names a rating player who is not a Participant yet ("fp<id>").
	EntrantRefs []string `json:"entrant_refs"`
	// DSL is the format's scheme in the scheme language (brain, si, troika,
	// hamsa, ek, es). Every format with a bracket needs one.
	DSL string `json:"dsl"`
	// A ChGK sheet's tours (OD, the minefield, the friendship cup): so many
	// of so many questions, or the tours of a rating.chgk.info tournament.
	ODTours          int   `json:"od_tours"`
	ODQuestions      int   `json:"od_questions"`
	RatingTournament int64 `json:"rating_tournament"`
	// KDTables is how many tables a friendship cup seats.
	KDTables     int    `json:"kd_tables"`
	KSIThemes    int    `json:"ksi_themes"`
	MultiGames   string `json:"multi_games"`
	MultiSorting string `json:"multi_sorting"`
	// Stickers maps a sticker id (neutral, x2, nowrong, emptywrong) to its
	// colour and how many each team holds; 0 or absent means none.
	Stickers map[string]struct {
		Color string `json:"color"`
		Max   int    `json:"max"`
	} `json:"stickers"`
	// StickerPlusNominal makes a stickers KSI's first tiebreaker the right
	// answers' nominals.
	StickerPlusNominal bool `json:"sticker_plus_nominal"`
	// The minefield: what a wrong answer costs (0: DefaultODMinus's), the
	// bonus for a whole tour, and the empty-tour charge.
	MinusWrong int  `json:"minus_wrong"`
	MinusBonus int  `json:"minus_bonus"`
	MinusEmpty bool `json:"minus_empty"`
}

// setKnobs sets the flat formats' number fields the request gives.
func (req GameCreateRequest) setKnobs(form url.Values) {
	setIfGiven := func(key string, v int) {
		if v != 0 {
			form.Set(key, strconv.Itoa(v))
		}
	}
	for _, prefix := range []string{games.OD, games.MP} {
		setIfGiven(prefix+"_tours", req.ODTours)
		setIfGiven(prefix+"_questions", req.ODQuestions)
		if req.RatingTournament != 0 {
			form.Set(prefix+"_rating_id", fmt.Sprint(req.RatingTournament))
		}
	}
	setIfGiven("kd_tours", req.ODTours)
	setIfGiven("kd_questions", req.ODQuestions)
	setIfGiven("kd_tables", req.KDTables)
	setIfGiven("ksi_themes", req.KSIThemes)
	setIfGiven("ksis_themes", req.KSIThemes)
	setIfGiven("mp_wrong", cmp.Or(req.MinusWrong, games.DefaultODMinus.Wrong))
	setIfGiven("mp_bonus", req.MinusBonus)
	setIfTrue := func(key string, v bool) {
		if v {
			form.Set(key, "1")
		}
	}
	setIfTrue("mp_empty", req.MinusEmpty)
	setIfTrue("ksis_plus_nominal", req.StickerPlusNominal)
}

func (req GameCreateRequest) form() url.Values {
	form := url.Values{"title": {req.Title}, "slug": {req.Slug}}
	for _, id := range req.Entrants {
		form.Add("entrant_id", idstr.Format(id))
	}
	for _, ref := range req.EntrantRefs {
		form.Add("entrant_id", ref)
	}
	if field, ok := dslFieldOf(req.GameType); ok {
		form.Set(field, req.DSL)
	}
	req.setKnobs(form)
	form.Set("multi_games", req.MultiGames)
	form.Set("multi_sorting", req.MultiSorting)
	for id, sticker := range req.Stickers {
		form.Set("ksis_"+id+"_color", sticker.Color)
		form.Set("ksis_"+id+"_max", strconv.Itoa(sticker.Max))
	}
	return form
}

// CreateGame creates a game in the fest from a JSON request and returns its id.
func (s *Server) CreateGame(ctx context.Context, festID int64, req GameCreateRequest) (int64, error) {
	return s.createHostGame(ctx, festID, req.GameType, req.form())
}

func (s *Server) handleHostCreateGame(w http.ResponseWriter, r *http.Request, festID int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	gameType := strings.TrimSpace(r.Form.Get("game_type"))
	gameID, err := s.createHostGame(r.Context(), festID, gameType, r.Form)
	if err != nil {
		s.renderHostCreateGamePage(w, r, festID, err.Error(), gameType)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/host/fest/%s/game/%s/", s.festRefOrID(r.Context(), festID), s.gameRefOrID(r.Context(), gameID)), http.StatusSeeOther)
}

// gameSpecFromForm reads the creation form into what gamebuild needs: the
// format's title and DSL, the entrants ticked, and a flat format's own knobs
// (formatForms).
func gameSpecFromForm(ctx context.Context, tx *sql.Tx, festID int64, gameType string, form url.Values) (gamebuild.Spec, error) {
	entrants, err := gamebuild.ResolveEntrantRefsTx(ctx, tx, festID, chosenEntrantRefs(form))
	if err != nil {
		return gamebuild.Spec{}, err
	}
	spec := gamebuild.Spec{FestID: festID, Type: gameType, Entrants: entrants}
	read := formatForms[gameType].read
	for _, d := range games.All() {
		if v := formatForms[d.Code].variant; v != nil && v.value == gameType {
			spec.Type, read = d.Code, v.read
		}
	}
	def := games.Get(spec.Type)
	spec.Label = cmp.Or(strings.TrimSpace(form.Get("title")), def.Title)
	if field, ok := dslFieldOf(spec.Type); ok {
		spec.DSL = strings.TrimSpace(form.Get(field))
	}
	if read != nil {
		if err := read(&spec, form); err != nil {
			return spec, err
		}
	}
	return spec, nil
}

func (s *Server) createHostGame(ctx context.Context, festID int64, gameType string, form url.Values) (int64, error) {
	gameType = strings.TrimSpace(gameType)
	if !games.Known(gameType) && gameType != ksiStickersGameType {
		return 0, corei18n.User(dopestrings.Default.Host.Games.ErrorTypeMissing())
	}
	if err := s.ratingTours(ctx, gameType, form); err != nil {
		return 0, err
	}
	var gameID int64
	_, err := s.commit(ctx, festID, "game-create", nil, func(ctx context.Context, tx *sql.Tx) (core.FestWrite, error) {
		spec, err := gameSpecFromForm(ctx, tx, festID, gameType, form)
		if err != nil {
			return core.FestWrite{}, err
		}
		// The creation form carries the settings page's fields too, so a Game
		// is made with its title, slug and options at once.
		settings := settingsFromForm(form)
		settings.SchemeDSL = ""
		var written core.FestWrite
		gameID, written, err = festops.CreateGameWithSettingsTx(ctx, tx, spec, &settings)
		return written, err
	})
	return gameID, err
}

// ksiStickersGameType is the creation-form value for the "KSI with stickers"
// variant. It produces an ordinary KSI game (game_type "ksi") whose scheme
// carries a `stickers` block, so all serve/seed/roster paths keep working.
const ksiStickersGameType = "ksi_stickers"

// ksiStickerConfigFromForm reads the per-sticker colour and max-count inputs of
// the stickers creation form into a scheme `stickers` block. Each sticker is
// included only when its max is > 0.
func ksiStickerConfigFromForm(form url.Values) (json.RawMessage, error) {
	all := []struct {
		id, label, colorField, maxField, defColor string
	}{
		{games.KSIStickerNeutral, dopestrings.Default.Host.Games.StickerNeutral(), "ksis_neutral_color", "ksis_neutral_max", games.KSIStickerNeutralColor},
		{games.KSIStickerX2, "×2", "ksis_x2_color", "ksis_x2_max", games.KSIStickerX2Color},
		{games.KSIStickerNoWrong, dopestrings.Default.Host.Games.StickerNowrong(), "ksis_nowrong_color", "ksis_nowrong_max", games.KSIStickerNoWrongColor},
		{games.KSIStickerEmptyWrong, dopestrings.Default.Host.Games.StickerEmptywrong(), "ksis_emptywrong_color", "ksis_emptywrong_max", games.KSIStickerEmptyWrongColor},
	}
	cfg := games.KSIStickerConfig{}
	for _, s := range all {
		max, err := parseNonNegativeFormInt(form, s.maxField, dopestrings.Default.Host.Games.StickerMaxField(), 0, maxStickerCount)
		if err != nil {
			return nil, err
		}
		if max <= 0 {
			continue
		}
		maxCopy := max
		cfg.Types = append(cfg.Types, games.KSIStickerType{
			ID:    s.id,
			Label: s.label,
			Color: stickerColorFromForm(form, s.colorField, s.defColor),
			Max:   &maxCopy,
		})
	}
	if form.Get("ksis_plus_nominal") != "" {
		cfg.Plus = games.KSIPlusNominal
	}
	return json.Marshal(cfg)
}

// odMinusFromForm reads the OD minus fields; a wrong answer that costs
// nothing is a plain ChGK, and nil.
func odMinusFromForm(form url.Values) (*games.ODMinus, error) {
	s := dopestrings.Default
	wrong, err := parsePositiveFormInt(form, "mp_wrong", s.Host.Games.OdMinusWrongLabel(), 1, maxFormQuestions)
	if err != nil {
		return nil, err
	}
	bonus, err := parseNonNegativeFormInt(form, "mp_bonus", s.Host.Games.OdMinusBonusLabel(), 0, maxFormQuestions)
	if err != nil {
		return nil, err
	}
	return &games.ODMinus{Wrong: wrong, AllBonus: bonus}, nil
}

// toursKnobs are a ChGK sheet's tours: so many of so many questions, or a
// rating.chgk.info tournament whose tours it takes. prefix keeps each
// format's fields apart, since every section posts.
func toursKnobs(prefix string) []dopeui.Item {
	s := dopestrings.Default
	return []dopeui.Item{
		dopeui.Field(dopeui.Label(s.Host.Games.OdToursLabel()), dopeui.Textfield(dopeui.Name(prefix+"_tours"), dopeui.Inputmode("numeric"), dopeui.Value("3"))),
		dopeui.Field(dopeui.Label(s.Host.Games.OdQuestionsLabel()), dopeui.Textfield(dopeui.Name(prefix+"_questions"), dopeui.Inputmode("numeric"), dopeui.Value("15"))),
		dopeui.Field(dopeui.Label(s.Host.Games.OdRatingLabel()), dopeui.Textfield(dopeui.Name(prefix+"_rating_id"), dopeui.Inputmode("numeric"))),
	}
}

// readTours reads toursKnobs: the tours ratingTours fetched, else so many
// of so many questions.
func readTours(spec *gamebuild.Spec, form url.Values, prefix string) (err error) {
	s := dopestrings.Default
	if comp := strings.TrimSpace(form.Get(prefix + "_tour_comp")); comp != "" {
		spec.TourComp = games.ParseTourComp(`{"tourComp":"` + comp + `"}`)
		if len(spec.TourComp) == 0 || len(spec.TourComp) > maxFormTours ||
			slices.ContainsFunc(spec.TourComp, func(n int) bool { return n < 1 || n > maxFormQuestions }) {
			return corei18n.User(s.Host.Games.ErrorRatingNoTours(form.Get(prefix + "_rating_id")))
		}
		return nil
	}
	if spec.ODTours, err = parsePositiveFormInt(form, prefix+"_tours", s.Host.Games.OdToursLabel(), 1, maxFormTours); err != nil {
		return err
	}
	spec.ODQuestions, err = parsePositiveFormInt(form, prefix+"_questions", s.Host.Games.OdQuestionsLabel(), 1, maxFormQuestions)
	return err
}

// ratingTours fetches the tours of the rating.chgk.info tournament the form
// names for the format picked into <type>_tour_comp, before the write: the
// fetch is a network call and must not hold the write lock.
func (srv *Server) ratingTours(ctx context.Context, gameType string, form url.Values) error {
	raw := strings.TrimSpace(form.Get(gameType + "_rating_id"))
	if raw == "" {
		return nil
	}
	s := dopestrings.Default
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		return corei18n.User(s.Host.Games.ErrorRatingNoTours(raw))
	}
	tours, err := srv.h.Engine().RatingSite("").Tours(ctx, int64(id))
	if err != nil {
		return corei18n.User(s.Host.Games.ErrorRatingTours(raw, err.Error()))
	}
	comp := make([]string, 0, len(tours))
	for _, n := range tours {
		comp = append(comp, strconv.Itoa(n))
	}
	if len(comp) == 0 {
		return corei18n.User(s.Host.Games.ErrorRatingNoTours(raw))
	}
	form.Set(gameType+"_tour_comp", strings.Join(comp, ","))
	return nil
}

func stickerColorFromForm(form url.Values, field, fallback string) string {
	value := strings.TrimSpace(form.Get(field))
	if !isHexColor(value) {
		return fallback
	}
	return value
}

func isHexColor(value string) bool {
	if len(value) != shortHexColorLen && len(value) != longHexColorLen {
		return false
	}
	if value[0] != '#' {
		return false
	}
	for _, c := range value[1:] {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}
