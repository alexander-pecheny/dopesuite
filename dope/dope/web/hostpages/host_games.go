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
	"time"

	"dope/dope/domain/core"
	"dope/dope/domain/games"
	"dope/dope/domain/imports"
	"dope/dope/domain/protocol"
	"dope/dope/domain/resolver"
	"dope/dope/domain/roster"
	"dope/dope/domain/schemedsl"
	"dope/dope/domain/view"
	"dope/dope/platform/util"
	"dope/dope/storage/festwrite"
	"dope/dope/storage/journal"
	"dope/dope/storage/store"
	"dope/dope/storage/storeutil"
	"dope/dope/web/pages"
	dopeui "dope/dope/web/ui"

	"pecheny.me/dopeuikit/palette"
)

type hostGameSettingsData struct {
	Fest      view.HostFest
	Game      PublicFestGame
	Slug      string
	Error     string
	SchemeDSL string
	HasDSL    bool
}

type hostGameCreateData struct {
	Fest         view.HostFest
	Error        string
	SelectedType string
	BrainDSL     string
	SIDSL        string
	// Entrants is the фест's registry offered as this Game's entrant list.
	// A Game numbers whom it seats from 1 (ADR-0009), so a фест of 65 can hold
	// an ЭК of 48 and a брейн of a different 48.
	Entrants []gameEntrantOption
}

type gameEntrantOption struct {
	ID    int64
	Label string
}

type gameIdentity struct {
	Code     string
	Title    string
	Position int
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
			dopeui.Field(dopeui.Label("Макс."), dopeui.Textfield(dopeui.Name(maxName), dopeui.Inputmode("numeric"), dopeui.Value(maxVal))),
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
	page := []dopeui.Item{
		dopeui.Title(data.Fest.Title + " · новая игра"), dopeui.PagePublic, dopeui.Classicscripts("dist/gamecreate.js"),
		dopeui.Publictopbar(pages.Trail(pages.FestCrumbs(ref, data.Fest.Title), "Добавить игру")),
	}
	if data.Error != "" {
		page = append(page, dopeui.Empty(dopeui.Text(data.Error)))
	}

	submit := []dopeui.Item{dopeui.Data("game-submit", "")}
	if sel == "" {
		submit = append(submit, dopeui.Hidden())
	}
	submit = append(submit, dopeui.Button(dopeui.Submit(), dopeui.Text("Создать")))

	page = append(page, dopeui.Form(dopeui.DirCol, dopeui.Method("post"), dopeui.Action("/host/fest/"+ref+"/game/new"),
		dopeui.Autocomplete("off"), dopeui.Data("game-create-form", ""),
		dopeui.Pickgroup(dopeui.Label("Тип игры"),
			gameTypeRadio("od", "ОД", sel),
			gameTypeRadio("ksi", "КСИ", sel),
			gameTypeRadio("ksi_stickers", "КСИ со стикерами", sel),
			gameTypeRadio("brain", "Брейн", sel),
			gameTypeRadio("ek", "ЭК", sel),
			gameTypeRadio("si", "Личная СИ", sel),
		),
		gameSettings("od", sel,
			dopeui.Field(dopeui.Label("Количество туров"), dopeui.Textfield(dopeui.Name("od_tours"), dopeui.Inputmode("numeric"), dopeui.Value("3"))),
			dopeui.Field(dopeui.Label("Количество вопросов в туре"), dopeui.Textfield(dopeui.Name("od_questions"), dopeui.Inputmode("numeric"), dopeui.Value("15"))),
		),
		gameSettings("ksi", sel,
			dopeui.Field(dopeui.Label("Количество тем"), dopeui.Textfield(dopeui.Name("ksi_themes"), dopeui.Inputmode("numeric"), dopeui.Value("20"))),
		),
		gameSettings("ksi_stickers", sel,
			dopeui.Field(dopeui.Label("Количество тем"), dopeui.Textfield(dopeui.Name("ksis_themes"), dopeui.Inputmode("numeric"), dopeui.Value("20"))),
			dopeui.Hint(dopeui.Text("Для каждого стикера задайте, сколько штук есть у команды (0 — стикер не используется) и цвет для подсветки. «Обычный» стикер работает как стандартная тема КСИ.")),
			stickerRow("Обычный", "ksis_neutral_max", "20", "ksis_neutral_color", "#ffffff"),
			stickerRow("×2 (правильные и неправильные удваиваются)", "ksis_x2_max", "2", "ksis_x2_color", "#fdf66f"),
			stickerRow("Без минуса (неправильные = 0)", "ksis_nowrong_max", "1", "ksis_nowrong_color", "#aded87"),
			stickerRow("Пустой = минус (пустые = −номинал)", "ksis_emptywrong_max", "1", "ksis_emptywrong_color", "#ff7a6b"),
		),
		gameSettings("brain", sel,
			dopeui.Field(dopeui.Label("Схема"),
				dopeui.Editor(dopeui.Name("brain_dsl"), dopeui.Rows("14"), dopeui.Spellcheck("false"), dopeui.Text(data.BrainDSL))),
			dopeui.Hint(dopeui.Text("Формат описан в docs/scheme-dsl.md: блоки [scheme] через ---, типы roundrobin / single_elimination / double_elimination.")),
		),
		gameSettings("si", sel,
			dopeui.Field(dopeui.Label("Схема"),
				dopeui.Editor(dopeui.Name("brain_dsl"), dopeui.Rows("14"), dopeui.Spellcheck("false"), dopeui.Text(data.SIDSL))),
			dopeui.Hint(dopeui.Text("Тот же язык схем: за столом сидят игроки, а не команды. Бой на троих — match_size: 3, проходят двое — winning_places: 2.")),
		),
		gameSettings("ek", sel,
			dopeui.Field(dopeui.Label("Схема"),
				dopeui.Editor(dopeui.Name("brain_dsl"), dopeui.Rows("10"), dopeui.Spellcheck("false"), dopeui.Placeholder("[scheme]\ntype: single_elimination\nteams: 48\nmatch_size: 4\nwinning_places: 2"))),
			dopeui.Hint(dopeui.Text("Либо схемой, либо готовым JSON ниже — что заполнено, то и используется.")),
			dopeui.Field(dopeui.Label("JSON-схема"),
				dopeui.Editor(dopeui.Name("ek_scheme"), dopeui.Rows("14"), dopeui.Placeholder(`{"slug":"...","title":"...","gameType":"ek","stages":[...]}`))),
		),
		entrantPicker(data),
		dopeui.Row(submit...),
	))
	return &dopeui.Doc{Nodes: []dopeui.Node{dopeui.Page(page...)}}
}

// entrantPicker offers the фест's registry as this Game's entrant list. Ticking
// nothing means everyone, which is what a one-game фест wants and what every
// Game did before Games could differ.
func entrantPicker(data hostGameCreateData) dopeui.Item {
	if len(data.Entrants) == 0 {
		return dopeui.Empty()
	}
	boxes := make([]dopeui.Item, 0, len(data.Entrants)+1)
	boxes = append(boxes,
		dopeui.Hint(dopeui.Text("Отметьте, кто играет в этой игре. Если не отметить никого, играют все — "+
			"а номера игра раздаёт свои, с единицы, так что одна и та же команда бывает второй в ЭК и четвёртой в ОД. "+
			"Командная игра сажает за стол команды, личная — игроков; сначала команды, потом игроки.")))
	for _, entrant := range data.Entrants {
		boxes = append(boxes, dopeui.Checkbox(dopeui.Name("entrant_id"),
			dopeui.Value(strconv.FormatInt(entrant.ID, 10)), dopeui.Text(entrant.Label)))
	}
	return dopeui.Details(dopeui.Summary(dopeui.Text("Состав игры")), dopeui.Col(boxes...))
}

