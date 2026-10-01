package tests

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"testing"

	"dope/dope/domain/gamebuild"
	"dope/dope/domain/roster"

	corei18n "pecheny.me/dopecore/i18nstrings"
)

// Troikas are assembled out of fest players: they are Participants the rating
// roster does not have, a troika of two or more players of one team counts
// for that team, and a Тройка Game seats them like any other entrant.
func TestTroikasAreAssembledFromFestPlayers(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	festTeamWithPlayers(t, db, festID, "Альфа", [][2]string{{"Иван", "Петров"}, {"Анна", "Сидорова"}, {"Олег", "Кузнецов"}})

	inputs, err := roster.ParseAssembledLines("Бобры: Иван Петров (Альфа), Анна Сидорова, Ия Ли\n\nЕжи: Олег Кузнецов, Ян Ким\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 2 || inputs[0].Name != "Бобры" || len(inputs[0].Players) != 3 {
		t.Fatalf("parsed %+v", inputs)
	}
	withTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
		for _, in := range inputs {
			if _, err := roster.SaveAssembledTx(ctx, tx, festID, 0, in); err != nil {
				return err
			}
		}
		return nil
	})
	troikas, err := roster.LoadAssembled(t.Context(), db, festID)
	if err != nil {
		t.Fatal(err)
	}
	if len(troikas) != 2 {
		t.Fatalf("troikas = %+v", troikas)
	}
	bobry, ezhi := troikas[0], troikas[1]
	if bobry.Name != "Бобры" || strings.Join(bobry.Players, ", ") != "Иван Петров, Анна Сидорова, Ия Ли" || bobry.HeadTeam != "Альфа" {
		t.Fatalf("Бобры = %+v", bobry)
	}
	if ezhi.HeadTeam != "" {
		t.Fatalf("Ежи have one Альфа player and count for nobody, got %q", ezhi.HeadTeam)
	}

	// The rules a host is told about.
	for _, bad := range []roster.AssembledInput{
		{Name: "Один", Players: []string{"Ия Ли"}},
		{Name: "Пятеро", Players: []string{"А Б", "В Г", "Д Е", "Ж З", "И К"}},
		{Name: "Бобры", Players: []string{"А Б", "В Г"}},
		{Name: "Дубль", Players: []string{"Ия Ли", "Ия  Ли"}},
	} {
		err := saveErr(t, db, festID, 0, bad)
		if _, user := corei18n.AsUser(err); !user {
			t.Errorf("%s: err = %v, want a message for the host", bad.Name, err)
		}
	}

	// A substitution: Ежи take a third player.
	withTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
		_, err := roster.SaveAssembledTx(ctx, tx, festID, ezhi.ID, roster.AssembledInput{Name: "Ежи", Players: []string{"Олег Кузнецов", "Ян Ким", "Иван Петров"}})
		return err
	})

	// A Тройка Game seats them, and a seated troika cannot be deleted.
	gameID := createSchemeGameFor(t, db, festID, "troika", "Тройка",
		"[scheme]\nkind: single_elimination\nparticipants: 2\nthemes: 1\n", []int64{bobry.ID, ezhi.ID})
	if got := len(gameEntrants(t, db, gameID)); got != 2 {
		t.Fatalf("entrants = %d", got)
	}
	var roster3 int
	if err := db.QueryRow(`select count(*) from participant_players where participant_id = ?`, ezhi.ID).Scan(&roster3); err != nil || roster3 != 3 {
		t.Fatalf("Ежи roster = %d, %v", roster3, err)
	}
	err = func() error {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		return roster.DeleteAssembledTx(t.Context(), tx, festID, bobry.ID)
	}()
	if _, user := corei18n.AsUser(err); !user {
		t.Fatalf("deleting a seated troika: %v", err)
	}
}

func festTeamWithPlayers(t *testing.T, db *sql.DB, festID int64, name string, players [][2]string) int64 {
	t.Helper()
	res, err := db.Exec(`insert into fest_teams(fest_id, name, city, position) values(?, ?, '', 1)`, festID, name)
	if err != nil {
		t.Fatal(err)
	}
	teamID, _ := res.LastInsertId()
	for i, p := range players {
		res, err := db.Exec(`insert into fest_players(fest_id, first_name, last_name) values(?, ?, ?)`, festID, p[0], p[1])
		if err != nil {
			t.Fatal(err)
		}
		playerID, _ := res.LastInsertId()
		if _, err := db.Exec(`insert into fest_team_players(team_id, player_id, roster_order) values(?, ?, ?)`, teamID, playerID, i); err != nil {
			t.Fatal(err)
		}
	}
	return teamID
}

