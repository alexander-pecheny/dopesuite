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
	"dope/dope/domain/ratingsync"
	"dope/dope/domain/roster"
	"dope/dope/storage/store"
	dopestrings "dope/i18nstrings"

	corei18n "code.pecheny.me/pecheny/dopesuite/dopecore/i18nstrings"
	"code.pecheny.me/pecheny/dopesuite/dopecore/idstr"
)

// Plan is what an export does (ADR-0036): the facts where dope and the site
// differ, and of them what goes to the site — each venue's rosters under its
// representative, the teams to move to their venue's request, the зачёты and
// the answers — and what dope takes from the site. Skipped and Warnings are
// for the person to read first.
type Plan struct {
	Tournament int64       `json:"tournament"`
	Questions  int         `json:"questions"`
	Facts      []Fact      `json:"facts"`
	Venues     []Venue     `json:"venues"`
	Moves      []Move      `json:"moves"`
	Flags      []TeamFlags `json:"flags"`
	Results    []Result    `json:"results"`
	Unknown    []Unknown   `json:"unknown"`
	Skipped    []Skipped   `json:"skipped"`
	Warnings   []string    `json:"warnings"`
	// Undecided is how many facts wait for a person to pick a side.
	Undecided int `json:"undecided"`
	// Blocked says why the plan may not be sent; empty when it may.
	Blocked string `json:"blocked,omitempty"`
	// Agreed is the facts dope and the site hold alike, for the base.
	Agreed []ratingsync.Base `json:"-"`
}

// Fact is a fact of a team where dope and the site differ, each side's
// version as a person reads it, and what the export does about it.
type Fact struct {
	// Key names the fact in a person's choice.
	Key    string            `json:"key"`
	Team   int64             `json:"team"`
	TeamID int64             `json:"team_id"`
	Name   string            `json:"name"`
	Fact   ratingsync.Fact   `json:"fact"`
	Action ratingsync.Action `json:"action"`
	// Chosen is set when a person picked the side.
	Chosen bool   `json:"chosen,omitempty"`
	Dope   string `json:"dope"`
	Site   string `json:"site"`
	// Base is what the base becomes once a Take is done.
	Base ratingsync.Base `json:"-"`
	// Players and Flags are the site's version, for dope to take.
	Players []roster.FestRosterImportPlayer `json:"-"`
	Flags   []string                        `json:"-"`
}

// TeamFlags is a team's зачёты to send, by short name and by the site's id.
type TeamFlags struct {
	Team   int64    `json:"team"`
	Name   string   `json:"name"`
	Shorts []string `json:"shorts"`
	IDs    []int64  `json:"ids"`
}

// Venue is one venue's rosters to send, and every team it has on the site,
// for the moves. Request and Representative are 0 on a tournament without
// requests, where the organizer enters every team.
type Venue struct {
	Title              string   `json:"title"`
	Request            int64    `json:"request,omitempty"`
	Representative     int64    `json:"representative,omitempty"`
	RepresentativeName string   `json:"representative_name,omitempty"`
	Teams              []Roster `json:"teams"`
	Members            []int64  `json:"members"`
}

// Roster is a team and its players with their flags: the site's letters for
// the base roster and for a legionnaire.
type Roster struct {
	Team    int64    `json:"team"`
	Name    string   `json:"name"`
	Players []Player `json:"players"`
}

// Player keeps the captain the site marks.
type Player struct {
	Player  int64  `json:"player"`
	Flag    string `json:"flag"`
	Captain bool   `json:"captain,omitempty"`
	Name    string `json:"name"`
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

// Base, legionnaire and a captain outside the base roster, as the site
// spells a roster flag.
const (
	flagBase      = "\u0411"
	flagLegionary = "\u041b"
	flagCaptain   = "\u041a"
)

type festTeam struct {
	id, ratingID, venue, number int64
	name, city                  string
	players                     []festPlayer
	flags                       []roster.FestRosterFlag
}

type festPlayer struct {
	id, ratingID int64
	first, last  string
}

func (p festPlayer) name() string { return strings.TrimSpace(p.last + " " + p.first) }

// Build compares the fest's teams and its OD gameID with the site, fact by
// fact, under the person's choices by Fact.Key ("dope" or "site"). It writes
// nothing anywhere.
func Build(ctx context.Context, db *sql.DB, festID, gameID int64, site Site, choices map[string]string) (Plan, error) {
	s := dopestrings.Default
	p := Plan{Facts: []Fact{}, Venues: []Venue{}, Moves: []Move{}, Flags: []TeamFlags{}, Results: []Result{},
		Unknown: []Unknown{}, Skipped: []Skipped{}, Warnings: []string{}}
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
	state.Scored()

	tours, err := site.Tours(ctx, p.Tournament)
	if err != nil {
		return p, err
	}
	for _, n := range tours {
		p.Questions += n
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
	b := builder{plan: &p, site: site, groups: groups, choices: choices}
	if err := b.read(ctx, db, festID); err != nil {
		return p, err
	}
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
	// A venue the site refused has no group: its teams went to Skipped.
	for _, v := range groups.order {
		if group := groups.byVenue[v]; group != nil && len(group.Members) > 0 {
			p.Venues = append(p.Venues, *group)
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
	flags, err := roster.LoadFestTeamFlags(ctx, db, festID)
	if err != nil {
		return nil, err
	}
	teams, err := store.CollectRows(ctx, db, `
select id, coalesce(rating_id, 0), coalesce(multifest_venue_id, 0), coalesce(number, 0), name, coalesce(city, '')
from fest_teams where fest_id = ? and deleted = 0 order by coalesce(number, 0), name`, []any{festID}, func(rows *sql.Rows) (festTeam, error) {
		var t festTeam
		err := rows.Scan(&t.id, &t.ratingID, &t.venue, &t.number, &t.name, &t.city)
		t.flags = flags[t.id]
		return t, err
	})
	if err != nil {
		return nil, err
	}
	type member struct {
		team int64
		festPlayer
	}
	members, err := store.CollectRows(ctx, db, `
select tp.team_id, p.id, coalesce(p.rating_id, 0), p.first_name, p.last_name
from fest_team_players tp join fest_players p on p.id = tp.player_id
join fest_teams t on t.id = tp.team_id
where t.fest_id = ? order by tp.team_id, tp.roster_order`, []any{festID}, func(rows *sql.Rows) (member, error) {
		var m member
		return m, rows.Scan(&m.team, &m.id, &m.ratingID, &m.first, &m.last)
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
