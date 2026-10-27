package ratingexport

import (
	"context"
	"time"

	"dope/dope/domain/multifest"
	dopestrings "dope/i18nstrings"

	"code.pecheny.me/pecheny/dopesuite/dopecore/idstr"
)

// groups is where the fest's teams go on the site: a Venue per venue
// whose request the site accepted, or one Venue of every team on a tournament
// without requests (venue 0).
type groups struct {
	synch   bool
	order   []int64
	byVenue map[int64]*Venue
	// refused is why a venue's teams cannot go, by venue.
	refused map[int64]string
	// town is the site's town of each venue, for a new team there.
	town map[int64]siteTown
}

// requestAccepted is the status of a request the site has accepted.
const requestAccepted = "A"

func (p *Plan) group(ctx context.Context, site Site, kind string, venues []multifest.Venue) (groups, error) {
	s := dopestrings.Default
	g := groups{synch: kind == multifest.Synch, byVenue: map[int64]*Venue{}, refused: map[int64]string{}, town: map[int64]siteTown{}}
	if !g.synch {
		g.order = []int64{0}
		g.byVenue[0] = &Venue{Title: s.Host.RatingExport.AllTeams(), Teams: []Roster{}}
		return g, nil
	}
	requests, err := site.requests(ctx, p.Tournament)
	if err != nil {
		return g, err
	}
	byRatingVenue := map[int64]request{}
	for _, r := range requests {
		if r.Status != requestAccepted || r.Venue == nil {
			continue
		}
		if _, seen := byRatingVenue[r.Venue.ID]; !seen {
			byRatingVenue[r.Venue.ID] = r
		}
	}
	for _, v := range venues {
		g.order = append(g.order, v.ID)
		r, ok := byRatingVenue[v.RatingVenueID]
		switch {
		case !ok || v.RatingVenueID == 0:
			g.refused[v.ID] = s.Host.RatingExport.NoRequest(v.Title)
		case r.Representative == nil:
			g.refused[v.ID] = s.Host.RatingExport.NoRepresentative(v.Title)
		default:
			if r.Venue.Town != nil {
				g.town[v.ID] = *r.Venue.Town
			}
			g.byVenue[v.ID] = &Venue{Title: v.Title, Request: r.ID, Representative: r.Representative.ID,
				RepresentativeName: r.Representative.fullName(), Teams: []Roster{}}
		}
	}
	return g, nil
}

// builder adds the fest's teams to a Plan one by one.
type builder struct {
	plan   *Plan
	site   Site
	groups groups
	season int64
}

func (b *builder) skip(team, reason string) {
	b.plan.Skipped = append(b.plan.Skipped, Skipped{Team: team, Reason: reason})
}

func (b *builder) add(ctx context.Context, team festTeam, answers string) error {
	s := dopestrings.Default
	venue := int64(0)
	if b.groups.synch {
		venue = team.venue
	}
	group, ok := b.groups.byVenue[venue]
	switch {
	case b.groups.refused[venue] != "":
		b.skip(team.name, b.groups.refused[venue])
		return nil
	case !ok:
		b.skip(team.name, s.Host.RatingExport.NoVenue())
		return nil
	}
	if err := b.unknown(ctx, team, venue); err != nil || team.ratingID == 0 {
		return err
	}
	base, err := b.base(ctx, team.ratingID)
	if err != nil {
		return err
	}
	roster := Roster{Team: team.ratingID, Name: team.name, Players: []Player{}}
	for _, player := range team.players {
		if player.ratingID == 0 {
			continue
		}
		flag := flagLegionary
		if base[player.ratingID] {
			flag = flagBase
		}
		roster.Players = append(roster.Players, Player{Player: player.ratingID, Flag: flag, Name: player.name()})
	}
	if len(roster.Players) == 0 {
		b.skip(team.name, s.Host.RatingExport.NoPlayers())
		return nil
	}
	group.Teams = append(group.Teams, roster)
	b.plan.Results = append(b.plan.Results, Result{Team: team.ratingID, Name: team.name, Mask: answers})
	return nil
}

// unknown lists the team, when the site has no id for it in dope, and each
// of its players without one, with their namesakes on the site.
func (b *builder) unknown(ctx context.Context, team festTeam, venue int64) error {
	held := false
	if team.ratingID == 0 {
		candidates, err := b.site.teamNamesakes(ctx, team.name)
		if err != nil {
			return err
		}
		town, err := b.site.town(ctx, team.city)
		if err != nil {
			return err
		}
		if town.ID == 0 {
			town = b.groups.town[venue]
		}
		u := Unknown{Ref: "t" + idstr.Format(team.id), Team: team.id, TeamName: team.name,
			Name: team.name, Candidates: candidates, Town: town.ID, TownName: town.Name}
		held = u.Default() == ""
		b.plan.Unknown = append(b.plan.Unknown, u)
	}
	for _, p := range team.players {
		if p.ratingID != 0 {
			continue
		}
		candidates, err := b.site.playerNamesakes(ctx, p.first, p.last)
		if err != nil {
			return err
		}
		b.plan.Unknown = append(b.plan.Unknown, Unknown{Ref: "p" + idstr.Format(p.id), Team: team.id, TeamName: team.name,
			Player: p.id, FirstName: p.first, LastName: p.last, Name: p.name(), Candidates: candidates, Held: held})
	}
	return nil
}

// base is the team's base roster: buff's when it mirrors the season, else
// the site's.
func (b *builder) base(ctx context.Context, team int64) (map[int64]bool, error) {
	if people := b.site.Buff.BaseRoster(ctx, team, time.Now()); len(people) > 0 {
		out := map[int64]bool{}
		for _, p := range people {
			out[p.ID] = true
		}
		return out, nil
	}
	if b.season == 0 {
		season, err := b.site.season(ctx)
		if err != nil {
			return nil, err
		}
		b.season = season
	}
	return b.site.seasonRoster(ctx, team, b.season)
}

// planMoves lists the teams the tournament already has in another request
// than their venue's.
func (b *builder) planMoves(ctx context.Context) error {
	moves, err := misplaced(ctx, b.site, b.plan)
	b.plan.Moves = append(b.plan.Moves, moves...)
	return err
}

func misplaced(ctx context.Context, site Site, p *Plan) ([]Move, error) {
	have, err := site.entered(ctx, p.Tournament)
	if err != nil {
		return nil, err
	}
	want := map[int64]Move{}
	for _, v := range p.Venues {
		if v.Request == 0 {
			continue
		}
		for _, t := range v.Teams {
			want[t.Team] = Move{Team: t.Team, Name: t.Name, Request: v.Request, Venue: v.Title}
		}
	}
	moves := []Move{}
	for _, t := range have {
		if m, ok := want[t.Team]; ok && (t.Request == nil || *t.Request != m.Request) {
			moves = append(moves, m)
		}
	}
	return moves, nil
}
