package hostpages

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
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
// sticker. Each entry pairs the CSS variable that draws the swatch (defined in
// styles.css) with the hex submitted as the form value — keep both in sync.
var stickerPaletteColors = []struct{ Var, Hex string }{
	{"--sticker-c-white", "#ffffff"},
	{"--sticker-c-yellow", "#fdf66f"},
	{"--sticker-c-green", "#aded87"},
	{"--sticker-c-red", "#ff7a6b"},
	{"--sticker-c-blue", "#68caff"},
	{"--sticker-c-pink", "#f4a8ff"},
	{"--sticker-c-orange", "#ffae37"},
}

// stickerPaletteHTML renders the swatch radio group for one sticker colour field.
func stickerPaletteHTML(name, selected string) template.HTML {
	var b strings.Builder
	b.WriteString(`<div class="sticker-palette" role="radiogroup" aria-label="Цвет"><span>Цвет</span>`)
	for _, s := range stickerPaletteColors {
		checked := ""
		if strings.EqualFold(s.Hex, selected) {
			checked = " checked"
		}
		fmt.Fprintf(&b, `<label class="swatch" title="%s"><input type="radio" name="%s" value="%s"%s><span class="swatch-dot" style="--swatch:var(%s)"></span></label>`,
			s.Hex, name, s.Hex, checked, s.Var)
	}
	b.WriteString(`</div>`)
	return template.HTML(b.String())
}

var hostGameCreateTemplate = template.Must(template.New("hostGameCreate").Funcs(template.FuncMap{
	"stickerPalette": stickerPaletteHTML,
}).Parse(`<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Fest.Title}} · новая игра</title>
  <link rel="preload" href="/static/fonts/noto-sans-400.woff2" as="font" type="font/woff2" crossorigin>
  <link rel="stylesheet" href="/static/styles.css">
  <script src="/static/menu.js"></script>
</head>
<body class="public">
  <header class="public-top">
    <a class="public-back" href="/host/fest/{{.Fest.Ref}}">←</a>
    <h1>Добавить игру</h1>
  </header>
  <main class="public-main">
    {{if .Error}}<p class="empty">{{.Error}}</p>{{end}}
    <form method="post" action="/host/fest/{{.Fest.Ref}}/game/new" class="card stack" autocomplete="off" data-game-create-form>
      <fieldset class="field game-type-fieldset">
        <span>Тип игры</span>
        <label class="checkbox">
          <input type="radio" name="game_type" value="od" {{if eq .SelectedType "od"}}checked{{end}}>
          <span>ОД</span>
        </label>
        <label class="checkbox">
          <input type="radio" name="game_type" value="ksi" {{if eq .SelectedType "ksi"}}checked{{end}}>
          <span>КСИ</span>
        </label>
        <label class="checkbox">
          <input type="radio" name="game_type" value="ksi_stickers" {{if eq .SelectedType "ksi_stickers"}}checked{{end}}>
          <span>КСИ со стикерами</span>
        </label>
        <label class="checkbox">
          <input type="radio" name="game_type" value="ek" {{if eq .SelectedType "ek"}}checked{{end}}>
          <span>ЭК</span>
        </label>
      </fieldset>

      <section class="stack game-create-settings" data-game-settings="od" {{if ne .SelectedType "od"}}hidden{{end}}>
        <label class="field">
          <span>Количество туров</span>
          <input name="od_tours" inputmode="numeric" value="3">
        </label>
        <label class="field">
          <span>Количество вопросов в туре</span>
          <input name="od_questions" inputmode="numeric" value="15">
        </label>
      </section>

      <section class="stack game-create-settings" data-game-settings="ksi" {{if ne .SelectedType "ksi"}}hidden{{end}}>
        <label class="field">
          <span>Количество тем</span>
          <input name="ksi_themes" inputmode="numeric" value="20">
        </label>
      </section>

      <section class="stack game-create-settings" data-game-settings="ksi_stickers" {{if ne .SelectedType "ksi_stickers"}}hidden{{end}}>
        <label class="field">
          <span>Количество тем</span>
          <input name="ksis_themes" inputmode="numeric" value="20">
        </label>
        <p class="hint">Для каждого стикера задайте, сколько штук есть у команды (0 — стикер не используется) и цвет для подсветки. «Обычный» стикер работает как стандартная тема КСИ.</p>
        <div class="field">
          <span>Обычный</span>
          <label class="sticker-max">Макс. <input name="ksis_neutral_max" inputmode="numeric" value="20"></label>
          {{stickerPalette "ksis_neutral_color" "#ffffff"}}
        </div>
        <div class="field">
          <span>×2 (правильные и неправильные удваиваются)</span>
          <label class="sticker-max">Макс. <input name="ksis_x2_max" inputmode="numeric" value="2"></label>
          {{stickerPalette "ksis_x2_color" "#fdf66f"}}
        </div>
        <div class="field">
          <span>Без минуса (неправильные = 0)</span>
          <label class="sticker-max">Макс. <input name="ksis_nowrong_max" inputmode="numeric" value="1"></label>
          {{stickerPalette "ksis_nowrong_color" "#aded87"}}
        </div>
        <div class="field">
          <span>Пустой = минус (пустые = −номинал)</span>
          <label class="sticker-max">Макс. <input name="ksis_emptywrong_max" inputmode="numeric" value="1"></label>
          {{stickerPalette "ksis_emptywrong_color" "#ff7a6b"}}
        </div>
      </section>

      <section class="stack game-create-settings" data-game-settings="ek" {{if ne .SelectedType "ek"}}hidden{{end}}>
        <label class="field">
          <span>JSON-схема</span>
          <textarea name="ek_scheme" rows="14" placeholder='{"slug":"...","title":"...","gameType":"ek","stages":[...]}'></textarea>
        </label>
      </section>

      <div class="cluster" data-game-submit {{if eq .SelectedType ""}}hidden{{end}}>
        <button class="btn" type="submit">Создать</button>
      </div>
    </form>
  </main>
  <script>
    (() => {
      const form = document.querySelector("[data-game-create-form]");
      if (!form) return;
      const sync = () => {
        const selected = form.querySelector('input[name="game_type"]:checked')?.value || "";
        form.querySelectorAll("[data-game-settings]").forEach((section) => {
          section.hidden = section.dataset.gameSettings !== selected;
        });
        const submit = form.querySelector("[data-game-submit]");
        if (submit) submit.hidden = selected === "";
      };
      form.querySelectorAll('input[name="game_type"]').forEach((input) => input.addEventListener("change", sync));
      sync();
    })();
  </script>
</body>
</html>`))

