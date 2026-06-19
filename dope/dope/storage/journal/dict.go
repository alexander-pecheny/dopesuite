package journal

import (
	"database/sql"
	"time"
)

// Dict interns table names, column names and request-ids to small integer ids,
// persisted in the journal_dict table. id 0 means "none". It supports
// incremental use (load existing ids, intern new ones) so the live archiver can
// extend the same dictionary the converter seeded.
type Dict struct {
	ids     map[string]uint64
	pending map[uint64]string // newly-interned ids not yet persisted
	maxID   uint64
}

// NewDict returns an empty dictionary.
func NewDict() *Dict {
	return &Dict{ids: map[string]uint64{}, pending: map[uint64]string{}}
}

// LoadWritableDict reads the existing dictionary so new interns continue from
// the current max id (ids already present are reused).
func LoadWritableDict(db interface {
	Query(string, ...any) (*sql.Rows, error)
}) (*Dict, error) {
	d := NewDict()
	rows, err := db.Query(`select id, str from journal_dict`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uint64
		var s string
		if err := rows.Scan(&id, &s); err != nil {
			return nil, err
		}
		d.ids[s] = id
		if id > d.maxID {
			d.maxID = id
		}
	}
	return d, rows.Err()
}

// Intern returns the id for s, assigning a new one if unseen (0 for "").
func (d *Dict) Intern(s string) uint64 {
	if s == "" {
		return 0
	}
	if id, ok := d.ids[s]; ok {
		return id
	}
	d.maxID++
	id := d.maxID
	d.ids[s] = id
	d.pending[id] = s
	return id
}

// Persist writes the not-yet-persisted entries. Safe to call repeatedly.
func (d *Dict) Persist(db interface {
	Exec(string, ...any) (sql.Result, error)
}) error {
	for id, s := range d.pending {
		if _, err := db.Exec(`insert or replace into journal_dict(id, str) values(?, ?)`, int64(id), s); err != nil {
			return err
		}
	}
	d.pending = map[uint64]string{}
	return nil
}

// PersistTx is Persist within a transaction.
func (d *Dict) PersistTx(tx *sql.Tx) error {
	for id, s := range d.pending {
		if _, err := tx.Exec(`insert or replace into journal_dict(id, str) values(?, ?)`, int64(id), s); err != nil {
			return err
		}
	}
	d.pending = map[uint64]string{}
	return nil
}

// CreateTables creates the journal_dict and journal_segment tables if absent.
func CreateTables(db *sql.DB) error {
	_, err := db.Exec(`
create table if not exists journal_dict(
  id  integer primary key,
  str text not null unique
);
create table if not exists journal_segment(
  id          integer primary key,
  fest_id     integer not null,
  seq_start   integer not null,
  seq_end     integer not null,
  dsl_version integer not null,
  n_records   integer not null,
  blob        blob not null,
  created_at  text not null
);`)
	return err
}

// ParseTSMilli parses a journal timestamp to unix-millis, tolerating the
// RFC3339 variants the rows may carry; returns 0 on blank/unparseable input.
func ParseTSMilli(ts string) int64 {
	if ts == "" {
		return 0
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.999Z07:00"} {
		if t, err := time.Parse(layout, ts); err == nil {
			return t.UnixMilli()
		}
	}
	return 0
}
