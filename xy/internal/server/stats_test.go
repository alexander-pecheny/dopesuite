package server

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

var cyrillic = regexp.MustCompile(`\p{Cyrillic}`)

func postStats(t *testing.T, ts *httptest.Server, c *apiClient, fields map[string]string, table string) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	if table != "" {
		fw, _ := mw.CreateFormFile("results", "results.csv")
		_, _ = fw.Write([]byte(table))
	}
	mw.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/api/stats/lines", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	for _, ck := range c.jar {
		req.AddCookie(ck)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func statsLines(t *testing.T, resp *http.Response) []string {
	t.Helper()
	defer resp.Body.Close()
	mustStatus(t, resp, 200)
	var got statsResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	return got.Lines
}

func TestStatsLines(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tournaments/5/results.json" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`[{"mask":"110","current":{"name":"Т1"},"team":{"id":1}},{"mask":"100","current":{"name":"Т2"},"team":{"id":2}}]`))
	}))
	defer site.Close()
	ts, srv := newTestServer(t)
	srv.ratingAPIBase = site.URL
	c := registerUser(t, srv, ts, 770911, "stats")

	lines := statsLines(t, postStats(t, ts, c, map[string]string{"rating_ids": "5", "count": "3"}, ""))
	want := []string{"Взятия: 2/2 (100%) (Т1, Т2)", "Взятия: 1/2 (50%) (Т1)", "Взятия: 0/2 (0%)"}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Fatalf("lines = %q", lines)
	}

	table := "Team ID,Название,Город,Тур,1,2,3\n1,Т1,М,1,1,1,0\n2,Т2,М,1,1,0,0\n"
	if got := statsLines(t, postStats(t, ts, c, map[string]string{"count": "3", "team_naming_threshold": "0"}, table)); len(got) != 3 || got[1] != "Взятия: 1/2 (50%)" {
		t.Fatalf("from a table = %q", got)
	}

	for _, fields := range []map[string]string{
		{"rating_ids": "../5", "count": "3"},
		{"count": "3"},
		{"rating_ids": "6", "count": "3"},
	} {
		resp := postStats(t, ts, c, fields, "")
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest || !cyrillic.Match(b) {
			t.Errorf("%v: %d %q, want a 400 a person can read", fields, resp.StatusCode, b)
		}
	}
}
