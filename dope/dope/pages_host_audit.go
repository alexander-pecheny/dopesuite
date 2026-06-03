package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// auditRevertScanRows bounds how many recent audit rows the page reads before
// grouping. One edit is one request (one group), so this covers a deep history
// while keeping the query and the page size bounded.
const auditRevertScanRows = 5000

// auditRevertMaxGroups caps how many change groups the page renders.
const auditRevertMaxGroups = 300

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
	MinID    int64
	MaxID    int64
	When     string
	Actor    string
	Summary  string
	Count    int
	IsNewest bool
	RevertTo int64 // target id for "revert to the state before this group"
}

type hostFestAuditData struct {
	Fest   hostMyFest
	Groups []auditChangeGroup
	Error  string
	Notice string
}

var hostFestAuditTemplate = template.Must(template.New("hostAudit").Parse(`<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Fest.Title}} · история изменений</title>
  <link rel="preload" href="/static/fonts/noto-sans-400.woff2" as="font" type="font/woff2" crossorigin>
  <link rel="stylesheet" href="/static/styles.css">
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
          <td class="audit-summary">{{.Summary}}{{if .IsNewest}} <span class="muted">(последнее)</span>{{end}}</td>
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
	groups, err := s.loadFestAuditGroups(r.Context(), festID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = hostFestAuditTemplate.Execute(w, hostFestAuditData{
		Fest:   fest,
		Groups: groups,
		Error:  errMsg,
		Notice: notice,
	})
}

// loadFestAuditGroups reads the recent fest-scoped audit rows and folds them
// into per-request change groups (rows of one request are contiguous in the
// id-descending scan), newest first.
func (s *server) loadFestAuditGroups(ctx context.Context, festID int64) ([]auditChangeGroup, error) {
	rows, err := s.db.QueryContext(ctx, `
select a.id, a.ts, a.table_name, coalesce(a.request_id, ''), coalesce(u.username, '')
from audit_log a
left join users u on u.id = a.actor_user_id
where a.fest_id = ?
order by a.id desc
limit ?`, festID, auditRevertScanRows)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type entry struct {
		id        int64
		ts        string
		table     string
		requestID string
		username  string
	}
	var groups []auditChangeGroup
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
	}
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.id, &e.ts, &e.table, &e.requestID, &e.username); err != nil {
			return nil, err
		}
		// Group rows of the same request; a blank request_id is its own group.
		key := e.requestID
		if key == "" {
			key = fmt.Sprintf("id:%d", e.id)
		}
		if cur == nil || key != curKey {
			flush()
			if len(groups) >= auditRevertMaxGroups {
				cur = nil
				break
			}
			actor := strings.TrimSpace(e.username)
			if actor == "" {
				actor = "система"
			}
			cur = &auditChangeGroup{
				MinID: e.id,
				MaxID: e.id,
				When:  formatAuditTime(e.ts),
				Actor: actor,
			}
			curKey = key
			tableCounts = map[string]int{}
		}
		if e.id > cur.MaxID {
			cur.MaxID = e.id
		}
		if e.id < cur.MinID {
			cur.MinID = e.id
		}
		cur.Count++
		tableCounts[e.table]++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	flush()
	if len(groups) > 0 {
		groups[0].IsNewest = true
	}
	return groups, nil
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
select table_name, op, before_json, after_json
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
