package tests

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"dope/dope/domain/gamebuild"
	"dope/dope/storage/store"
	dopestrings "dope/i18nstrings"

	"pecheny.me/dopecore/session"
)

// legacyPastedGameTx stores a Game the way a pasted detailed JSON scheme made
// one before 2026-10-06 — its scheme_json and no DSL — and clears it, which
// writes its Structure from that scheme. Nothing creates such a Game any more;
// production keeps one (chr2026/ek-3), and it must still play, seat and rank.
func legacyPastedGameTx(ctx context.Context, tx *sql.Tx, festID int64, scheme store.FestScheme) (int64, error) {
	raw, err := json.Marshal(scheme)
	if err != nil {
		return 0, err
	}
	var position int
	if err := tx.QueryRowContext(ctx, `select coalesce(max(position), 0) + 1 from games where fest_id = ?`, festID).Scan(&position); err != nil {
		return 0, err
	}
	gameID, err := store.InsertReturningID(ctx, tx, `
insert into games(fest_id, code, title, game_type, position, scheme_json, scheme_dsl, state_json, status, team_list_source, roster_source, revision, created_at, updated_at)
values(?, ?, ?, ?, ?, ?, '', '{}', 'pending', 'fest', 'fest', 1, 'now', 'now')`,
		festID, fmt.Sprintf("%s-%d", scheme.GameType, position), scheme.Title, scheme.GameType, position, string(raw))
	if err != nil {
		return 0, err
	}
	_, err = gamebuild.Clear(ctx, tx, festID, gameID)
	return gameID, err
}

func pastedEKScheme() store.FestScheme {
	return store.FestScheme{SchemaVersion: 2, Slug: "pasted-ek", Title: "Вставленный ЭК", GameType: "ek",
		Stages: []store.SchemeStage{{Code: "r1", Title: "Раунд", StageType: "matches", Kind: "manual",
			Matches: []store.SchemeMatch{{Code: "A", Title: "Бой 1", ParticipantCount: 4, Slots: []store.SchemeSlot{
				{Seed: &store.SchemeSeedRef{Basket: 1, Number: 1}}, {Seed: &store.SchemeSeedRef{Basket: 1, Number: 2}},
				{Seed: &store.SchemeSeedRef{Basket: 1, Number: 3}}, {Seed: &store.SchemeSeedRef{Basket: 1, Number: 4}},
			}}}}}}
}

// A Game pasted as JSON before every bracket Game needed a scheme has no DSL.
// The settings page used to hide the editor, but the JSON twin recompiled it
// from any text and replaced its bracket. Both now refuse a scheme edit with
// a message, and the rest of its settings still save.
func TestLegacyPastedGameRefusesASchemeEdit(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	token := createTestSession(t, srv, systemUserID(t, db))
	var gameID int64
	withTx(t, db, func(ctx context.Context, tx *sql.Tx) (err error) {
		gameID, err = legacyPastedGameTx(ctx, tx, festID, pastedEKScheme())
		return err
	})
	var before string
	if err := db.QueryRow(`select scheme_json from games where id = ?`, gameID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	want := dopestrings.Default.Gamebuild.Recompile.Pasted()
	const dsl = "[scheme]\nkind: single_elimination\nparticipants: 4\nmatch_size: 4\nwinning_places: 2\n"

	settings := fmt.Sprintf("/api/fest/%d/games/%d/settings", festID, gameID)
	resp := scopedAPIRequest(t, srv, http.MethodPatch, settings, map[string]any{"scheme_dsl": dsl}, token)
	if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), want) {
		t.Fatalf("a scheme edit through the API: %d %s, want 400 saying %q", resp.Code, resp.Body.String(), want)
	}

	form := url.Values{"title": {"Вставленный ЭК"}, "scheme_dsl": {dsl}}
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/host/fest/%d/game/%d/settings", festID, gameID), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
	page := httptest.NewRecorder()
	srv.HostPageServer().HandleHostRouter(page, req)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), want) {
		t.Fatalf("a scheme edit through the form: %d, want the page saying %q", page.Code, want)
	}
	if strings.Contains(page.Body.String(), `name="scheme_dsl"`) {
		t.Fatal("the refused page offers a scheme editor for a Game that has no scheme")
	}

	var after string
	if err := db.QueryRow(`select scheme_json from games where id = ?`, gameID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatal("a refused scheme edit changed the bracket")
	}
	resp = scopedAPIRequest(t, srv, http.MethodPatch, settings, map[string]any{"title": "ЭК июля"}, token)
	if resp.Code != http.StatusOK {
		t.Fatalf("renaming the Game: %d %s", resp.Code, resp.Body.String())
	}
	withTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
		err := gamebuild.Recompile(ctx, tx, festID, gameID, dsl)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Recompile of a pasted Game: %v, want %q", err, want)
		}
		return nil
	})
}

// A scheme that does not compile reaches an API caller as the compiler wrote
// it, with a 400, as the form shows it. It used to be a bare 500.
func TestABadSchemeThroughTheAPIIsAUserError(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	token := createTestSession(t, srv, systemUserID(t, db))
	for i, name := range []string{"Альфа", "Бета", "Гамма", "Дельта"} {
		if _, err := db.Exec(`insert into fest_teams(fest_id, name, city, position, number) values(?, ?, '', ?, ?)`, festID, name, i+1, i+1); err != nil {
			t.Fatal(err)
		}
	}
	const bad = "[scheme]\nkind: roundrobbin\n"
	resp := scopedAPIRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/fest/%d/games", festID),
		map[string]any{"game_type": "ek", "dsl": bad}, token)
	if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "roundrobbin") {
		t.Fatalf("creating from a bad scheme: %d %s, want 400 naming the bad kind", resp.Code, resp.Body.String())
	}
	resp = scopedAPIRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/fest/%d/games", festID),
		map[string]any{"game_type": "ek", "dsl": "[scheme]\nkind: single_elimination\nparticipants: 4\nmatch_size: 4\nwinning_places: 2\n"}, token)
	if resp.Code != http.StatusOK {
		t.Fatalf("creating ЭК from a scheme: %d %s", resp.Code, resp.Body.String())
	}
	var game struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &game); err != nil {
		t.Fatal(err)
	}
	resp = scopedAPIRequest(t, srv, http.MethodPatch, fmt.Sprintf("/api/fest/%d/games/%d/settings", festID, game.ID),
		map[string]any{"scheme_dsl": bad}, token)
	if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "roundrobbin") {
		t.Fatalf("a bad scheme edit: %d %s, want 400 naming the bad kind", resp.Code, resp.Body.String())
	}
}

// A bracket Game is built from a scheme or not at all: the JSON twin ignores a
// pasted scheme, and the fest-wide scheme import is gone.
func TestNoGameIsCreatedFromPastedJSON(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	token := createTestSession(t, srv, systemUserID(t, srv.Eng().DB))
	resp := scopedAPIRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/fest/%d/games", festID),
		map[string]any{"game_type": "ek", "scheme": pastedEKScheme()}, token)
	if want := dopestrings.Default.Gamebuild.Create.SchemeRequired(); resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), want) {
		t.Fatalf("creating ЭК from pasted JSON: %d %s, want 400 saying %q", resp.Code, resp.Body.String(), want)
	}
	resp = scopedAPIRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/fest/%d/scheme-import", festID), pastedEKScheme(), token)
	if resp.Code != http.StatusNotFound && resp.Code != http.StatusMethodNotAllowed {
		t.Fatalf("the scheme import still answers: %d %s", resp.Code, resp.Body.String())
	}
}
