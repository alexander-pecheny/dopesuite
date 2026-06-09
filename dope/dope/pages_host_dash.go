package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strings"
)

type hostFestDashData struct {
	Fest            hostMyFest
	Description     string
	Slug            string
	RatingID        int64
	Games           []publicFestGame
	Access          []hostAccessMember
	TeamCount       int
	PlayerCount     int
	NumbersAssigned int
	NumbersAllSet   bool
	CurrentRole     string
	CanManageFest   bool
	CanManageGames  bool
	CanManageAccess bool
	CanDeleteFest   bool
	IsCreator       bool
	Error           string
	AccessError     string
	AccessNotice    string
	ImportError     string
	ImportNotice    string
	RosterError     string
	RosterNotice    string
}

type hostDashMessages struct {
	FormError    string
	AccessError  string
	AccessNotice string
	ImportError  string
	ImportNotice string
	RosterError  string
	RosterNotice string
}

var hostFestDashTemplate = template.Must(template.New("hostDash").Parse(`<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Fest.Title}} · ведущий</title>
  <link rel="preload" href="/static/fonts/noto-sans-400.woff2" as="font" type="font/woff2" crossorigin>
  <link rel="stylesheet" href="/static/styles.css">
  <script src="/static/appearance.js"></script>
</head>
<body class="public">
  <header class="public-top">
    <a class="public-back" href="/host">←</a>
    <h1>{{.Fest.Title}}</h1>
  </header>
  <main class="public-main">
    {{if .Error}}<p class="empty">{{.Error}}</p>{{end}}
    {{if .CanManageFest}}
    <form method="post" action="/host/fest/{{.Fest.Ref}}" class="card stack" autocomplete="off">
      <label class="field">
        <span>Название</span>
        <input name="title" value="{{.Fest.Title}}" required>
      </label>
      <label class="field">
        <span>Описание (markdown)</span>
        <textarea name="description" rows="6">{{.Description}}</textarea>
      </label>
      <label class="field">
        <span>Slug (необязательно; задайте, чтобы получить URL вида /fest/{slug})</span>
        <input name="slug" value="{{.Slug}}" pattern="[a-z0-9-]+" placeholder="my-fest">
      </label>
      <label class="field">
        <span>Дата начала</span>
        <input name="start_date" value="{{.Fest.StartDate}}">
      </label>
      <label class="field">
        <span>Дата окончания</span>
        <input name="end_date" value="{{.Fest.EndDate}}">
      </label>
      <label class="field">
        <span>rating.chgk.info ID</span>
        <input name="rating_id" value="{{if .RatingID}}{{.RatingID}}{{end}}" inputmode="numeric">
      </label>
      <label class="checkbox">
        <input type="checkbox" name="is_public" value="1"{{if .Fest.IsPublic}} checked{{end}}>
        <span>Публичный</span>
      </label>
      <div class="cluster">
        <button class="btn" type="submit">Сохранить</button>
      </div>
    </form>
    {{end}}

    <section class="section">
      <h2>Игры</h2>
      {{if .Games}}
      <ul class="list">
        {{range .Games}}
        <li class="list-action-row">
          <a class="list-row" href="/host/fest/{{$.Fest.Ref}}/game/{{.Ref}}/">
            <span class="list-row-title">{{.Title}}</span>
            {{if .Slug}}<span class="muted">{{.Slug}}</span>{{end}}
          </a>
          {{if $.CanManageGames}}
          <a class="btn" href="/host/fest/{{$.Fest.Ref}}/game/{{.Ref}}/settings">Свойства</a>
          <form method="post" action="/host/fest/{{$.Fest.Ref}}/game/{{.Ref}}/clear" onsubmit="return confirm('Очистить игру? Все результаты, импортированные команды и посев будут удалены, игра вернётся в исходное состояние. Настройки и ссылка сохранятся.');">
            <button class="btn danger" type="submit">Очистить</button>
          </form>
          <form method="post" action="/host/fest/{{$.Fest.Ref}}/game/{{.Ref}}/delete" onsubmit="return confirm('Удалить игру? Все результаты этой игры будут потеряны.');">
            <button class="btn danger" type="submit">Удалить</button>
          </form>
          {{end}}
        </li>
        {{end}}
      </ul>
      {{else}}
      <p class="empty">Игр пока нет.</p>
      {{end}}
      {{if .CanManageGames}}
      <div class="cluster">
        <a class="btn" href="/host/fest/{{.Fest.Ref}}/game/new">Добавить игру</a>
      </div>
      {{end}}
    </section>

    {{if .CanManageAccess}}
    <section class="section" id="access">
      <h2>Доступ</h2>
      {{if .AccessError}}<p class="empty">{{.AccessError}}</p>{{end}}
      {{if .AccessNotice}}<p class="muted">{{.AccessNotice}}</p>{{end}}
      <div class="cluster">
        <button class="btn" type="button" data-access-bulk-open>Массовое действие</button>
      </div>
      <dialog class="modal-dialog" data-access-bulk-dialog>
        <form method="post" action="/host/fest/{{.Fest.Ref}}/access#access" class="stack" autocomplete="off">
          <h2>Массовое действие</h2>
          <input type="hidden" name="bulk_access" value="1">
          <label class="field">
            <span>Данные</span>
            <textarea name="bulk_access_lines" rows="8" placeholder="username1:host&#10;username2:host&#10;username3:admin&#10;username4:remove" required></textarea>
          </label>
          <div class="cluster">
            <button class="btn" type="submit">Применить</button>
            <button class="btn" type="button" data-access-bulk-close>Отмена</button>
          </div>
        </form>
      </dialog>
      <form method="post" action="/host/fest/{{.Fest.Ref}}/access#access" class="card stack" autocomplete="off">
        <div class="table-scroll">
          <table class="data-table access-table">
            <thead><tr><th class="access-name-col">Никнейм</th><th class="access-role-col">Роль</th><th class="access-action-col"></th></tr></thead>
            <tbody>
              {{range .Access}}
              <tr>
                <td class="access-name-cell">{{.Nickname}}</td>
                <td class="access-role-cell">
                  {{if .IsCreator}}
                  <input type="hidden" name="role_{{.UserID}}" value="creator">
                  <span class="access-role-label">creator</span>
                  {{else}}
                  <select name="role_{{.UserID}}" onchange="this.form.requestSubmit ? this.form.requestSubmit() : this.form.submit()">
                    <option value="admin"{{if eq .Role "admin"}} selected{{end}}>admin</option>
                    <option value="host"{{if eq .Role "host"}} selected{{end}}>host</option>
                  </select>
                  {{end}}
                </td>
                <td class="access-action-cell">
                  {{if not .IsCreator}}
                  <button class="btn danger" type="submit" name="delete_{{.UserID}}" value="1" onclick="return confirm('Удалить доступ для {{.Nickname}}?');">Удалить</button>
                  {{end}}
                </td>
              </tr>
              {{end}}
              <tr>
                <td class="access-name-cell"><input name="new_nickname" placeholder="nickname"></td>
                <td class="access-role-cell">
                  <select name="new_role">
                    <option value="host">host</option>
                    <option value="admin">admin</option>
                  </select>
                </td>
                <td class="access-action-cell"><button class="btn" type="submit" name="add_access" value="1">Добавить</button></td>
              </tr>
            </tbody>
          </table>
        </div>
      </form>
    </section>
    {{end}}

    {{if .CanManageFest}}
    <section class="section">
      <h2>Участники</h2>
      {{if .RosterError}}<p class="empty">{{.RosterError}}</p>{{end}}
      {{if .RosterNotice}}<p class="muted">{{.RosterNotice}}</p>{{end}}
      <ul class="list">
        <li>
          <a class="list-row" href="/host/fest/{{.Fest.Ref}}/teams">
            <span class="list-row-title">Команды</span>
            <span class="muted">{{.TeamCount}}</span>
          </a>
        </li>
        <li>
          <a class="list-row" href="/host/fest/{{.Fest.Ref}}/players">
            <span class="list-row-title">Игроки</span>
            <span class="muted">{{.PlayerCount}}</span>
          </a>
        </li>
        {{if .TeamCount}}
        <li>
          <a class="list-row" href="/host/fest/{{.Fest.Ref}}/numbers">
            <span class="list-row-title">Номера команд</span>
            <span class="muted">{{if .NumbersAllSet}}готово{{else if .NumbersAssigned}}{{.NumbersAssigned}} из {{.TeamCount}}{{else}}не выставлены{{end}}</span>
          </a>
        </li>
        {{end}}
        <li>
          <a class="list-row" href="/host/fest/{{.Fest.Ref}}/rating/import">
            <span class="list-row-title">Загрузить команды и игроков</span>
            <span class="muted">{{if .RatingID}}rating {{.RatingID}}{{else}}нет rating ID{{end}}</span>
          </a>
        </li>
        <li>
          <a class="list-row" href="/host/fest/{{.Fest.Ref}}/audit">
            <span class="list-row-title">История изменений</span>
            <span class="muted">откат состояния</span>
          </a>
        </li>
      </ul>
    </section>
    {{end}}

    {{if .CanDeleteFest}}
    <section class="section">
      <h2>Удаление</h2>
      <form method="post" action="/host/fest/{{.Fest.Ref}}/delete" class="card stack" autocomplete="off" onsubmit="return confirm('Удалить турнир? Все игры, команды и результаты будут удалены.');">
        <p class="muted">Удаление убирает фест со всеми играми, командами и результатами.</p>
        <div class="cluster">
          <button class="btn danger" type="submit">Удалить фест</button>
        </div>
      </form>
    </section>
    {{end}}
  </main>
  <script>
    (() => {
      const dialog = document.querySelector("[data-access-bulk-dialog]");
      const open = document.querySelector("[data-access-bulk-open]");
      const close = document.querySelector("[data-access-bulk-close]");
      if (!dialog || !open) return;
      open.addEventListener("click", () => {
        if (typeof dialog.showModal === "function") dialog.showModal();
        else dialog.setAttribute("open", "");
      });
      close?.addEventListener("click", () => {
        if (typeof dialog.close === "function") dialog.close();
        else dialog.removeAttribute("open");
      });
    })();
  </script>
</body>
</html>`))

