package localserver

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestImportFolder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Кубок")
	write := func(name, body string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("2 тур.4s", "? Второй вопрос\n! Ответ два\n")
	write("1 тур.4s", "### Первый тур\n\n? (img q1.png) Что на картинке?\n! Кот\n\n? Без картинки\n! Да\n")
	write("q1.png", "PNG-BYTES")

	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	board, err := s.Import(dir)
	if err != nil {
		t.Fatal(err)
	}

	var snap struct {
		Name  string `json:"name"`
		Lists []struct {
			ID    int64  `json:"id"`
			Title string `json:"title"`
		} `json:"lists"`
		Cards []struct {
			ID     int64  `json:"id"`
			ListID int64  `json:"list_id"`
			Kind   string `json:"kind"`
		} `json:"cards"`
	}
	get(t, s, fmt.Sprintf("/api/boards/%d", board), &snap)
	if snap.Name != "Кубок" || len(snap.Lists) != 2 || plainText(t, snap.Lists[0].Title) != "1 тур" || plainText(t, snap.Lists[1].Title) != "2 тур" {
		t.Fatalf("board %q, lists %+v", snap.Name, snap.Lists)
	}
	var kinds []string
	var first []int64
	for _, c := range snap.Cards {
		if c.ListID == snap.Lists[0].ID {
			kinds = append(kinds, c.Kind)
			first = append(first, c.ID)
		}
	}
	if fmt.Sprint(kinds) != "[heading question question]" {
		t.Fatalf("first list's kinds = %v", kinds)
	}
	var withPicture []struct{}
	get(t, s, fmt.Sprintf("/api/cards/%d/attachments", first[1]), &withPicture)
	var without []struct{}
	get(t, s, fmt.Sprintf("/api/cards/%d/attachments", first[2]), &without)
	if len(withPicture) != 1 || len(without) != 0 {
		t.Fatalf("attachments: %d on the card that names q1.png, %d on the other", len(withPicture), len(without))
	}
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

func plainText(t *testing.T, field string) string {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(field)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
