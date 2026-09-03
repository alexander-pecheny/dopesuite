package hostpages

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"dope/dope/domain/games"
	"dope/dope/domain/view"
	"dope/dope/platform/roles"
	"dope/dope/platform/util"
	"dope/dope/storage/festaccess"
	"dope/dope/storage/store"
	"dope/dope/web/pages"
	dopeui "dope/dope/web/ui"
	dopestrings "dope/i18nstrings"

	"pecheny.me/dopecore/session"

	"dope/dope/web/route"
)

type hostFestDashData struct {
	Fest            view.HostFest
	Description     string
	Slug            string
	RatingID        int64
	Games           []PublicFestGame
	Access          []festaccess.HostAccessMember
	TeamCount       int
	PlayerCount     int
	NumbersAssigned int
	NumbersAllSet   bool
	CurrentRole     string
	CanManageFest   bool
	CanManageGames  bool
	CanManageAccess bool
	CanDeleteFest   bool
	IsCreator       bool
	Error           string
	AccessError     string
	AccessNotice    string
	ImportError     string
	ImportNotice    string
	RosterError     string
	RosterNotice    string
}

type hostDashMessages struct {
	FormError    string
	AccessError  string
	AccessNotice string
	ImportError  string
	ImportNotice string
	RosterError  string
	RosterNotice string
}

// hostFestDashDoc builds the fest dashboard: the (role-gated) fest-edit form, the
// games list with per-row settings/clear/delete controls, the access management
// section (bulk-action dialog + editable roster table), the participants links,
// and the delete-fest section. Confirms and the bulk dialog run through
// pageforms.js data-attributes (no inline on* handlers).
func hostFestDashDoc(data hostFestDashData) *dopeui.Doc {
	ref := data.Fest.Ref()
	s := dopestrings.Default
	page := []dopeui.Item{
		dopeui.Title(s.Host.Dash.PageTitle(data.Fest.Title)), dopeui.PagePublic, dopeui.Classicscripts("dist/pageforms.js"),
	}
	if data.Fest.IsPublic {
		page = append(page,
			dopeui.Data("jump-label", s.Host.Dash.JumpLabel()),
			dopeui.Data("jump-href", "/fest/"+ref),
			dopeui.Data("jump-title", s.Host.Dash.JumpTitle()),
		)
	}
	page = append(page, dopeui.Publictopbar(pages.Trail(pages.HostCrumbs(), data.Fest.Title)))
	if data.Error != "" {
		page = append(page, dopeui.Empty(dopeui.Text(data.Error)))
	}
	if data.CanManageFest {
		page = append(page, hostDashFestForm(data, ref))
	}
	page = append(page, hostDashGamesSection(data, ref))
	if data.CanManageAccess {
		page = append(page, hostDashAccessSection(data, ref))
	}
	if data.CanManageFest {
		page = append(page, hostDashRosterSection(data, ref))
	}
	if data.CanDeleteFest {
		page = append(page, dopeui.Section(
			dopeui.Subhead(dopeui.Text(s.Host.Dash.DeleteSubhead())),
			dopeui.Form(dopeui.DirCol, dopeui.Method("post"), dopeui.Action("/host/fest/"+ref+"/delete"), dopeui.Autocomplete("off"),
				dopeui.Data("confirm", s.Host.Dash.DeleteConfirm()),
				dopeui.Note(dopeui.Text(s.Host.Dash.DeleteNote())),
				dopeui.Row(dopeui.Button(dopeui.Danger, dopeui.Submit(), dopeui.Text(s.Host.Dash.DeleteSubmit()))),
			),
		))
	}
	return &dopeui.Doc{Nodes: []dopeui.Node{dopeui.Page(page...)}}
}

