package hostpages

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"dope/dope/domain/games"
	"dope/dope/domain/venues"
	"dope/dope/platform/markdown"
	"dope/dope/platform/roles"
	"dope/dope/platform/util"
	"dope/dope/storage/buffdb"
	"dope/dope/storage/festaccess"
	"dope/dope/storage/store"
	"dope/dope/web/pages"
	"dope/dope/web/route"
)

// denyVenuePage is the public Venue pages' policy: a form post without a
// session goes through the Telegram handshake and comes back to the same page,
// which is what a registration or voting link is for.
func denyVenuePage(w http.ResponseWriter, r *http.Request, d route.Denial) {
	if d == route.NoSession {
		http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.Path), http.StatusSeeOther)
		return
	}
	route.DenyAPI(w, r, d)
}

func (s *Server) VenueRoutes() *route.Table {
	s.venueOnce.Do(func() {
		t := route.New(s.h.Engine(), denyVenuePage)
		t.Handle("GET /venues", route.Public, s.renderVenuesIndex)
		t.Handle("GET /venue/{ref}", route.Public, s.renderVenuePage)
		t.Handle("GET /reg/{token}", route.Public, func(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
			return s.renderRegPage(w, r, r.PathValue("token"), "", "")
		})
		t.Handle("POST /reg/{token}", route.Session, s.handleRegSubmit)
		t.Handle("GET /vote/{token}", route.Public, func(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
			return s.renderVotePage(w, r, r.PathValue("token"), "")
		})
		t.Handle("POST /vote/{token}", route.Session, s.handleVoteSubmit)
		s.venueTable = t
	})
	return s.venueTable
}

func (s *Server) HandleVenueRouter(w http.ResponseWriter, r *http.Request) {
	s.VenueRoutes().Mux.ServeHTTP(w, r)
}

func (s *Server) renderVenuesIndex(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	list, err := venues.PublicVenues(r.Context(), s.h.Engine().DB)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	next, err := venues.NextSlots(r.Context(), s.h.Engine().DB, now.Format(venues.TimeLayout))
	if err != nil {
		return err
	}
	rows := make([]VenueRow, 0, len(list))
	for _, v := range list {
		row := VenueRow{Ref: v.Ref(), Title: v.Title, City: v.City, RatingVenueID: v.RatingVenueID}
		if slot, ok := next[v.ID]; ok {
			row.NextSlot = slot.StartsAt
			row.Registration = registrationLabel(slot, now)
			row.Accepted = slot.Accepted
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i].NextSlot, rows[j].NextSlot
		if (a == "") != (b == "") {
			return b == ""
		}
		return a < b
	})
	pages.RenderDoc(w, s.h.Engine().AssetETags, VenuesIndexDoc(rows))
	return nil
}

func registrationLabel(slot venues.Slot, now time.Time) string {
	switch venues.Registration(slot.RegOpensAt, slot.RegClosed, now) {
	case venues.RegClosed:
		return strs.Venues.Public.RegClosed()
	case venues.RegScheduled:
		return strs.Venues.Public.RegScheduled(slot.RegOpensAt)
	default:
		return strs.Venues.Public.RegOpen()
	}
}

