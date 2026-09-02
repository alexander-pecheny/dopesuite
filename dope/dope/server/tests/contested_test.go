package tests

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"dope/dope/domain/venues"
	"dope/dope/platform/util"
	"dope/dope/storage/store"
)

// A спорный is stored beside the ОД document and spliced into every copy of it
// a reader gets, and stripped again from whatever a client writes back.
func TestContestedRideTheDocumentButAreNotInIt(t *testing.T) {
	db := venueTestDB(t)
	ctx := t.Context()
	festID, slot := newVenueSlot(t, db, []int{2})
	alice := newVenueUser(t, db, "alice")
	fileApplication(t, db, slot, alice, "Мантисса", 0, nil)
	setStatus(t, db, slot, alice, venues.StatusAccepted)

	if err := inTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return store.SaveContestedTx(ctx, tx, festID, slot.GameID, alice, 1, 1, "Текст ответа", util.UtcNow())
	}); err != nil {
		t.Fatal(err)
	}
	list, err := store.LoadContested(ctx, db, slot.GameID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list %+v %v", list, err)
	}
	if list[0].Question != 1 || list[0].Number != 1 || list[0].Answer != "Текст ответа" || list[0].AcceptedHere {
		t.Fatalf("stored %+v", list[0])
	}

	doc, err := store.LoadGameDoc(ctx, db, festID, slot.GameID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(doc.State, store.ContestedKey) {
		t.Fatal("the stored document must not carry the спорные")
	}
	spliced := store.WithContested([]byte(doc.State), list)
	var read struct {
		Teams     []map[string]any        `json:"teams"`
		Contested []store.ContestedAnswer `json:"contested"`
	}
	if err := json.Unmarshal(spliced, &read); err != nil {
		t.Fatal(err)
	}
	if len(read.Contested) != 1 || len(read.Teams) != 1 {
		t.Fatalf("spliced %s", spliced)
	}
	if got := store.StripContested(spliced); strings.Contains(string(got), store.ContestedKey) {
		t.Fatalf("stripped %s", got)
	}

	// The host's toggle, then the delete.
	if err := inTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return store.SetContestedAcceptedTx(ctx, tx, festID, slot.GameID, 1, 1, true)
	}); err != nil {
		t.Fatal(err)
	}
	if list, _ := store.LoadContested(ctx, db, slot.GameID); len(list) != 1 || !list[0].AcceptedHere {
		t.Fatalf("after accept: %+v", list)
	}
	if err := inTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return store.DeleteContestedTx(ctx, tx, festID, slot.GameID, 1, 1)
	}); err != nil {
		t.Fatal(err)
	}
	if list, _ := store.LoadContested(ctx, db, slot.GameID); len(list) != 0 {
		t.Fatalf("after delete: %+v", list)
	}
}

func TestContestedRefusesAnUnseatedNumber(t *testing.T) {
	db := venueTestDB(t)
	festID, slot := newVenueSlot(t, db, []int{2})
	err := inTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return store.SaveContestedTx(ctx, tx, festID, slot.GameID, 0, 0, 9, "нет такой", util.UtcNow())
	})
	if err == nil || !strings.Contains(err.Error(), store.ErrNoSuchNumber.Error()) {
		t.Fatalf("err = %v, want ErrNoSuchNumber", err)
	}
}
