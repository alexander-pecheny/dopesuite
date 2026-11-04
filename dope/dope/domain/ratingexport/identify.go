package ratingexport

import (
	"context"
	"strings"
	"unicode"

	"dope/dope/storage/ratingsite"
	dopestrings "dope/i18nstrings"

	corei18n "code.pecheny.me/pecheny/dopesuite/dopecore/i18nstrings"
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
	// Held is a player of a team without an id that has no obvious choice:
	// the player goes to the site only once a person settles the team.
	Held bool `json:"held,omitempty"`
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
	if u.Held {
		return ""
	}
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
		return site.existing(ctx, u, choice)
	}
	if u.Player != 0 {
		return site.CreatePlayer(ctx, tournament, u.LastName, u.FirstName)
	}
	return site.CreateTeam(ctx, tournament, u.Name, u.Town)
}

// existing is the id a person chose or typed, once the site confirms it
// has a team or a player under it.
func (s Site) existing(ctx context.Context, u Unknown, choice string) (int64, error) {
	id, err := idstr.Parse(strings.TrimSpace(choice))
	if err != nil || id <= 0 {
		return 0, corei18n.User(dopestrings.Default.Host.RatingExport.BadChoice(u.Name))
	}
	if u.Player != 0 {
		_, err = s.Player(ctx, id)
	} else {
		_, err = s.Team(ctx, id)
	}
	if ratingsite.NotFound(err) {
		return 0, corei18n.User(dopestrings.Default.Host.RatingExport.NoSuchId(idstr.Format(id), u.Name))
	}
	return id, err
}

// fold is a name as dope compares it with the site's: case, the letter yo
// and the punctuation between words aside, so "Jersey M" is "Jersey-M".
func fold(s string) string {
	words := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	return strings.ReplaceAll(strings.Join(words, " "), "\u0451", "\u0435")
}

// longestWord is what the site is asked for when the whole name finds
// nothing, since the site's search keeps the punctuation.
func longestWord(name string) string {
	longest := ""
	for _, w := range strings.Fields(fold(name)) {
		if len([]rune(w)) > len([]rune(longest)) {
			longest = w
		}
	}
	return longest
}

// namesakesPage is more than any name has namesakes.
const namesakesPage = 50

func (s Site) playerNamesakes(ctx context.Context, first, last string) ([]Candidate, error) {
	people, err := s.Players(ctx, last, first, namesakesPage)
	if err != nil {
		return nil, err
	}
	out := []Candidate{}
	for _, p := range people {
		if fold(p.Surname) != fold(last) || fold(p.Name) != fold(first) {
			continue
		}
		label := p.Surname + " " + p.Name
		if p.Patronymic != "" {
			label += " " + p.Patronymic
		}
		out = append(out, Candidate{ID: p.ID, Label: label})
	}
	return out, nil
}

func (s Site) teamNamesakes(ctx context.Context, name string) ([]Candidate, error) {
	out, err := s.teamsNamed(ctx, name, name)
	if err != nil || len(out) > 0 {
		return out, err
	}
	if word := longestWord(name); word != "" && word != fold(name) {
		return s.teamsNamed(ctx, word, name)
	}
	return out, nil
}

// teamsNamed is the site's answer to the query, kept to the teams called
// name.
func (s Site) teamsNamed(ctx context.Context, query, name string) ([]Candidate, error) {
	teams, err := s.Teams(ctx, query, namesakesPage)
	if err != nil {
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
func (s Site) town(ctx context.Context, name string) (ratingsite.Town, error) {
	if strings.TrimSpace(name) == "" {
		return ratingsite.Town{}, nil
	}
	towns, err := s.Towns(ctx, name, namesakesPage)
	if err != nil {
		return ratingsite.Town{}, err
	}
	var found []ratingsite.Town
	for _, t := range towns {
		if fold(t.Name) == fold(name) {
			found = append(found, t)
		}
	}
	if len(found) != 1 {
		return ratingsite.Town{}, nil
	}
	return found[0], nil
}
