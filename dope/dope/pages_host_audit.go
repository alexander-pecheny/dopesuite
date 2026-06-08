package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// auditRevertScanRows is a hard ceiling on how many recent audit rows the page
// reads before grouping. One edit is one request (one group); pagination breaks
// out of the scan as soon as it has filled the requested page, so this only
// caps pathological deep pages.
const auditRevertScanRows = 5000

// auditPageSize is how many change groups (edits) one audit page shows.
const auditPageSize = 100

// auditDetailMaxLines caps how many individual changes one group spells out
// before collapsing the rest into a "+N more" note, so a 60-cell bulk edit
// doesn't blow up the page.
const auditDetailMaxLines = 24

// auditTableLabels maps audited table names to short Russian labels for the
// change summary. Unlisted tables fall back to their raw name.
var auditTableLabels = map[string]string{
	"answers":        "ответы",
	"themes":         "темы и игроки",
	"match_results":  "результаты боёв",
	"matches":        "бои",
	"match_slots":    "слоты сетки",
	"reseed_entries": "пересев",
	"stages":         "этапы",
	"venues":         "площадки",
	"games":          "игры",
	"fest_teams":     "команды феста",
	"fest_players":   "игроки феста",
}

type auditChangeGroup struct {
	MinID       int64
	MaxID       int64
	When        string
	Actor       string
	Summary     string
	Details     []string // human-readable per-cell / per-field changes
	MoreDetails int      // changes beyond auditDetailMaxLines, summarised as "+N"
	Count       int
	IsNewest    bool
	RevertTo    int64 // target id for "revert to the state before this group"
}

type hostFestAuditData struct {
	Fest      hostMyFest
	Groups    []auditChangeGroup
	Error     string
	Notice    string
	PageHuman int // 1-based page number for display
	HasPrev   bool
	HasNext   bool
	PrevPage  int
	NextPage  int
}

var hostFestAuditTemplate = template.Must(template.New("hostAudit").Parse(`<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Fest.Title}} · история изменений</title>
  <link rel="preload" href="/static/fonts/noto-sans-400.woff2" as="font" type="font/woff2" crossorigin>
  <link rel="stylesheet" href="/static/styles.css">
  <script src="/static/appearance.js"></script>
  <style>
    .audit-changes { margin: .25rem 0 0; padding-left: 1.1rem; font-size: .85em; line-height: 1.35; }
    .audit-changes li { color: var(--muted, #555); }
    .audit-more { display: inline-block; margin-top: .15rem; font-size: .85em; }
    .audit-pager { display: flex; gap: .75rem; align-items: center; justify-content: center; margin: 1.25rem 0 .5rem; }
    .audit-pager .audit-page-num { font-size: .9em; }
  </style>
</head>
<body class="public">
  <header class="public-top">
    <a class="public-back" href="/host/fest/{{.Fest.Ref}}">←</a>
    <h1>История изменений</h1>
  </header>
  <main class="public-main">
    {{if .Error}}<p class="empty">{{.Error}}</p>{{end}}
    {{if .Notice}}<p class="muted">{{.Notice}}</p>{{end}}
    {{if not .Groups}}
    <p class="empty">Изменений пока нет. Сюда попадают правки, сделанные после обновления, в котором появилась эта страница.</p>
    {{else}}
    <p class="muted">Откат вернёт состояние феста к тому, что было до выбранного изменения: само изменение и всё, что сделано после него, будет отменено. Откат тоже записывается в историю, поэтому его можно отменить таким же образом.</p>
    <table class="data-table audit-table">
      <thead><tr><th>Когда</th><th>Кто</th><th>Что изменилось</th><th></th></tr></thead>
      <tbody>
        {{range .Groups}}
        <tr>
          <td class="audit-when">{{.When}}</td>
          <td class="audit-actor">{{.Actor}}</td>
          <td class="audit-summary">{{.Summary}}{{if .IsNewest}} <span class="muted">(последнее)</span>{{end}}
            {{if .Details}}<ul class="audit-changes">{{range .Details}}<li>{{.}}</li>{{end}}</ul>{{end}}
            {{if .MoreDetails}}<span class="audit-more muted">…и ещё {{.MoreDetails}} изменений</span>{{end}}
          </td>
          <td class="audit-action">
            <form method="post" action="/host/fest/{{$.Fest.Ref}}/audit/revert">
              <input type="hidden" name="target" value="{{.RevertTo}}">
              <button class="btn danger" type="submit" onclick="return confirm('Откатить состояние феста к моменту до этого изменения? Это изменение и все последующие будут отменены.');">Откатить сюда</button>
            </form>
          </td>
        </tr>
        {{end}}
      </tbody>
    </table>
    {{if or .HasPrev .HasNext}}
    <nav class="audit-pager">
      {{if .HasPrev}}<a class="btn" href="?page={{.PrevPage}}">← Новее</a>{{else}}<span class="btn" aria-disabled="true">← Новее</span>{{end}}
      <span class="audit-page-num muted">страница {{.PageHuman}}</span>
      {{if .HasNext}}<a class="btn" href="?page={{.NextPage}}">Старее →</a>{{else}}<span class="btn" aria-disabled="true">Старее →</span>{{end}}
    </nav>
    {{end}}
    {{end}}
  </main>
</body>
</html>`))