// hostGameSettingsDoc builds a game's settings page: a small form to rename the
// game and set its slug (its type is shown read-only).
func hostGameSettingsDoc(data hostGameSettingsData) *dopeui.Doc {
	festRef := data.Fest.Ref()
	page := []dopeui.Item{
		dopeui.Title(data.Game.Title + " · " + data.Fest.Title), dopeui.PagePublic,
		dopeui.Publictopbar(pages.Trail(pages.FestCrumbs(festRef, data.Fest.Title), data.Game.Title)),
	}
	if data.Error != "" {
		page = append(page, dopeui.Empty(dopeui.Text(data.Error)))
	}
	form := []dopeui.Item{dopeui.DirCol, dopeui.Method("post"),
		dopeui.Action("/host/fest/" + festRef + "/game/" + data.Game.Ref() + "/settings"), dopeui.Autocomplete("off"),
		dopeui.Field(dopeui.Label("Тип игры"), dopeui.Textfield(dopeui.Value(data.Game.Type), dopeui.Disabled())),
		dopeui.Field(dopeui.Label("Название"), dopeui.Textfield(dopeui.Name("title"), dopeui.Value(data.Game.Title), dopeui.Required())),
		dopeui.Field(dopeui.Label("Slug (необязательно, a-z, 0-9, дефис)"), dopeui.Textfield(dopeui.Name("slug"), dopeui.Value(data.Slug), dopeui.Pattern("[a-z0-9-]+"))),
	}
	if data.HasDSL {
		form = append(form,
			dopeui.Field(dopeui.Label("Схема"),
				dopeui.Editor(dopeui.Name("brain_dsl"), dopeui.Rows("14"), dopeui.Spellcheck("false"), dopeui.Text(data.SchemeDSL))),
			dopeui.Hint(dopeui.Text("Пересборка меняет только не начатые бои: можно поменять число вопросов или добавить блок, но начатый бой должен сохраниться без изменений.")),
		)
	}
	form = append(form, dopeui.Row(dopeui.Button(dopeui.Submit(), dopeui.Text("Сохранить"))))
	page = append(page, dopeui.Form(form...))
	return &dopeui.Doc{Nodes: []dopeui.Node{dopeui.Page(page...)}}
}

func (s *Server) renderHostGameSettings(w http.ResponseWriter, r *http.Request, festID, gameID int64, errMsg string) {
	fest, err := s.h.LoadHostFestHeader(r.Context(), festID)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var (
		code      string
		title     string
		gameType  string
		slug      sql.NullString
		schemeDSL string
	)
	if err := s.h.Engine().DB.QueryRowContext(r.Context(), `
select code, title, game_type, slug, coalesce(scheme_dsl, '') from games where id = ? and fest_id = ?`, gameID, festID).Scan(&code, &title, &gameType, &slug, &schemeDSL); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if submitted := strings.TrimSpace(r.Form.Get("brain_dsl")); submitted != "" && errMsg != "" {
		schemeDSL = r.Form.Get("brain_dsl")
	}
	pages.RenderDoc(w, s.h.Engine().AssetETags, hostGameSettingsDoc(hostGameSettingsData{
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
	}))
}

