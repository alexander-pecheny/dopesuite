package ratingexport

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"dope/dope/domain/games"
	"dope/dope/domain/multifest"
	"dope/dope/storage/store"
	dopestrings "dope/i18nstrings"

	corei18n "code.pecheny.me/pecheny/dopesuite/dopecore/i18nstrings"
	"code.pecheny.me/pecheny/dopesuite/dopecore/idstr"
)

// Plan is what an export sends: each venue's rosters under its
// representative, the teams to move to their venue's request, and every
// team's answers. Skipped and Warnings are for the person to read first.
type Plan struct {
	Tournament int64     `json:"tournament"`
	Questions  int       `json:"questions"`
	Venues     []Venue   `json:"venues"`
	Moves      []Move    `json:"moves"`
	Results    []Result  `json:"results"`
	Skipped    []Skipped `json:"skipped"`
	Warnings   []string  `json:"warnings"`
	// Blocked says why the plan may not be sent; empty when it may.
	Blocked string `json:"blocked,omitempty"`
}

// Venue is one venue's rosters. Request and Representative are 0 on a
// tournament without requests, where the organizer enters every team.
type Venue struct {
	Title              string   `json:"title"`
	Request            int64    `json:"request,omitempty"`
	Representative     int64    `json:"representative,omitempty"`
	RepresentativeName string   `json:"representative_name,omitempty"`
	Teams              []Roster `json:"teams"`
}

// Roster is a team and its players with their flags: the site's letters for
// the base roster and for a legionnaire.
type Roster struct {
	Team    int64    `json:"team"`
	Name    string   `json:"name"`
	Players []Player `json:"players"`
}

type Player struct {
	Player int64  `json:"player"`
	Flag   string `json:"flag"`
	Name   string `json:"name"`
}

// Move puts a team the tournament already has into its venue's request.
type Move struct {
	Team    int64  `json:"team"`
	Name    string `json:"name"`
	Request int64  `json:"request"`
	Venue   string `json:"venue"`
}

// Result is a team's answers, a character per question: 1 taken, 0 not.
type Result struct {
	Team int64  `json:"team"`
	Name string `json:"name"`
	Mask string `json:"mask"`
}

// Skipped is a team the export leaves out, or a player it leaves off a
// roster, and why.
type Skipped struct {
	Team   string `json:"team"`
	Reason string `json:"reason"`
}

// Base and legionnaire, as the site spells a roster flag.
const (
	flagBase      = "\u0411"
	flagLegionary = "\u041b"
)

type festTeam struct {
	id, ratingID, venue, number int64
	name                        string
	players                     []festPlayer
}

type festPlayer struct {
	ratingID int64
	name     string
}

// Build reads what the fest's OD gameID would send and asks the site where
// it would go. It writes nothing anywhere.
func Build(ctx context.Context, db *sql.DB, festID, gameID int64, site Site) (Plan, error) {
	s := dopestrings.Default
	p := Plan{Venues: []Venue{}, Moves: []Move{}, Results: []Result{}, Skipped: []Skipped{}, Warnings: []string{}}
	var kind string
	if err := db.QueryRowContext(ctx, `select coalesce(rating_id, 0), kind from fests where id = ?`, festID).Scan(&p.Tournament, &kind); err != nil {
		return p, err
	}
	if p.Tournament == 0 {
		return p, corei18n.User(s.Host.RatingExport.NoTournament())
	}
	if kind == multifest.Multi {
		return p, corei18n.User(s.Host.RatingExport.MultiFest())
	}
	doc, err := store.LoadGameDoc(ctx, db, festID, gameID)
	if err != nil {
		return p, err
	}
	if games.Get(doc.GameType).Sheets != games.SheetsODRating || doc.MultiFestVenueID != 0 {
		return p, corei18n.User(s.Host.RatingExport.NotFestOd())
	}
	var state games.ODState
	if err := json.Unmarshal([]byte(doc.State), &state); err != nil {
		return p, err
	}
	venues, err := multifest.List(ctx, db, festID)
	if err != nil {
		return p, err
	}
	questions := 0
	for _, n := range games.ParseTourComp(doc.SchemeJSON) {
		questions += n
	}
	p.Warnings = append(p.Warnings, unfinished(state, venues, questions)...)
	state.FlattenVenues()

	if p.Questions, err = site.questions(ctx, p.Tournament); err != nil {
		return p, err
	}
	if p.Questions != questions {
		p.Blocked = s.Host.RatingExport.QuestionsDiffer(strconv.Itoa(questions), strconv.Itoa(p.Questions))
	}
	teams, err := loadTeams(ctx, db, festID)
	if err != nil {
		return p, err
	}
	groups, err := p.group(ctx, site, kind, venues)
	if err != nil {
		return p, err
	}
	b := builder{plan: &p, site: site, groups: groups}
	seated := map[int64]bool{}
	for _, team := range state.Teams {
		seated[team.Number] = true
	}
	for _, team := range teams {
		if team.number > 0 && seated[team.number] {
			if err := b.add(ctx, team, mask(state, team.number, p.Questions)); err != nil {
				return p, err
			}
		}
	}
	for _, v := range groups.order {
		if len(groups.byVenue[v].Teams) > 0 {
			p.Venues = append(p.Venues, *groups.byVenue[v])
		}
	}
	return p, b.planMoves(ctx)
}

