package ratingexport

import (
	"context"
	"fmt"
	"net/http"

	dopestrings "dope/i18nstrings"

	corei18n "code.pecheny.me/pecheny/dopesuite/dopecore/i18nstrings"
)

// Report is what the site did with a sent Plan: how many teams it moved, how
// many rosters and results it took, and its remarks.
type Report struct {
	Moved    int      `json:"moved"`
	Rosters  int      `json:"rosters"`
	Results  int      `json:"results"`
	Warnings []string `json:"warnings"`
}

// Send sends the Plan: the moves it found, each venue's rosters under its
// representative, the moves those rosters turned out to need (a
// representative of several venues gets new teams in their first request),
// and then every team's answers at once. It stops at the site's first
// refusal; what went before stays on the site, and sending again repeats it
// harmlessly.
func Send(ctx context.Context, p Plan, site Site) (Report, error) {
	r := Report{Warnings: []string{}}
	if p.Blocked != "" {
		return r, corei18n.User(p.Blocked)
	}
	if err := r.move(ctx, site, p.Tournament, p.Moves); err != nil {
		return r, err
	}
	for _, v := range p.Venues {
		if err := r.rosters(ctx, site, p.Tournament, v); err != nil {
			return r, err
		}
	}
	moves, err := misplaced(ctx, site, &p)
	if err != nil {
		return r, err
	}
	if err := r.move(ctx, site, p.Tournament, moves); err != nil {
		return r, err
	}
	var saved struct {
		Warnings []string `json:"warnings"`
	}
	if err := site.do(ctx, http.MethodPost, fmt.Sprintf("/tournaments/%d/results_entry", p.Tournament), map[string]any{"teams": p.Results}, &saved); err != nil {
		return r, err
	}
	r.Results = len(p.Results)
	r.Warnings = append(r.Warnings, saved.Warnings...)
	return r, nil
}

// move puts each team into its venue's request, a request per request.
func (r *Report) move(ctx context.Context, site Site, tournament int64, moves []Move) error {
	byRequest := map[int64][]int64{}
	var order []int64
	for _, m := range moves {
		if _, ok := byRequest[m.Request]; !ok {
			order = append(order, m.Request)
		}
		byRequest[m.Request] = append(byRequest[m.Request], m.Team)
	}
	for _, request := range order {
		var moved struct {
			Moved    []int64  `json:"moved"`
			Warnings []string `json:"warnings"`
		}
		body := map[string]any{"teams": byRequest[request], "request": request}
		if err := site.do(ctx, http.MethodPost, fmt.Sprintf("/tournaments/%d/results_entry/move", tournament), body, &moved); err != nil {
			return err
		}
		r.Moved += len(moved.Moved)
		r.Warnings = append(r.Warnings, moved.Warnings...)
	}
	return nil
}

// rosters sends one venue's rosters, as its representative when it has one.
func (r *Report) rosters(ctx context.Context, site Site, tournament int64, v Venue) error {
	type row struct {
		Player int64  `json:"player"`
		Flag   string `json:"flag"`
	}
	type team struct {
		Team    int64 `json:"team"`
		Players []row `json:"players"`
	}
	body := struct {
		Representative int64  `json:"representative,omitempty"`
		Teams          []team `json:"teams"`
	}{Representative: v.Representative}
	names := map[int64]string{}
	for _, t := range v.Teams {
		names[t.Team] = t.Name
		rows := make([]row, len(t.Players))
		for i, p := range t.Players {
			rows[i] = row{Player: p.Player, Flag: p.Flag}
		}
		body.Teams = append(body.Teams, team{Team: t.Team, Players: rows})
	}
	var saved struct {
		Warnings []struct {
			Team    int64  `json:"team"`
			Message string `json:"message"`
		} `json:"warnings"`
	}
	if err := site.do(ctx, http.MethodPost, fmt.Sprintf("/tournaments/%d/results_entry/rosters", tournament), body, &saved); err != nil {
		return err
	}
	r.Rosters += len(v.Teams)
	for _, w := range saved.Warnings {
		r.Warnings = append(r.Warnings, dopestrings.Default.Host.RatingExport.TeamRemark(names[w.Team], w.Message))
	}
	return nil
}
