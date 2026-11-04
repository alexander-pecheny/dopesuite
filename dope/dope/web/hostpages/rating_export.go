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
	"dope/dope/domain/ratingsync"
	"dope/dope/domain/roster"
	"dope/dope/domain/view"
	"dope/dope/storage/ratingsite"
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
	return ratingexport.Site{Client: s.h.Engine().RatingSite(token.Token), Buff: s.h.Engine().Buff}, nil
}

// siteError says in words what went wrong between dope and the site.
func siteError(err error) error {
	s := dopestrings.Default
	var refused *ratingsite.Error
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
	// Choices is the side picked for each fact both sides changed.
	Choices map[string]string
	Error   string
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
		if ref, ok := strings.CutPrefix(key, identifyField); ok && len(values) > 0 && choices[ref] == "" {
			choices[ref] = values[0]
		}
		if ref, ok := strings.CutPrefix(key, typedIDField); ok && len(values) > 0 && strings.TrimSpace(values[0]) != "" {
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

// typedIDField starts the name of the field where a person types a site id
// the choices lack; it wins over the choice.
const typedIDField = "id."

// exportIdentify gives each Unknown of the plan that choices name its site
// id: a namesake's, or that of the team or player it creates on the site.
// It writes each id into the fest roster as the team editor would, and
// answers how many it wrote before any refusal.
func (s *Server) exportIdentify(ctx context.Context, sc route.Scope, gameID int64, choices map[string]string) (int, error) {
	site, err := s.exportSite(ctx, sc.User.UserID)
	if err != nil {
		return 0, err
	}
	plan, err := ratingexport.Build(ctx, s.h.Engine().DB, sc.FestID, gameID, site, nil)
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
			if err == nil || !errors.As(err, new(*ratingsite.Error)) {
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
	choices := formChoices(r)
	plan, report, err := s.exportRun(r.Context(), sc, game.ID, r.Method == http.MethodPost, choices)
	data.Plan, data.Report, data.Choices = plan, report, choices
	if r.Method == http.MethodGet && r.FormValue("all") == choiceSite && plan != nil {
		for _, f := range plan.Facts {
			if f.Action == ratingsync.Ask {
				data.Choices[f.Key] = choiceSite
			}
		}
	}
	if isUserError(err) {
		data.Error = err.Error()
		return data, nil
	}
	return data, err
}

// formChoices is the side a person picked for each fact both sides
// changed, by its Fact.Key.
func formChoices(r *http.Request) map[string]string {
	choices := map[string]string{}
	_ = r.ParseForm()
	for key, values := range r.Form {
		if ref, ok := strings.CutPrefix(key, choiceField); ok && len(values) > 0 && values[0] != "" {
			choices[ref] = values[0]
		}
	}
	return choices
}

// choiceField starts the name of a fact's choice field; its Fact.Key ends it.
const choiceField = "c."

// choiceSite is the choice of the site's version.
const choiceSite = "site"

// exportRun builds the plan of the Game's export under the person's
// choices, and carries it out when send is set: dope takes the site's
// versions it should, the rest goes to the site, and the base records what
// both now hold. The plan comes back even when the send fails.
func (s *Server) exportRun(ctx context.Context, sc route.Scope, gameID int64, send bool, choices map[string]string) (*ratingexport.Plan, *ratingexport.Report, error) {
	site, err := s.exportSite(ctx, sc.User.UserID)
	if err != nil {
		return nil, nil, err
	}
	plan, err := ratingexport.Build(ctx, s.h.Engine().DB, sc.FestID, gameID, site, choices)
	if err != nil {
		return nil, nil, siteError(err)
	}
	if !send {
		return &plan, nil, nil
	}
	if plan.Undecided > 0 {
		return &plan, nil, corei18n.User(dopestrings.Default.Host.RatingExport.Undecided(strconv.Itoa(plan.Undecided)))
	}
	taken, err := s.takeFromSite(ctx, sc, plan)
	if err != nil {
		return &plan, nil, err
	}
	report, sendErr := ratingexport.Send(ctx, plan, site)
	for _, b := range taken {
		if b.Fact != ratingsync.Answers {
			report.Taken++
		}
	}
	bases := append(append(taken, plan.Agreed...), report.Sent...)
	err = s.h.Engine().WithWriteTx(ctx, sc.FestID, "rating-sync-base", func(ctx context.Context, tx *sql.Tx) error {
		return ratingsync.SaveTx(ctx, tx, sc.FestID, bases)
	})
	if sendErr != nil {
		return &plan, &report, siteError(sendErr)
	}
	return &plan, &report, err
}

// takeFromSite writes into the fest roster each roster and Flags whose
// site version stands, as the team editor would, and answers the base
// every taken fact now has.
func (s *Server) takeFromSite(ctx context.Context, sc route.Scope, plan ratingexport.Plan) ([]ratingsync.Base, error) {
	// The export is a Manager's, who edits every team of the fest.
	sc.AllVenues = true
	var bases []ratingsync.Base
	var order []int64
	byTeam := map[int64][]ratingexport.Fact{}
	for _, f := range plan.Facts {
		if f.Action != ratingsync.Take {
			continue
		}
		bases = append(bases, f.Base)
		if f.Fact == ratingsync.Answers {
			continue
		}
		if byTeam[f.TeamID] == nil {
			order = append(order, f.TeamID)
		}
		byTeam[f.TeamID] = append(byTeam[f.TeamID], f)
	}
	for _, teamID := range order {
		team, err := s.loadFestTeamDetail(ctx, sc, teamID)
		if err != nil {
			return nil, err
		}
		in := imports.TeamInput{Name: team.Name, City: team.City}
		for _, p := range team.Players {
			in.Players = append(in.Players, roster.FestRosterImportPlayer{RatingID: p.RatingID, FirstName: p.FirstName, LastName: p.LastName})
		}
		for _, f := range byTeam[teamID] {
			if f.Fact == ratingsync.Roster {
				in.Players = f.Players
			} else {
				flags := append([]string{}, f.Flags...)
				in.Flags = &flags
			}
		}
		if err := s.SaveFestTeam(ctx, sc, teamID, in); err != nil {
			return nil, err
		}
	}
	return bases, nil
}

// apiRatingExport is the page's twin: GET answers the plan, POST {choices:
// {key: "dope" | "site"}} carries it out and answers it with the site's
// report.
func (s *Server) apiRatingExport(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	list, err := s.exportGames(r.Context(), sc.FestID)
	if err != nil {
		return err
	}
	game, ok := exportGameOf(r, list)
	if !ok {
		return route.BadUser(corei18n.User(dopestrings.Default.Host.RatingExport.NoGame()))
	}
	var req struct {
		Choices map[string]string `json:"choices"`
	}
	if r.Method == http.MethodPost && r.ContentLength != 0 {
		if err := route.DecodeJSON(r, &req); err != nil {
			return err
		}
	}
	plan, report, err := s.exportRun(r.Context(), sc, game.ID, r.Method == http.MethodPost, req.Choices)
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
	plan, _, err := s.exportRun(r.Context(), sc, game.ID, false, nil)
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
		page = append(page, exportPlanSections(*data.Plan, action, data.Game.ID, data.Choices)...)
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
		dopeui.Note(dopeui.Text(s.Host.RatingExport.Sent(strconv.Itoa(report.Rosters), strconv.Itoa(report.Flags), strconv.Itoa(report.Results))))}
	if report.Taken > 0 {
		items = append(items, dopeui.Muted(dopeui.Text(s.Host.RatingExport.SentTaken(strconv.Itoa(report.Taken)))))
	}
	if report.Moved > 0 {
		items = append(items, dopeui.Muted(dopeui.Text(s.Host.RatingExport.SentMoved(strconv.Itoa(report.Moved)))))
	}
	for _, w := range report.Warnings {
		items = append(items, dopeui.Muted(dopeui.Text(w)))
	}
	items = append(items, dopeui.Row(
		dopeui.Button(dopeui.Href(fmt.Sprintf(ratingsite.TournamentPage, tournament)), dopeui.Text(s.Host.RatingExport.OpenTournament())),
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
		typed := dopeui.Textfield(dopeui.Name(typedIDField+u.Ref), dopeui.Inputmode("numeric"), dopeui.Placeholder(s.Host.RatingExport.TypedId()))
		form = append(form, dopeui.Field(append(field, dopeui.Selectfield(options...), typed)...))
	}
	form = append(form, dopeui.Row(dopeui.Button(dopeui.Submit(), dopeui.Text(s.Host.RatingExport.Identify()))))
	return dopeui.Section(dopeui.Subhead(dopeui.Text(s.Host.RatingExport.UnknownSubhead())),
		dopeui.Hint(dopeui.Text(s.Host.RatingExport.UnknownHint())), dopeui.Form(form...))
}

// factName is what a person calls the fact.
func factName(f ratingsync.Fact) string {
	s := dopestrings.Default
	switch f {
	case ratingsync.Roster:
		return s.Host.RatingExport.FactRoster()
	case ratingsync.Flags:
		return s.Host.RatingExport.FactFlags()
	default:
		return s.Host.RatingExport.FactAnswers()
	}
}

// factValue is a side's version as the page shows it.
func factValue(v string) string {
	if v == "" {
		return dopestrings.Default.Host.RatingExport.EmptyValue()
	}
	return v
}

// exportFactItems shows how the export settles each fact where dope and the
// site differ: the site's versions that stand because only the site changed,
// and a choice for each fact both sides changed.
func exportFactItems(plan ratingexport.Plan, all string, choices map[string]string) []dopeui.Item {
	s := dopestrings.Default
	var taken, asked []dopeui.Item
	for _, f := range plan.Facts {
		switch {
		case f.Action == ratingsync.Ask || f.Chosen:
			option := func(value, text string) dopeui.Item {
				items := []dopeui.Item{dopeui.Value(value), dopeui.Text(text)}
				if choices[f.Key] == value {
					items = append(items, dopeui.Selected())
				}
				return dopeui.Option(items...)
			}
			asked = append(asked, dopeui.Field(dopeui.Label(s.Host.RatingExport.AskLabel(f.Name, factName(f.Fact))), dopeui.Selectfield(
				dopeui.Name(choiceField+f.Key), option("", s.Host.RatingExport.ChoiceNone()),
				option("dope", s.Host.RatingExport.ChoiceDope(factValue(f.Dope))), option(choiceSite, s.Host.RatingExport.ChoiceSite(factValue(f.Site))))))
		case f.Action == ratingsync.Take:
			line := s.Host.RatingExport.TakeLine(f.Name, factName(f.Fact), factValue(f.Site))
			if f.Fact != ratingsync.Answers {
				line += ". " + s.Host.RatingExport.TakeIntoDope()
			}
			taken = append(taken, dopeui.Muted(dopeui.Text(line)))
		}
	}
	if len(taken)+len(asked) == 0 {
		return nil
	}
	items := append([]dopeui.Item{dopeui.Subhead(dopeui.Text(s.Host.RatingExport.FactsSubhead())),
		dopeui.Hint(dopeui.Text(s.Host.RatingExport.FactsHint()))}, taken...)
	if len(asked) > 0 {
		items = append(append(items, asked...), dopeui.Row(dopeui.Button(dopeui.Href(all), dopeui.Text(s.Host.RatingExport.AllSite()))))
	}
	return items
}

// exportHasWork reports whether carrying out the plan changes anything.
func exportHasWork(plan ratingexport.Plan) bool {
	if len(plan.Moves)+len(plan.Flags)+len(plan.Results)+len(plan.Facts) > 0 {
		return true
	}
	for _, v := range plan.Venues {
		if len(v.Teams) > 0 {
			return true
		}
	}
	return false
}

func exportPlanSections(plan ratingexport.Plan, action string, gameID int64, choices map[string]string) []dopeui.Item {
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
		if len(v.Teams) == 0 {
			continue
		}
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
	if len(venues) > 1 {
		out = append(out, dopeui.Section(venues...))
	}
	if len(plan.Flags) > 0 {
		flags := []dopeui.Item{dopeui.Subhead(dopeui.Text(s.Host.RatingExport.FlagsSubhead()))}
		for _, f := range plan.Flags {
			flags = append(flags, dopeui.Muted(dopeui.Text(s.Host.RatingExport.FlagsLine(f.Name, factValue(strings.Join(f.Shorts, ", "))))))
		}
		out = append(out, dopeui.Section(flags...))
	}
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
	facts := exportFactItems(plan, action+"?game="+idstr.Format(gameID)+"&all="+choiceSite, choices)
	switch {
	case plan.Blocked != "" && facts != nil:
		out = append(out, dopeui.Section(facts...))
	case plan.Blocked != "":
	case !exportHasWork(plan):
		out = append(out, dopeui.Section(dopeui.Note(dopeui.Text(s.Host.RatingExport.NothingToSend()))))
	default:
		form := append([]dopeui.Item{dopeui.DirCol, dopeui.Method("post"), dopeui.Action(action),
			dopeui.Data("confirm", s.Host.RatingExport.SendConfirm(strconv.Itoa(len(plan.Results)))),
			dopeui.Hiddenfield(dopeui.Name("game"), dopeui.Value(idstr.Format(gameID)))}, facts...)
		if len(plan.Results) > 0 {
			form = append(form, dopeui.Note(dopeui.Text(s.Host.RatingExport.ResultsNote(strconv.Itoa(len(plan.Results)), strconv.Itoa(plan.Questions)))))
		}
		form = append(form, dopeui.Row(dopeui.Button(dopeui.Submit(), dopeui.Text(s.Host.RatingExport.Send()))))
		out = append(out, dopeui.Section(dopeui.Form(form...)))
	}
	return out
}
