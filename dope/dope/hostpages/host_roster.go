package hostpages

import (
	"context"
	"database/sql"
	"dope/dope/imports"
	"dope/dope/overrides"
	"dope/dope/store"
	"dope/dope/util"
	"dope/dope/view"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"strings"
)

type hostFestTeam struct {
	RatingID int64
	Name     string
	City     string
	Players  int
}

type hostFestPlayer struct {
	RatingID int64
	Name     string
	Team     string
}

type hostFestRosterData struct {
	Fest            view.HostFest
	Teams           []hostFestTeam
	Players         []hostFestPlayer
	OverridePlayers []overrides.HostPlayerOverrideOption
	OverrideTeams   []overrides.HostTeamOverrideOption
	OverrideGames   []overrides.HostGameOverrideOption
	Overrides       []overrides.HostPlayerOverrideRow
	Error           string
	Notice          string
}

type hostFestImportData struct {
	Fest     view.HostFest
	RatingID int64
	Error    string
	Notice   string
}

var hostFestTeamsTemplate = template.Must(template.New("hostTeams").Parse(`<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Fest.Title}} · команды</title>
  <link rel="preload" href="/static/fonts/noto-sans-400.woff2" as="font" type="font/woff2" crossorigin>
  <link rel="stylesheet" href="/static/styles.css">
  <script src="/static/menu.js"></script>
</head>
<body class="public">
  <header class="public-top">
    <a class="public-back" href="/host/fest/{{.Fest.Ref}}">←</a>
    <h1>Команды</h1>
  </header>
  <main class="public-main">
    {{if .Teams}}
    <div class="table-scroll">
      <table class="data-table">
        <thead><tr><th>ID</th><th>Команда</th><th>Город</th><th>Игроков</th></tr></thead>
        <tbody>
          {{range .Teams}}
          <tr><td>{{if .RatingID}}{{.RatingID}}{{end}}</td><td>{{.Name}}</td><td>{{.City}}</td><td>{{.Players}}</td></tr>
          {{end}}
        </tbody>
      </table>
    </div>
    {{else}}
    <p class="empty">Команды пока не загружены.</p>
    {{end}}
  </main>
</body>
</html>`))