func (s *Server) handleHostUpdateGameSettings(w http.ResponseWriter, r *http.Request, festID, gameID int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	title := strings.TrimSpace(r.Form.Get("title"))
	if title == "" {
		s.renderHostGameSettings(w, r, festID, gameID, "Название обязательно.")
		return
	}
	slug := strings.TrimSpace(r.Form.Get("slug"))
	var slugValue any
	if slug != "" {
		if err := util.ValidateSlug(slug); err != nil {
			s.renderHostGameSettings(w, r, festID, gameID, "Slug: "+err.Error())
			return
		}
		var count int
		if err := s.h.Engine().DB.QueryRowContext(r.Context(), `
select count(*) from games where fest_id = ? and slug = ? and id <> ?`, festID, slug, gameID).Scan(&count); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if count > 0 {
			s.renderHostGameSettings(w, r, festID, gameID, "Slug уже занят в этом фесте.")
			return
		}
		slugValue = slug
	}
	// One transaction for the whole save: a refused recompile must not leave a
	// half-applied rename behind.
	err := s.h.Engine().WithWriteTx(r.Context(), festID, "game-settings", func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
update games set title = ?, slug = ?, updated_at = ? where id = ? and fest_id = ?`,
			title, slugValue, util.UtcNow(), gameID, festID); err != nil {
			return err
		}
		dsl := r.Form.Get("brain_dsl")
		if strings.TrimSpace(dsl) == "" {
			return nil
		}
		var stored string
		if err := tx.QueryRowContext(ctx, `
select coalesce(scheme_dsl, '') from games where id = ?`, gameID).Scan(&stored); err != nil {
			return err
		}
		if strings.TrimSpace(stored) == strings.TrimSpace(dsl) {
			return nil
		}
		return recompileBrainGameTx(ctx, tx, festID, gameID, dsl)
	})
	if err != nil {
		s.renderHostGameSettings(w, r, festID, gameID, err.Error())
		return
	}
	s.h.Engine().InvalidateFestViewCache(festID)
	gameRef := slug
	if gameRef == "" {
		gameRef = fmt.Sprintf("%d", gameID)
	}
	http.Redirect(w, r, fmt.Sprintf("/host/fest/%s/game/%s/settings", s.festRefOrID(r.Context(), festID), gameRef), http.StatusSeeOther)
}

func (s *Server) handleHostDeleteGame(w http.ResponseWriter, r *http.Request, festID, gameID int64) {
	// Acquire the pooled connection BEFORE the write lock and bound the whole
	// write with festwrite.WriteTxTimeout, so a starved pool can never pin s.h.Engine().Mu (the
	// 2026-06-13 freeze). The lock is held across the post-commit active-game
	// pointer update, which is why this uses the lower-level trio rather than
	// withWriteTx.
	ctx, cancel := festwrite.AuditDetachedContext(r.Context(), festID)
	defer cancel()
	conn, err := s.h.Engine().AcquireWriteConn(ctx, "game-delete")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer conn.Close()
	defer s.h.Engine().LockWrite("game-delete")()

	tx, err := s.h.Engine().BeginWriteTxConn(ctx, conn)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	var title string
	if err := tx.QueryRowContext(ctx, `
select title from games where id = ? and fest_id = ?`, gameID, festID).Scan(&title); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := tx.ExecContext(ctx, `delete from games where id = ? and fest_id = ?`, gameID, festID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
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
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := festwrite.BumpFestRevisionTx(ctx, tx, festID, "game:delete", util.MustJSON(map[string]any{
		"gameID": gameID,
		"title":  title,
	})); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
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
	http.Redirect(w, r, fmt.Sprintf("/host/fest/%s", s.festRefOrID(r.Context(), festID)), http.StatusSeeOther)
}

// handleHostClearGame resets a game to its just-created state: it drops every
// game-scoped derived row (results, imported seeds/rosters, EK bracket
// resolution) and regenerates the pristine scheme/state — the same content a
// fresh game of this type would have — while keeping the game's id, code, slug
// and title so its URLs stay valid. Fest-scoped teams/players and the audit log
// are left intact (the latter is fest-scoped, like the delete path leaves it).
func (s *Server) handleHostClearGame(w http.ResponseWriter, r *http.Request, festID, gameID int64) {
	s.h.Engine().Mu.Lock()
	defer s.h.Engine().Mu.Unlock()

	tx, err := s.h.Engine().BeginWriteTx(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	var gameType, title, schemeJSON, schemeDSL string
	if err := tx.QueryRowContext(r.Context(), `
select game_type, title, coalesce(scheme_json, '{}'), coalesce(scheme_dsl, '') from games where id = ? and fest_id = ?`,
		gameID, festID).Scan(&gameType, &title, &schemeJSON, &schemeDSL); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Drop all game-scoped derived rows. matches/stages cascade to their slots,
	// themes, answers, results and reseed entries (FKs are ON). Fest-scoped
	// teams/players are shared across games and intentionally left alone.
	for _, q := range []string{
		`delete from matches where game_id = ?`,
		`delete from stages where game_id = ?`,
		`delete from game_assignments where game_id = ?`,
		`delete from game_participants where game_id = ?`,
		`delete from game_team_players where game_id = ?`,
		`delete from game_player_team_overrides where game_id = ?`,
	} {
		if _, err := tx.ExecContext(r.Context(), q, gameID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// Preserve the game's display slug/title from its current scheme.
	var meta struct {
		Slug  string `json:"slug"`
		Title string `json:"title"`
	}
	_ = json.Unmarshal([]byte(schemeJSON), &meta)
	if strings.TrimSpace(meta.Title) == "" {
		meta.Title = title
	}

	now := util.UtcNow()
	status := "active"
	var newScheme, newState []byte

	switch gameType {
	case "od":
		tourComp := games.ParseTourComp(schemeJSON)
		if len(tourComp) == 0 {
			tourComp = []int{15}
		}
		newScheme, newState = games.ODEmptyGameJSON(meta.Slug, meta.Title, tourComp)
		teams, err := roster.LoadFestRosterImportTeamsTx(r.Context(), tx, festID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if len(teams) > 0 {
			if newScheme, err = roster.ApplyRosterToChGKScheme(string(newScheme), teams); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if newState, err = roster.ApplyRosterToChGKState(string(newState), teams, nil); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
	case "ksi":
		var sc struct {
			Themes   int             `json:"themes"`
			Stickers json.RawMessage `json:"stickers"`
		}
		_ = json.Unmarshal([]byte(schemeJSON), &sc)
		if sc.Themes <= 0 {
			sc.Themes = 20
		}
		// Preserve the sticker configuration across a clear-to-pristine so a
		// stickers game stays a stickers game (only the answers/choices reset).
		newScheme, newState = games.KSIStickersEmptyGameJSON(meta.Slug, meta.Title, sc.Themes, sc.Stickers)
		teams, err := roster.LoadFestRosterImportTeamsTx(r.Context(), tx, festID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if len(teams) > 0 {
			if newScheme, err = roster.ApplyRosterToKSIScheme(string(newScheme), teams); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if newState, err = roster.ApplyRosterToKSIState(string(newState), teams, sc.Themes); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
	case "brain":
		// Pre-DSL games get their shortcut scheme re-expressed in the DSL, so a
		// clear upgrades them onto the one authoring path.
		if strings.TrimSpace(schemeDSL) == "" {
			var count int
			if err := tx.QueryRowContext(r.Context(), `select count(*) from fest_teams where fest_id = ?`, festID).Scan(&count); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			schemeDSL = defaultBrainDSL(count, games.BrainQuestions(schemeJSON))
			if _, err := tx.ExecContext(r.Context(), `update games set scheme_dsl = ? where id = ?`, schemeDSL, gameID); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		scheme, err := brainSchemeFromDSLTx(r.Context(), tx, festID, meta.Slug, meta.Title, schemeDSL)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if newScheme, err = json.Marshal(scheme); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		newState = []byte("{}")
		if err := buildBrainStructureTx(r.Context(), tx, festID, gameID, scheme); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	case "ek":
		status = "pending"
		var scheme store.FestScheme
		if err := json.Unmarshal([]byte(schemeJSON), &scheme); err != nil {
			http.Error(w, fmt.Sprintf("не удалось разобрать схему ЭК: %v", err), http.StatusInternalServerError)
			return
		}
		scheme.Teams = nil // seeded teams come from a fresh import, not the scheme
		if newScheme, err = json.Marshal(scheme); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		newState = []byte("{}")
		if err := buildEKStructureTx(r.Context(), tx, festID, gameID, scheme, now); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	default:
		http.Error(w, "очистка не поддерживается для этого типа игры", http.StatusBadRequest)
		return
	}

	if _, err := tx.ExecContext(r.Context(), `
update games set scheme_json = ?, state_json = '{}', status = ?,
  team_list_source = 'fest', roster_source = 'fest', revision = revision + 1, updated_at = ?
where id = ? and fest_id = ?`, string(newScheme), status, now, gameID, festID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if gameType != "ek" && gameType != "brain" {
		matchID, err := store.FlatMatchID(r.Context(), tx, gameID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := festwrite.SetFlatGameStateTx(r.Context(), tx, matchID, string(newState)); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	var nextMatchCode sql.NullString
	if err := tx.QueryRowContext(r.Context(), `
select coalesce((select code from matches where game_id = ? order by position, id limit 1), '')`,
		gameID).Scan(&nextMatchCode); err != nil && !errors.Is(err, sql.ErrNoRows) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := festwrite.BumpFestRevisionTx(r.Context(), tx, festID, "game:clear", util.MustJSON(map[string]any{
		"gameID": gameID,
		"title":  title,
	})); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if s.h.Engine().FestID == festID && s.h.Engine().ActiveGameID == gameID {
		s.h.Engine().ActiveMatchCode = nextMatchCode.String
	}
	s.h.Engine().InvalidateFestViewCache(festID)
	http.Redirect(w, r, fmt.Sprintf("/host/fest/%s", s.festRefOrID(r.Context(), festID)), http.StatusSeeOther)
}

func (s *Server) renderHostCreateGamePage(w http.ResponseWriter, r *http.Request, festID int64, errMsg string, selectedType string) {
	fest, err := s.h.LoadHostFestHeader(r.Context(), festID)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	brainDSL := strings.TrimSpace(r.Form.Get("brain_dsl"))
	if brainDSL == "" {
		var count int
		_ = s.h.Engine().DB.QueryRowContext(r.Context(), `select count(*) from fest_teams where fest_id = ?`, festID).Scan(&count)
		brainDSL = defaultBrainDSL(count, 5)
	}
	var teamCount int
	_ = s.h.Engine().DB.QueryRowContext(r.Context(), `select count(*) from fest_teams where fest_id = ?`, festID).Scan(&teamCount)
	entrants, err := festEntrantOptions(r.Context(), s.h.Engine().DB, festID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	pages.RenderDoc(w, s.h.Engine().AssetETags, hostGameCreateDoc(hostGameCreateData{
		Fest: fest, Error: errMsg, SelectedType: selectedType,
		BrainDSL: brainDSL, SIDSL: defaultSIDSL(teamCount), Entrants: entrants,
	}))
}

// festEntrantOptions lists the фест's Participants a Game may seat, teams and
// players alike — which kind a Game wants depends on its format, and the picker
// offers both rather than guessing before the type is chosen.
func festEntrantOptions(ctx context.Context, db *sql.DB, festID int64) ([]gameEntrantOption, error) {
	return store.CollectRows(ctx, db, `
select id, name, coalesce(city, ''), roster from participants
where fest_id = ? order by roster desc, coalesce(nullif(number, 0), 1 << 30), id`,
		[]any{festID}, func(rows *sql.Rows) (gameEntrantOption, error) {
			var option gameEntrantOption
			var city, roster string
			if err := rows.Scan(&option.ID, &option.Label, &city, &roster); err != nil {
				return option, err
			}
			if city != "" {
				option.Label += " (" + city + ")"
			}
			return option, nil
		})
}

// chosenEntrantIDs reads the picker: whom this Game seats, in the фест's order.
// Nothing ticked means everyone, which is what every Game did before Games
// could name their own.
func chosenEntrantIDs(form url.Values) []int64 {
	var out []int64
	for _, raw := range form["entrant_id"] {
		if id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); err == nil && id > 0 {
			out = append(out, id)
		}
	}
	return out
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

func (s *Server) createHostGame(reqCtx context.Context, festID int64, gameType string, form url.Values) (int64, error) {
	if s.h.Engine().DB == nil {
		return 0, errors.New("sqlite is not enabled")
	}
	gameType = strings.TrimSpace(gameType)
	if gameType != games.OD && gameType != games.KSI && gameType != games.EK && gameType != games.Brain &&
		gameType != games.SI && gameType != ksiStickersGameType {
		return 0, errors.New("выберите тип игры")
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

		entrants := chosenEntrantIDs(form)
		var err error
		switch gameType {
		case games.OD:
			tours, err := parsePositiveFormInt(form, "od_tours", "Количество туров", 1, 20)
			if err != nil {
				return err
			}
			questions, err := parsePositiveFormInt(form, "od_questions", "Количество вопросов в туре", 1, 100)
			if err != nil {
				return err
			}
			gameID, err = createODGameTx(ctx, tx, festID, tours, questions)
			if err != nil {
				return err
			}
		case games.KSI:
			themes, err := parsePositiveFormInt(form, "ksi_themes", "Количество тем", 1, 100)
			if err != nil {
				return err
			}
			gameID, err = createKSIGameTx(ctx, tx, festID, themes, nil)
			if err != nil {
				return err
			}
		case ksiStickersGameType:
			themes, err := parsePositiveFormInt(form, "ksis_themes", "Количество тем", 1, 100)
			if err != nil {
				return err
			}
			stickers, err := ksiStickerConfigFromForm(form)
			if err != nil {
				return err
			}
			gameID, err = createKSIGameTx(ctx, tx, festID, themes, stickers)
			if err != nil {
				return err
			}
		case games.Brain:
			gameID, err = CreateSchemeGameForTx(ctx, tx, festID, games.Brain, "Брейн", form.Get("brain_dsl"), entrants)
			if err != nil {
				return err
			}
		case games.SI:
			gameID, err = CreateSchemeGameForTx(ctx, tx, festID, games.SI, "Личная СИ", form.Get("brain_dsl"), entrants)
			if err != nil {
				return err
			}
		case games.EK:
			// ЭК's bracket is describable in the scheme language now that an
			// elimination counts Losses rather than seats, so a DSL wins over
			// the pasted JSON when both are offered.
			if dsl := strings.TrimSpace(form.Get("brain_dsl")); dsl != "" {
				gameID, err = CreateSchemeGameForTx(ctx, tx, festID, games.EK, "ЭК", dsl, entrants)
				if err != nil {
					return err
				}
				break
			}
			raw := strings.TrimSpace(form.Get("ek_scheme"))
			if raw == "" {
				return errors.New("Вставьте JSON-схему ЭК или опишите её схемой")
			}
			var scheme store.FestScheme
			if err := json.Unmarshal([]byte(raw), &scheme); err != nil {
				return fmt.Errorf("Не удалось разобрать JSON: %w", err)
			}
			gameID, err = CreateEKGameTx(ctx, tx, festID, scheme)
			if err != nil {
				return err
			}
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

func nextGameIdentityTx(ctx context.Context, tx *sql.Tx, festID int64, gameType, titleBase string) (gameIdentity, error) {
	var position int
	if err := tx.QueryRowContext(ctx, `select coalesce(max(position), 0) + 1 from games where fest_id = ?`, festID).Scan(&position); err != nil {
		return gameIdentity{}, err
	}
	// Suffix only to break a collision. A фест may hold two games of one type
	// under names of their own — СтудЧР played личная СИ and ТПШ, both `si` —
	// and numbering the second «ТПШ 2» renames a tournament that had a name.
	title := titleBase
	for n := 2; ; n++ {
		var taken int
		if err := tx.QueryRowContext(ctx, `select count(*) from games where fest_id = ? and title = ?`, festID, title).Scan(&taken); err != nil {
			return gameIdentity{}, err
		}
		if taken == 0 {
			break
		}
		title = fmt.Sprintf("%s %d", titleBase, n)
	}
	for suffix := position; ; suffix++ {
		code := fmt.Sprintf("%s-%d", gameType, suffix)
		var existing int
		if err := tx.QueryRowContext(ctx, `select count(*) from games where fest_id = ? and code = ?`, festID, code).Scan(&existing); err != nil {
			return gameIdentity{}, err
		}
		if existing == 0 {
			return gameIdentity{Code: code, Title: title, Position: position}, nil
		}
	}
}

func createODGameTx(ctx context.Context, tx *sql.Tx, festID int64, tours, questions int) (int64, error) {
	identity, err := nextGameIdentityTx(ctx, tx, festID, "od", "ОД")
	if err != nil {
		return 0, err
	}
	tourComp := make([]int, tours)
	for i := range tourComp {
		tourComp[i] = questions
	}
	schemeJSON, stateJSON := games.ODEmptyGameJSON(identity.Code, identity.Title, tourComp)
	teams, err := roster.LoadFestRosterImportTeamsTx(ctx, tx, festID)
	if err != nil {
		return 0, err
	}
	if len(teams) > 0 {
		schemeJSON, err = roster.ApplyRosterToChGKScheme(string(schemeJSON), teams)
		if err != nil {
			return 0, err
		}
		stateJSON, err = roster.ApplyRosterToChGKState(string(stateJSON), teams, nil)
		if err != nil {
			return 0, err
		}
	}
	return insertJSONGameTx(ctx, tx, festID, identity, "od", schemeJSON, stateJSON)
}

// createBrainGameTx creates a brain game from its scheme DSL: the compiled
// Structure's stages and matches materialise upfront, each match pre-seeded
// with the pristine protocol state; seed slots seat the fest's numbered teams
// unless [init] declares a seed source to import later.
func createBrainGameTx(ctx context.Context, tx *sql.Tx, festID int64, dsl string) (int64, error) {
	return CreateSchemeGameTx(ctx, tx, festID, games.Brain, "Брейн", dsl)
}

// CreateSchemeGameTx creates a game of any type from a scheme DSL: compile,
// store the scheme with its source, and materialise the structure. Every game
// type reaches the same plumbing — the DSL is the way a bracket is described,
// not a брейн feature.
func CreateSchemeGameTx(ctx context.Context, tx *sql.Tx, festID int64, gameType, label, dsl string) (int64, error) {
	return CreateSchemeGameForTx(ctx, tx, festID, gameType, label, dsl, nil)
}

// CreateSchemeGameForTx is CreateSchemeGameTx with the Game's entrants spelled
// out: which of the фест's Participants play it, in seed order. Absent, the whole
// фест plays, which is what a one-game фест wants and what every caller did
// before Games could differ.
//
// A фест's Games rarely share an entrant list (ADR-0009): СтудЧР-2026 registered
// 65 teams, its ОД seated all of them, its ЭК seated 48 and its брейн a
// different 48. Numbers are dealt from 1 inside the Game, so the same team is
// «2» in one and «4» in another.
func CreateSchemeGameForTx(ctx context.Context, tx *sql.Tx, festID int64, gameType, label, dsl string, entrants []int64) (int64, error) {
	identity, err := nextGameIdentityTx(ctx, tx, festID, gameType, label)
	if err != nil {
		return 0, err
	}
	scheme, err := schemeForEntrantsTx(ctx, tx, festID, gameType, identity.Code, identity.Title, dsl, entrants)
	if err != nil {
		return 0, err
	}
	schemeJSON, err := json.Marshal(scheme)
	if err != nil {
		return 0, err
	}
	now := util.UtcNow()
	schemeID, err := store.InsertReturningID(ctx, tx, `
insert into schemes(slug, title, version, schema_json, created_at)
values(?, ?, 2, ?, ?)`, uniqueSchemeSlug(identity.Code), identity.Title, string(schemeJSON), now)
	if err != nil {
		return 0, err
	}
	gameID, err := store.InsertReturningID(ctx, tx, `
insert into games(fest_id, code, title, game_type, position, scheme_id, scheme_json, scheme_dsl, state_json, status, team_list_source, roster_source, revision, created_at, updated_at)
values(?, ?, ?, ?, ?, ?, ?, ?, '{}', 'active', 'fest', 'fest', 1, ?, ?)`,
		festID, identity.Code, identity.Title, gameType, identity.Position, schemeID, string(schemeJSON), dsl, now, now)
	if err != nil {
		return 0, err
	}
	if len(entrants) > 0 {
		if err := seatChosenTx(ctx, tx, gameID, entrants); err != nil {
			return 0, err
		}
	}
	if err := buildSchemeStructureTx(ctx, tx, festID, gameID, gameType, scheme); err != nil {
		return 0, err
	}
	if err := recordGameEntrantsTx(ctx, tx, gameID); err != nil {
		return 0, err
	}
	return gameID, nil
}

// hasAssignmentsTx reports whether the Game's seats are already claimed.
func hasAssignmentsTx(ctx context.Context, tx *sql.Tx, gameID int64) (bool, error) {
	var count int
	if err := tx.QueryRowContext(ctx, `
select count(*) from game_assignments where game_id = ?`, gameID).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

// seatChosenTx numbers the Game's chosen Participants from 1, in the order
// given. It runs before the Structure is built, so the Slots resolve against
// these numbers rather than against the фест's.
func seatChosenTx(ctx context.Context, tx *sql.Tx, gameID int64, entrants []int64) error {
	for i, participantID := range entrants {
		if _, err := tx.ExecContext(ctx, `
insert into game_assignments(game_id, basket, number, participant_id) values(?, 1, ?, ?)
on conflict(game_id, basket, number) do update set participant_id = excluded.participant_id`,
			gameID, i+1, participantID); err != nil {
			return err
		}
	}
	return nil
}

// recordGameEntrantsTx writes who plays this Game, in seed order and under the
// number the Game deals them. It reads back the seating rather than the list it
// was given, so the entrant list can never claim somebody the Structure did not
// seat. A team knocked out before its first бой is still visibly an entrant,
// which is the point of keeping the list at all.
func recordGameEntrantsTx(ctx context.Context, tx *sql.Tx, gameID int64) error {
	rows, err := tx.QueryContext(ctx, `
select participant_id, number from game_assignments
where game_id = ? and basket = 1 and participant_id is not null order by number`, gameID)
	if err != nil {
		return err
	}
	type entry struct {
		id     int64
		number int
	}
	var seated []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.id, &e.number); err != nil {
			rows.Close()
			return err
		}
		seated = append(seated, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for position, e := range seated {
		if _, err := tx.ExecContext(ctx, `
insert into game_participants(game_id, participant_id, position, number) values(?, ?, ?, ?)
on conflict(game_id, participant_id) do update set position = excluded.position, number = excluded.number`,
			gameID, e.id, position+1, e.number); err != nil {
			return err
		}
	}
	return nil
}

// brainSchemeFromDSLTx compiles a brain DSL into the detailed scheme. Without
// an [init] seed source, the fest's numbered teams are the seeding — entrant
// order is team numbers, not the roster view's alphabetical order — and the
// count must match the scheme's draw.
func brainSchemeFromDSLTx(ctx context.Context, tx *sql.Tx, festID int64, slug, title, dsl string) (store.FestScheme, error) {
	return schemeFromDSLTx(ctx, tx, festID, games.Brain, slug, title, dsl)
}

// gameEntrantsTx is who this Game seats, in its own seed order — empty for a
// Game created before Games could name their entrants, which then reads the
// фест's registry as it always did.
func gameEntrantsTx(ctx context.Context, tx *sql.Tx, gameID int64) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, `
select participant_id from game_participants where game_id = ? order by position`, gameID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func schemeFromDSLTx(ctx context.Context, tx *sql.Tx, festID int64, gameType, slug, title, dsl string) (store.FestScheme, error) {
	return schemeForEntrantsTx(ctx, tx, festID, gameType, slug, title, dsl, nil)
}

func schemeForEntrantsTx(ctx context.Context, tx *sql.Tx, festID int64, gameType, slug, title, dsl string, chosen []int64) (store.FestScheme, error) {
	if strings.TrimSpace(dsl) == "" {
		return store.FestScheme{}, errors.New("опишите схему игры")
	}
	doc, err := schemedsl.Parse(dsl)
	if err != nil {
		return store.FestScheme{}, err
	}
	input := schemedsl.Input{Slug: slug, Title: title, GameType: gameType}
	if seed, hasSeed := doc.Init.Str("seed"); !hasSeed {
		entrants, err := chosenEntrantsTx(ctx, tx, festID, gameType, chosen)
		if err != nil {
			return store.FestScheme{}, err
		}
		input.Entrants = entrants
	} else if seed != "random" && seed != "xlsx" {
		var known int
		if err := tx.QueryRowContext(ctx, `select count(*) from games where fest_id = ? and code = ?`, festID, seed).Scan(&known); err != nil {
			return store.FestScheme{}, err
		}
		if known == 0 {
			return store.FestScheme{}, fmt.Errorf("seed: %s — не random, не xlsx и не код игры этого феста", seed)
		}
	}
	return schemedsl.Compile(doc, input)
}

// chosenEntrantsTx turns the Game's chosen Participants into scheme entrants,
// numbered from 1 in the order given. Without a choice the whole фест plays.
func chosenEntrantsTx(ctx context.Context, tx *sql.Tx, festID int64, gameType string, chosen []int64) ([]store.SchemeSlot, error) {
	if len(chosen) == 0 {
		return seedEntrantsTx(ctx, tx, festID, gameType)
	}
	// A team format seats teams and an individual one players, so a chosen
	// Participant of the other kind is a mistake worth naming rather than a
	// seat left empty at the стол.
	want := "team"
	if games.IsIndividual(gameType) {
		want = "player"
	}
	entrants := make([]store.SchemeSlot, len(chosen))
	for i, participantID := range chosen {
		var name, roster string
		if err := tx.QueryRowContext(ctx, `
select name, roster from participants where id = ? and fest_id = ?`, participantID, festID).Scan(&name, &roster); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("участника %d нет в этом фесте", participantID)
			}
			return nil, err
		}
		if roster != want {
			if want == "player" {
				return nil, fmt.Errorf("%s — команда, а в этой игре за столом сидят игроки", name)
			}
			return nil, fmt.Errorf("%s — игрок, а в этой игре за столом сидят команды", name)
		}
		entrants[i] = store.SchemeSlot{Seed: &store.SchemeSeedRef{Basket: 1, Number: i + 1}, Label: name}
	}
	return entrants, nil
}

// seedEntrantsTx is the fest's own roster as scheme entrants: teams in a team
// format, players in an individual one. A Participant is whoever the format
// seats, and the seeding is where that first shows up.
func seedEntrantsTx(ctx context.Context, tx *sql.Tx, festID int64, gameType string) ([]store.SchemeSlot, error) {
	if games.IsIndividual(gameType) {
		return seedPlayerEntrantsTx(ctx, tx, festID)
	}
	teams, err := roster.LoadFestRosterImportTeamsTx(ctx, tx, festID)
	if err != nil {
		return nil, err
	}
	if len(teams) < 2 {
		return nil, errors.New("для схемы нужны хотя бы два участника в фесте")
	}
	sort.Slice(teams, func(i, j int) bool { return teams[i].Number < teams[j].Number })
	entrants := make([]store.SchemeSlot, len(teams))
	for i, team := range teams {
		if team.Number <= 0 {
			return nil, errors.New("перед созданием игры пронумеруйте участников феста")
		}
		entrants[i] = store.SchemeSlot{Seed: &store.SchemeSeedRef{Basket: 1, Number: int(team.Number)}, Label: team.Name}
	}
	return entrants, nil
}

// defaultBrainDSL is the creation form's prefill: today's shortcut — one group
// over the whole fest — written in the DSL so the host sees something editable.
func defaultBrainDSL(teams, questions int) string {
	if teams < 2 {
		teams = 4
	}
	if questions <= 0 {
		questions = 5
	}
	return fmt.Sprintf("[defaults]\nquestions: %d\n\n[scheme]\ntype: roundrobin\nteams_in_group: %d\n", questions, teams)
}

// defaultSIDSL is личная СИ's shape at its smallest: one table, everyone at it,
// eight themes. A real tournament edits it into groups and a play-off.
func defaultSIDSL(players int) string {
	if players < 3 {
		players = 3
	}
	return fmt.Sprintf("[scheme]\ntype: roundrobin\nteams_in_group: %d\nmatch_size: 3\nthemes: 8\nbout.points: seats + 1 - place\nsorting: [points, total, plus]\n", players)
}

// buildBrainStructureTx materialises a brain scheme into stage/match/slot rows.
// Seed slots are occupied immediately: each fest team is ensured a teams-table
// row by number (shared with the EK seed import) and pinned into its slot and
// game_assignments, so the group is playable the moment it exists.
// stageEmptyState builds a match's pristine Protocol document for a stage of a
// compiled scheme. The Protocol owns the shape — a брейн бой is a row of
// questions, a СИ бой a grid of themes — so the builder asks it rather than
// knowing. Falls back to брейн's for schemes compiled before Protocols carried
// their own config.
func stageEmptyState(gameType string, stage store.SchemeStage, seats, fallbackQuestions int) string {
	// A blob-shaped Protocol (ЭК, личная СИ) stores an empty document: its
	// seats come from the Slots and its marks arrive as edits. Seeding it with
	// the Protocol's own state shape would write an array where the blob keys
	// a map, and the first edit would fail to parse it.
	if (store.DBMatchState{GameType: gameType}).IsEKShaped() {
		return "{}"
	}
	p, ok := protocol.Get(gameType)
	if !ok {
		return string(games.BrainEmptyStateJSON(stageQuestions(stage, fallbackQuestions)))
	}
	config := map[string]any{}
	if len(stage.Config) > 0 {
		_ = json.Unmarshal(stage.Config, &config)
	}
	config["participants"] = seats
	cfgJSON, err := json.Marshal(config)
	if err != nil {
		return "{}"
	}
	state, err := p.EmptyState(cfgJSON)
	if err != nil || len(state) == 0 {
		return "{}"
	}
	return string(state)
}

func buildBrainStructureTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, scheme store.FestScheme) error {
	return buildSchemeStructureTx(ctx, tx, festID, gameID, "brain", scheme)
}

// buildSchemeStructureTx materialises a compiled scheme into stages, matches
// and slots for any game type — the plumbing used to be брейн's alone, and the
// only thing that was ever брейн-specific about it was the empty state.
func buildSchemeStructureTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, gameType string, scheme store.FestScheme) error {
	// A declared seed source owns game_assignments — «Import seed» writes them
	// by seed rank, so creation must not pre-fill them by number. Nor may it when
	// the Game already named its entrants: seating the фест's registry on top
	// would add the teams this Game does not play.
	seated, err := hasAssignmentsTx(ctx, tx, gameID)
	if err != nil {
		return err
	}
	if scheme.Seeding == nil && !seated {
		if err := seatRosterTx(ctx, tx, festID, gameID, gameType); err != nil {
			return err
		}
	}
	seat, err := seedSeaterTx(ctx, tx, festID, gameID, gameType)
	if err != nil {
		return err
	}
	for stageIndex, stage := range scheme.Stages {
		position := stage.Position
		if position == 0 {
			position = stageIndex + 1
		}
		grain := stage.Grain.Normalized()
		stageID, err := store.InsertReturningID(ctx, tx, `
insert into stages(fest_id, game_id, code, title, stage_type, kind, position, status, config_json, block_code, wave_index, group_code)
values(?, ?, ?, ?, ?, ?, ?, 'active', ?, ?, ?, ?)`,
			festID, gameID, stage.Code, stage.Title, stage.StageType, stage.Kind, position, storeutil.StageConfigJSON(stage),
			grain.Block, grain.Wave, grain.Group)
		if err != nil {
			return err
		}
		for matchIndex, match := range stage.Matches {
			emptyState := stageEmptyState(gameType, stage, len(match.Slots), scheme.Questions)
			matchID, err := store.InsertReturningID(ctx, tx, `
insert into matches(fest_id, game_id, stage_id, code, title, letter, position, round, wave, participant_count, status, revision, state_json)
values(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'active', 1, ?)`,
				festID, gameID, stageID, match.Code, match.Title, match.Letter, matchIndex+1, match.Round, match.Wave, len(match.Slots), emptyState)
			if err != nil {
				return err
			}
			if err := insertMatchSlots(ctx, tx, matchID, match.Slots, seat); err != nil {
				return err
			}
		}
	}
	return nil
}

// RecompileSchemeGameTx is the settings page's DSL edit, as a seam tests can
// drive — the same path a host takes when they change a scheme in place.
func RecompileSchemeGameTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, dsl string) error {
	return recompileBrainGameTx(ctx, tx, festID, gameID, dsl)
}

// recompileBrainGameTx re-expands an edited DSL onto a live game: stages and
// unstarted бои follow the new scheme (questions changes included), started
// бои must survive with identical slot sources — else the whole edit is
// refused, naming them.
func recompileBrainGameTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, dsl string) error {
	var oldSchemeJSON, gameType string
	if err := tx.QueryRowContext(ctx, `
select coalesce(scheme_json, '{}'), game_type from games where id = ? and fest_id = ? and scheme_dsl is not null`,
		gameID, festID).Scan(&oldSchemeJSON, &gameType); err != nil {
		return err
	}
	var meta struct {
		Slug  string `json:"slug"`
		Title string `json:"title"`
	}
	_ = json.Unmarshal([]byte(oldSchemeJSON), &meta)
	// A Game that named its entrants keeps them across a recompile. Falling back
	// to the фест's registry here would recompile a game of 48 against a roster
	// of 65 and refuse the scheme it was created from.
	entrants, err := gameEntrantsTx(ctx, tx, gameID)
	if err != nil {
		return err
	}
	scheme, err := schemeForEntrantsTx(ctx, tx, festID, gameType, meta.Slug, meta.Title, dsl, entrants)
	if err != nil {
		return err
	}

	type dbMatch struct {
		ID      int64
		StageID int64
		Status  string
		State   string
	}
	existingMatches := map[string]dbMatch{}
	rows, err := tx.QueryContext(ctx, `
select id, stage_id, code, status, coalesce(state_json, '{}') from matches where game_id = ?`, gameID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var m dbMatch
		var code string
		if err := rows.Scan(&m.ID, &m.StageID, &code, &m.Status, &m.State); err != nil {
			rows.Close()
			return err
		}
		existingMatches[code] = m
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	existingStages := map[string]int64{}
	stageRows, err := tx.QueryContext(ctx, `select id, code from stages where game_id = ?`, gameID)
	if err != nil {
		return err
	}
	for stageRows.Next() {
		var id int64
		var code string
		if err := stageRows.Scan(&id, &code); err != nil {
			stageRows.Close()
			return err
		}
		existingStages[code] = id
	}
	if err := stageRows.Err(); err != nil {
		stageRows.Close()
		return err
	}
	stageRows.Close()

	planned := map[string]store.SchemeMatch{}
	for _, stage := range scheme.Stages {
		for _, match := range stage.Matches {
			planned[match.Code] = match
		}
	}
	var blocked []string
	for code, m := range existingMatches {
		if m.Status != "finished" && !games.BrainStateStarted(m.State) {
			continue
		}
		match, survives := planned[code]
		if !survives || !sameSlotIdentities(ctx, tx, m.ID, match.Slots) {
			blocked = append(blocked, code)
		}
	}
	if len(blocked) > 0 {
		sort.Strings(blocked)
		return fmt.Errorf("нельзя менять начатые бои: %s — уберите их изменения или снимите отметку «Закончен» и очистите протокол", strings.Join(blocked, ", "))
	}

	seat, err := seedSeaterTx(ctx, tx, festID, gameID, gameType)
	if err != nil {
		return err
	}
	for stageIndex, stage := range scheme.Stages {
		position := stage.Position
		if position == 0 {
			position = stageIndex + 1
		}
		grain := stage.Grain.Normalized()
		stageID, exists := existingStages[stage.Code]
		if exists {
			// The grain is refreshed here, not only on insert: a recompile is how
			// a game whose stages predate the coordinates acquires them, and a
			// block that moved needs its new ones.
			if _, err := tx.ExecContext(ctx, `
update stages set title = ?, stage_type = ?, kind = ?, position = ?, config_json = ?,
  block_code = ?, wave_index = ?, group_code = ? where id = ?`,
				stage.Title, stage.StageType, stage.Kind, position, storeutil.StageConfigJSON(stage),
				grain.Block, grain.Wave, grain.Group, stageID); err != nil {
				return err
			}
			delete(existingStages, stage.Code)
		} else {
			if stageID, err = store.InsertReturningID(ctx, tx, `
insert into stages(fest_id, game_id, code, title, stage_type, kind, position, status, config_json, block_code, wave_index, group_code)
values(?, ?, ?, ?, ?, ?, ?, 'active', ?, ?, ?, ?)`,
				festID, gameID, stage.Code, stage.Title, stage.StageType, stage.Kind, position, storeutil.StageConfigJSON(stage),
				grain.Block, grain.Wave, grain.Group); err != nil {
				return err
			}
		}
		emptyState := string(games.BrainEmptyStateJSON(stageQuestions(stage, scheme.Questions)))
		for matchIndex, match := range stage.Matches {
			existing, ok := existingMatches[match.Code]
			if ok {
				delete(existingMatches, match.Code)
				started := existing.Status == "finished" || games.BrainStateStarted(existing.State)
				if started {
					if _, err := tx.ExecContext(ctx, `
update matches set stage_id = ?, title = ?, letter = ?, position = ?, round = ?, wave = ? where id = ?`,
						stageID, match.Title, match.Letter, matchIndex+1, match.Round, match.Wave, existing.ID); err != nil {
						return err
					}
					continue
				}
				if _, err := tx.ExecContext(ctx, `
update matches set stage_id = ?, title = ?, letter = ?, position = ?, round = ?, wave = ?, participant_count = ?, status = 'active', state_json = ? where id = ?`,
					stageID, match.Title, match.Letter, matchIndex+1, match.Round, match.Wave, len(match.Slots), emptyState, existing.ID); err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `delete from match_slots where match_id = ?`, existing.ID); err != nil {
					return err
				}
				if err := insertMatchSlots(ctx, tx, existing.ID, match.Slots, seat); err != nil {
					return err
				}
				continue
			}
			matchID, err := store.InsertReturningID(ctx, tx, `
insert into matches(fest_id, game_id, stage_id, code, title, letter, position, round, wave, participant_count, status, revision, state_json)
values(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'active', 1, ?)`,
				festID, gameID, stageID, match.Code, match.Title, match.Letter, matchIndex+1, match.Round, match.Wave, len(match.Slots), emptyState)
			if err != nil {
				return err
			}
			if err := insertMatchSlots(ctx, tx, matchID, match.Slots, seat); err != nil {
				return err
			}
		}
	}
	for _, m := range existingMatches {
		if _, err := tx.ExecContext(ctx, `delete from matches where id = ?`, m.ID); err != nil {
			return err
		}
	}
	for _, stageID := range existingStages {
		if _, err := tx.ExecContext(ctx, `delete from stages where id = ?`, stageID); err != nil {
			return err
		}
	}

	schemeJSON, err := json.Marshal(scheme)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
update games set scheme_json = ?, scheme_dsl = ?, revision = revision + 1, updated_at = ? where id = ?`,
		string(schemeJSON), dsl, util.UtcNow(), gameID); err != nil {
		return err
	}
	if _, err := resolver.ResolveGameSlotsTx(ctx, tx, gameID); err != nil {
		return err
	}
	_, err = festwrite.BumpFestRevisionTx(ctx, tx, festID, "game:recompile", util.MustJSON(map[string]any{
		"gameID": gameID,
		"stages": len(scheme.Stages),
	}))
	return err
}

