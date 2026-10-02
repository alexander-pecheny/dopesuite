package hostpages

import (
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

	"dope/dope/domain/core"
	"dope/dope/domain/gamebuild"
	"dope/dope/domain/games"
	"dope/dope/domain/view"
	"dope/dope/platform/util"
	"dope/dope/storage/festwrite"
	"dope/dope/storage/journal"
	"dope/dope/storage/store"
	"dope/dope/web/pages"
	dopeui "dope/dope/web/ui"
	dopestrings "dope/i18nstrings"

	corei18n "pecheny.me/dopecore/i18nstrings"
	"pecheny.me/dopeuikit/palette"

	"dope/dope/web/route"
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
	BrainDSL     string
	SIDSL        string
	TroikaDSL    string
	HamsaDSL     string
	EKDSL        string
	ESDSL        string
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
	return strconv.FormatInt(o.ID, 10)
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

	page = append(page, dopeui.Form(dopeui.DirCol, dopeui.Method("post"), dopeui.Action("/host/fest/"+ref+"/game/new"),
		dopeui.Autocomplete("off"), dopeui.Data("game-create-form", ""),
		dopeui.Pickgroup(dopeui.Label(s.Host.Games.TypeLabel()),
			gameTypeRadio("od", s.Host.Games.TypeOd(), sel),
			gameTypeRadio("ksi", s.Host.Games.TypeKsi(), sel),
			gameTypeRadio("ksi_stickers", s.Host.Games.TypeKsiStickers(), sel),
			gameTypeRadio("brain", s.Host.Games.TypeBrain(), sel),
			gameTypeRadio("ek", s.Host.Games.TypeEk(), sel),
			gameTypeRadio("es", s.Host.Games.TypeEs(), sel),
			gameTypeRadio("si", s.Host.Games.TypeSi(), sel),
			gameTypeRadio("multi", s.Host.Games.TypeMulti(), sel),
			gameTypeRadio("troika", s.Host.Games.TypeTroika(), sel),
			gameTypeRadio("hamsa", s.Host.Games.TypeHamsa(), sel),
			gameTypeRadio("kd", s.Host.Games.TypeKd(), sel),
		),
		gameSettings("od", sel,
			dopeui.Field(dopeui.Label(s.Host.Games.OdToursLabel()), dopeui.Textfield(dopeui.Name("od_tours"), dopeui.Inputmode("numeric"), dopeui.Value("3"))),
			dopeui.Field(dopeui.Label(s.Host.Games.OdQuestionsLabel()), dopeui.Textfield(dopeui.Name("od_questions"), dopeui.Inputmode("numeric"), dopeui.Value("15"))),
			dopeui.Hint(dopeui.Text(s.Host.Games.WholeRosterHint())),
		),
		gameSettings("kd", sel,
			dopeui.Field(dopeui.Label(s.Host.Games.OdToursLabel()), dopeui.Textfield(dopeui.Name("kd_tours"), dopeui.Inputmode("numeric"), dopeui.Value("9"))),
			dopeui.Field(dopeui.Label(s.Host.Games.OdQuestionsLabel()), dopeui.Textfield(dopeui.Name("kd_questions"), dopeui.Inputmode("numeric"), dopeui.Value("4"))),
			dopeui.Field(dopeui.Label(s.Host.Games.KdTablesLabel()), dopeui.Textfield(dopeui.Name("kd_tables"), dopeui.Inputmode("numeric"), dopeui.Value("11"))),
			dopeui.Hint(dopeui.Text(s.Host.Games.KdHint())),
		),
		gameSettings("ksi", sel,
			dopeui.Field(dopeui.Label(s.Host.Games.ThemesLabel()), dopeui.Textfield(dopeui.Name("ksi_themes"), dopeui.Inputmode("numeric"), dopeui.Value("20"))),
			dopeui.Hint(dopeui.Text(s.Host.Games.WholeRosterHint())),
		),
		gameSettings("ksi_stickers", sel,
			dopeui.Field(dopeui.Label(s.Host.Games.ThemesLabel()), dopeui.Textfield(dopeui.Name("ksis_themes"), dopeui.Inputmode("numeric"), dopeui.Value("20"))),
			dopeui.Hint(dopeui.Text(s.Host.Games.WholeRosterHint())),
			dopeui.Hint(dopeui.Text(s.Host.Games.StickersHint())),
			stickerRow(s.Host.Games.StickerNeutral(), "ksis_neutral_max", "20", "ksis_neutral_color", "#ffffff"),
			stickerRow(s.Host.Games.StickerX2Row(), "ksis_x2_max", "2", "ksis_x2_color", "#fdf66f"),
			stickerRow(s.Host.Games.StickerNowrongRow(), "ksis_nowrong_max", "1", "ksis_nowrong_color", "#aded87"),
			stickerRow(s.Host.Games.StickerEmptywrongRow(), "ksis_emptywrong_max", "1", "ksis_emptywrong_color", "#ff7a6b"),
		),
		gameSettings("brain", sel,
			dopeui.Field(dopeui.Label(s.Host.Games.SchemeLabel()),
				dopeui.Editor(dopeui.Name("brain_dsl"), dopeui.Rows("14"), dopeui.Spellcheck("false"), dopeui.Text(data.BrainDSL))),
			dopeui.Hint(dopeui.Text(s.Host.Games.BrainHint())),
		),
		gameSettings("si", sel,
			dopeui.Field(dopeui.Label(s.Host.Games.SchemeLabel()),
				dopeui.Editor(dopeui.Name("si_dsl"), dopeui.Rows("14"), dopeui.Spellcheck("false"), dopeui.Text(data.SIDSL))),
			dopeui.Hint(dopeui.Text(s.Host.Games.SiHint())),
		),
		gameSettings("multi", sel,
			dopeui.Field(dopeui.Label(s.Host.Games.MinigamesLabel()),
				dopeui.Editor(dopeui.Name("multi_games"), dopeui.Rows("8"), dopeui.Spellcheck("false"),
					dopeui.Placeholder(s.Host.Games.MinigamesPlaceholder()))),
			dopeui.Hint(dopeui.Text(s.Host.Games.MinigamesHint())),
			dopeui.Hint(dopeui.Text(s.Host.Games.MinigamesShareHint())),
			dopeui.Hint(dopeui.Text(s.Host.Games.WholeRosterHint())),
			dopeui.Field(dopeui.Label(s.Host.Games.MultiSortingLabel()),
				dopeui.Textfield(dopeui.Name("multi_sorting"), dopeui.Placeholder("total, game2, plus"))),
			dopeui.Hint(dopeui.Text(s.Host.Games.MultiSortingHint())),
		),
		gameSettings("troika", sel,
			dopeui.Field(dopeui.Label(s.Host.Games.SchemeLabel()),
				dopeui.Editor(dopeui.Name("troika_dsl"), dopeui.Rows("16"), dopeui.Spellcheck("false"), dopeui.Text(data.TroikaDSL))),
			dopeui.Hint(dopeui.Text(s.Host.Games.TroikaHint())),
		),
		gameSettings("hamsa", sel,
			dopeui.Field(dopeui.Label(s.Host.Games.SchemeLabel()),
				dopeui.Editor(dopeui.Name("hamsa_dsl"), dopeui.Rows("18"), dopeui.Spellcheck("false"), dopeui.Text(data.HamsaDSL))),
			dopeui.Hint(dopeui.Text(s.Host.Games.HamsaHint())),
		),
		gameSettings("ek", sel,
			dopeui.Field(dopeui.Label(s.Host.Games.SchemeLabel()),
				dopeui.Editor(dopeui.Name("ek_dsl"), dopeui.Rows("10"), dopeui.Spellcheck("false"), dopeui.Text(data.EKDSL), dopeui.Placeholder("[scheme]\nkind: single_elimination\nparticipants: 48\nmatch_size: 4\nwinning_places: 2"))),
			dopeui.Hint(dopeui.Text(s.Host.Games.EkHint())),
			dopeui.Field(dopeui.Label(s.Host.Games.EkJsonLabel()),
				dopeui.Editor(dopeui.Name("ek_scheme"), dopeui.Rows("14"), dopeui.Placeholder(`{"slug":"...","title":"...","gameType":"ek","stages":[...]}`))),
		),
		gameSettings("es", sel,
			dopeui.Field(dopeui.Label(s.Host.Games.SchemeLabel()),
				dopeui.Editor(dopeui.Name("es_dsl"), dopeui.Rows("10"), dopeui.Spellcheck("false"), dopeui.Text(data.ESDSL), dopeui.Placeholder("[scheme]\nkind: single_elimination\nparticipants: 48\nmatch_size: 4\nwinning_places: 2\nplayers: 3"))),
			dopeui.Hint(dopeui.Text(s.Host.Games.EsHint())),
			dopeui.Field(dopeui.Label(s.Host.Games.EkJsonLabel()),
				dopeui.Editor(dopeui.Name("es_scheme"), dopeui.Rows("14"), dopeui.Placeholder(`{"slug":"...","title":"...","gameType":"es","stages":[...]}`))),
		),
		entrantPicker(data),
		dopeui.Row(submit...),
	))
	return &dopeui.Doc{Nodes: []dopeui.Node{dopeui.Page(page...)}}
}

