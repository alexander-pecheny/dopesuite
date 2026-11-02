package ratingexport

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"dope/dope/storage/ratingsite"
)

// The site's search keeps punctuation, so «Джерси М» finds «Джерси-М» only
// by its longest word; an id a person types counts once the site has it.
func TestANamesakeDiffersOnlyInPunctuation(t *testing.T) {
	t.Parallel()
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/teams" && r.URL.Query().Get("name") == "джерси":
			fmt.Fprint(w, `[{"id": 5379, "name": "Джерси-М", "town": {"id": 277, "name": "Ростов-на-Дону"}}, {"id": 1, "name": "Бесы в джерси"}]`)
		case r.URL.Path == "/teams/5379":
			fmt.Fprint(w, `{"id": 5379}`)
		case r.URL.Path == "/teams":
			fmt.Fprint(w, `[]`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(site.Close)
	s := Site{Client: ratingsite.Client{Base: site.URL}}
	got, err := s.teamNamesakes(t.Context(), "Джерси М")
	if err != nil || len(got) != 1 || got[0].ID != 5379 {
		t.Fatalf("namesakes %+v, %v", got, err)
	}
	team := Unknown{Name: "Джерси М"}
	if id, err := Identify(t.Context(), s, 1, team, " 5379 "); err != nil || id != 5379 {
		t.Fatalf("a typed id: %d, %v", id, err)
	}
	if _, err := Identify(t.Context(), s, 1, team, "5380"); err == nil {
		t.Fatal("an id the site lacks went through")
	}
}
