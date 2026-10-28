package hostpages

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"dope/dope/domain/games"
	"dope/dope/domain/imports"
	"dope/dope/domain/ratingexport"
	"dope/dope/domain/roster"
	"dope/dope/domain/view"
	"dope/dope/storage/store"
	"dope/dope/web/pages"
	"dope/dope/web/route"
	dopeui "dope/dope/web/ui"
	dopestrings "dope/i18nstrings"

	corei18n "code.pecheny.me/pecheny/dopesuite/dopecore/i18nstrings"
	"code.pecheny.me/pecheny/dopesuite/dopecore/idstr"
)

// The export of a fest's rosters and its OD's results to rating.chgk.info,
// sent with the user's own token for the site (ADR-0034). The page shows
// what would go first; only its button sends.

type exportGame struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}

// exportGames is the fest's ODs of every team, which the site's tournament
// is.
func (s *Server) exportGames(ctx context.Context, festID int64) ([]exportGame, error) {
	type row struct {
		exportGame
		kind string
	}
	rows, err := store.CollectRows(ctx, s.h.Engine().DB, `
select id, title, game_type from games where fest_id = ? and multifest_venue_id is null order by position, id`,
		[]any{festID}, func(rows *sql.Rows) (row, error) {
			var r row
			return r, rows.Scan(&r.ID, &r.Title, &r.kind)
		})
	if err != nil {
		return nil, err
	}
	var out []exportGame
	for _, r := range rows {
		if games.Get(r.kind).Sheets == games.SheetsODRating {
			out = append(out, r.exportGame)
		}
	}
	return out, nil
}

// exportSite is the rating site as the user, or an error a person reads
// when they saved no token.
func (s *Server) exportSite(ctx context.Context, userID int64) (ratingexport.Site, error) {
	token, ok, err := s.h.Engine().LoadRatingToken(ctx, userID)
	if err != nil {
		return ratingexport.Site{}, err
	}
	if !ok {
		return ratingexport.Site{}, corei18n.User(dopestrings.Default.Host.RatingExport.NoToken())
	}
	return ratingexport.Site{Base: s.h.Engine().RatingAPI, Token: token.Token, Buff: s.h.Engine().Buff}, nil
}

// siteError says in words what went wrong between dope and the site.
func siteError(err error) error {
	s := dopestrings.Default
	var refused *ratingexport.Error
	switch {
	case err == nil || isUserError(err):
		return err
	case errors.As(err, &refused) && refused.Status == http.StatusUnauthorized:
		return corei18n.User(s.Host.RatingExport.TokenRefused())
	case errors.As(err, &refused):
		return corei18n.User(s.Host.RatingExport.SiteRefused(refused.Message))
	default:
		return corei18n.User(s.Host.RatingExport.SiteUnreachable())
	}
}

func isUserError(err error) bool {
	_, ok := corei18n.AsUser(err)
	return ok
}

// exportGameOf is the Game the request names, else the fest's first OD.
func exportGameOf(r *http.Request, list []exportGame) (exportGame, bool) {
	asked, _ := idstr.Parse(r.FormValue("game"))
	for _, g := range list {
		if g.ID == asked {
			return g, true
		}
	}
	if len(list) == 0 {
		return exportGame{}, false
	}
	return list[0], true
}

type ratingExportData struct {
	Fest   view.HostFest
	Games  []exportGame
	Game   exportGame
	Plan   *ratingexport.Plan
	Report *ratingexport.Report
	// Identified is how many teams and players the page just linked to the
	// site, nil when it linked none.
	Identified *int
	Error      string
}

func (s *Server) renderRatingExport(w http.ResponseWriter, r *http.Request, sc route.Scope) {
	s.festPage(w, r, sc.FestID, func(fest view.HostFest) (*dopeui.Doc, error) {
		data, err := s.ratingExport(r, sc)
		data.Fest = fest
		return ratingExportDoc(data), err
	})
}

// renderRatingIdentify links the teams and players the form chose to the
// site and shows the plan again.
func (s *Server) renderRatingIdentify(w http.ResponseWriter, r *http.Request, sc route.Scope) {
	s.festPage(w, r, sc.FestID, func(fest view.HostFest) (*dopeui.Doc, error) {
		data, err := s.ratingIdentify(r, sc)
		data.Fest = fest
		return ratingExportDoc(data), err
	})
}

