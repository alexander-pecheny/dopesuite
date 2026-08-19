package storeutil

import (
	"encoding/json"
	"strings"
	"testing"

	"dope/dope/storage/store"
)

func seed(n int) store.SchemeSlot { return store.SchemeSlot{Seed: &store.SchemeSeedRef{Number: n}} }

func validScheme() store.FestScheme {
	return store.FestScheme{
		Slug: "fest", Title: "Фест",
		Stages: []store.SchemeStage{{Code: "s1", Matches: []store.SchemeMatch{
			{Code: "m1", ParticipantCount: 2, Slots: []store.SchemeSlot{seed(1), seed(2)}},
		}}},
		Teams: []store.SchemeTeam{{Name: "A", Basket: 1, Number: 1}, {Name: "B", Basket: 1, Number: 2}},
	}
}

func TestValidateScheme(t *testing.T) {
	if err := ValidateScheme(validScheme()); err != nil {
		t.Fatalf("valid scheme rejected: %v", err)
	}
	cases := map[string]struct {
		mutate func(*store.FestScheme)
		want   string
	}{
		"no slug":           {func(s *store.FestScheme) { s.Slug = " " }, "slug"},
		"ek needs stages":   {func(s *store.FestScheme) { s.Stages = nil }, "stages are required"},
		"od may be flat":    {func(s *store.FestScheme) { s.Stages = nil; s.GameType = "od" }, ""},
		"dup stage":         {func(s *store.FestScheme) { s.Stages = append(s.Stages, s.Stages[0]) }, "duplicate stage"},
		"bad stage type":    {func(s *store.FestScheme) { s.Stages[0].StageType = "swiss" }, "bad stage_type"},
		"reseed no matches": {func(s *store.FestScheme) { s.Stages[0].StageType = "reseed"; s.Stages[0].Matches = nil }, ""},
		"slot count":        {func(s *store.FestScheme) { s.Stages[0].Matches[0].ParticipantCount = 3 }, "participantCount"},
		"removed team src": {func(s *store.FestScheme) {
			s.Stages[0].Matches[0].Slots[0] = store.SchemeSlot{Team: &store.SchemeTeamRef{Name: "x"}}
		}, "removed source"},
		"seed zero":      {func(s *store.FestScheme) { s.Stages[0].Matches[0].Slots[0] = seed(0) }, "bad seed number"},
		"team collision": {func(s *store.FestScheme) { s.Teams[1].Number = 1 }, "collides"},
		"team no basket": {func(s *store.FestScheme) { s.Teams[1].Basket = 0 }, "basket>=1"},
	}
	for name, c := range cases {
		s := validScheme()
		c.mutate(&s)
		err := ValidateScheme(s)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: unexpected %v", name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
}

func TestPKWhere(t *testing.T) {
	where, args, err := PKWhere([]string{"fest_id", `we"ird`}, map[string]any{"fest_id": 1, `we"ird`: "x", "other": 2})
	if err != nil || where != `"fest_id" = ? and "we""ird" = ?` || len(args) != 2 || args[0] != 1 || args[1] != "x" {
		t.Fatalf("got %q %v %v", where, args, err)
	}
	if _, _, err := PKWhere(nil, nil); err == nil {
		t.Fatal("no pk accepted")
	}
	if _, _, err := PKWhere([]string{"id"}, map[string]any{}); err == nil {
		t.Fatal("missing pk column accepted")
	}
}

func TestJSONToSQLValue(t *testing.T) {
	cases := []struct{ in, want any }{
		{nil, nil},
		{json.Number("42"), int64(42)},
		{json.Number("4.5"), 4.5},
		{json.Number("1e3"), 1000.0},
		{json.Number("99999999999999999999"), 1e20},
		{"s", "s"},
		{true, true},
	}
	for _, c := range cases {
		if got := JSONToSQLValue(c.in); got != c.want {
			t.Errorf("%v: got %#v want %#v", c.in, got, c.want)
		}
	}
}

func TestSortedKeys(t *testing.T) {
	got := SortedKeys(map[string]any{"b": 1, "a": 2, "c": 3})
	if strings.Join(got, ",") != "a,b,c" {
		t.Fatalf("got %v", got)
	}
}