func (s *server) renderHostFestAudit(w http.ResponseWriter, r *http.Request, festID int64, errMsg, notice string) {
	fest, err := s.loadHostFestHeader(r.Context(), festID)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	page := 0
	if v := strings.TrimSpace(r.URL.Query().Get("page")); v != "" {
		if p, perr := strconv.Atoi(v); perr == nil && p > 0 {
			page = p
		}
	}
	groups, hasPrev, hasNext, err := s.loadFestAuditPage(r.Context(), festID, page)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = hostFestAuditTemplate.Execute(w, hostFestAuditData{
		Fest:      fest,
		Groups:    groups,
		Error:     errMsg,
		Notice:    notice,
		PageHuman: page + 1,
		HasPrev:   hasPrev,
		HasNext:   hasNext,
		PrevPage:  page - 1,
		NextPage:  page + 1,
	})
}

// loadFestAuditPage reads the recent fest-scoped audit rows, folds them into
// per-request change groups (newest first), and returns the requested page of
// auditPageSize groups together with whether older/newer pages exist. page is
// 0-based.
func (s *server) loadFestAuditPage(ctx context.Context, festID int64, page int) (groups []auditChangeGroup, hasPrev, hasNext bool, err error) {
	if page < 0 {
		page = 0
	}
	rows, err := s.db.QueryContext(ctx, `
select a.id, a.ts, a.table_name, coalesce(a.request_id, ''), coalesce(u.username, ''),
       dope_unz(a.before_json), dope_unz(a.after_json)
from audit_log a
left join users u on u.id = a.actor_user_id
where a.fest_id = ?
order by a.id desc
limit ?`, festID, auditRevertScanRows)
	if err != nil {
		return nil, false, false, err
	}
	defer rows.Close()

	// Fold enough groups to cover the requested page; `more` tells us a further
	// (older) group exists just past it, which is exactly our "next page" signal.
	res := newAuditMatchResolver(ctx, s.db)
	all, more, err := foldAuditGroups(rows, (page+1)*auditPageSize, res)
	if err != nil {
		return nil, false, false, err
	}
	hasPrev = page > 0
	hasNext = more
	start := page * auditPageSize
	if start >= len(all) {
		return nil, hasPrev, false, nil
	}
	end := start + auditPageSize
	if end > len(all) {
		end = len(all)
	}
	groups = all[start:end]
	if page == 0 && len(groups) > 0 {
		groups[0].IsNewest = true
	}
	return groups, hasPrev, hasNext, nil
}