// entrantFormats are the formats whose creation seats the entrants a host ticks
// — the ones described by a scheme. A flat format (OD, KSI, multi) seats the
// whole fest roster under the fest's own numbers and marks who did not play on
// its refusals tab, so it is not offered the picker at all and refuses a chosen
// list rather than dropping it (gamebuild.Create).
var entrantFormats = []string{games.Brain, games.SI, games.Troika, games.Hamsa, games.EK}

// SeatsChosenEntrants reports whether a format seats the entrant list a host
// picked, rather than the whole fest roster.
func SeatsChosenEntrants(gameType string) bool {
	return slices.Contains(entrantFormats, gameType)
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
	items := []dopeui.Item{dopeui.Data("game-entrants", strings.Join(entrantFormats, " "))}
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
				dopeui.Editor(dopeui.Name("brain_dsl"), dopeui.Rows("14"), dopeui.Spellcheck("false"), dopeui.Text(data.SchemeDSL))),
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
		var divisions []string
		if divisionsGame(gameType) {
			var err error
			if divisions, err = festDivisions(r.Context(), s.h.Engine().DB, festID); err != nil {
				return nil, err
			}
		}
		if submitted := strings.TrimSpace(r.Form.Get("brain_dsl")); submitted != "" && errMsg != "" {
			schemeDSL = r.Form.Get("brain_dsl")
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
			HasDSL:    gameType == games.Brain && schemeDSL != "",
			Divisions: divisions,
			Hidden:    store.ParseHiddenDivisions(hidden),
		}), nil
	})
}

