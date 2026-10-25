package hostpages

import (
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"dope/dope/domain/games"
	"dope/dope/domain/multifest"
	"dope/dope/domain/numbering"
	"dope/dope/domain/view"
	"dope/dope/web/pages"
	"dope/dope/web/route"
	dopeui "dope/dope/web/ui"
	dopestrings "dope/i18nstrings"

	"code.pecheny.me/pecheny/dopesuite/dopecore/idstr"
)

// A venue's page is where its host works: its teams with their
// numbers, and the Games the venue plays. The fest's pages for teams and
// numbers send a venue's host here, and so does the venue's invite link.

// venuePageURL is the page of one venue of the fest.
func venuePageURL(festRef string, venueID int64) string {
	return "/host/fest/" + festRef + "/venue/" + idstr.Format(venueID)
}

type hostVenuePageData struct {
	Fest          view.HostFest
	Venue         multifest.Venue
	Teams         []hostFestTeam
	Games         []PublicFestGame
	Error, Notice string
	// Numbers holds what the host typed when a save was refused, by team id.
	Numbers map[int64]string
	// Instant says the rating search reads buff, which answers every letter.
	Instant bool
}

// venueOfRequest is the venue the path names, refused unless the caller may
// edit its teams.
func (s *Server) venueOfRequest(r *http.Request, sc route.Scope) (multifest.Venue, error) {
	id, _ := idstr.Parse(r.PathValue("venue"))
	if !sc.MayEditVenue(id) {
		return multifest.Venue{}, route.ErrOtherVenue
	}
	return multifest.VenueOf(r.Context(), s.h.Engine().DB, sc.FestID, id)
}

func (s *Server) renderHostVenuePage(w http.ResponseWriter, r *http.Request, sc route.Scope, errMsg, notice string, typed map[int64]string) error {
	venue, err := s.venueOfRequest(r, sc)
	if err != nil {
		return err
	}
	s.festPage(w, r, sc.FestID, func(fest view.HostFest) (*dopeui.Doc, error) {
		data := hostVenuePageData{Fest: fest, Venue: venue, Error: errMsg, Notice: notice, Numbers: typed, Instant: s.h.Engine().Buff.Enabled()}
		teams, err := s.loadHostFestTeams(r.Context(), sc)
		if err != nil {
			return nil, err
		}
		for _, t := range teams {
			if t.Venue == venue.ID {
				data.Teams = append(data.Teams, t)
			}
		}
		sortByVenueNumber(data.Teams)
		rows, err := LoadFestGames(r.Context(), s.h.Engine().DB, sc.FestID)
		if err != nil {
			return nil, err
		}
		for _, g := range rows {
			if g.MultiFestVenueID == venue.ID {
				data.Games = append(data.Games, PublicFestGame{ID: g.ID, Slug: g.Slug, Title: g.Title, Type: g.Type, MultiFestVenueID: venue.ID})
			}
		}
		return hostVenueDoc(data), nil
	})
	return nil
}

// sortByVenueNumber puts the numbered teams first, in their numbers' order,
// and the rest after them as they were added.
func sortByVenueNumber(teams []hostFestTeam) {
	sort.SliceStable(teams, func(i, j int) bool {
		a, b := teams[i].VenueNumber, teams[j].VenueNumber
		if (a == 0) != (b == 0) {
			return b == 0
		}
		return a < b
	})
}

func hostVenueDoc(data hostVenuePageData) *dopeui.Doc {
	s := dopestrings.Default
	ref := data.Fest.Ref()
	page := []dopeui.Item{
		dopeui.Title(s.Host.Venue.Title(data.Venue.Label(), data.Fest.Title)), dopeui.PagePublic,
		dopeui.Classicscripts("dist/pageforms.js dist/fest-teams.js"),
		dopeui.Publictopbar(pages.Trail(pages.FestCrumbs(ref, data.Fest.Title), data.Venue.Label())),
	}
	if data.Notice != "" {
		page = append(page, dopeui.Note(dopeui.Text(data.Notice)))
	}
	page = append(page, venueGamesSection(data, ref), venueTeamsSection(data, ref))
	return &dopeui.Doc{Nodes: []dopeui.Node{dopeui.Page(page...)}}
}