// foldAuditGroups reads id-descending audit rows and folds runs of same-request
// rows into change groups (rows of one request are contiguous in the scan),
// newest first. It returns at most `limit` groups; `more` is true when at least
// one further group exists beyond the limit (so a caller can offer a next page).
func foldAuditGroups(rows *sql.Rows, limit int, res *auditMatchResolver) (groups []auditChangeGroup, more bool, err error) {
	var cur *auditChangeGroup
	var curKey string
	var tableCounts map[string]int
	flush := func() {
		if cur == nil {
			return
		}
		cur.Summary = summarizeAuditTables(tableCounts)
		cur.RevertTo = cur.MinID - 1
		groups = append(groups, *cur)
		cur = nil
	}
	// addDetail appends human-readable change lines to the current group, capped
	// so a huge bulk edit collapses the tail into a "+N" note.
	addDetail := func(lines []string) {
		for _, ln := range lines {
			if len(cur.Details) >= auditDetailMaxLines {
				cur.MoreDetails++
				continue
			}
			cur.Details = append(cur.Details, ln)
		}
	}
	for rows.Next() {
		var (
			id         int64
			ts         string
			table      string
			requestID  string
			username   string
			beforeJSON sql.NullString
			afterJSON  sql.NullString
		)
		if err := rows.Scan(&id, &ts, &table, &requestID, &username, &beforeJSON, &afterJSON); err != nil {
			return nil, false, err
		}
		// Group rows of the same request; a blank request_id is its own group.
		key := requestID
		if key == "" {
			key = fmt.Sprintf("id:%d", id)
		}
		if cur == nil || key != curKey {
			flush()
			if len(groups) >= limit {
				// We just saw the first row of the (limit+1)th group: there is an
				// older page, but it's out of this window — stop here.
				more = true
				break
			}
			actor := strings.TrimSpace(username)
			if actor == "" {
				actor = "система"
			}
			cur = &auditChangeGroup{
				MinID: id,
				MaxID: id,
				When:  formatAuditTime(ts),
				Actor: actor,
			}
			curKey = key
			tableCounts = map[string]int{}
		}
		if id > cur.MaxID {
			cur.MaxID = id
		}
		if id < cur.MinID {
			cur.MinID = id
		}
		cur.Count++
		tableCounts[table]++
		switch table {
		case "games":
			addDetail(gamesRowChanges(res, beforeJSON, afterJSON))
		case "matches":
			addDetail(matchRowChanges(res, beforeJSON, afterJSON))
		case "answers":
			addDetail(answerRowChanges(res, beforeJSON, afterJSON))
		}
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	flush()
	return groups, more, nil
}

func summarizeAuditTables(counts map[string]int) string {
	if len(counts) == 0 {
		return "—"
	}
	type tc struct {
		label string
		count int
	}
	items := make([]tc, 0, len(counts))
	for table, count := range counts {
		label := auditTableLabels[table]
		if label == "" {
			label = table
		}
		items = append(items, tc{label: label, count: count})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].count != items[j].count {
			return items[i].count > items[j].count
		}
		return items[i].label < items[j].label
	})
	parts := make([]string, 0, len(items))
	for _, it := range items {
		parts = append(parts, fmt.Sprintf("%s ×%d", it.label, it.count))
	}
	return strings.Join(parts, ", ")
}

// leafChange is one differing JSON leaf between a before/after pair, keyed by
// its path (string map keys and int slice indices interleaved).
type leafChange struct {
	path []any
	old  any
	new  any
}

// gamesRowChanges diffs the state_json of a games-row audit entry and renders the
// changed leaves. KSI/SI answer-grid cells (themes[t].answers[team][q]) become
// "Тема t · Вопрос q · «команда»: было → стало"; the finished flag and any other
// leaf fall back to a compact rendering. Returns nil when neither side parses.
func gamesRowChanges(res *auditMatchResolver, before, after sql.NullString) []string {
	beforeRow := auditRowMap(before)
	afterRow := auditRowMap(after)
	beforeState := gameStateFromRow(beforeRow)
	afterState := gameStateFromRow(afterRow)
	if beforeState == nil && afterState == nil {
		return nil
	}
	names := stateParticipantNames(afterState)
	if names == nil {
		names = stateParticipantNames(beforeState)
	}
	// Prefix every line with the game it belongs to, so a mixed group (e.g. a
	// revert touching several games) stays readable. title/code are unchanged on
	// a state_json edit and thus dropped from the diff; fall back to a live title
	// lookup by id before settling for "игра #id".
	label := gameAuditLabel(afterRow, beforeRow)
	if res != nil && strings.HasPrefix(label, "игра #") {
		id, ok := pathIndex(afterRow["id"])
		if !ok {
			id, ok = pathIndex(beforeRow["id"])
		}
		if ok {
			if t := strings.TrimSpace(res.gameTitle(int64(id))); t != "" {
				label = t
			}
		}
	}
	var leaves []leafChange
	diffJSONLeaves(nil, beforeState, afterState, &leaves)
	out := make([]string, 0, len(leaves))
	for _, lc := range leaves {
		line := formatGameChange(lc, names)
		if label != "" {
			line = label + " — " + line
		}
		out = append(out, line)
	}
	return out
}

