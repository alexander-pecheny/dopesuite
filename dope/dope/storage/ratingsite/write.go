package ratingsite

import (
	"context"
	"fmt"
)

// The writes go through the tournament's «ввод». Each needs a token with the
// right to write, and each replaces the whole of what it names for every
// team it sends.

type RosterPlayer struct {
	Player  int64  `json:"player"`
	Flag    string `json:"flag"`
	Captain bool   `json:"captain,omitempty"`
}

type RosterTeam struct {
	Team    int64          `json:"team"`
	Players []RosterPlayer `json:"players"`
}

// TeamRemark is the site's remark on one team of a save.
type TeamRemark struct {
	Team    int64  `json:"team"`
	Message string `json:"message"`
}

// SaveRosters sets each team's roster on the tournament, as the
// representative when it is not 0. A team the tournament lacks joins it, in
// the representative's request.
func (c Client) SaveRosters(ctx context.Context, tournament, representative int64, teams []RosterTeam) ([]TeamRemark, error) {
	body := struct {
		Representative int64        `json:"representative,omitempty"`
		Teams          []RosterTeam `json:"teams"`
	}{representative, teams}
	var saved struct {
		Warnings []TeamRemark `json:"warnings"`
	}
	err := c.post(ctx, fmt.Sprintf("/tournaments/%d/results_entry/rosters", tournament), body, &saved)
	return saved.Warnings, err
}

// TeamFlags is a team's зачёты on the tournament, by the site's ids.
type TeamFlags struct {
	Team  int64   `json:"team"`
	Flags []int64 `json:"flags"`
}

// SaveFlags sets each team's зачёты.
func (c Client) SaveFlags(ctx context.Context, tournament int64, teams []TeamFlags) error {
	return c.post(ctx, fmt.Sprintf("/tournaments/%d/results_entry/flags", tournament), map[string]any{"teams": teams}, nil)
}

// TeamAnswers is a team's answers, a character per question of the
// tournament: 1 taken, 0 not.
type TeamAnswers struct {
	Team int64  `json:"team"`
	Mask string `json:"mask"`
}

// SaveAnswers sets each team's answers.
func (c Client) SaveAnswers(ctx context.Context, tournament int64, teams []TeamAnswers) ([]string, error) {
	var saved struct {
		Warnings []string `json:"warnings"`
	}
	err := c.post(ctx, fmt.Sprintf("/tournaments/%d/results_entry", tournament), map[string]any{"teams": teams}, &saved)
	return saved.Warnings, err
}

// Move puts teams the tournament has into the request, and says how many it
// moved.
func (c Client) Move(ctx context.Context, tournament, request int64, teams []int64) (int, []string, error) {
	var moved struct {
		Moved    []int64  `json:"moved"`
		Warnings []string `json:"warnings"`
	}
	body := map[string]any{"teams": teams, "request": request}
	err := c.post(ctx, fmt.Sprintf("/tournaments/%d/results_entry/move", tournament), body, &moved)
	return len(moved.Moved), moved.Warnings, err
}

// CreatePlayer makes a player on the site and gives its id.
func (c Client) CreatePlayer(ctx context.Context, tournament int64, surname, name string) (int64, error) {
	var created struct {
		ID int64 `json:"id"`
	}
	err := c.post(ctx, fmt.Sprintf("/tournaments/%d/results_entry/players", tournament), map[string]string{"surname": surname, "name": name}, &created)
	return created.ID, err
}

// CreateTeam makes a team of the town on the site and gives its id.
func (c Client) CreateTeam(ctx context.Context, tournament int64, name string, town int64) (int64, error) {
	var created struct {
		ID int64 `json:"id"`
	}
	err := c.post(ctx, fmt.Sprintf("/tournaments/%d/results_entry/teams", tournament), map[string]any{"name": name, "town": town}, &created)
	return created.ID, err
}
