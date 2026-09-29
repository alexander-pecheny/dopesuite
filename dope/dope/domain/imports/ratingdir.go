package imports

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
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
		q := url.Values{"surname": {surname}, "itemsPerPage": {fmt.Sprint(ratingSuggestLimit)}}
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
	out := []RatingPerson{}
	if len(words) == 1 {
		return append(out, ask(words[0], "")...)
	}
	seen := map[int64]bool{}
	for _, p := range append(ask(words[0], words[1]), ask(words[1], words[0])...) {
		if !seen[p.RatingID] {
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
	if err := getRatingJSON(ctx, "/teams?"+url.Values{"name": {query}, "itemsPerPage": {fmt.Sprint(ratingSuggestLimit)}}.Encode(), &found); err != nil {
		return out
	}
	for _, t := range found {
		out = append(out, t.ref())
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
	var seasons []struct {
		PlayerID    int64   `json:"idplayer"`
		SeasonID    int64   `json:"idseason"`
		DateRemoved *string `json:"dateRemoved"`
	}
	if err := getRatingJSON(ctx, fmt.Sprintf("/teams/%d/seasons", teamID), &seasons); err != nil {
		return team.ref(), out, err
	}
	var latest int64
	for _, s := range seasons {
		if s.SeasonID > latest {
			latest = s.SeasonID
		}
	}
	for _, s := range seasons {
		if s.SeasonID != latest || s.DateRemoved != nil || s.PlayerID <= 0 {
			continue
		}
		var p apiPlayer
		if err := getRatingJSON(ctx, fmt.Sprintf("/players/%d", s.PlayerID), &p); err != nil {
			return team.ref(), out, err
		}
		out = append(out, p.person())
	}
	return team.ref(), out, nil
}
