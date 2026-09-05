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
		Upcoming:    []SlotRow{{Date: "2026-09-04 19:00", Registration: "открыта"}},
		Past:        []SlotRow{{Date: "2026-08-28 19:00", Tournament: "Синхрон"}},
	}))
	for _, want := range []string{
		`href="/venues"`,
		`<p>Привет</p>`,
		`Ближайшие игры`,
		`Прошедшие игры`,
		`Синхрон`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	// The reg token is handed out from the Slot's page and nowhere else, so a
	// public Venue says a registration is open without linking to it.
	if strings.Contains(body, "/reg/") {
		t.Error("the venue page hands out a reg link")
	}
	bare := renderPublic(t, VenueDoc(VenueDetail{Ref: "x", Title: "X"}))
	if !strings.Contains(bare, "Игр пока нет.") {
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

	// A closed registration still shows a user their own application, and still
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
	// A Slot starts closed, so this is the first thing most links show: say it
	// is closed rather than send someone through Telegram for nothing.
	shut := base
	shut.State = venues.RegClosed
	shut.LoginHref = "/login?next=%2Freg%2Ftok"
	body = renderPublic(t, RegDoc(shut))
	if !strings.Contains(body, "Регистрация закрыта.") {
		t.Error("an anonymous visitor is told it is closed")
	}
	if strings.Contains(body, `href="/login?next=%2Freg%2Ftok"`) {
		t.Error("a closed registration must not invite a login")
	}
}

// A Slot has more applicants than seats, so the roster is asked for only once
// the application is accepted.
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
	slot := venues.Slot{ID: 7, FestID: 1, GameID: 3, StartsAt: "2026-09-04 19:00", RegToken: "tok", RegSetUp: true}
	body := renderPublic(t, slotPageDoc(slotPageData{
		Venue: venue, Slot: slot, Tournament: "Синхрон", CanManage: true,
		GameHref: "/host/venue/tbilisi/game/3/table", RegURL: "https://dope.test/reg/tok",
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
		`/host/venue/tbilisi/game/3/token`,
		`/host/venue/tbilisi/game/3/clone`,
		`/host/venue/tbilisi/game/3/application/9/status`,
		`/host/venue/tbilisi/game/3/application/9/revert`,
		`/host/venue/tbilisi/game/3/application/9/edit`,
		`/host/venue/tbilisi/game/3/export/tours.xlsx`,
		`/host/venue/tbilisi/game/3/export/players.xlsx`,
		`rating.chgk.info/teams/5723`,
		`https://t.me/tester`,
		`2026-09-02 13:10`,
		`Принять`,
		`Отклонить`,
		`/host/venue/tbilisi/game/3/delete`,
		`Удалить игру`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	// A pending application is not offered «Вернуть в ожидание» — it is there.
	if strings.Contains(body, "Вернуть в ожидание") {
		t.Error("a pending заявка should not offer the status it already has")
	}

	viewer := renderPublic(t, slotPageDoc(slotPageData{Venue: venue, Slot: slot}))
	if strings.Contains(viewer, "Удалить игру") {
		t.Error("a host without manage rights is offered the delete")
	}
}

// A Слот is made without a registration: the section is a button until one is
// set up, and the dialog behind it is where the link lives.
func TestSlotPageSetsTheRegistrationUpInADialog(t *testing.T) {
	venue := venues.Venue{ID: 1, Slug: "tbilisi", Title: "Площадка"}
	slot := venues.Slot{ID: 7, FestID: 1, GameID: 3, RegToken: "tok"}
	data := slotPageData{Venue: venue, Slot: slot, CanManage: true, RegURL: "https://dope.test/reg/tok",
		RegState: venues.RegClosed}

	body := renderPublic(t, slotPageDoc(data))
	for _, want := range []string{
		"Настройки регистрации",
		`data-dialog-open="slotReg"`,
		`action="/host/venue/tbilisi/game/3/reg"`,
		"Показывать ссылку на странице площадки",
		"Регистрация закрывается",
		// The link is the Слот's from the moment it is made, set up or not.
		`value="https://dope.test/reg/tok"`,
		`data-copy-target="regLink"`,
		"/game/3/token",
		// Each end of the window is a radio pair, and its calendar is hidden
		// until the dated one is picked.
		`name="reg_closes" value="never" checked`,
		`hidden data-when="reg_closes=at"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}

	data.Slot.RegSetUp = true
	data.Slot.RegClosesAt = "2026-09-04 18:00"
	data.Slot.LinkVisible = true
	data.RegState = venues.RegOpen
	body = renderPublic(t, slotPageDoc(data))
	for _, want := range []string{"Открыта", "до 2026-09-04 18:00", "ссылка на странице площадки",
		`name="reg_closes" value="at" checked`} {
		if !strings.Contains(body, want) {
			t.Errorf("set up: missing %q", want)
		}
	}
}

// A Representative decides whether the public page carries the invitation.
func TestVenuePageCarriesTheLinkOnlyWhenTold(t *testing.T) {
	body := renderPublic(t, VenueDoc(VenueDetail{Ref: "tbilisi", Title: "Площадка",
		Upcoming: []SlotRow{{Date: "2026-09-04 19:00", Registration: "открыта"}}}))
	if strings.Contains(body, "/reg/") {
		t.Error("the venue page hands out a link nobody published")
	}
	body = renderPublic(t, VenueDoc(VenueDetail{Ref: "tbilisi", Title: "Площадка",
		Upcoming: []SlotRow{{Date: "2026-09-04 19:00", Registration: "открыта", RegHref: "/reg/tok"}}}))
	if !strings.Contains(body, `href="/reg/tok"`) {
		t.Error("a published link is not on the page")
	}
}

func TestVenueDashDocListsSlotsAndAccess(t *testing.T) {
	body := renderPublic(t, venueDashDoc(venueDashData{
		Venue:     venues.Venue{ID: 1, Slug: "tbilisi", Title: "Площадка", City: "Тбилиси", IsPublic: true},
		Slots:     []VenueDashSlot{{ID: 7, Date: "2026-09-04 19:00", Tournament: "Синхрон", Accepted: 2, Pending: 1, Href: "/host/venue/tbilisi/game/3"}},
		CanManage: true,
	}))
	for _, want := range []string{
		`data-jump-href="/venue/tbilisi"`,
		`href="/host/venue/tbilisi/game/3"`,
		`принято 2, ждут 1`,
		`/host/venue/tbilisi/game/new`,
		`id="access"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	viewer := renderPublic(t, venueDashDoc(venueDashData{Venue: venues.Venue{ID: 1, Title: "Площадка"}}))
	if strings.Contains(viewer, "Новая игра") || strings.Contains(viewer, `id="access"`) {
		t.Error("a host without manage rights gets no forms")
	}
}

// fests and Venues are the same shape on the landing: a heading, what there
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
	// The Venue's name and town are rating.chgk.info's, so the create form
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

// The picker is one screen twice: the cards carry what they are sorted and
// filtered by, and the mode decides what a Representative does with one.
func TestTournamentPickerIsOneListInTwoModes(t *testing.T) {
	p := TournamentPicker{
		Venue: venues.Venue{ID: 1, Slug: "tbilisi", Title: "Площадка"},
		Slot:  venues.Slot{ID: 7, FestID: 1, GameID: 3, StartsAt: "2026-09-04 19:00", RatingTournamentID: 10233},
		Base:  "/host/venue/tbilisi/game/3",
		Cards: []TournamentCard{
			{ID: 10233, Name: "Синхрон августа", Type: "Синхрон", Editors: "Максим Мерзляков", Difficulty: 3.5, Teams: 86, Chosen: true},
			{ID: 10234, Name: "Асинхрон", Type: "Асинхрон"},
		},
	}
	body := renderPublic(t, TournamentPickerDoc(p))
	for _, want := range []string{
		`data-tournament="10233"`, `data-difficulty="3.5"`, `data-teams="86"`, `data-kind="sync"`,
		`data-kind="async"`,
		"Максим Мерзляков", "выбран",
		// The numbers are read off glyphs, not off words beside them.
		`class="fact" title="Сложность"`, ">3.5<", `class="fact" title="Заявлено команд"`, ">~86<",
		`action="/host/venue/tbilisi/game/3/tournament"`,
		"Только синхроны", "Сортировка", "Выбрать все",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("pick: missing %q", want)
		}
	}
	if strings.Contains(body, "/game/3/voting") {
		t.Error("pick mode offers to save a poll")
	}
	// A tournament buff has no forecast for is still a card, with no number
	// where it has none.
	card := body[strings.Index(body, `data-tournament="10234"`):]
	if strings.Contains(card[:strings.Index(card, "</section>")], `title="Сложность"`) {
		t.Error("a tournament with no forecast still shows a number")
	}

	p.Poll = true
	body = renderPublic(t, TournamentPickerDoc(p))
	if !strings.Contains(body, `action="/host/venue/tbilisi/game/3/voting"`) {
		t.Error("poll mode does not save a poll")
	}
	if !strings.Contains(body, `name="candidate" value="10233" checked`) {
		t.Error("the cards are the poll's candidates")
	}
	if strings.Contains(body, "/game/3/tournament") {
		t.Error("poll mode names the game's tournament outright")
	}
	// A Slot with no date has nothing playable, and says so rather than
	// rendering an empty list with controls over it.
	p.Cards = nil
	if body := renderPublic(t, TournamentPickerDoc(p)); !strings.Contains(body, "Буфф не знает турниров") ||
		strings.Contains(body, "Сортировка") {
		t.Error("an empty picker still draws its controls")
	}
}
