package festops_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"dope/dope/domain/core"
	"dope/dope/domain/festops"
	"dope/dope/domain/gamebuild"
	dopeserver "dope/dope/server"
	"dope/dope/storage/store"

	corei18n "pecheny.me/dopecore/i18nstrings"
)

func newFest(t *testing.T) (*core.Engine, int64) {
	t.Helper()
	db, err := dopeserver.OpenFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	var festID int64
	eng := &core.Engine{DB: db}
	if _, err := eng.CommitFestWrite(t.Context(), 0, "test", func(ctx context.Context, tx *sql.Tx) (core.FestWrite, error) {
		userID, err := dopeserver.EnsureSystemUser(ctx, tx)
		if err != nil {
			return core.FestWrite{}, err
		}
		festID, err = store.InsertReturningID(ctx, tx, `
insert into fests(slug, title, created_by, created_at, updated_at) values('fest', 'Фест', ?, '2026-10-05', '2026-10-05')`, userID)
		return core.FestWrite{}, err
	}); err != nil {
		t.Fatal(err)
	}
	return eng, festID
}

func createGame(t *testing.T, eng *core.Engine, festID int64, label string) int64 {
	t.Helper()
	var gameID int64
	if _, err := eng.CommitFestWrite(t.Context(), festID, "test", func(ctx context.Context, tx *sql.Tx) (written core.FestWrite, err error) {
		gameID, written, err = festops.CreateGameTx(ctx, tx, gamebuild.Spec{FestID: festID, Type: "od", Label: label})
		return written, err
	}); err != nil {
		t.Fatal(err)
	}
	return gameID
}

// Deleting the active Game moves the engine's pointer to the fest's first
// Game left, once the delete has committed.
func TestDeleteGameMovesTheActivePointer(t *testing.T) {
	eng, festID := newFest(t)
	first := createGame(t, eng, festID, "ЧГК")
	second := createGame(t, eng, festID, "ЧГК 2")
	eng.FestID, eng.ActiveGameID = festID, first
	revision, err := eng.CommitFestWrite(t.Context(), festID, "test", func(ctx context.Context, tx *sql.Tx) (core.FestWrite, error) {
		return festops.DeleteGameTx(ctx, tx, festID, first)
	})
	if err != nil {
		t.Fatal(err)
	}
	if revision == 0 || eng.ActiveGameID != second {
		t.Fatalf("revision %d, active game %d, want a revision and %d", revision, eng.ActiveGameID, second)
	}
}

// A slug another Game of the fest has is refused with a message for the host,
// and nothing of the settings is saved.
func TestSettingsRefuseATakenSlug(t *testing.T) {
	eng, festID := newFest(t)
	first := createGame(t, eng, festID, "ЧГК")
	second := createGame(t, eng, festID, "ЧГК 2")
	save := func(gameID int64, g festops.Settings) error {
		_, err := eng.CommitFestWrite(t.Context(), festID, "test", func(ctx context.Context, tx *sql.Tx) (core.FestWrite, error) {
			return festops.UpdateSettingsTx(ctx, tx, festID, gameID, g)
		})
		return err
	}
	if err := save(first, festops.Settings{Title: "Первая", Slug: "chgk"}); err != nil {
		t.Fatal(err)
	}
	err := save(second, festops.Settings{Title: "Вторая", Slug: "chgk"})
	if _, user := corei18n.AsUser(err); !user {
		t.Fatalf("err = %v, want a message for the host", err)
	}
	var title string
	if err := eng.DB.QueryRow(`select title from games where id = ?`, second).Scan(&title); err != nil || title == "Вторая" {
		t.Fatalf("title = %q (%v): a refused save wrote the title", title, err)
	}
}
