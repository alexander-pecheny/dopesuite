package main

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
)

type hostGameSettingsData struct {
	Fest  hostMyFest
	Game  publicFestGame
	Slug  string
	Error string
}

type hostGameCreateData struct {
	Fest         hostMyFest
	Error        string
	SelectedType string
}

type gameIdentity struct {
	Code     string
	Title    string
	Position int
}

var hostGameCreateTemplate = template.Must(template.New("hostGameCreate").Parse(`<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Fest.Title}} · новая игра</title>
  <link rel="preload" href="/static/fonts/noto-sans-400.woff2" as="font" type="font/woff2" crossorigin>
  <link rel="stylesheet" href="/static/styles.css">
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
</head>
<body class="public">
  <header class="public-top">
    <a class="public-back" href="/host/fest/{{.Fest.Ref}}">←</a>
    <nav class="public-breadcrumbs" aria-label="Навигация">
      <a href="/host/fest/{{.Fest.Ref}}">{{.Fest.Title}}</a>
      <span>/</span>
      <span>{{.Game.Title}}</span>
    </nav>
    <h1>{{.Game.Title}}</h1>
  </header>
  <main class="public-main">
    {{if .Error}}<p class="empty">{{.Error}}</p>{{end}}
    <form method="post" action="/host/fest/{{.Fest.Ref}}/game/{{.Game.Ref}}/settings" class="card stack" autocomplete="off">
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

func (s *server) renderHostGameSettings(w http.ResponseWriter, r *http.Request, festID, gameID int64, errMsg string) {
	fest, err := s.loadHostFestHeader(r.Context(), festID)
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
	if err := s.db.QueryRowContext(r.Context(), `
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
		Game: publicFestGame{
			ID:    gameID,
			Slug:  slug.String,
			Code:  code,
			Title: title,
			Type:  gameTypeLabel(gameType),
		},
		Slug:  slug.String,
		Error: errMsg,
	})
}