func withTx(t *testing.T, db *sql.DB, fn func(ctx context.Context, tx *sql.Tx) error) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := fn(t.Context(), tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func saveErr(t *testing.T, db *sql.DB, festID, id int64, in roster.AssembledInput) error {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	_, err = roster.SaveAssembledTx(t.Context(), tx, festID, id, in)
	return err
}

// A troika's head team and зачёт are stored: a new troika takes the team that
// holds two of its players and follows that team's зачёт, and the host can
// pick another team or set the зачёт apart from it.
func TestTroikaHeadTeamAndDivision(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	alpha := festTeamWithPlayers(t, db, festID, "Альфа", [][2]string{{"Иван", "Петров"}, {"Анна", "Сидорова"}})
	beta := festTeamWithPlayers(t, db, festID, "Бета", [][2]string{{"Олег", "Кузнецов"}, {"Ян", "Ким"}})
	festTeamFlag(t, db, alpha, "Студ")

	var id int64
	withTx(t, db, func(ctx context.Context, tx *sql.Tx) (err error) {
		id, err = roster.SaveAssembledTx(ctx, tx, festID, 0, roster.AssembledInput{Name: "Смесь", Players: []string{"Иван Петров", "Анна Сидорова", "Ян Ким"}})
		return err
	})
	troika := assembledByID(t, db, festID, id)
	if troika.HeadTeamID != alpha || !troika.FollowsTeam() || strings.Join(troika.Flags, ",") != "Студ" {
		t.Fatalf("new troika = %+v, want Альфа and its Студ", troika)
	}

	// The host moves it to Бета: it follows Бета's зачёт, which is none.
	save := func(placement roster.AssembledPlacement) {
		withTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
			_, err := roster.SaveAssembledTx(ctx, tx, festID, id, roster.AssembledInput{Name: "Смесь", Players: troika.Players, Placement: &placement})
			return err
		})
	}
	save(roster.AssembledPlacement{HeadTeamID: beta})
	if troika = assembledByID(t, db, festID, id); troika.HeadTeam != "Бета" || len(troika.Flags) != 0 {
		t.Fatalf("moved to Бета = %+v", troika)
	}
	// No team, but the student зачёт by the host's word.
	stud := "Студ"
	save(roster.AssembledPlacement{Division: &stud})
	if troika = assembledByID(t, db, festID, id); troika.HeadTeamID != 0 || troika.FollowsTeam() || strings.Join(troika.Flags, ",") != "Студ" {
		t.Fatalf("no team, Студ = %+v", troika)
	}
	// Pasted lines never touch what the dialog set.
	withTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
		_, err := roster.SaveAssembledTx(ctx, tx, festID, id, roster.AssembledInput{Name: "Смесь", Players: troika.Players})
		return err
	})
	if troika = assembledByID(t, db, festID, id); troika.FollowsTeam() || strings.Join(troika.Flags, ",") != "Студ" {
		t.Fatalf("after a plain save = %+v", troika)
	}
	if err := saveErr(t, db, festID, id, roster.AssembledInput{Name: "Смесь", Players: troika.Players, Placement: &roster.AssembledPlacement{HeadTeamID: 999}}); err == nil {
		t.Fatal("a head team the fest does not have was accepted")
	}
}

