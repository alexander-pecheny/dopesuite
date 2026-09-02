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

	"dope/dope/domain/flatgame"
	"dope/dope/domain/gamebuild"
	"dope/dope/domain/games"
	"dope/dope/domain/protocol"
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

// The Площадка handlers: the three public pages (/venues, /venue/{ref},
// /reg/{token}) and the Representative's writes, which the /host table below
// dispatches like every other host page.

// VenueRoutes is the public Площадка table: /venues, /venue/… and /reg/….
func (s *Server) VenueRoutes() *route.Table {
	s.venueOnce.Do(func() {
		t := route.New(s.h.Engine(), route.DenyAPI)
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

// HandleVenueRouter serves the public Площадка pages.
func (s *Server) HandleVenueRouter(w http.ResponseWriter, r *http.Request) {
	s.VenueRoutes().Mux.ServeHTTP(w, r)
}

// ---- /venues ----------------------------------------------------------------

func (s *Server) renderVenuesIndex(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	list, err := venues.PublicVenues(r.Context(), s.h.Engine().DB)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	rows := make([]VenueRow, 0, len(list))
	sortKeys := make(map[string]string, len(list))
	for _, v := range list {
		slots, err := venues.VenueSlots(r.Context(), s.h.Engine().DB, v.ID)
		if err != nil {
			return err
		}
		row := VenueRow{Ref: v.Ref(), Title: v.Title, City: v.City, RatingVenueID: v.RatingVenueID}
		if next, ok := nextSlot(slots, now); ok {
			row.NextSlot = next.StartsAt
			row.Registration = registrationLabel(next, now)
			row.Accepted = next.Accepted
		}
		sortKeys[row.Ref] = row.NextSlot
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := sortKeys[rows[i].Ref], sortKeys[rows[j].Ref]
		if (a == "") != (b == "") {
			return b == ""
		}
		return a < b
	})
	pages.RenderDoc(w, s.h.Engine().AssetETags, VenuesIndexDoc(rows))
	return nil
}

func nextSlot(slots []venues.Slot, now time.Time) (venues.Slot, bool) {
	for _, s := range slots {
		if at, ok := venues.ParseTime(s.StartsAt); ok && at.After(now) {
			return s, true
		}
	}
	return venues.Slot{}, false
}

func registrationLabel(slot venues.Slot, now time.Time) string {
	switch venues.Registration(slot.RegOpensAt, slot.RegClosed, now) {
	case venues.RegClosed:
		return "закрыта"
	case venues.RegScheduled:
		return "откроется " + slot.RegOpensAt
	default:
		return "открыта"
	}
}

// ---- /venue/{ref} -----------------------------------------------------------

func (s *Server) renderVenuePage(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	festID, err := store.ResolveFestID(r.Context(), s.h.Engine().DB, r.PathValue("ref"))
	if err != nil {
		return route.NotFound
	}
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
		if venues.Registration(slot.RegOpensAt, slot.RegClosed, now) == venues.RegOpen {
			row.RegHref = "/reg/" + slot.RegToken
		}
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

// ---- /reg/{token} -----------------------------------------------------------

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
		RatingTeamID: app.RatingTeamID, BuffTeamName: s.h.Engine().BuffMirror().TeamName(ctx, app.RatingTeamID),
		Roster: app.Roster, Flags: s.rosterFlags(ctx, app, slot), Number: app.Number,
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
		return s.renderRegPage(w, r, token, "Регистрация ещё не открыта.", "")
	}
	if state == venues.RegClosed {
		if _, err := venues.UserApplication(r.Context(), s.h.Engine().DB, slot.ID, sc.User.UserID); errors.Is(err, sql.ErrNoRows) {
			return s.renderRegPage(w, r, token, "Регистрация закрыта.", "")
		} else if err != nil {
			return err
		}
	}
	err = s.h.Engine().WithWriteTx(r.Context(), slot.FestID, "slot-application", func(ctx context.Context, tx *sql.Tx) error {
		_, err := venues.SaveVersionTx(ctx, tx, slot.ID, sc.User.UserID, sc.User.UserID,
			r.Form.Get("team_name"), formInt64(r.Form, "rating_team_id"), venues.ParseRoster(r.Form.Get("roster_json")))
		return err
	})
	if err != nil {
		return s.renderRegPage(w, r, token, err.Error(), "")
	}
	if err := s.reseatSlot(r.Context(), slot); err != nil {
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

// reseatSlot re-runs the Слот's seating, which is what carries a roster edit
// into an already-accepted team's participant_players.
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

// ---- the Representative's pages ---------------------------------------------

// IsVenue reports whether a fest is a Площадка.
func (s *Server) IsVenue(ctx context.Context, festID int64) bool {
	var kind string
	if err := s.h.Engine().DB.QueryRowContext(ctx, `select coalesce(kind, 'fest') from fests where id = ?`, festID).Scan(&kind); err != nil {
		return false
	}
	return kind == venues.KindVenue
}

func (s *Server) renderVenueDashboard(w http.ResponseWriter, r *http.Request, festID int64, errMsg, notice string) {
	_ = r.ParseForm()
	venue, err := venues.LoadVenue(r.Context(), s.h.Engine().DB, festID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	slots, err := venues.VenueSlots(r.Context(), s.h.Engine().DB, festID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	names := s.tournamentNames(r.Context(), slots)
	rows := make([]VenueDashSlot, 0, len(slots))
	for _, slot := range slots {
		rows = append(rows, VenueDashSlot{
			ID: slot.ID, Date: slot.StartsAt, Tournament: names[slot.RatingTournamentID],
			Accepted: slot.Accepted, Pending: slot.Pending,
			Href: "/host/fest/" + venue.Ref() + "/slot/" + strconv.FormatInt(slot.ID, 10),
		})
	}
	members, err := festaccess.LoadFestAccessMembers(s.h.Engine(), r.Context(), festID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	user, _ := s.h.Engine().LookupSession(r)
	role, _ := festaccess.FestUserRoleFromQuery(r.Context(), s.h.Engine().DB, festID, user.UserID)
	pages.RenderDoc(w, s.h.Engine().AssetETags, venueDashDoc(venueDashData{
		Venue: venue, Slots: rows, Access: members,
		CanManage: roles.CanManageFest(role), CanDelete: roles.CanDeleteFest(role),
		Error: errMsg, Notice: notice,
	}))
}

func (s *Server) canManage(ctx context.Context, festID int64, r *http.Request) bool {
	user, ok := s.h.Engine().LookupSession(r)
	if !ok {
		return false
	}
	role, err := festaccess.FestUserRoleFromQuery(ctx, s.h.Engine().DB, festID, user.UserID)
	return err == nil && roles.CanManageFest(role)
}

func (s *Server) handleHostCreateVenue(w http.ResponseWriter, r *http.Request, userID int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	title := strings.TrimSpace(r.Form.Get("title"))
	if title == "" {
		s.renderHostLanding(w, r, "Название площадки обязательно.")
		return
	}
	slug := strings.TrimSpace(r.Form.Get("slug"))
	if slug != "" {
		if err := util.ValidateSlug(slug); err != nil {
			s.renderHostLanding(w, r, "Slug: "+err.Error())
			return
		}
	}
	now := util.UtcNow()
	var festID int64
	err := s.h.Engine().WithWriteTx(r.Context(), 0, "venue-create", func(ctx context.Context, tx *sql.Tx) error {
		id, err := store.InsertReturningID(ctx, tx, `
insert into fests(slug, title, description, kind, city, rating_venue_id, created_by, revision, created_at, updated_at, is_public)
values(?, ?, ?, 'venue', ?, ?, ?, 1, ?, ?, ?)`,
			util.NullableString(slug), title, r.Form.Get("description"),
			strings.TrimSpace(r.Form.Get("city")), util.ParseOptionalInt64(r.Form.Get("rating_venue_id")),
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
		s.renderHostLanding(w, r, err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/host/fest/%d", festID), http.StatusSeeOther)
}

func (s *Server) handleHostUpdateVenue(w http.ResponseWriter, r *http.Request, festID int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	title := strings.TrimSpace(r.Form.Get("title"))
	if title == "" {
		s.renderVenueDashboard(w, r, festID, "Название обязательно.", "")
		return
	}
	slug := strings.TrimSpace(r.Form.Get("slug"))
	if slug != "" {
		if err := util.ValidateSlug(slug); err != nil {
			s.renderVenueDashboard(w, r, festID, "Slug: "+err.Error(), "")
			return
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
		s.renderVenueDashboard(w, r, festID, err.Error(), "")
		return
	}
	s.h.Engine().InvalidateFestViewCache(festID)
	http.Redirect(w, r, "/host/fest/"+s.festRefOrID(r.Context(), festID), http.StatusSeeOther)
}

// tourComposition is a Слот's ОД shape: the tournament's own questions_by_tour
// when it names one, else what the form typed.
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

func (s *Server) handleHostCreateSlot(w http.ResponseWriter, r *http.Request, festID int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	tournamentID := formInt64(r.Form, "rating_tournament_id")
	comp := s.tourComposition(r.Context(), tournamentID, r.Form.Get("tour_comp"))
	var slotID int64
	err := s.h.Engine().WithWriteTx(r.Context(), festID, "slot-create", func(ctx context.Context, tx *sql.Tx) error {
		gameID, err := gamebuild.Create(ctx, tx, gamebuild.Spec{FestID: festID, Type: games.OD, ODTourComp: comp})
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
update games set team_list_source = 'game', roster_source = 'game' where id = ?`, gameID); err != nil {
			return err
		}
		slotID, err = venues.CreateSlotTx(ctx, tx, festID, gameID, r.Form.Get("starts_at"), tournamentID, "")
		return err
	})
	if err != nil {
		s.renderVenueDashboard(w, r, festID, err.Error(), "")
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/host/fest/%s/slot/%d", s.festRefOrID(r.Context(), festID), slotID), http.StatusSeeOther)
}

func (s *Server) slotOf(r *http.Request, festID int64) (venues.Venue, venues.Slot, error) {
	venue, err := venues.LoadVenue(r.Context(), s.h.Engine().DB, festID)
	if err != nil {
		return venue, venues.Slot{}, err
	}
	slotID, err := strconv.ParseInt(r.PathValue("slot"), 10, 64)
	if err != nil {
		return venue, venues.Slot{}, sql.ErrNoRows
	}
	slot, err := venues.LoadSlot(r.Context(), s.h.Engine().DB, slotID)
	if err != nil {
		return venue, slot, err
	}
	if slot.FestID != festID {
		return venue, slot, sql.ErrNoRows
	}
	return venue, slot, nil
}

func (s *Server) renderSlotPage(w http.ResponseWriter, r *http.Request, festID int64, errMsg, notice string) error {
	_ = r.ParseForm()
	venue, slot, err := s.slotOf(r, festID)
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
	gameRef := slot.GameSlug
	if gameRef == "" {
		gameRef = strconv.FormatInt(slot.GameID, 10)
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
		GameHref:  "/host/fest/" + venue.Ref() + "/game/" + gameRef + "/",
		RegURL:    publicURL(r, "/reg/"+slot.RegToken),
		CanManage: s.canManage(r.Context(), festID, r),
		Error:     errMsg, Notice: notice,
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

// publicURL is the absolute link a Representative copies and posts.
func publicURL(r *http.Request, path string) string {
	scheme := "https"
	if r.TLS == nil && !strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "http"
	}
	return scheme + "://" + r.Host + path
}

func (s *Server) handleSlotSave(w http.ResponseWriter, r *http.Request, festID int64) error {
	_, slot, err := s.slotOf(r, festID)
	if err != nil {
		return err
	}
	if err := r.ParseForm(); err != nil {
		return route.BadRequest("bad form")
	}
	tournamentID := formInt64(r.Form, "rating_tournament_id")
	err = s.h.Engine().WithWriteTx(r.Context(), festID, "slot-save", func(ctx context.Context, tx *sql.Tx) error {
		if err := venues.UpdateSlotTx(ctx, tx, slot.ID, r.Form.Get("starts_at"), tournamentID,
			r.Form.Get("reg_opens_at"), r.Form.Get("reg_closed") == "1"); err != nil {
			return err
		}
		if tournamentID <= 0 || tournamentID == slot.RatingTournamentID {
			return nil
		}
		comp := s.tourComposition(ctx, tournamentID, "")
		return retourGameTx(ctx, tx, festID, slot.GameID, comp)
	})
	if err != nil {
		return s.renderSlotPage(w, r, festID, err.Error(), "")
	}
	s.h.Engine().InvalidateFestViewCache(festID)
	return s.redirectToSlot(w, r, festID, slot.ID)
}

func (s *Server) redirectToSlot(w http.ResponseWriter, r *http.Request, festID, slotID int64) error {
	http.Redirect(w, r, fmt.Sprintf("/host/fest/%s/slot/%d", s.festRefOrID(r.Context(), festID), slotID), http.StatusSeeOther)
	return nil
}

func (s *Server) handleSlotToken(w http.ResponseWriter, r *http.Request, festID int64) error {
	_, slot, err := s.slotOf(r, festID)
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

func (s *Server) handleSlotClone(w http.ResponseWriter, r *http.Request, festID int64) error {
	_, slot, err := s.slotOf(r, festID)
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
		gameID, err := gamebuild.Create(ctx, tx, gamebuild.Spec{FestID: festID, Type: games.OD, ODTourComp: comp})
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
update games set team_list_source = 'game', roster_source = 'game' where id = ?`, gameID); err != nil {
			return err
		}
		newSlot, err = venues.CreateSlotTx(ctx, tx, festID, gameID, startsAt, 0, opensAt)
		return err
	})
	if err != nil {
		return s.renderSlotPage(w, r, festID, err.Error(), "")
	}
	return s.redirectToSlot(w, r, festID, newSlot)
}

// retourGameTx rewrites a Слот's Game to a new tour composition, keeping the
// teams it already seats. It is only ever run before the Слот is played —
// naming the tournament is what sets the tours — so it starts from a pristine
// document rather than trying to reshape the answers grid.
func retourGameTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, comp []int) error {
	doc, err := store.LoadGameDoc(ctx, tx, festID, gameID)
	if err != nil {
		return err
	}
	if sameComp(games.ParseTourComp(doc.SchemeJSON), comp) {
		return nil
	}
	var current games.ODState
	_ = json.Unmarshal([]byte(doc.State), &current)
	teams := make([]protocol.RosterTeam, 0, len(current.Teams))
	for _, t := range current.Teams {
		teams = append(teams, protocol.RosterTeam{Name: t.Name, City: t.City, Number: t.Number})
	}
	emptyScheme, emptyState := games.ODEmptyGameJSON(doc.Slug.String, "", comp)
	scheme, state, ok, err := protocol.FoldRoster(games.OD, string(emptyScheme), string(emptyState), teams, nil)
	if err != nil {
		return err
	}
	if !ok {
		scheme, state = emptyScheme, emptyState
	}
	if _, err := tx.ExecContext(ctx, `update games set scheme_json = ?, updated_at = ? where id = ? and fest_id = ?`,
		string(scheme), util.UtcNow(), gameID, festID); err != nil {
		return err
	}
	return flatgame.SetStateTx(ctx, tx, festID, gameID, string(state))
}

func sameComp(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (s *Server) handleApplicationStatus(w http.ResponseWriter, r *http.Request, festID int64) error {
	venue, slot, err := s.slotOf(r, festID)
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
		return s.renderSlotPage(w, r, festID, err.Error(), "")
	}
	s.h.Engine().InvalidateFestViewCache(festID)
	return s.redirectToSlot(w, r, festID, slot.ID)
}

func (s *Server) handleApplicationEdit(w http.ResponseWriter, r *http.Request, festID, authorID int64) error {
	_, slot, err := s.slotOf(r, festID)
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
		return s.renderSlotPage(w, r, festID, err.Error(), "")
	}
	if err := s.reseatSlot(r.Context(), slot); err != nil {
		return err
	}
	return s.redirectToSlot(w, r, festID, slot.ID)
}

func (s *Server) handleApplicationRevert(w http.ResponseWriter, r *http.Request, festID, authorID int64) error {
	_, slot, err := s.slotOf(r, festID)
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
		return s.renderSlotPage(w, r, festID, err.Error(), "")
	}
	if err := s.reseatSlot(r.Context(), slot); err != nil {
		return err
	}
	return s.redirectToSlot(w, r, festID, slot.ID)
}

// loadHostVenues are the Площадки a user represents.
func (s *Server) loadHostVenues(ctx context.Context, userID int64) ([]venues.Venue, error) {
	return store.CollectRows(ctx, s.h.Engine().DB, `
select f.id, coalesce(f.slug, ''), f.title, coalesce(f.city, ''), f.description,
       coalesce(f.rating_venue_id, 0), f.is_public
from fests f
join fest_organizers o on o.fest_id = f.id
where o.user_id = ? and f.kind = 'venue'
order by f.title, f.id`, []any{userID}, func(rows *sql.Rows) (venues.Venue, error) {
		var v venues.Venue
		var public int
		err := rows.Scan(&v.ID, &v.Slug, &v.Title, &v.City, &v.Description, &v.RatingVenueID, &public)
		v.IsPublic = public == 1
		return v, err
	})
}