// sameSlotIdentities compares a live match's slot sources with the planned
// ones, ignoring cosmetic labels.
func sameSlotIdentities(ctx context.Context, tx *sql.Tx, matchID int64, planned []store.SchemeSlot) bool {
	rows, err := tx.QueryContext(ctx, `
select source_type, source_ref_json from match_slots where match_id = ? order by slot_index`, matchID)
	if err != nil {
		return false
	}
	defer rows.Close()
	var current []string
	for rows.Next() {
		var sourceType, refJSON string
		if err := rows.Scan(&sourceType, &refJSON); err != nil {
			return false
		}
		current = append(current, slotIdentity(sourceType, refJSON))
	}
	if rows.Err() != nil || len(current) != len(planned) {
		return false
	}
	for i, slot := range planned {
		sourceType, refJSON := storeutil.SlotSource(slot)
		if slotIdentity(sourceType, refJSON) != current[i] {
			return false
		}
	}
	return true
}

func slotIdentity(sourceType, refJSON string) string {
	var ref map[string]any
	_ = json.Unmarshal([]byte(refJSON), &ref)
	switch sourceType {
	case "seed":
		return fmt.Sprintf("seed:%d:%d", store.IntFromMap(ref, "basket"), store.IntFromMap(ref, "number"))
	case "from_match":
		return fmt.Sprintf("from_match:%v:%d", ref["match"], store.IntFromMap(ref, "place"))
	case "reseed":
		return fmt.Sprintf("reseed:%v:%d", ref["stage"], store.IntFromMap(ref, "rank"))
	}
	return sourceType
}