func (s *server) handleHostCreateFest(w http.ResponseWriter, r *http.Request, user sessionUser) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	title := strings.TrimSpace(r.Form.Get("title"))
	if title == "" {
		s.renderHostLanding(w, r, "Название обязательно.")
		return
	}
	description := r.Form.Get("description")
	startDate := strings.TrimSpace(r.Form.Get("start_date"))
	endDate := strings.TrimSpace(r.Form.Get("end_date"))
	ratingID := parseOptionalInt64(r.Form.Get("rating_id"))
	isPublic := r.Form.Get("is_public") == "1"

	now := utcNow()
	tx, err := s.beginWriteTx(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	festID, err := insertReturningID(r.Context(), tx, `
insert into fests(slug, title, description, rating_id, created_by, revision, created_at, updated_at, start_date, end_date, is_public)
values(?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?)`,
		nil, title, description, ratingID, user.UserID, now, now,
		nullableString(startDate), nullableString(endDate), boolToInt(isPublic))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := tx.ExecContext(r.Context(), `
insert into fest_organizers(fest_id, user_id, role, added_at)
values(?, ?, 'creator', ?)`, festID, user.UserID, now); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/host/fest/%d", festID), http.StatusSeeOther)
}

func (s *server) handleHostUpdateFest(w http.ResponseWriter, r *http.Request, festID int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	title := strings.TrimSpace(r.Form.Get("title"))
	if title == "" {
		s.renderHostFestDashboard(w, r, festID, hostDashMessages{FormError: "Название обязательно."})
		return
	}
	description := r.Form.Get("description")
	startDate := strings.TrimSpace(r.Form.Get("start_date"))
	endDate := strings.TrimSpace(r.Form.Get("end_date"))
	ratingID := parseOptionalInt64(r.Form.Get("rating_id"))
	isPublic := r.Form.Get("is_public") == "1"
	slug := strings.TrimSpace(r.Form.Get("slug"))
	var slugValue any
	if slug != "" {
		if err := validateSlug(slug); err != nil {
			s.renderHostFestDashboard(w, r, festID, hostDashMessages{FormError: "Slug: " + err.Error()})
			return
		}
		if taken, err := s.slugTakenByOtherFest(r.Context(), slug, festID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		} else if taken {
			s.renderHostFestDashboard(w, r, festID, hostDashMessages{FormError: "Slug уже занят."})
			return
		}
		slugValue = slug
	}

	if _, err := s.writeExec(r.Context(), `
update fests
set title = ?, slug = ?, description = ?, rating_id = ?, start_date = ?, end_date = ?, is_public = ?, updated_at = ?
where id = ?`,
		title, slugValue, description, ratingID,
		nullableString(startDate), nullableString(endDate), boolToInt(isPublic),
		utcNow(), festID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.invalidateFestViewCache(festID)
	redirectRef := slug
	if redirectRef == "" {
		redirectRef = fmt.Sprintf("%d", festID)
	}
	http.Redirect(w, r, fmt.Sprintf("/host/fest/%s", redirectRef), http.StatusSeeOther)
}

func (s *server) handleHostSaveAccess(w http.ResponseWriter, r *http.Request, festID, actorID int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if r.Form.Get("bulk_access") == "1" {
		count, err := s.saveFestAccessBulk(r.Context(), festID, actorID, r.Form.Get("bulk_access_lines"))
		if err != nil {
			s.renderHostFestDashboard(w, r, festID, hostDashMessages{AccessError: err.Error()})
			return
		}
		s.renderHostFestDashboard(w, r, festID, hostDashMessages{AccessNotice: fmt.Sprintf("Массовое действие выполнено: %d.", count)})
		return
	}
	if err := s.saveFestAccess(r.Context(), festID, actorID, r.Form); err != nil {
		s.renderHostFestDashboard(w, r, festID, hostDashMessages{AccessError: err.Error()})
		return
	}
	s.renderHostFestDashboard(w, r, festID, hostDashMessages{AccessNotice: "Доступ сохранён."})
}

func (s *server) slugTakenByOtherFest(ctx context.Context, slug string, festID int64) (bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `select count(*) from fests where slug = ? and id <> ?`, slug, festID).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *server) festRefOrID(ctx context.Context, festID int64) string {
	var slug string
	if err := s.db.QueryRowContext(ctx, `select coalesce(slug, '') from fests where id = ?`, festID).Scan(&slug); err == nil && slug != "" {
		return slug
	}
	return fmt.Sprintf("%d", festID)
}