var hostFestPlayersTemplate = template.Must(template.New("hostPlayers").Parse(`<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Fest.Title}} · игроки</title>
  <link rel="preload" href="/static/fonts/noto-sans-400.woff2" as="font" type="font/woff2" crossorigin>
  <link rel="stylesheet" href="/static/styles.css">
  <script src="/static/menu.js"></script>
</head>
<body class="public">
  <header class="public-top">
    <a class="public-back" href="/host/fest/{{.Fest.Ref}}">←</a>
    <h1>Игроки</h1>
  </header>
  <main class="public-main">
    {{if .Error}}<p class="empty">{{.Error}}</p>{{end}}
    {{if .Notice}}<p class="muted">{{.Notice}}</p>{{end}}
    <div class="cluster">
      <button class="btn" type="button" data-player-override-open>Добавить оверрайд для игры</button>
    </div>

    <dialog class="modal-dialog player-override-dialog" data-player-override-dialog>
      <form method="post" action="/host/fest/{{.Fest.Ref}}/players/overrides" class="stack" autocomplete="off" data-player-override-form>
        <h2>Оверрайд игрока</h2>
        <input type="hidden" name="player_id" data-player-override-player-id>
        <input type="hidden" name="team_id" data-player-override-team-id>
        <label class="field">
          <span>Игрок</span>
          <input name="player_label" list="playerOverridePlayers" required data-player-override-player>
        </label>
        <datalist id="playerOverridePlayers">
          {{range .OverridePlayers}}<option value="{{.Label}}" data-id="{{.ID}}"></option>{{end}}
        </datalist>
        <label class="field">
          <span>Новая команда</span>
          <input name="team_label" list="playerOverrideTeams" required data-player-override-team>
        </label>
        <datalist id="playerOverrideTeams">
          {{range .OverrideTeams}}<option value="{{.Label}}" data-id="{{.ID}}"></option>{{end}}
        </datalist>
        <fieldset class="field game-type-fieldset">
          <span>Игры</span>
          <div class="checkbox-list">
            {{range .OverrideGames}}
            <label class="checkbox">
              <input type="checkbox" name="game_id" value="{{.ID}}">
              <span>{{.Label}}</span>
            </label>
            {{else}}
            <p class="empty">В фесте пока нет игр КСИ или ЭК.</p>
            {{end}}
          </div>
        </fieldset>
        <div class="cluster">
          <button class="btn" type="submit">Сохранить</button>
          <button class="btn" type="button" data-player-override-close>Отмена</button>
        </div>
      </form>
    </dialog>

    {{if .Overrides}}
    <section class="section" id="overrides">
      <h2>Оверрайды</h2>
      <div class="table-scroll">
        <table class="data-table player-overrides-table">
          <thead><tr><th>Игрок</th><th>Из команды</th><th>В команду</th><th>Игры</th><th class="override-action-cell"></th></tr></thead>
          <tbody>
            {{range .Overrides}}
            <tr>
              <td>{{.Player}}</td>
              <td>{{.SourceTeam}}</td>
              <td>{{.OverrideTeam}}</td>
              <td>{{.Games}}</td>
              <td class="override-action-cell"><button class="override-edit-button" type="button" data-player-override-edit-open="{{.DialogID}}" aria-label="Редактировать оверрайд" title="Редактировать оверрайд">✏️</button></td>
            </tr>
            {{end}}
          </tbody>
        </table>
      </div>
      {{range .Overrides}}
      {{$row := .}}
      <dialog class="modal-dialog player-override-dialog" id="{{.DialogID}}" data-player-override-edit-dialog>
        <form method="post" action="/host/fest/{{$.Fest.Ref}}/players/overrides" class="stack" autocomplete="off">
          <h2>Оверрайд игрока</h2>
          <input type="hidden" name="mode" value="edit">
          <input type="hidden" name="player_id" value="{{.PlayerID}}">
          <input type="hidden" name="source_team_id" value="{{.SourceTeamID}}">
          <input type="hidden" name="team_id" value="{{.OverrideTeamID}}">
          <div class="override-summary">
            <div><span>Игрок</span><strong>{{.Player}}</strong></div>
            <div><span>Из команды</span><strong>{{.SourceTeam}}</strong></div>
            <div><span>В команду</span><strong>{{.OverrideTeam}}</strong></div>
          </div>
          <fieldset class="field game-type-fieldset">
            <span>Игры</span>
            <div class="checkbox-list">
              {{range $.OverrideGames}}
              <label class="checkbox">
                <input type="checkbox" name="game_id" value="{{.ID}}" {{if $row.HasGame .ID}}checked{{end}}>
                <span>{{.Label}}</span>
              </label>
              {{end}}
            </div>
          </fieldset>
          <div class="cluster">
            <button class="btn" type="submit">Сохранить</button>
            <button class="btn danger" type="submit" name="delete" value="1" formnovalidate data-player-override-delete>Удалить</button>
            <button class="btn" type="button" data-player-override-edit-close>Отмена</button>
          </div>
        </form>
      </dialog>
      {{end}}
    </section>
    {{end}}

    {{if .Players}}
    <div class="table-scroll">
      <table class="data-table">
        <thead><tr><th>ID</th><th>Игрок</th><th>Команда</th></tr></thead>
        <tbody>
          {{range .Players}}
          <tr><td>{{if .RatingID}}{{.RatingID}}{{end}}</td><td>{{.Name}}</td><td>{{.Team}}</td></tr>
          {{end}}
        </tbody>
      </table>
    </div>
    {{else}}
    <p class="empty">Игроки пока не загружены.</p>
    {{end}}
  </main>
  <script>
    (() => {
      const dialog = document.querySelector("[data-player-override-dialog]");
      const open = document.querySelector("[data-player-override-open]");
      const close = document.querySelector("[data-player-override-close]");
      const form = document.querySelector("[data-player-override-form]");
      if (!dialog || !open || !form) return;
      const bindSuggest = (inputSelector, hiddenSelector, listID) => {
        const input = form.querySelector(inputSelector);
        const hidden = form.querySelector(hiddenSelector);
        const options = Array.from(document.getElementById(listID)?.options || []);
        const sync = () => {
          const found = options.find((option) => option.value === input.value);
          hidden.value = found?.dataset.id || "";
          input.setCustomValidity(hidden.value ? "" : "Выберите значение из подсказки");
        };
        input.addEventListener("input", sync);
        input.addEventListener("change", sync);
        return {sync, input, hidden};
      };
      const syncPlayer = bindSuggest("[data-player-override-player]", "[data-player-override-player-id]", "playerOverridePlayers");
      const syncTeam = bindSuggest("[data-player-override-team]", "[data-player-override-team-id]", "playerOverrideTeams");
      open.addEventListener("click", () => {
        if (typeof dialog.showModal === "function") dialog.showModal();
        else dialog.setAttribute("open", "");
      });
      close?.addEventListener("click", () => dialog.close ? dialog.close() : dialog.removeAttribute("open"));
      document.querySelectorAll("[data-player-override-edit-open]").forEach((button) => {
        button.addEventListener("click", () => {
          const editDialog = document.getElementById(button.dataset.playerOverrideEditOpen);
          if (!editDialog) return;
          if (typeof editDialog.showModal === "function") editDialog.showModal();
          else editDialog.setAttribute("open", "");
        });
      });
      document.querySelectorAll("[data-player-override-edit-close]").forEach((button) => {
        button.addEventListener("click", () => {
          const editDialog = button.closest("dialog");
          if (!editDialog) return;
          if (typeof editDialog.close === "function") editDialog.close();
          else editDialog.removeAttribute("open");
        });
      });
      document.querySelectorAll("[data-player-override-delete]").forEach((button) => {
        button.addEventListener("click", (event) => {
          if (!window.confirm("Удалить оверрайд?")) event.preventDefault();
        });
      });
      form.addEventListener("submit", (event) => {
        syncPlayer.sync();
        syncTeam.sync();
        if (syncPlayer.hidden.value && syncTeam.hidden.value) return;
        event.preventDefault();
        (syncPlayer.hidden.value ? syncTeam.input : syncPlayer.input).reportValidity();
      });
    })();
  </script>
</body>
</html>`))