// gameStateFromRow parses the state_json field of a games-row audit map into a
// decoded JSON value (nil when absent or unparseable).
func gameStateFromRow(row map[string]any) any {
	if row == nil {
		return nil
	}
	stateStr, _ := row["state_json"].(string)
	if stateStr == "" {
		return nil
	}
	var state any
	if err := json.Unmarshal([]byte(stateStr), &state); err != nil {
		return nil
	}
	return state
}

// gameAuditLabel picks a short human label for a games-row audit entry: the game
// title, falling back to its code, then "игра #id". Rows are tried in order
// (after-state first), so a delete still resolves a name from the before-row.
func gameAuditLabel(rows ...map[string]any) string {
	for _, row := range rows {
		if title, _ := row["title"].(string); strings.TrimSpace(title) != "" {
			return strings.TrimSpace(title)
		}
	}
	for _, row := range rows {
		if code, _ := row["code"].(string); strings.TrimSpace(code) != "" {
			return strings.TrimSpace(code)
		}
	}
	for _, row := range rows {
		if id, ok := pathIndex(row["id"]); ok {
			return fmt.Sprintf("игра #%d", id)
		}
	}
	return ""
}

func stateParticipantNames(state any) []string {
	m, ok := state.(map[string]any)
	if !ok {
		return nil
	}
	arr, ok := m["participants"].([]any)
	if !ok {
		return nil
	}
	names := make([]string, len(arr))
	for i, v := range arr {
		switch p := v.(type) {
		case string: // legacy ["name", ...]
			names[i] = p
		case map[string]any: // current [{number,name}, ...]
			names[i], _ = p["name"].(string)
		}
	}
	return names
}

// diffJSONLeaves walks two decoded JSON values in lockstep and records every
// differing leaf into out. Maps recurse by (sorted) key, slices by index; a
// scalar change or a container/scalar shape change is recorded as one leaf.
func diffJSONLeaves(prefix []any, before, after any, out *[]leafChange) {
	if reflect.DeepEqual(before, after) {
		return
	}
	switch b := before.(type) {
	case map[string]any:
		if a, ok := after.(map[string]any); ok {
			seen := map[string]struct{}{}
			keys := make([]string, 0, len(b)+len(a))
			for k := range b {
				if _, dup := seen[k]; !dup {
					seen[k] = struct{}{}
					keys = append(keys, k)
				}
			}
			for k := range a {
				if _, dup := seen[k]; !dup {
					seen[k] = struct{}{}
					keys = append(keys, k)
				}
			}
			sort.Strings(keys)
			for _, k := range keys {
				diffJSONLeaves(append(append([]any{}, prefix...), k), b[k], a[k], out)
			}
			return
		}
	case []any:
		if a, ok := after.([]any); ok {
			n := len(b)
			if len(a) > n {
				n = len(a)
			}
			for i := 0; i < n; i++ {
				var bv, av any
				if i < len(b) {
					bv = b[i]
				}
				if i < len(a) {
					av = a[i]
				}
				diffJSONLeaves(append(append([]any{}, prefix...), i), bv, av, out)
			}
			return
		}
	}
	*out = append(*out, leafChange{path: append([]any{}, prefix...), old: before, new: after})
}

func formatGameChange(lc leafChange, names []string) string {
	// KSI/SI answer cell: themes[t].answers[team][q].
	if len(lc.path) == 5 {
		if k0, _ := lc.path[0].(string); k0 == "themes" {
			if k2, _ := lc.path[2].(string); k2 == "answers" {
				t, ok1 := pathIndex(lc.path[1])
				team, ok2 := pathIndex(lc.path[3])
				q, ok3 := pathIndex(lc.path[4])
				if ok1 && ok2 && ok3 {
					return fmt.Sprintf("Тема %d · Вопрос %d · %s: %s → %s",
						t+1, q+1, participantLabel(names, team), answerLabel(lc.old), answerLabel(lc.new))
				}
			}
		}
	}
	if len(lc.path) == 1 {
		if k0, _ := lc.path[0].(string); k0 == "finished" {
			if truthyJSON(lc.new) {
				return "Игра отмечена завершённой"
			}
			return "С игры снята отметка о завершении"
		}
	}
	return fmt.Sprintf("%s: %s → %s", joinAuditPath(lc.path), leafLabel(lc.old), leafLabel(lc.new))
}

