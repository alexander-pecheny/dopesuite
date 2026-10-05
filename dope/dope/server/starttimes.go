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

	"dope/dope/domain/core"
	"dope/dope/domain/matchedit"
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

// The largest hour and minute a start time may name.
const (
	maxHour   = 23
	maxMinute = 59
)

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
	if hours > maxHour || minutes > maxMinute {
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
func (s *server) setStartsAt(ctx context.Context, mscope matchScope, at string, wave bool) ([]matchScope, int64, error) {
	var targets []matchScope
	revision, err := s.eng.CommitFestWrite(ctx, mscope.FestID, "match-starts-at", func(ctx context.Context, tx *sql.Tx) (core.FestWrite, error) {
		changed, err := matchedit.SetStartsAtTx(ctx, tx, mscope.festScope, mscope.MatchID, at, wave)
		if err != nil {
			return core.FestWrite{}, err
		}
		for _, bout := range changed {
			targets = append(targets, matchScope{festScope: mscope.festScope, MatchID: bout.ID, Code: bout.Code})
		}
		payload := map[string]any{"code": mscope.Code, "time": at}
		if wave {
			payload["wave"] = true
		}
		return core.FestWrite{Event: "match:starts-at", Payload: payload}, nil
	})
	return targets, revision, err
}
