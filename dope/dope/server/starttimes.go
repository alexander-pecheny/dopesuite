package dopeserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"dope/dope/platform/util"
	"dope/dope/storage/festwrite"
	"dope/dope/storage/store"
	"dope/dope/web/route"
	dopestrings "dope/i18nstrings"

	corei18n "pecheny.me/dopecore/i18nstrings"
)

// A bout's start time is a schedule note the host types ("10:30"), shown
// beside its venue. Nothing derives it and nothing reads it but the pages: a
// bout without one looks as it always did. The host sets it on one bout or
// on its whole wave — every bout of the Game in the same block, round and
// wave, the bouts that start together.

// startsAtRequest is the body of POST …/matches/{code}/starts-at. An empty
// Time clears it. Wave sets it on every bout of the bout's wave.
type startsAtRequest struct {
	Time string `json:"time"`
	Wave bool   `json:"wave,omitempty"`
}

var startsAtPattern = regexp.MustCompile(`^(\d{1,2})[:.](\d{2})$`)

// normalizeStartsAt reads a time as a host types it, "9:05", "09.05" or
// "09:05", as "09:05". An empty one stays empty.
func normalizeStartsAt(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	m := startsAtPattern.FindStringSubmatch(raw)
	if m == nil {
		return "", corei18n.User(dopestrings.Default.Server.StartsAt.Bad(raw))
	}
	hours, _ := strconv.Atoi(m[1])
	minutes, _ := strconv.Atoi(m[2])
	if hours > 23 || minutes > 59 {
		return "", corei18n.User(dopestrings.Default.Server.StartsAt.Bad(raw))
	}
	return fmt.Sprintf("%02d:%02d", hours, minutes), nil
}

func (s *server) scopedMatchStartsAt(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	mscope, err := s.matchScopeOf(r, sc)
	if err != nil {
		return err
	}
	var req startsAtRequest
	if err := route.DecodeJSON(r, &req); err != nil {
		return err
	}
	at, err := normalizeStartsAt(req.Time)
	if err != nil {
		return route.BadUser(err)
	}
	touched, revision, err := s.setStartsAt(r.Context(), mscope, at, req.Wave)
	if err != nil {
		return err
	}
	var own []byte
	for _, bout := range touched {
		view, err := s.loadScopedMatchViewSnapshot(bout)
		if err != nil {
			continue
		}
		data, err := json.Marshal(view)
		if err != nil {
			continue
		}
		s.eng.BroadcastState(sc.FestID, matchScopeKey(bout), revision, data)
		if bout.MatchID == mscope.MatchID {
			own = data
		}
	}
	s.eng.InvalidateFestViewCache(sc.FestID)
	s.broadcastFestView(festScope{FestID: sc.FestID, GameID: sc.GameID}, revision)
	if own == nil {
		// The bout had that time already (its wave may not have): it answers
		// with its view all the same.
		view, err := s.loadScopedMatchViewSnapshot(mscope)
		if err != nil {
			return err
		}
		if own, err = json.Marshal(view); err != nil {
			return err
		}
	}
	return route.JSONBytes(w, own)
}

// setStartsAt writes the time on the bout, or on its whole wave, and returns
// the bouts it changed with the fest revision it reached.
func (s *server) setStartsAt(reqCtx context.Context, mscope matchScope, at string, wave bool) ([]matchScope, int64, error) {
	const label = "match-starts-at"
	ctx, cancel := festwrite.AuditDetachedContext(reqCtx, mscope.FestID)
	defer cancel()
	conn, err := s.eng.AcquireWriteConn(ctx, label)
	if err != nil {
		return nil, 0, err
	}
	defer conn.Close()
	defer s.eng.LockWrite(label)()
	tx, err := s.eng.BeginWriteTxConn(ctx, conn)
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()

	where := `m.id = ?`
	args := []any{mscope.MatchID}
	if wave {
		// The bouts of one wave: the same block, round and wave of this Game.
		// A bout with no round of its own has only its stage to go by.
		where = `m.game_id = ? and exists (
  select 1 from matches o join stages os on os.id = o.stage_id
  where o.id = ? and (
    (o.round > 0 and os.block_code = s.block_code and o.round = m.round and o.wave = m.wave)
    or (o.round = 0 and o.stage_id = m.stage_id)))`
		args = []any{mscope.GameID, mscope.MatchID}
	}
	targets, err := store.CollectRows(ctx, tx, `
select m.id, m.code from matches m join stages s on s.id = m.stage_id
where `+where+` and coalesce(m.starts_at, '') != ?
order by m.position, m.id`, append(args, at), func(rows *sql.Rows) (matchScope, error) {
		bout := matchScope{festScope: festScope{FestID: mscope.FestID, GameID: mscope.GameID}}
		return bout, rows.Scan(&bout.MatchID, &bout.Code)
	})
	if err != nil {
		return nil, 0, err
	}
	for _, bout := range targets {
		if _, err := tx.ExecContext(ctx, `
update matches set starts_at = ?, revision = revision + 1 where id = ?`, nullableString(at), bout.MatchID); err != nil {
			return nil, 0, err
		}
	}
	payload := map[string]any{"code": mscope.Code, "time": at}
	if wave {
		payload["wave"] = true
	}
	revision, err := festwrite.BumpFestRevisionTx(ctx, tx, mscope.FestID, "match:starts-at", util.MustJSON(payload))
	if err != nil {
		return nil, 0, err
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	return targets, revision, nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