func venueGamesSection(data hostVenuePageData, ref string) *dopeui.Element {
	s := dopestrings.Default
	sect := []dopeui.Item{dopeui.Subhead(dopeui.Text(s.Host.Venue.GamesSubhead()))}
	if len(data.Games) == 0 {
		return dopeui.Section(append(sect, dopeui.Empty(dopeui.Text(s.Host.Venue.GamesEmpty())))...)
	}
	rows := make([]dopeui.Item, 0, len(data.Games))
	for _, g := range data.Games {
		row := []dopeui.Item{dopeui.Href("/host/fest/" + ref + "/game/" + g.Ref() + "/"), dopeui.Listtitle(dopeui.Text(games.Get(g.Type).Title))}
		if g.Title != data.Venue.Title {
			row = append(row, dopeui.Muted(dopeui.Text(g.Title)))
		}
		rows = append(rows, dopeui.Listrow(row...))
	}
	return dopeui.Section(append(sect, dopeui.List(rows...))...)
}

func venueTeamsSection(data hostVenuePageData, ref string) *dopeui.Element {
	s := dopestrings.Default
	sect := []dopeui.Item{
		dopeui.Subhead(dopeui.Text(s.Host.Venue.TeamsSubhead(strconv.Itoa(len(data.Teams))))),
		// The editor (fest-teams.ts) reads the fest's API off the add button and
		// puts a new team on the venue the button names.
		dopeui.Row(dopeui.Button(dopeui.Data("team-add", "/api/fest/"+ref), dopeui.Data("team-venue", idstr.Format(data.Venue.ID)), searchMode(data.Instant),
			dopeui.Text(s.Host.Roster.AddTeamBtn()))),
	}
	if len(data.Teams) == 0 {
		return dopeui.Section(append(sect, dopeui.Empty(dopeui.Text(s.Host.Roster.TeamsEmptyVenue())))...)
	}
	rows := []dopeui.Item{dopeui.Trow(
		dopeui.Hcell(dopeui.Text(s.Host.Venue.ColNumber())), dopeui.Hcell(),
		dopeui.Hcell(dopeui.Text(s.Host.Roster.TeamLabel())),
		dopeui.Hcell(dopeui.Text(s.Host.Roster.ColCity())), dopeui.Hcell(dopeui.Text(s.Host.Roster.ColPlayers())),
	)}
	for _, t := range data.Teams {
		rows = append(rows, dopeui.Trow(
			numberCell(t, t.VenueNumber, data.Numbers),
			dopeui.Cell(dopeui.Iconbtn(dopeui.IconPencil, dopeui.Label(s.Host.Roster.EditTeamLabel(t.Name)),
				dopeui.Data("team-edit", idstr.Format(t.ID)))),
			dopeui.Cell(dopeui.Text(t.Name)),
			dopeui.Cell(dopeui.Text(t.City)),
			dopeui.Cell(dopeui.Text(strconv.Itoa(t.Players))),
		))
	}
	sect = append(sect, dopeui.Note(dopeui.Text(s.Host.Venue.TeamsHint())))
	// A refusal is about the table, so it stands right above it.
	if data.Error != "" {
		sect = append(sect, dopeui.Empty(dopeui.Text(data.Error)))
	}
	return dopeui.Section(append(sect,
		dopeui.Form(dopeui.DirCol, dopeui.Method("post"), dopeui.Action(venuePageURL(ref, data.Venue.ID)+"/numbers"), dopeui.Autocomplete("off"),
			dopeui.Data("dirty-submit", ""), dopeui.Data("unique-values", s.Host.Venue.NumberDuplicate()), refusedForm(data.Numbers != nil),
			dopeui.Table(append([]dopeui.Item{dopeui.Scroll()}, rows...)...),
			dopeui.Row(dopeui.Button(dopeui.Submit(), dopeui.Text(s.Host.Venue.SaveNumbers()))),
		))...)
}