func hostDashFestForm(data hostFestDashData, ref string) *dopeui.Element {
	s := dopestrings.Default
	ratingID := ""
	if data.RatingID != 0 {
		ratingID = strconv.FormatInt(data.RatingID, 10)
	}
	pub := dopeui.Checkbox(dopeui.Name("is_public"), dopeui.Value("1"), dopeui.Text(s.Host.Dash.PublicLabel()))
	if data.Fest.IsPublic {
		pub = dopeui.Checkbox(dopeui.Name("is_public"), dopeui.Value("1"), dopeui.Checked(), dopeui.Text(s.Host.Dash.PublicLabel()))
	}
	return dopeui.Form(dopeui.DirCol, dopeui.Method("post"), dopeui.Action("/host/fest/"+ref), dopeui.Autocomplete("off"),
		dopeui.Field(dopeui.Label(s.Host.Dash.TitleLabel()), dopeui.Textfield(dopeui.Name("title"), dopeui.Value(data.Fest.Title), dopeui.Required())),
		dopeui.Field(dopeui.Label(s.Host.Dash.DescriptionLabel()), dopeui.Editor(dopeui.Name("description"), dopeui.Rows("6"), dopeui.Text(data.Description))),
		dopeui.Field(dopeui.Label(s.Host.Dash.SlugLabel()),
			dopeui.Textfield(dopeui.Name("slug"), dopeui.Value(data.Slug), dopeui.Pattern("[a-z0-9-]+"), dopeui.Placeholder("my-fest"))),
		dopeui.Field(dopeui.Label(s.Host.Dash.StartDateLabel()), dopeui.Textfield(dopeui.Name("start_date"), dopeui.Value(data.Fest.StartDate))),
		dopeui.Field(dopeui.Label(s.Host.Dash.EndDateLabel()), dopeui.Textfield(dopeui.Name("end_date"), dopeui.Value(data.Fest.EndDate))),
		dopeui.Field(dopeui.Label("rating.chgk.info ID"), dopeui.Textfield(dopeui.Name("rating_id"), dopeui.Value(ratingID), dopeui.Inputmode("numeric"))),
		pub,
		dopeui.Row(dopeui.Button(dopeui.Submit(), dopeui.Text(s.Host.Dash.SaveSubmit()))),
	)
}

func hostDashGamesSection(data hostFestDashData, ref string) *dopeui.Element {
	s := dopestrings.Default
	sect := []dopeui.Item{dopeui.Subhead(dopeui.Text(s.Host.Dash.GamesSubhead()))}
	if len(data.Games) > 0 {
		rows := make([]dopeui.Item, 0, len(data.Games))
		for _, g := range data.Games {
			base := "/host/fest/" + ref + "/game/" + g.Ref()
			link := []dopeui.Item{dopeui.Href(base + "/"), dopeui.Listtitle(dopeui.Text(g.Title))}
			if g.Slug != "" {
				link = append(link, dopeui.Muted(dopeui.Text(g.Slug)))
			}
			row := []dopeui.Item{dopeui.Rowlink(link...)}
			if data.CanManageGames {
				row = append(row,
					dopeui.Button(dopeui.Href(base+"/settings"), dopeui.Text(s.Host.Dash.SettingsBtn())),
					dopeui.Form(dopeui.Method("post"), dopeui.Action(base+"/clear"),
						dopeui.Data("confirm", s.Host.Dash.ClearConfirm()),
						dopeui.Button(dopeui.Danger, dopeui.Submit(), dopeui.Text(s.Host.Dash.ClearBtn()))),
					dopeui.Form(dopeui.Method("post"), dopeui.Action(base+"/delete"),
						dopeui.Data("confirm", s.Host.Dash.DeleteGameConfirm()),
						dopeui.Button(dopeui.Danger, dopeui.Submit(), dopeui.Text(s.Host.Dash.DeleteBtn()))),
				)
			}
			rows = append(rows, dopeui.Actionrow(row...))
		}
		sect = append(sect, dopeui.Actionlist(rows...))
	} else {
		sect = append(sect, dopeui.Empty(dopeui.Text(s.Host.Dash.GamesEmpty())))
	}
	if data.CanManageGames {
		sect = append(sect, dopeui.Row(dopeui.Button(dopeui.Href("/host/fest/"+ref+"/game/new"), dopeui.Text(s.Host.Dash.AddGameBtn()))))
	}
	return dopeui.Section(sect...)
}

