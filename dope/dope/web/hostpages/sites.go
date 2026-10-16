package hostpages

import (
	"context"
	"database/sql"
	"strconv"

	"dope/dope/domain/core"
	"dope/dope/domain/festops"
	"dope/dope/domain/gamebuild"
	"dope/dope/domain/games"
	"dope/dope/domain/imports"
	"dope/dope/domain/sites"
	"dope/dope/storage/store"
	dopeui "dope/dope/web/ui"
	dopestrings "dope/i18nstrings"
)

// kindField is the fest type's select, with current chosen.
func kindField(current string) *dopeui.Element {
	s := dopestrings.Default
	option := func(kind, label string) *dopeui.Element {
		items := []dopeui.Item{dopeui.Value(kind), dopeui.Text(label)}
		if kind == current {
			items = append(items, dopeui.Selected())
		}
		return dopeui.Option(items...)
	}
	return dopeui.Field(dopeui.Label(s.Host.Sites.KindLabel()),
		dopeui.Selectfield(dopeui.Name("kind"),
			option(sites.Regular, s.Host.Sites.KindRegular()),
			option(sites.Synch, s.Host.Sites.KindSynch()),
			option(sites.Multi, s.Host.Sites.KindMulti())),
		dopeui.Hint(dopeui.Text(s.Host.Sites.KindHint())))
}

// ensureSiteGames gives every site of a synch fest that has no Game yet an
// OD of the tournament's tours (ADR-0031), on every import, so a site
// whose Game the host deleted gets a new one.
func (s *Server) ensureSiteGames(ctx context.Context, festID, ratingID int64) error {
	type bare struct {
		id    int64
		title string
	}
	db := s.h.Engine().DB
	missing, err := store.CollectRows(ctx, db, `
select s.id, s.title from fest_sites s
where s.fest_id = ? and not exists (select 1 from games g where g.site_id = s.id)
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
	for _, site := range missing {
		spec := gamebuild.Spec{FestID: festID, Type: games.OD, Label: site.title, TourComp: tours, SiteID: site.id}
		if _, err := s.commit(ctx, festID, "site-game", nil, func(ctx context.Context, tx *sql.Tx) (core.FestWrite, error) {
			_, written, err := festops.CreateGameTx(ctx, tx, spec)
			return written, err
		}); err != nil {
			return err
		}
	}
	return nil
}

// siteRow is one site on the dashboard: how many teams play there and
// its Games.
type siteRow struct {
	sites.Site
	Teams int
	Games []PublicFestGame
}

func (s *Server) loadSiteRows(ctx context.Context, festID int64, festGames []PublicFestGame) ([]siteRow, error) {
	db := s.h.Engine().DB
	list, err := sites.List(ctx, db, festID)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	counts := map[int64]int{}
	rows, err := db.QueryContext(ctx, `
select site_id, count(*) from fest_teams where fest_id = ? and deleted = 0 and site_id is not null group by site_id`, festID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var site int64
		var n int
		if err := rows.Scan(&site, &n); err != nil {
			return nil, err
		}
		counts[site] = n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]siteRow, len(list))
	for i, site := range list {
		out[i] = siteRow{Site: site, Teams: counts[site.ID]}
		for _, g := range festGames {
			if g.SiteID == site.ID {
				out[i].Games = append(out[i].Games, g)
			}
		}
	}
	return out, nil
}

// hostDashSitesSection lists the sites, each with its team count and the
// Games played there.
func hostDashSitesSection(data hostFestDashData, ref string) *dopeui.Element {
	s := dopestrings.Default
	rows := make([]dopeui.Item, 0, len(data.Sites))
	for _, site := range data.Sites {
		facts := s.Host.Sites.Teams(strconv.Itoa(site.Teams))
		if site.Town != "" {
			facts = site.Town + " · " + facts
		}
		head := dopeui.Col(dopeui.Listtitle(dopeui.Text(site.Title)), dopeui.Muted(dopeui.Text(facts)))
		if len(site.Games) == 0 {
			rows = append(rows, dopeui.Listrow(head))
			continue
		}
		for _, g := range site.Games {
			rows = append(rows, hostDashGameRow(g, ref, data.CanManageGames))
		}
	}
	return dopeui.Section(dopeui.ID("sites"), dopeui.Subhead(dopeui.Text(s.Host.Sites.Subhead())), dopeui.Actionlist(rows...))
}