var hostFestRatingImportTemplate = template.Must(template.New("hostRatingImport").Parse(`<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Fest.Title}} · импорт участников</title>
  <link rel="preload" href="/static/fonts/noto-sans-400.woff2" as="font" type="font/woff2" crossorigin>
  <link rel="stylesheet" href="/static/styles.css">
  <script src="/static/menu.js"></script>
</head>
<body class="public">
  <header class="public-top">
    <a class="public-back" href="/host/fest/{{.Fest.Ref}}">←</a>
    <h1>Импорт участников</h1>
  </header>
  <main class="public-main">
    {{if .Error}}<p class="empty">{{.Error}}</p>{{end}}
    {{if .Notice}}<p class="muted">{{.Notice}}</p>{{end}}
    <section class="section">
      {{if .RatingID}}
      <p class="muted">Источник: rating.chgk.info ID {{.RatingID}}</p>
      <form method="post" action="/host/fest/{{.Fest.Ref}}/rating/import" class="card stack" autocomplete="off">
        <p class="muted">Импорт заменит списки команд и игроков феста и обновит список команд в играх ЧГК и КСИ.</p>
        <div class="cluster">
          <button class="btn" type="submit">Загрузить команды и игроков</button>
        </div>
      </form>
      {{else}}
      <p class="empty">Сначала сохраните rating.chgk.info ID в свойствах феста.</p>
      {{end}}
    </section>
  </main>
</body>
</html>`))