func hostDashAccessSection(data hostFestDashData, ref string) *dopeui.Element {
	s := dopestrings.Default
	sect := []dopeui.Item{dopeui.ID("access"), dopeui.Subhead(dopeui.Text(s.Host.Dash.AccessSubhead()))}
	if data.AccessError != "" {
		sect = append(sect, dopeui.Empty(dopeui.Text(data.AccessError)))
	}
	if data.AccessNotice != "" {
		sect = append(sect, dopeui.Note(dopeui.Text(data.AccessNotice)))
	}
	sect = append(sect,
		dopeui.Row(dopeui.Button(dopeui.Data("dialog-open", "bulkAccessDialog"), dopeui.Text(s.Host.Dash.BulkLabel()))),
		dopeui.Dialog(dopeui.ID("bulkAccessDialog"),
			dopeui.Form(dopeui.DirCol, dopeui.Method("post"), dopeui.Action("/host/fest/"+ref+"/access#access"), dopeui.Autocomplete("off"),
				dopeui.Subhead(dopeui.Text(s.Host.Dash.BulkLabel())),
				dopeui.Hiddenfield(dopeui.Name("bulk_access"), dopeui.Value("1")),
				dopeui.Field(dopeui.Label(s.Host.Dash.BulkDataLabel()),
					dopeui.Editor(dopeui.Name("bulk_access_lines"), dopeui.Rows("8"),
						dopeui.Placeholder("username1:host\nusername2:host\nusername3:admin\nusername4:remove"), dopeui.Required())),
				dopeui.Row(
					dopeui.Button(dopeui.Submit(), dopeui.Text(s.Host.Dash.BulkApply())),
					dopeui.Button(dopeui.Data("dialog-close", ""), dopeui.Text(s.Host.Dash.CancelBtn())),
				),
			),
		),
	)

	rows := []dopeui.Item{dopeui.Trow(
		dopeui.Hcell(dopeui.Text(s.Host.Dash.ColNickname())), dopeui.Hcell(dopeui.Text(s.Host.Dash.ColRole())), dopeui.Hcell(),
	)}
	for _, m := range data.Access {
		uid := strconv.FormatInt(m.UserID, 10)
		var roleCell, actionCell *dopeui.Element
		if m.IsCreator {
			roleCell = dopeui.Cell(dopeui.Hiddenfield(dopeui.Name("role_"+uid), dopeui.Value("creator")), dopeui.Text("creator"))
			actionCell = dopeui.Cell()
		} else {
			roleCell = dopeui.Cell(dopeui.Selectfield(dopeui.Name("role_"+uid), dopeui.Data("autosubmit", ""),
				roleOption("admin", m.Role), roleOption("host", m.Role)))
			actionCell = dopeui.Cell(dopeui.Button(dopeui.Danger, dopeui.Submit(), dopeui.Name("delete_"+uid), dopeui.Value("1"),
				dopeui.Data("confirm", s.Host.Dash.DeleteAccessConfirm(m.Nickname)), dopeui.Text(s.Host.Dash.DeleteBtn())))
		}
		rows = append(rows, dopeui.Trow(dopeui.Cell(dopeui.Text(m.Nickname)), roleCell, actionCell))
	}
	rows = append(rows, dopeui.Trow(
		dopeui.Cell(dopeui.Textfield(dopeui.Name("new_nickname"), dopeui.Placeholder("nickname"))),
		dopeui.Cell(dopeui.Selectfield(dopeui.Name("new_role"),
			dopeui.Option(dopeui.Value("host"), dopeui.Text("host")),
			dopeui.Option(dopeui.Value("admin"), dopeui.Text("admin")))),
		dopeui.Cell(dopeui.Button(dopeui.Submit(), dopeui.Name("add_access"), dopeui.Value("1"), dopeui.Text(s.Host.Dash.AddBtn()))),
	))
	sect = append(sect,
		dopeui.Form(dopeui.Method("post"), dopeui.Action("/host/fest/"+ref+"/access#access"), dopeui.Autocomplete("off"),
			dopeui.Table(append([]dopeui.Item{dopeui.Scroll()}, rows...)...),
		),
	)
	return dopeui.Section(sect...)
}

func roleOption(value, current string) *dopeui.Element {
	if value == current {
		return dopeui.Option(dopeui.Value(value), dopeui.Selected(), dopeui.Text(value))
	}
	return dopeui.Option(dopeui.Value(value), dopeui.Text(value))
}