// A Тройка Game that declares a зачёт seats that зачёт's troikas and follows
// them as they are added, moved or deleted, until something is entered in it.
func TestTroikaGameFollowsItsDivision(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	alpha := festTeamWithPlayers(t, db, festID, "Альфа", [][2]string{{"А", "Один"}, {"А", "Два"}, {"А", "Три"}, {"А", "Четыре"}, {"А", "Пять"}, {"А", "Шесть"}, {"А", "Семь"}, {"А", "Восемь"}})
	festTeamWithPlayers(t, db, festID, "Бета", [][2]string{{"Б", "Один"}, {"Б", "Два"}, {"Б", "Три"}, {"Б", "Четыре"}})
	festTeamFlag(t, db, alpha, "Студ")

	add := func(name string, players ...string) int64 {
		var id int64
		withTx(t, db, func(ctx context.Context, tx *sql.Tx) (err error) {
			if id, err = roster.SaveAssembledTx(ctx, tx, festID, 0, roster.AssembledInput{Name: name, Players: players}); err != nil {
				return err
			}
			_, err = gamebuild.SyncDivisionEntrantsTx(ctx, tx, festID, 0)
			return err
		})
		return id
	}
	s1 := add("С1", "А Один", "А Два")
	s2 := add("С2", "А Три", "А Четыре")
	a1 := add("В1", "Б Один", "Б Два")
	a2 := add("В2", "Б Три", "Б Четыре")

	const scheme = "\n[scheme]\nkind: flat\nwritten: true\nthemes: 1\nletters: false\n"
	// The picker's choice does not matter: the зачёт decides.
	students := createSchemeGameFor(t, db, festID, "troika", "Тройка — студенты", "[init]\ndivision: Студ\n"+scheme, []int64{a1})
	adults := createSchemeGameFor(t, db, festID, "troika", "Тройка — взрослые", "[init]\ndivision: -Студ\n"+scheme, nil)
	expectEntrants := func(gameID int64, want ...int64) {
		t.Helper()
		if got := gameEntrants(t, db, gameID); !slices.Equal(got, want) {
			t.Fatalf("game %d entrants = %v, want %v", gameID, got, want)
		}
		if got := matchSeatIDs(t, db, gameID, "s1-m1"); !slices.Equal(got, want) {
			t.Fatalf("game %d отбор seats = %v, want %v", gameID, got, want)
		}
	}
	expectEntrants(students, s1, s2)
	expectEntrants(adults, a1, a2)

	// A new student troika joins the student game.
	s3 := add("С3", "А Пять", "А Шесть")
	expectEntrants(students, s1, s2, s3)

	// Out of the зачёт by the host's word: it moves to the adults.
	none := ""
	withTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := roster.SaveAssembledTx(ctx, tx, festID, s3, roster.AssembledInput{Name: "С3", Players: []string{"А Пять", "А Шесть"},
			Placement: &roster.AssembledPlacement{HeadTeamID: alpha, Division: &none}}); err != nil {
			return err
		}
		_, err := gamebuild.SyncDivisionEntrantsTx(ctx, tx, festID, 0)
		return err
	})
	expectEntrants(students, s1, s2)
	expectEntrants(adults, a1, a2, s3)

	// A troika only a following game seats can still be deleted.
	withTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := gamebuild.SyncDivisionEntrantsTx(ctx, tx, festID, s3); err != nil {
			return err
		}
		return roster.DeleteAssembledTx(ctx, tx, festID, s3)
	})
	expectEntrants(adults, a1, a2)

	// The отбор has results, and a late troika still writes it: it gets an
	// empty row of its own, and the rows there keep what they hold.
	if _, err := db.Exec(`
update matches set status = 'finished', state_json = json_set(state_json, '$.sides[0].counts[0][0]', 2) where game_id = ?`, students); err != nil {
		t.Fatal(err)
	}
	s4 := add("С4", "А Семь", "А Восемь")
	expectEntrants(students, s1, s2, s4)
	var counts string
	if err := db.QueryRow(`select json_extract(state_json, '$.sides') from matches where game_id = ? and code = 's1-m1'`, students).Scan(&counts); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(counts, `[{"counts":[[2,0,0]]}`) || strings.Count(counts, `"counts"`) != 3 {
		t.Fatalf("отбор rows after a late troika = %s, want the first row's 2 kept and three rows", counts)
	}
	// It has a row in an отбор that has results now, so it cannot be deleted.
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := gamebuild.DropTroikaFromListsTx(t.Context(), tx, festID, s4); err != nil {
		t.Fatal(err)
	}
	if _, user := corei18n.AsUser(roster.DeleteAssembledTx(t.Context(), tx, festID, s4)); !user {
		t.Fatal("a troika with a row in a started отбор was deleted")
	}
	tx.Rollback()

	expectEntrants(students, s1, s2, s4)
	divisionGames, err := gamebuild.LoadDivisionGames(t.Context(), db, festID)
	if err != nil {
		t.Fatal(err)
	}
	if len(divisionGames) != 2 || !divisionGames[0].Frozen || !divisionGames[0].Current || !divisionGames[1].Current {
		t.Fatalf("division games = %+v", divisionGames)
	}

	// A зачёт without a troika yet still makes a game: its отбор waits with
	// empty seats, and the troikas take them as they are entered.
	school := createSchemeGameFor(t, db, festID, "troika", "Тройка — школьники", "[init]\ndivision: Школ\n"+scheme, nil)
	var slots, seated int
	if err := db.QueryRow(`
select count(*), count(ms.participant_id) from match_slots ms join matches m on m.id = ms.match_id
where m.game_id = ?`, school).Scan(&slots, &seated); err != nil || slots == 0 || seated != 0 {
		t.Fatalf("the empty зачёт's отбор: %d seats, %d taken (%v), want empty seats", slots, seated, err)
	}
	gamma := festTeamWithPlayers(t, db, festID, "Гамма", [][2]string{{"Г", "Один"}, {"Г", "Два"}, {"Г", "Три"}, {"Г", "Четыре"}})
	festTeamFlag(t, db, gamma, "Школ")
	k1 := add("Ш1", "Г Один", "Г Два")
	k2 := add("Ш2", "Г Три", "Г Четыре")
	expectEntrants(school, k1, k2)
}

func festTeamFlag(t *testing.T, db *sql.DB, teamID int64, short string) {
	t.Helper()
	if _, err := db.Exec(`insert into fest_team_flags(team_id, position, short, full) values(?, 1, ?, ?)`, teamID, short, short); err != nil {
		t.Fatal(err)
	}
}

func assembledByID(t *testing.T, db *sql.DB, festID, id int64) roster.Assembled {
	t.Helper()
	all, err := roster.LoadAssembled(t.Context(), db, festID)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range all {
		if a.ID == id {
			return a
		}
	}
	t.Fatalf("no troika %d", id)
	return roster.Assembled{}
}