// venueNumberField names one team's number input; the save reads the team id
// back off the name.
func venueNumberField(teamID int64) string {
	return "number_" + idstr.Format(teamID)
}

// handleHostSaveVenueNumbers saves the numbers typed on a venue's page: a
// blank leaves a team without one, and two teams may not share a number.
func (s *Server) handleHostSaveVenueNumbers(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	venue, err := s.venueOfRequest(r, sc)
	if err != nil {
		return err
	}
	if err := r.ParseForm(); err != nil {
		return route.BadRequest(err.Error())
	}
	teams, err := s.loadHostFestTeams(r.Context(), sc)
	if err != nil {
		return err
	}
	var own []hostFestTeam
	for _, t := range teams {
		if t.Venue == venue.ID {
			own = append(own, t)
		}
	}
	typed, assignments, refusal := typedNumbers(r.PostForm, own)
	if refusal != "" {
		return s.renderHostVenuePage(w, r, sc, refusal, "", typed)
	}
	if err := s.pages().SaveFestNumbers(r.Context(), sc.FestID, venue.ID, assignments); err != nil {
		return s.renderHostVenuePage(w, r, sc, err.Error(), "", typed)
	}
	http.Redirect(w, r, venuePageURL(r.PathValue("fest"), venue.ID)+"?saved=1", http.StatusSeeOther)
	return nil
}

// typedNumbers reads the teams' number fields. typed keeps what was typed,
// for a refused save to show again, and refusal says why the numbers cannot
// be saved: one out of range, or one two teams share. A blank leaves a team
// without a number.
func typedNumbers(form url.Values, teams []hostFestTeam) (typed map[int64]string, assignments map[int64]int, refusal string) {
	typed, assignments = map[int64]string{}, map[int64]int{}
	holder := map[int]string{}
	for _, t := range teams {
		text := strings.TrimSpace(form.Get(venueNumberField(t.ID)))
		typed[t.ID] = text
		if text == "" || refusal != "" {
			continue
		}
		n, err := strconv.Atoi(text)
		switch {
		case err != nil || n < 1 || n > numbering.MaxNumber:
			refusal = dopestrings.Default.Host.Venue.NumberRange(t.Name, strconv.Itoa(numbering.MaxNumber))
		case holder[n] != "":
			refusal = dopestrings.Default.Host.Venue.NumberTaken(strconv.Itoa(n), holder[n], t.Name)
		default:
			holder[n] = t.Name
			assignments[t.ID] = n
		}
	}
	return typed, assignments, refusal
}

// numberCell is a team's number field, holding what was typed if a save was
// refused, else its number.
func numberCell(t hostFestTeam, number int, typed map[int64]string) *dopeui.Element {
	text, ok := typed[t.ID]
	if !ok && number > 0 {
		text = strconv.Itoa(number)
	}
	return dopeui.Cell(dopeui.Numfield(dopeui.Name(venueNumberField(t.ID)), dopeui.Value(text), dopeui.Narrow(), dopeui.Data("unique", ""),
		dopeui.Min("1"), dopeui.Max(strconv.Itoa(numbering.MaxNumber)),
		dopeui.Aria("label", dopestrings.Default.Host.Venue.NumberLabel(t.Name))))
}

// refusedForm marks a form the server sent back with what the host typed,
// which counts as changed though its fields hold the values they came with.
func refusedForm(refused bool) dopeui.Item {
	if refused {
		return dopeui.Data("refused", "yes")
	}
	return dopeui.Data("refused", "no")
}

// searchMode tells the team editor whether to ask the rating search at every
// letter, which only buff's local mirror can answer, or once typing stops,
// which spares the rating site.
func searchMode(instant bool) dopeui.Item {
	if instant {
		return dopeui.Data("team-search", "instant")
	}
	return dopeui.Data("team-search", "debounced")
}

// soleVenue is the one venue a host limited to venues edits, or 0 when they
// edit none, several, or every team of the fest.
func soleVenue(sc route.Scope) int64 {
	if sc.AllVenues || len(sc.Venues) != 1 {
		return 0
	}
	return sc.Venues[0]
}