func hostDashRosterSection(data hostFestDashData, ref string) *dopeui.Element {
	s := dopestrings.Default
	sect := []dopeui.Item{dopeui.Subhead(dopeui.Text(s.Host.Dash.RosterSubhead()))}
	if data.RosterError != "" {
		sect = append(sect, dopeui.Empty(dopeui.Text(data.RosterError)))
	}
	if data.RosterNotice != "" {
		sect = append(sect, dopeui.Note(dopeui.Text(data.RosterNotice)))
	}
	rows := []dopeui.Item{
		dopeui.Listrow(dopeui.Href("/host/fest/"+ref+"/teams"), dopeui.Listtitle(dopeui.Text(s.Host.Dash.RosterTeamsLink())), dopeui.Muted(dopeui.Text(strconv.Itoa(data.TeamCount)))),
		dopeui.Listrow(dopeui.Href("/host/fest/"+ref+"/players"), dopeui.Listtitle(dopeui.Text(s.Host.Dash.RosterPlayersLink())), dopeui.Muted(dopeui.Text(strconv.Itoa(data.PlayerCount)))),
	}
	if data.TeamCount > 0 {
		status := s.Host.Dash.NumbersStatusUnset()
		if data.NumbersAllSet {
			status = s.Host.Dash.NumbersStatusDone()
		} else if data.NumbersAssigned > 0 {
			status = s.Host.Dash.NumbersStatusPartial(strconv.Itoa(data.NumbersAssigned), strconv.Itoa(data.TeamCount))
		}
		rows = append(rows, dopeui.Listrow(dopeui.Href("/host/fest/"+ref+"/numbers"),
			dopeui.Listtitle(dopeui.Text(s.Host.Dash.NumbersLink())), dopeui.Muted(dopeui.Text(status))))
	}
	ratingStatus := s.Host.Dash.RatingStatusNone()
	if data.RatingID != 0 {
		ratingStatus = "rating " + strconv.FormatInt(data.RatingID, 10)
	}
	rows = append(rows,
		dopeui.Listrow(dopeui.Href("/host/fest/"+ref+"/rating/import"),
			dopeui.Listtitle(dopeui.Text(s.Host.Dash.RosterImportLink())), dopeui.Muted(dopeui.Text(ratingStatus))),
		dopeui.Listrow(dopeui.Href("/host/fest/"+ref+"/audit"),
			dopeui.Listtitle(dopeui.Text(s.Host.Dash.AuditLink())), dopeui.Muted(dopeui.Text(s.Host.Dash.AuditMuted()))),
	)
	sect = append(sect, dopeui.List(rows...))
	return dopeui.Section(sect...)
}

