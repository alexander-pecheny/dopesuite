package imports

import (
	"context"
	"sort"
	"strings"
	"time"

	"dope/dope/domain/multifest"
	"dope/dope/storage/buffdb"
	"dope/dope/storage/ratingsite"

	"code.pecheny.me/pecheny/dopesuite/dopecore/idstr"
)

// The rating site's people and teams, for the fest roster editor to suggest
// (ADR-0024). buff's mirror answers when dope can read it; otherwise the site's
// API is asked, so the editor works on a box without the mirror.

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

// suggestTimeout keeps a suggestion from waiting on a slow site.
const suggestTimeout = 6 * time.Second

func person(p ratingsite.Player) RatingPerson {
	return RatingPerson{RatingID: p.ID, FirstName: p.Name, LastName: p.Surname, Patronymic: p.Patronymic}
}

func teamRef(t ratingsite.Team) RatingTeamRef {
	return RatingTeamRef{RatingID: t.ID, Name: t.Name, City: ratingsite.TownName(t.Town)}
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
func SearchRatingPlayers(ctx context.Context, buff *buffdb.Store, site ratingsite.Client, query string) []RatingPerson {
	ctx, cancel := context.WithTimeout(ctx, suggestTimeout)
	defer cancel()
	if id, ok := ratingIDQuery(query); ok {
		if p, ok := buff.PlayerByID(ctx, id); ok {
			return []RatingPerson{fromBuff(p)}
		}
		if buff.Enabled() {
			return []RatingPerson{}
		}
		p, err := site.Player(ctx, id)
		if err != nil || p.ID == 0 {
			return []RatingPerson{}
		}
		return []RatingPerson{person(p)}
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
		found, err := site.Players(ctx, surname, name, ratingAPIPage)
		if err != nil {
			return nil
		}
		var out []RatingPerson
		for _, p := range found {
			out = append(out, person(p))
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

// SearchRatingTeams finds the rating site's teams by the start of their name,
// or of a word of it.
func SearchRatingTeams(ctx context.Context, buff *buffdb.Store, site ratingsite.Client, query string) []RatingTeamRef {
	ctx, cancel := context.WithTimeout(ctx, suggestTimeout)
	defer cancel()
	query = strings.TrimSpace(query)
	out := []RatingTeamRef{}
	// A team named with digits is found by its name after the one with that id.
	if id, ok := ratingIDQuery(query); ok {
		out = append(out, ratingTeamByID(ctx, buff, site, id)...)
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
	found, err := site.Teams(ctx, query, ratingAPIPage)
	if err != nil {
		return out
	}
	// The API cannot sort; the newest ids are the teams playing now.
	sort.SliceStable(found, func(i, j int) bool { return found[i].ID > found[j].ID })
	for _, t := range found {
		if len(out) < ratingSuggestLimit && (len(out) == 0 || out[0].RatingID != t.ID) {
			out = append(out, teamRef(t))
		}
	}
	return out
}

// ratingTeamByID is the team with this rating id, or none.
func ratingTeamByID(ctx context.Context, buff *buffdb.Store, site ratingsite.Client, id int64) []RatingTeamRef {
	if t, ok := buff.TeamByID(ctx, id); ok {
		return []RatingTeamRef{{RatingID: t.ID, Name: t.Name, City: t.Town}}
	}
	if buff.Enabled() {
		return nil
	}
	team, err := site.Team(ctx, id)
	if err != nil || team.ID == 0 {
		return nil
	}
	return []RatingTeamRef{teamRef(team)}
}

// RatingBaseRoster is a team's base roster in the season
// running now, with the team itself. buff answers when it has mirrored that
// season; otherwise the site does, from the team's latest season.
func RatingBaseRoster(ctx context.Context, buff *buffdb.Store, site ratingsite.Client, teamID int64) (RatingTeamRef, []RatingPerson, error) {
	team, teamErr := site.Team(ctx, teamID)
	team.ID = teamID
	out := []RatingPerson{}
	if people := buff.BaseRoster(ctx, teamID, time.Now()); len(people) > 0 {
		for _, p := range people {
			out = append(out, fromBuff(p))
		}
		return teamRef(team), out, nil
	}
	if teamErr != nil {
		return RatingTeamRef{}, nil, teamErr
	}
	season, err := site.Season(ctx, time.Now())
	if err != nil {
		return teamRef(team), out, err
	}
	ids, err := site.BaseRoster(ctx, teamID, season)
	if err != nil {
		return teamRef(team), out, err
	}
	for _, id := range ids {
		p, err := site.Player(ctx, id)
		if err != nil {
			return teamRef(team), out, err
		}
		out = append(out, person(p))
	}
	return teamRef(team), out, nil
}

// FetchRatingVenues is the venues whose requests to play a synch tournament
// the site has accepted. They are known well before the teams are.
func FetchRatingVenues(ctx context.Context, site ratingsite.Client, ratingID int64) ([]multifest.RatingVenue, error) {
	requests, err := site.Requests(ctx, ratingID)
	if err != nil {
		return nil, err
	}
	var venues []multifest.RatingVenue
	for _, r := range requests {
		if r.Status == ratingsite.RequestAccepted && r.ID > 0 && r.Venue != nil && r.Venue.ID > 0 {
			venues = append(venues, multifest.RatingVenue{ID: r.Venue.ID, Request: r.ID, Title: strings.TrimSpace(r.Venue.Name),
				Town: ratingsite.TownName(r.Venue.Town), Representative: r.Representative.FullName()})
		}
	}
	return venues, nil
}
