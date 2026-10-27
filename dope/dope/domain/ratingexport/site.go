// Package ratingexport sends a fest's rosters and its OD's results to
// rating.chgk.info through the site's results entry API, as the user whose token it
// carries. On a synch tournament each venue's teams go in under its
// representative, which puts a new team in that venue's request.
package ratingexport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"dope/dope/storage/buffdb"
)

// DefaultBase is the rating site's API.
const DefaultBase = "https://api.rating.chgk.info"

// TournamentPage is a tournament's page on the site, for people.
const TournamentPage = "https://rating.chgk.info/tournament/%d"

const siteTimeout = 30 * time.Second

// Site is the rating site's API as the export uses it, authenticated by the
// user's token.
type Site struct {
	Base  string
	Token string
	// Buff answers the teams' base rosters when it mirrors the season, so the
	// site is asked only for the rest.
	Buff *buffdb.Store
	HTTP *http.Client
}

// Error is the site's refusal, in the site's own words.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("%d: %s", e.Status, e.Message) }

func (s Site) client() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return &http.Client{Timeout: siteTimeout}
}

func (s Site) do(ctx context.Context, method, path string, body, into any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	base := s.Base
	if base == "" {
		base = DefaultBase
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base, "/")+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if s.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.Token)
	}
	resp, err := s.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return &Error{Status: resp.StatusCode, Message: refusal(raw, resp.Status)}
	}
	if into == nil {
		return nil
	}
	return json.Unmarshal(raw, into)
}

// refusal is what the site said in a refused answer: each violation, else
// its detail, else the bare status.
func refusal(raw []byte, status string) string {
	var problem struct {
		Detail     string `json:"detail"`
		Violations []struct {
			PropertyPath string `json:"propertyPath"`
			Message      string `json:"message"`
		} `json:"violations"`
	}
	if json.Unmarshal(raw, &problem) != nil {
		return status
	}
	var lines []string
	for _, v := range problem.Violations {
		if v.PropertyPath != "" {
			lines = append(lines, v.PropertyPath+": "+v.Message)
		} else {
			lines = append(lines, v.Message)
		}
	}
	if len(lines) > 0 {
		return strings.Join(lines, "\n")
	}
	if problem.Detail != "" {
		return problem.Detail
	}
	return status
}

// request is a request to play a synch tournament.
type request struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
	Venue  *struct {
		ID   int64     `json:"id"`
		Town *siteTown `json:"town"`
	} `json:"venue"`
	Representative *person `json:"representative"`
}

type person struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Surname string `json:"surname"`
}

func (p *person) fullName() string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(p.Surname + " " + p.Name)
}

// requestsPage is more than any tournament has requests.
const requestsPage = 1000

func (s Site) requests(ctx context.Context, tournament int64) ([]request, error) {
	var out []request
	err := s.do(ctx, http.MethodGet, fmt.Sprintf("/tournaments/%d/requests?itemsPerPage=%d", tournament, requestsPage), nil, &out)
	return out, err
}

// questions is how many questions the tournament has, across its tours.
func (s Site) questions(ctx context.Context, tournament int64) (int, error) {
	var t struct {
		QuestionQty map[string]int `json:"questionQty"`
	}
	if err := s.do(ctx, http.MethodGet, fmt.Sprintf("/tournaments/%d", tournament), nil, &t); err != nil {
		return 0, err
	}
	n := 0
	for _, q := range t.QuestionQty {
		n += q
	}
	return n, nil
}

// entered is a team the tournament's results entry already has, with its request.
type entered struct {
	Team    int64  `json:"team"`
	Name    string `json:"name"`
	Request *int64 `json:"request"`
}

func (s Site) entered(ctx context.Context, tournament int64) ([]entered, error) {
	var entry struct {
		Teams []entered `json:"teams"`
	}
	err := s.do(ctx, http.MethodGet, fmt.Sprintf("/tournaments/%d/results_entry", tournament), nil, &entry)
	return entry.Teams, err
}

// seasonRoster is the rating ids of a team's base roster in the season.
func (s Site) seasonRoster(ctx context.Context, team, season int64) (map[int64]bool, error) {
	var rows []struct {
		PlayerID    int64   `json:"idplayer"`
		DateRemoved *string `json:"dateRemoved"`
	}
	if err := s.do(ctx, http.MethodGet, fmt.Sprintf("/teams/%d/seasons?idseason=%d&itemsPerPage=100", team, season), nil, &rows); err != nil {
		return nil, err
	}
	out := map[int64]bool{}
	for _, row := range rows {
		if row.DateRemoved == nil && row.PlayerID > 0 {
			out[row.PlayerID] = true
		}
	}
	return out, nil
}

// season is the rating site's season running now.
func (s Site) season(ctx context.Context) (int64, error) {
	var seasons []struct {
		ID        int64  `json:"id"`
		DateStart string `json:"dateStart"`
		DateEnd   string `json:"dateEnd"`
	}
	if err := s.do(ctx, http.MethodGet, "/seasons?itemsPerPage=200", nil, &seasons); err != nil {
		return 0, err
	}
	now := time.Now()
	for _, season := range seasons {
		start, err1 := time.Parse(time.RFC3339, season.DateStart)
		end, err2 := time.Parse(time.RFC3339, season.DateEnd)
		if err1 == nil && err2 == nil && !now.Before(start) && now.Before(end) {
			return season.ID, nil
		}
	}
	return 0, fmt.Errorf("no rating season on %s", now.Format(time.DateOnly))
}
