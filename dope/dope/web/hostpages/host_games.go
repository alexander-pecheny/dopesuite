package hostpages

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"dope/dope/domain/core"
	"dope/dope/domain/games"
	"dope/dope/domain/roster"
	"dope/dope/domain/view"
	"dope/dope/platform/util"
	"dope/dope/storage/festwrite"
	"dope/dope/storage/journal"
	"dope/dope/storage/store"
	"dope/dope/storage/storeutil"
	"dope/dope/web/pages"
	dopeui "dope/dope/web/ui"
)

type hostGameSettingsData struct {
	Fest  view.HostFest
	Game  PublicFestGame
	Slug  string
	Error string
}

type hostGameCreateData struct {
	Fest         view.HostFest
	Error        string
	SelectedType string
}

type gameIdentity struct {
	Code     string
	Title    string
	Position int
}

// stickerPaletteColors is the fixed set of colours an organizer may assign to a
// sticker. Name is the closed swatch-color enum token the swatchradio primitive
// turns into --sticker-c-<name>; Hex is the value submitted with the form.
var stickerPaletteColors = []struct{ Name, Hex string }{
	{"white", "#ffffff"},
	{"yellow", "#fdf66f"},
	{"green", "#aded87"},
	{"red", "#ff7a6b"},
	{"blue", "#68caff"},
	{"pink", "#f4a8ff"},
	{"orange", "#ffae37"},
}

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
		dopeui.Title(data.Fest.Title + " · новая игра"), dopeui.PagePublic, dopeui.Classicscripts("gamecreate.js"),
		dopeui.Publictopbar(dopeui.Title("Добавить игру"), dopeui.Back("/host/fest/"+ref)),
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
			gameTypeRadio("ek", "ЭК", sel),
			gameTypeRadio("brain", "Брейн", sel),
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
		gameSettings("ek", sel,
			dopeui.Field(dopeui.Label("JSON-схема"),
				dopeui.Editor(dopeui.Name("ek_scheme"), dopeui.Rows("14"), dopeui.Placeholder(`{"slug":"...","title":"...","gameType":"ek","stages":[...]}`))),
		),
		gameSettings("brain", sel,
			dopeui.Field(dopeui.Label("Количество групп"), dopeui.Textfield(dopeui.Name("brain_groups"), dopeui.Inputmode("numeric"), dopeui.Value("6"))),
			dopeui.Field(dopeui.Label("Команд в группе"), dopeui.Textfield(dopeui.Name("brain_team_count"), dopeui.Inputmode("numeric"), dopeui.Value("4"))),
			dopeui.Field(dopeui.Label("Вопросов в бою"), dopeui.Textfield(dopeui.Name("brain_questions"), dopeui.Inputmode("numeric"), dopeui.Value("5"))),
			dopeui.Hint(dopeui.Text("Команды распределяются по группам жеребьёвкой (посевом): каждая группа получает по одной команде из каждой корзины.")),
		),
		dopeui.Row(submit...),
	))
	return &dopeui.Doc{Nodes: []dopeui.Node{dopeui.Page(page...)}}
}