var hostFestSchemeImportTemplate = template.Must(template.New("hostSchemeImport").Parse(`<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Fest.Title}} · импорт схемы</title>
  <link rel="preload" href="/static/fonts/noto-sans-400.woff2" as="font" type="font/woff2" crossorigin>
  <link rel="stylesheet" href="/static/styles.css">
  <script src="/static/menu.js"></script>
</head>
<body class="public">
  <header class="public-top">
    <a class="public-back" href="/host/fest/{{.Fest.Ref}}">←</a>
    <h1>Импорт схемы</h1>
  </header>
  <main class="public-main">
    {{if .Error}}<p class="empty">{{.Error}}</p>{{end}}
    {{if .Notice}}<p class="muted">{{.Notice}}</p>{{end}}
    <form method="post" action="/host/fest/{{.Fest.Ref}}/import" class="card stack" autocomplete="off">
      <p class="muted">Импорт пересоздаёт игру феста из JSON-схемы. Существующие игры этого феста будут заменены.</p>
      <label class="field">
        <span>JSON-схема</span>
        <textarea name="scheme" rows="14" placeholder='{"slug":"...","title":"...","gameType":"ek","stages":[...]}'></textarea>
      </label>
      <div class="cluster">
        <button class="btn" type="submit">Импортировать</button>
      </div>
    </form>
  </main>
</body>
</html>`))

