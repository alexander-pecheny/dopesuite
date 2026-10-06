package imports_test

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"dope/dope/domain/imports"
	"dope/dope/domain/schemedsl"
	dopeserver "dope/dope/server"
	"dope/dope/storage/store"
)

// Where a list comes from, for every kind of entrant: what it was last
// imported from wins, then the seed [init] declares, then the format's own
// default. A format that seats troikas defaults to the troikas, those of its
// division when [init] names one, and never to the fest's teams.
func TestSourceForEveryKindOfEntrant(t *testing.T) {
	none := imports.Declared{DSL: true}
	random := imports.Declared{Seed: "random", DSL: true}
	fromOD := imports.Declared{Seed: "od-1", Division: "Студ", DSL: true}
	players := imports.Declared{Seed: "players", DSL: true}
	troikaDivision := imports.Declared{Division: "Студ", DSL: true}
	stored := imports.ParseSource("ek-1", "-Студ")
	legacy := imports.ParseSource("ksi", "")
	cases := []struct {
		name     string
		kind     string
		stored   imports.Source
		declared imports.Declared
		want     imports.Source
	}{
		{"team, nothing", imports.KindTeam, imports.Source{}, none, imports.Source{Kind: imports.SourceFest}},
		{"player, nothing", imports.KindPlayer, imports.Source{}, none, imports.Source{Kind: imports.SourceFest}},
		{"troika, nothing", imports.KindTroika, imports.Source{}, none, imports.Source{Kind: imports.SourceTroikas}},
		{"troika, a division", imports.KindTroika, imports.Source{}, troikaDivision, imports.Source{Kind: imports.SourceTroikas, Division: "Студ"}},
		{"team, a lot declared", imports.KindTeam, imports.Source{}, random, imports.Source{Kind: imports.SourceRandom}},
		{"team, a Game declared", imports.KindTeam, imports.Source{}, fromOD, imports.Source{Kind: imports.SourceGame, Game: "od-1", Division: "Студ"}},
		{"troika, players declared", imports.KindTroika, imports.Source{}, players, imports.Source{Kind: imports.SourcePlayers}},
		{"team, stored over declared", imports.KindTeam, stored, fromOD, imports.Source{Kind: imports.SourceGame, Game: "ek-1", Division: "-Студ"}},
		{"troika, stored over the division", imports.KindTroika, imports.ParseSource("troikas", ""), troikaDivision, imports.Source{Kind: imports.SourceTroikas}},
		{"team, the legacy KSI word", imports.KindTeam, legacy, none, imports.Source{Kind: imports.SourceKSI}},
		{"player, stored fest", imports.KindPlayer, imports.ParseSource("fest", ""), none, imports.Source{Kind: imports.SourceFest}},
	}
	for _, c := range cases {
		if got := imports.SourceFor(c.kind, c.stored, c.declared); got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

// [init] read from the compiled scheme says the same as [init] read from the
// DSL, and a pasted scheme (no DSL) never follows its list or takes a
// division's troikas.
func TestDeclaredReadsTheCompiledScheme(t *testing.T) {
	cases := []struct {
		name, gameType, dsl string
		seeded, sized       bool
		division            string
	}{
		{"no init", "brain", "[init]\n", false, true, ""},
		{"a lot", "ek", "[init]\nseed: random\n", true, false, ""},
		{"a Game in one division", "es", "[init]\nseed: od-1\ndivision: Студ\n", true, false, ""},
		{"a troika division", "troika", "[init]\ndivision: Студ\n", false, true, "Студ"},
	}
	for _, c := range cases {
		doc, err := schemedsl.Parse(c.dsl)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		init, err := schemedsl.ReadInit(doc, c.gameType)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		parsed := imports.DeclaredOfInit(init)
		compiled := imports.DeclaredOf(store.FestScheme{Seeding: init.Seeding, Division: init.Division}, true)
		if !reflect.DeepEqual(parsed, compiled) {
			t.Errorf("%s: parsed %+v, compiled %+v", c.name, parsed, compiled)
		}
		division, _ := compiled.EntrantDivision()
		if compiled.Seeded() != c.seeded || compiled.EntrantSized() != c.sized || division != c.division {
			t.Errorf("%s: seeded %v sized %v division %q", c.name, compiled.Seeded(), compiled.EntrantSized(), division)
		}
		pasted := imports.DeclaredOf(store.FestScheme{Seeding: init.Seeding, Division: init.Division}, false)
		if _, divided := pasted.EntrantDivision(); pasted.EntrantSized() || divided {
			t.Errorf("%s: a pasted scheme follows its list: %+v", c.name, pasted)
		}
	}
}

// Nobody chosen: a team Game takes the fest's teams by number, an individual
// one the fest's players as they registered, and a Troika the troikas — of
// one division when it names one, less a troika about to be deleted.
func TestDefaultEntrantsByKind(t *testing.T) {
	db, err := dopeserver.OpenFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	owner, err := dopeserver.EnsureSystemUser(ctx, tx)
	if err != nil {
		t.Fatal(err)
	}
	festID, err := store.InsertReturningID(ctx, tx, `
insert into fests(slug, title, created_by, created_at, updated_at) values('fest', 'Фест', ?, '2026-10-06', '2026-10-06')`, owner)
	if err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) int64 {
		t.Helper()
		id, err := store.InsertReturningID(ctx, tx, query, args...)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	exec(`insert into fest_teams(fest_id, name, city, position, number) values(?, 'Бобры', '', 1, 2)`, festID)
	exec(`insert into fest_teams(fest_id, name, city, position, number) values(?, 'Зубры', '', 2, 1)`, festID)
	exec(`insert into fest_players(fest_id, first_name, last_name) values(?, 'Анна', 'Б')`, festID)
	exec(`insert into fest_players(fest_id, first_name, last_name) values(?, 'Борис', 'А')`, festID)
	troika := func(name, division string, applied int) int64 {
		return exec(`insert into participants(fest_id, roster, name, city, assembled, division, applied) values(?, 'team', ?, '', 1, ?, ?)`,
			festID, name, division, applied)
	}
	first := troika("Тройка 1", "Студ", 1)
	second := troika("Тройка 2", "", 2)
	third := troika("Тройка 3", "Студ", 3)

	names := func(kind, division string, exclude int64) []string {
		t.Helper()
		entrants, err := imports.DefaultEntrants(ctx, tx, festID, kind, division, exclude)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, e := range entrants {
			out = append(out, e.Name)
		}
		return out
	}
	for _, c := range []struct {
		name, kind, division string
		exclude              int64
		want                 []string
	}{
		{"teams by number", imports.KindTeam, "", 0, []string{"Зубры", "Бобры"}},
		{"players as they registered", imports.KindPlayer, "", 0, []string{"Анна Б", "Борис А"}},
		{"every troika", imports.KindTroika, "", 0, []string{"Тройка 1", "Тройка 2", "Тройка 3"}},
		{"one division's troikas", imports.KindTroika, "Студ", 0, []string{"Тройка 1", "Тройка 3"}},
		{"less the one deleted", imports.KindTroika, "", second, []string{"Тройка 1", "Тройка 3"}},
		{"in a division, less the one deleted", imports.KindTroika, "Студ", first, []string{"Тройка 3"}},
	} {
		if got := names(c.kind, c.division, c.exclude); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
	troikas, err := imports.DefaultEntrants(ctx, tx, festID, imports.KindTroika, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if troikas[2].ParticipantID != third {
		t.Errorf("a troika is named by its Participant: got %d, want %d", troikas[2].ParticipantID, third)
	}
}
