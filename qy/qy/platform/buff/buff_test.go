package buff

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestPlayersAreNamedByTheMirror(t *testing.T) {
	path := filepath.Join(t.TempDir(), "buff.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`create table players(id integer primary key, name text, patronymic text, surname text);
insert into players values(24813, 'Andrei', null, 'Petukhov')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	names, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got := names.Players(t.Context(), []int64{24813, 1})
	if len(got) != 1 || got[24813] != "Andrei Petukhov" {
		t.Errorf("names: %v", got)
	}
	if none, _ := Open(filepath.Join(t.TempDir(), "missing.db")); none.Players(t.Context(), []int64{1}) != nil {
		t.Error("a missing mirror should know nobody")
	}
}