func (s *Server) handleHostCreateFest(w http.ResponseWriter, r *http.Request, user session.User) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	title := strings.TrimSpace(r.Form.Get("title"))
	if title == "" {
		s.renderHostLanding(w, r, dopestrings.Default.Host.Dash.ErrorTitleRequired())
		return
	}
	description := r.Form.Get("description")
	startDate := strings.TrimSpace(r.Form.Get("start_date"))
	endDate := strings.TrimSpace(r.Form.Get("end_date"))
	ratingID := util.ParseOptionalInt64(r.Form.Get("rating_id"))
	isPublic := r.Form.Get("is_public") == "1"

	now := util.UtcNow()
	tx, err := s.h.Engine().BeginWriteTx(r.Context())
	if err != nil {
		route.WriteError(w, r, err)
		return
	}
	defer tx.Rollback()
	festID, err := store.InsertReturningID(r.Context(), tx, `
insert into fests(slug, title, description, rating_id, created_by, revision, created_at, updated_at, start_date, end_date, is_public)
values(?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?)`,
		nil, title, description, ratingID, user.UserID, now, now,
		util.NullableString(startDate), util.NullableString(endDate), util.BoolToInt(isPublic))
	if err != nil {
		route.WriteError(w, r, err)
		return
	}
	if _, err := tx.ExecContext(r.Context(), `
insert into fest_organizers(fest_id, user_id, role, added_at)
values(?, ?, 'creator', ?)`, festID, user.UserID, now); err != nil {
		route.WriteError(w, r, err)
		return
	}
	if err := tx.Commit(); err != nil {
		route.WriteError(w, r, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/host/fest/%d", festID), http.StatusSeeOther)
}

func (s *Server) handleHostUpdateFest(w http.ResponseWriter, r *http.Request, festID int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	title := strings.TrimSpace(r.Form.Get("title"))
	if title == "" {
		s.renderHostFestDashboard(w, r, festID, hostDashMessages{FormError: dopestrings.Default.Host.Dash.ErrorTitleRequired()})
		return
	}
	description := r.Form.Get("description")
	startDate := strings.TrimSpace(r.Form.Get("start_date"))
	endDate := strings.TrimSpace(r.Form.Get("end_date"))
	ratingID := util.ParseOptionalInt64(r.Form.Get("rating_id"))
	isPublic := r.Form.Get("is_public") == "1"
	slug := strings.TrimSpace(r.Form.Get("slug"))
	var slugValue any
	if slug != "" {
		if err := util.ValidateSlug(slug); err != nil {
			s.renderHostFestDashboard(w, r, festID, hostDashMessages{FormError: dopestrings.Default.Host.Dash.ErrorSlugInvalid(err.Error())})
			return
		}
		if taken, err := s.slugTakenByOtherFest(r.Context(), slug, festID); err != nil {
			route.WriteError(w, r, err)
			return
		} else if taken {
			s.renderHostFestDashboard(w, r, festID, hostDashMessages{FormError: dopestrings.Default.Host.Dash.ErrorSlugTaken()})
			return
		}
		slugValue = slug
	}

	if _, err := s.h.Engine().WriteExec(r.Context(), `
update fests
set title = ?, slug = ?, description = ?, rating_id = ?, start_date = ?, end_date = ?, is_public = ?, updated_at = ?
where id = ?`,
		title, slugValue, description, ratingID,
		util.NullableString(startDate), util.NullableString(endDate), util.BoolToInt(isPublic),
		util.UtcNow(), festID); err != nil {
		route.WriteError(w, r, err)
		return
	}
	s.h.Engine().InvalidateFestViewCache(festID)
	redirectRef := slug
	if redirectRef == "" {
		redirectRef = fmt.Sprintf("%d", festID)
	}
	http.Redirect(w, r, fmt.Sprintf("/host/fest/%s", redirectRef), http.StatusSeeOther)
}

func (s *Server) handleHostSaveAccess(w http.ResponseWriter, r *http.Request, festID, actorID int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if r.Form.Get("bulk_access") == "1" {
		count, err := festaccess.SaveFestAccessBulk(s.h.Engine(), r.Context(), festID, actorID, r.Form.Get("bulk_access_lines"))
		if err != nil {
			s.renderHostFestDashboard(w, r, festID, hostDashMessages{AccessError: err.Error()})
			return
		}
		s.renderHostFestDashboard(w, r, festID, hostDashMessages{AccessNotice: dopestrings.Default.Host.Dash.BulkDoneNotice(strconv.Itoa(count))})
		return
	}
	if err := festaccess.SaveFestAccess(s.h.Engine(), r.Context(), festID, actorID, r.Form); err != nil {
		s.renderHostFestDashboard(w, r, festID, hostDashMessages{AccessError: err.Error()})
		return
	}
	s.renderHostFestDashboard(w, r, festID, hostDashMessages{AccessNotice: dopestrings.Default.Host.Dash.AccessSavedNotice()})
}

func (s *Server) slugTakenByOtherFest(ctx context.Context, slug string, festID int64) (bool, error) {
	var count int
	if err := s.h.Engine().DB.QueryRowContext(ctx, `select count(*) from fests where slug = ? and id <> ?`, slug, festID).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *Server) festRefOrID(ctx context.Context, festID int64) string {
	var slug string
	if err := s.h.Engine().DB.QueryRowContext(ctx, `select coalesce(slug, '') from fests where id = ?`, festID).Scan(&slug); err == nil && slug != "" {
		return slug
	}
	return fmt.Sprintf("%d", festID)
}

func (s *Server) gameRefOrID(ctx context.Context, gameID int64) string {
	var slug string
	if err := s.h.Engine().DB.QueryRowContext(ctx, `select coalesce(slug, '') from games where id = ?`, gameID).Scan(&slug); err == nil && slug != "" {
		return slug
	}
	return fmt.Sprintf("%d", gameID)
}

func (s *Server) handleHostDeleteFest(w http.ResponseWriter, r *http.Request, festID, userID int64) {
	creator, err := s.isFestCreator(r.Context(), festID, userID)
	if err != nil {
		route.WriteError(w, r, err)
		return
	}
	if !creator {
		http.Error(w, "only fest creator can delete fest", http.StatusForbidden)
		return
	}
	s.h.Engine().Mu.Lock()
	defer s.h.Engine().Mu.Unlock()
	result, err := s.h.Engine().WriteExec(r.Context(), `delete from fests where id = ? and created_by = ?`, festID, userID)
	if err != nil {
		route.WriteError(w, r, err)
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		http.NotFound(w, r)
		return
	}
	if s.h.Engine().FestID == festID {
		s.h.Engine().FestID = 0
		s.h.Engine().ActiveGameID = 0
		s.h.Engine().ActiveMatchCode = ""
	}
	http.Redirect(w, r, "/host", http.StatusSeeOther)
}

func (s *Server) renderHostFestDashboard(w http.ResponseWriter, r *http.Request, festID int64, msgs hostDashMessages) {
	var (
		title       string
		slug        string
		description string
		startDate   sql.NullString
		endDate     sql.NullString
		ratingID    sql.NullInt64
		isPublic    int
	)
	if err := s.h.Engine().DB.QueryRowContext(r.Context(), `
select title, coalesce(slug, ''), description, start_date, end_date, rating_id, is_public
from fests where id = ?`, festID).Scan(&title, &slug, &description, &startDate, &endDate, &ratingID, &isPublic); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		route.WriteError(w, r, err)
		return
	}
	gameRows, err := LoadFestGames(r.Context(), s.h.Engine().DB, festID)
	if err != nil {
		route.WriteError(w, r, err)
		return
	}
	festRef := slug
	if festRef == "" {
		festRef = fmt.Sprintf("%d", festID)
	}
	hostGames := make([]PublicFestGame, len(gameRows))
	for i, g := range gameRows {
		hostGames[i] = PublicFestGame{
			ID:    g.ID,
			Slug:  g.Slug,
			Code:  g.Code,
			Title: g.Title,
			Type:  games.Label(g.Type),
			URL:   fmt.Sprintf("/host/fest/%s/game/%s/", festRef, g.Ref()),
		}
	}
	teamCount, playerCount, err := s.loadHostFestRosterCounts(r.Context(), festID)
	if err != nil {
		route.WriteError(w, r, err)
		return
	}
	var numbersAssigned int
	if err := s.h.Engine().DB.QueryRowContext(r.Context(), `
select coalesce(sum(case when number is not null then 1 else 0 end), 0)
from fest_teams where fest_id = ? and deleted = 0`, festID).Scan(&numbersAssigned); err != nil {
		route.WriteError(w, r, err)
		return
	}
	currentRole := ""
	if user, ok := s.h.Engine().LookupSession(r); ok {
		currentRole, err = festaccess.FestUserRoleFromQuery(r.Context(), s.h.Engine().DB, festID, user.UserID)
		if err != nil {
			route.WriteError(w, r, err)
			return
		}
	}
	canManageFest := roles.CanManageFest(currentRole)
	canManageAccess := roles.CanManageAccess(currentRole)
	canDeleteFest := roles.CanDeleteFest(currentRole)
	canManageGames := canManageFest
	var access []festaccess.HostAccessMember
	if canManageAccess {
		access, err = festaccess.LoadFestAccessMembers(s.h.Engine(), r.Context(), festID)
		if err != nil {
			route.WriteError(w, r, err)
			return
		}
	}
	data := hostFestDashData{
		Fest: view.HostFest{
			ID:        festID,
			Slug:      slug,
			Title:     title,
			StartDate: startDate.String,
			EndDate:   endDate.String,
			Dates:     util.FormatFestDates(startDate.String, endDate.String),
			IsPublic:  isPublic == 1,
		},
		Description:     description,
		Slug:            slug,
		Games:           hostGames,
		Access:          access,
		TeamCount:       teamCount,
		PlayerCount:     playerCount,
		NumbersAssigned: numbersAssigned,
		NumbersAllSet:   teamCount > 0 && numbersAssigned == teamCount,
		CurrentRole:     currentRole,
		CanManageFest:   canManageFest,
		CanManageGames:  canManageGames,
		CanManageAccess: canManageAccess,
		CanDeleteFest:   canDeleteFest,
		IsCreator:       canDeleteFest,
		Error:           msgs.FormError,
		AccessError:     msgs.AccessError,
		AccessNotice:    msgs.AccessNotice,
		ImportError:     msgs.ImportError,
		ImportNotice:    msgs.ImportNotice,
		RosterError:     msgs.RosterError,
		RosterNotice:    msgs.RosterNotice,
	}
	if ratingID.Valid {
		data.RatingID = ratingID.Int64
	}
	pages.RenderDoc(w, s.h.Engine().AssetETags, hostFestDashDoc(data))
}

