package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

const festNumberMax = 9999

type hostFestNumberRow struct {
	Index     int
	Number    string
	TeamID    int64
	TeamLabel string
}

type hostFestNumberOption struct {
	ID    int64
	Label string
}

type hostFestNumbersData struct {
	Fest    hostMyFest
	Rows    []hostFestNumberRow
	Options []hostFestNumberOption
	Error   string
	Notice  string
	HasAny  bool
	AllSet  bool
	Pending int
}

var hostFestNumbersTemplate = template.Must(template.New("hostNumbers").Parse(`<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Fest.Title}} · номера команд</title>
  <link rel="stylesheet" href="/static/styles.css">
</head>
<body class="public">
  <header class="public-top">
    <a class="public-back" href="/host/fest/{{.Fest.Ref}}">←</a>
    <h1>Номера команд</h1>
  </header>
  <main class="public-main">
    {{if .Error}}<p class="empty">{{.Error}}</p>{{end}}
    {{if .Notice}}<p class="muted">{{.Notice}}</p>{{end}}
    {{if not .Rows}}
    <p class="empty">Сначала загрузите команды на странице феста.</p>
    {{else}}
    <p class="muted">Назначьте команду каждому номеру. Номер можно переименовать в любую сторону — например, заменить на резервный (101 и т. п.). При изменении номера все его упоминания в ОД заменятся на новый.</p>
    <form method="post" action="/host/fest/{{.Fest.Ref}}/numbers/auto" class="cluster" autocomplete="off">
      <button class="btn" type="submit">Проставить автоматически</button>
      {{if .HasAny}}
      <button class="btn" type="submit" formaction="/host/fest/{{.Fest.Ref}}/numbers/clear">Очистить</button>
      {{end}}
    </form>
    <form method="post" action="/host/fest/{{.Fest.Ref}}/numbers" class="card stack number-form" autocomplete="off">
      <datalist id="number-team-options">
        {{range .Options}}<option data-id="{{.ID}}" value="{{.Label}}"></option>{{end}}
      </datalist>
      <ol class="number-list">
        {{range .Rows}}
        <li class="number-row">
          <input class="number-row-num"
                 type="text"
                 inputmode="numeric"
                 maxlength="4"
                 name="num_{{.Index}}"
                 value="{{.Number}}">
          <input class="number-row-team"
                 list="number-team-options"
                 name="team_label_{{.Index}}"
                 value="{{.TeamLabel}}"
                 placeholder="Название команды">
          <input type="hidden" name="team_id_{{.Index}}" value="{{if .TeamID}}{{.TeamID}}{{end}}">
        </li>
        {{end}}
      </ol>
      <div class="cluster">
        <button class="btn" type="submit">Сохранить</button>
      </div>
    </form>
    <script>
      (() => {
        const list = document.getElementById("number-team-options");
        if (!list) return;
        const byLabel = new Map();
        for (const option of list.options) {
          byLabel.set(option.value.toLowerCase(), option.dataset.id || "");
        }
        document.querySelectorAll(".number-row").forEach((row) => {
          const teamInput = row.querySelector(".number-row-team");
          const hidden = row.querySelector('input[type="hidden"]');
          if (!teamInput || !hidden) return;
          const sync = () => {
            const key = teamInput.value.trim().toLowerCase();
            const id = key ? (byLabel.get(key) || "") : "";
            hidden.value = id;
            teamInput.classList.toggle("number-row-bad", Boolean(key) && !id);
          };
          teamInput.addEventListener("input", sync);
          teamInput.addEventListener("change", sync);
          sync();
        });
      })();
    </script>
    {{end}}
  </main>
</body>
</html>`))

func (s *server) renderHostFestNumbers(w http.ResponseWriter, r *http.Request, festID int64, errMsg, notice string, override []hostFestNumberRow) {
	fest, err := s.loadHostFestHeader(r.Context(), festID)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data, err := s.buildHostFestNumbersData(r.Context(), festID, errMsg, notice, override)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data.Fest = fest
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = hostFestNumbersTemplate.Execute(w, data)
}