// hostGameSettingsDoc builds a game's settings page: a small form to rename the
// game and set its slug (its type is shown read-only).
func hostGameSettingsDoc(data hostGameSettingsData) *dopeui.Doc {
	festRef := data.Fest.Ref()
	page := []dopeui.Item{
		dopeui.Title(data.Game.Title + " · " + data.Fest.Title), dopeui.PagePublic,
		dopeui.Publictopbar(dopeui.Title(data.Game.Title), dopeui.Back("/host/fest/"+festRef)),
	}
	if data.Error != "" {
		page = append(page, dopeui.Empty(dopeui.Text(data.Error)))
	}
	page = append(page, dopeui.Form(dopeui.DirCol, dopeui.Method("post"),
		dopeui.Action("/host/fest/"+festRef+"/game/"+data.Game.Ref()+"/settings"), dopeui.Autocomplete("off"),
		dopeui.Field(dopeui.Label("Тип игры"), dopeui.Textfield(dopeui.Value(data.Game.Type), dopeui.Disabled())),
		dopeui.Field(dopeui.Label("Название"), dopeui.Textfield(dopeui.Name("title"), dopeui.Value(data.Game.Title), dopeui.Required())),
		dopeui.Field(dopeui.Label("Slug (необязательно, a-z, 0-9, дефис)"), dopeui.Textfield(dopeui.Name("slug"), dopeui.Value(data.Slug), dopeui.Pattern("[a-z0-9-]+"))),
		dopeui.Row(dopeui.Button(dopeui.Submit(), dopeui.Text("Сохранить"))),
	))
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
		code     string
		title    string
		gameType string
		slug     sql.NullString
	)
	if err := s.h.Engine().DB.QueryRowContext(r.Context(), `
select code, title, game_type, slug from games where id = ? and fest_id = ?`, gameID, festID).Scan(&code, &title, &gameType, &slug); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
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
		Slug:  slug.String,
		Error: errMsg,
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
	if _, err := s.h.Engine().WriteExec(r.Context(), `
update games set title = ?, slug = ?, updated_at = ? where id = ? and fest_id = ?`,
		title, slugValue, util.UtcNow(), gameID, festID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
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

	var gameType, title, schemeJSON string
	if err := tx.QueryRowContext(r.Context(), `
select game_type, title, coalesce(scheme_json, '{}') from games where id = ? and fest_id = ?`,
		gameID, festID).Scan(&gameType, &title, &schemeJSON); err != nil {
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
		`delete from game_teams where game_id = ?`,
		`delete from game_players where game_id = ?`,
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
update games set scheme_json = ?, state_json = ?, status = ?,
  team_list_source = 'fest', roster_source = 'fest', revision = revision + 1, updated_at = ?
where id = ? and fest_id = ?`, string(newScheme), string(newState), status, now, gameID, festID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
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
	pages.RenderDoc(w, s.h.Engine().AssetETags, hostGameCreateDoc(hostGameCreateData{Fest: fest, Error: errMsg, SelectedType: selectedType}))
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
	if gameType != games.OD && gameType != games.KSI && gameType != games.EK && gameType != ksiStickersGameType && gameType != games.BRAIN {
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
		case games.EK:
			raw := strings.TrimSpace(form.Get("ek_scheme"))
			if raw == "" {
				return errors.New("Вставьте JSON-схему ЭК")
			}
			var scheme store.FestScheme
			if err := json.Unmarshal([]byte(raw), &scheme); err != nil {
				return fmt.Errorf("Не удалось разобрать JSON: %w", err)
			}
			gameID, err = CreateEKGameTx(ctx, tx, festID, scheme)
			if err != nil {
				return err
			}
		case games.BRAIN:
			nGroups, err := parsePositiveFormInt(form, "brain_groups", "Количество групп", 1, 52)
			if err != nil {
				return err
			}
			teamCount, err := parsePositiveFormInt(form, "brain_team_count", "Команд в группе", 2, 4)
			if err != nil {
				return err
			}
			questions, err := parsePositiveFormInt(form, "brain_questions", "Вопросов в бою", 1, 12)
			if err != nil {
				return err
			}
			gameID, err = CreateBrainGameTx(ctx, tx, festID, nGroups, teamCount, questions)
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
	var typeCount int
	if err := tx.QueryRowContext(ctx, `select count(*) from games where fest_id = ? and game_type = ?`, festID, gameType).Scan(&typeCount); err != nil {
		return gameIdentity{}, err
	}
	title := titleBase
	if typeCount > 0 && gameType != "ek" {
		title = fmt.Sprintf("%s %d", titleBase, typeCount+1)
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

// CreateBrainGameTx generates a брейн group-stage scheme (venues + one round-robin
// group per group index) and materialises it through the shared EK bracket builder.
// Teams land later via the seed draw; state_json stays '{}' until then.
func CreateBrainGameTx(ctx context.Context, tx *sql.Tx, festID int64, nGroups, teamCount, questions int) (int64, error) {
	identity, err := nextGameIdentityTx(ctx, tx, festID, games.BRAIN, "Брейн")
	if err != nil {
		return 0, err
	}
	scheme := games.BrainGenerateScheme(identity.Code, identity.Title, nGroups, teamCount, questions)
	if err := storeutil.ValidateScheme(scheme); err != nil {
		return 0, err
	}
	schemeJSON, err := json.Marshal(scheme)
	if err != nil {
		return 0, err
	}
	now := util.UtcNow()
	schemeID, err := store.InsertReturningID(ctx, tx, `
insert into schemes(slug, title, version, schema_json, created_at)
values(?, ?, ?, ?, ?)`, uniqueSchemeSlug(scheme.Slug), identity.Title, util.MaxInt(scheme.SchemaVersion, 2), string(schemeJSON), now)
	if err != nil {
		return 0, err
	}
	gameID, err := store.InsertReturningID(ctx, tx, `
insert into games(fest_id, code, title, game_type, position, scheme_id, scheme_json, state_json, status, team_list_source, roster_source, revision, created_at, updated_at)
values(?, ?, ?, ?, ?, ?, ?, '{}', 'pending', 'fest', 'fest', 1, ?, ?)`,
		festID, identity.Code, identity.Title, games.BRAIN, identity.Position, schemeID, string(schemeJSON), now, now)
	if err != nil {
		return 0, err
	}
	if err := buildEKStructureTx(ctx, tx, festID, gameID, scheme, now); err != nil {
		return 0, err
	}
	return gameID, nil
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
	return store.InsertReturningID(ctx, tx, `
insert into games(fest_id, code, title, game_type, position, scheme_id, scheme_json, state_json, status, team_list_source, roster_source, revision, created_at, updated_at)
values(?, ?, ?, ?, ?, ?, ?, ?, 'active', 'fest', 'fest', 1, ?, ?)`,
		festID, identity.Code, identity.Title, gameType, identity.Position, schemeID, string(schemeJSON), string(stateJSON), now, now)
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
		stageID, err := store.InsertReturningID(ctx, tx, `
insert into stages(fest_id, game_id, code, title, stage_type, position, status, config_json)
values(?, ?, ?, ?, ?, ?, 'pending', ?)`, festID, gameID, stage.Code, stage.Title, stageType, position, configJSON)
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
insert into matches(fest_id, game_id, stage_id, code, title, position, participant_count, venue_id, status, revision)
values(?, ?, ?, ?, ?, ?, ?, ?, 'pending', 1)`, festID, gameID, stageID, match.Code, match.Title, matchIndex+1, participantCount, venueID)
			if err != nil {
				return err
			}
			for slotIndex, slot := range match.Slots {
				sourceType, sourceRef := storeutil.SlotSource(slot)
				if _, err := tx.ExecContext(ctx, `
insert into match_slots(match_id, slot_index, source_type, source_ref_json, team_id, locked)
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
