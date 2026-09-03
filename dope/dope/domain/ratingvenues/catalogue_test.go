package ratingvenues

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// venuesJSON is one page of the rating site's answer.
func venuesJSON(rows ...[3]string) string {
	var out []string
	for _, r := range rows {
		out = append(out, fmt.Sprintf(`{"id":%s,"name":%q,"town":{"name":%q}}`, r[0], r[1], r[2]))
	}
	return "[" + strings.Join(out, ",") + "]"
}

func testSite(t *testing.T, pages map[string]string) *Catalogue {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimSuffix(r.URL.Path+"?"+r.URL.RawQuery, "?")
		body, ok := pages[key]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return &Catalogue{HTTP: srv.Client(), Base: srv.URL}
}

const firstPage = "/venues?itemsPerPage=512&page=1"

// The catalogue is searched by id, by name and by the town a venue is in.
func TestSearch(t *testing.T) {
	c := testSite(t, map[string]string{firstPage: venuesJSON(
		[3]string{"3152", "Тбилиси", "Тбилиси"},
		[3]string{"4714", "Тбилиси / Горбунов Тимур", "Тбилиси"},
		[3]string{"3541", "Больбес", "Москва"},
	)})
	cases := []struct {
		query string
		want  []int64
	}{
		{"тбилиси", []int64{3152, 4714}},
		{"горбунов", []int64{4714}},
		{"москва", []int64{3541}},
		{"3541", []int64{3541}},
		{"нет такого", nil},
	}
	for _, c2 := range cases {
		found, err := c.Search(context.Background(), c2.query, 20)
		if err != nil {
			t.Fatalf("%s: %v", c2.query, err)
		}
		var got []int64
		for _, v := range found {
			got = append(got, v.ID)
		}
		if fmt.Sprint(got) != fmt.Sprint(c2.want) {
			t.Errorf("%q: %v, want %v", c2.query, got, c2.want)
		}
	}
}

// A venue registered since the copy was taken is not in it; the id still
// resolves, from the site itself.
func TestLookupAsksTheSiteForWhatItDoesNotHave(t *testing.T) {
	c := testSite(t, map[string]string{
		firstPage:     venuesJSON([3]string{"3152", "Тбилиси", "Тбилиси"}),
		"/venues/999": `{"id":999,"name":"Новая","town":{"name":"Ереван"}}`,
	})
	venue, err := c.Lookup(context.Background(), 999)
	if err != nil || venue.Name != "Новая" || venue.Town != "Ереван" {
		t.Fatalf("%+v %v", venue, err)
	}
	if _, err := c.Lookup(context.Background(), 1); err != ErrNoVenue {
		t.Fatalf("unknown venue: %v", err)
	}
	if _, err := c.Lookup(context.Background(), 0); err != ErrNoVenue {
		t.Fatalf("no venue: %v", err)
	}
}

// Every page is read, and a short one ends the walk.
func TestFetchAllWalksPages(t *testing.T) {
	full := make([][3]string, 0, pageSize)
	for i := 0; i < pageSize; i++ {
		full = append(full, [3]string{fmt.Sprint(1000 + i), fmt.Sprintf("Площадка %d", i), "Москва"})
	}
	c := testSite(t, map[string]string{
		firstPage:                         venuesJSON(full...),
		"/venues?itemsPerPage=512&page=2": venuesJSON([3]string{"9000", "Последняя", "Ереван"}),
	})
	found, err := c.Search(context.Background(), "последняя", 20)
	if err != nil || len(found) != 1 || found[0].ID != 9000 {
		t.Fatalf("%+v %v", found, err)
	}
}

// A site that cannot be reached is said so, once there is nothing to answer with.
func TestSearchWithoutTheSite(t *testing.T) {
	c := testSite(t, nil)
	if _, err := c.Search(context.Background(), "тбилиси", 20); err != ErrUnavailable {
		t.Fatalf("%v", err)
	}
}

// The rows are what the endpoint hands the suggest.
func TestVenueJSON(t *testing.T) {
	body, _ := json.Marshal(Venue{ID: 1, Name: "Тбилиси", Town: "Тбилиси"})
	if string(body) != `{"id":1,"name":"Тбилиси","town":"Тбилиси"}` {
		t.Fatal(string(body))
	}
}