func (s *server) buildHostFestNumbersData(ctx context.Context, festID int64, errMsg, notice string, override []hostFestNumberRow) (hostFestNumbersData, error) {
	teams, err := loadFestTeamsForNumbering(ctx, s.db, festID)
	if err != nil {
		return hostFestNumbersData{}, err
	}
	data := hostFestNumbersData{Error: errMsg, Notice: notice}
	if len(teams) == 0 {
		return data, nil
	}

	byName := append([]festNumberingTeam(nil), teams...)
	sort.SliceStable(byName, func(i, j int) bool {
		if cmp := compareAlpha(byName[i].Name, byName[j].Name); cmp != 0 {
			return cmp < 0
		}
		if cmp := compareAlpha(byName[i].City, byName[j].City); cmp != 0 {
			return cmp < 0
		}
		return byName[i].ID < byName[j].ID
	})
	data.Options = make([]hostFestNumberOption, len(byName))
	for i, team := range byName {
		data.Options[i] = hostFestNumberOption{ID: team.ID, Label: teamDisplayName(team)}
	}

	if len(override) > 0 {
		data.Rows = override
	} else {
		data.Rows = defaultNumberRows(teams)
	}
	for _, row := range data.Rows {
		if row.TeamID > 0 || strings.TrimSpace(row.Number) != "" {
			data.HasAny = true
		}
	}
	assigned := 0
	for _, row := range data.Rows {
		if row.TeamID > 0 && strings.TrimSpace(row.Number) != "" {
			assigned++
		}
	}
	data.AllSet = assigned == len(data.Rows)
	data.Pending = len(data.Rows) - assigned
	return data, nil
}

// defaultNumberRows builds N rows: numbered teams first (sorted by number ascending),
// followed by unnumbered teams with auto-suggested unused numbers (1..) so the user
// sees "left=number, right=team" with numbers 1..N pre-filled when nothing is assigned.
func defaultNumberRows(teams []festNumberingTeam) []hostFestNumberRow {
	used := make(map[int]bool, len(teams))
	for _, team := range teams {
		if team.Number > 0 {
			used[team.Number] = true
		}
	}
	numbered := make([]festNumberingTeam, 0, len(teams))
	unnumbered := make([]festNumberingTeam, 0, len(teams))
	for _, team := range teams {
		if team.Number > 0 {
			numbered = append(numbered, team)
		} else {
			unnumbered = append(unnumbered, team)
		}
	}
	sort.SliceStable(numbered, func(i, j int) bool { return numbered[i].Number < numbered[j].Number })
	sort.SliceStable(unnumbered, func(i, j int) bool {
		if cmp := compareAlpha(unnumbered[i].Name, unnumbered[j].Name); cmp != 0 {
			return cmp < 0
		}
		if cmp := compareAlpha(unnumbered[i].City, unnumbered[j].City); cmp != 0 {
			return cmp < 0
		}
		return unnumbered[i].ID < unnumbered[j].ID
	})
	nextUnused := 1
	rows := make([]hostFestNumberRow, 0, len(teams))
	for i, team := range numbered {
		rows = append(rows, hostFestNumberRow{
			Index:     i + 1,
			Number:    strconv.Itoa(team.Number),
			TeamID:    team.ID,
			TeamLabel: teamDisplayName(team),
		})
	}
	for i, team := range unnumbered {
		for used[nextUnused] {
			nextUnused++
		}
		rows = append(rows, hostFestNumberRow{
			Index:     len(numbered) + i + 1,
			Number:    strconv.Itoa(nextUnused),
			TeamID:    team.ID,
			TeamLabel: teamDisplayName(team),
		})
		used[nextUnused] = true
		nextUnused++
	}
	if len(numbered) == 0 {
		// Fresh state: keep teams empty on the right, just numbers 1..N on the left.
		for i := range rows {
			rows[i].TeamID = 0
			rows[i].TeamLabel = ""
		}
	}
	return rows
}

type parsedNumberRow struct {
	Index    int
	NumRaw   string
	LabelRaw string
	TeamID   int64
}

func parseNumberRowsFromForm(r *http.Request, rowCount int) []parsedNumberRow {
	rows := make([]parsedNumberRow, 0, rowCount)
	for i := 1; i <= rowCount; i++ {
		numRaw := r.Form.Get(fmt.Sprintf("num_%d", i))
		labelRaw := r.Form.Get(fmt.Sprintf("team_label_%d", i))
		idRaw := strings.TrimSpace(r.Form.Get(fmt.Sprintf("team_id_%d", i)))
		var teamID int64
		if idRaw != "" {
			if v, err := strconv.ParseInt(idRaw, 10, 64); err == nil && v > 0 {
				teamID = v
			}
		}
		rows = append(rows, parsedNumberRow{Index: i, NumRaw: numRaw, LabelRaw: labelRaw, TeamID: teamID})
	}
	return rows
}

