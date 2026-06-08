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

type hostFestNumbersData struct {
	Fest       hostMyFest
	Rows       []hostFestNumberRow
	Error      string
	Notice     string
	HasNumbers bool
}

var hostFestNumbersTemplate = template.Must(template.New("hostNumbers").Parse(`<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Fest.Title}} · номера команд</title>
  <link rel="preload" href="/static/fonts/noto-sans-400.woff2" as="font" type="font/woff2" crossorigin>
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
    <form method="post" action="/host/fest/{{.Fest.Ref}}/numbers" class="card stack number-form" id="numbers-form" autocomplete="off" data-has-numbers="{{if .HasNumbers}}1{{end}}">
      <div class="cluster numbers-actions">
        {{if .HasNumbers}}
        <button class="btn" type="submit" id="numbers-clear-btn" formaction="/host/fest/{{.Fest.Ref}}/numbers/clear" formnovalidate>Очистить</button>
        {{end}}
        <button class="btn" type="submit" id="numbers-auto-btn" formaction="/host/fest/{{.Fest.Ref}}/numbers/auto" formnovalidate>Проставить автоматически</button>
        <button class="btn" type="button" id="numbers-import-btn">Импорт номеров</button>
        {{if .HasNumbers}}
        <button class="btn" type="button" id="numbers-edit-btn">Замена номера</button>
        {{end}}
      </div>
      <p class="muted numbers-help" id="numbers-help" hidden>
        Меняйте номер прямо в строке. Когда сохраните, все упоминания старого номера в ОД заменятся на новый — удобно, чтобы перевести команду на резервный номер (101 и т. п.), не задевая остальных.
      </p>
      <ol class="number-list">
        {{range .Rows}}
        <li class="number-row">
          <input class="number-row-num"
                 type="text"
                 inputmode="numeric"
                 maxlength="4"
                 name="num_{{.Index}}"
                 value="{{.Number}}"
                 readonly>
          <span class="number-row-team">{{.TeamLabel}}</span>
          <input type="hidden" name="team_label_{{.Index}}" value="{{.TeamLabel}}">
          <input type="hidden" name="team_id_{{.Index}}" value="{{if .TeamID}}{{.TeamID}}{{end}}">
        </li>
        {{end}}
      </ol>
      <div class="cluster" id="numbers-save" hidden>
        <button class="btn" type="submit">Сохранить</button>
        <button class="btn btn-secondary" type="button" id="numbers-cancel-btn">Отмена</button>
      </div>
    </form>
    <script>
      (() => {
        const form = document.getElementById("numbers-form");
        if (!form) return;
        const hasNumbers = form.dataset.hasNumbers === "1";
        const editBtn = document.getElementById("numbers-edit-btn");
        const autoBtn = document.getElementById("numbers-auto-btn");
        const clearBtn = document.getElementById("numbers-clear-btn");
        const cancelBtn = document.getElementById("numbers-cancel-btn");
        const help = document.getElementById("numbers-help");
        const save = document.getElementById("numbers-save");
        const numInputs = form.querySelectorAll(".number-row-num");

        const enterEdit = () => {
          form.classList.add("editing");
          numInputs.forEach((input) => input.removeAttribute("readonly"));
          help.removeAttribute("hidden");
          save.removeAttribute("hidden");
          if (editBtn) editBtn.setAttribute("hidden", "");
          if (autoBtn) autoBtn.setAttribute("hidden", "");
          if (clearBtn) clearBtn.setAttribute("hidden", "");
        };
        const exitEdit = () => {
          location.reload();
        };

        if (editBtn) editBtn.addEventListener("click", enterEdit);
        if (cancelBtn) cancelBtn.addEventListener("click", exitEdit);
        if (autoBtn && hasNumbers) {
          autoBtn.addEventListener("click", (event) => {
            const ok = window.confirm("Команды будут перенумерованы 1..N по алфавиту. Если бланки ответов уже напечатаны со старыми номерами — они станут невалидными. Продолжить?");
            if (!ok) event.preventDefault();
          });
        }
        if (clearBtn) {
          clearBtn.addEventListener("click", (event) => {
            const ok = window.confirm("Очистить все номера команд?");
            if (!ok) event.preventDefault();
          });
        }

        const importBtn = document.getElementById("numbers-import-btn");
        const baseAction = form.getAttribute("action");

        const closeDialog = (dialog) => {
          if (dialog && dialog.parentNode) dialog.close();
        };

        const openImportDialog = () => {
          const dialog = document.createElement("dialog");
          dialog.className = "numbers-import-dialog";
          const dform = document.createElement("form");
          dform.className = "numbers-import-form";
          const title = document.createElement("h2");
          title.textContent = "Импорт номеров";
          const hint = document.createElement("p");
          hint.className = "muted";
          hint.textContent = "Вставьте строки в формате «номер⇥команда» (через табуляцию), по одной на строку. Имена сопоставятся с командами феста; неточные совпадения можно будет поправить.";
          const textarea = document.createElement("textarea");
          textarea.className = "numbers-import-textarea";
          textarea.rows = 12;
          textarea.placeholder = "1\tНазвание команды\n2\tДругая команда";
          const err = document.createElement("p");
          err.className = "empty";
          err.hidden = true;
          const actions = document.createElement("div");
          actions.className = "numbers-import-actions cluster";
          const cancel = document.createElement("button");
          cancel.type = "button";
          cancel.className = "btn btn-secondary";
          cancel.textContent = "Отмена";
          cancel.addEventListener("click", () => closeDialog(dialog));
          const submit = document.createElement("button");
          submit.type = "submit";
          submit.className = "btn";
          submit.textContent = "Сопоставить";
          actions.append(cancel, submit);
          dform.append(title, hint, textarea, err, actions);
          dform.addEventListener("submit", (event) => {
            event.preventDefault();
            const text = textarea.value;
            if (!text.trim()) {
              err.textContent = "Вставьте данные для импорта.";
              err.hidden = false;
              return;
            }
            submit.disabled = true;
            err.hidden = true;
            fetch(baseAction + "/import/match", {
              method: "POST",
              headers: { "Content-Type": "application/x-www-form-urlencoded" },
              body: new URLSearchParams({ text }),
            })
              .then((resp) => {
                if (!resp.ok) throw new Error("Ошибка сервера (" + resp.status + ").");
                return resp.json();
              })
              .then((data) => {
                closeDialog(dialog);
                openConfirmDialog(data);
              })
              .catch((e) => {
                submit.disabled = false;
                err.textContent = e.message || "Не удалось сопоставить.";
                err.hidden = false;
              });
          });
          dialog.appendChild(dform);
          dialog.addEventListener("close", () => dialog.remove());
          document.body.appendChild(dialog);
          dialog.showModal();
          textarea.focus();
        };

        const openConfirmDialog = (data) => {
          const teams = (data && data.teams) || [];
          const matches = (data && data.matches) || [];
          const errors = (data && data.errors) || [];

          const dialog = document.createElement("dialog");
          dialog.className = "numbers-import-dialog";
          const dform = document.createElement("form");
          dform.className = "numbers-import-form";
          const title = document.createElement("h2");
          title.textContent = "Подтвердите сопоставление";

          dform.append(title);

          if (errors.length) {
            const errBox = document.createElement("ul");
            errBox.className = "numbers-import-errors";
            errors.forEach((line) => {
              const li = document.createElement("li");
              li.textContent = line;
              errBox.appendChild(li);
            });
            dform.appendChild(errBox);
          }

          if (!matches.length) {
            const empty = document.createElement("p");
            empty.className = "empty";
            empty.textContent = "Не удалось разобрать ни одной строки.";
            dform.appendChild(empty);
          }

          const buildSelect = (selectedId) => {
            const select = document.createElement("select");
            select.className = "numbers-import-select";
            const skip = document.createElement("option");
            skip.value = "";
            skip.textContent = "— пропустить —";
            select.appendChild(skip);
            teams.forEach((team) => {
              const opt = document.createElement("option");
              opt.value = String(team.id);
              opt.textContent = team.label;
              if (team.id === selectedId) opt.selected = true;
              select.appendChild(opt);
            });
            return select;
          };

          const rowEls = [];
          if (matches.length) {
            const list = document.createElement("ol");
            list.className = "numbers-import-list";
            matches.forEach((m) => {
              const li = document.createElement("li");
              li.className = "numbers-import-row";
              if (!m.teamId) li.classList.add("is-unmatched");
              else if (!m.exact) li.classList.add("is-fuzzy");

              const num = document.createElement("span");
              num.className = "numbers-import-num";
              num.textContent = m.number;

              const raw = document.createElement("span");
              raw.className = "numbers-import-raw";
              raw.textContent = m.raw;

              const arrow = document.createElement("span");
              arrow.className = "numbers-import-arrow";
              arrow.textContent = "→";

              const select = buildSelect(m.teamId);

              const badge = document.createElement("span");
              badge.className = "numbers-import-badge";
              if (!m.teamId) badge.textContent = "нет совпадения";
              else if (m.exact) badge.textContent = "точно";
              else badge.textContent = "≈ (" + m.distance + ")";

              li.append(num, raw, arrow, select, badge);
              list.appendChild(li);
              rowEls.push({ number: m.number, select });
            });
            dform.appendChild(list);
          }

          const err = document.createElement("p");
          err.className = "empty";
          err.hidden = true;
          dform.appendChild(err);

          const actions = document.createElement("div");
          actions.className = "numbers-import-actions cluster";
          const back = document.createElement("button");
          back.type = "button";
          back.className = "btn btn-secondary";
          back.textContent = "Назад";
          back.addEventListener("click", () => {
            closeDialog(dialog);
            openImportDialog();
          });
          const apply = document.createElement("button");
          apply.type = "submit";
          apply.className = "btn";
          apply.textContent = "Применить";
          if (!matches.length) apply.disabled = true;
          actions.append(back, apply);
          dform.appendChild(actions);

          dform.addEventListener("submit", (event) => {
            event.preventDefault();
            const assignments = [];
            const usedTeams = new Set();
            for (const row of rowEls) {
              const val = row.select.value;
              if (!val) continue;
              const teamId = Number(val);
              if (usedTeams.has(teamId)) {
                err.textContent = "Одна команда выбрана несколько раз — поправьте сопоставление.";
                err.hidden = false;
                return;
              }
              usedTeams.add(teamId);
              assignments.push({ teamId, number: row.number });
            }
            if (!assignments.length) {
              err.textContent = "Нет ни одного подтверждённого сопоставления.";
              err.hidden = false;
              return;
            }
            apply.disabled = true;
            err.hidden = true;
            fetch(baseAction + "/import/apply", {
              method: "POST",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify({ assignments }),
            })
              .then((resp) => resp.json())
              .then((res) => {
                if (!res.ok) throw new Error(res.error || "Не удалось сохранить.");
                location.reload();
              })
              .catch((e) => {
                apply.disabled = false;
                err.textContent = e.message || "Не удалось сохранить.";
                err.hidden = false;
              });
          });

          dialog.appendChild(dform);
          dialog.addEventListener("close", () => dialog.remove());
          document.body.appendChild(dialog);
          dialog.showModal();
        };

        if (importBtn) importBtn.addEventListener("click", openImportDialog);
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

	if len(override) > 0 {
		data.Rows = override
	} else {
		data.Rows = defaultNumberRows(teams)
	}
	for _, row := range data.Rows {
		if strings.TrimSpace(row.Number) != "" {
			data.HasNumbers = true
			break
		}
	}
	return data, nil
}

// defaultNumberRows builds one row per team: numbered teams first (sorted by
// number ascending), then unnumbered teams (alphabetical). Unnumbered teams
// get an empty number cell — the host has to explicitly trigger numbering via
// the auto-assign action or by editing a row.
func defaultNumberRows(teams []festNumberingTeam) []hostFestNumberRow {
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
		rows = append(rows, hostFestNumberRow{
			Index:     len(numbered) + i + 1,
			Number:    "",
			TeamID:    team.ID,
			TeamLabel: teamDisplayName(team),
		})
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
		if !hasTeam {
			if hasNum {
				s.renderHostFestNumbers(w, r, festID, fmt.Sprintf("Строка %d: укажите команду или удалите номер.", row.Index), "", override)
				return
			}
			continue
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
			// Team intentionally left without a number — keep it unnumbered.
			continue
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
	if err := s.purgeFestSoftDeletedTeams(r.Context(), festID); err != nil {
		s.renderHostFestNumbers(w, r, festID, err.Error(), "", nil)
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
	if err := s.purgeFestSoftDeletedTeams(r.Context(), festID); err != nil {
		s.renderHostFestNumbers(w, r, festID, err.Error(), "", nil)
		return
	}
	if err := s.saveFestNumbers(r.Context(), festID, nil); err != nil {
		s.renderHostFestNumbers(w, r, festID, err.Error(), "", nil)
		return
	}
	s.renderHostFestNumbers(w, r, festID, "", "Номера очищены.", nil)
}

// purgeFestSoftDeletedTeams hard-deletes any soft-deleted fest_teams rows for
// this fest. Used by the "assign numbers" and "clear" actions, which the host
// has explicitly confirmed as destructive resets — archived numbers from teams
// that left the roster must not block reuse of those numbers.
func (s *server) purgeFestSoftDeletedTeams(ctx context.Context, festID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.writeExec(ctx, `delete from fest_teams where fest_id = ? and deleted = 1`, festID)
	return err
}

func (s *server) saveFestNumbers(ctx context.Context, festID int64, assignments map[int64]int) error {
	var updates []gameStateBroadcast
	var revision int64
	err := func() error {
		s.mu.Lock()
		defer s.mu.Unlock()

		tx, err := s.beginWriteTx(ctx)
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

		if _, err := tx.ExecContext(ctx, `update fest_teams set number = null where fest_id = ? and deleted = 0`, festID); err != nil {
			return err
		}
		for teamID, number := range assignments {
			if _, err := tx.ExecContext(ctx, `update fest_teams set number = ? where id = ? and fest_id = ? and deleted = 0`, number, teamID, festID); err != nil {
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
		chgkUpdates, err := propagateRosterToChGKTx(ctx, tx, festID, teams, entryRemap)
		if err != nil {
			return err
		}
		// KSI carries the same universal team number in its participants list, so a
		// number reassignment must flow into KSI states too (the full roster import
		// propagates to both; the number-edit path used to skip KSI, leaving its
		// numbers stale relative to OD). Answers follow each team by name when the
		// number changed, so scores are preserved.
		ksiUpdates, err := propagateRosterToKSITx(ctx, tx, festID, teams)
		if err != nil {
			return err
		}
		updates = append(chgkUpdates, ksiUpdates...)
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
	return collectRows(ctx, q, `
select id, name, city, coalesce(number, 0)
from fest_teams
where fest_id = ? and deleted = 0
order by position, id`, []any{festID}, func(rows *sql.Rows) (festNumberingTeam, error) {
		var team festNumberingTeam
		if err := rows.Scan(&team.ID, &team.Name, &team.City, &team.Number); err != nil {
			return team, err
		}
		return team, nil
	})
}

func festTeamsAllNumbered(ctx context.Context, q dbQueryer, festID int64) (bool, int, error) {
	var total, numbered int
	if err := q.QueryRowContext(ctx, `
select count(*), coalesce(sum(case when number is not null then 1 else 0 end), 0)
from fest_teams where fest_id = ? and deleted = 0`, festID).Scan(&total, &numbered); err != nil {
		return false, 0, err
	}
	if total == 0 {
		return false, 0, nil
	}
	return numbered == total, total, nil
}

// festHasUnnumberedTeams reports whether the fest has active teams of which some
// lack a number — the precondition that blocks game editing (see
// requireNumberedTeams). A fest with no teams at all returns false: there is
// nothing to number yet, so editing (e.g. player-mode KSI) is not blocked.
func festHasUnnumberedTeams(ctx context.Context, q dbQueryer, festID int64) (bool, error) {
	allNumbered, total, err := festTeamsAllNumbered(ctx, q, festID)
	if err != nil {
		return false, err
	}
	return total > 0 && !allNumbered, nil
}
