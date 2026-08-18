package schema

import (
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"
)

func TestApplyRunsEachStepOnceInOrder(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var ran []int
	step := func(v int) Migration {
		return Migration{Version: v, Name: "step", Up: func(*sql.DB) error { ran = append(ran, v); return nil }}
	}
	if err := Apply(db, []Migration{step(2), step(5)}); err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, []Migration{step(2), step(5), step(7)}); err != nil {
		t.Fatal(err)
	}
	if got := len(ran); got != 3 || ran[0] != 2 || ran[1] != 5 || ran[2] != 7 {
		t.Errorf("ran %v, want 2 5 7 once each", ran)
	}
	if err := Apply(db, []Migration{step(9), step(8)}); err == nil {
		t.Error("an out-of-order list was accepted")
	}
	boom := errors.New("boom")
	err = Apply(db, []Migration{{Version: 10, Name: "fails", Up: func(*sql.DB) error { return boom }}})
	if !errors.Is(err, boom) {
		t.Errorf("failure not surfaced: %v", err)
	}
	if applied, _ := Applied(db, 10); applied {
		t.Error("a failed step was recorded")
	}
}