func (s *Server) loadHostFestRosterCounts(ctx context.Context, festID int64) (int, int, error) {
	var teamCount, playerCount int
	if err := s.h.Engine().DB.QueryRowContext(ctx, `select count(*) from fest_teams where fest_id = ? and deleted = 0`, festID).Scan(&teamCount); err != nil {
		return 0, 0, err
	}
	if err := s.h.Engine().DB.QueryRowContext(ctx, `select count(*) from fest_players where fest_id = ?`, festID).Scan(&playerCount); err != nil {
		return 0, 0, err
	}
	return teamCount, playerCount, nil
}

func (s *Server) loadFestRatingID(ctx context.Context, festID int64) (int64, error) {
	var ratingID sql.NullInt64
	if err := s.h.Engine().DB.QueryRowContext(ctx, `select rating_id from fests where id = ?`, festID).Scan(&ratingID); err != nil {
		return 0, err
	}
	if !ratingID.Valid {
		return 0, nil
	}
	return ratingID.Int64, nil
}

func (s *Server) loadHostFests(ctx context.Context, userID int64) ([]view.HostFest, error) {
	return store.CollectRows(ctx, s.h.Engine().DB, `
select t.id, coalesce(t.slug, ''), t.title, coalesce(t.start_date, ''), coalesce(t.end_date, ''), t.is_public
from fests t
join fest_organizers o on o.fest_id = t.id
where o.user_id = ?
order by case when t.start_date is null or t.start_date = '' then 1 else 0 end,
         t.start_date desc,
         t.id desc`, []any{userID}, func(rows *sql.Rows) (view.HostFest, error) {
		var t view.HostFest
		var pub int
		if err := rows.Scan(&t.ID, &t.Slug, &t.Title, &t.StartDate, &t.EndDate, &pub); err != nil {
			return t, err
		}
		t.IsPublic = pub == 1
		t.Dates = util.HumanizeFestDates(t.StartDate, t.EndDate, time.Now().Year())
		return t, nil
	})
}

