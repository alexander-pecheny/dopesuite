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

	"dope/dope/storage/buffdb"
)

// The rating site's people and teams, for the fest roster editor to suggest
// (ADR-0024). buff's mirror answers when dope can read it; otherwise the site's
// API is asked, so the editor works on a box without the mirror.

const ratingAPI = "https://api.rating.chgk.info"

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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ratingAPI+path, nil)
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

// SearchRatingPlayers finds the rating site's people by a surname, or a
// surname and a name in either order, each as a word's start.
func SearchRatingPlayers(ctx context.Context, buff *buffdb.Store, query string) []RatingPerson {
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
	if len([]rune(query)) < 2 {
		return out
	}
	if buff.Enabled() {
		for _, t := range buff.SearchTeams(ctx, query, ratingSuggestLimit) {
			out = append(out, RatingTeamRef{RatingID: t.ID, Name: t.Name, City: t.Town})
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
		if len(out) < ratingSuggestLimit {
			out = append(out, t.ref())
		}
	}
	return out
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
