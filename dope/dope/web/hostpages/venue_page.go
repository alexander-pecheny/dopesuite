package hostpages

import (
	"encoding/json"
	"errors"
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
	Fest  view.HostFest
	Venue multifest.Venue
	Teams []hostFestTeam
	Games []PublicFestGame
	// Instant says the rating search reads buff, which answers every letter.
	Instant bool
	// Synch says the venue's sheet of the fest's Games can be worked on
	// without a network.
	Synch bool
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

func (s *Server) renderHostVenuePage(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	venue, err := s.venueOfRequest(r, sc)
	if errors.Is(err, route.ErrOtherVenue) {
		s.renderForbidden(w, r, dopestrings.Default.Host.Forbidden.OtherVenue())
		return nil
	}
	if err != nil {
		return err
	}
	s.festPage(w, r, sc.FestID, func(fest view.HostFest) (*dopeui.Doc, error) {
		data := hostVenuePageData{Fest: fest, Venue: venue, Instant: s.h.Engine().Buff.Enabled()}
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
		data.Synch, data.Games, err = s.venueGames(r, sc.FestID, venue.ID)
		if err != nil {
			return nil, err
		}
		return hostVenueDoc(data), nil
	})
	return nil
}

// venueGames is what a venue plays: a venue of a synch fest its sheet of the
// fest's Games, which open on that sheet, a venue of a multi fest its own
// Games.
func (s *Server) venueGames(r *http.Request, festID, venueID int64) (bool, []PublicFestGame, error) {
	rows, err := LoadFestGames(r.Context(), s.h.Engine().DB, festID)
	if err != nil {
		return false, nil, err
	}
	kind, err := multifest.FestKind(r.Context(), s.h.Engine().DB, festID)
	if err != nil {
		return false, nil, err
	}
	synch := kind == multifest.Synch
	var out []PublicFestGame
	for _, g := range rows {
		if synch && g.MultiFestVenueID == 0 || !synch && g.MultiFestVenueID == venueID {
			out = append(out, PublicFestGame{ID: g.ID, Slug: g.Slug, Title: g.Title, Type: g.Type, MultiFestVenueID: g.MultiFestVenueID})
		}
	}
	return synch, out, nil
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
		dopeui.Classicscripts("dist/pageforms.js dist/venue-page.js"),
		dopeui.Publictopbar(pages.Trail(pages.FestCrumbs(ref, data.Fest.Title), data.Venue.Label())),
	}
	// venue-page.ts draws the teams from the page's copy and the device's
	// unsent changes, and keeps both when the network is gone.
	work := []dopeui.Item{dopeui.SpaceLG, dopeui.Data("venue-page", venuePageConfig(data, ref)), dopeui.Data("venue-teams", venueTeamsJSON(data.Teams)),
		dopeui.Note(dopeui.Hidden(), dopeui.Data("offline-status", ""))}
	work = append(work, venueGamesSection(data, ref), venueTeamsSection(data))
	if data.Synch {
		work = append(work, venueOfflineSection())
	}
	return &dopeui.Doc{Nodes: []dopeui.Node{dopeui.Page(append(page, dopeui.Col(work...))...)}}
}

// venueGame is a Game of the venue as its page's script knows it: the id its
// unsent answers are kept under, and the page to save for work offline.
type venueGame struct {
	ID  int64  `json:"id"`
	URL string `json:"url"`
}

func venuePageConfig(data hostVenuePageData, ref string) string {
	config := struct {
		Fest  int64       `json:"fest"`
		Venue int64       `json:"venue"`
		API   string      `json:"api"`
		Max   int         `json:"max"`
		Games []venueGame `json:"games"`
	}{Fest: data.Fest.ID, Venue: data.Venue.ID, API: "/api/fest/" + ref, Max: numbering.MaxNumber, Games: []venueGame{}}
	for _, g := range data.Games {
		config.Games = append(config.Games, venueGame{ID: g.ID, URL: venueGameURL(g, data.Venue.ID, ref)})
	}
	return mustJSON(config)
}

// venueTeamsJSON is the venue's teams as venue-page.ts draws them.
func venueTeamsJSON(teams []hostFestTeam) string {
	type row struct {
		Ref     int64  `json:"ref"`
		Name    string `json:"name"`
		City    string `json:"city"`
		Players int    `json:"players"`
		Number  int    `json:"number"`
	}
	rows := make([]row, len(teams))
	for i, t := range teams {
		rows[i] = row{Ref: t.ID, Name: t.Name, City: t.City, Players: t.Players, Number: t.VenueNumber}
	}
	return mustJSON(rows)
}

func mustJSON(v any) string {
	out, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(out)
}

