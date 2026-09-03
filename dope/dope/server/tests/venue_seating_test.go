package tests

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"dope/dope/domain/flatgame"
	"dope/dope/domain/roster"
	"dope/dope/domain/venues"
	"dope/dope/export/xlsxexport"
	"dope/dope/platform/util"
	dopeserver "dope/dope/server"
	"dope/dope/storage/store"
)

// Accepting a Заявка seats the team in the Слот's Game with the next free
// Number; declining unseats it, unless the Game already scored it.
func TestSlotSeatingFollowsTheAcceptedApplications(t *testing.T) {
	db := venueTestDB(t)
	festID, slot := newVenueSlot(t, db, []int{2, 2})
	alice := newVenueUser(t, db, "alice")
	bob := newVenueUser(t, db, "bob")

	fileApplication(t, db, slot, alice, "Мантисса", 5723, []venues.RosterPlayer{
		{PlayerID: 1, Surname: "Иванов", Name: "Иван", Captain: true},
		{PlayerID: 2, Surname: "Петров", Name: "Пётр"},
	})
	fileApplication(t, db, slot, bob, "Вторая", 0, []venues.RosterPlayer{
		{PlayerID: 0, Surname: "Сидоров", Name: "Сидор", Captain: true},
	})

	setStatus(t, db, slot, alice, venues.StatusAccepted)
	setStatus(t, db, slot, bob, venues.StatusAccepted)

	if got := numbersByTeam(t, db, festID); got["Мантисса"] != 1 || got["Вторая"] != 2 {
		t.Fatalf("numbers %v", got)
	}
	// The registry gains the team the заявка named by its rating id, and the
	// Состав reaches the fest roster.
	var teamID int64
	if err := db.QueryRow(`select id from fest_teams where fest_id = ? and rating_id = 5723`, festID).Scan(&teamID); err != nil {
		t.Fatalf("registry row: %v", err)
	}
	var players int
	if err := db.QueryRow(`
select count(*) from game_team_players gtp
join participants p on p.id = gtp.participant_id
where p.fest_id = ? and p.number = 1`, festID).Scan(&players); err != nil {
		t.Fatal(err)
	}
	if players != 2 {
		t.Fatalf("game_team_players = %d", players)
	}

	// Declining frees the seat, and the next acceptance reuses the number.
	setStatus(t, db, slot, bob, venues.StatusDeclined)
	if got := numbersByTeam(t, db, festID); got["Вторая"] != 0 {
		t.Fatalf("declined team still seated: %v", got)
	}
	carol := newVenueUser(t, db, "carol")
	fileApplication(t, db, slot, carol, "Третья", 0, nil)
	setStatus(t, db, slot, carol, venues.StatusAccepted)
	if got := numbersByTeam(t, db, festID); got["Третья"] != 2 {
		t.Fatalf("the freed number was not reused: %v", got)
	}
}

func TestUnseatingAScoredTeamIsRefused(t *testing.T) {
	db := venueTestDB(t)
	festID, slot := newVenueSlot(t, db, []int{2})
	alice := newVenueUser(t, db, "alice")
	fileApplication(t, db, slot, alice, "Мантисса", 0, nil)
	setStatus(t, db, slot, alice, venues.StatusAccepted)

	if _, err := db.Exec(`
update matches set state_json = ?
where game_id = ? and code = 'main'`,
		`{"teams":[{"name":"Мантисса","city":"","number":1}],"entries":[[1],[]],"completed":[true,false],"shootoutRounds":[]}`,
		slot.GameID); err != nil {
		t.Fatal(err)
	}
	err := inTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return venues.SetStatusTx(ctx, tx, slot, "", applicationID(t, db, slot, alice), venues.StatusDeclined)
	})
	if !errors.Is(err, venues.ErrHasResults) {
		t.Fatalf("err = %v, want ErrHasResults", err)
	}
	if got := numbersByTeam(t, db, festID); got["Мантисса"] != 1 {
		t.Fatalf("the team was unseated anyway: %v", got)
	}
}