func (s *Server) ratingIdentify(r *http.Request, sc route.Scope) (ratingExportData, error) {
	if err := r.ParseForm(); err != nil {
		return ratingExportData{}, err
	}
	choices := map[string]string{}
	for key, values := range r.PostForm {
		if ref, ok := strings.CutPrefix(key, identifyField); ok && len(values) > 0 {
			choices[ref] = values[0]
		}
	}
	list, err := s.exportGames(r.Context(), sc.FestID)
	if err != nil {
		return ratingExportData{}, err
	}
	game, ok := exportGameOf(r, list)
	if !ok {
		return ratingExportData{Games: list, Error: dopestrings.Default.Host.RatingExport.NoGame()}, nil
	}
	done, err := s.exportIdentify(r.Context(), sc, game.ID, choices)
	if err != nil && !isUserError(err) {
		return ratingExportData{}, err
	}
	r.Method = http.MethodGet
	data, buildErr := s.ratingExport(r, sc)
	data.Identified = &done
	if err != nil {
		data.Error = err.Error()
	}
	return data, buildErr
}

// identifyField starts the name of a choice's form field; the Unknown's Ref
// ends it.
const identifyField = "u."

// exportIdentify gives each Unknown of the plan that choices name its site
// id: a namesake's, or that of the team or player it creates on the site.
// It writes each id into the fest roster as the team editor would, and
// answers how many it wrote before any refusal.
func (s *Server) exportIdentify(ctx context.Context, sc route.Scope, gameID int64, choices map[string]string) (int, error) {
	site, err := s.exportSite(ctx, sc.User.UserID)
	if err != nil {
		return 0, err
	}
	plan, err := ratingexport.Build(ctx, s.h.Engine().DB, sc.FestID, gameID, site)
	if err != nil {
		return 0, siteError(err)
	}
	// The export is a Manager's, who edits every team of the fest.
	sc.AllVenues = true
	done := 0
	for _, u := range plan.Unknown {
		choice := strings.TrimSpace(choices[u.Ref])
		if choice == "" {
			continue
		}
		id, err := ratingexport.Identify(ctx, site, plan.Tournament, u, choice)
		if err != nil || id <= 0 {
			if err == nil || !errors.As(err, new(*ratingexport.Error)) {
				err = corei18n.User(dopestrings.Default.Host.RatingExport.BadChoice(u.Name))
			}
			return done, siteError(err)
		}
		if err := s.writeIdentified(ctx, sc, u, id); err != nil {
			return done, err
		}
		done++
	}
	return done, nil
}

// writeIdentified writes the site's id of a team or a player into the fest
// roster.
func (s *Server) writeIdentified(ctx context.Context, sc route.Scope, u ratingexport.Unknown, id int64) error {
	if u.Player == 0 {
		return s.editFestRoster(ctx, sc.FestID, "fest:team-rating", func(ctx context.Context, tx *sql.Tx) (imports.RosterWrite, error) {
			if _, err := teamVenueTx(ctx, tx, sc, u.Team); err != nil {
				return imports.RosterWrite{}, err
			}
			return imports.SetHandTeamRatingTx(ctx, tx, sc.FestID, u.Team, id)
		})
	}
	team, err := s.loadFestTeamDetail(ctx, sc, u.Team)
	if err != nil {
		return err
	}
	in := imports.TeamInput{Name: team.Name, City: team.City}
	for _, p := range team.Players {
		if p.RatingID == 0 && p.FirstName == u.FirstName && p.LastName == u.LastName {
			p.RatingID = id
		}
		in.Players = append(in.Players, roster.FestRosterImportPlayer{RatingID: p.RatingID, FirstName: p.FirstName, LastName: p.LastName})
	}
	return s.SaveFestTeam(ctx, sc, u.Team, in)
}

