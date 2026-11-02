package ratingexport

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"dope/dope/domain/multifest"
	"dope/dope/domain/ratingsync"
	"dope/dope/domain/roster"
	"dope/dope/storage/ratingsite"
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
	town map[int64]ratingsite.Town
}

func (p *Plan) group(ctx context.Context, site Site, kind string, venues []multifest.Venue) (groups, error) {
	s := dopestrings.Default
	g := groups{synch: kind == multifest.Synch, byVenue: map[int64]*Venue{}, refused: map[int64]string{}, town: map[int64]ratingsite.Town{}}
	if !g.synch {
		g.order = []int64{0}
		g.byVenue[0] = &Venue{Title: s.Host.RatingExport.AllTeams(), Teams: []Roster{}}
		return g, nil
	}
	requests, err := site.Requests(ctx, p.Tournament)
	if err != nil {
		return g, err
	}
	byRequest, byRatingVenue := map[int64]ratingsite.Request{}, map[int64]ratingsite.Request{}
	for _, r := range requests {
		if r.Status != ratingsite.RequestAccepted || r.Venue == nil {
			continue
		}
		byRequest[r.ID] = r
		if _, seen := byRatingVenue[r.Venue.ID]; !seen {
			byRatingVenue[r.Venue.ID] = r
		}
	}
	for _, v := range venues {
		g.order = append(g.order, v.ID)
		r, ok := byRequest[v.RatingRequestID]
		if v.RatingRequestID == 0 {
			r, ok = byRatingVenue[v.RatingVenueID]
		}
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
				RepresentativeName: r.Representative.FullName(), Teams: []Roster{}}
		}
	}
	return g, nil
}

// builder adds the fest's teams to a Plan one by one, comparing each with
// the site.
type builder struct {
	plan    *Plan
	site    Site
	groups  groups
	choices map[string]string
	season  int64
	// agreed is the fest's base; onSite is every team the tournament has.
	agreed map[ratingsync.Key]string
	onSite map[int64]siteTeam
	// flagIDs is the site's id of each зачёт by its short name, read when a
	// team's зачёты first go; 0 for a short name several flags share.
	flagIDs map[string]int64
}

// siteTeam is a team as the tournament has it.
type siteTeam struct {
	members []ratingsite.Member
	flags   []string
	mask    string
}

// read loads what the comparison needs: the base, and the tournament's
// rosters, зачёты and answers.
func (b *builder) read(ctx context.Context, db *sql.DB, festID int64) error {
	agreed, err := ratingsync.Load(ctx, db, festID)
	if err != nil {
		return err
	}
	results, err := b.site.Results(ctx, b.plan.Tournament)
	if err != nil {
		return err
	}
	entry, err := b.site.Entry(ctx, b.plan.Tournament)
	if err != nil {
		return err
	}
	b.agreed, b.onSite = agreed, map[int64]siteTeam{}
	for _, r := range results {
		t := siteTeam{members: r.TeamMembers}
		for _, f := range r.Flags {
			t.flags = append(t.flags, f.ShortName)
		}
		b.onSite[r.Team.ID] = t
	}
	for _, e := range entry {
		t := b.onSite[e.Team]
		if e.Mask != nil {
			t.mask = *e.Mask
		}
		b.onSite[e.Team] = t
	}
	return nil
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
	group.Members = append(group.Members, team.ratingID)
	site, onSite := b.onSite[team.ratingID]
	rosterTeam, err := b.roster(ctx, team, site)
	if err != nil {
		return err
	}
	if b.compareRoster(team, rosterTeam, site, onSite) == ratingsync.Send {
		group.Teams = append(group.Teams, rosterTeam)
	}
	if err := b.compareFlags(ctx, team, site, onSite); err != nil {
		return err
	}
	dope, other := ratingsync.AnswersBase(team.ratingID, answers), ratingsync.AnswersBase(team.ratingID, site.mask)
	if b.decide(team, dope, other, onSite, answers, site.mask) == ratingsync.Send {
		b.plan.Results = append(b.plan.Results, Result{Team: team.ratingID, Name: team.name, Mask: answers})
	}
	return nil
}

// roster is the team's roster as dope would send it: its players the site
// knows, flagged by the base roster, with the captain the site marks.
func (b *builder) roster(ctx context.Context, team festTeam, site siteTeam) (Roster, error) {
	base, err := b.baseRoster(ctx, team.ratingID)
	if err != nil {
		return Roster{}, err
	}
	marked := map[int64]ratingsite.Member{}
	for _, m := range site.members {
		marked[m.Player.ID] = m
	}
	out := Roster{Team: team.ratingID, Name: team.name, Players: []Player{}}
	for _, player := range team.players {
		if player.ratingID == 0 {
			continue
		}
		flag := flagLegionary
		if base[player.ratingID] {
			flag = flagBase
		}
		m := marked[player.ratingID]
		if m.Flag == flagCaptain {
			flag = flagCaptain
		}
		out.Players = append(out.Players, Player{Player: player.ratingID, Flag: flag, Captain: m.Captain, Name: player.name()})
	}
	return out, nil
}