func (s *Server) renderVenuePage(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	festID, err := store.ResolveFestID(r.Context(), s.h.Engine().DB, r.PathValue("ref"))
	if err != nil {
		return route.NotFound
	}
	// The reg token is the invitation, and a Representative hands it out from
	// the Slot's own page: this one says a registration is open and no more.
	venue, err := venues.LoadVenue(r.Context(), s.h.Engine().DB, festID)
	if err != nil || !venue.IsPublic {
		return route.NotFound
	}
	slots, err := venues.VenueSlots(r.Context(), s.h.Engine().DB, festID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	detail := VenueDetail{
		Ref: venue.Ref(), Title: venue.Title, City: venue.City,
		RatingVenueID: venue.RatingVenueID, Description: markdown.Render(venue.Description),
	}
	names := s.tournamentNames(r.Context(), slots)
	for _, slot := range slots {
		row := SlotRow{Date: slot.StartsAt, Tournament: names[slot.RatingTournamentID], Accepted: slot.Accepted}
		at, dated := venues.ParseTime(slot.StartsAt)
		if dated && !at.After(now) {
			detail.Past = append(detail.Past, row)
			continue
		}
		row.Registration = registrationLabel(slot, now)
		detail.Upcoming = append(detail.Upcoming, row)
	}
	pages.RenderDoc(w, s.h.Engine().AssetETags, VenueDoc(detail))
	return nil
}

func (s *Server) tournamentNames(ctx context.Context, slots []venues.Slot) map[int64]string {
	out := map[int64]string{}
	buff := s.h.Engine().BuffMirror()
	for _, slot := range slots {
		if slot.RatingTournamentID <= 0 {
			continue
		}
		if _, seen := out[slot.RatingTournamentID]; seen {
			continue
		}
		if t, ok := buff.Tournament(ctx, slot.RatingTournamentID); ok {
			out[slot.RatingTournamentID] = t.Name
		}
	}
	return out
}

func (s *Server) renderRegPage(w http.ResponseWriter, r *http.Request, token, errMsg, notice string) error {
	slot, err := venues.SlotByToken(r.Context(), s.h.Engine().DB, token)
	if err != nil {
		return route.NotFound
	}
	venue, err := venues.LoadVenue(r.Context(), s.h.Engine().DB, slot.FestID)
	if err != nil {
		return route.NotFound
	}
	now := time.Now().UTC()
	page := RegPage{
		Token: slot.RegToken, VenueTitle: venue.Title, VenueRef: venue.Ref(), City: venue.City,
		Date: slot.StartsAt, State: venues.Registration(slot.RegOpensAt, slot.RegClosed, now),
		OpensAt: slot.RegOpensAt, Error: errMsg, Notice: notice,
		LoginHref: "/login?next=" + url.QueryEscape("/reg/"+slot.RegToken),
	}
	if slot.RatingTournamentID > 0 {
		if t, ok := s.h.Engine().BuffMirror().Tournament(r.Context(), slot.RatingTournamentID); ok {
			page.Tournament = t.Name
		}
	}
	page.GameHref = "/venue/" + venue.Ref() + "/game/" + slot.GameRef() + "/table"
	user, ok := s.h.Engine().LookupSession(r)
	page.LoggedIn = ok
	if ok {
		app, err := venues.UserApplication(r.Context(), s.h.Engine().DB, slot.ID, user.UserID)
		if err == nil {
			view := s.applicationView(r.Context(), app, slot)
			page.Application = &view
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	pages.RenderDoc(w, s.h.Engine().AssetETags, RegDoc(page))
	return nil
}

func (s *Server) applicationView(ctx context.Context, app venues.Application, slot venues.Slot) ApplicationView {
	return ApplicationView{
		Status: app.Status, StatusLabel: StatusLabel(app.Status), TeamName: app.TeamName,
		RatingTeamID: app.RatingTeamID,
		Roster:       app.Roster, Flags: s.rosterFlags(ctx, app, slot), Number: app.Number,
	}
}

func (s *Server) rosterFlags(ctx context.Context, app venues.Application, slot venues.Slot) []string {
	at, ok := venues.ParseTime(slot.StartsAt)
	if !ok {
		at = time.Now().UTC()
	}
	base, _ := s.h.Engine().BuffMirror().BaseRoster(ctx, app.RatingTeamID, at)
	return venues.Flags(app.Roster, app.RatingTeamID, base)
}

func (s *Server) handleRegSubmit(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	token := r.PathValue("token")
	slot, err := venues.SlotByToken(r.Context(), s.h.Engine().DB, token)
	if err != nil {
		return route.NotFound
	}
	if err := r.ParseForm(); err != nil {
		return route.BadRequest("bad form")
	}
	now := time.Now().UTC()
	state := venues.Registration(slot.RegOpensAt, slot.RegClosed, now)
	if state == venues.RegScheduled {
		return s.renderRegPage(w, r, token, strs.Venues.Reg.ErrorNotOpen(), "")
	}
	current, err := venues.UserApplication(r.Context(), s.h.Engine().DB, slot.ID, sc.User.UserID)
	has := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if state == venues.RegClosed && !has {
		return s.renderRegPage(w, r, token, strs.Venues.Reg.Closed(), "")
	}
	// The roster is asked for only once the application is accepted, so a pending
	// one stores an empty roster whatever the request carries.
	var roster []venues.RosterPlayer
	if has && current.Status == venues.StatusAccepted {
		roster = venues.ParseRoster(r.Form.Get("roster_json"))
	}
	err = s.h.Engine().WithWriteTx(r.Context(), slot.FestID, "slot-application", func(ctx context.Context, tx *sql.Tx) error {
		_, err := venues.SaveVersionTx(ctx, tx, slot.ID, sc.User.UserID, sc.User.UserID,
			r.Form.Get("team_name"), formInt64(r.Form, "rating_team_id"), roster)
		return err
	})
	if err != nil {
		return s.renderRegPage(w, r, token, err.Error(), "")
	}
	if err := s.reseatIfSeated(r.Context(), slot, sc.User.UserID); err != nil {
		return err
	}
	http.Redirect(w, r, "/reg/"+token, http.StatusSeeOther)
	return nil
}

func formInt64(form url.Values, key string) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(form.Get(key)), 10, 64)
	if err != nil || value < 0 {
		return 0
	}
	return value
}

// reseatIfSeated carries a application's edit into the Game only when that application
// is already accepted; a pending one is a version row and nothing more.
func (s *Server) reseatIfSeated(reqCtx context.Context, slot venues.Slot, userID int64) error {
	app, err := venues.UserApplication(reqCtx, s.h.Engine().DB, slot.ID, userID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && app.Status != venues.StatusAccepted) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.reseatSlot(reqCtx, slot)
}

func (s *Server) reseatSlot(reqCtx context.Context, slot venues.Slot) error {
	venue, err := venues.LoadVenue(reqCtx, s.h.Engine().DB, slot.FestID)
	if err != nil {
		return err
	}
	err = s.h.Engine().WithWriteTx(reqCtx, slot.FestID, "slot-reseat", func(ctx context.Context, tx *sql.Tx) error {
		return venues.ReseatTx(ctx, tx, slot, venue.City)
	})
	if err == nil {
		s.h.Engine().InvalidateFestViewCache(slot.FestID)
	}
	return err
}

// userTimezone reads the viewer's profile zone (users.timezone) — the caption
// the datetime pickers show, so the wall-clock a slot is written in says
// whose clock it is. "" when the profile has none.
func (s *Server) userTimezone(ctx context.Context, userID int64) string {
	var tz sql.NullString
	if err := s.h.Engine().DB.QueryRowContext(ctx,
		`select timezone from users where id = ?`, userID).Scan(&tz); err != nil {
		return ""
	}
	return tz.String
}

func (s *Server) renderVenueDashboard(w http.ResponseWriter, r *http.Request, sc route.Scope, errMsg, notice string) error {
	festID := sc.FestID
	_ = r.ParseForm()
	venue, err := venues.LoadVenue(r.Context(), s.h.Engine().DB, festID)
	if err != nil {
		return err
	}
	// A refused save comes back with what was typed, not with what is stored.
	// Only the venue form posts a slug; a refused slot form leaves it alone.
	if errMsg != "" && r.Form.Has("slug") {
		venue = venues.Venue{
			ID: venue.ID, Slug: r.Form.Get("slug"), Title: r.Form.Get("title"),
			City: r.Form.Get("city"), Description: r.Form.Get("description"),
			RatingVenueID: formInt64(r.Form, "rating_venue_id"),
			IsPublic:      r.Form.Get("is_public") == "1",
		}
	}
	slots, err := venues.VenueSlots(r.Context(), s.h.Engine().DB, festID)
	if err != nil {
		return err
	}
	names := s.tournamentNames(r.Context(), slots)
	rows := make([]VenueDashSlot, 0, len(slots))
	for _, slot := range slots {
		rows = append(rows, VenueDashSlot{
			ID: slot.ID, Date: slot.StartsAt, Tournament: names[slot.RatingTournamentID],
			Accepted: slot.Accepted, Pending: slot.Pending,
			Href: VenueBase(venue) + "/game/" + slot.GameRef(),
		})
	}
	members, err := festaccess.LoadFestAccessMembers(s.h.Engine(), r.Context(), festID)
	if err != nil {
		return err
	}
	pages.RenderDoc(w, s.h.Engine().AssetETags, venueDashDoc(venueDashData{
		Venue: venue, Slots: rows, Access: members,
		CanManage: roles.CanManageFest(sc.Role), CanDelete: roles.CanDeleteFest(sc.Role),
		Tz:    s.userTimezone(r.Context(), sc.User.UserID),
		Error: errMsg, Notice: notice,
	}))
	return nil
}

func (s *Server) handleHostCreateVenue(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	userID := sc.User.UserID
	if err := r.ParseForm(); err != nil {
		return route.BadRequest("bad form")
	}
	refused := func(msg string) error {
		s.renderHostLandingForm(w, r, msg, "venue", r.Form)
		return nil
	}
	// A Venue is a venue rating.chgk.info already knows: the form picks one,
	// and its name and town are theirs, not typed here.
	ratingID := formInt64(r.Form, "rating_venue_id")
	if ratingID <= 0 {
		return refused(strs.Venues.Errors.RatingVenueRequired())
	}
	venue, err := s.h.Engine().RatingVenues().Lookup(r.Context(), ratingID)
	if err != nil {
		return refused(err.Error())
	}
	slug := strings.TrimSpace(r.Form.Get("slug"))
	if slug != "" {
		if util.ValidateSlug(slug) != nil {
			return refused("Slug: " + SlugTitle)
		}
	}
	now := util.UtcNow()
	var festID int64
	err = s.h.Engine().WithWriteTx(r.Context(), 0, "venue-create", func(ctx context.Context, tx *sql.Tx) error {
		id, err := store.InsertReturningID(ctx, tx, `
insert into fests(slug, title, description, kind, city, rating_venue_id, created_by, revision, created_at, updated_at, is_public)
values(?, ?, ?, 'venue', ?, ?, ?, 1, ?, ?, ?)`,
			util.NullableString(slug), venue.Name, r.Form.Get("description"),
			venue.Town, venue.ID,
			userID, now, now, util.BoolToInt(r.Form.Get("is_public") == "1"))
		if err != nil {
			return err
		}
		festID = id
		_, err = tx.ExecContext(ctx, `
insert into fest_organizers(fest_id, user_id, role, added_at) values(?, ?, 'creator', ?)`, festID, userID, now)
		return err
	})
	if err != nil {
		return refused(err.Error())
	}
	http.Redirect(w, r, fmt.Sprintf("/host/venue/%d", festID), http.StatusSeeOther)
	return nil
}

func (s *Server) handleHostUpdateVenue(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	festID := sc.FestID
	if err := r.ParseForm(); err != nil {
		return route.BadRequest("bad form")
	}
	title := strings.TrimSpace(r.Form.Get("title"))
	if title == "" {
		return s.renderVenueDashboard(w, r, sc, strs.Venues.Errors.TitleRequired(), "")
	}
	slug := strings.TrimSpace(r.Form.Get("slug"))
	if slug != "" {
		if util.ValidateSlug(slug) != nil {
			return s.renderVenueDashboard(w, r, sc, "Slug: "+SlugTitle, "")
		}
	}
	err := s.h.Engine().WithWriteTx(r.Context(), festID, "venue-update", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
update fests set title = ?, slug = ?, description = ?, city = ?, rating_venue_id = ?, is_public = ?, updated_at = ?
where id = ?`, title, util.NullableString(slug), r.Form.Get("description"),
			strings.TrimSpace(r.Form.Get("city")), util.ParseOptionalInt64(r.Form.Get("rating_venue_id")),
			util.BoolToInt(r.Form.Get("is_public") == "1"), util.UtcNow(), festID)
		return err
	})
	if err != nil {
		return s.renderVenueDashboard(w, r, sc, err.Error(), "")
	}
	s.h.Engine().InvalidateFestViewCache(festID)
	http.Redirect(w, r, "/host/venue/"+s.festRefOrID(r.Context(), festID), http.StatusSeeOther)
	return nil
}

func (s *Server) tourComposition(ctx context.Context, tournamentID int64, typed string) []int {
	if tournamentID > 0 {
		if t, ok := s.h.Engine().BuffMirror().Tournament(ctx, tournamentID); ok {
			if comp := buffdb.TourComposition(t.QuestionsByTour); len(comp) > 0 {
				return comp
			}
		}
	}
	if comp := buffdb.TourComposition(typed); len(comp) > 0 {
		return comp
	}
	return []int{12, 12, 12}
}

func (s *Server) handleHostCreateSlot(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	festID := sc.FestID
	if err := r.ParseForm(); err != nil {
		return route.BadRequest("bad form")
	}
	tournamentID := formInt64(r.Form, "rating_tournament_id")
	comp := s.tourComposition(r.Context(), tournamentID, r.Form.Get("tour_comp"))
	var slotID int64
	err := s.h.Engine().WithWriteTx(r.Context(), festID, "slot-create", func(ctx context.Context, tx *sql.Tx) error {
		var err error
		slotID, err = venues.CreateSlotTx(ctx, tx, festID, r.Form.Get("starts_at"), tournamentID, "", comp)
		return err
	})
	if err != nil {
		return s.renderVenueDashboard(w, r, sc, err.Error(), "")
	}
	return s.redirectToSlot(w, r, festID, slotID)
}

// slotOf reads the Slot the route named. A Venue's Slot is named by its Game,
// the one the dispatcher already resolved off {game}.
func (s *Server) slotOf(r *http.Request, sc route.Scope) (venues.Venue, venues.Slot, error) {
	venue, err := venues.LoadVenue(r.Context(), s.h.Engine().DB, sc.FestID)
	if err != nil {
		return venue, venues.Slot{}, err
	}
	slot, err := venues.SlotByGameID(r.Context(), s.h.Engine().DB, sc.GameID)
	if err != nil {
		return venue, slot, err
	}
	if slot.FestID != sc.FestID {
		return venue, slot, sql.ErrNoRows
	}
	return venue, slot, nil
}

func (s *Server) renderSlotPage(w http.ResponseWriter, r *http.Request, sc route.Scope, errMsg, notice string) error {
	festID := sc.FestID
	_ = r.ParseForm()
	venue, slot, err := s.slotOf(r, sc)
	if err != nil {
		return err
	}
	apps, err := venues.SlotApplications(r.Context(), s.h.Engine().DB, slot.ID)
	if err != nil {
		return err
	}
	rows := make([]SlotApplicationRow, 0, len(apps))
	for _, app := range apps {
		versions, err := venues.ApplicationVersions(r.Context(), s.h.Engine().DB, app.ID)
		if err != nil {
			return err
		}
		flags := s.rosterFlags(r.Context(), app, slot)
		rows = append(rows, SlotApplicationRow{
			App: app, Flags: flags, FlagSummary: venues.FlagSummary(flags),
			Submitter: submitterName(app), SubmitterTg: submitterLink(app), Versions: versions,
		})
	}
	voting, err := s.loadVotingView(r, slot)
	if err != nil {
		return err
	}
	contested, err := s.loadContestedRows(r.Context(), festID, slot.GameID)
	if err != nil {
		return err
	}
	data := slotPageData{
		Venue: venue, Slot: slot, Applications: rows, Voting: voting, Contested: contested,
		GameStatus: s.gameProgress(r.Context(), festID, slot.GameID),
		GameHref:   VenueBase(venue) + "/game/" + slot.GameRef() + "/table",
		RegURL:     publicURL(r, "/reg/"+slot.RegToken),
		CanManage:  roles.CanManageFest(sc.Role),
		Tz:         s.userTimezone(r.Context(), sc.User.UserID),
		Error:      errMsg, Notice: notice,
	}
	if slot.RatingTournamentID > 0 {
		if t, ok := s.h.Engine().BuffMirror().Tournament(r.Context(), slot.RatingTournamentID); ok {
			data.Tournament = t.Name
		}
	}
	pages.RenderDoc(w, s.h.Engine().AssetETags, slotPageDoc(data))
	return nil
}

func submitterName(app venues.Application) string {
	if app.Submitter != "" {
		return "@" + app.Submitter
	}
	if app.SubmitterTgID > 0 {
		return "tg:" + strconv.FormatInt(app.SubmitterTgID, 10)
	}
	return "—"
}

func submitterLink(app venues.Application) string {
	if app.Submitter != "" {
		return "https://t.me/" + app.Submitter
	}
	if app.SubmitterTgID > 0 {
		return "tg://user?id=" + strconv.FormatInt(app.SubmitterTgID, 10)
	}
	return ""
}

func publicURL(r *http.Request, path string) string {
	scheme := "https"
	if r.TLS == nil && !strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "http"
	}
	return scheme + "://" + r.Host + path
}

func (s *Server) handleSlotSave(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	festID := sc.FestID
	_, slot, err := s.slotOf(r, sc)
	if err != nil {
		return err
	}
	if err := r.ParseForm(); err != nil {
		return route.BadRequest("bad form")
	}
	tournamentID := formInt64(r.Form, "rating_tournament_id")
	err = s.h.Engine().WithWriteTx(r.Context(), festID, "slot-save", func(ctx context.Context, tx *sql.Tx) error {
		if err := venues.UpdateSlotTx(ctx, tx, slot.ID, r.Form.Get("starts_at"), tournamentID,
			r.Form.Get("reg_opens_at"), r.Form.Get("reg_closed") == "1",
			r.Form.Get("link_visible") == "1"); err != nil {
			return err
		}
		if tournamentID <= 0 || tournamentID == slot.RatingTournamentID {
			return nil
		}
		return venues.RetourTx(ctx, tx, festID, slot.GameID, s.tourComposition(ctx, tournamentID, ""))
	})
	if err != nil {
		return s.renderSlotPage(w, r, sc, err.Error(), "")
	}
	s.h.Engine().InvalidateFestViewCache(festID)
	return s.redirectToSlot(w, r, festID, slot.ID)
}

// redirectToSlot sends the host back to the Slot's page, which is named by its
// Game: a Slot just created or cloned is re-read for the Game it made.
func (s *Server) redirectToSlot(w http.ResponseWriter, r *http.Request, festID, slotID int64) error {
	slot, err := venues.LoadSlot(r.Context(), s.h.Engine().DB, slotID)
	if err != nil {
		return err
	}
	http.Redirect(w, r, "/host/venue/"+s.festRefOrID(r.Context(), festID)+"/game/"+slot.GameRef(), http.StatusSeeOther)
	return nil
}

func (s *Server) handleSlotToken(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	festID := sc.FestID
	_, slot, err := s.slotOf(r, sc)
	if err != nil {
		return err
	}
	if err := s.h.Engine().WithWriteTx(r.Context(), festID, "slot-token", func(ctx context.Context, tx *sql.Tx) error {
		return venues.NewTokenTx(ctx, tx, slot.ID)
	}); err != nil {
		return err
	}
	return s.redirectToSlot(w, r, festID, slot.ID)
}

func (s *Server) handleSlotClone(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	festID := sc.FestID
	_, slot, err := s.slotOf(r, sc)
	if err != nil {
		return err
	}
	if err := r.ParseForm(); err != nil {
		return route.BadRequest("bad form")
	}
	startsAt := venues.FormatTime(r.Form.Get("starts_at"))
	opensAt := ""
	if from, ok := venues.ParseTime(slot.StartsAt); ok {
		if to, ok := venues.ParseTime(startsAt); ok {
			opensAt = venues.Shift(slot.RegOpensAt, to.Sub(from))
		}
	}
	doc, err := store.LoadGameDoc(r.Context(), s.h.Engine().DB, festID, slot.GameID)
	if err != nil {
		return err
	}
	comp := games.ParseTourComp(doc.SchemeJSON)
	var newSlot int64
	err = s.h.Engine().WithWriteTx(r.Context(), festID, "slot-clone", func(ctx context.Context, tx *sql.Tx) error {
		var err error
		newSlot, err = venues.CreateSlotTx(ctx, tx, festID, startsAt, 0, opensAt, comp)
		return err
	})
	if err != nil {
		return s.renderSlotPage(w, r, sc, err.Error(), "")
	}
	return s.redirectToSlot(w, r, festID, newSlot)
}

func (s *Server) handleApplicationStatus(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	festID := sc.FestID
	venue, slot, err := s.slotOf(r, sc)
	if err != nil {
		return err
	}
	if err := r.ParseForm(); err != nil {
		return route.BadRequest("bad form")
	}
	appID, err := strconv.ParseInt(r.PathValue("app"), 10, 64)
	if err != nil {
		return route.NotFound
	}
	status := r.Form.Get("status")
	switch status {
	case venues.StatusAccepted, venues.StatusDeclined, venues.StatusPending:
	default:
		return route.BadRequest("unknown status")
	}
	err = s.h.Engine().WithWriteTx(r.Context(), festID, "slot-application-status", func(ctx context.Context, tx *sql.Tx) error {
		return venues.SetStatusTx(ctx, tx, slot, venue.City, appID, status)
	})
	if errors.Is(err, venues.ErrHasResults) {
		return route.Conflict(venues.ErrHasResults.Error())
	}
	if err != nil {
		return s.renderSlotPage(w, r, sc, err.Error(), "")
	}
	s.h.Engine().InvalidateFestViewCache(festID)
	return s.redirectToSlot(w, r, festID, slot.ID)
}

func (s *Server) handleApplicationEdit(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	festID, authorID := sc.FestID, sc.User.UserID
	_, slot, err := s.slotOf(r, sc)
	if err != nil {
		return err
	}
	if err := r.ParseForm(); err != nil {
		return route.BadRequest("bad form")
	}
	appID, err := strconv.ParseInt(r.PathValue("app"), 10, 64)
	if err != nil {
		return route.NotFound
	}
	app, err := venues.LoadApplication(r.Context(), s.h.Engine().DB, appID)
	if err != nil || app.SlotID != slot.ID {
		return route.NotFound
	}
	err = s.h.Engine().WithWriteTx(r.Context(), festID, "slot-application-edit", func(ctx context.Context, tx *sql.Tx) error {
		_, err := venues.SaveVersionTx(ctx, tx, slot.ID, app.UserID, authorID,
			r.Form.Get("team_name"), formInt64(r.Form, "rating_team_id"), venues.ParseRoster(r.Form.Get("roster_json")))
		return err
	})
	if err != nil {
		return s.renderSlotPage(w, r, sc, err.Error(), "")
	}
	if err := s.reseatIfSeated(r.Context(), slot, app.UserID); err != nil {
		return err
	}
	return s.redirectToSlot(w, r, festID, slot.ID)
}

func (s *Server) handleApplicationRevert(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	festID, authorID := sc.FestID, sc.User.UserID
	_, slot, err := s.slotOf(r, sc)
	if err != nil {
		return err
	}
	if err := r.ParseForm(); err != nil {
		return route.BadRequest("bad form")
	}
	appID, err := strconv.ParseInt(r.PathValue("app"), 10, 64)
	if err != nil {
		return route.NotFound
	}
	app, err := venues.LoadApplication(r.Context(), s.h.Engine().DB, appID)
	if err != nil || app.SlotID != slot.ID {
		return route.NotFound
	}
	wanted := formInt64(r.Form, "seq")
	versions, err := venues.ApplicationVersions(r.Context(), s.h.Engine().DB, appID)
	if err != nil {
		return err
	}
	var chosen *venues.Version
	for i := range versions {
		if versions[i].Seq == wanted {
			chosen = &versions[i]
			break
		}
	}
	if chosen == nil {
		return route.NotFound
	}
	err = s.h.Engine().WithWriteTx(r.Context(), festID, "slot-application-revert", func(ctx context.Context, tx *sql.Tx) error {
		_, err := venues.SaveVersionTx(ctx, tx, slot.ID, app.UserID, authorID, chosen.TeamName, chosen.RatingTeamID, chosen.Roster)
		return err
	})
	if err != nil {
		return s.renderSlotPage(w, r, sc, err.Error(), "")
	}
	if err := s.reseatIfSeated(r.Context(), slot, app.UserID); err != nil {
		return err
	}
	return s.redirectToSlot(w, r, festID, slot.ID)
}

func (s *Server) loadHostVenues(ctx context.Context, userID int64) ([]venues.Venue, error) {
	return venues.VenuesOf(ctx, s.h.Engine().DB, userID)
}

// gameProgress is the OD page's own header line — not-started until a
// question is entered, then how far the sitting has got.
func (s *Server) gameProgress(ctx context.Context, festID, gameID int64) string {
	doc, err := store.LoadGameDoc(ctx, s.h.Engine().DB, festID, gameID)
	if err != nil {
		return ""
	}
	var state games.ODState
	if err := json.Unmarshal([]byte(doc.State), &state); err != nil {
		return ""
	}
	last := 0
	for q, entries := range state.Entries {
		entered := q < len(state.Completed) && state.Completed[q]
		for _, number := range entries {
			entered = entered || number > 0
		}
		if entered {
			last = q + 1
		}
	}
	if last == 0 {
		return strs.Venues.Errors.GameNotStarted()
	}
	return strs.Venues.Errors.GameEntered(strconv.Itoa(last))
}