// ratingExport builds the page's plan, and sends it on a POST. An error a
// person reads goes on the page; any other fails the page.
func (s *Server) ratingExport(r *http.Request, sc route.Scope) (ratingExportData, error) {
	var data ratingExportData
	list, err := s.exportGames(r.Context(), sc.FestID)
	if err != nil {
		return data, err
	}
	data.Games = list
	game, ok := exportGameOf(r, list)
	if !ok {
		data.Error = dopestrings.Default.Host.RatingExport.NoGame()
		return data, nil
	}
	data.Game = game
	plan, report, err := s.exportRun(r.Context(), sc, game.ID, r.Method == http.MethodPost)
	data.Plan, data.Report = plan, report
	if isUserError(err) {
		data.Error = err.Error()
		return data, nil
	}
	return data, err
}

// exportRun builds the plan of the Game's export, and sends it when send is
// set. The plan comes back even when the send fails.
func (s *Server) exportRun(ctx context.Context, sc route.Scope, gameID int64, send bool) (*ratingexport.Plan, *ratingexport.Report, error) {
	site, err := s.exportSite(ctx, sc.User.UserID)
	if err != nil {
		return nil, nil, err
	}
	plan, err := ratingexport.Build(ctx, s.h.Engine().DB, sc.FestID, gameID, site)
	if err != nil {
		return nil, nil, siteError(err)
	}
	if !send {
		return &plan, nil, nil
	}
	report, err := ratingexport.Send(ctx, plan, site)
	return &plan, &report, siteError(err)
}

// apiRatingExport is the page's twin: GET answers the plan, POST sends it
// and answers the plan with the site's report.
func (s *Server) apiRatingExport(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	list, err := s.exportGames(r.Context(), sc.FestID)
	if err != nil {
		return err
	}
	game, ok := exportGameOf(r, list)
	if !ok {
		return route.BadUser(corei18n.User(dopestrings.Default.Host.RatingExport.NoGame()))
	}
	plan, report, err := s.exportRun(r.Context(), sc, game.ID, r.Method == http.MethodPost)
	if err != nil {
		return route.BadUser(err)
	}
	return route.JSON(w, map[string]any{"game": game, "plan": plan, "report": report})
}

// apiRatingIdentify is the identify form's twin: {game?, choices: {ref:
// "<site id>" | "new"}} answers how many it wrote and the plan after.
func (s *Server) apiRatingIdentify(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	var req struct {
		Game    int64             `json:"game"`
		Choices map[string]string `json:"choices"`
	}
	if err := route.DecodeJSON(r, &req); err != nil {
		return err
	}
	list, err := s.exportGames(r.Context(), sc.FestID)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	if req.Game != 0 {
		q.Set("game", idstr.Format(req.Game))
		r.URL.RawQuery = q.Encode()
	}
	game, ok := exportGameOf(r, list)
	if !ok {
		return route.BadUser(corei18n.User(dopestrings.Default.Host.RatingExport.NoGame()))
	}
	done, err := s.exportIdentify(r.Context(), sc, game.ID, req.Choices)
	if err != nil {
		return route.BadUser(err)
	}
	plan, _, err := s.exportRun(r.Context(), sc, game.ID, false)
	if err != nil {
		return route.BadUser(err)
	}
	return route.JSON(w, map[string]any{"game": game, "identified": done, "plan": plan})
}

func ratingExportDoc(data ratingExportData) *dopeui.Doc {
	s := dopestrings.Default
	ref := data.Fest.Ref()
	action := "/host/fest/" + ref + "/rating/export"
	page := []dopeui.Item{
		dopeui.Title(s.Host.RatingExport.Title(data.Fest.Title)), dopeui.PagePublic, dopeui.Classicscripts("dist/pageforms.js"),
		dopeui.Publictopbar(pages.Trail(pages.FestCrumbs(ref, data.Fest.Title), s.Host.RatingExport.Crumb())),
	}
	if data.Report == nil {
		page = append(page, dopeui.Section(dopeui.Hint(dopeui.Text(s.Host.RatingExport.Lead()))))
	}
	if len(data.Games) > 1 {
		options := []dopeui.Item{dopeui.Name("game"), dopeui.Data("autosubmit", "")}
		for _, g := range data.Games {
			option := []dopeui.Item{dopeui.Value(idstr.Format(g.ID)), dopeui.Text(g.Title)}
			if g.ID == data.Game.ID {
				option = append(option, dopeui.Selected())
			}
			options = append(options, dopeui.Option(option...))
		}
		page = append(page, dopeui.Section(dopeui.Form(dopeui.Method("get"), dopeui.Action(action),
			dopeui.Field(dopeui.Label(s.Host.RatingExport.GameLabel()), dopeui.Selectfield(options...)))))
	}
	if data.Error != "" {
		page = append(page, dopeui.Section(dopeui.Empty(dopeui.Text(data.Error)), exportTokenLink(data.Error)))
	}
	if data.Identified != nil && *data.Identified > 0 {
		page = append(page, dopeui.Section(dopeui.Note(dopeui.Text(s.Host.RatingExport.Identified(strconv.Itoa(*data.Identified))))))
	}
	if data.Report != nil && data.Error == "" {
		page = append(page, exportReportSection(*data.Report, data.Plan.Tournament, action+"?game="+idstr.Format(data.Game.ID)))
	}
	if data.Plan != nil && data.Report == nil {
		page = append(page, exportPlanSections(*data.Plan, action, data.Game.ID)...)
	}
	return &dopeui.Doc{Nodes: []dopeui.Node{dopeui.Page(page...)}}
}