var hostGameSettingsTemplate = template.Must(template.New("hostGameSettings").Parse(`<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Game.Title}} · {{.Fest.Title}}</title>
  <link rel="preload" href="/static/fonts/noto-sans-400.woff2" as="font" type="font/woff2" crossorigin>
  <link rel="stylesheet" href="/static/styles.css">
  <script src="/static/menu.js"></script>
</head>
<body class="public">
  <header class="public-top">
    <a class="public-back" href="/host/fest/{{.Fest.Ref}}">←</a>
    <h1>{{.Game.Title}}</h1>
  </header>
  <main class="public-main">
    {{if .Error}}<p class="empty">{{.Error}}</p>{{end}}
    <form method="post" action="/host/fest/{{.Fest.Ref}}/game/{{.Game.Ref}}/settings" class="card stack" autocomplete="off">
      <label class="field">
        <span>Тип игры</span>
        <input value="{{.Game.Type}}" disabled>
      </label>
      <label class="field">
        <span>Название</span>
        <input name="title" value="{{.Game.Title}}" required>
      </label>
      <label class="field">
        <span>Slug (необязательно, a-z, 0-9, дефис)</span>
        <input name="slug" value="{{.Slug}}" pattern="[a-z0-9-]+">
      </label>
      <div class="cluster">
        <button class="btn" type="submit">Сохранить</button>
      </div>
    </form>
  </main>
</body>
</html>`))

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
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = hostGameSettingsTemplate.Execute(w, hostGameSettingsData{
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
	})
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
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = hostGameCreateTemplate.Execute(w, hostGameCreateData{Fest: fest, Error: errMsg, SelectedType: selectedType})
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
	if gameType != games.OD && gameType != games.KSI && gameType != games.EK && gameType != ksiStickersGameType {
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
