package imports

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"dope/dope/domain/multifest"
	"dope/dope/storage/buffdb"

	"code.pecheny.me/pecheny/dopesuite/dopecore/idstr"
)

// The rating site's people and teams, for the fest roster editor to suggest
// (ADR-0024). buff's mirror answers when dope can read it; otherwise the site's
// API is asked, so the editor works on a box without the mirror.

// RatingAPI is the site's API. Tests point it at a fake, so none of them
// reaches the network.
var RatingAPI = "https://api.rating.chgk.info"

// RatingPerson is one player of the rating site as the editor offers them.
type RatingPerson struct {
	RatingID   int64  `json:"rating_id"`
	FirstName  string `json:"first_name"`
	LastName   string `json:"last_name"`
	Patronymic string `json:"patronymic,omitempty"`
	Games      int    `json:"games,omitempty"`
}

// RatingTeamRef is one team of the rating site as the editor offers it.
type RatingTeamRef struct {
	RatingID int64  `json:"rating_id"`
	Name     string `json:"name"`
	City     string `json:"city"`
}

const ratingSuggestLimit = 12

// ratingAPIPage is how many the API is asked for, to pick the newest from.
const ratingAPIPage = 50

var ratingClient = &http.Client{Timeout: 6 * time.Second}

func getRatingJSON(ctx context.Context, path string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, RatingAPI+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := ratingClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("rating %s: %s", path, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(into)
}

type apiPlayer struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Patronymic string `json:"patronymic"`
	Surname    string `json:"surname"`
}

func (p apiPlayer) person() RatingPerson {
	return RatingPerson{RatingID: p.ID, FirstName: p.Name, LastName: p.Surname, Patronymic: p.Patronymic}
}

func fromBuff(p buffdb.Person) RatingPerson {
	return RatingPerson{RatingID: p.ID, FirstName: p.Name, LastName: p.Surname, Patronymic: p.Patronymic, Games: p.Games}
}

// ratingIDQuery reads a query of digits alone as a rating id.
func ratingIDQuery(query string) (int64, bool) {
	query = strings.TrimSpace(query)
	if query == "" || strings.TrimLeft(query, "0123456789") != "" {
		return 0, false
	}
	id, err := idstr.Parse(query)
	return id, err == nil && id > 0
}

// SearchRatingPlayers finds the rating site's people by a surname, or a
// surname and a name in either order, each as a word's start, or one person by
// their rating id.
func SearchRatingPlayers(ctx context.Context, buff *buffdb.Store, query string) []RatingPerson {
	if id, ok := ratingIDQuery(query); ok {
		if p, ok := buff.PlayerByID(ctx, id); ok {
			return []RatingPerson{fromBuff(p)}
		}
		var p apiPlayer
		if buff.Enabled() || getRatingJSON(ctx, fmt.Sprintf("/players/%d", id), &p) != nil || p.ID == 0 {
			return []RatingPerson{}
		}
		return []RatingPerson{p.person()}
	}
	words := strings.Fields(query)
	if len(words) == 0 || len([]rune(words[0])) < 2 {
		return []RatingPerson{}
	}
	if buff.Enabled() {
		out := []RatingPerson{}
		for _, p := range buff.SearchPlayers(ctx, query, ratingSuggestLimit) {
			out = append(out, fromBuff(p))
		}
		return out
	}
	ask := func(surname, name string) []RatingPerson {
		q := url.Values{"surname": {surname}, "itemsPerPage": {fmt.Sprint(ratingAPIPage)}}
		if name != "" {
			q.Set("name", name)
		}
		var found []apiPlayer
		if err := getRatingJSON(ctx, "/players?"+q.Encode(), &found); err != nil {
			return nil
		}
		var out []RatingPerson
		for _, p := range found {
			out = append(out, p.person())
		}
		return out
	}
	var found []RatingPerson
	if len(words) == 1 {
		found = ask(words[0], "")
	} else {
		found = append(ask(words[0], words[1]), ask(words[1], words[0])...)
	}
	// The API cannot sort; the newest ids are the people playing now.
	sort.SliceStable(found, func(i, j int) bool { return found[i].RatingID > found[j].RatingID })
	out := []RatingPerson{}
	seen := map[int64]bool{}
	for _, p := range found {
		if !seen[p.RatingID] && len(out) < ratingSuggestLimit {
			seen[p.RatingID] = true
			out = append(out, p)
		}
	}
	return out
}

type apiTeamRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Town *struct {
		Name string `json:"name"`
	} `json:"town"`
}

func (t apiTeamRef) ref() RatingTeamRef {
	ref := RatingTeamRef{RatingID: t.ID, Name: t.Name}
	if t.Town != nil {
		ref.City = t.Town.Name
	}
	return ref
}

// SearchRatingTeams finds the rating site's teams by the start of their name,
// or of a word of it.
func SearchRatingTeams(ctx context.Context, buff *buffdb.Store, query string) []RatingTeamRef {
	query = strings.TrimSpace(query)
	out := []RatingTeamRef{}
	// A team named with digits is found by its name after the one with that id.
	if id, ok := ratingIDQuery(query); ok {
		out = append(out, ratingTeamByID(ctx, buff, id)...)
	}
	if len([]rune(query)) < 2 {
		return out
	}
	if buff.Enabled() {
		for _, t := range buff.SearchTeams(ctx, query, ratingSuggestLimit) {
			if len(out) == 0 || out[0].RatingID != t.ID {
				out = append(out, RatingTeamRef{RatingID: t.ID, Name: t.Name, City: t.Town})
			}
		}
		return out
	}
	var found []apiTeamRef
	if err := getRatingJSON(ctx, "/teams?"+url.Values{"name": {query}, "itemsPerPage": {fmt.Sprint(ratingAPIPage)}}.Encode(), &found); err != nil {
		return out
	}
	// The API cannot sort; the newest ids are the teams playing now.
	sort.SliceStable(found, func(i, j int) bool { return found[i].ID > found[j].ID })
	for _, t := range found {
		if len(out) < ratingSuggestLimit && (len(out) == 0 || out[0].RatingID != t.ID) {
			out = append(out, t.ref())
		}
	}
	return out
}

// ratingTeamByID is the team with this rating id, or none.
func ratingTeamByID(ctx context.Context, buff *buffdb.Store, id int64) []RatingTeamRef {
	if t, ok := buff.TeamByID(ctx, id); ok {
		return []RatingTeamRef{{RatingID: t.ID, Name: t.Name, City: t.Town}}
	}
	var team apiTeamRef
	if buff.Enabled() || getRatingJSON(ctx, fmt.Sprintf("/teams/%d", id), &team) != nil || team.ID == 0 {
		return nil
	}
	return []RatingTeamRef{team.ref()}
}

// RatingBaseRoster is a team's base roster in the season
// running now, with the team itself. buff answers when it has mirrored that
// season; otherwise the site does, from the team's latest season.
func RatingBaseRoster(ctx context.Context, buff *buffdb.Store, teamID int64) (RatingTeamRef, []RatingPerson, error) {
	var team apiTeamRef
	teamErr := getRatingJSON(ctx, fmt.Sprintf("/teams/%d", teamID), &team)
	team.ID = teamID
	out := []RatingPerson{}
	if people := buff.BaseRoster(ctx, teamID, time.Now()); len(people) > 0 {
		for _, p := range people {
			out = append(out, fromBuff(p))
		}
		return team.ref(), out, nil
	}
	if teamErr != nil {
		return RatingTeamRef{}, nil, teamErr
	}
	season, err := currentRatingSeason(ctx, time.Now())
	if err != nil {
		return team.ref(), out, err
	}
	var rows []struct {
		PlayerID    int64   `json:"idplayer"`
		DateRemoved *string `json:"dateRemoved"`
	}
	if err := getRatingJSON(ctx, fmt.Sprintf("/teams/%d/seasons?idseason=%d&itemsPerPage=100", teamID, season), &rows); err != nil {
		return team.ref(), out, err
	}
	for _, row := range rows {
		if row.DateRemoved != nil || row.PlayerID <= 0 {
			continue
		}
		var p apiPlayer
		if err := getRatingJSON(ctx, fmt.Sprintf("/players/%d", row.PlayerID), &p); err != nil {
			return team.ref(), out, err
		}
		out = append(out, p.person())
	}
	return team.ref(), out, nil
}