// exportTokenLink leads to the profile when the error is about the token.
func exportTokenLink(message string) dopeui.Item {
	s := dopestrings.Default
	if message != s.Host.RatingExport.NoToken() && message != s.Host.RatingExport.TokenRefused() {
		return dopeui.Row()
	}
	return dopeui.Row(dopeui.Button(dopeui.Href("/profile#rating-token"), dopeui.Text(s.Host.RatingExport.ToProfile())))
}

func exportReportSection(report ratingexport.Report, tournament int64, back string) *dopeui.Element {
	s := dopestrings.Default
	items := []dopeui.Item{dopeui.Subhead(dopeui.Text(s.Host.RatingExport.SentSubhead())),
		dopeui.Note(dopeui.Text(s.Host.RatingExport.Sent(strconv.Itoa(report.Rosters), strconv.Itoa(report.Results))))}
	if report.Moved > 0 {
		items = append(items, dopeui.Muted(dopeui.Text(s.Host.RatingExport.SentMoved(strconv.Itoa(report.Moved)))))
	}
	for _, w := range report.Warnings {
		items = append(items, dopeui.Muted(dopeui.Text(w)))
	}
	items = append(items, dopeui.Row(
		dopeui.Button(dopeui.Href(fmt.Sprintf(ratingexport.TournamentPage, tournament)), dopeui.Text(s.Host.RatingExport.OpenTournament())),
		dopeui.Button(dopeui.Href(back), dopeui.Text(s.Host.RatingExport.BackToPreview()))))
	return dopeui.Section(items...)
}

// exportUnknownSection asks who each team and player without a site id is
// on the site.
func exportUnknownSection(plan ratingexport.Plan, action string, gameID int64) *dopeui.Element {
	s := dopestrings.Default
	form := []dopeui.Item{dopeui.DirCol, dopeui.Method("post"), dopeui.Action(action + "/identify"),
		dopeui.Data("confirm", s.Host.RatingExport.IdentifyConfirm()),
		dopeui.Hiddenfield(dopeui.Name("game"), dopeui.Value(idstr.Format(gameID)))}
	for _, u := range plan.Unknown {
		label := s.Host.RatingExport.UnknownPlayer(u.Name, u.TeamName)
		if u.Player == 0 {
			label = s.Host.RatingExport.UnknownTeam(u.Name)
		}
		pick := u.Default()
		option := func(value, text string) dopeui.Item {
			items := []dopeui.Item{dopeui.Value(value), dopeui.Text(text)}
			if value == pick {
				items = append(items, dopeui.Selected())
			}
			return dopeui.Option(items...)
		}
		options := []dopeui.Item{dopeui.Name(identifyField + u.Ref), option("", s.Host.RatingExport.ChoiceSkip())}
		for _, c := range u.Candidates {
			options = append(options, option(idstr.Format(c.ID), s.Host.RatingExport.ChoiceCandidate(c.Label, idstr.Format(c.ID))))
		}
		field := []dopeui.Item{dopeui.Label(label)}
		switch {
		case u.Player != 0:
			options = append(options, option(ratingexport.ChoiceNew, s.Host.RatingExport.ChoiceNewPlayer()))
		case u.Town != 0:
			options = append(options, option(ratingexport.ChoiceNew, s.Host.RatingExport.ChoiceNewTeam(u.TownName)))
		default:
			field = append(field, dopeui.Muted(dopeui.Text(s.Host.RatingExport.NoTown(u.Name))))
		}
		form = append(form, dopeui.Field(append(field, dopeui.Selectfield(options...))...))
	}
	form = append(form, dopeui.Row(dopeui.Button(dopeui.Submit(), dopeui.Text(s.Host.RatingExport.Identify()))))
	return dopeui.Section(dopeui.Subhead(dopeui.Text(s.Host.RatingExport.UnknownSubhead())),
		dopeui.Hint(dopeui.Text(s.Host.RatingExport.UnknownHint())), dopeui.Form(form...))
}