func (b *builder) compareRoster(team festTeam, dope Roster, site siteTeam, onSite bool) ratingsync.Action {
	var dopeIDs, siteIDs []int64
	var dopeNames, siteNames []string
	for _, p := range dope.Players {
		dopeIDs, dopeNames = append(dopeIDs, p.Player), append(dopeNames, p.Name)
	}
	var take []roster.FestRosterImportPlayer
	for _, m := range site.members {
		siteIDs, siteNames = append(siteIDs, m.Player.ID), append(siteNames, m.Player.FullName())
		take = append(take, roster.FestRosterImportPlayer{RatingID: m.Player.ID, FirstName: m.Player.Name, LastName: m.Player.Surname})
	}
	return b.decide(team, ratingsync.RosterBase(team.ratingID, dopeIDs), ratingsync.RosterBase(team.ratingID, siteIDs), onSite,
		strings.Join(dopeNames, ", "), strings.Join(siteNames, ", "), func(f *Fact) { f.Players = take })
}

func (b *builder) compareFlags(ctx context.Context, team festTeam, site siteTeam, onSite bool) error {
	shorts := roster.FlagShortNames(team.flags)
	dope, other := ratingsync.FlagsBase(team.ratingID, shorts), ratingsync.FlagsBase(team.ratingID, site.flags)
	if b.decide(team, dope, other, onSite, strings.Join(shorts, ", "), strings.Join(site.flags, ", "),
		func(f *Fact) { f.Flags = site.flags }) != ratingsync.Send {
		return nil
	}
	if err := b.loadFlagIDs(ctx); err != nil {
		return err
	}
	out := TeamFlags{Team: team.ratingID, Name: team.name, Shorts: []string{}, IDs: []int64{}}
	for _, f := range team.flags {
		id := f.RatingID
		if id == 0 {
			id = b.flagIDs[f.Short]
		}
		if id == 0 {
			b.skip(team.name, dopestrings.Default.Host.RatingExport.FlagUnknown(f.Short))
			continue
		}
		out.Shorts, out.IDs = append(out.Shorts, f.Short), append(out.IDs, id)
	}
	b.plan.Flags = append(b.plan.Flags, out)
	return nil
}

func (b *builder) loadFlagIDs(ctx context.Context) error {
	if b.flagIDs != nil {
		return nil
	}
	flags, err := b.site.TeamFlags(ctx)
	if err != nil {
		return err
	}
	b.flagIDs = map[string]int64{}
	for _, f := range flags {
		short := strings.TrimSpace(f.ShortName)
		if _, seen := b.flagIDs[short]; seen {
			b.flagIDs[short] = 0
		} else {
			b.flagIDs[short] = f.ID
		}
	}
	return nil
}

// decide compares a fact of the team, under the person's choice when both
// sides changed, and puts it on the plan unless the two agree. take fills in
// the site's version for dope to take.
func (b *builder) decide(team festTeam, dope, site ratingsync.Base, onSite bool, dopeText, siteText string, take ...func(*Fact)) ratingsync.Action {
	base, hasBase := b.agreed[dope.Key]
	action := ratingsync.Decide(dope.Fact, dope.Value, site.Value, onSite, base, hasBase)
	if action == ratingsync.Same {
		if onSite && base != dope.Value {
			b.plan.Agreed = append(b.plan.Agreed, dope)
		}
		return action
	}
	f := Fact{Key: fmt.Sprintf("%d.%s", team.ratingID, dope.Fact), Team: team.ratingID, TeamID: team.id, Name: team.name,
		Fact: dope.Fact, Dope: dopeText, Site: siteText}
	if action == ratingsync.Ask {
		switch b.choices[f.Key] {
		case choiceDope:
			action, f.Chosen = ratingsync.Send, true
		case choiceSite:
			action, f.Chosen = ratingsync.Take, true
		default:
			b.plan.Undecided++
		}
	}
	f.Action = action
	if action == ratingsync.Take {
		// dope cannot take the site's answers into its sheet, so it agrees
		// with them as its own answers stand; a roster or зачёты it takes.
		f.Base = site
		if dope.Fact == ratingsync.Answers {
			f.Base = dope
		}
		for _, fill := range take {
			fill(&f)
		}
	}
	b.plan.Facts = append(b.plan.Facts, f)
	return action
}

// The sides a person picks for a fact both changed.
const (
	choiceDope = "dope"
	choiceSite = "site"
)

// baseRoster is the team's base roster: buff's when it mirrors the season,
// else the site's.
func (b *builder) baseRoster(ctx context.Context, team int64) (map[int64]bool, error) {
	if people := b.site.Buff.BaseRoster(ctx, team, time.Now()); len(people) > 0 {
		out := map[int64]bool{}
		for _, p := range people {
			out[p.ID] = true
		}
		return out, nil
	}
	if b.season == 0 {
		season, err := b.site.Season(ctx, time.Now())
		if err != nil {
			return nil, err
		}
		b.season = season
	}
	ids, err := b.site.BaseRoster(ctx, team, b.season)
	out := map[int64]bool{}
	for _, id := range ids {
		out[id] = true
	}
	return out, err
}

// planMoves lists the teams the tournament already has in another request
// than their venue's.
func (b *builder) planMoves(ctx context.Context) error {
	moves, err := misplaced(ctx, b.site, b.plan)
	b.plan.Moves = append(b.plan.Moves, moves...)
	return err
}

func misplaced(ctx context.Context, site Site, p *Plan) ([]Move, error) {
	have, err := site.Entry(ctx, p.Tournament)
	if err != nil {
		return nil, err
	}
	want := map[int64]Move{}
	for _, v := range p.Venues {
		if v.Request == 0 {
			continue
		}
		for _, t := range v.Members {
			want[t] = Move{Team: t, Request: v.Request, Venue: v.Title}
		}
	}
	moves := []Move{}
	for _, t := range have {
		if m, ok := want[t.Team]; ok && (t.Request == nil || *t.Request != m.Request) {
			m.Name = t.Name
			moves = append(moves, m)
		}
	}
	return moves, nil
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
