package pages

import (
	"context"
	"dope/dope/domain/edit"
	"dope/dope/storage/journal"
	"dope/dope/storage/store"
	dopestrings "dope/i18nstrings"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	ui "dope/dope/web/ui"
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
	op      journal.Op
	payload []byte
}

func (s *Server) loadGameJournalGroups(ctx context.Context, festID, gameID int64) ([]journalChange, error) {
	doc, _ := store.LoadGameDoc(ctx, s.h.Engine().DB, festID, gameID)
	gameType, stateJSON := doc.GameType, doc.State

	rows, err := s.h.Engine().DB.QueryContext(ctx, `
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
		g.ops = append(g.ops, journalOpRow{op: journal.Op(op), payload: append([]byte(nil), payload...)})
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
		res.prepareEK(ctx, s.h.Engine().DB, allOps)
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
	odNum         map[int]string   // OD team number -> name (entries store numbers)
	ekAnswer      map[int64]ekCell // answer id -> resolved cell
	ekAnswerTeam  map[int64]int64  // answer id -> participant_id (pre-name-resolution)
	ekAnswerMatch map[int64]int64  // answer id -> match_id
	ekTeam        map[int64]string // participant_id -> name
	ekMatch       map[int64]string // match_id -> code
	ekPlayer      map[int64]string // player_id -> name
}

func (s *Server) newNameResolver(ctx context.Context, gameType, stateJSON string) *nameResolver {
	r := &nameResolver{gameType: gameType}
	switch gameType {
	case "ksi":
		r.names = ksiParticipantNames(stateJSON)
	case "od":
		r.names, r.odNum = odTeamNames(stateJSON)
	case "ek":
		r.ekAnswer = map[int64]ekCell{}
		r.ekAnswerTeam = map[int64]int64{}
		r.ekAnswerMatch = map[int64]int64{}
		r.ekTeam = map[int64]string{}
		r.ekMatch = map[int64]string{}
		r.ekPlayer = map[int64]string{}
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

// odTeamNames returns OD team names both by roster index and keyed by the team's
// printed number. The number map matters because the OD entries grid stores team
// NUMBERS as values (entries[question][slot] = number), not roster indices.
func odTeamNames(stateJSON string) ([]string, map[int]string) {
	var st struct {
		Teams []struct {
			Name   string `json:"name"`
			Number int    `json:"number"`
		} `json:"teams"`
	}
	_ = json.Unmarshal([]byte(stateJSON), &st)
	out := make([]string, len(st.Teams))
	byNum := make(map[int]string, len(st.Teams))
	for i, t := range st.Teams {
		out[i] = t.Name
		if t.Number != 0 {
			byNum[t.Number] = t.Name
		}
	}
	return out, byNum
}

// prepareEK batch-resolves the answer / team / match identities referenced by a
// game's row-ops, so EK descriptions can show real names without per-row queries.
func (r *nameResolver) prepareEK(ctx context.Context, db store.Queryer, allOps [][]journalOpRow) {
	answerIDs := map[int64]bool{}
	teamIDs := map[int64]bool{}
	matchIDs := map[int64]bool{}
	playerIDs := map[int64]bool{}
	for _, ops := range allOps {
		for _, o := range ops {
			if o.op > journal.OpRowDel {
				continue
			}
			table, row, err := journal.DecodeRowOpJSON(o.payload)
			if err != nil {
				continue
			}
			switch table {
			case "answers":
				if id, ok := rowInt(row, "id"); ok {
					answerIDs[id] = true
				}
			case "match_results":
				if id, ok := rowInt(row, "participant_id"); ok {
					teamIDs[id] = true
				}
				if id, ok := rowInt(row, "match_id"); ok {
					matchIDs[id] = true
				}
			case "themes":
				if id, ok := rowInt(row, "participant_id"); ok {
					teamIDs[id] = true
				}
				if id, ok := rowInt(row, "match_id"); ok {
					matchIDs[id] = true
				}
				if id, ok := rowInt(row, "player_id"); ok {
					playerIDs[id] = true
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
select a.id, t.participant_id, t.theme_index, a.answer_index, t.match_id
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
		rows, err := db.QueryContext(ctx, `select id, name from participants where id in (`+placeholders(len(batch))+`)`, idArgs(batch)...)
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
	for _, batch := range chunkIDs(keysOfInt(playerIDs), 400) {
		rows, err := db.QueryContext(ctx, `select id, trim(first_name || ' ' || last_name) from players where id in (`+placeholders(len(batch))+`)`, idArgs(batch)...)
		if err != nil {
			continue
		}
		for rows.Next() {
			var id int64
			var name string
			if rows.Scan(&id, &name) == nil {
				r.ekPlayer[id] = name
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
			if o.op >= journal.OpEvImport {
				lines = append(lines, describeEvent(o.op, o.payload)...)
			}
		}
		return lines
	}
}

// describeStatePatchGroup renders the group's game-state patch ops via lineFn,
// and falls back to coarse event labels for non-patch events.
func (r *nameResolver) describeStatePatchGroup(ops []journalOpRow, lineFn func(op edit.PatchOp) string) []string {
	var lines []string
	s := dopestrings.Default
	for _, o := range ops {
		switch {
		case o.op == journal.OpEvGameStatePatch:
			var req edit.PatchRequest
			if json.Unmarshal(o.payload, &req) != nil {
				lines = append(lines, s.Journal.Patch.StateFallback())
				continue
			}
			for _, p := range req.Ops {
				if l := lineFn(p); l != "" {
					lines = append(lines, l)
				}
			}
		case o.op == journal.OpEvGameState:
			lines = append(lines, s.Journal.Event.StateReplaced())
		case o.op >= journal.OpEvImport && o.op != journal.OpEvMatchUpdate:
			lines = append(lines, describeEvent(o.op, o.payload)...)
		}
	}
	return lines
}

// ksiPatchLine renders one KSI state-patch op. State shape:
// themes[t].answers[player][question] = mark; participants[i] = name.
func (r *nameResolver) ksiPatchLine(op edit.PatchOp) string {
	segs := patchSegs(op.Path)
	s := dopestrings.Default
	switch {
	case len(segs) == 5 && segs[0].s == "themes" && segs[2].s == "answers" &&
		segs[1].num && segs[3].num && segs[4].num:
		who := r.name(segs[3].n)
		if who == "" {
			who = s.Journal.Ksi.ParticipantFallback(strconv.Itoa(segs[3].n + 1))
		}
		return s.Journal.Ksi.Answer(strconv.Itoa(segs[0+1].n+1), who, strconv.Itoa(segs[4].n+1), patchMark(op.Value))
	case len(segs) == 2 && segs[0].s == "participants" && segs[1].num:
		_, name := patchValue(op.Value)
		return s.Journal.Ksi.Rename(strconv.Itoa(segs[1].n+1), name)
	case len(segs) >= 1 && segs[0].s == "finished":
		return s.Journal.Ksi.Finished()
	case len(segs) >= 1 && segs[0].s == "declined":
		return s.Journal.Ksi.Declined()
	default:
		return genericPatchLine(op)
	}
}

// odPatchLine renders one OD state-patch op. State shape:
// entries[question][slot] = teamNumber (0 = empty slot). The slot is just a grid
// position; the team is identified by the VALUE (its printed number), so resolve
// the team from the value, not from the slot index.
func (r *nameResolver) odPatchLine(op edit.PatchOp) string {
	segs := patchSegs(op.Path)
	s := dopestrings.Default
	switch {
	case len(segs) == 3 && segs[0].s == "entries" && segs[1].num && segs[2].num:
		num := patchInt(op.Value)
		if num <= 0 {
			return s.Journal.Od.AnswerClear(strconv.Itoa(segs[1].n + 1))
		}
		return s.Journal.Od.AnswerSet(strconv.Itoa(segs[1].n+1), r.odTeamLabel(num))
	case len(segs) == 2 && segs[0].s == "entries" && segs[1].num:
		return s.Journal.Od.EntryChanged(strconv.Itoa(segs[1].n + 1))
	case len(segs) == 1 && segs[0].s == "entries":
		return s.Journal.Od.EntriesChanged()
	case len(segs) == 2 && segs[0].s == "completed" && segs[1].num:
		_, val := patchValue(op.Value)
		return s.Journal.Od.Readiness(strconv.Itoa(segs[1].n+1), val)
	case len(segs) >= 1 && segs[0].s == "shootoutRounds":
		return s.Journal.Od.Shootout()
	default:
		return genericPatchLine(op)
	}
}

// odTeamLabel renders a team identified by its printed number, using its name
// when known and falling back to the bare number.
func (r *nameResolver) odTeamLabel(num int) string {
	s := dopestrings.Default
	if name := strings.TrimSpace(r.odNum[num]); name != "" {
		return s.Journal.Od.TeamNamed(name, strconv.Itoa(num))
	}
	return s.Journal.Od.TeamUnnamed(strconv.Itoa(num))
}

func (r *nameResolver) describeEK(ops []journalOpRow) []string {
	var lines []string
	s := dopestrings.Default
	for _, o := range ops {
		if o.op > journal.OpRowDel {
			continue
		}
		table, row, err := journal.DecodeRowOpJSON(o.payload)
		if err != nil {
			continue
		}
		switch table {
		case "answers":
			id, _ := rowInt(row, "id")
			cell := r.ekAnswer[id]
			mark := markLabel(rowStr(row, "mark"))
			lines = append(lines, s.Journal.Ek.Answer(
				matchPrefix(cell.match), teamOr(cell.team), strconv.Itoa(cell.theme+1), strconv.Itoa(cell.question+1), mark))
		case "match_results":
			teamID, _ := rowInt(row, "participant_id")
			matchID, _ := rowInt(row, "match_id")
			if rank, ok := rowInt(row, "rank"); ok {
				lines = append(lines, s.Journal.Ek.Rank(
					matchPrefix(r.ekMatch[matchID]), teamOr(r.ekTeam[teamID]), strconv.FormatInt(rank, 10)))
			}
		case "themes":
			teamID, _ := rowInt(row, "participant_id")
			matchID, _ := rowInt(row, "match_id")
			theme, _ := rowInt(row, "theme_index")
			prefix := s.Journal.Ek.ThemePrefix(
				matchPrefix(r.ekMatch[matchID]), teamOr(r.ekTeam[teamID]), strconv.FormatInt(theme+1, 10))
			if playerID, ok := rowInt(row, "player_id"); ok && playerID != 0 {
				if name := strings.TrimSpace(r.ekPlayer[playerID]); name != "" {
					lines = append(lines, prefix+s.Journal.Ek.PlayerPlays(name))
				} else {
					lines = append(lines, prefix+s.Journal.Ek.PlayerAssigned())
				}
			} else {
				lines = append(lines, prefix+s.Journal.Ek.PlayerRemoved())
			}
		case "matches":
			if st := rowStr(row, "status"); st != "" {
				label := s.Journal.Ek.MatchReopened()
				if st == "finished" {
					label = s.Journal.Ek.MatchFinished()
				}
				lines = append(lines, matchPrefix(r.ekMatch[rowInt64(row, "id")])+label)
			}
		}
	}
	if len(lines) == 0 {
		// No row-level detail (e.g. a coarse import/reseed) — use the event label.
		for _, o := range ops {
			if o.op >= journal.OpEvImport {
				if l := describeEvent(o.op, o.payload); len(l) > 0 {
					lines = append(lines, l...)
				} else if o.op == journal.OpEvMatchUpdate {
					lines = append(lines, s.Journal.Ek.MatchUpdate())
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
	return dopestrings.Default.Journal.Ek.MatchPrefix(code)
}

func teamOr(name string) string {
	if name == "" {
		return dopestrings.Default.Journal.Ek.TeamUnnamed()
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

func genericPatchLine(op edit.PatchOp) string {
	parts := make([]string, 0, len(op.Path))
	for _, seg := range op.Path {
		parts = append(parts, strings.Trim(strings.TrimSpace(string(seg)), `"`))
	}
	_, val := patchValue(op.Value)
	path := strings.Join(parts, " · ")
	if op.Op == "remove" {
		return dopestrings.Default.Journal.Patch.GenericRemoved(path)
	}
	return dopestrings.Default.Journal.Patch.GenericSet(path, val)
}

