package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
)

// The per-game journal page lists a game's edits newest-first, each rendered as
// a human-readable description of what changed, with a "revert to before here"
// action driving the per-game derived revert (checkpoint + replay). Edits are
// grouped into the request that produced them so one host action is one row.
//
// Descriptions resolve indices to NAMES for display only (the journal itself
// stays index-based). The source differs by game type: KSI/OD read the state
// patch + participant/team names from the game's state_json; EK reads the row
// deltas and resolves the real team / match / theme from the DB.

const (
	journalPageGroups = 200
	journalMaxLines   = 16
)

type journalChange struct {
	When     string
	Actor    string
	Lines    []string
	More     int
	RevertTo int64
}

type journalOpRow struct {
	op      journalOp
	payload []byte
}

func (s *server) loadGameJournalGroups(ctx context.Context, gameID int64) ([]journalChange, error) {
	var gameType, stateJSON string
	_ = s.db.QueryRowContext(ctx, `select game_type, coalesce(state_json, '{}') from games where id = ?`, gameID).
		Scan(&gameType, &stateJSON)

	rows, err := s.db.QueryContext(ctx, `
select j.id, j.ts, j.op, j.payload, coalesce(j.request_id, ''), coalesce(u.username, '')
from journal j
left join users u on u.id = j.actor_user_id
where j.game_id = ?
order by j.id`, gameID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type acc struct {
		minID int64
		when  string
		actor string
		ops   []journalOpRow
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
			g = &acc{minID: id}
			groups[key] = g
			order = append(order, key)
		}
		if id < g.minID {
			g.minID = id
		}
		g.when = ts // ascending scan → last seen is the latest in the group
		if actor != "" {
			g.actor = actor
		}
		g.ops = append(g.ops, journalOpRow{op: journalOp(op), payload: append([]byte(nil), payload...)})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	res := s.newNameResolver(ctx, gameType, stateJSON)
	if gameType == "ek" {
		allOps := make([][]journalOpRow, 0, len(groups))
		for _, g := range groups {
			allOps = append(allOps, g.ops)
		}
		res.prepareEK(ctx, s.db, allOps)
	}

	out := make([]journalChange, 0, len(order))
	// Newest-first: iterate the ascending-built order in reverse.
	for i := len(order) - 1; i >= 0; i-- {
		g := groups[order[i]]
		lines := res.describeGroup(g.ops)
		if len(lines) == 0 {
			continue
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

// --- name resolver ----------------------------------------------------------

type ekCell struct {
	match    string
	team     string
	theme    int
	question int
	ok       bool
}

type nameResolver struct {
	gameType      string
	names         []string         // KSI participants / OD team names by index
	ekAnswer      map[int64]ekCell // answer id -> resolved cell
	ekAnswerTeam  map[int64]int64  // answer id -> team_id (pre-name-resolution)
	ekAnswerMatch map[int64]int64  // answer id -> match_id
	ekTeam        map[int64]string // team_id -> name
	ekMatch       map[int64]string // match_id -> code
}

func (s *server) newNameResolver(ctx context.Context, gameType, stateJSON string) *nameResolver {
	r := &nameResolver{gameType: gameType}
	switch gameType {
	case "ksi":
		r.names = ksiParticipantNames(stateJSON)
	case "od":
		r.names = odTeamNames(stateJSON)
	case "ek":
		r.ekAnswer = map[int64]ekCell{}
		r.ekAnswerTeam = map[int64]int64{}
		r.ekAnswerMatch = map[int64]int64{}
		r.ekTeam = map[int64]string{}
		r.ekMatch = map[int64]string{}
	}
	return r
}

func (r *nameResolver) name(i int) string {
	if i >= 0 && i < len(r.names) && strings.TrimSpace(r.names[i]) != "" {
		return strings.TrimSpace(r.names[i])
	}
	return ""
}

func ksiParticipantNames(stateJSON string) []string {
	var st struct {
		Participants []json.RawMessage `json:"participants"`
	}
	_ = json.Unmarshal([]byte(stateJSON), &st)
	out := make([]string, 0, len(st.Participants))
	for _, raw := range st.Participants {
		s := strings.TrimSpace(string(raw))
		if len(s) >= 2 && s[0] == '"' {
			var name string
			if json.Unmarshal(raw, &name) == nil {
				out = append(out, name)
				continue
			}
		}
		var obj struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(raw, &obj) == nil {
			out = append(out, obj.Name)
		} else {
			out = append(out, "")
		}
	}
	return out
}

func odTeamNames(stateJSON string) []string {
	var st struct {
		Teams []struct {
			Name string `json:"name"`
		} `json:"teams"`
	}
	_ = json.Unmarshal([]byte(stateJSON), &st)
	out := make([]string, len(st.Teams))
	for i, t := range st.Teams {
		out[i] = t.Name
	}
	return out
}

// prepareEK batch-resolves the answer / team / match identities referenced by a
// game's row-ops, so EK descriptions can show real names without per-row queries.
func (r *nameResolver) prepareEK(ctx context.Context, db rowQuerier, allOps [][]journalOpRow) {
	answerIDs := map[int64]bool{}
	teamIDs := map[int64]bool{}
	matchIDs := map[int64]bool{}
	for _, ops := range allOps {
		for _, o := range ops {
			if o.op > opRowDel {
				continue
			}
			table, row, err := decodeRowOpJSON(o.payload)
			if err != nil {
				continue
			}
			switch table {
			case "answers":
				if id, ok := rowInt(row, "id"); ok {
					answerIDs[id] = true
				}
			case "match_results":
				if id, ok := rowInt(row, "team_id"); ok {
					teamIDs[id] = true
				}
				if id, ok := rowInt(row, "match_id"); ok {
					matchIDs[id] = true
				}
			case "matches":
				if id, ok := rowInt(row, "id"); ok {
					matchIDs[id] = true
				}
			}
		}
	}
	// answers -> theme/team/match/indices
	for _, batch := range chunkIDs(keysOfInt(answerIDs), 400) {
		rows, err := db.QueryContext(ctx, `
select a.id, t.team_id, t.theme_index, a.answer_index, t.match_id
from answers a join themes t on a.theme_id = t.id
where a.id in (`+placeholders(len(batch))+`)`, idArgs(batch)...)
		if err != nil {
			continue
		}
		for rows.Next() {
			var aid, teamID, matchID int64
			var theme, question int
			if rows.Scan(&aid, &teamID, &theme, &question, &matchID) == nil {
				r.ekAnswer[aid] = ekCell{theme: theme, question: question, ok: true}
				r.ekAnswerTeam[aid] = teamID
				r.ekAnswerMatch[aid] = matchID
				teamIDs[teamID] = true
				matchIDs[matchID] = true
			}
		}
		rows.Close()
	}
	for _, batch := range chunkIDs(keysOfInt(teamIDs), 400) {
		rows, err := db.QueryContext(ctx, `select id, name from teams where id in (`+placeholders(len(batch))+`)`, idArgs(batch)...)
		if err != nil {
			continue
		}
		for rows.Next() {
			var id int64
			var name string
			if rows.Scan(&id, &name) == nil {
				r.ekTeam[id] = name
			}
		}
		rows.Close()
	}
	for _, batch := range chunkIDs(keysOfInt(matchIDs), 400) {
		rows, err := db.QueryContext(ctx, `select id, code from matches where id in (`+placeholders(len(batch))+`)`, idArgs(batch)...)
		if err != nil {
			continue
		}
		for rows.Next() {
			var id int64
			var code string
			if rows.Scan(&id, &code) == nil {
				r.ekMatch[id] = code
			}
		}
		rows.Close()
	}
	// Fill team/match names onto each answer cell.
	for aid, cell := range r.ekAnswer {
		cell.team = r.ekTeam[r.ekAnswerTeam[aid]]
		cell.match = r.ekMatch[r.ekAnswerMatch[aid]]
		r.ekAnswer[aid] = cell
	}
}

// --- per-group description --------------------------------------------------

func (r *nameResolver) describeGroup(ops []journalOpRow) []string {
	switch r.gameType {
	case "ek":
		return r.describeEK(ops)
	case "od":
		return r.describeStatePatchGroup(ops, r.odPatchLine)
	case "ksi":
		return r.describeStatePatchGroup(ops, r.ksiPatchLine)
	default:
		var lines []string
		for _, o := range ops {
			if o.op >= opEvImport {
				lines = append(lines, describeEvent(o.op, o.payload)...)
			}
		}
		return lines
	}
}

// describeStatePatchGroup renders the group's game-state patch ops via lineFn,
// and falls back to coarse event labels for non-patch events.
func (r *nameResolver) describeStatePatchGroup(ops []journalOpRow, lineFn func(op gameStatePatchOp) string) []string {
	var lines []string
	for _, o := range ops {
		switch {
		case o.op == opEvGameStatePatch:
			var req gameStatePatchRequest
			if json.Unmarshal(o.payload, &req) != nil {
				lines = append(lines, "изменение состояния игры")
				continue
			}
			for _, p := range req.Ops {
				if l := lineFn(p); l != "" {
					lines = append(lines, l)
				}
			}
		case o.op == opEvGameState:
			lines = append(lines, "состояние игры заменено целиком")
		case o.op >= opEvImport && o.op != opEvMatchUpdate:
			lines = append(lines, describeEvent(o.op, o.payload)...)
		}
	}
	return lines
}

// ksiPatchLine renders one KSI state-patch op. State shape:
// themes[t].answers[player][question] = mark; participants[i] = name.
func (r *nameResolver) ksiPatchLine(op gameStatePatchOp) string {
	segs := patchSegs(op.Path)
	switch {
	case len(segs) == 5 && segs[0].s == "themes" && segs[2].s == "answers" &&
		segs[1].num && segs[3].num && segs[4].num:
		who := r.name(segs[3].n)
		if who == "" {
			who = fmt.Sprintf("участник %d", segs[3].n+1)
		}
		return fmt.Sprintf("тема %d, %s, вопрос %d: %s", segs[0+1].n+1, who, segs[4].n+1, patchMark(op.Value))
	case len(segs) == 2 && segs[0].s == "participants" && segs[1].num:
		_, name := patchValue(op.Value)
		return fmt.Sprintf("переименование участника %d → %s", segs[1].n+1, name)
	case len(segs) >= 1 && segs[0].s == "finished":
		return "завершение матча"
	case len(segs) >= 1 && segs[0].s == "declined":
		return "отказ от участия"
	default:
		return genericPatchLine(op)
	}
}

// odPatchLine renders one OD state-patch op. State shape:
// entries[question][teamRow] = value (or entries[question] = whole row).
func (r *nameResolver) odPatchLine(op gameStatePatchOp) string {
	segs := patchSegs(op.Path)
	switch {
	case len(segs) == 3 && segs[0].s == "entries" && segs[1].num && segs[2].num:
		who := r.name(segs[2].n)
		if who == "" {
			who = fmt.Sprintf("команда %d", segs[2].n+1)
		}
		_, val := patchValue(op.Value)
		return fmt.Sprintf("%s, вопрос %d → %s", who, segs[1].n+1, val)
	case len(segs) == 2 && segs[0].s == "entries" && segs[1].num:
		return fmt.Sprintf("вопрос %d изменён", segs[1].n+1)
	case len(segs) == 1 && segs[0].s == "entries":
		return "ответы изменены"
	case len(segs) == 2 && segs[0].s == "completed" && segs[1].num:
		_, val := patchValue(op.Value)
		return fmt.Sprintf("вопрос %d: готовность → %s", segs[1].n+1, val)
	case len(segs) >= 1 && segs[0].s == "shootoutRounds":
		return "перестрелка"
	default:
		return genericPatchLine(op)
	}
}

func (r *nameResolver) describeEK(ops []journalOpRow) []string {
	var lines []string
	for _, o := range ops {
		if o.op > opRowDel {
			continue
		}
		table, row, err := decodeRowOpJSON(o.payload)
		if err != nil {
			continue
		}
		switch table {
		case "answers":
			id, _ := rowInt(row, "id")
			cell := r.ekAnswer[id]
			mark := markLabel(rowStr(row, "mark"))
			lines = append(lines, fmt.Sprintf("%s%s, тема %d, вопрос %d: %s",
				matchPrefix(cell.match), teamOr(cell.team), cell.theme+1, cell.question+1, mark))
		case "match_results":
			teamID, _ := rowInt(row, "team_id")
			matchID, _ := rowInt(row, "match_id")
			if rank, ok := rowInt(row, "rank"); ok {
				lines = append(lines, fmt.Sprintf("%s%s: место %d",
					matchPrefix(r.ekMatch[matchID]), teamOr(r.ekTeam[teamID]), rank))
			}
		case "themes":
			lines = append(lines, "изменена тема / состав")
		case "matches":
			if st := rowStr(row, "status"); st != "" {
				label := "матч открыт заново"
				if st == "finished" {
					label = "матч завершён"
				}
				lines = append(lines, matchPrefix(r.ekMatch[rowInt64(row, "id")])+label)
			}
		}
	}
	if len(lines) == 0 {
		// No row-level detail (e.g. a coarse import/reseed) — use the event label.
		for _, o := range ops {
			if o.op >= opEvImport {
				if l := describeEvent(o.op, o.payload); len(l) > 0 {
					lines = append(lines, l...)
				} else if o.op == opEvMatchUpdate {
					lines = append(lines, "редактирование матча")
				}
			}
		}
	}
	return lines
}

func matchPrefix(code string) string {
	if code == "" {
		return ""
	}
	return "матч " + code + ", "
}

func teamOr(name string) string {
	if name == "" {
		return "команда"
	}
	return name
}

// --- patch helpers ----------------------------------------------------------

type patchSeg struct {
	s   string
	n   int
	num bool
}

func patchSegs(path []json.RawMessage) []patchSeg {
	out := make([]patchSeg, len(path))
	for i, seg := range path {
		s := strings.Trim(strings.TrimSpace(string(seg)), `"`)
		out[i].s = s
		if n, err := strconv.Atoi(s); err == nil {
			out[i].n, out[i].num = n, true
		}
	}
	return out
}

func genericPatchLine(op gameStatePatchOp) string {
	parts := make([]string, 0, len(op.Path))
	for _, seg := range op.Path {
		parts = append(parts, strings.Trim(strings.TrimSpace(string(seg)), `"`))
	}
	_, val := patchValue(op.Value)
	if op.Op == "remove" {
		return strings.Join(parts, " · ") + ": снято"
	}
	return strings.Join(parts, " · ") + " → " + val
}

func patchMark(v json.RawMessage) string {
	raw, _ := patchValue(v)
	return markLabel(raw)
}

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

// describeEvent renders coarse, non-game-typed events (imports, reseeds, etc.).
func describeEvent(op journalOp, payload []byte) []string {
	switch op {
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
	case opEvFestNumbers:
		return []string{"изменены номера команд"}
	case opEvVenuesUpdate, opEvMatchVenue:
		return []string{"изменена площадка"}
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

// --- small row/id helpers ---------------------------------------------------

func rowInt(m map[string]any, key string) (int64, bool) {
	switch v := m[key].(type) {
	case int64:
		return v, true
	case float64:
		return int64(v), true
	}
	return 0, false
}

func rowInt64(m map[string]any, key string) int64 {
	n, _ := rowInt(m, key)
	return n
}

func rowStr(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func keysOfInt(m map[int64]bool) []int64 {
	out := make([]int64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func chunkIDs(ids []int64, size int) [][]int64 {
	var out [][]int64
	for i := 0; i < len(ids); i += size {
		j := i + size
		if j > len(ids) {
			j = len(ids)
		}
		out = append(out, ids[i:j])
	}
	return out
}

func placeholders(n int) string {
	if n == 0 {
		return "null"
	}
	return strings.TrimRight(strings.Repeat("?,", n), ",")
}

func idArgs(ids []int64) []any {
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}

func formatJournalTime(ts string) string {
	s := strings.Replace(ts, "T", " ", 1)
	if i := strings.IndexAny(s, ".Z"); i >= 0 {
		s = s[:i]
	}
	return s
}

// --- HTTP -------------------------------------------------------------------

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
	groups, err := s.loadGameJournalGroups(r.Context(), gameID)
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
