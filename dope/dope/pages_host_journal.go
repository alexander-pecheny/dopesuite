package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
)

// The per-game journal page lists a game's edits newest-first, each rendered as
// a human-readable description of what changed (parsed from the semantic event
// payload — answer marks, places, state-patch cells, …), with a "revert to
// before here" action driving the per-game derived revert (checkpoint + replay).
// Edits are grouped into the request that produced them so one host action is
// one row.

const journalPageGroups = 200

type journalChange struct {
	When     string
	Actor    string
	Lines    []string
	More     int
	RevertTo int64
}

const journalMaxLines = 16

func (s *server) loadGameJournalGroups(gameID int64) ([]journalChange, error) {
	rows, err := s.db.Query(`
select j.id, j.ts, j.op, j.payload, coalesce(j.request_id, ''), coalesce(u.username, '')
from journal j
left join users u on u.id = j.actor_user_id
where j.game_id = ?
order by j.id desc
limit 20000`, gameID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type acc struct {
		minID  int64
		when   string
		actor  string
		lines  []string
		tables map[string]int
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
			key = "id:" + strconv.FormatInt(id, 10)
		}
		g := groups[key]
		if g == nil {
			g = &acc{minID: id, when: ts, actor: actor, tables: map[string]int{}}
			groups[key] = g
			order = append(order, key)
		}
		if id < g.minID {
			g.minID = id
		}
		if actor != "" && g.actor == "" {
			g.actor = actor
		}
		jop := journalOp(op)
		if jop >= opEvImport {
			g.lines = append(g.lines, describeEvent(jop, payload)...)
		} else {
			// Row-op: fall back to a table tally if no semantic line is present.
			if table, _, err := decodeRowOpJSON(payload); err == nil {
				g.tables[table]++
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]journalChange, 0, len(order))
	for _, key := range order {
		g := groups[key]
		lines := g.lines
		if len(lines) == 0 { // no semantic event — summarize the touched tables
			if s := summarizeTables(g.tables); s != "" {
				lines = []string{s}
			}
		}
		more := 0
		if len(lines) > journalMaxLines {
			more = len(lines) - journalMaxLines
			lines = lines[:journalMaxLines]
		}
		out = append(out, journalChange{
			When:     formatJournalTime(g.when),
			Actor:    g.actor,
			Lines:    lines,
			More:     more,
			RevertTo: g.minID - 1,
		})
		if len(out) >= journalPageGroups {
			break
		}
	}
	return out, nil
}

// formatJournalTime renders the stored ISO-8601 UTC timestamp compactly as
// "YYYY-MM-DD HH:MM:SS", dropping the millisecond/zone noise.
func formatJournalTime(ts string) string {
	s := strings.Replace(ts, "T", " ", 1)
	if i := strings.IndexAny(s, ".Z"); i >= 0 {
		s = s[:i]
	}
	return s
}

// --- payload → human description --------------------------------------------

func describeEvent(op journalOp, payload []byte) []string {
	switch op {
	case opEvMatchUpdate:
		return describeMatchUpdate(payload)
	case opEvGameStatePatch:
		return describeStatePatch(payload)
	case opEvGameState:
		return []string{"состояние игры заменено целиком"}
	case opEvMatchVenue:
		return []string{"изменена площадка матча"}
	case opEvVenuesUpdate:
		return []string{"переименована площадка"}
	case opEvFestNumbers:
		return []string{"изменены номера команд"}
	case opEvReseedCalculate:
		return []string{"пересчёт посева этапа"}
	case opEvRatingImport:
		return []string{"импорт ростера из rating.chgk.info"}
	case opEvSeedImportKSI:
		return []string{"импорт посева из КСИ"}
	case opEvSeedImportDecline:
		return []string{"отказ команды от посева"}
	case opEvGameCreate:
		return []string{"игра создана"}
	case opEvGameClear:
		return []string{"игра очищена"}
	case opEvGameDelete:
		return []string{"игра удалена"}
	case opEvPlayerOverride, opEvPlayerOverrideEdit:
		return []string{"переопределение игрока в составе"}
	case opEvFestAccess:
		return []string{"изменение доступа"}
	case opEvImport:
		return []string{"импорт схемы"}
	case opEvGameRevert:
		return []string{"откат игры к более раннему состоянию"}
	default:
		return nil
	}
}

func markLabel(m string) string {
	switch m {
	case "right":
		return "верно"
	case "wrong":
		return "неверно"
	case "":
		return "снято"
	default:
		return m
	}
}

func describeMatchUpdate(payload []byte) []string {
	var reqs []updateRequest
	if err := json.Unmarshal(payload, &reqs); err != nil {
		return []string{"редактирование матча"}
	}
	var lines []string
	var walk func(r updateRequest)
	walk = func(r updateRequest) {
		if len(r.Edits) > 0 {
			for _, e := range r.Edits {
				walk(e)
			}
			return
		}
		switch {
		case r.Finished != nil:
			if *r.Finished {
				lines = append(lines, "матч завершён")
			} else {
				lines = append(lines, "финиш матча снят")
			}
		case r.Action == actionAddShootoutTheme:
			lines = append(lines, "добавлена тема перестрелки")
		case r.Action == actionRemoveShootoutTheme:
			lines = append(lines, "удалена тема перестрелки")
		case r.Mark != nil:
			lines = append(lines, cellRefLabel(r)+": "+markLabel(*r.Mark))
		case r.Player != nil:
			lines = append(lines, themeTeamLabel(r)+": игрок → "+*r.Player)
		case r.Place != nil:
			lines = append(lines, fmt.Sprintf("команда %d: место %s", r.Team+1, trimFloat(*r.Place)))
		case r.Tiebreak != nil:
			lines = append(lines, fmt.Sprintf("команда %d: добор %d", r.Team+1, *r.Tiebreak))
		}
	}
	for _, r := range reqs {
		walk(r)
	}
	if len(lines) == 0 {
		return []string{"редактирование матча"}
	}
	return lines
}

func cellRefLabel(r updateRequest) string {
	var b strings.Builder
	if r.Theme != nil {
		fmt.Fprintf(&b, "тема %d, ", *r.Theme+1)
	}
	if r.Answer != nil {
		fmt.Fprintf(&b, "вопрос %d, ", *r.Answer+1)
	}
	fmt.Fprintf(&b, "команда %d", r.Team+1)
	return b.String()
}

func themeTeamLabel(r updateRequest) string {
	if r.Theme != nil {
		return fmt.Sprintf("тема %d, команда %d", *r.Theme+1, r.Team+1)
	}
	return fmt.Sprintf("команда %d", r.Team+1)
}

func trimFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func describeStatePatch(payload []byte) []string {
	var req gameStatePatchRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return []string{"изменение состояния игры"}
	}
	var lines []string
	for _, op := range req.Ops {
		path := renderPatchPath(op.Path)
		raw, val := patchValue(op.Value)
		switch {
		case op.Op == "remove":
			lines = append(lines, path+": снято")
		case isMarkValue(raw):
			lines = append(lines, path+": "+markLabel(raw)) // "…команда 2: неверно"
		default:
			lines = append(lines, path+" → "+val)
		}
	}
	if len(lines) == 0 {
		return []string{"изменение состояния игры"}
	}
	return lines
}