func exportPlanSections(plan ratingexport.Plan, action string, gameID int64) []dopeui.Item {
	s := dopestrings.Default
	var out []dopeui.Item
	notes := []dopeui.Item{}
	if plan.Blocked != "" {
		notes = append(notes, dopeui.Hint(dopeui.HintDanger, dopeui.Text(plan.Blocked)))
	}
	for _, w := range plan.Warnings {
		notes = append(notes, dopeui.Muted(dopeui.Text(w)))
	}
	if len(notes) > 0 {
		out = append(out, dopeui.Section(notes...))
	}
	venues := []dopeui.Item{dopeui.Subhead(dopeui.Text(s.Host.RatingExport.RostersSubhead(idstr.Format(plan.Tournament))))}
	for _, v := range plan.Venues {
		head := []dopeui.Item{dopeui.SpaceXS, dopeui.Strong(dopeui.Text(v.Title))}
		if v.Representative != 0 {
			head = append(head, dopeui.Muted(dopeui.Text(s.Host.RatingExport.Representative(v.RepresentativeName, idstr.Format(v.Representative), idstr.Format(v.Request)))))
		}
		rows := make([]dopeui.Item, 0, len(v.Teams))
		for _, t := range v.Teams {
			names := make([]string, len(t.Players))
			for i, p := range t.Players {
				names[i] = s.Host.RatingExport.PlayerFlag(p.Name, p.Flag)
			}
			rows = append(rows, dopeui.Listrow(dopeui.Col(dopeui.Listtitle(dopeui.Text(t.Name)), dopeui.Muted(dopeui.Text(strings.Join(names, ", "))))))
		}
		venues = append(venues, dopeui.Col(append(head, dopeui.List(rows...))...))
	}
	out = append(out, dopeui.Section(venues...))
	if len(plan.Moves) > 0 {
		moves := []dopeui.Item{dopeui.Subhead(dopeui.Text(s.Host.RatingExport.MovesSubhead()))}
		for _, m := range plan.Moves {
			moves = append(moves, dopeui.Muted(dopeui.Text(s.Host.RatingExport.MoveLine(m.Name, m.Venue))))
		}
		out = append(out, dopeui.Section(moves...))
	}
	if len(plan.Unknown) > 0 {
		out = append(out, exportUnknownSection(plan, action, gameID))
	}
	if len(plan.Skipped) > 0 {
		skipped := []dopeui.Item{dopeui.Subhead(dopeui.Text(s.Host.RatingExport.SkippedSubhead()))}
		for _, k := range plan.Skipped {
			skipped = append(skipped, dopeui.Muted(dopeui.Text(s.Host.RatingExport.SkippedLine(k.Team, k.Reason))))
		}
		out = append(out, dopeui.Section(skipped...))
	}
	if plan.Blocked == "" {
		out = append(out, dopeui.Section(dopeui.Form(dopeui.DirCol, dopeui.Method("post"), dopeui.Action(action),
			dopeui.Data("confirm", s.Host.RatingExport.SendConfirm(strconv.Itoa(len(plan.Results)))),
			dopeui.Hiddenfield(dopeui.Name("game"), dopeui.Value(idstr.Format(gameID))),
			dopeui.Note(dopeui.Text(s.Host.RatingExport.ResultsNote(strconv.Itoa(len(plan.Results)), strconv.Itoa(plan.Questions)))),
			dopeui.Row(dopeui.Button(dopeui.Submit(), dopeui.Text(s.Host.RatingExport.Send()))))))
	}
	return out
}
