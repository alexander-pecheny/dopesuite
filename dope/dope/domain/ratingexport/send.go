package ratingexport

import (
	"context"

	"dope/dope/storage/ratingsite"
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
	answers := make([]ratingsite.TeamAnswers, len(p.Results))
	for i, res := range p.Results {
		answers[i] = ratingsite.TeamAnswers{Team: res.Team, Mask: res.Mask}
	}
	warnings, err := site.SaveAnswers(ctx, p.Tournament, answers)
	if err != nil {
		return r, err
	}
	r.Results = len(p.Results)
	r.Warnings = append(r.Warnings, warnings...)
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
		moved, warnings, err := site.Move(ctx, tournament, request, byRequest[request])
		if err != nil {
			return err
		}
		r.Moved += moved
		r.Warnings = append(r.Warnings, warnings...)
	}
	return nil
}

// rosters sends one venue's rosters, as its representative when it has one.
func (r *Report) rosters(ctx context.Context, site Site, tournament int64, v Venue) error {
	names := map[int64]string{}
	teams := make([]ratingsite.RosterTeam, 0, len(v.Teams))
	for _, t := range v.Teams {
		names[t.Team] = t.Name
		players := make([]ratingsite.RosterPlayer, len(t.Players))
		for i, p := range t.Players {
			players[i] = ratingsite.RosterPlayer{Player: p.Player, Flag: p.Flag}
		}
		teams = append(teams, ratingsite.RosterTeam{Team: t.Team, Players: players})
	}
	remarks, err := site.SaveRosters(ctx, tournament, v.Representative, teams)
	if err != nil {
		return err
	}
	r.Rosters += len(v.Teams)
	for _, w := range remarks {
		r.Warnings = append(r.Warnings, dopestrings.Default.Host.RatingExport.TeamRemark(names[w.Team], w.Message))
	}
	return nil
}
