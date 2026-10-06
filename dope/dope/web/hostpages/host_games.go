package hostpages

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"dope/dope/domain/core"
	"dope/dope/domain/gamebuild"
	"dope/dope/domain/games"
	"dope/dope/domain/view"
	"dope/dope/platform/util"
	"dope/dope/storage/store"
	"dope/dope/web/pages"
	dopeui "dope/dope/web/ui"
	dopestrings "dope/i18nstrings"

	corei18n "pecheny.me/dopecore/i18nstrings"
	"pecheny.me/dopecore/idstr"
	"pecheny.me/dopeuikit/palette"

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
	// Divisions is every division the game's teams offer, Hidden the ones it
	// does not show; nil Divisions draws no divisions field.
	Divisions []string
	Hidden    []string
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

// gameTypeRadio builds one game-type radio, pre-checked when it is the selected type.
func gameTypeRadio(value, label, selected string) *dopeui.Element {
	items := []dopeui.Item{dopeui.Name("game_type"), dopeui.Value(value)}
	if value == selected {
		items = append(items, dopeui.Checked())
	}
	return dopeui.Radio(append(items, dopeui.Text(label))...)
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

	submit := []dopeui.Item{dopeui.Data("game-submit", "")}
	if sel == "" {
		submit = append(submit, dopeui.Hidden())
	}
	submit = append(submit, dopeui.Button(dopeui.Submit(), dopeui.Text(s.Host.Games.CreateSubmit())))

	form := []dopeui.Item{dopeui.DirCol, dopeui.Method("post"), dopeui.Action("/host/fest/" + ref + "/game/new"),
		dopeui.Autocomplete("off"), dopeui.Data("game-create-form", ""),
		dopeui.Pickgroup(append([]dopeui.Item{dopeui.Label(s.Host.Games.TypeLabel())}, typeRadios(sel)...)...),
	}
	form = append(form, settingsSections(data)...)
	form = append(form, entrantPicker(data), dopeui.Row(submit...))
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
			s := dopestrings.Default
			return []dopeui.Item{
				dopeui.Field(dopeui.Label(s.Host.Games.OdToursLabel()), dopeui.Textfield(dopeui.Name("od_tours"), dopeui.Inputmode("numeric"), dopeui.Value("3"))),
				dopeui.Field(dopeui.Label(s.Host.Games.OdQuestionsLabel()), dopeui.Textfield(dopeui.Name("od_questions"), dopeui.Inputmode("numeric"), dopeui.Value("15"))),
				dopeui.Hint(dopeui.Text(s.Host.Games.WholeRosterHint())),
			}
		},
		read: func(spec *gamebuild.Spec, form url.Values) (err error) {
			s := dopestrings.Default
			if spec.ODTours, err = parsePositiveFormInt(form, "od_tours", s.Host.Games.OdToursLabel(), 1, maxFormTours); err != nil {
				return err
			}
			spec.ODQuestions, err = parsePositiveFormInt(form, "od_questions", s.Host.Games.OdQuestionsLabel(), 1, maxFormQuestions)
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

// dslFieldOf names a scheme format's DSL editor on the creation form, and
// jsonFieldOf its pasted-JSON editor. Every section is in the document at
// once and the page merely hides the ones not picked — a hidden field still
// posts — so one shared name would have handed a brain game's prefilled
// scheme to whatever type the host actually chose. A flat format has no
// scheme on the form and its DSL is empty.
func dslFieldOf(code string) (string, bool) {
	d, ok := games.Lookup(code)
	if !ok || d.Flat {
		return "", false
	}
	return code + "_dsl", true
}

func jsonFieldOf(code string) (string, bool) {
	d, ok := games.Lookup(code)
	if !ok || !d.PastedScheme {
		return "", false
	}
	return code + "_scheme", true
}

// typeRadios is the game-type picker: every registered format in the
// registry's order, each followed by its variants.
func typeRadios(selected string) []dopeui.Item {
	var out []dopeui.Item
	for _, d := range games.All() {
		out = append(out, gameTypeRadio(d.Code, d.Title, selected))
		if v := formatForms[d.Code].variant; v != nil {
			out = append(out, gameTypeRadio(v.value, v.label, selected))
		}
	}
	return out
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
			if field, ok := jsonFieldOf(d.Code); ok {
				kids = append(kids, dopeui.Field(dopeui.Label(s.Host.Games.EkJsonLabel()),
					dopeui.Editor(dopeui.Name(field), dopeui.Rows("14"), dopeui.Placeholder(`{"slug":"...","title":"...","gameType":"`+d.Code+`","stages":[...]}`))))
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
		hidden := map[string]bool{}
		for _, d := range data.Hidden {
			hidden[d] = true
		}
		boxes := []dopeui.Item{dopeui.Hiddenfield(dopeui.Name("divisions_present"), dopeui.Value("1"))}
		for _, d := range data.Divisions {
			items := []dopeui.Item{dopeui.Name("division_shown"), dopeui.Value(d), dopeui.Text(d)}
			if !hidden[d] {
				items = append(items, dopeui.Checked())
			}
			boxes = append(boxes, dopeui.Checkbox(items...))
		}
		form = append(form,
			dopeui.Field(dopeui.Label(s.Host.Games.DivisionsLabel()), dopeui.Row(append([]dopeui.Item{dopeui.SpaceMD}, boxes...)...)),
			dopeui.Hint(dopeui.Text(s.Host.Games.DivisionsHint())),
		)
	}
	if data.HasDSL {
		form = append(form,
			dopeui.Field(dopeui.Label(s.Host.Games.SchemeLabel()),
				dopeui.Editor(dopeui.Name("scheme_dsl"), dopeui.Rows("14"), dopeui.Spellcheck("false"), dopeui.Text(data.SchemeDSL))),
			dopeui.Hint(dopeui.Text(s.Host.Games.RebuildHint())),
		)
	}
	form = append(form, dopeui.Row(dopeui.Button(dopeui.Submit(), dopeui.Text(s.Host.Games.SaveSubmit()))))
	page = append(page, dopeui.Form(form...))
	return &dopeui.Doc{Nodes: []dopeui.Node{dopeui.Page(page...)}}
}

func (s *Server) renderHostGameSettings(w http.ResponseWriter, r *http.Request, festID, gameID int64, errMsg string) {
	s.festPage(w, r, festID, func(fest view.HostFest) (*dopeui.Doc, error) {
		var (
			code      string
			title     string
			gameType  string
			slug      sql.NullString
			schemeDSL string
			hidden    string
		)
		if err := s.h.Engine().DB.QueryRowContext(r.Context(), `
select code, title, game_type, slug, coalesce(scheme_dsl, ''), coalesce(hidden_divisions, '') from games where id = ? and fest_id = ?`, gameID, festID).Scan(&code, &title, &gameType, &slug, &schemeDSL, &hidden); err != nil {
			return nil, err
		}
		divisions, err := festops.OfferedDivisions(r.Context(), s.h.Engine().DB, festID, gameType)
		if err != nil {
			return nil, err
		}
		if submitted := strings.TrimSpace(r.Form.Get("scheme_dsl")); submitted != "" && errMsg != "" {
			schemeDSL = r.Form.Get("scheme_dsl")
		}
		return hostGameSettingsDoc(hostGameSettingsData{
			Fest: fest,
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
			HasDSL:    games.Get(gameType).DSL == games.DSLEditable && schemeDSL != "",
			Divisions: divisions,
			Hidden:    store.ParseHiddenDivisions(hidden),
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
	g := GameSettings{Title: r.Form.Get("title"), Slug: r.Form.Get("slug"), SchemeDSL: r.Form.Get("scheme_dsl")}
	if r.Form.Get("divisions_present") != "" {
		// The boxes say which offered Divisions are shown (festops.Settings).
		shown := r.Form["division_shown"]
		g.ShownDivisions = &shown
	}
	if err := s.UpdateGameSettings(r.Context(), festID, gameID, g); errors.Is(err, sql.ErrNoRows) {
		// The Game left the fest after the router found it.
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.renderHostGameSettings(w, r, festID, gameID, err.Error())
		return
	}
	gameRef := strings.TrimSpace(g.Slug)
	if gameRef == "" {
		gameRef = fmt.Sprintf("%d", gameID)
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
		return hostGameCreateDoc(hostGameCreateData{
			Fest: fest, Error: errMsg, SelectedType: selectedType,
			DSL:      createFormDSLs(teamCount, kept),
			Entrants: entrants,
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
	GameType string  `json:"game_type"`
	Entrants []int64 `json:"entrants"`
	// EntrantRefs are more entrants by the ref GET …/entrants gives, which
	// also names a rating player who is not a Participant yet ("fp<id>").
	EntrantRefs []string `json:"entrant_refs"`
	// DSL is the format's scheme in the scheme language (brain, si, troika,
	// hamsa, ek, es). Scheme is a pasted JSON scheme, for ek and es only.
	DSL         string          `json:"dsl"`
	Scheme      json.RawMessage `json:"scheme"`
	ODTours     int             `json:"od_tours"`
	ODQuestions int             `json:"od_questions"`
	// KDTables is how many tables a friendship cup seats; its tours and
	// questions come in od_tours and od_questions.
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
}

// setKnobs sets the flat formats' number fields the request gives.
func (req GameCreateRequest) setKnobs(form url.Values) {
	setIfGiven := func(key string, v int) {
		if v != 0 {
			form.Set(key, strconv.Itoa(v))
		}
	}
	setIfGiven("od_tours", req.ODTours)
	setIfGiven("od_questions", req.ODQuestions)
	setIfGiven("kd_tours", req.ODTours)
	setIfGiven("kd_questions", req.ODQuestions)
	setIfGiven("kd_tables", req.KDTables)
	setIfGiven("ksi_themes", req.KSIThemes)
	setIfGiven("ksis_themes", req.KSIThemes)
}

func (req GameCreateRequest) form() url.Values {
	form := url.Values{}
	for _, id := range req.Entrants {
		form.Add("entrant_id", idstr.Format(id))
	}
	for _, ref := range req.EntrantRefs {
		form.Add("entrant_id", ref)
	}
	if field, ok := dslFieldOf(req.GameType); ok {
		form.Set(field, req.DSL)
	}
	if field, ok := jsonFieldOf(req.GameType); ok && len(req.Scheme) > 0 {
		form.Set(field, string(req.Scheme))
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
	spec.Label = def.Title
	if field, ok := dslFieldOf(spec.Type); ok {
		spec.DSL = strings.TrimSpace(form.Get(field))
	}
	if read != nil {
		if err := read(&spec, form); err != nil {
			return spec, err
		}
	}
	// A format that takes a pasted JSON scheme (EK, Erudit-Sextet) is
	// describable in the scheme language too, now that an elimination counts
	// Losses rather than seats, so a DSL wins over the pasted JSON when both
	// are offered.
	if field, ok := jsonFieldOf(spec.Type); ok && spec.DSL == "" {
		if spec.Pasted, err = pastedScheme(form.Get(field)); err != nil {
			return spec, err
		}
	}
	return spec, nil
}

// pastedScheme parses the JSON scheme pasted into the creation form.
func pastedScheme(raw string) (*store.FestScheme, error) {
	s := dopestrings.Default
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, corei18n.User(s.Host.Games.ErrorEkSchemeMissing())
	}
	var scheme store.FestScheme
	if err := json.Unmarshal([]byte(raw), &scheme); err != nil {
		return nil, corei18n.User(s.Host.Games.ErrorJsonParse(err.Error()))
	}
	return &scheme, nil
}

func (s *Server) createHostGame(ctx context.Context, festID int64, gameType string, form url.Values) (int64, error) {
	gameType = strings.TrimSpace(gameType)
	if !games.Known(gameType) && gameType != ksiStickersGameType {
		return 0, corei18n.User(dopestrings.Default.Host.Games.ErrorTypeMissing())
	}
	var gameID int64
	_, err := s.commit(ctx, festID, "game-create", nil, func(ctx context.Context, tx *sql.Tx) (core.FestWrite, error) {
		spec, err := gameSpecFromForm(ctx, tx, festID, gameType, form)
		if err != nil {
			return core.FestWrite{}, err
		}
		var written core.FestWrite
		gameID, written, err = festops.CreateGameTx(ctx, tx, spec)
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
	return json.Marshal(cfg)
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