func (s *Server) renderHostFestTeams(w http.ResponseWriter, r *http.Request, festID int64) {
	fest, err := s.h.LoadHostFestHeader(r.Context(), festID)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	teams, err := s.loadHostFestTeams(r.Context(), festID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = hostFestTeamsTemplate.Execute(w, hostFestRosterData{Fest: fest, Teams: teams})
}

func (s *Server) renderHostFestPlayers(w http.ResponseWriter, r *http.Request, festID int64) {
	s.renderHostFestPlayersWithMessage(w, r, festID, "", "")
}

func (s *Server) renderHostFestPlayersWithMessage(w http.ResponseWriter, r *http.Request, festID int64, errMsg, notice string) {
	fest, err := s.h.LoadHostFestHeader(r.Context(), festID)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	players, err := s.loadHostFestPlayers(r.Context(), festID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	overridePlayers, overrideTeams, overrideGames, overrides, err := overrides.LoadHostPlayerOverrideOptions(r.Context(), s.h.Engine().DB, festID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = hostFestPlayersTemplate.Execute(w, hostFestRosterData{
		Fest:            fest,
		Players:         players,
		OverridePlayers: overridePlayers,
		OverrideTeams:   overrideTeams,
		OverrideGames:   overrideGames,
		Overrides:       overrides,
		Error:           errMsg,
		Notice:          notice,
	})
}

func (s *Server) handleHostAddPlayerOverride(w http.ResponseWriter, r *http.Request, festID int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if r.Form.Get("mode") == "edit" || r.Form.Get("delete") == "1" {
		s.handleHostEditPlayerOverride(w, r, festID)
		return
	}
	playerID, err := overrides.ParseHostOverrideID(r.Form.Get("player_id"), "игрока")
	if err != nil {
		s.renderHostFestPlayersWithMessage(w, r, festID, err.Error(), "")
		return
	}
	teamID, err := overrides.ParseHostOverrideID(r.Form.Get("team_id"), "команду")
	if err != nil {
		s.renderHostFestPlayersWithMessage(w, r, festID, err.Error(), "")
		return
	}
	gameIDs, err := overrides.ParseHostOverrideGameIDs(r.Form["game_id"])
	if err != nil {
		s.renderHostFestPlayersWithMessage(w, r, festID, err.Error(), "")
		return
	}
	revision, ekGameIDs, err := overrides.SavePlayerTeamOverride(s.h, r.Context(), festID, playerID, teamID, gameIDs)
	if err != nil {
		s.renderHostFestPlayersWithMessage(w, r, festID, err.Error(), "")
		return
	}
	for _, gameID := range ekGameIDs {
		s.h.Engine().BroadcastState(festID, fmt.Sprintf("game-roster:%d", gameID), revision, []byte(`{}`))
	}
	http.Redirect(w, r, fmt.Sprintf("/host/fest/%s/players#overrides", s.festRefOrID(r.Context(), festID)), http.StatusSeeOther)
}

func (s *Server) handleHostEditPlayerOverride(w http.ResponseWriter, r *http.Request, festID int64) {
	playerID, err := overrides.ParseHostOverrideID(r.Form.Get("player_id"), "игрока")
	if err != nil {
		s.renderHostFestPlayersWithMessage(w, r, festID, err.Error(), "")
		return
	}
	sourceTeamID, err := overrides.ParseHostOverrideID(r.Form.Get("source_team_id"), "исходную команду")
	if err != nil {
		s.renderHostFestPlayersWithMessage(w, r, festID, err.Error(), "")
		return
	}
	teamID, err := overrides.ParseHostOverrideID(r.Form.Get("team_id"), "команду")
	if err != nil {
		s.renderHostFestPlayersWithMessage(w, r, festID, err.Error(), "")
		return
	}
	var gameIDs []int64
	if r.Form.Get("delete") != "1" {
		gameIDs, err = overrides.ParseHostOverrideGameIDs(r.Form["game_id"])
		if err != nil {
			s.renderHostFestPlayersWithMessage(w, r, festID, err.Error(), "")
			return
		}
	}
	revision, ekGameIDs, err := overrides.ReplacePlayerTeamOverride(s.h, r.Context(), festID, playerID, sourceTeamID, teamID, gameIDs)
	if err != nil {
		s.renderHostFestPlayersWithMessage(w, r, festID, err.Error(), "")
		return
	}
	for _, gameID := range ekGameIDs {
		s.h.Engine().BroadcastState(festID, fmt.Sprintf("game-roster:%d", gameID), revision, []byte(`{}`))
	}
	http.Redirect(w, r, fmt.Sprintf("/host/fest/%s/players#overrides", s.festRefOrID(r.Context(), festID)), http.StatusSeeOther)
}

func (s *Server) renderHostRatingImportPage(w http.ResponseWriter, r *http.Request, festID int64, errMsg, notice string) {
	fest, err := s.h.LoadHostFestHeader(r.Context(), festID)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ratingID, err := s.loadFestRatingID(r.Context(), festID)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = hostFestRatingImportTemplate.Execute(w, hostFestImportData{
		Fest:     fest,
		RatingID: ratingID,
		Error:    errMsg,
		Notice:   notice,
	})
}

func (s *Server) renderHostSchemeImportPage(w http.ResponseWriter, r *http.Request, festID int64, errMsg, notice string) {
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
	_ = hostFestSchemeImportTemplate.Execute(w, hostFestImportData{
		Fest:   fest,
		Error:  errMsg,
		Notice: notice,
	})
}

func (s *Server) loadHostFestTeams(ctx context.Context, festID int64) ([]hostFestTeam, error) {
	teams, err := store.CollectRows(ctx, s.h.Engine().DB, `
select coalesce(tt.rating_id, 0), tt.name, tt.city, count(ttp.player_id)
from fest_teams tt
left join fest_team_players ttp on ttp.team_id = tt.id
where tt.fest_id = ? and tt.deleted = 0
group by tt.id
order by tt.position, tt.id`, []any{festID}, func(rows *sql.Rows) (hostFestTeam, error) {
		var team hostFestTeam
		if err := rows.Scan(&team.RatingID, &team.Name, &team.City, &team.Players); err != nil {
			return team, err
		}
		return team, nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(teams, func(i, j int) bool {
		if cmp := util.CompareAlpha(teams[i].Name, teams[j].Name); cmp != 0 {
			return cmp < 0
		}
		if cmp := util.CompareAlpha(teams[i].City, teams[j].City); cmp != 0 {
			return cmp < 0
		}
		return teams[i].RatingID < teams[j].RatingID
	})
	return teams, nil
}

func (s *Server) loadHostFestPlayers(ctx context.Context, festID int64) ([]hostFestPlayer, error) {
	players, err := store.CollectRows(ctx, s.h.Engine().DB, `
select coalesce(p.rating_id, 0), p.first_name, p.last_name, tt.name
from fest_team_players ttp
join fest_players p on p.id = ttp.player_id
join fest_teams tt on tt.id = ttp.team_id
where tt.fest_id = ? and tt.deleted = 0
order by tt.position, tt.id, ttp.roster_order, p.id`, []any{festID}, func(rows *sql.Rows) (hostFestPlayer, error) {
		var firstName, lastName, teamName string
		var ratingID int64
		if err := rows.Scan(&ratingID, &firstName, &lastName, &teamName); err != nil {
			return hostFestPlayer{}, err
		}
		return hostFestPlayer{
			RatingID: ratingID,
			Name:     store.JoinPlayerName(firstName, lastName),
			Team:     teamName,
		}, nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(players, func(i, j int) bool {
		if cmp := util.CompareAlpha(players[i].Team, players[j].Team); cmp != 0 {
			return cmp < 0
		}
		if cmp := util.CompareAlpha(players[i].Name, players[j].Name); cmp != 0 {
			return cmp < 0
		}
		return players[i].RatingID < players[j].RatingID
	})
	return players, nil
}

func (s *Server) handleHostImportScheme(w http.ResponseWriter, r *http.Request, festID int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	raw := strings.TrimSpace(r.Form.Get("scheme"))
	if raw == "" {
		s.renderHostSchemeImportPage(w, r, festID, "Вставьте JSON схемы.", "")
		return
	}
	var scheme store.FestScheme
	if err := json.Unmarshal([]byte(raw), &scheme); err != nil {
		s.renderHostSchemeImportPage(w, r, festID, "Не удалось разобрать JSON: "+err.Error(), "")
		return
	}
	if err := s.h.ImportSchemeIntoFest(r.Context(), festID, scheme); err != nil {
		s.renderHostSchemeImportPage(w, r, festID, err.Error(), "")
		return
	}
	s.renderHostSchemeImportPage(w, r, festID, "", "Импорт выполнен.")
}

func (s *Server) handleHostImportRatingRoster(w http.ResponseWriter, r *http.Request, festID int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	ratingID, err := s.loadFestRatingID(r.Context(), festID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if ratingID <= 0 {
		s.renderHostRatingImportPage(w, r, festID, "Сначала сохраните rating.chgk.info ID в свойствах феста.", "")
		return
	}
	result, err := imports.FetchAndImportRatingRoster(s.h.Engine(), r.Context(), festID, ratingID)
	if err != nil {
		s.renderHostRatingImportPage(w, r, festID, err.Error(), "")
		return
	}
	var msg string
	if result.Unchanged {
		msg = fmt.Sprintf("Списки уже совпадают с рейтингом — изменений нет. Команд: %d, игроков: %d.", result.TeamCount, result.PlayerCount)
	} else {
		msg = fmt.Sprintf("Загружено команд: %d, игроков: %d. Обновлено игр ЧГК: %d, КСИ: %d.", result.TeamCount, result.PlayerCount, result.ODGameCount, result.KSIGameCount)
	}
	s.renderHostRatingImportPage(w, r, festID, "", msg)
}