func (s *server) gameRefOrID(ctx context.Context, gameID int64) string {
	var slug string
	if err := s.db.QueryRowContext(ctx, `select coalesce(slug, '') from games where id = ?`, gameID).Scan(&slug); err == nil && slug != "" {
		return slug
	}
	return fmt.Sprintf("%d", gameID)
}

func (s *server) handleHostDeleteFest(w http.ResponseWriter, r *http.Request, festID, userID int64) {
	creator, err := s.isFestCreator(r.Context(), festID, userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !creator {
		http.Error(w, "only fest creator can delete fest", http.StatusForbidden)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.writeExec(r.Context(), `delete from fests where id = ? and created_by = ?`, festID, userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		http.NotFound(w, r)
		return
	}
	if s.festID == festID {
		s.festID = 0
		s.activeGameID = 0
		s.activeMatchCode = ""
	}
	http.Redirect(w, r, "/host", http.StatusSeeOther)
}

func (s *server) renderHostFestDashboard(w http.ResponseWriter, r *http.Request, festID int64, msgs hostDashMessages) {
	var (
		title       string
		slug        string
		description string
		startDate   sql.NullString
		endDate     sql.NullString
		ratingID    sql.NullInt64
		isPublic    int
	)
	if err := s.db.QueryRowContext(r.Context(), `
select title, coalesce(slug, ''), description, start_date, end_date, rating_id, is_public
from fests where id = ?`, festID).Scan(&title, &slug, &description, &startDate, &endDate, &ratingID, &isPublic); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	games, err := loadFestGames(r.Context(), s.db, festID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	festRef := slug
	if festRef == "" {
		festRef = fmt.Sprintf("%d", festID)
	}
	hostGames := make([]publicFestGame, len(games))
	for i, g := range games {
		hostGames[i] = publicFestGame{
			ID:    g.ID,
			Slug:  g.Slug,
			Code:  g.Code,
			Title: g.Title,
			Type:  gameTypeLabel(g.Type),
			URL:   fmt.Sprintf("/host/fest/%s/game/%s/", festRef, g.Ref()),
		}
	}
	teamCount, playerCount, err := s.loadHostFestRosterCounts(r.Context(), festID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var numbersAssigned int
	if err := s.db.QueryRowContext(r.Context(), `
select coalesce(sum(case when number is not null then 1 else 0 end), 0)
from fest_teams where fest_id = ? and deleted = 0`, festID).Scan(&numbersAssigned); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	currentRole := ""
	if user, ok := s.lookupSession(r); ok {
		currentRole, err = s.festUserRole(r.Context(), festID, user.UserID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	canManageFest := festRoleCanManageFest(currentRole)
	canManageAccess := festRoleCanManageAccess(currentRole)
	canDeleteFest := festRoleCanDeleteFest(currentRole)
	canManageGames := canManageFest
	var access []hostAccessMember
	if canManageAccess {
		access, err = s.loadFestAccessMembers(r.Context(), festID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	data := hostFestDashData{
		Fest: hostMyFest{
			ID:        festID,
			Slug:      slug,
			Title:     title,
			StartDate: startDate.String,
			EndDate:   endDate.String,
			Dates:     formatFestDates(startDate.String, endDate.String),
			IsPublic:  isPublic == 1,
		},
		Description:     description,
		Slug:            slug,
		Games:           hostGames,
		Access:          access,
		TeamCount:       teamCount,
		PlayerCount:     playerCount,
		NumbersAssigned: numbersAssigned,
		NumbersAllSet:   teamCount > 0 && numbersAssigned == teamCount,
		CurrentRole:     currentRole,
		CanManageFest:   canManageFest,
		CanManageGames:  canManageGames,
		CanManageAccess: canManageAccess,
		CanDeleteFest:   canDeleteFest,
		IsCreator:       canDeleteFest,
		Error:           msgs.FormError,
		AccessError:     msgs.AccessError,
		AccessNotice:    msgs.AccessNotice,
		ImportError:     msgs.ImportError,
		ImportNotice:    msgs.ImportNotice,
		RosterError:     msgs.RosterError,
		RosterNotice:    msgs.RosterNotice,
	}
	if ratingID.Valid {
		data.RatingID = ratingID.Int64
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = hostFestDashTemplate.Execute(w, data)
}

func (s *server) loadHostFestHeader(ctx context.Context, festID int64) (hostMyFest, error) {
	var t hostMyFest
	var pub int
	if err := s.db.QueryRowContext(ctx, `
select id, coalesce(slug, ''), title, coalesce(start_date, ''), coalesce(end_date, ''), is_public
from fests where id = ?`, festID).Scan(&t.ID, &t.Slug, &t.Title, &t.StartDate, &t.EndDate, &pub); err != nil {
		return hostMyFest{}, err
	}
	t.IsPublic = pub == 1
	t.Dates = formatFestDates(t.StartDate, t.EndDate)
	return t, nil
}

func (s *server) loadHostFestRosterCounts(ctx context.Context, festID int64) (int, int, error) {
	var teamCount, playerCount int
	if err := s.db.QueryRowContext(ctx, `select count(*) from fest_teams where fest_id = ? and deleted = 0`, festID).Scan(&teamCount); err != nil {
		return 0, 0, err
	}
	if err := s.db.QueryRowContext(ctx, `select count(*) from fest_players where fest_id = ?`, festID).Scan(&playerCount); err != nil {
		return 0, 0, err
	}
	return teamCount, playerCount, nil
}

func (s *server) loadFestRatingID(ctx context.Context, festID int64) (int64, error) {
	var ratingID sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `select rating_id from fests where id = ?`, festID).Scan(&ratingID); err != nil {
		return 0, err
	}
	if !ratingID.Valid {
		return 0, nil
	}
	return ratingID.Int64, nil
}

func (s *server) loadHostFests(ctx context.Context, userID int64) ([]hostMyFest, error) {
	return collectRows(ctx, s.db, `
select t.id, coalesce(t.slug, ''), t.title, coalesce(t.start_date, ''), coalesce(t.end_date, ''), t.is_public
from fests t
join fest_organizers o on o.fest_id = t.id
where o.user_id = ?
order by case when t.start_date is null or t.start_date = '' then 1 else 0 end,
         t.start_date desc,
         t.id desc`, []any{userID}, func(rows *sql.Rows) (hostMyFest, error) {
		var t hostMyFest
		var pub int
		if err := rows.Scan(&t.ID, &t.Slug, &t.Title, &t.StartDate, &t.EndDate, &pub); err != nil {
			return t, err
		}
		t.IsPublic = pub == 1
		t.Dates = formatFestDates(t.StartDate, t.EndDate)
		return t, nil
	})
}

func (s *server) isOrganizer(ctx context.Context, festID, userID int64) (bool, error) {
	role, err := s.festUserRole(ctx, festID, userID)
	if err != nil {
		return false, err
	}
	return role != "", nil
}

func (s *server) isFestCreator(ctx context.Context, festID, userID int64) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
select count(*) from fests where id = ? and created_by = ?`, festID, userID).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
