package ratingexport

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"code.pecheny.me/pecheny/dopesuite/dopecore/idstr"
)

// Unknown is a team, or a player of a team, that the site has no id for in
// dope, with the site's namesakes to link it to. Ref names it in a choice.
type Unknown struct {
	Ref        string      `json:"ref"`
	Team       int64       `json:"team_id"`
	TeamName   string      `json:"team_name"`
	Player     int64       `json:"player_id,omitempty"`
	FirstName  string      `json:"first_name,omitempty"`
	LastName   string      `json:"last_name,omitempty"`
	Name       string      `json:"name"`
	Candidates []Candidate `json:"candidates"`
	// Town is the site's town a new team would get, 0 when the site has no
	// town for the team's city or its venue.
	Town     int64  `json:"town,omitempty"`
	TownName string `json:"town_name,omitempty"`
}

// Candidate is a namesake on the site.
type Candidate struct {
	ID    int64  `json:"id"`
	Label string `json:"label"`
}

// ChoiceNew is the choice that creates the team or the player on the site.
const ChoiceNew = "new"

// Default is the choice the page starts with: the only namesake, a new one
// when there is none, and nothing when a person has to pick.
func (u Unknown) Default() string {
	switch len(u.Candidates) {
	case 0:
		if u.Player == 0 && u.Town == 0 {
			return ""
		}
		return ChoiceNew
	case 1:
		return idstr.Format(u.Candidates[0].ID)
	default:
		return ""
	}
}

// Identify is the site's id for the Unknown under the choice: a namesake's,
// or that of a team or player it creates.
func Identify(ctx context.Context, site Site, tournament int64, u Unknown, choice string) (int64, error) {
	if choice != ChoiceNew {
		return idstr.Parse(choice)
	}
	var created struct {
		ID int64 `json:"id"`
	}
	path := fmt.Sprintf("/tournaments/%d/results_entry/", tournament)
	var err error
	if u.Player != 0 {
		err = site.do(ctx, http.MethodPost, path+"players", map[string]string{"surname": u.LastName, "name": u.FirstName}, &created)
	} else {
		err = site.do(ctx, http.MethodPost, path+"teams", map[string]any{"name": u.Name, "town": u.Town}, &created)
	}
	return created.ID, err
}

// fold is a name as the site compares names: case and the letter yo aside.
func fold(s string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), "\u0451", "\u0435")
}

// namesakesPage is more than any name has namesakes.
const namesakesPage = 50

func (s Site) playerNamesakes(ctx context.Context, first, last string) ([]Candidate, error) {
	var people []struct {
		ID         int64   `json:"id"`
		Name       string  `json:"name"`
		Surname    string  `json:"surname"`
		Patronymic *string `json:"patronymic"`
	}
	q := url.Values{"surname": {last}, "name": {first}, "itemsPerPage": {fmt.Sprint(namesakesPage)}}
	if err := s.do(ctx, http.MethodGet, "/players?"+q.Encode(), nil, &people); err != nil {
		return nil, err
	}
	out := []Candidate{}
	for _, p := range people {
		if fold(p.Surname) != fold(last) || fold(p.Name) != fold(first) {
			continue
		}
		label := p.Surname + " " + p.Name
		if p.Patronymic != nil && *p.Patronymic != "" {
			label += " " + *p.Patronymic
		}
		out = append(out, Candidate{ID: p.ID, Label: label})
	}
	return out, nil
}

type siteTown struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

func (s Site) teamNamesakes(ctx context.Context, name string) ([]Candidate, error) {
	var teams []struct {
		ID   int64     `json:"id"`
		Name string    `json:"name"`
		Town *siteTown `json:"town"`
	}
	q := url.Values{"name": {name}, "itemsPerPage": {fmt.Sprint(namesakesPage)}}
	if err := s.do(ctx, http.MethodGet, "/teams?"+q.Encode(), nil, &teams); err != nil {
		return nil, err
	}
	out := []Candidate{}
	for _, t := range teams {
		if fold(t.Name) != fold(name) {
			continue
		}
		label := t.Name
		if t.Town != nil {
			label += " (" + t.Town.Name + ")"
		}
		out = append(out, Candidate{ID: t.ID, Label: label})
	}
	return out, nil
}

// town is the site's only town of that name, if it has exactly one.
func (s Site) town(ctx context.Context, name string) (siteTown, error) {
	if strings.TrimSpace(name) == "" {
		return siteTown{}, nil
	}
	var towns []siteTown
	q := url.Values{"name": {name}, "itemsPerPage": {fmt.Sprint(namesakesPage)}}
	if err := s.do(ctx, http.MethodGet, "/towns?"+q.Encode(), nil, &towns); err != nil {
		return siteTown{}, err
	}
	var found []siteTown
	for _, t := range towns {
		if fold(t.Name) == fold(name) {
			found = append(found, t)
		}
	}
	if len(found) != 1 {
		return siteTown{}, nil
	}
	return found[0], nil
}
