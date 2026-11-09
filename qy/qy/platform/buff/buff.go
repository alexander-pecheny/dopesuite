// Package buff reads players' names from buff's mirror of rating.chgk.info,
// the SQLite file dope reads too. It is opened read-only and fails soft: a
// missing file or a broken query gives no names, never an error page.
package buff

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// reconnectAfter is how long a connection reads the file it opened.
const reconnectAfter = 5 * time.Minute

// Names maps a rating ID to the player's name as the rating site has it.
type Names interface {
	Players(ctx context.Context, ids []int64) map[int64]string
}

// None knows no names.
type None struct{}

func (None) Players(context.Context, []int64) map[int64]string { return nil }

type DB struct{ db *sql.DB }

// Open opens the mirror at path, or answers None when there is no file there.
func Open(path string) (Names, error) {
	if _, err := os.Stat(path); err != nil {
		return None{}, nil
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(2000)")
	if err != nil {
		return None{}, err
	}
	db.SetMaxOpenConns(2)
	// buff-sync replaces the file by a rename; a connection older than that
	// still reads the old one, so none lives past a few minutes.
	db.SetConnMaxLifetime(reconnectAfter)
	return &DB{db: db}, nil
}

// Players is "Имя Фамилия" for each ID the mirror knows.
func (d *DB) Players(ctx context.Context, ids []int64) map[int64]string {
	out := map[int64]string{}
	for _, id := range ids {
		var name, surname sql.NullString
		if err := d.db.QueryRowContext(ctx, `select name, surname from players where id = ?`, id).Scan(&name, &surname); err != nil {
			continue
		}
		if full := strings.TrimSpace(name.String + " " + surname.String); full != "" {
			out[id] = full
		}
	}
	return out
}