// hostFestGroup is one collapsible bucket on the host landing page.
type hostFestGroup struct {
	Title string
	Fests []view.HostFest
}

// groupHostFests partitions the host's fests into current/future/past buckets
// relative to today ("YYYY-MM-DD"), sorts each bucket by start date descending
// (then title ascending), and drops empty buckets.
func groupHostFests(fests []view.HostFest, today string) []hostFestGroup {
	s := dopestrings.Default
	var current, future, past []view.HostFest
	for _, f := range fests {
		switch util.ClassifyFestDate(f.StartDate, f.EndDate, today) {
		case util.FestCurrent:
			current = append(current, f)
		case util.FestFuture:
			future = append(future, f)
		default:
			past = append(past, f)
		}
	}
	sortHostFests(current)
	sortHostFests(future)
	sortHostFests(past)
	groups := make([]hostFestGroup, 0, 3)
	for _, g := range []hostFestGroup{
		{Title: s.Host.Pages.GroupCurrent(), Fests: current},
		{Title: s.Host.Pages.GroupFuture(), Fests: future},
		{Title: s.Host.Pages.GroupPast(), Fests: past},
	} {
		if len(g.Fests) > 0 {
			groups = append(groups, g)
		}
	}
	return groups
}

func sortHostFests(fests []view.HostFest) {
	sort.SliceStable(fests, func(i, j int) bool {
		if fests[i].StartDate != fests[j].StartDate {
			return fests[i].StartDate > fests[j].StartDate // descending
		}
		return fests[i].Title < fests[j].Title
	})
}

func (s *Server) isFestCreator(ctx context.Context, festID, userID int64) (bool, error) {
	var n int
	err := s.h.Engine().DB.QueryRowContext(ctx, `
select count(*) from fests where id = ? and created_by = ?`, festID, userID).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