// unfinished warns of every sheet that has not completed all its questions.
func unfinished(state games.ODState, venues []multifest.Venue, questions int) []string {
	s := dopestrings.Default
	count := func(completed []bool) int {
		n := 0
		for _, done := range completed {
			if done {
				n++
			}
		}
		return n
	}
	var out []string
	if len(state.Venues) == 0 {
		if n := count(state.Completed); n < questions {
			out = append(out, s.Host.RatingExport.Unfinished(strconv.Itoa(n), strconv.Itoa(questions)))
		}
		return out
	}
	for _, v := range venues {
		if sheet, ok := state.Venues[idstr.Format(v.ID)]; ok {
			if n := count(sheet.Completed); n < questions {
				out = append(out, s.Host.RatingExport.VenueUnfinished(v.Title, strconv.Itoa(n), strconv.Itoa(questions)))
			}
		}
	}
	return out
}

// mask is the team's answers to the tournament's questions.
func mask(state games.ODState, number int64, questions int) string {
	var b strings.Builder
	for q := range questions {
		taken := q < len(state.Entries) && q < len(state.Completed) && state.Completed[q] && slices.Contains(state.Entries[q], number)
		if taken {
			b.WriteByte('1')
		} else {
			b.WriteByte('0')
		}
	}
	return b.String()
}

func loadTeams(ctx context.Context, db *sql.DB, festID int64) ([]festTeam, error) {
	teams, err := store.CollectRows(ctx, db, `
select id, coalesce(rating_id, 0), coalesce(multifest_venue_id, 0), coalesce(number, 0), name
from fest_teams where fest_id = ? and deleted = 0 order by coalesce(number, 0), name`, []any{festID}, func(rows *sql.Rows) (festTeam, error) {
		var t festTeam
		return t, rows.Scan(&t.id, &t.ratingID, &t.venue, &t.number, &t.name)
	})
	if err != nil {
		return nil, err
	}
	type member struct {
		team int64
		festPlayer
	}
	members, err := store.CollectRows(ctx, db, `
select tp.team_id, coalesce(p.rating_id, 0), trim(p.last_name || ' ' || p.first_name)
from fest_team_players tp join fest_players p on p.id = tp.player_id
join fest_teams t on t.id = tp.team_id
where t.fest_id = ? order by tp.team_id, tp.roster_order`, []any{festID}, func(rows *sql.Rows) (member, error) {
		var m member
		return m, rows.Scan(&m.team, &m.ratingID, &m.name)
	})
	if err != nil {
		return nil, err
	}
	index := make(map[int64]int, len(teams))
	for i, t := range teams {
		index[t.id] = i
	}
	for _, m := range members {
		if i, ok := index[m.team]; ok {
			teams[i].players = append(teams[i].players, m.festPlayer)
		}
	}
	return teams, nil
}