// A new version of an accepted заявка rewrites the seated team's roster.
func TestRosterVersionAfterAcceptanceRewritesTheRoster(t *testing.T) {
	db := venueTestDB(t)
	festID, slot := venueSlotWithAcceptedTeam(t, db)
	fileApplicationFor(t, db, slot, "alice", "Мантисса", 5723, []venues.RosterPlayer{
		{PlayerID: 3, Surname: "Новиков", Name: "Новик", Captain: true},
	})
	if err := inTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return venues.ReseatTx(ctx, tx, slot, "Тбилиси")
	}); err != nil {
		t.Fatal(err)
	}
	var last string
	if err := db.QueryRow(`
select p.last_name from game_team_players gtp
join players p on p.id = gtp.player_id
join participants t on t.id = gtp.participant_id
where t.fest_id = ?`, festID).Scan(&last); err != nil {
		t.Fatal(err)
	}
	if last != "Новиков" {
		t.Fatalf("roster is %q, want the new version", last)
	}
}

// ---- fixtures ----------------------------------------------------------------

func venueTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := dopeserver.OpenFestDB(filepath.Join(t.TempDir(), "venue.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func inTx(t *testing.T, db *sql.DB, fn func(context.Context, *sql.Tx) error) error {
	t.Helper()
	ctx := t.Context()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := fn(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func newVenueFest(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	now := util.UtcNow()
	result, err := db.Exec(`
insert into fests(slug, title, description, kind, city, created_by, revision, created_at, updated_at, is_public)
values(null, 'Площадка', '', 'venue', 'Тбилиси', null, 1, ?, ?, 1)`, now, now)
	if err != nil {
		t.Fatal(err)
	}
	festID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return festID
}

func newVenueSlot(t *testing.T, db *sql.DB, comp []int) (int64, venues.Slot) {
	t.Helper()
	festID := newVenueFest(t, db)
	var slotID int64
	if err := inTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		slotID, err = venues.CreateSlotTx(ctx, tx, festID, "2026-09-04 19:00", 0, "", comp)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// A Слот is born with its registration shut; these tests are about what
	// happens after a Representative opens it.
	if _, err := db.Exec(`update slots set reg_closed = 0 where id = ?`, slotID); err != nil {
		t.Fatal(err)
	}
	slot, err := venues.LoadSlot(t.Context(), db, slotID)
	if err != nil {
		t.Fatal(err)
	}
	return festID, slot
}

// A new Слот has a reg token from the first moment, so it must not also have an
// open registration: the Representative picks the турнир and the date first.
func TestNewSlotStartsWithRegistrationClosed(t *testing.T) {
	db := venueTestDB(t)
	festID := newVenueFest(t, db)
	var slotID int64
	if err := inTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		slotID, err = venues.CreateSlotTx(ctx, tx, festID, "2026-09-04 19:00", 0, "", []int{2})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	slot, err := venues.LoadSlot(t.Context(), db, slotID)
	if err != nil {
		t.Fatal(err)
	}
	if !slot.RegClosed {
		t.Error("a fresh Слот takes заявки before anyone opened it")
	}
	if venues.Registration(slot.RegOpensAt, slot.RegClosed, time.Now().UTC()) != venues.RegClosed {
		t.Error("the registration state disagrees with the flag")
	}
}

func newVenueUser(t *testing.T, db *sql.DB, name string) int64 {
	t.Helper()
	now := util.UtcNow()
	result, err := db.Exec(`insert into users(username, is_system, created_at, updated_at) values(?, 0, ?, ?)`, name, now, now)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func fileApplication(t *testing.T, db *sql.DB, slot venues.Slot, userID int64, name string, ratingID int64, roster []venues.RosterPlayer) {
	t.Helper()
	if err := inTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
		_, err := venues.SaveVersionTx(ctx, tx, slot.ID, userID, userID, name, ratingID, roster)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func fileApplicationFor(t *testing.T, db *sql.DB, slot venues.Slot, username, name string, ratingID int64, roster []venues.RosterPlayer) {
	t.Helper()
	var userID int64
	if err := db.QueryRow(`select id from users where username = ?`, username).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	fileApplication(t, db, slot, userID, name, ratingID, roster)
}

func applicationID(t *testing.T, db *sql.DB, slot venues.Slot, userID int64) int64 {
	t.Helper()
	app, err := venues.UserApplication(t.Context(), db, slot.ID, userID)
	if err != nil {
		t.Fatal(err)
	}
	return app.ID
}

func setStatus(t *testing.T, db *sql.DB, slot venues.Slot, userID int64, status string) {
	t.Helper()
	if err := inTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return venues.SetStatusTx(ctx, tx, slot, "Тбилиси", applicationID(t, db, slot, userID), status)
	}); err != nil {
		t.Fatal(err)
	}
}

func numbersByTeam(t *testing.T, db *sql.DB, festID int64) map[string]int64 {
	t.Helper()
	rows, err := db.Query(`select name, coalesce(number, 0) from participants where fest_id = ?`, festID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var name string
		var number int64
		if err := rows.Scan(&name, &number); err != nil {
			t.Fatal(err)
		}
		out[name] = number
	}
	return out
}

func venueSlotWithAcceptedTeam(t *testing.T, db *sql.DB) (int64, venues.Slot) {
	t.Helper()
	festID, slot := newVenueSlot(t, db, []int{2})
	alice := newVenueUser(t, db, "alice")
	fileApplication(t, db, slot, alice, "Мантисса", 5723, []venues.RosterPlayer{
		{PlayerID: 1, Surname: "Иванов", Name: "Иван", Captain: true},
	})
	setStatus(t, db, slot, alice, venues.StatusAccepted)
	return festID, slot
}

// Two Слоты of one Venue both number their teams from 1, and neither renames
// the other's: a Слот's Participants are its Game's, not the фест's.
func TestTwoSlotsBothNumberFromOne(t *testing.T) {
	db := venueTestDB(t)
	festID, first := newVenueSlot(t, db, []int{2})
	alice := newVenueUser(t, db, "alice")
	fileApplication(t, db, first, alice, "Мантисса", 0, nil)
	setStatus(t, db, first, alice, venues.StatusAccepted)

	var second venues.Slot
	if err := inTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
		_, err := venues.CreateSlotTx(ctx, tx, festID, "2026-09-11 19:00", 0, "", []int{2})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	slots, err := venues.VenueSlots(t.Context(), db, festID)
	if err != nil || len(slots) != 2 {
		t.Fatalf("slots %v %v", slots, err)
	}
	second = slots[1]
	bob := newVenueUser(t, db, "bob")
	fileApplication(t, db, second, bob, "Вторая", 0, nil)
	setStatus(t, db, second, bob, venues.StatusAccepted)

	names := map[int64]string{}
	rows, err := db.Query(`select game_id, name from participants where fest_id = ? and number = 1`, festID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var gameID int64
		var name string
		if err := rows.Scan(&gameID, &name); err != nil {
			t.Fatal(err)
		}
		names[gameID] = name
	}
	if names[first.GameID] != "Мантисса" || names[second.GameID] != "Вторая" {
		t.Fatalf("number 1 by game: %v", names)
	}
}

// A Заявка owns its own seat and no other: a team the host seated by hand on
// the game page survives a stranger's заявка, and its Number is not reused.
func TestHandSeatedTeamSurvivesAnApplication(t *testing.T) {
	db := venueTestDB(t)
	festID, slot := newVenueSlot(t, db, []int{2})
	if err := inTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return venues.ReseatTx(ctx, tx, slot, "Тбилиси")
	}); err != nil {
		t.Fatal(err)
	}
	// The host types a team straight into the Game's document.
	if _, err := db.Exec(`
update matches set state_json = ?
where game_id = ? and code = 'main'`,
		`{"teams":[{"name":"Вручную","city":"Тбилиси","number":1}],"entries":[[],[]],"completed":[false,false],"shootoutRounds":[]}`,
		slot.GameID); err != nil {
		t.Fatal(err)
	}

	alice := newVenueUser(t, db, "alice")
	fileApplication(t, db, slot, alice, "Мантисса", 0, nil)
	// A pending заявка touches nothing.
	if err := inTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return venues.ReseatTx(ctx, tx, slot, "Тбилиси")
	}); err != nil {
		t.Fatal(err)
	}
	if got := numbersByTeam(t, db, festID); got["Мантисса"] != 0 {
		t.Fatalf("a pending заявка seated a team: %v", got)
	}

	setStatus(t, db, slot, alice, venues.StatusAccepted)
	got := numbersByTeam(t, db, festID)
	if got["Вручную"] != 1 {
		t.Fatalf("the hand-seated team was dropped: %v", got)
	}
	if got["Мантисса"] != 2 {
		t.Fatalf("the заявка took a seat it does not own: %v", got)
	}

	// Declining frees only the заявка's own seat.
	setStatus(t, db, slot, alice, venues.StatusDeclined)
	if got := numbersByTeam(t, db, festID); got["Вручную"] != 1 || got["Мантисса"] != 0 {
		t.Fatalf("after the decline: %v", got)
	}
}