// matchRowChanges renders the meaningful changes of a matches-row audit entry —
// primarily the finished/active status toggle (the EK "tick").
func matchRowChanges(res *auditMatchResolver, before, after sql.NullString) []string {
	a := auditRowMap(after)
	if a == nil {
		return nil
	}
	b := auditRowMap(before)
	title, _ := a["title"].(string)
	if title == "" {
		title, _ = b["title"].(string)
	}
	label := "Бой"
	if title != "" {
		label = "Бой «" + title + "»"
	} else if res != nil {
		// title is unchanged → dropped from the diff; resolve it live by id.
		if id, ok := pathIndex(a["id"]); ok {
			label = res.matchLabel(int64(id))
		}
	}
	var out []string
	if b != nil {
		bs, _ := b["status"].(string)
		as, _ := a["status"].(string)
		if bs != as {
			switch as {
			case "finished":
				out = append(out, label+": отмечен законченным")
			case "active":
				out = append(out, label+": снята отметка о завершении")
			default:
				out = append(out, fmt.Sprintf("%s: статус → %s", label, as))
			}
		}
	}
	return out
}

// auditMatchResolver resolves match/team labels for EK (bracket) audit rows,
// which reference rows by id rather than carrying a title. Lookups are cached
// (including misses) so one page of answer edits costs only a handful of
// queries. Labels reflect current DB state, which is fine for display.
type auditMatchResolver struct {
	ctx        context.Context
	db         *sql.DB
	themes     map[int64]*themeRef
	matchTitle map[int64]string
	teamName   map[int64]string
	gameTitles map[int64]string
	answerCell map[int64]*answerCellRef
}

type answerCellRef struct {
	themeID     int64
	answerIndex int64
}

type themeRef struct {
	matchID    int64
	teamID     int64
	kind       string
	themeIndex int64
}

func newAuditMatchResolver(ctx context.Context, db *sql.DB) *auditMatchResolver {
	return &auditMatchResolver{
		ctx:        ctx,
		db:         db,
		themes:     map[int64]*themeRef{},
		matchTitle: map[int64]string{},
		teamName:   map[int64]string{},
		gameTitles: map[int64]string{},
		answerCell: map[int64]*answerCellRef{},
	}
}

// gameTitle resolves a game's current title by id. UPDATE audit snapshots store
// only changed columns, so a state_json edit drops the (unchanged) title — we
// look it up live for the line label. Returns "" when the game is gone.
func (r *auditMatchResolver) gameTitle(id int64) string {
	if t, ok := r.gameTitles[id]; ok {
		return t
	}
	var title string
	if err := r.db.QueryRowContext(r.ctx, `select title from games where id = ?`, id).Scan(&title); err != nil {
		title = ""
	}
	r.gameTitles[id] = title
	return title
}

// cellRef resolves an answer row's (theme_id, answer_index) by its id. Those
// columns are immutable identity and get dropped from a mark-only UPDATE diff,
// so we recover them live (current value == historical value). Returns nil when
// the answer row no longer exists.
func (r *auditMatchResolver) cellRef(id int64) *answerCellRef {
	if ref, ok := r.answerCell[id]; ok {
		return ref
	}
	var ref answerCellRef
	if err := r.db.QueryRowContext(r.ctx,
		`select theme_id, answer_index from answers where id = ?`, id).
		Scan(&ref.themeID, &ref.answerIndex); err != nil {
		r.answerCell[id] = nil
		return nil
	}
	r.answerCell[id] = &ref
	return &ref
}

func (r *auditMatchResolver) theme(id int64) *themeRef {
	if ref, ok := r.themes[id]; ok {
		return ref
	}
	var ref themeRef
	err := r.db.QueryRowContext(r.ctx,
		`select match_id, team_id, kind, theme_index from themes where id = ?`, id).
		Scan(&ref.matchID, &ref.teamID, &ref.kind, &ref.themeIndex)
	if err != nil {
		r.themes[id] = nil
		return nil
	}
	r.themes[id] = &ref
	return &ref
}

func (r *auditMatchResolver) matchLabel(matchID int64) string {
	title, ok := r.matchTitle[matchID]
	if !ok {
		if err := r.db.QueryRowContext(r.ctx, `select title from matches where id = ?`, matchID).Scan(&title); err != nil {
			title = ""
		}
		r.matchTitle[matchID] = title
	}
	if strings.TrimSpace(title) != "" {
		return "Бой «" + title + "»"
	}
	return fmt.Sprintf("Бой #%d", matchID)
}

