package main

import (
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// The per-game journal page lists a game's edits (grouped into the requests that
// produced them) newest-first, each with a "revert to before here" action that
// drives the per-game derived revert (checkpoint + replay). It reads the forward
// row-ops from the unified journal — the same entries replay reconstructs from —
// so what you see is exactly what revert acts on.

const journalPageGroups = 200

type journalGroup struct {
	MinID, MaxID int64
	When         string
	Actor        string
	Summary      string
	Count        int
	RevertTo     int64 // journal id to revert the game to (state before this group)
}

func (s *server) loadGameJournalGroups(gameID int64) ([]journalGroup, error) {
	rows, err := s.db.Query(`
select j.id, j.ts, j.op, j.payload, coalesce(j.request_id, ''), coalesce(u.username, '')
from journal j
left join users u on u.id = j.actor_user_id
where j.game_id = ? and j.op in (?, ?, ?)
order by j.id desc
limit 20000`, gameID, int(opRowIns), int(opRowSet), int(opRowDel))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type acc struct {
		minID, maxID int64
		when, actor  string
		tables       map[string]int
		count        int
	}
	order := []string{}
	groups := map[string]*acc{}
	for rows.Next() {
		var (
			id      int64
			ts      string
			op      int
			payload []byte
			reqID   string
			actor   string
		)
		if err := rows.Scan(&id, &ts, &op, &payload, &reqID, &actor); err != nil {
			return nil, err
		}
		key := reqID
		if key == "" {
			key = "id:" + strconv.FormatInt(id, 10) // ungrouped: one per row
		}
		g := groups[key]
		if g == nil {
			g = &acc{minID: id, maxID: id, when: ts, actor: actor, tables: map[string]int{}}
			groups[key] = g
			order = append(order, key)
		}
		if id < g.minID {
			g.minID = id
		}
		if id > g.maxID {
			g.maxID = id
		}
		table, _, err := decodeRowOpJSON(payload)
		if err == nil {
			g.tables[table]++
		}
		g.count++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]journalGroup, 0, len(order))
	for _, key := range order {
		g := groups[key]
		out = append(out, journalGroup{
			MinID:    g.minID,
			MaxID:    g.maxID,
			When:     g.when,
			Actor:    g.actor,
			Summary:  summarizeTables(g.tables),
			Count:    g.count,
			RevertTo: g.minID - 1,
		})
		if len(out) >= journalPageGroups {
			break
		}
	}
	return out, nil
}

func summarizeTables(tables map[string]int) string {
	names := make([]string, 0, len(tables))
	for t := range tables {
		names = append(names, t)
	}
	sort.Slice(names, func(i, j int) bool {
		if tables[names[i]] != tables[names[j]] {
			return tables[names[i]] > tables[names[j]]
		}
		return names[i] < names[j]
	})
	parts := make([]string, 0, len(names))
	for _, t := range names {
		parts = append(parts, fmt.Sprintf("%s ×%d", journalTableLabel(t), tables[t]))
	}
	return strings.Join(parts, ", ")
}

// journalTableLabel maps a table name to a short Russian-ish label for the UI.
func journalTableLabel(t string) string {
	switch t {
	case "answers":
		return "ответы"
	case "match_results":
		return "результаты"
	case "matches":
		return "матчи"
	case "themes":
		return "темы"
	case "match_slots":
		return "слоты"
	case "reseed_entries":
		return "пересев"
	case "games":
		return "состояние игры"
	default:
		return t
	}
}

var gameJournalTmpl = template.Must(template.New("game-journal").Parse(`<!doctype html>
<html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>История игры</title><link rel="stylesheet" href="/static/styles.css"></head>
<body><main class="container">
<h1>История изменений игры</h1>
{{if .Err}}<p class="error">{{.Err}}</p>{{end}}
{{if .Notice}}<p class="notice">{{.Notice}}</p>{{end}}
<p><a href="/host/fest/{{.FestRef}}/game/{{.GameID}}">← к игре</a></p>
<table class="data-table"><thead><tr><th>когда</th><th>кто</th><th>изменения</th><th></th></tr></thead><tbody>
{{range .Groups}}
<tr>
  <td>{{.When}}</td><td>{{.Actor}}</td><td>{{.Summary}} ({{.Count}})</td>
  <td><form method="post" action="/host/fest/{{$.FestRef}}/game/{{$.GameID}}/revert">
    <input type="hidden" name="target" value="{{.RevertTo}}">
    <button type="submit">откатить до этого момента</button></form></td>
</tr>
{{else}}
<tr><td colspan="4">пока нет изменений</td></tr>
{{end}}
</tbody></table>
</main></body></html>`))

func (s *server) renderGameJournal(w http.ResponseWriter, r *http.Request, festID, gameID int64, errMsg, notice string) {
	groups, err := s.loadGameJournalGroups(gameID)
	if err != nil {
		http.Error(w, "journal: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = gameJournalTmpl.Execute(w, map[string]any{
		"FestRef": festID,
		"GameID":  gameID,
		"Groups":  groups,
		"Err":     errMsg,
		"Notice":  notice,
	})
}

func (s *server) handleGameRevert(w http.ResponseWriter, r *http.Request, festID, gameID int64) {
	if err := r.ParseForm(); err != nil {
		s.renderGameJournal(w, r, festID, gameID, "bad form", "")
		return
	}
	target, err := strconv.ParseInt(strings.TrimSpace(r.Form.Get("target")), 10, 64)
	if err != nil {
		s.renderGameJournal(w, r, festID, gameID, "bad target", "")
		return
	}
	revision, err := s.revertGameToPoint(r.Context(), festID, gameID, target)
	if err != nil {
		s.renderGameJournal(w, r, festID, gameID, "Не удалось откатить: "+err.Error(), "")
		return
	}
	s.broadcastFestView(festScope{FestID: festID, GameID: gameID}, revision)
	s.renderGameJournal(w, r, festID, gameID, "", "Откат выполнен.")
}