// venueGameURL is a Game's page as the venue plays it: a Game of the whole
// synch fest opens on the venue's sheet.
func venueGameURL(g PublicFestGame, venueID int64, ref string) string {
	href := "/host/fest/" + ref + "/game/" + g.Ref() + "/"
	if g.MultiFestVenueID == 0 {
		href += "?venue=" + idstr.Format(venueID)
	}
	return href
}

// venueOfflineSection saves the venue's page and its Games on the device, so
// they open without a network.
func venueOfflineSection() *dopeui.Element {
	s := dopestrings.Default
	return dopeui.Section(
		dopeui.Subhead(dopeui.Text(s.Host.Offline.Subhead())),
		dopeui.Note(dopeui.Data("offline-prepared", ""), dopeui.Text(s.Host.Offline.PrepareHint())),
		dopeui.Row(dopeui.Button(dopeui.Data("offline-prepare", ""), dopeui.Text(s.Host.Offline.Prepare()))),
	)
}

func venueGamesSection(data hostVenuePageData, ref string) *dopeui.Element {
	s := dopestrings.Default
	sect := []dopeui.Item{dopeui.Subhead(dopeui.Text(s.Host.Venue.GamesSubhead()))}
	if len(data.Games) == 0 {
		return dopeui.Section(append(sect, dopeui.Empty(dopeui.Text(s.Host.Venue.GamesEmpty())))...)
	}
	rows := make([]dopeui.Item, 0, len(data.Games))
	for _, g := range data.Games {
		href := venueGameURL(g, data.Venue.ID, ref)
		if g.MultiFestVenueID == 0 {
			href += "#input"
		}
		kind := games.Get(g.Type).Title
		row := []dopeui.Item{dopeui.Href(href), dopeui.Listtitle(dopeui.Text(kind))}
		if g.Title != data.Venue.Title && g.Title != kind {
			row = append(row, dopeui.Muted(dopeui.Text(g.Title)))
		}
		rows = append(rows, dopeui.Listrow(row...))
	}
	return dopeui.Section(append(sect, dopeui.List(rows...))...)
}

func venueTeamsSection(data hostVenuePageData) *dopeui.Element {
	s := dopestrings.Default
	// The rows are venue-page.ts's: it fills a copy of the hidden one per team.
	template := dopeui.Trow(dopeui.Hidden(), dopeui.Data("team-row", ""),
		dopeui.Cell(dopeui.Numfield(dopeui.Narrow(), dopeui.Data("unique", ""), dopeui.Min("1"), dopeui.Max(strconv.Itoa(numbering.MaxNumber)))),
		dopeui.Cell(dopeui.Iconbtn(dopeui.IconPencil, dopeui.Label(s.Host.Roster.EditTeamLabel("")), dopeui.Data("team-edit", ""))),
		dopeui.Cell(dopeui.Data("team-name", "")),
		dopeui.Cell(dopeui.Data("team-city", "")),
		dopeui.Cell(dopeui.Data("team-players", "")),
	)
	return dopeui.Section(
		dopeui.Subhead(dopeui.Data("teams-count", ""), dopeui.Text(s.Host.Venue.TeamsSubhead(strconv.Itoa(len(data.Teams))))),
		dopeui.Row(dopeui.Button(dopeui.Data("team-add", ""), searchMode(data.Instant), dopeui.Text(s.Host.Roster.AddTeamBtn()))),
		dopeui.Empty(dopeui.Data("teams-empty", ""), dopeui.Text(s.Host.Roster.TeamsEmptyVenue())),
		dopeui.Form(dopeui.DirCol, dopeui.Autocomplete("off"), dopeui.Data("venue-numbers", ""),
			dopeui.Data("dirty-submit", ""), dopeui.Data("unique-values", s.Host.Venue.NumberDuplicate()),
			dopeui.Note(dopeui.Text(s.Host.Venue.TeamsHint())),
			dopeui.Table(dopeui.Scroll(), dopeui.Trow(
				dopeui.Hcell(dopeui.Text(s.Host.Venue.ColNumber())), dopeui.Hcell(),
				dopeui.Hcell(dopeui.Text(s.Host.Roster.TeamLabel())),
				dopeui.Hcell(dopeui.Text(s.Host.Roster.ColCity())), dopeui.Hcell(dopeui.Text(s.Host.Roster.ColPlayers())),
			), template),
			dopeui.Row(dopeui.Button(dopeui.Submit(), dopeui.Text(s.Host.Venue.SaveNumbers()))),
		),
	)
}

// venueNumberField names one team's number input; the save reads the team id
// back off the name.
func venueNumberField(teamID int64) string {
	return "number_" + idstr.Format(teamID)
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
