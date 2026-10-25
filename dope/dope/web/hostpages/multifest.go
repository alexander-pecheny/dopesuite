package hostpages

import (
	"context"
	"database/sql"
	"slices"
	"strconv"
	"strings"

	"dope/dope/domain/core"
	"dope/dope/domain/festops"
	"dope/dope/domain/gamebuild"
	"dope/dope/domain/games"
	"dope/dope/domain/imports"
	"dope/dope/domain/multifest"
	"dope/dope/storage/store"
	dopeui "dope/dope/web/ui"
	dopestrings "dope/i18nstrings"
)

// kindField is the fest type's select, with current chosen. A fest with
// Games asks before a new type deletes them.
func kindField(current string, festGames []PublicFestGame) *dopeui.Element {
	s := dopestrings.Default
	option := func(kind, label string) *dopeui.Element {
		items := []dopeui.Item{dopeui.Value(kind), dopeui.Text(label)}
		if kind == current {
			items = append(items, dopeui.Selected())
		}
		return dopeui.Option(items...)
	}
	selectItems := []dopeui.Item{dopeui.Name("kind"),
		option(multifest.Regular, s.Host.Multifest.KindRegular()),
		option(multifest.Synch, s.Host.Multifest.KindSynch()),
		option(multifest.Multi, s.Host.Multifest.KindMulti())}
	field := []dopeui.Item{dopeui.Label(s.Host.Multifest.KindLabel())}
	if len(festGames) > 0 {
		titles := make([]string, len(festGames))
		for i, g := range festGames {
			titles[i] = "«" + g.Title + "»"
		}
		selectItems = append(selectItems, dopeui.Data("confirm-changed", s.Host.Multifest.KindReplaceConfirm(strings.Join(titles, ", "))))
		field = append(field, dopeui.Hiddenfield(dopeui.Name("replace_games"), dopeui.Value("1")))
	}
	field = append(field, dopeui.Selectfield(selectItems...), dopeui.Hint(dopeui.Text(s.Host.Multifest.KindHint())))
	return dopeui.Field(field...)
}

// ensureVenueGames gives every venue of a synch fest that has no Game yet an
// OD of the tournament's tours (ADR-0031), on every import, so a venue
// whose Game the host deleted gets a new one.
func (s *Server) ensureVenueGames(ctx context.Context, festID, ratingID int64) error {
	type bare struct {
		id    int64
		title string
	}
	db := s.h.Engine().DB
	missing, err := store.CollectRows(ctx, db, `
select s.id, s.title from multifest_venues s
where s.fest_id = ? and not exists (select 1 from games g where g.multifest_venue_id = s.id)
order by s.position, s.id`, []any{festID}, func(rows *sql.Rows) (bare, error) {
		var b bare
		return b, rows.Scan(&b.id, &b.title)
	})
	if err != nil || len(missing) == 0 {
		return err
	}
	tours, err := imports.FetchRatingTourComp(ctx, ratingID)
	if err != nil {
		return err
	}
	for _, venue := range missing {
		spec := gamebuild.Spec{FestID: festID, Type: games.OD, Label: venue.title, TourComp: tours, MultiFestVenueID: venue.id}
		if _, err := s.commit(ctx, festID, "venue-game", nil, func(ctx context.Context, tx *sql.Tx) (core.FestWrite, error) {
			_, written, err := festops.CreateGameTx(ctx, tx, spec)
			return written, err
		}); err != nil {
			return err
		}
	}
	return nil
}

// venueRow is one venue on the dashboard: how many teams play there and
// its Games.
type venueRow struct {
	multifest.Venue
	Teams int
	Games []PublicFestGame
}

func (s *Server) loadVenueRows(ctx context.Context, festID int64, festGames []PublicFestGame) ([]venueRow, error) {
	db := s.h.Engine().DB
	list, err := multifest.List(ctx, db, festID)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	counts := map[int64]int{}
	rows, err := db.QueryContext(ctx, `
select multifest_venue_id, count(*) from fest_teams where fest_id = ? and deleted = 0 and multifest_venue_id is not null group by multifest_venue_id`, festID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var venue int64
		var n int
		if err := rows.Scan(&venue, &n); err != nil {
			return nil, err
		}
		counts[venue] = n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]venueRow, len(list))
	for i, venue := range list {
		out[i] = venueRow{Venue: venue, Teams: counts[venue.ID]}
		for _, g := range festGames {
			if g.MultiFestVenueID == venue.ID {
				out[i].Games = append(out[i].Games, g)
			}
		}
	}
	return out, nil
}

// hostDashVenuesSection lists the venues, each with its team count and the
// Games played there.
func hostDashVenuesSection(data hostFestDashData, ref string) *dopeui.Element {
	s := dopestrings.Default
	rows := make([]dopeui.Item, 0, len(data.MultiFestVenues))
	for _, venue := range data.MultiFestVenues {
		facts := s.Host.Multifest.Teams(strconv.Itoa(venue.Teams))
		if venue.Town != "" {
			facts = venue.Town + " · " + facts
		}
		if len(venue.Games) == 0 {
			rows = append(rows, dopeui.Listrow(dopeui.Col(dopeui.Listtitle(dopeui.Text(venue.Title)), dopeui.Muted(dopeui.Text(facts)))))
			continue
		}
		for _, g := range venue.Games {
			rows = append(rows, hostDashGameRow(g, ref, data.CanManageGames, facts))
		}
	}
	return dopeui.Section(dopeui.ID("venues"), dopeui.Subhead(dopeui.Text(s.Host.Multifest.Subhead())), dopeui.Actionlist(rows...))
}

// ownVenueRows keeps the venues a host has a part in: those they edit the
// teams of, and those with a Game they run.
func ownVenueRows(rows []venueRow, edited []int64) []venueRow {
	var out []venueRow
	for _, row := range rows {
		if len(row.Games) > 0 || slices.Contains(edited, row.ID) {
			out = append(out, row)
		}
	}
	return out
}

// hostDashVenueRosterSection is the roster of a host limited to venues: the
// teams and the numbers of their venues.
func hostDashVenueRosterSection(data hostFestDashData, ref string) *dopeui.Element {
	s := dopestrings.Default
	teams := 0
	for _, row := range data.MultiFestVenues {
		if slices.Contains(data.RosterVenues, row.ID) {
			teams += row.Teams
		}
	}
	return dopeui.Section(dopeui.Subhead(dopeui.Text(s.Host.Dash.RosterSubhead())), dopeui.List(
		dopeui.Listrow(dopeui.Href("/host/fest/"+ref+"/teams"), dopeui.Listtitle(dopeui.Text(s.Host.Dash.RosterTeamsLink())), dopeui.Muted(dopeui.Text(strconv.Itoa(teams)))),
		dopeui.Listrow(dopeui.Href("/host/fest/"+ref+"/numbers"), dopeui.Listtitle(dopeui.Text(s.Host.Dash.NumbersLink()))),
	))
}