// seedSeaterTx builds the seat lookup a recompile reuses: fest teams by
// number (roster-seeded games) plus the seed-import ladder's assignments
// (declared-seed games).
func seedSeaterTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, gameType string) (func(slot store.SchemeSlot) any, error) {
	// A Game numbers the Participants it seats, and the assignment rows carry
	// that numbering (ADR-0009). Reading them first is what lets one фест hold an
	// ЭК of 48 and a брейн of a different 48.
	byNumber := map[int]int64{}
	rows, err := tx.QueryContext(ctx, `
select number, participant_id from game_assignments
where game_id = ? and basket = 1 and participant_id is not null`, gameID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var number int
		var participantID int64
		if err := rows.Scan(&number, &participantID); err != nil {
			rows.Close()
			return nil, err
		}
		byNumber[number] = participantID
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if !games.IsIndividual(gameType) {
		// A team Game that never named its entrants seats the фест's registry by
		// its registration numbers, as every game did before Games could differ.
		teams, err := roster.LoadFestRosterImportTeamsTx(ctx, tx, festID)
		if err != nil {
			return nil, err
		}
		for _, team := range teams {
			if team.Number <= 0 {
				continue
			}
			if _, taken := byNumber[int(team.Number)]; taken {
				continue
			}
			teamID, _, err := imports.EnsureSeedTeamByNumber(ctx, tx, festID, team.Number, team.Name, team.City, nil)
			if err != nil {
				return nil, err
			}
			byNumber[int(team.Number)] = teamID
		}
	}
	assignments := map[[2]int]int64{}
	rows, err = tx.QueryContext(ctx, `select basket, number, participant_id from game_assignments where game_id = ?`, gameID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var basket, number int
		var teamID int64
		if err := rows.Scan(&basket, &number, &teamID); err != nil {
			return nil, err
		}
		assignments[[2]int{basket, number}] = teamID
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return func(slot store.SchemeSlot) any {
		if slot.Seed == nil {
			return nil
		}
		// Number-refs seat by team number only; Position-refs by the seed
		// ladder only — a number missing from the roster must NOT fall through
		// to the rank-keyed assignments (15 the team ≠ 15 the seed rank).
		if slot.Seed.Number > 0 {
			if id, ok := byNumber[slot.Seed.Number]; ok {
				return id
			}
			return nil
		}
		if slot.Seed.Position > 0 {
			basket := slot.Seed.Basket
			if basket <= 0 {
				basket = 1
			}
			if id, ok := assignments[[2]int{basket, slot.Seed.Position}]; ok {
				return id
			}
		}
		return nil
	}, nil
}

func insertMatchSlots(ctx context.Context, tx *sql.Tx, matchID int64, slots []store.SchemeSlot, seat func(store.SchemeSlot) any) error {
	for slotIndex, slot := range slots {
		sourceType, sourceRef := storeutil.SlotSource(slot)
		if _, err := tx.ExecContext(ctx, `
insert into match_slots(match_id, slot_index, source_type, source_ref_json, participant_id, locked)
values(?, ?, ?, ?, ?, 0)`, matchID, slotIndex, sourceType, sourceRef, seat(slot)); err != nil {
			return err
		}
	}
	return nil
}

// stageQuestions reads a stage's questions override from its kind config,
// falling back to the scheme-wide count.
func stageQuestions(stage store.SchemeStage, fallback int) int {
	var config struct {
		Questions int `json:"questions"`
	}
	if err := json.Unmarshal(stage.Config, &config); err == nil && config.Questions > 0 {
		return config.Questions
	}
	return fallback
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
		{games.KSIStickerNeutral, "Обычный", "ksis_neutral_color", "ksis_neutral_max", "#ffffff"},
		{games.KSIStickerX2, "×2", "ksis_x2_color", "ksis_x2_max", "#fdf66f"},
		{games.KSIStickerNoWrong, "Без минуса", "ksis_nowrong_color", "ksis_nowrong_max", "#aded87"},
		{games.KSIStickerEmptyWrong, "Пустой = минус", "ksis_emptywrong_color", "ksis_emptywrong_max", "#ff7a6b"},
	}
	cfg := games.KSIStickerConfig{}
	for _, s := range all {
		max, err := parseNonNegativeFormInt(form, s.maxField, "Максимум стикеров", 0, 100)
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

func createKSIGameTx(ctx context.Context, tx *sql.Tx, festID int64, themesCount int, stickers json.RawMessage) (int64, error) {
	identity, err := nextGameIdentityTx(ctx, tx, festID, "ksi", "КСИ")
	if err != nil {
		return 0, err
	}
	schemeJSON, stateJSON := games.KSIStickersEmptyGameJSON(identity.Code, identity.Title, themesCount, stickers)
	teams, err := roster.LoadFestRosterImportTeamsTx(ctx, tx, festID)
	if err != nil {
		return 0, err
	}
	if len(teams) > 0 {
		schemeJSON, err = roster.ApplyRosterToKSIScheme(string(schemeJSON), teams)
		if err != nil {
			return 0, err
		}
		stateJSON, err = roster.ApplyRosterToKSIState(string(stateJSON), teams, themesCount)
		if err != nil {
			return 0, err
		}
	}
	return insertJSONGameTx(ctx, tx, festID, identity, "ksi", schemeJSON, stateJSON)
}

func insertJSONGameTx(ctx context.Context, tx *sql.Tx, festID int64, identity gameIdentity, gameType string, schemeJSON, stateJSON []byte) (int64, error) {
	now := util.UtcNow()
	schemeID, err := store.InsertReturningID(ctx, tx, `
insert into schemes(slug, title, version, schema_json, created_at)
values(?, ?, 2, ?, ?)`, uniqueSchemeSlug(identity.Code), identity.Title, string(schemeJSON), now)
	if err != nil {
		return 0, err
	}
	gameID, err := store.InsertReturningID(ctx, tx, `
insert into games(fest_id, code, title, game_type, position, scheme_id, scheme_json, state_json, status, team_list_source, roster_source, revision, created_at, updated_at)
values(?, ?, ?, ?, ?, ?, ?, '{}', 'active', 'fest', 'fest', 1, ?, ?)`,
		festID, identity.Code, identity.Title, gameType, identity.Position, schemeID, string(schemeJSON), now, now)
	if err != nil {
		return 0, err
	}
	if err := insertFlatMatchTx(ctx, tx, festID, gameID, identity.Title, string(stateJSON), now); err != nil {
		return 0, err
	}
	return gameID, nil
}

// insertFlatMatchTx creates a flat game's unified structure: one 'main' stage
// (kind matches) holding one 'main' match that carries the whole game state.
func insertFlatMatchTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, title, stateJSON, now string) error {
	stageID, err := store.InsertReturningID(ctx, tx, `
insert into stages(fest_id, game_id, code, title, stage_type, kind, position, status, config_json, block_code, wave_index)
values(?, ?, 'main', '', 'matches', 'matches', 1, 'active', '{}', 'main', 1)`, festID, gameID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
insert into matches(fest_id, game_id, stage_id, code, title, position, round, wave, participant_count, status, revision, state_json)
values(?, ?, ?, 'main', ?, 1, 1, 1, 0, 'active', 0, ?)`, festID, gameID, stageID, title, stateJSON)
	return err
}

func CreateEKGameTx(ctx context.Context, tx *sql.Tx, festID int64, scheme store.FestScheme) (int64, error) {
	if scheme.GameType == "" {
		scheme.GameType = games.Default
	}
	if scheme.GameType != games.Default {
		return 0, errors.New("для ЭК нужна JSON-схема с gameType \"ek\"")
	}
	if err := storeutil.ValidateScheme(scheme); err != nil {
		return 0, err
	}
	if len(scheme.Teams) > 0 {
		return 0, errors.New("команды загружаются отдельным импортом посева; уберите teams из JSON-схемы")
	}
	schemaJSON, err := json.Marshal(scheme)
	if err != nil {
		return 0, err
	}
	title := strings.TrimSpace(scheme.Title)
	if title == "" {
		title = "ЭК"
	}
	identity, err := nextGameIdentityTx(ctx, tx, festID, "ek", title)
	if err != nil {
		return 0, err
	}
	identity.Title = title

	now := util.UtcNow()
	schemeID, err := store.InsertReturningID(ctx, tx, `
insert into schemes(slug, title, version, schema_json, created_at)
values(?, ?, ?, ?, ?)`, uniqueSchemeSlug(scheme.Slug), title, util.MaxInt(scheme.SchemaVersion, 2), string(schemaJSON), now)
	if err != nil {
		return 0, err
	}
	gameID, err := store.InsertReturningID(ctx, tx, `
insert into games(fest_id, code, title, game_type, position, scheme_id, scheme_json, state_json, status, team_list_source, roster_source, revision, created_at, updated_at)
values(?, ?, ?, ?, ?, ?, ?, '{}', 'pending', 'fest', 'fest', 1, ?, ?)`,
		festID, identity.Code, title, games.Default, identity.Position, schemeID, string(schemaJSON), now, now)
	if err != nil {
		return 0, err
	}

	if err := buildEKStructureTx(ctx, tx, festID, gameID, scheme, now); err != nil {
		return 0, err
	}
	return gameID, nil
}

// buildEKStructureTx materialises an EK game's bracket (venues, stages, matches
// and their unresolved seed slots) from the scheme. Shared by game creation and
// the "clear to pristine" path, which rebuilds the same empty bracket in place.
func buildEKStructureTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, scheme store.FestScheme, now string) error {
	venueIDs := make(map[int]int64, len(scheme.Venues))
	for _, venue := range scheme.Venues {
		venueID, err := upsertVenueTx(ctx, tx, festID, venue, now)
		if err != nil {
			return err
		}
		venueIDs[venue.Number] = venueID
	}

	for stageIndex, stage := range scheme.Stages {
		position := stage.Position
		if position == 0 {
			position = stageIndex + 1
		}
		configJSON := storeutil.StageConfigJSON(stage)
		stageType := stage.StageType
		if stageType == "" {
			stageType = "matches"
		}
		grain := stage.Grain.Normalized()
		stageID, err := store.InsertReturningID(ctx, tx, `
insert into stages(fest_id, game_id, code, title, stage_type, position, status, config_json, block_code, wave_index, group_code)
values(?, ?, ?, ?, ?, ?, 'pending', ?, ?, ?, ?)`, festID, gameID, stage.Code, stage.Title, stageType, position, configJSON,
			grain.Block, grain.Wave, grain.Group)
		if err != nil {
			return err
		}
		if stageType != "matches" {
			continue
		}
		for matchIndex, match := range stage.Matches {
			participantCount := match.ParticipantCount
			if participantCount == 0 {
				participantCount = len(match.Slots)
			}
			var venueID any
			if id, ok := venueIDs[match.Venue]; ok {
				venueID = id
			}
			matchID, err := store.InsertReturningID(ctx, tx, `
insert into matches(fest_id, game_id, stage_id, code, title, position, round, wave, participant_count, venue_id, status, revision)
values(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', 1)`, festID, gameID, stageID, match.Code, match.Title, matchIndex+1, match.Round, match.Wave, participantCount, venueID)
			if err != nil {
				return err
			}
			for slotIndex, slot := range match.Slots {
				sourceType, sourceRef := storeutil.SlotSource(slot)
				if _, err := tx.ExecContext(ctx, `
insert into match_slots(match_id, slot_index, source_type, source_ref_json, participant_id, locked)
values(?, ?, ?, ?, null, 0)`, matchID, slotIndex, sourceType, sourceRef); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func upsertVenueTx(ctx context.Context, tx *sql.Tx, festID int64, venue store.SchemeVenue, now string) (int64, error) {
	if _, err := tx.ExecContext(ctx, `
insert into venues(fest_id, number, title, created_at, updated_at)
values(?, ?, ?, ?, ?)
on conflict(fest_id, number) do update set title = excluded.title, updated_at = excluded.updated_at`,
		festID, venue.Number, venue.Title, now, now); err != nil {
		return 0, err
	}
	var id int64
	err := tx.QueryRowContext(ctx, `select id from venues where fest_id = ? and number = ?`, festID, venue.Number).Scan(&id)
	return id, err
}

func uniqueSchemeSlug(base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "game"
	}
	return fmt.Sprintf("%s-%d", base, time.Now().UnixNano())
}

// seedPlayerEntrantsTx lists the fest's players as entrants, in the order they
// were registered — that order IS the seeding, the way a fest's registration
// list is. They are numbered here rather than in the roster because a fest
// numbers its teams; an individual game numbers the players it seats.
func seedPlayerEntrantsTx(ctx context.Context, tx *sql.Tx, festID int64) ([]store.SchemeSlot, error) {
	rows, err := tx.QueryContext(ctx, `
select p.id, trim(p.first_name || ' ' || p.last_name)
from fest_players p where p.fest_id = ?
order by p.id`, festID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entrants []store.SchemeSlot
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		entrants = append(entrants, store.SchemeSlot{
			Seed:  &store.SchemeSeedRef{Basket: 1, Number: len(entrants) + 1},
			Label: name,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(entrants) < 3 {
		return nil, errors.New("для личной игры нужны игроки в ростере феста")
	}
	return entrants, nil
}

// seatRosterTx pre-fills a game's seed assignments from the fest roster —
// teams in a team format, players in an individual one, each becoming a
// Participant of the matching kind.
func seatRosterTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, gameType string) error {
	assign := func(number, participantID int64) error {
		_, err := tx.ExecContext(ctx, `
insert into game_assignments(game_id, basket, number, participant_id) values(?, 1, ?, ?)
on conflict(game_id, basket, number) do update set participant_id = excluded.participant_id`,
			gameID, number, participantID)
		return err
	}
	if games.IsIndividual(gameType) {
		rows, err := tx.QueryContext(ctx, `
select p.id, trim(p.first_name || ' ' || p.last_name)
from fest_players p where p.fest_id = ?
order by p.id`, festID)
		if err != nil {
			return err
		}
		type entry struct {
			id   int64
			name string
		}
		var players []entry
		for rows.Next() {
			var e entry
			if err := rows.Scan(&e.id, &e.name); err != nil {
				rows.Close()
				return err
			}
			players = append(players, e)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for i, player := range players {
			number := int64(i + 1)
			participantID, err := imports.EnsureSeedPlayerByNumber(ctx, tx, festID, number, player.name, player.id)
			if err != nil {
				return err
			}
			if err := assign(number, participantID); err != nil {
				return err
			}
		}
		return nil
	}
	teams, err := roster.LoadFestRosterImportTeamsTx(ctx, tx, festID)
	if err != nil {
		return err
	}
	for _, team := range teams {
		teamID, _, err := imports.EnsureSeedTeamByNumber(ctx, tx, festID, team.Number, team.Name, team.City, nil)
		if err != nil {
			return err
		}
		if err := assign(team.Number, teamID); err != nil {
			return err
		}
	}
	return nil
}