func (s *server) handleHostUpdateGameSettings(w http.ResponseWriter, r *http.Request, festID, gameID int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	slug := strings.TrimSpace(r.Form.Get("slug"))
	var slugValue any
	if slug != "" {
		if err := validateSlug(slug); err != nil {
			s.renderHostGameSettings(w, r, festID, gameID, "Slug: "+err.Error())
			return
		}
		var count int
		if err := s.db.QueryRowContext(r.Context(), `
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
	if _, err := s.db.ExecContext(r.Context(), `
update games set slug = ?, updated_at = ? where id = ? and fest_id = ?`,
		slugValue, utcNow(), gameID, festID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.invalidateFestViewCache(festID)
	gameRef := slug
	if gameRef == "" {
		gameRef = fmt.Sprintf("%d", gameID)
	}
	http.Redirect(w, r, fmt.Sprintf("/host/fest/%s/game/%s/settings", s.festRefOrID(r.Context(), festID), gameRef), http.StatusSeeOther)
}

func (s *server) handleHostDeleteGame(w http.ResponseWriter, r *http.Request, festID, gameID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	var title string
	if err := tx.QueryRowContext(r.Context(), `
select title from games where id = ? and fest_id = ?`, gameID, festID).Scan(&title); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := tx.ExecContext(r.Context(), `delete from games where id = ? and fest_id = ?`, gameID, festID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var nextGameID sql.NullInt64
	var nextMatchCode sql.NullString
	if err := tx.QueryRowContext(r.Context(), `
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
	if _, err := bumpFestRevisionTx(r.Context(), tx, festID, "game:delete", mustJSON(map[string]any{
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
	if s.festID == festID && s.activeGameID == gameID {
		if nextGameID.Valid {
			s.activeGameID = nextGameID.Int64
			s.activeMatchCode = nextMatchCode.String
		} else {
			s.activeGameID = 0
			s.activeMatchCode = ""
		}
	}
	http.Redirect(w, r, fmt.Sprintf("/host/fest/%s", s.festRefOrID(r.Context(), festID)), http.StatusSeeOther)
}

func (s *server) renderHostCreateGamePage(w http.ResponseWriter, r *http.Request, festID int64, errMsg string, selectedType string) {
	fest, err := s.loadHostFestHeader(r.Context(), festID)
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

func (s *server) handleHostCreateGame(w http.ResponseWriter, r *http.Request, festID int64) {
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

func (s *server) createHostGame(ctx context.Context, festID int64, gameType string, form url.Values) (int64, error) {
	if s.db == nil {
		return 0, errors.New("sqlite is not enabled")
	}
	gameType = strings.TrimSpace(gameType)
	if gameType != "od" && gameType != "ksi" && gameType != "ek" {
		return 0, errors.New("выберите тип игры")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var exists int
	if err := tx.QueryRowContext(ctx, `select count(*) from fests where id = ?`, festID).Scan(&exists); err != nil {
		return 0, err
	}
	if exists == 0 {
		return 0, sql.ErrNoRows
	}

	var gameID int64
	switch gameType {
	case "od":
		tours, err := parsePositiveFormInt(form, "od_tours", "Количество туров", 1, 20)
		if err != nil {
			return 0, err
		}
		questions, err := parsePositiveFormInt(form, "od_questions", "Количество вопросов в туре", 1, 100)
		if err != nil {
			return 0, err
		}
		gameID, err = createODGameTx(ctx, tx, festID, tours, questions)
		if err != nil {
			return 0, err
		}
	case "ksi":
		themes, err := parsePositiveFormInt(form, "ksi_themes", "Количество тем", 1, 100)
		if err != nil {
			return 0, err
		}
		gameID, err = createKSIGameTx(ctx, tx, festID, themes)
		if err != nil {
			return 0, err
		}
	case "ek":
		raw := strings.TrimSpace(form.Get("ek_scheme"))
		if raw == "" {
			return 0, errors.New("Вставьте JSON-схему ЭК")
		}
		var scheme festScheme
		if err := json.Unmarshal([]byte(raw), &scheme); err != nil {
			return 0, fmt.Errorf("Не удалось разобрать JSON: %w", err)
		}
		gameID, err = createEKGameTx(ctx, tx, festID, scheme)
		if err != nil {
			return 0, err
		}
	}

	if _, err := bumpFestRevisionTx(ctx, tx, festID, "game:create", mustJSON(map[string]any{
		"gameID":   gameID,
		"gameType": gameType,
	})); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return gameID, nil
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
	totalQuestions := tours * questions
	entries := make([][]int, totalQuestions)
	for i := range entries {
		entries[i] = []int{}
	}
	schemeJSON := []byte(mustJSON(map[string]any{
		"schemaVersion": 2,
		"slug":          identity.Code,
		"title":         identity.Title,
		"gameType":      "od",
		"tourComp":      tourComp,
		"nTeams":        0,
		"teams":         []chgkTeamJSON{},
	}))
	stateJSON := []byte(mustJSON(map[string]any{
		"teams":          []chgkTeamJSON{},
		"entries":        entries,
		"completed":      make([]bool, totalQuestions),
		"shootoutRounds": []any{},
	}))
	teams, err := loadFestRosterImportTeamsTx(ctx, tx, festID)
	if err != nil {
		return 0, err
	}
	if len(teams) > 0 {
		schemeJSON, err = applyRosterToChGKScheme(string(schemeJSON), teams)
		if err != nil {
			return 0, err
		}
		stateJSON, err = applyRosterToChGKState(string(stateJSON), teams, nil)
		if err != nil {
			return 0, err
		}
	}
	return insertJSONGameTx(ctx, tx, festID, identity, "od", schemeJSON, stateJSON)
}

func createKSIGameTx(ctx context.Context, tx *sql.Tx, festID int64, themesCount int) (int64, error) {
	identity, err := nextGameIdentityTx(ctx, tx, festID, "ksi", "КСИ")
	if err != nil {
		return 0, err
	}
	themes := make([]map[string]any, themesCount)
	for i := range themes {
		themes[i] = map[string]any{"answers": [][]string{}}
	}
	schemeJSON := []byte(mustJSON(map[string]any{
		"schemaVersion": 2,
		"slug":          identity.Code,
		"title":         identity.Title,
		"gameType":      "ksi",
		"participants":  []string{},
		"themes":        themesCount,
	}))
	stateJSON := []byte(mustJSON(map[string]any{
		"participants": []string{},
		"themes":       themes,
		"finished":     false,
	}))
	teams, err := loadFestRosterImportTeamsTx(ctx, tx, festID)
	if err != nil {
		return 0, err
	}
	if len(teams) > 0 {
		schemeJSON, err = applyRosterToKSIScheme(string(schemeJSON), teams)
		if err != nil {
			return 0, err
		}
		stateJSON, err = applyRosterToKSIState(string(stateJSON), teams, themesCount)
		if err != nil {
			return 0, err
		}
	}
	return insertJSONGameTx(ctx, tx, festID, identity, "ksi", schemeJSON, stateJSON)
}

func insertJSONGameTx(ctx context.Context, tx *sql.Tx, festID int64, identity gameIdentity, gameType string, schemeJSON, stateJSON []byte) (int64, error) {
	now := utcNow()
	schemeID, err := insertReturningID(ctx, tx, `
insert into schemes(slug, title, version, schema_json, created_at)
values(?, ?, 2, ?, ?)`, uniqueSchemeSlug(identity.Code), identity.Title, string(schemeJSON), now)
	if err != nil {
		return 0, err
	}
	return insertReturningID(ctx, tx, `
insert into games(fest_id, code, title, game_type, position, scheme_id, scheme_json, state_json, status, team_list_source, roster_source, revision, created_at, updated_at)
values(?, ?, ?, ?, ?, ?, ?, ?, 'active', 'fest', 'fest', 1, ?, ?)`,
		festID, identity.Code, identity.Title, gameType, identity.Position, schemeID, string(schemeJSON), string(stateJSON), now, now)
}

func createEKGameTx(ctx context.Context, tx *sql.Tx, festID int64, scheme festScheme) (int64, error) {
	if scheme.GameType == "" {
		scheme.GameType = defaultGameType
	}
	if scheme.GameType != defaultGameType {
		return 0, errors.New("для ЭК нужна JSON-схема с gameType \"ek\"")
	}
	if err := validateScheme(scheme); err != nil {
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

	now := utcNow()
	schemeID, err := insertReturningID(ctx, tx, `
insert into schemes(slug, title, version, schema_json, created_at)
values(?, ?, ?, ?, ?)`, uniqueSchemeSlug(scheme.Slug), title, maxInt(scheme.SchemaVersion, 2), string(schemaJSON), now)
	if err != nil {
		return 0, err
	}
	gameID, err := insertReturningID(ctx, tx, `
insert into games(fest_id, code, title, game_type, position, scheme_id, scheme_json, state_json, status, team_list_source, roster_source, revision, created_at, updated_at)
values(?, ?, ?, ?, ?, ?, ?, '{}', 'pending', 'fest', 'fest', 1, ?, ?)`,
		festID, identity.Code, title, defaultGameType, identity.Position, schemeID, string(schemaJSON), now, now)
	if err != nil {
		return 0, err
	}

	venueIDs := make(map[int]int64, len(scheme.Venues))
	for _, venue := range scheme.Venues {
		venueID, err := upsertVenueTx(ctx, tx, festID, venue, now)
		if err != nil {
			return 0, err
		}
		venueIDs[venue.Number] = venueID
	}

	for stageIndex, stage := range scheme.Stages {
		position := stage.Position
		if position == 0 {
			position = stageIndex + 1
		}
		configJSON := stageConfigJSON(stage)
		stageType := stage.StageType
		if stageType == "" {
			stageType = "matches"
		}
		stageID, err := insertReturningID(ctx, tx, `
insert into stages(fest_id, game_id, code, title, stage_type, position, status, config_json)
values(?, ?, ?, ?, ?, ?, 'pending', ?)`, festID, gameID, stage.Code, stage.Title, stageType, position, configJSON)
		if err != nil {
			return 0, err
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
			matchID, err := insertReturningID(ctx, tx, `
insert into matches(fest_id, game_id, stage_id, code, title, position, participant_count, venue_id, status, revision)
values(?, ?, ?, ?, ?, ?, ?, ?, 'pending', 1)`, festID, gameID, stageID, match.Code, match.Title, matchIndex+1, participantCount, venueID)
			if err != nil {
				return 0, err
			}
			for slotIndex, slot := range match.Slots {
				sourceType, sourceRef := slotSource(slot)
				if _, err := tx.ExecContext(ctx, `
insert into match_slots(match_id, slot_index, source_type, source_ref_json, team_id, locked)
values(?, ?, ?, ?, null, 0)`, matchID, slotIndex, sourceType, sourceRef); err != nil {
					return 0, err
				}
			}
		}
	}
	return gameID, nil
}

func upsertVenueTx(ctx context.Context, tx *sql.Tx, festID int64, venue schemeVenue, now string) (int64, error) {
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
