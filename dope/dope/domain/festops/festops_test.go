package festops_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"testing"

	"dope/dope/domain/core"
	"dope/dope/domain/festops"
	"dope/dope/domain/gamebuild"
	"dope/dope/domain/games"
	dopeserver "dope/dope/server"
	"dope/dope/storage/store"
	dopestrings "dope/i18nstrings"

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

// addFlaggedTeams adds one fest team per Flag, named and numbered after it.
func addFlaggedTeams(t *testing.T, eng *core.Engine, festID int64, flags ...string) {
	t.Helper()
	for i, flag := range flags {
		res, err := eng.DB.Exec(`insert into fest_teams(fest_id, name, city, position, number) values(?, ?, '', ?, ?)`, festID, flag, i+1, i+1)
		if err != nil {
			t.Fatal(err)
		}
		teamID, _ := res.LastInsertId()
		if _, err := eng.DB.Exec(`insert into fest_team_flags(team_id, position, short, full) values(?, 1, ?, ?)`, teamID, flag, flag); err != nil {
			t.Fatal(err)
		}
	}
}

func saveSettings(t *testing.T, eng *core.Engine, festID, gameID int64, g festops.Settings) error {
	t.Helper()
	_, err := eng.CommitFestWrite(t.Context(), festID, "test", func(ctx context.Context, tx *sql.Tx) (core.FestWrite, error) {
		return festops.UpdateSettingsTx(ctx, tx, festID, gameID, g)
	})
	return err
}

// A flat Game is shaped by its own fields, so its settings refuse a scheme
// edit even when it was created from a DSL; the scheme it already has passes,
// which is what the JSON twin sends back when the host changes only the title.
func TestSettingsRefuseASchemeEditOfAFlatGame(t *testing.T) {
	eng, festID := newFest(t)
	addFlaggedTeams(t, eng, festID, "Студ", "Школ")
	const dsl = "[scheme]\nkind: flat\nthemes: 2\n"
	var gameID int64
	if _, err := eng.CommitFestWrite(t.Context(), festID, "test", func(ctx context.Context, tx *sql.Tx) (written core.FestWrite, err error) {
		gameID, written, err = festops.CreateGameTx(ctx, tx, gamebuild.Spec{FestID: festID, Type: "ksi", Label: "КСИ", DSL: dsl})
		return written, err
	}); err != nil {
		t.Fatal(err)
	}
	err := saveSettings(t, eng, festID, gameID, festops.Settings{Title: "КСИ", SchemeDSL: "[scheme]\nkind: flat\nthemes: 3\n"})
	if msg, _ := corei18n.AsUser(err); msg != dopestrings.Default.Host.Games.ErrorSchemeNotEditable(games.Label("ksi")) {
		t.Fatalf("err = %v, want the scheme edit refused as not editable", err)
	}
	if err := saveSettings(t, eng, festID, gameID, festops.Settings{Title: "КСИ отбора", SchemeDSL: dsl}); err != nil {
		t.Fatalf("the stored scheme sent back: %v", err)
	}
	knobs := createGame(t, eng, festID, "ЧГК")
	err = saveSettings(t, eng, festID, knobs, festops.Settings{Title: "ЧГК", SchemeDSL: dsl})
	if _, user := corei18n.AsUser(err); !user {
		t.Fatalf("err = %v, want a scheme refused on a Game built from its fields", err)
	}
}

// The settings page ticks the Divisions a Game shows. Every offered one left
// unticked is hidden, and one hidden before that no team carries now stays
// hidden. A list of hidden ones is written as given.
func TestSettingsHideTheDivisionsNotTicked(t *testing.T) {
	eng, festID := newFest(t)
	gameID := createGame(t, eng, festID, "ЧГК")
	addFlaggedTeams(t, eng, festID, "Студ", "Школ")
	hidden := func() []string {
		t.Helper()
		var raw string
		if err := eng.DB.QueryRow(`select coalesce(hidden_divisions, '') from games where id = ?`, gameID).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		return store.ParseHiddenDivisions(raw)
	}
	gone := []string{"ЧР"}
	if err := saveSettings(t, eng, festID, gameID, festops.Settings{Title: "ЧГК", HiddenDivisions: &gone}); err != nil {
		t.Fatal(err)
	}
	if got := hidden(); !slices.Equal(got, gone) {
		t.Fatalf("hidden = %v, want the list as given", got)
	}
	shown := []string{"Студ"}
	if err := saveSettings(t, eng, festID, gameID, festops.Settings{Title: "ЧГК", ShownDivisions: &shown}); err != nil {
		t.Fatal(err)
	}
	if got := hidden(); !slices.Equal(got, []string{"Школ", "ЧР"}) {
		t.Fatalf("hidden = %v, want Школ unticked and ЧР kept", got)
	}
	if err := saveSettings(t, eng, festID, gameID, festops.Settings{Title: "ЧГК"}); err != nil {
		t.Fatal(err)
	}
	if got := hidden(); !slices.Equal(got, []string{"Школ", "ЧР"}) {
		t.Fatalf("hidden = %v after a rename, want it left as it was", got)
	}
}