// renderPatchPath turns a JSON-pointer path into a domain label. Index segments
// take their meaning (1-based) from the preceding key: themes→тема,
// answers/questions→вопрос; a bare trailing index after a question is the team.
func renderPatchPath(path []json.RawMessage) string {
	segs := make([]string, len(path))
	nums := make([]int, len(path))
	isNum := make([]bool, len(path))
	for i, seg := range path {
		s := strings.Trim(strings.TrimSpace(string(seg)), `"`)
		segs[i] = s
		if n, err := strconv.Atoi(s); err == nil {
			nums[i], isNum[i] = n, true
		}
	}
	var parts []string
	sawQuestion := false
	for i := 0; i < len(segs); i++ {
		if isNum[i] {
			// Bare index not consumed by a key below — a team if it trails a question.
			if sawQuestion {
				parts = append(parts, fmt.Sprintf("команда %d", nums[i]+1))
			} else {
				parts = append(parts, fmt.Sprintf("#%d", nums[i]+1))
			}
			continue
		}
		label, isQuestion := patchKeyLabel(segs[i])
		if i+1 < len(segs) && isNum[i+1] {
			parts = append(parts, fmt.Sprintf("%s %d", label, nums[i+1]+1))
			if isQuestion {
				sawQuestion = true
			}
			i++
			continue
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, ", ")
}

// patchKeyLabel maps a state key to a Russian label; the bool marks question-ish
// keys so a following bare index reads as a team.
func patchKeyLabel(key string) (label string, question bool) {
	switch key {
	case "themes":
		return "тема", false
	case "tours":
		return "тур", false
	case "answers", "questions":
		return "вопрос", true
	case "teams":
		return "команда", false
	case "players", "roster":
		return "игрок", false
	default:
		return key, false
	}
}

func isMarkValue(raw string) bool {
	return raw == "right" || raw == "wrong" || raw == ""
}

// patchValue returns (rawString, display). rawString is the unquoted scalar (for
// mark detection); display is a short human rendering.
func patchValue(v json.RawMessage) (raw, display string) {
	s := strings.TrimSpace(string(v))
	if s == "" || s == "null" {
		return "", "пусто"
	}
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		var str string
		if json.Unmarshal(v, &str) == nil {
			if str == "" {
				return "", "пусто"
			}
			return str, str
		}
	}
	if len(s) > 60 {
		return s, s[:57] + "…"
	}
	return s, s
}

