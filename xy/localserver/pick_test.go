package localserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestPickFile: the board's import in the app gets the picked .4s back with the
// pictures beside it that it names, and a cancelled dialog is a 204.
func TestPickFile(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"тур.4s":    "? (img pics/q1.png) Что это?\n! Кот\n\n? (img q2.png) А это?\n! Пёс\n",
		"q1.png":    "PNG-1",
		"other.png": "not named",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s, err := Open(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	picked := filepath.Join(dir, "тур.4s")
	s.SetFilePicker(func() (string, error) { return picked, nil })

	var me struct {
		Instance struct {
			Pick bool `json:"pick"`
		} `json:"instance"`
	}
	get(t, s, "/api/auth/me", &me)
	if !me.Instance.Pick {
		t.Fatal("/api/auth/me does not offer the picker")
	}

	rec := pick(s)
	var body struct {
		Files []struct{ Name, MIME, Data string } `json:"files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("pick: %d %s", rec.Code, rec.Body)
	}
	if len(body.Files) != 2 || body.Files[0].Name != "тур.4s" || body.Files[1].Name != "q1.png" || body.Files[1].MIME != "image/png" {
		t.Fatalf("files %+v", body.Files)
	}

	picked = ""
	if rec := pick(s); rec.Code != http.StatusNoContent {
		t.Fatalf("cancelled pick: %d", rec.Code)
	}
}

func pick(s *Server) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/import/pick", nil))
	return rec
}

func get(t *testing.T, s *Server, path string, into any) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), into); err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
}
