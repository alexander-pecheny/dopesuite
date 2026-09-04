package ratingtournaments

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDetailsSumsTheRequestsAndNamesTheEditors(t *testing.T) {
	var calls atomic.Int64
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case "/tournaments/1":
			_, _ = w.Write([]byte(`{"difficultyForecast":3.5,
"editors":[{"id":1,"name":"Максим","patronymic":"Петрович","surname":"Мерзляков"},
           {"id":2,"name":"Анна","surname":"Коробейникова"}]}`))
		case "/tournaments/1/requests":
			// A turned-down request is not a team anyone expects to see.
			_, _ = w.Write([]byte(`[{"status":"A","approximateTeamsCount":3},
{"status":"N","approximateTeamsCount":4},{"status":"R","approximateTeamsCount":99}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer site.Close()

	c := &Catalogue{Base: site.URL, HTTP: site.Client()}
	got := c.Details(context.Background(), []int64{1, 2})
	d, ok := got[1]
	if !ok {
		t.Fatalf("no detail for 1: %+v", got)
	}
	if d.Difficulty != 3.5 || d.Teams != 7 {
		t.Errorf("difficulty %v teams %d", d.Difficulty, d.Teams)
	}
	if strings.Join(d.Editors, ", ") != "Максим Мерзляков, Анна Коробейникова" {
		t.Errorf("editors %q", d.Editors)
	}
	// An id the site will not answer for is left out; the rest still answer.
	if _, ok := got[2]; ok {
		t.Error("a tournament the site denied still got a detail")
	}

	// Asked again inside the window, the site is not asked again.
	before := calls.Load()
	if again := c.Details(context.Background(), []int64{1}); again[1].Teams != 7 {
		t.Errorf("cached %+v", again[1])
	}
	if calls.Load() != before {
		t.Errorf("%d calls after the cache was warm", calls.Load()-before)
	}
}

func TestName(t *testing.T) {
	for _, c := range []struct{ name, surname, want string }{
		{"Максим", "Мерзляков", "Максим Мерзляков"},
		{"Эмиль", "", "Эмиль"},
		{"", "Голуб", "Голуб"},
		{"", "", ""},
	} {
		if got := Name(c.name, c.surname); got != c.want {
			t.Errorf("Name(%q,%q) = %q, want %q", c.name, c.surname, got, c.want)
		}
	}
}