func summarizeTables(tables map[string]int) string {
	if len(tables) == 0 {
		return ""
	}
	names := make([]string, 0, len(tables))
	for t := range tables {
		names = append(names, t)
	}
	// stable: most-changed first, then name
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && (tables[names[j]] > tables[names[j-1]] ||
			(tables[names[j]] == tables[names[j-1]] && names[j] < names[j-1])); j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	parts := make([]string, 0, len(names))
	for _, t := range names {
		parts = append(parts, fmt.Sprintf("%s ×%d", journalTableLabel(t), tables[t]))
	}
	return strings.Join(parts, ", ")
}

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

// --- rendering --------------------------------------------------------------

var gameJournalTmpl = template.Must(template.New("game-journal").Parse(`<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>История игры · {{.GameTitle}}</title>
  <link rel="preload" href="/static/fonts/noto-sans-400.woff2" as="font" type="font/woff2" crossorigin>
  <link rel="stylesheet" href="/static/styles.css">
  <script src="/static/menu.js"></script>
</head>
<body class="public">
  <header class="public-top">
    <a class="public-back" href="/host/fest/{{.FestID}}/audit">←</a>
    <h1>История · {{.GameTitle}}</h1>
  </header>
  <main class="public-main">
    {{if .Err}}<p class="empty">{{.Err}}</p>{{end}}
    {{if .Notice}}<p class="muted">{{.Notice}}</p>{{end}}
    <section class="section">
      {{if .Groups}}
      <div class="table-scroll">
        <table class="data-table">
          <thead><tr><th>когда</th><th>кто</th><th>изменения</th><th></th></tr></thead>
          <tbody>
          {{range .Groups}}
            <tr>
              <td class="muted">{{.When}}</td>
              <td>{{if .Actor}}{{.Actor}}{{else}}<span class="muted">—</span>{{end}}</td>
              <td>
                {{range .Lines}}<div>{{.}}</div>{{end}}
                {{if .More}}<div class="muted">+ ещё {{.More}}</div>{{end}}
              </td>
              <td>
                <form method="post" action="/host/fest/{{$.FestID}}/game/{{$.GameID}}/revert"
                      onsubmit="return confirm('Откатить игру до состояния перед этим изменением? Все последующие изменения этой игры будут отменены.');">
                  <input type="hidden" name="target" value="{{.RevertTo}}">
                  <button class="btn danger" type="submit">откатить сюда</button>
                </form>
              </td>
            </tr>
          {{end}}
          </tbody>
        </table>
      </div>
      {{else}}
      <p class="empty">Изменений пока нет.</p>
      {{end}}
    </section>
  </main>
</body>
</html>`))

func (s *server) renderGameJournal(w http.ResponseWriter, r *http.Request, festID, gameID int64, errMsg, notice string) {
	var title string
	_ = s.db.QueryRowContext(r.Context(), `select coalesce(title, code) from games where id = ? and fest_id = ?`, gameID, festID).Scan(&title)
	if title == "" {
		title = fmt.Sprintf("игра %d", gameID)
	}
	groups, err := s.loadGameJournalGroups(gameID)
	if err != nil {
		http.Error(w, "journal: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = gameJournalTmpl.Execute(w, map[string]any{
		"FestID":    festID,
		"GameID":    gameID,
		"GameTitle": title,
		"Groups":    groups,
		"Err":       errMsg,
		"Notice":    notice,
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