// A спорный counts as a result: unseating the team it names is refused.
func TestContestedBlocksAnUnseat(t *testing.T) {
	db := venueTestDB(t)
	festID, slot := newVenueSlot(t, db, []int{2})
	alice := newVenueUser(t, db, "alice")
	fileApplication(t, db, slot, alice, "Мантисса", 0, nil)
	setStatus(t, db, slot, alice, venues.StatusAccepted)
	if err := inTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return store.SaveContestedTx(ctx, tx, festID, slot.GameID, alice, 0, 1, "Ответ", util.UtcNow())
	}); err != nil {
		t.Fatal(err)
	}
	err := inTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return venues.SetStatusTx(ctx, tx, slot, "", applicationID(t, db, slot, alice), venues.StatusDeclined)
	})
	if !errors.Is(err, venues.ErrHasResults) {
		t.Fatalf("err = %v, want ErrHasResults", err)
	}
}

// The host may renumber a team on the game page; the Заявка follows it there
// rather than seating a second copy under the number it used to hold.
func TestReseatFollowsARenumberedTeam(t *testing.T) {
	db := venueTestDB(t)
	festID, slot := newVenueSlot(t, db, []int{2})
	alice := newVenueUser(t, db, "alice")
	fileApplication(t, db, slot, alice, "Мантисса", 5723, []venues.RosterPlayer{
		{PlayerID: 1, Surname: "Иванов", Name: "Иван", Captain: true},
	})
	setStatus(t, db, slot, alice, venues.StatusAccepted)

	// The game page renumbers 1 → 3 — a document write, which reseats the бой
	// and so mints a Participant at 3 and leaves the Заявка pointing at 1.
	if err := inTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return flatgame.SetStateTx(ctx, tx, festID, slot.GameID,
			`{"teams":[{"name":"Мантисса","city":"Тбилиси","number":3}],"entries":[[],[]],"completed":[false,false],"shootoutRounds":[]}`)
	}); err != nil {
		t.Fatal(err)
	}
	if err := inTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return venues.ReseatTx(ctx, tx, slot, "Тбилиси")
	}); err != nil {
		t.Fatal(err)
	}

	// A roster edit reseats again; the team must still be one row at 3.
	if err := inTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return venues.ReseatTx(ctx, tx, slot, "Тбилиси")
	}); err != nil {
		t.Fatal(err)
	}
	if got := numbersByTeam(t, db, festID); len(got) != 1 || got["Мантисса"] != 3 {
		t.Fatalf("participants %v, want one team at 3", got)
	}
	var seats int
	if err := db.QueryRow(`select count(*) from game_participants where game_id = ?`, slot.GameID).Scan(&seats); err != nil {
		t.Fatal(err)
	}
	if seats != 1 {
		t.Fatalf("game_participants = %d, want 1", seats)
	}
	app, err := venues.UserApplication(t.Context(), db, slot.ID, alice)
	if err != nil {
		t.Fatal(err)
	}
	if app.Number != 3 || app.ParticipantID == 0 {
		t.Fatalf("заявка %+v, want the renumbered seat", app)
	}

	// And the queue still works over the renumbered seat.
	setStatus(t, db, slot, alice, venues.StatusDeclined)
	if got := numbersByTeam(t, db, festID); len(got) != 0 {
		t.Fatalf("after the decline: %v", got)
	}
	setStatus(t, db, slot, alice, venues.StatusAccepted)
	if got := numbersByTeam(t, db, festID); len(got) != 1 || got["Мантисса"] != 1 {
		t.Fatalf("after re-accepting: %v", got)
	}
}