// patchInt reads a JSON patch value as an integer (OD entries store team
// numbers). Returns 0 for null / non-numeric / empty.
func patchInt(v json.RawMessage) int {
	s := strings.TrimSpace(string(v))
	if s == "" || s == "null" {
		return 0
	}
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return 0
}

func patchMark(v json.RawMessage) string {
	raw, _ := patchValue(v)
	return markLabel(raw)
}

func patchValue(v json.RawMessage) (raw, display string) {
	s := strings.TrimSpace(string(v))
	if s == "" || s == "null" {
		return "", dopestrings.Default.Journal.Value.Empty()
	}
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		var str string
		if json.Unmarshal(v, &str) == nil {
			if str == "" {
				return "", dopestrings.Default.Journal.Value.Empty()
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
	s := dopestrings.Default
	switch m {
	case "right":
		return s.Journal.Mark.Right()
	case "wrong":
		return s.Journal.Mark.Wrong()
	case "":
		return s.Journal.Mark.None()
	default:
		return m
	}
}

// describeEvent renders coarse, non-game-typed events (imports, reseeds, etc.).
func describeEvent(op journal.Op, payload []byte) []string {
	s := dopestrings.Default
	switch op {
	case journal.OpEvReseedCalculate:
		return []string{s.Journal.Event.Reseed()}
	case journal.OpEvRatingImport:
		return []string{s.Journal.Event.RatingImport()}
	case journal.OpEvSeedImportKSI:
		return []string{s.Journal.Event.SeedImportKsi()}
	case journal.OpEvSeedImportDecline:
		return []string{s.Journal.Event.SeedDecline()}
	case journal.OpEvGameCreate:
		return []string{s.Journal.Event.GameCreate()}
	case journal.OpEvGameClear:
		return []string{s.Journal.Event.GameClear()}
	case journal.OpEvGameDelete:
		return []string{s.Journal.Event.GameDelete()}
	case journal.OpEvFestNumbers:
		return []string{s.Journal.Event.FestNumbers()}
	case journal.OpEvVenuesUpdate, journal.OpEvMatchVenue:
		return []string{s.Journal.Event.Venue()}
	case journal.OpEvPlayerOverride, journal.OpEvPlayerOverrideEdit:
		return []string{s.Journal.Event.PlayerOverride()}
	case journal.OpEvFestAccess:
		return []string{s.Journal.Event.FestAccess()}
	case journal.OpEvImport:
		return []string{s.Journal.Event.SchemeImport()}
	case journal.OpEvGameRevert:
		return []string{s.Journal.Event.GameRevert()}
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

// journalDoc builds a game's edit-history page: a scrollable table with one row
// per host action (when / who / the change descriptions) and a per-row
// revert-to-here form. The revert confirm is a data-confirm attribute wired by
// pageforms.js (no inline on* handler).
func journalDoc(festID, gameID int64, title, festTitle, errMsg, notice string, groups []journalChange) *ui.Doc {
	var main []ui.Item
	s := dopestrings.Default
	if errMsg != "" {
		main = append(main, ui.Empty(ui.Text(errMsg)))
	}
	if notice != "" {
		main = append(main, ui.Note(ui.Text(notice)))
	}
	if len(groups) > 0 {
		rows := []ui.Item{ui.Trow(
			ui.Hcell(ui.Text(s.Journal.Page.ColWhen())), ui.Hcell(ui.Text(s.Journal.Page.ColWho())),
			ui.Hcell(ui.Text(s.Journal.Page.ColChanges())), ui.Hcell(),
		)}
		for _, g := range groups {
			rows = append(rows, journalRow(festID, gameID, g))
		}
		main = append(main, ui.Section(ui.Table(append([]ui.Item{ui.Scroll()}, rows...)...)))
	} else {
		main = append(main, ui.Section(ui.Empty(ui.Text(s.Journal.Page.Empty()))))
	}

	page := []ui.Item{
		ui.Title(s.Journal.Page.Title(title)), ui.PagePublic, ui.Classicscripts("dist/pageforms.js"),
		ui.Publictopbar(Trail(append(FestCrumbs(strconv.FormatInt(festID, 10), festTitle),
			ui.Crumb(ui.Href(fmt.Sprintf("/host/fest/%d/audit", festID)), ui.Text(s.Journal.Index.Title()))), title)),
	}
	page = append(page, main...)
	return &ui.Doc{Nodes: []ui.Node{ui.Page(page...)}}
}

func journalRow(festID, gameID int64, g journalChange) *ui.Element {
	s := dopestrings.Default
	actor := ui.Cell(ui.Muted(ui.Text("—")))
	if g.Actor != "" {
		actor = ui.Cell(ui.Text(g.Actor))
	}
	lines := make([]ui.Item, 0, len(g.Lines)+1)
	for _, ln := range g.Lines {
		lines = append(lines, ui.Paragraph(ui.Text(ln)))
	}
	if g.More > 0 {
		lines = append(lines, ui.Note(ui.Text(s.Journal.Page.More(strconv.Itoa(g.More)))))
	}
	revert := ui.Cell(ui.Form(
		ui.Method("post"), ui.Action(fmt.Sprintf("/host/fest/%d/audit/%d/revert", festID, gameID)),
		ui.Data("confirm", s.Journal.Page.RevertConfirm()),
		ui.Hiddenfield(ui.Name("target"), ui.Value(strconv.FormatInt(g.RevertTo, 10))),
		ui.Button(ui.Danger, ui.Submit(), ui.Text(s.Journal.Page.RevertSubmit())),
	))
	return ui.Trow(ui.Cell(ui.Muted(ui.Text(g.When))), actor, ui.Cell(lines...), revert)
}

func (s *Server) RenderGameJournal(w http.ResponseWriter, r *http.Request, festID, gameID int64, errMsg, notice string) {
	var title string
	_ = s.h.Engine().DB.QueryRowContext(r.Context(), `select coalesce(title, code) from games where id = ? and fest_id = ?`, gameID, festID).Scan(&title)
	if title == "" {
		title = dopestrings.Default.Journal.Page.DefaultTitle(strconv.FormatInt(gameID, 10))
	}
	groups, err := s.loadGameJournalGroups(r.Context(), festID, gameID)
	if err != nil {
		http.Error(w, "journal: "+err.Error(), http.StatusInternalServerError)
		return
	}
	RenderDoc(w, s.h.Engine().AssetETags, journalDoc(festID, gameID, title, FestTitle(r.Context(), s.h.Engine().DB, festID), errMsg, notice, groups))
}

func (s *Server) HandleGameRevert(w http.ResponseWriter, r *http.Request, festID, gameID int64) {
	if err := r.ParseForm(); err != nil {
		s.RenderGameJournal(w, r, festID, gameID, "bad form", "")
		return
	}
	target, err := strconv.ParseInt(strings.TrimSpace(r.Form.Get("target")), 10, 64)
	if err != nil {
		s.RenderGameJournal(w, r, festID, gameID, "bad target", "")
		return
	}
	revision, err := s.h.RevertGameToPoint(r.Context(), festID, gameID, target)
	if err != nil {
		s.RenderGameJournal(w, r, festID, gameID, dopestrings.Default.Journal.Page.RevertFailed(err.Error()), "")
		return
	}
	s.h.BroadcastFestView(festID, gameID, revision)
	s.RenderGameJournal(w, r, festID, gameID, "", dopestrings.Default.Journal.Page.RevertDone())
}