func (r *auditMatchResolver) team(teamID int64) string {
	if name, ok := r.teamName[teamID]; ok {
		return name
	}
	var name string
	if err := r.db.QueryRowContext(r.ctx, `select name from teams where id = ?`, teamID).Scan(&name); err != nil {
		name = ""
	}
	r.teamName[teamID] = name
	return name
}

// cellLabel builds the "Бой «A» · «Команда» · тема 2 · вопрос 3" prefix for one
// EK answer cell, resolving the match/team via the theme id.
func (r *auditMatchResolver) cellLabel(themeID int64, answerIndex int) string {
	th := r.theme(themeID)
	if th == nil {
		return fmt.Sprintf("вопрос %d", answerIndex+1)
	}
	parts := []string{r.matchLabel(th.matchID)}
	if name := strings.TrimSpace(r.team(th.teamID)); name != "" {
		parts = append(parts, "«"+name+"»")
	}
	if th.kind == "shootout" {
		parts = append(parts, "перестрелка")
	} else {
		parts = append(parts, fmt.Sprintf("тема %d", th.themeIndex+1))
	}
	parts = append(parts, fmt.Sprintf("вопрос %d", answerIndex+1))
	return strings.Join(parts, " · ")
}

// answerRowChanges renders one EK answer-cell change, leading with the match it
// belongs to (e.g. "Бой «A» · «Команда» · тема 2 · вопрос 3: пусто → верно").
func answerRowChanges(res *auditMatchResolver, before, after sql.NullString) []string {
	if res == nil {
		return nil
	}
	b := auditRowMap(before)
	a := auditRowMap(after)
	row := a
	if row == nil {
		row = b
	}
	if row == nil {
		return nil
	}
	var oldMark, newMark any
	if b != nil {
		oldMark = b["mark"]
	}
	if a != nil {
		newMark = a["mark"]
	}
	if reflect.DeepEqual(oldMark, newMark) {
		return nil
	}
	// theme_id / answer_index are dropped from a mark-only UPDATE diff; recover
	// them from the row JSON (legacy full snapshots) or live by the answer id.
	themeID, ok := pathIndex(row["theme_id"])
	answerIndex, hasIdx := pathIndex(row["answer_index"])
	if (!ok || !hasIdx) && res != nil {
		if id, hasID := pathIndex(row["id"]); hasID {
			if ref := res.cellRef(int64(id)); ref != nil {
				themeID, ok = int(ref.themeID), true
				answerIndex = int(ref.answerIndex)
			}
		}
	}
	if !ok {
		return nil
	}
	return []string{fmt.Sprintf("%s: %s → %s",
		res.cellLabel(int64(themeID), answerIndex), answerLabel(oldMark), answerLabel(newMark))}
}

func auditRowMap(raw sql.NullString) map[string]any {
	if !raw.Valid || raw.String == "" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw.String), &m); err != nil {
		return nil
	}
	return m
}

func pathIndex(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case float64:
		return int(n), true
	}
	return 0, false
}

func participantLabel(names []string, idx int) string {
	if idx >= 0 && idx < len(names) && strings.TrimSpace(names[idx]) != "" {
		return "«" + names[idx] + "»"
	}
	return fmt.Sprintf("команда %d", idx+1)
}

func answerLabel(v any) string {
	switch s := v.(type) {
	case string:
		switch s {
		case "right":
			return "верно"
		case "wrong":
			return "неверно"
		case "":
			return "пусто"
		}
		return s
	case nil:
		return "пусто"
	}
	return leafLabel(v)
}

func leafLabel(v any) string {
	switch t := v.(type) {
	case nil:
		return "пусто"
	case string:
		if t == "" {
			return "пусто"
		}
		return t
	case bool:
		if t {
			return "да"
		}
		return "нет"
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'g', -1, 64)
	case map[string]any, []any:
		return "(изменено)"
	}
	return fmt.Sprint(v)
}

func joinAuditPath(path []any) string {
	var b strings.Builder
	for _, p := range path {
		switch v := p.(type) {
		case string:
			if b.Len() > 0 {
				b.WriteByte('.')
			}
			b.WriteString(v)
		case int:
			b.WriteString("[" + strconv.Itoa(v) + "]")
		case float64:
			b.WriteString("[" + strconv.Itoa(int(v)) + "]")
		}
	}
	return b.String()
}

