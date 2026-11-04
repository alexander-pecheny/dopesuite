package ratingsite

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"
)

type Town struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Country *struct {
		ID int64 `json:"id"`
	} `json:"country"`
}

// TownName is the town's name, empty for no town.
func TownName(t *Town) string {
	if t == nil {
		return ""
	}
	return strings.TrimSpace(t.Name)
}

type Team struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Town *Town  `json:"town"`
}

type Player struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Surname    string `json:"surname"`
	Patronymic string `json:"patronymic"`
}

// FullName is the surname, then the name.
func (p *Player) FullName() string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(strings.TrimSpace(p.Surname) + " " + strings.TrimSpace(p.Name))
}

// Flag is a division a team plays in on a tournament (a dope Flag).
type Flag struct {
	ID        int64  `json:"id"`
	ShortName string `json:"shortName"`
	FullName  string `json:"fullName"`
}

// Member is a player of a team's roster on a tournament, with the roster's
// flag, the site's one-letter mark of a base player, a legionnaire and so on.
type Member struct {
	Player  Player `json:"player"`
	Flag    string `json:"flag"`
	Captain bool   `json:"captain"`
}

type Venue struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Town *Town  `json:"town"`
}

// Request is a request to play a synch tournament at a venue.
type Request struct {
	ID             int64   `json:"id"`
	Status         string  `json:"status"`
	Venue          *Venue  `json:"venue"`
	Representative *Player `json:"representative"`
}

// RequestAccepted is the status of a request the site has accepted.
const RequestAccepted = "A"

// Result is a team on a tournament as the site publishes it: Current is the
// name and town it played under, which may be one-off.
type Result struct {
	Team         Team     `json:"team"`
	Current      Team     `json:"current"`
	TeamMembers  []Member `json:"teamMembers"`
	Flags        []Flag   `json:"flags"`
	SynchRequest *Request `json:"synchRequest"`
}

// Results is every team of the tournament with its roster and flags. Anyone
// may read it, results or not.
func (c Client) Results(ctx context.Context, tournament int64) ([]Result, error) {
	var out []Result
	err := c.get(ctx, fmt.Sprintf("/tournaments/%d/results?includeTeamMembers=1&includeTeamFlags=1", tournament), &out)
	return out, err
}

// Tours is how many questions each tour of the tournament has.
func (c Client) Tours(ctx context.Context, tournament int64) ([]int, error) {
	var t struct {
		QuestionQty map[string]int `json:"questionQty"`
	}
	if err := c.get(ctx, fmt.Sprintf("/tournaments/%d", tournament), &t); err != nil {
		return nil, err
	}
	tours := make([]int, 0, len(t.QuestionQty))
	for n := 1; n <= len(t.QuestionQty); n++ {
		tours = append(tours, t.QuestionQty[fmt.Sprint(n)])
	}
	return tours, nil
}

// requestsPage is more than any tournament has requests.
const requestsPage = 1000

func (c Client) Requests(ctx context.Context, tournament int64) ([]Request, error) {
	var out []Request
	err := c.get(ctx, fmt.Sprintf("/tournaments/%d/requests?itemsPerPage=%d", tournament, requestsPage), &out)
	return out, err
}

// EntryTeam is a team on the tournament's data-entry page, with its request and its
// answers: nil when it has none yet.
type EntryTeam struct {
	Team    int64   `json:"team"`
	Name    string  `json:"name"`
	Request *int64  `json:"request"`
	Mask    *string `json:"mask"`
}

// Entry is the tournament's data-entry page as the token's user sees it.
func (c Client) Entry(ctx context.Context, tournament int64) ([]EntryTeam, error) {
	var entry struct {
		Teams []EntryTeam `json:"teams"`
	}
	err := c.get(ctx, fmt.Sprintf("/tournaments/%d/results_entry", tournament), &entry)
	return entry.Teams, err
}

// Season is the site's season running on the day. The API ignores date
// filters, so every season is read and the day's is picked here.
func (c Client) Season(ctx context.Context, day time.Time) (int64, error) {
	var seasons []struct {
		ID        int64  `json:"id"`
		DateStart string `json:"dateStart"`
		DateEnd   string `json:"dateEnd"`
	}
	if err := c.get(ctx, "/seasons?itemsPerPage=200", &seasons); err != nil {
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

// BaseRoster is the ids of the team's base roster in the season, in the
// site's order.
func (c Client) BaseRoster(ctx context.Context, team, season int64) ([]int64, error) {
	var rows []struct {
		PlayerID    int64   `json:"idplayer"`
		DateRemoved *string `json:"dateRemoved"`
	}
	if err := c.get(ctx, fmt.Sprintf("/teams/%d/seasons?idseason=%d&itemsPerPage=100", team, season), &rows); err != nil {
		return nil, err
	}
	var out []int64
	for _, row := range rows {
		if row.DateRemoved == nil && row.PlayerID > 0 {
			out = append(out, row.PlayerID)
		}
	}
	return out, nil
}

func (c Client) Player(ctx context.Context, id int64) (Player, error) {
	var p Player
	err := c.get(ctx, fmt.Sprintf("/players/%d", id), &p)
	return p, err
}

func (c Client) Team(ctx context.Context, id int64) (Team, error) {
	var t Team
	err := c.get(ctx, fmt.Sprintf("/teams/%d", id), &t)
	return t, err
}

func (c Client) Town(ctx context.Context, id int64) (Town, error) {
	var t Town
	err := c.get(ctx, fmt.Sprintf("/towns/%d", id), &t)
	return t, err
}

// Players is the site's answer to a search by surname, and by name unless
// it is empty. The site matches the starts of words and cannot sort.
func (c Client) Players(ctx context.Context, surname, name string, limit int) ([]Player, error) {
	q := url.Values{"surname": {surname}, "itemsPerPage": {fmt.Sprint(limit)}}
	if name != "" {
		q.Set("name", name)
	}
	var out []Player
	err := c.get(ctx, "/players?"+q.Encode(), &out)
	return out, err
}

// Teams is the site's answer to a search by name.
func (c Client) Teams(ctx context.Context, name string, limit int) ([]Team, error) {
	var out []Team
	err := c.get(ctx, "/teams?"+url.Values{"name": {name}, "itemsPerPage": {fmt.Sprint(limit)}}.Encode(), &out)
	return out, err
}

// TeamFlags is every Flag the site knows: its own and those organizers made.
func (c Client) TeamFlags(ctx context.Context) ([]Flag, error) {
	var out []Flag
	err := c.get(ctx, "/tournament_team_flags", &out)
	return out, err
}

// Towns is the site's answer to a search by name.
func (c Client) Towns(ctx context.Context, name string, limit int) ([]Town, error) {
	var out []Town
	err := c.get(ctx, "/towns?"+url.Values{"name": {name}, "itemsPerPage": {fmt.Sprint(limit)}}.Encode(), &out)
	return out, err
}