// GameSettings is what a game's settings page edits: its title, its slug,
// and for a game built from the scheme language its scheme. An empty
// SchemeDSL leaves the scheme as it is.
type GameSettings struct {
	Title     string `json:"title"`
	Slug      string `json:"slug"`
	SchemeDSL string `json:"scheme_dsl"`
	// HiddenDivisions are the Flags whose divisions the game does not show; nil
	// leaves them as they are.
	HiddenDivisions *[]string `json:"hidden_divisions"`
}

// UpdateGameSettings saves a game's settings. A changed scheme recompiles the
// game, in the same transaction as the rename, so a refused recompile leaves
// nothing half-applied.
func (s *Server) UpdateGameSettings(reqCtx context.Context, festID, gameID int64, g GameSettings) error {
	title := strings.TrimSpace(g.Title)
	if title == "" {
		return corei18n.User(dopestrings.Default.Host.Games.ErrorTitleRequired())
	}
	slug := strings.TrimSpace(g.Slug)
	var slugValue any
	if slug != "" {
		if err := util.ValidateSlug(slug); err != nil {
			return corei18n.User(dopestrings.Default.Host.Games.ErrorSlugInvalid(err.Error()))
		}
		var count int
		if err := s.h.Engine().DB.QueryRowContext(reqCtx, `
select count(*) from games where fest_id = ? and slug = ? and id <> ?`, festID, slug, gameID).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			return corei18n.User(dopestrings.Default.Host.Games.ErrorSlugTaken())
		}
		slugValue = slug
	}
	err := s.h.Engine().WithWriteTx(reqCtx, festID, "game-settings", func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
update games set title = ?, slug = ?, updated_at = ? where id = ? and fest_id = ?`,
			title, slugValue, util.UtcNow(), gameID, festID); err != nil {
			return err
		}
		if g.HiddenDivisions != nil {
			var hidden any
			if cleaned := cleanDivisions(*g.HiddenDivisions); len(cleaned) > 0 {
				hidden = util.MustJSON(cleaned)
			}
			if _, err := tx.ExecContext(ctx, `update games set hidden_divisions = ? where id = ? and fest_id = ?`, hidden, gameID, festID); err != nil {
				return err
			}
		}
		if strings.TrimSpace(g.SchemeDSL) == "" {
			return nil
		}
		var stored string
		if err := tx.QueryRowContext(ctx, `
select coalesce(scheme_dsl, '') from games where id = ?`, gameID).Scan(&stored); err != nil {
			return err
		}
		if strings.TrimSpace(stored) == strings.TrimSpace(g.SchemeDSL) {
			return nil
		}
		if err := gamebuild.Recompile(ctx, tx, festID, gameID, g.SchemeDSL); err != nil {
			return err
		}
		// A Troika that now takes a division, or another one, seats its troikas.
		_, err := gamebuild.SyncDivisionEntrantsTx(ctx, tx, festID, 0)
		return err
	})
	if err != nil {
		return err
	}
	s.h.Engine().InvalidateFestViewCache(festID)
	return nil
}

// hiddenFromForm is the hidden divisions the settings form means: every offered
// one not ticked, and every one hidden before that is not offered now.
func (s *Server) hiddenFromForm(ctx context.Context, festID, gameID int64, shown []string) ([]string, error) {
	offered, err := festDivisions(ctx, s.h.Engine().DB, festID)
	if err != nil {
		return nil, err
	}
	var stored string
	if err := s.h.Engine().DB.QueryRowContext(ctx, `select coalesce(hidden_divisions, '') from games where id = ? and fest_id = ?`, gameID, festID).Scan(&stored); err != nil {
		return nil, err
	}
	ticked := map[string]bool{}
	for _, d := range shown {
		ticked[strings.TrimSpace(d)] = true
	}
	isOffered := map[string]bool{}
	var hidden []string
	for _, d := range offered {
		isOffered[d] = true
		if !ticked[d] {
			hidden = append(hidden, d)
		}
	}
	for _, d := range store.ParseHiddenDivisions(stored) {
		if !isOffered[d] {
			hidden = append(hidden, d)
		}
	}
	return cleanDivisions(hidden), nil
}

// cleanDivisions is a list of Flag short names trimmed, without blanks or
// repeats, in the order given.
func cleanDivisions(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, d := range in {
		d = strings.TrimSpace(d)
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return out
}

// festDivisions is every Flag the fest's teams carry, by short name, in the
// order the roster lists the teams and each team its Flags: the divisions a game
// that seats the fest's teams can offer.
func festDivisions(ctx context.Context, q store.Queryer, festID int64) ([]string, error) {
	flags, err := store.CollectRows(ctx, q, `
select f.short from fest_team_flags f join fest_teams t on t.id = f.team_id
where t.fest_id = ? and t.deleted = 0 and trim(f.short) != ''
order by t.position, t.id, f.position`, []any{festID}, func(rows *sql.Rows) (string, error) {
		var short string
		return short, rows.Scan(&short)
	})
	if err != nil {
		return nil, err
	}
	return cleanDivisions(flags), nil
}

// divisionsGame reports whether a game type shows divisions to choose among: the
// flat games, whose results tabs and screen carry the chips.
func divisionsGame(gameType string) bool {
	return gameType == games.OD || gameType == games.KSI || gameType == games.Multi
}

func (s *Server) handleHostUpdateGameSettings(w http.ResponseWriter, r *http.Request, festID, gameID int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	g := GameSettings{Title: r.Form.Get("title"), Slug: r.Form.Get("slug"), SchemeDSL: r.Form.Get("brain_dsl")}
	if r.Form.Get("divisions_present") != "" {
		// The boxes say which offered divisions are shown; the rest of the offered
		// ones are hidden, and one hidden before that no team carries now
		// stays hidden.
		hidden, err := s.hiddenFromForm(r.Context(), festID, gameID, r.Form["division_shown"])
		if err != nil {
			s.renderHostGameSettings(w, r, festID, gameID, err.Error())
			return
		}
		g.HiddenDivisions = &hidden
	}
	if err := s.UpdateGameSettings(r.Context(), festID, gameID, g); err != nil {
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
// off it.
func (s *Server) DeleteGame(reqCtx context.Context, festID, gameID int64) error {
	// Acquire the pooled connection BEFORE the write lock and bound the whole
	// write with festwrite.WriteTxTimeout, so a starved pool can never pin s.h.Engine().Mu (the
	// 2026-06-13 freeze). The lock is held across the post-commit active-game
	// pointer update, which is why this uses the lower-level trio rather than
	// withWriteTx.
	ctx, cancel := festwrite.AuditDetachedContext(reqCtx, festID)
	defer cancel()
	conn, err := s.h.Engine().AcquireWriteConn(ctx, "game-delete")
	if err != nil {
		return err
	}
	defer conn.Close()
	defer s.h.Engine().LockWrite("game-delete")()

	tx, err := s.h.Engine().BeginWriteTxConn(ctx, conn)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var title string
	if err := tx.QueryRowContext(ctx, `
select title from games where id = ? and fest_id = ?`, gameID, festID).Scan(&title); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `delete from games where id = ? and fest_id = ?`, gameID, festID); err != nil {
		return err
	}
	var nextGameID sql.NullInt64
	var nextMatchCode sql.NullString
	if err := tx.QueryRowContext(ctx, `
select g.id, coalesce((
  select m.code from matches m where m.game_id = g.id order by m.position, m.id limit 1
), '')
from games g
where g.fest_id = ?
order by g.position, g.id
limit 1`, festID).Scan(&nextGameID, &nextMatchCode); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err := festwrite.BumpFestRevisionTx(ctx, tx, festID, "game:delete", util.MustJSON(map[string]any{
		"gameID": gameID,
		"title":  title,
	})); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if s.h.Engine().FestID == festID && s.h.Engine().ActiveGameID == gameID {
		if nextGameID.Valid {
			s.h.Engine().ActiveGameID = nextGameID.Int64
			s.h.Engine().ActiveMatchCode = nextMatchCode.String
		} else {
			s.h.Engine().ActiveGameID = 0
			s.h.Engine().ActiveMatchCode = ""
		}
	}
	return nil
}

func (s *Server) handleHostDeleteGame(w http.ResponseWriter, r *http.Request, festID, gameID int64) {
	if err := s.DeleteGame(r.Context(), festID, gameID); err != nil {
		route.WriteError(w, r, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/host/fest/%s", s.festRefOrID(r.Context(), festID)), http.StatusSeeOther)
}

// ClearGame resets a game to its just-created state: it drops every
// game-scoped derived row (results, imported seeds/rosters, EK bracket
// resolution) and regenerates the pristine scheme/state — the same content a
// fresh game of this type would have — while keeping the game's id, code, slug
// and title so its URLs stay valid. Fest-scoped teams/players and the audit log
// are left intact (the latter is fest-scoped, like the delete path leaves it).
func (s *Server) ClearGame(ctx context.Context, festID, gameID int64) error {
	s.h.Engine().Mu.Lock()
	defer s.h.Engine().Mu.Unlock()

	tx, err := s.h.Engine().BeginWriteTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	firstMatchCode, err := gamebuild.Clear(ctx, tx, festID, gameID)
	if errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err != nil {
		return route.BadUser(err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if s.h.Engine().FestID == festID && s.h.Engine().ActiveGameID == gameID {
		s.h.Engine().ActiveMatchCode = firstMatchCode
	}
	s.h.Engine().InvalidateFestViewCache(festID)
	return nil
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
			BrainDSL:  kept("brain_dsl", gamebuild.DefaultBrainDSL(teamCount, 5)),
			SIDSL:     kept("si_dsl", defaultSIDSL(teamCount)),
			TroikaDSL: kept("troika_dsl", defaultTroikaDSL(teamCount)),
			HamsaDSL:  kept("hamsa_dsl", defaultHamsaDSL(teamCount)),
			EKDSL:     kept("ek_dsl", ""),
			ESDSL:     kept("es_dsl", ""),
			Entrants:  entrants,
		}), nil
	})
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

func (req GameCreateRequest) form() url.Values {
	form := url.Values{}
	for _, id := range req.Entrants {
		form.Add("entrant_id", strconv.FormatInt(id, 10))
	}
	for _, ref := range req.EntrantRefs {
		form.Add("entrant_id", ref)
	}
	if field, ok := dslField[req.GameType]; ok {
		form.Set(field, req.DSL)
	}
	if field, ok := schemeJSONField[req.GameType]; ok && len(req.Scheme) > 0 {
		form.Set(field, string(req.Scheme))
	}
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

// dslField names each format's scheme editor on the creation form. Every section
// is in the document at once and the page merely hides the ones not picked — a
// hidden field still posts — so one shared name would have handed a brain game's
// prefilled scheme to whatever type the host actually chose. A format absent
// here has no scheme of its own and its DSL is empty.
var dslField = map[string]string{
	games.Brain:  "brain_dsl",
	games.SI:     "si_dsl",
	games.Troika: "troika_dsl",
	games.Hamsa:  "hamsa_dsl",
	games.EK:     "ek_dsl",
	games.ES:     "es_dsl",
}

// schemeJSONField names each format's pasted-JSON editor, for the two that
// take a detailed scheme instead of a DSL.
var schemeJSONField = map[string]string{
	games.EK: "ek_scheme",
	games.ES: "es_scheme",
}

// gameSpecFromForm reads the creation form into what gamebuild needs: the
// format's label and DSL, the entrants ticked, and for the three pre-DSL
// formats their own knobs.
func gameSpecFromForm(ctx context.Context, tx *sql.Tx, festID int64, gameType string, form url.Values) (gamebuild.Spec, error) {
	entrants, err := gamebuild.ResolveEntrantRefsTx(ctx, tx, festID, chosenEntrantRefs(form))
	if err != nil {
		return gamebuild.Spec{}, err
	}
	spec := gamebuild.Spec{FestID: festID, Type: gameType, Entrants: entrants, DSL: strings.TrimSpace(form.Get(dslField[gameType]))}
	s := dopestrings.Default
	switch gameType {
	case games.OD:
		if spec.ODTours, err = parsePositiveFormInt(form, "od_tours", s.Host.Games.OdToursLabel(), 1, 20); err != nil {
			return spec, err
		}
		if spec.ODQuestions, err = parsePositiveFormInt(form, "od_questions", s.Host.Games.OdQuestionsLabel(), 1, 100); err != nil {
			return spec, err
		}
	case games.KD:
		if spec.ODTours, err = parsePositiveFormInt(form, "kd_tours", s.Host.Games.OdToursLabel(), 1, 20); err != nil {
			return spec, err
		}
		if spec.ODQuestions, err = parsePositiveFormInt(form, "kd_questions", s.Host.Games.OdQuestionsLabel(), 1, 100); err != nil {
			return spec, err
		}
		if spec.KDTables, err = parsePositiveFormInt(form, "kd_tables", s.Host.Games.KdTablesLabel(), 2, 199); err != nil {
			return spec, err
		}
	case games.KSI:
		if spec.KSIThemes, err = parsePositiveFormInt(form, "ksi_themes", s.Host.Games.ThemesLabel(), 1, 100); err != nil {
			return spec, err
		}
	case ksiStickersGameType:
		spec.Type = games.KSI
		if spec.KSIThemes, err = parsePositiveFormInt(form, "ksis_themes", s.Host.Games.ThemesLabel(), 1, 100); err != nil {
			return spec, err
		}
		if spec.KSIStickers, err = ksiStickerConfigFromForm(form); err != nil {
			return spec, err
		}
	case games.Multi:
		spec.Label = s.Host.Games.TypeMulti()
		if spec.Minigames, err = games.ParseMultiGames(form.Get("multi_games")); err != nil {
			return spec, corei18n.User(s.Host.Games.ErrorMinigames(err.Error()))
		}
		if spec.MultiSorting, err = games.ParseMultiSorting(spec.Minigames, form.Get("multi_sorting")); err != nil {
			return spec, corei18n.User(s.Host.Games.ErrorMultiSorting(err.Error()))
		}
	case games.Troika:
		spec.Label = s.Host.Games.TypeTroika()
	case games.Hamsa:
		spec.Label = s.Host.Games.TypeHamsa()
	case games.Brain:
		spec.Label = s.Host.Games.TypeBrain()
	case games.SI:
		spec.Label = s.Host.Games.TypeSi()
	case games.EK, games.ES:
		// EK's bracket is describable in the scheme language now that an
		// elimination counts Losses rather than seats, so a DSL wins over
		// the pasted JSON when both are offered. Erudit-Sextet plays the
		// same bracket and takes the same two ways of describing one.
		spec.Label = s.Host.Games.TypeEk()
		if gameType == games.ES {
			spec.Label = s.Host.Games.TypeEs()
		}
		if spec.DSL == "" {
			raw := strings.TrimSpace(form.Get(schemeJSONField[gameType]))
			if raw == "" {
				return spec, corei18n.User(s.Host.Games.ErrorEkSchemeMissing())
			}
			var scheme store.FestScheme
			if err := json.Unmarshal([]byte(raw), &scheme); err != nil {
				return spec, corei18n.User(s.Host.Games.ErrorJsonParse(err.Error()))
			}
			spec.Pasted = &scheme
		}
	}
	return spec, nil
}

func (s *Server) createHostGame(reqCtx context.Context, festID int64, gameType string, form url.Values) (int64, error) {
	if s.h.Engine().DB == nil {
		return 0, errors.New("sqlite is not enabled")
	}
	gameType = strings.TrimSpace(gameType)
	if !games.Known(gameType) && gameType != ksiStickersGameType {
		return 0, corei18n.User(dopestrings.Default.Host.Games.ErrorTypeMissing())
	}

	var gameID int64
	err := s.h.Engine().WithWriteTx(reqCtx, festID, "game-create", func(ctx context.Context, tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRowContext(ctx, `select count(*) from fests where id = ?`, festID).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return sql.ErrNoRows
		}

		spec, err := gameSpecFromForm(ctx, tx, festID, gameType, form)
		if err != nil {
			return err
		}
		if gameID, err = gamebuild.Create(ctx, tx, spec); err != nil {
			return err
		}
		if _, err = festwrite.BumpFestRevisionTx(ctx, tx, festID, "game:create", util.MustJSON(map[string]any{
			"gameID":   gameID,
			"gameType": gameType,
		})); err != nil {
			return err
		}
		// Genesis checkpoint: anchor per-game derived revert at the freshly-created
		// game so replay always has a checkpoint at or before any future edit.
		return journal.WriteGameCheckpoint(ctx, tx, gameID, core.JournalIDForSeqTx(ctx, tx))
	})
	return gameID, err
}

// defaultBrainDSL is the creation form's prefill: today's shortcut — one group
// over the whole fest — written in the DSL so the host sees something editable.
// defaultSIDSL is personal SI's shape at its smallest: one table, everyone at it,
// eight themes. A real tournament edits it into groups and a play-off.
func defaultSIDSL(players int) string {
	if players < 3 {
		players = 3
	}
	return fmt.Sprintf("[scheme]\nkind: roundrobin\ngroup_size: %d\nmatch_size: 3\nthemes: 8\nbout.points: seats + 1 - place\nsorting: [points, total, plus]\n", players)
}

// defaultTroikaDSL is Troika's regulations at their smallest: one group of
// everybody over six themes, ranked as the regulations rank — a rating score
// of 1 / 0.5 / 0 per Match plus game points over fifty, then head-to-head,
// taken, difference. A real tournament edits it into the group stages and the
// final.
func defaultTroikaDSL(participants int) string {
	if participants < 2 {
		participants = 2
	}
	return fmt.Sprintf("[scheme]\nkind: roundrobin\ngroup_size: %d\nthemes: 6\nmetric: total\npoints: [1, 0.5, 0]\nstandings.rating: points + taken / 50\nsorting: [rating, h2h, taken, diff]\n", participants)
}

// defaultHamsaDSL is the tournament's own shape at its smallest: a group stage
// of two Games four to a table, then a final of the four best. The scheme
// itself is in the Catalog — it is text a host reads and edits — and carries
// no `[init]` line naming the KSI qualifier, since a fest that has not played
// one yet would not compile it.
func defaultHamsaDSL(participants int) string {
	if participants < 4 {
		participants = 4
	}
	participants -= participants % 4
	return dopestrings.Default.Host.Games.HamsaScheme(strconv.Itoa(participants))
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
		{games.KSIStickerNeutral, dopestrings.Default.Host.Games.StickerNeutral(), "ksis_neutral_color", "ksis_neutral_max", "#ffffff"},
		{games.KSIStickerX2, "×2", "ksis_x2_color", "ksis_x2_max", "#fdf66f"},
		{games.KSIStickerNoWrong, dopestrings.Default.Host.Games.StickerNowrong(), "ksis_nowrong_color", "ksis_nowrong_max", "#aded87"},
		{games.KSIStickerEmptyWrong, dopestrings.Default.Host.Games.StickerEmptywrong(), "ksis_emptywrong_color", "ksis_emptywrong_max", "#ff7a6b"},
	}
	cfg := games.KSIStickerConfig{}
	for _, s := range all {
		max, err := parseNonNegativeFormInt(form, s.maxField, dopestrings.Default.Host.Games.StickerMaxField(), 0, 100)
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
	if len(value) != 4 && len(value) != 7 {
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