func truthyJSON(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t != "" && t != "false"
	case float64:
		return t != 0
	}
	return v != nil
}

func formatAuditTime(ts string) string {
	// Stored as RFC3339-ish UTC ("2026-06-04T12:34:56.789Z"); show a compact
	// "YYYY-MM-DD HH:MM:SS" without forcing a timezone library.
	t := strings.TrimSuffix(ts, "Z")
	t = strings.Replace(t, "T", " ", 1)
	if i := strings.IndexByte(t, '.'); i >= 0 {
		t = t[:i]
	}
	return t
}

func (s *server) handleHostFestAuditRevert(w http.ResponseWriter, r *http.Request, festID int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	target, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("target")), 10, 64)
	if err != nil || target < 0 {
		http.Error(w, "bad target", http.StatusBadRequest)
		return
	}
	count, revision, err := s.revertFestToAudit(r.Context(), festID, target)
	if err != nil {
		s.renderHostFestAudit(w, r, festID, "Не удалось откатить: "+err.Error(), "")
		return
	}
	// Force every connected host/viewer to refetch the reverted state.
	if revision > 0 {
		s.broadcastState(festID, fmt.Sprintf("fest:%d", festID), revision, []byte("{}"))
	}
	notice := "Откат выполнен: ничего не изменилось."
	if count > 0 {
		notice = fmt.Sprintf("Откат выполнен: отменено изменений — %d.", count)
	}
	s.renderHostFestAudit(w, r, festID, "", notice)
}

type auditRevertEntry struct {
	table      string
	op         string
	beforeJSON sql.NullString
	afterJSON  sql.NullString
}

// revertFestToAudit reverse-applies every fest-scoped audit row newer than
// targetID (id > targetID), newest first, restoring the fest's audited tables to
// the state they had immediately after audit row targetID. The reversal runs in
// one write transaction with deferred FK checks so intermediate steps may
// momentarily violate constraints as long as the final state is consistent. The
// reversal mutations are themselves audited (so the revert is undoable). Returns
// the number of rows reversed and the new fest revision.
func (s *server) revertFestToAudit(ctx context.Context, festID, targetID int64) (int, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.beginWriteTx(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `PRAGMA defer_foreign_keys = ON`); err != nil {
		return 0, 0, err
	}

	rows, err := tx.QueryContext(ctx, `
select table_name, op, dope_unz(before_json), dope_unz(after_json)
from audit_log
where fest_id = ? and id > ?
order by id desc`, festID, targetID)
	if err != nil {
		return 0, 0, err
	}
	var entries []auditRevertEntry
	for rows.Next() {
		var e auditRevertEntry
		if err := rows.Scan(&e.table, &e.op, &e.beforeJSON, &e.afterJSON); err != nil {
			_ = rows.Close()
			return 0, 0, err
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, 0, err
	}
	if len(entries) == 0 {
		// Nothing to revert; don't bump a revision or commit a no-op.
		return 0, 0, nil
	}

	pkCache := map[string][]string{}
	tablePKs := func(table string) ([]string, error) {
		if pks, ok := pkCache[table]; ok {
			return pks, nil
		}
		_, pks, err := auditTableShape(s.db, table)
		if err != nil {
			return nil, err
		}
		pkCache[table] = pks
		return pks, nil
	}

	reversed := 0
	for _, e := range entries {
		if !isAuditedTable(e.table) {
			// Defensive: never touch a table outside the audited whitelist.
			continue
		}
		pks, err := tablePKs(e.table)
		if err != nil {
			return 0, 0, err
		}
		if err := reverseAuditEntry(ctx, tx, e, pks); err != nil {
			return 0, 0, fmt.Errorf("revert %s %s: %w", e.table, e.op, err)
		}
		reversed++
	}

	revision, err := bumpFestRevisionTx(ctx, tx, festID, "audit:revert", strconv.FormatInt(targetID, 10))
	if err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return reversed, revision, nil
}

func isAuditedTable(name string) bool {
	for _, t := range auditedTables {
		if t == name {
			return true
		}
	}
	return false
}

// reverseAuditEntry undoes one recorded mutation:
//   - INSERT → delete the inserted row (by pk, from after_json)
//   - DELETE → re-insert the deleted row (from before_json)
//   - UPDATE → restore the row's prior column values (from before_json, by pk)
func reverseAuditEntry(ctx context.Context, tx *sql.Tx, e auditRevertEntry, pks []string) error {
	switch e.op {
	case "INSERT":
		after, err := decodeAuditRow(e.afterJSON)
		if err != nil {
			return err
		}
		return deleteByPK(ctx, tx, e.table, pks, after)
	case "DELETE":
		before, err := decodeAuditRow(e.beforeJSON)
		if err != nil {
			return err
		}
		return insertRow(ctx, tx, e.table, before)
	case "UPDATE":
		before, err := decodeAuditRow(e.beforeJSON)
		if err != nil {
			return err
		}
		return updateRowByPK(ctx, tx, e.table, pks, before)
	default:
		return fmt.Errorf("unknown op %q", e.op)
	}
}

func decodeAuditRow(s sql.NullString) (map[string]any, error) {
	if !s.Valid || s.String == "" {
		return nil, errors.New("missing row json")
	}
	dec := json.NewDecoder(strings.NewReader(s.String))
	dec.UseNumber()
	var raw map[string]any
	if err := dec.Decode(&raw); err != nil {
		return nil, err
	}
	out := make(map[string]any, len(raw))
	for k, v := range raw {
		out[k] = jsonToSQLValue(v)
	}
	return out, nil
}

// jsonToSQLValue maps a decoded JSON value to a SQLite bind value, keeping
// integers as int64 (not float64) so ids and counts round-trip exactly.
func jsonToSQLValue(v any) any {
	switch n := v.(type) {
	case nil:
		return nil
	case json.Number:
		str := n.String()
		if !strings.ContainsAny(str, ".eE") {
			if i, err := strconv.ParseInt(str, 10, 64); err == nil {
				return i
			}
		}
		if f, err := n.Float64(); err == nil {
			return f
		}
		return str
	default:
		return v
	}
}

func insertRow(ctx context.Context, tx *sql.Tx, table string, row map[string]any) error {
	cols := sortedKeys(row)
	if len(cols) == 0 {
		return errors.New("empty row")
	}
	placeholders := make([]string, len(cols))
	args := make([]any, len(cols))
	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = quoteIdent(c)
		placeholders[i] = "?"
		args[i] = row[c]
	}
	// INSERT OR IGNORE: if the row already exists (e.g. a later reversal already
	// recreated it via cascade), don't fail the whole revert.
	query := fmt.Sprintf("insert or ignore into %s (%s) values (%s)",
		table, strings.Join(quoted, ", "), strings.Join(placeholders, ", "))
	_, err := tx.ExecContext(ctx, query, args...)
	return err
}

