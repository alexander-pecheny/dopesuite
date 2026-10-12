package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const buffDBEnv = "XY_BUFF_DB"

// ratingsRelease is one release of rating.chgk.gg's "b" model, whole: every
// rated player with a name to match testers by, and every team. The browser
// matches tester names itself, so the server never learns who tested.
type ratingsRelease struct {
	ID      int64      `json:"id"`
	Date    string     `json:"date"`
	Q       float64    `json:"q"`
	Players [][]any    `json:"players"` // id, surname, name, patronymic, rating
	Teams   [][2]int64 `json:"teams"`   // id, rating; best first
}

// ratingsCache keeps the last answer for as long as buff.db is the same file:
// the mirror is replaced whole once a day, never edited in place.
type ratingsCache struct {
	mu    sync.Mutex
	mtime time.Time
	body  []byte
}

func (s *server) handleRatings(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	path := strings.TrimSpace(os.Getenv(buffDBEnv))
	st, err := os.Stat(path)
	if path == "" || err != nil {
		httpError(w, http.StatusNotFound, "no ratings")
		return
	}
	s.ratings.mu.Lock()
	defer s.ratings.mu.Unlock()
	if !st.ModTime().Equal(s.ratings.mtime) {
		rel, err := readRatings(r.Context(), path)
		if handleErr(w, err) {
			return
		}
		body, err := json.Marshal(rel)
		if handleErr(w, err) {
			return
		}
		s.ratings.mtime, s.ratings.body = st.ModTime(), body
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	_, _ = w.Write(s.ratings.body)
}

func readRatings(ctx context.Context, path string) (*ratingsRelease, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(2000)")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rel := &ratingsRelease{Players: [][]any{}, Teams: [][2]int64{}}
	err = db.QueryRowContext(ctx, `
select r.id, r.date, r.q from releases r
where r.id = (select max(release_id) from player_ratings)`).Scan(&rel.ID, &rel.Date, &rel.Q)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `
select pr.player_id, coalesce(p.surname, ''), coalesce(p.name, ''), coalesce(p.patronymic, ''), pr.rating
from player_ratings pr join players p on p.id = pr.player_id
where pr.rating > 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, rating int64
		var surname, name, patronymic string
		if err := rows.Scan(&id, &surname, &name, &patronymic, &rating); err != nil {
			return nil, err
		}
		rel.Players = append(rel.Players, []any{id, surname, name, patronymic, rating})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	teams, err := db.QueryContext(ctx, `
select team_id, rating from ratings where release_id = ? order by rating desc`, rel.ID)
	if err != nil {
		return nil, err
	}
	defer teams.Close()
	for teams.Next() {
		var t [2]int64
		if err := teams.Scan(&t[0], &t[1]); err != nil {
			return nil, err
		}
		rel.Teams = append(rel.Teams, t)
	}
	return rel, teams.Err()
}