// currentRatingSeason is the rating site's season running on the day. The
// API pages its lists and ignores date filters, so every season is read and
// the day's is picked here.
func currentRatingSeason(ctx context.Context, day time.Time) (int64, error) {
	var seasons []struct {
		ID        int64  `json:"id"`
		DateStart string `json:"dateStart"`
		DateEnd   string `json:"dateEnd"`
	}
	if err := getRatingJSON(ctx, "/seasons?itemsPerPage=200", &seasons); err != nil {
		return 0, err
	}
	for _, s := range seasons {
		start, err1 := time.Parse(time.RFC3339, s.DateStart)
		end, err2 := time.Parse(time.RFC3339, s.DateEnd)
		if err1 == nil && err2 == nil && !day.Before(start) && day.Before(end) {
			return s.ID, nil
		}
	}
	return 0, fmt.Errorf("no rating season on %s", day.Format(time.DateOnly))
}

// FetchRatingTourComp is how many questions each tour of a tournament has, as
// the rating site lists them.
func FetchRatingTourComp(ctx context.Context, ratingID int64) ([]int, error) {
	var tournament struct {
		QuestionQty map[string]int `json:"questionQty"`
	}
	if err := getRatingJSON(ctx, fmt.Sprintf("/tournaments/%d.json", ratingID), &tournament); err != nil {
		return nil, err
	}
	tours := make([]int, 0, len(tournament.QuestionQty))
	for n := 1; n <= len(tournament.QuestionQty); n++ {
		tours = append(tours, tournament.QuestionQty[fmt.Sprint(n)])
	}
	return tours, nil
}

// ratingRequestsPage is more than any tournament has venues.
const ratingRequestsPage = 1000

// requestAccepted is the status of a request the site has accepted.
const requestAccepted = "A"

// FetchRatingVenues is the venues whose requests to play a synch tournament
// the site has accepted. They are known well before the teams are.
func FetchRatingVenues(ctx context.Context, ratingID int64) ([]multifest.RatingVenue, error) {
	var requests []struct {
		ID     int64  `json:"id"`
		Status string `json:"status"`
		Venue  struct {
			ID   int64       `json:"id"`
			Name string      `json:"name"`
			Town *ratingTown `json:"town"`
		} `json:"venue"`
		Representative *struct {
			Name    string `json:"name"`
			Surname string `json:"surname"`
		} `json:"representative"`
	}
	if err := getRatingJSON(ctx, fmt.Sprintf("/tournaments/%d/requests?itemsPerPage=%d", ratingID, ratingRequestsPage), &requests); err != nil {
		return nil, err
	}
	var venues []multifest.RatingVenue
	for _, r := range requests {
		if r.Status == requestAccepted && r.ID > 0 && r.Venue.ID > 0 {
			venue := multifest.RatingVenue{ID: r.Venue.ID, Request: r.ID, Title: strings.TrimSpace(r.Venue.Name), Town: ratingTownName(r.Venue.Town)}
			if p := r.Representative; p != nil {
				venue.Representative = strings.TrimSpace(strings.TrimSpace(p.Surname) + " " + strings.TrimSpace(p.Name))
			}
			venues = append(venues, venue)
		}
	}
	return venues, nil
}