func updateRowByPK(ctx context.Context, tx *sql.Tx, table string, pks []string, row map[string]any) error {
	if len(pks) == 0 {
		return errors.New("table has no primary key")
	}
	pkSet := map[string]bool{}
	for _, p := range pks {
		pkSet[p] = true
	}
	setCols := make([]string, 0, len(row))
	args := make([]any, 0, len(row)+len(pks))
	for _, c := range sortedKeys(row) {
		if pkSet[c] {
			continue
		}
		setCols = append(setCols, quoteIdent(c)+" = ?")
		args = append(args, row[c])
	}
	if len(setCols) == 0 {
		return nil
	}
	where, whereArgs, err := pkWhere(pks, row)
	if err != nil {
		return err
	}
	args = append(args, whereArgs...)
	query := fmt.Sprintf("update %s set %s where %s", table, strings.Join(setCols, ", "), where)
	_, err = tx.ExecContext(ctx, query, args...)
	return err
}

func deleteByPK(ctx context.Context, tx *sql.Tx, table string, pks []string, row map[string]any) error {
	where, whereArgs, err := pkWhere(pks, row)
	if err != nil {
		return err
	}
	query := fmt.Sprintf("delete from %s where %s", table, where)
	_, err = tx.ExecContext(ctx, query, whereArgs...)
	return err
}

func pkWhere(pks []string, row map[string]any) (string, []any, error) {
	if len(pks) == 0 {
		return "", nil, errors.New("table has no primary key")
	}
	clauses := make([]string, len(pks))
	args := make([]any, len(pks))
	for i, p := range pks {
		v, ok := row[p]
		if !ok {
			return "", nil, fmt.Errorf("row json missing pk column %q", p)
		}
		clauses[i] = quoteIdent(p) + " = ?"
		args[i] = v
	}
	return strings.Join(clauses, " and "), args, nil
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