func (rows parsedNumberRowSlice) override() []hostFestNumberRow {
	out := make([]hostFestNumberRow, len(rows))
	for i, row := range rows {
		out[i] = hostFestNumberRow{
			Index:     row.Index,
			Number:    strings.TrimSpace(row.NumRaw),
			TeamID:    row.TeamID,
			TeamLabel: strings.TrimSpace(row.LabelRaw),
		}
	}
	return out
}

type parsedNumberRowSlice []parsedNumberRow

func (s *server) handleHostSaveFestNumbers(w http.ResponseWriter, r *http.Request, festID int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	teams, err := loadFestTeamsForNumbering(r.Context(), s.db, festID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if len(teams) == 0 {
		s.renderHostFestNumbers(w, r, festID, "Сначала загрузите команды.", "", nil)
		return
	}
	validIDs := make(map[int64]bool, len(teams))
	for _, team := range teams {
		validIDs[team.ID] = true
	}
	rows := parseNumberRowsFromForm(r, len(teams))
	override := parsedNumberRowSlice(rows).override()

	numberToRow := make(map[int]int)
	teamToRow := make(map[int64]int)
	assignments := make(map[int64]int, len(teams))

	for _, row := range rows {
		numTxt := strings.TrimSpace(row.NumRaw)
		hasNum := numTxt != ""
		hasTeam := row.TeamID != 0
		if !hasNum && !hasTeam {
			continue
		}
		if !hasTeam {
			s.renderHostFestNumbers(w, r, festID, fmt.Sprintf("Строка %d: укажите команду или удалите номер.", row.Index), "", override)
			return
		}
		if !validIDs[row.TeamID] {
			s.renderHostFestNumbers(w, r, festID, fmt.Sprintf("Строка %d: команда не из этого феста.", row.Index), "", override)
			return
		}
		if prev, ok := teamToRow[row.TeamID]; ok {
			s.renderHostFestNumbers(w, r, festID, fmt.Sprintf("Команда выбрана сразу в строках %d и %d.", prev, row.Index), "", override)
			return
		}
		teamToRow[row.TeamID] = row.Index
		if !hasNum {
			s.renderHostFestNumbers(w, r, festID, fmt.Sprintf("Строка %d: введите номер.", row.Index), "", override)
			return
		}
		n, err := strconv.Atoi(numTxt)
		if err != nil || n <= 0 || n > festNumberMax {
			s.renderHostFestNumbers(w, r, festID, fmt.Sprintf("Строка %d: номер должен быть целым от 1 до %d.", row.Index, festNumberMax), "", override)
			return
		}
		if prev, ok := numberToRow[n]; ok {
			s.renderHostFestNumbers(w, r, festID, fmt.Sprintf("Номер %d указан сразу в строках %d и %d.", n, prev, row.Index), "", override)
			return
		}
		numberToRow[n] = row.Index
		assignments[row.TeamID] = n
	}

	if err := s.saveFestNumbers(r.Context(), festID, assignments); err != nil {
		s.renderHostFestNumbers(w, r, festID, err.Error(), "", override)
		return
	}
	notice := "Номера сохранены."
	if len(assignments) < len(teams) {
		notice = fmt.Sprintf("Сохранено. Осталось без номера: %d.", len(teams)-len(assignments))
	}
	s.renderHostFestNumbers(w, r, festID, "", notice, nil)
}

func (s *server) handleHostAutoFestNumbers(w http.ResponseWriter, r *http.Request, festID int64) {
	teams, err := loadFestTeamsForNumbering(r.Context(), s.db, festID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if len(teams) == 0 {
		s.renderHostFestNumbers(w, r, festID, "Сначала загрузите команды.", "", nil)
		return
	}
	sorted := append([]festNumberingTeam(nil), teams...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if cmp := compareAlpha(sorted[i].Name, sorted[j].Name); cmp != 0 {
			return cmp < 0
		}
		if cmp := compareAlpha(sorted[i].City, sorted[j].City); cmp != 0 {
			return cmp < 0
		}
		return sorted[i].ID < sorted[j].ID
	})
	assignments := make(map[int64]int, len(sorted))
	for i, team := range sorted {
		assignments[team.ID] = i + 1
	}
	if err := s.saveFestNumbers(r.Context(), festID, assignments); err != nil {
		s.renderHostFestNumbers(w, r, festID, err.Error(), "", nil)
		return
	}
	s.renderHostFestNumbers(w, r, festID, "", "Номера проставлены автоматически по алфавиту.", nil)
}

func (s *server) handleHostClearFestNumbers(w http.ResponseWriter, r *http.Request, festID int64) {
	if err := s.saveFestNumbers(r.Context(), festID, nil); err != nil {
		s.renderHostFestNumbers(w, r, festID, err.Error(), "", nil)
		return
	}
	s.renderHostFestNumbers(w, r, festID, "", "Номера очищены.", nil)
}

func (s *server) saveFestNumbers(ctx context.Context, festID int64, assignments map[int64]int) error {
	var updates []gameStateBroadcast
	var revision int64
	err := func() error {
		s.mu.Lock()
		defer s.mu.Unlock()

		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()

		oldTeams, err := loadFestTeamsForNumbering(ctx, tx, festID)
		if err != nil {
			return err
		}
		oldByID := make(map[int64]int, len(oldTeams))
		for _, team := range oldTeams {
			oldByID[team.ID] = team.Number
		}
		entryRemap := make(map[int]int)
		for teamID, newNum := range assignments {
			oldNum := oldByID[teamID]
			if oldNum > 0 && oldNum != newNum {
				entryRemap[oldNum] = newNum
			}
		}

		if _, err := tx.ExecContext(ctx, `update fest_teams set number = null where fest_id = ?`, festID); err != nil {
			return err
		}
		for teamID, number := range assignments {
			if _, err := tx.ExecContext(ctx, `update fest_teams set number = ? where id = ? and fest_id = ?`, number, teamID, festID); err != nil {
				return err
			}
		}
		teams, err := loadFestRosterImportTeamsTx(ctx, tx, festID)
		if err != nil {
			return err
		}
		if len(entryRemap) == 0 {
			entryRemap = nil
		}
		updates, err = propagateRosterToChGKTx(ctx, tx, festID, teams, entryRemap)
		if err != nil {
			return err
		}
		revision, err = bumpFestRevisionTx(ctx, tx, festID, "fest:numbers", mustJSON(map[string]any{
			"assigned": len(assignments),
			"remapped": len(entryRemap),
		}))
		if err != nil {
			return err
		}
		return tx.Commit()
	}()
	if err != nil {
		return err
	}
	for _, update := range updates {
		s.broadcastState(festID, fmt.Sprintf("game-state:%d", update.GameID), revision, update.StateJSON)
	}
	return nil
}

type festNumberingTeam struct {
	ID     int64
	Name   string
	City   string
	Number int
}

func teamDisplayName(team festNumberingTeam) string {
	if team.City == "" {
		return team.Name
	}
	return fmt.Sprintf("%s (%s)", team.Name, team.City)
}

func loadFestTeamsForNumbering(ctx context.Context, q dbQueryer, festID int64) ([]festNumberingTeam, error) {
	rows, err := q.QueryContext(ctx, `
select id, name, city, coalesce(number, 0)
from fest_teams
where fest_id = ?
order by position, id`, festID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []festNumberingTeam
	for rows.Next() {
		var team festNumberingTeam
		if err := rows.Scan(&team.ID, &team.Name, &team.City, &team.Number); err != nil {
			return nil, err
		}
		out = append(out, team)
	}
	return out, rows.Err()
}

func festTeamsAllNumbered(ctx context.Context, q dbQueryer, festID int64) (bool, int, error) {
	var total, numbered int
	if err := q.QueryRowContext(ctx, `
select count(*), coalesce(sum(case when number is not null then 1 else 0 end), 0)
from fest_teams where fest_id = ?`, festID).Scan(&total, &numbered); err != nil {
		return false, 0, err
	}
	if total == 0 {
		return false, 0, nil
	}
	return numbered == total, total, nil
}
