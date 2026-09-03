package hostpages

import (
	"net/url"
	"strings"
	"testing"

	"dope/dope/domain/venues"
)

func TestVenuesIndexDocIsOneFilteredTable(t *testing.T) {
	body := renderPublic(t, VenuesIndexDoc([]VenueRow{
		{Ref: "tbilisi", Title: "Площадка Тбилиси", City: "Тбилиси", NextSlot: "2026-09-04 19:00",
			Registration: "открыта", Accepted: 3, RatingVenueID: 123},
	}))
	for _, want := range []string{
		`data-filter-rows="venues"`,
		`id="venues"`,
		`href="/venue/tbilisi"`,
		`Площадка Тбилиси`,
		`2026-09-04 19:00`,
		`rating.chgk.info/venues/123`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if empty := renderPublic(t, VenuesIndexDoc(nil)); !strings.Contains(empty, "Публичных площадок пока нет.") {
		t.Error("missing the empty note")
	}
}

func TestVenueDocSplitsUpcomingFromPast(t *testing.T) {
	body := renderPublic(t, VenueDoc(VenueDetail{
		Ref: "tbilisi", Title: "Площадка Тбилиси", City: "Тбилиси", RatingVenueID: 123,
		Description: "<p>Привет</p>",
		Upcoming:    []SlotRow{{Date: "2026-09-04 19:00", Registration: "открыта", RegHref: "/reg/tok"}},
		Past:        []SlotRow{{Date: "2026-08-28 19:00", Tournament: "Синхрон"}},
	}))
	for _, want := range []string{
		`href="/venues"`,
		`<p>Привет</p>`,
		`Ближайшие слоты`,
		`href="/reg/tok"`,
		`Прошедшие слоты`,
		`Синхрон`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	bare := renderPublic(t, VenueDoc(VenueDetail{Ref: "x", Title: "X"}))
	if !strings.Contains(bare, "Слотов пока нет.") {
		t.Error("missing the empty note")
	}
}

// The landing and the index offer the organizer's side the way a fest's public
// page does: an authed-only ☰ row into the tree the Venue is edited under.
func TestVenuePagesOfferTheOrganizerMode(t *testing.T) {
	for name, body := range map[string]string{
		"landing": renderPublic(t, VenueDoc(VenueDetail{Ref: "tbilisi", Title: "Площадка"})),
		"index":   renderPublic(t, VenuesIndexDoc([]VenueRow{{Ref: "tbilisi", Title: "Площадка"}})),
	} {
		want := `data-jump-href="/host/venue/tbilisi"`
		if name == "index" {
			want = `data-jump-href="/host"`
		}
		for _, s := range []string{want, `data-jump-label="Режим организатора"`, `data-jump-icon="clipboard"`, `data-jump-authed="1"`} {
			if !strings.Contains(body, s) {
				t.Errorf("%s: missing %q", name, s)
			}
		}
	}
}

func TestRegDocSaysWhatEachStateAllows(t *testing.T) {
	base := RegPage{Token: "tok", VenueTitle: "Площадка", VenueRef: "tbilisi", Date: "2026-09-04 19:00"}

	scheduled := base
	scheduled.State = venues.RegScheduled
	scheduled.OpensAt = "2026-09-01 10:00"
	if body := renderPublic(t, RegDoc(scheduled)); !strings.Contains(body, "Регистрация откроется 2026-09-01 10:00.") {
		t.Error("a scheduled registration must say when it opens")
	}

	anon := base
	anon.LoginHref = "/login?next=%2Freg%2Ftok"
	body := renderPublic(t, RegDoc(anon))
	if !strings.Contains(body, `href="/login?next=%2Freg%2Ftok"`) {
		t.Error("an anonymous visitor is sent to the handshake and back")
	}
	if strings.Contains(body, "data-roster-editor") {
		t.Error("no form before login")
	}

	open := base
	open.LoggedIn = true
	open.Application = &ApplicationView{
		Status: venues.StatusAccepted, StatusLabel: StatusLabel(venues.StatusAccepted),
		TeamName: "Мантисса", RatingTeamID: 5723, Number: 4,
		Roster: []venues.RosterPlayer{{PlayerID: 1033, Surname: "Ковалёва", Name: "Елена", Captain: true}},
		Flags:  []string{venues.FlagCaptain},
	}
	body = renderPublic(t, RegDoc(open))
	for _, want := range []string{
		`Статус: принята · номер команды 4`,
		`Ковалёва Елена`,
		`>К<`,
		`data-roster-editor`,
		`name="roster_json"`,
		`value="Мантисса"`,
		`Сохранить заявку`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}

	// A closed registration still shows a user their own заявка, and still
	// lets them edit it; a stranger sees only that it is closed.
	closed := open
	closed.State = venues.RegClosed
	if body := renderPublic(t, RegDoc(closed)); !strings.Contains(body, "data-roster-editor") {
		t.Error("a closed registration keeps the owner's form")
	}
	stranger := base
	stranger.LoggedIn = true
	stranger.State = venues.RegClosed
	if body := renderPublic(t, RegDoc(stranger)); !strings.Contains(body, "Регистрация закрыта.") {
		t.Error("a stranger is told it is closed")
	}
}

// A Слот has more applicants than seats, so the Состав is asked for only once
// the Заявка is accepted.
func TestRegDocAsksForTheRosterOnlyOnceAccepted(t *testing.T) {
	base := RegPage{Token: "tok", VenueTitle: "Площадка", VenueRef: "tbilisi", LoggedIn: true}

	fresh := renderPublic(t, RegDoc(base))
	if strings.Contains(fresh, "data-roster-editor") {
		t.Error("a new заявка asks for a team, not a состав")
	}

	for _, status := range []string{venues.StatusPending, venues.StatusDeclined} {
		p := base
		p.Application = &ApplicationView{Status: status, StatusLabel: StatusLabel(status), TeamName: "Мантисса"}
		if body := renderPublic(t, RegDoc(p)); strings.Contains(body, "data-roster-editor") {
			t.Errorf("%s: no состав before acceptance", status)
		}
	}

	p := base
	p.Application = &ApplicationView{Status: venues.StatusAccepted, StatusLabel: StatusLabel(venues.StatusAccepted), TeamName: "Мантисса"}
	body := renderPublic(t, RegDoc(p))
	if !strings.Contains(body, "data-roster-editor") || !strings.Contains(body, `name="roster_json"`) {
		t.Error("an accepted заявка gets the состав editor")
	}
}

func TestSlotPageDocCarriesTheLinkAndTheQueue(t *testing.T) {
	venue := venues.Venue{ID: 1, Slug: "tbilisi", Title: "Площадка", City: "Тбилиси"}
	slot := venues.Slot{ID: 7, FestID: 1, GameID: 3, StartsAt: "2026-09-04 19:00", RegToken: "tok"}
	body := renderPublic(t, slotPageDoc(slotPageData{
		Venue: venue, Slot: slot, Tournament: "Синхрон", CanManage: true,
		GameHref: "/host/venue/tbilisi/game/3/", RegURL: "https://dope.test/reg/tok",
		Applications: []SlotApplicationRow{{
			App: venues.Application{ID: 9, Status: venues.StatusPending, TeamName: "Мантисса",
				RatingTeamID: 5723, Number: 1, CreatedAt: "2026-09-02T13:10:36Z", UpdatedAt: "2026-09-02T13:10:36Z"},
			FlagSummary: "1К", Submitter: "@tester", SubmitterTg: "https://t.me/tester",
			Versions: []venues.Version{{Seq: 1, TeamName: "Мантисса", CreatedAt: "2026-09-02T13:10:36Z"}},
		}},
	}))
	for _, want := range []string{
		`value="https://dope.test/reg/tok"`,
		`data-copy-target="regLink"`,
		`/host/venue/tbilisi/slot/7/token`,
		`/host/venue/tbilisi/slot/7/clone`,
		`/host/venue/tbilisi/slot/7/application/9/status`,
		`/host/venue/tbilisi/slot/7/application/9/revert`,
		`/host/venue/tbilisi/slot/7/application/9/edit`,
		`/host/venue/tbilisi/slot/7/export/tours.xlsx`,
		`/host/venue/tbilisi/slot/7/export/players.xlsx`,
		`rating.chgk.info/teams/5723`,
		`https://t.me/tester`,
		`2026-09-02 13:10`,
		`Принять`,
		`Отклонить`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	// A pending заявка is not offered «Вернуть в ожидание» — it is there.
	if strings.Contains(body, "Вернуть в ожидание") {
		t.Error("a pending заявка should not offer the status it already has")
	}
}

func TestVenueDashDocListsSlotsAndAccess(t *testing.T) {
	body := renderPublic(t, venueDashDoc(venueDashData{
		Venue:     venues.Venue{ID: 1, Slug: "tbilisi", Title: "Площадка", City: "Тбилиси", IsPublic: true},
		Slots:     []VenueDashSlot{{ID: 7, Date: "2026-09-04 19:00", Tournament: "Синхрон", Accepted: 2, Pending: 1, Href: "/host/venue/tbilisi/slot/7"}},
		CanManage: true,
	}))
	for _, want := range []string{
		`data-jump-href="/venue/tbilisi"`,
		`href="/host/venue/tbilisi/slot/7"`,
		`принято 2, ждут 1`,
		`/host/venue/tbilisi/slot/new`,
		`id="access"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	viewer := renderPublic(t, venueDashDoc(venueDashData{Venue: venues.Venue{ID: 1, Title: "Площадка"}}))
	if strings.Contains(viewer, "Новый слот") || strings.Contains(viewer, `id="access"`) {
		t.Error("a host without manage rights gets no forms")
	}
}

// Фесты and Площадки are the same shape on the landing: a heading, what there
// is, and the way to make another.
func TestHostLandingSectionsMatch(t *testing.T) {
	body := renderPublic(t, hostLoggedInDoc(hostLandingData{LoggedIn: true, Username: "tester"}))
	for _, want := range []string{
		"Фесты", "Фестов пока нет.", "Создать фест",
		"Площадки", "Площадок пока нет.", "Создать площадку",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Index(body, "Фестов пока нет.") > strings.Index(body, "Площадок пока нет.") {
		t.Error("Фесты come first")
	}
	// The Площадка's name and town are rating.chgk.info's, so the create form
	// does not ask for them.
	if strings.Contains(body, `name="city"`) {
		t.Error("the create form asks for a city")
	}
}

// A refused create form comes back open, with what was typed and why it was
// refused; a fresh one is blank and closed.
func TestHostLandingKeepsARefusedForm(t *testing.T) {
	form := url.Values{
		"slug": {"bad_slug"}, "rating_venue_id": {"6826"},
		"description": {"Играем"}, "is_public": {"1"},
	}
	body := renderPublic(t, hostLoggedInDoc(hostLandingData{
		LoggedIn: true, Username: "tester",
		Error: "Slug: " + SlugTitle,
		Open:  "venue", Form: form,
	}))
	for _, want := range []string{
		`value="bad_slug"`,
		`value="6826"`,
		`>Играем</textarea>`,
		`type="checkbox" name="is_public" value="1" checked`,
		SlugTitle,
		`pattern="` + SlugPattern + `"`,
		`data-rating-venue`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	fresh := renderPublic(t, hostLoggedInDoc(hostLandingData{LoggedIn: true, Username: "tester"}))
	if strings.Contains(fresh, `value="bad_slug"`) || strings.Contains(fresh, "checked") {
		t.Error("a fresh form must be blank")
	}
}
