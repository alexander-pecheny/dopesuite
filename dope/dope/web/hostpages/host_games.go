package hostpages

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"dope/dope/domain/core"
	"dope/dope/domain/gamebuild"
	"dope/dope/domain/games"
	"dope/dope/domain/roster"
	"dope/dope/domain/view"
	"dope/dope/platform/util"
	"dope/dope/storage/festwrite"
	"dope/dope/storage/journal"
	"dope/dope/storage/store"
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
		return gamebuild.Recompile(ctx, tx, festID, gameID, dsl)
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
		if newScheme, err = gamebuild.Rebuild(r.Context(), tx, festID, gameID); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		newState = []byte("{}")
	case "ek":
		status = "pending"
		if newScheme, err = gamebuild.Rebuild(r.Context(), tx, festID, gameID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		newState = []byte("{}")
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

// gameSpecFromForm reads the creation form into what gamebuild needs: the
// format's label and DSL, the entrants ticked, and for the three pre-DSL
// formats their own knobs.
func gameSpecFromForm(festID int64, gameType string, form url.Values) (gamebuild.Spec, error) {
	spec := gamebuild.Spec{FestID: festID, Type: gameType, Entrants: chosenEntrantIDs(form), DSL: strings.TrimSpace(form.Get("brain_dsl"))}
	var err error
	switch gameType {
	case games.OD:
		if spec.ODTours, err = parsePositiveFormInt(form, "od_tours", "Количество туров", 1, 20); err != nil {
			return spec, err
		}
		if spec.ODQuestions, err = parsePositiveFormInt(form, "od_questions", "Количество вопросов в туре", 1, 100); err != nil {
			return spec, err
		}
	case games.KSI:
		if spec.KSIThemes, err = parsePositiveFormInt(form, "ksi_themes", "Количество тем", 1, 100); err != nil {
			return spec, err
		}
	case ksiStickersGameType:
		spec.Type = games.KSI
		if spec.KSIThemes, err = parsePositiveFormInt(form, "ksis_themes", "Количество тем", 1, 100); err != nil {
			return spec, err
		}
		if spec.KSIStickers, err = ksiStickerConfigFromForm(form); err != nil {
			return spec, err
		}
	case games.Brain:
		spec.Label = "Брейн"
	case games.SI:
		spec.Label = "Личная СИ"
	case games.EK:
		// ЭК's bracket is describable in the scheme language now that an
		// elimination counts Losses rather than seats, so a DSL wins over
		// the pasted JSON when both are offered.
		spec.Label = "ЭК"
		if spec.DSL == "" {
			raw := strings.TrimSpace(form.Get("ek_scheme"))
			if raw == "" {
				return spec, errors.New("Вставьте JSON-схему ЭК или опишите её схемой")
			}
			var scheme store.FestScheme
			if err := json.Unmarshal([]byte(raw), &scheme); err != nil {
				return spec, fmt.Errorf("Не удалось разобрать JSON: %w", err)
			}
			spec.EKScheme = &scheme
		}
	}
	return spec, nil
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

		spec, err := gameSpecFromForm(festID, gameType, form)
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