// A спорный is the жюри's to rule on, so it follows the team through a
// renumber rather than being cascaded away with the Participant it retired.
func TestContestedFollowARenumberedTeam(t *testing.T) {
	db := venueTestDB(t)
	festID, slot := newVenueSlot(t, db, []int{2})
	alice := newVenueUser(t, db, "alice")
	fileApplication(t, db, slot, alice, "Мантисса", 0, []venues.RosterPlayer{
		{PlayerID: 1, Surname: "Иванов", Name: "Иван", Captain: true},
	})
	setStatus(t, db, slot, alice, venues.StatusAccepted)
	if err := inTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return store.SaveContestedTx(ctx, tx, festID, slot.GameID, alice, 0, 1, "Текст ответа", util.UtcNow())
	}); err != nil {
		t.Fatal(err)
	}

	if err := inTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return flatgame.SetStateTx(ctx, tx, festID, slot.GameID,
			`{"teams":[{"name":"Мантисса","city":"Тбилиси","number":3}],"entries":[[],[]],"completed":[false,false],"shootoutRounds":[]}`)
	}); err != nil {
		t.Fatal(err)
	}
	if err := inTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return venues.ReseatTx(ctx, tx, slot, "Тбилиси")
	}); err != nil {
		t.Fatal(err)
	}

	list, err := store.LoadContested(t.Context(), db, slot.GameID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Number != 3 || list[0].Answer != "Текст ответа" {
		t.Fatalf("спорные %+v, want one on the team at 3", list)
	}
	var rosterRows int
	if err := db.QueryRow(`
select count(*) from game_team_players gtp
join participants p on p.id = gtp.participant_id
where gtp.game_id = ? and p.number = 3`, slot.GameID).Scan(&rosterRows); err != nil {
		t.Fatal(err)
	}
	if rosterRows != 1 {
		t.Fatalf("game_team_players on the new seat = %d, want 1", rosterRows)
	}

	// The tours export still writes the answer text in the cell.
	doc, err := store.LoadGameDoc(t.Context(), db, festID, slot.GameID)
	if err != nil {
		t.Fatal(err)
	}
	state := string(store.WithContestedFor(t.Context(), db, slot.GameID, []byte(doc.State)))
	f := excelize.NewFile()
	defer f.Close()
	if err := xlsxexport.BuildODSheet(f, doc.SchemeJSON, state, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := f.GetCellValue("Worksheet", "E3"); err != nil || got != "Текст ответа" {
		t.Fatalf("tours cell = %q (%v), want the спорный's text", got, err)
	}
}

// A Состав names a rating.chgk.info player in three parts, and the roster the
// Слот's game answers with keeps them apart; a player without an отчество
// reads exactly as every other fest's does.
func TestSlotRosterKeepsThePatronymic(t *testing.T) {
	db := venueTestDB(t)
	festID, slot := newVenueSlot(t, db, []int{2})
	alice := newVenueUser(t, db, "alice")
	fileApplication(t, db, slot, alice, "Мантисса", 0, []venues.RosterPlayer{
		{PlayerID: 1033, Surname: "Ковалёва", Name: "Елена", Patronymic: "Александровна", Captain: true},
		{PlayerID: 0, Surname: "Новичок", Name: "Пётр"},
	})
	setStatus(t, db, slot, alice, venues.StatusAccepted)

	var first, last, patronymic string
	if err := db.QueryRow(`
select first_name, last_name, patronymic from players where fest_id = ? and last_name = 'Ковалёва'`,
		festID).Scan(&first, &last, &patronymic); err != nil {
		t.Fatal(err)
	}
	if first != "Елена" || last != "Ковалёва" || patronymic != "Александровна" {
		t.Fatalf("players row %q %q %q", first, last, patronymic)
	}

	teams, err := roster.LoadGameRosterView(t.Context(), db, festID, slot.GameID)
	if err != nil {
		t.Fatal(err)
	}
	if len(teams) != 1 || len(teams[0].Players) != 2 {
		t.Fatalf("roster %+v", teams)
	}
	withPatronymic := teams[0].Players[0]
	if withPatronymic.Name != "Ковалёва Елена Александровна" || withPatronymic.Patronymic != "Александровна" {
		t.Fatalf("player %+v", withPatronymic)
	}
	if withPatronymic.FirstName != "Елена" || withPatronymic.LastName != "Ковалёва" {
		t.Fatalf("player parts %+v", withPatronymic)
	}
	// No отчество: the фест's own «Имя Фамилия», as before.
	if plain := teams[0].Players[1]; plain.Name != "Пётр Новичок" || plain.Patronymic != "" {
		t.Fatalf("player without a patronymic %+v", plain)
	}
}
