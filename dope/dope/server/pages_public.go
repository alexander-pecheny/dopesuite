package dopeserver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"sort"
	"time"

	"dope/dope/domain/games"
	"dope/dope/platform/markdown"
	"dope/dope/platform/util"
	"dope/dope/web/hostpages"
	"dope/dope/web/pages"
	dopestrings "dope/i18nstrings"
)

type publicFestSummary struct {
	ID          int64
	Slug        string
	Title       string
	StartDate   string
	EndDate     string
	Dates       string
	Description string
}

func (s publicFestSummary) Ref() string {
	if s.Slug != "" {
		return s.Slug
	}
	return fmt.Sprintf("%d", s.ID)
}

type publicFestDetail struct {
	ID          int64
	Slug        string
	Title       string
	Dates       string
	Description template.HTML
	Games       []hostpages.PublicFestGame
}

func (d publicFestDetail) Ref() string {
	if d.Slug != "" {
		return d.Slug
	}
	return fmt.Sprintf("%d", d.ID)
}

func (s *server) handlePublicIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	summaries, err := s.loadPublicFestSummaries(r.Context())
	if err != nil {
		log.Printf("internal error: %v", err)
		http.Error(w, dopestrings.Default.Server.Error.Internal(), http.StatusInternalServerError)
		return
	}
	groups := groupPublicFests(summaries, time.Now().Format("2006-01-02"))
	out := make([]hostpages.PublicFestGroup, 0, len(groups))
	for _, g := range groups {
		fests := make([]hostpages.PublicFest, 0, len(g.Fests))
		for _, f := range g.Fests {
			fests = append(fests, hostpages.PublicFest{Ref: f.Ref(), Title: f.Title, Dates: f.Dates})
		}
		out = append(out, hostpages.PublicFestGroup{Title: g.Title, Fests: fests})
	}
	pages.RenderDoc(w, s.eng.AssetETags, hostpages.PublicIndexDoc(out))
}

// publicFestGroup is one collapsible bucket on the public index.
type publicFestGroup struct {
	Title string
	Fests []publicFestSummary
}

// groupPublicFests partitions fests into the current/future/past buckets
// (the Server.PublicFests headings) relative to today ("YYYY-MM-DD"), sorts
// each bucket by start date descending (then title ascending), and drops
// empty buckets. Fests without an effective start date land in the past.
func groupPublicFests(fests []publicFestSummary, today string) []publicFestGroup {
	var current, future, past []publicFestSummary
	for _, f := range fests {
		switch util.ClassifyFestDate(f.StartDate, f.EndDate, today) {
		case util.FestCurrent:
			current = append(current, f)
		case util.FestFuture:
			future = append(future, f)
		default:
			past = append(past, f)
		}
	}
	sortPublicFests(current)
	sortPublicFests(future)
	sortPublicFests(past)
	groups := make([]publicFestGroup, 0, 3)
	for _, g := range []publicFestGroup{
		{Title: dopestrings.Default.Server.PublicFests.Current(), Fests: current},
		{Title: dopestrings.Default.Server.PublicFests.Future(), Fests: future},
		{Title: dopestrings.Default.Server.PublicFests.Past(), Fests: past},
	} {
		if len(g.Fests) > 0 {
			groups = append(groups, g)
		}
	}
	return groups
}

func sortPublicFests(fests []publicFestSummary) {
	sort.SliceStable(fests, func(i, j int) bool {
		if fests[i].StartDate != fests[j].StartDate {
			return fests[i].StartDate > fests[j].StartDate // descending
		}
		return fests[i].Title < fests[j].Title
	})
}

func (s *server) renderPublicFestPage(w http.ResponseWriter, r *http.Request, id int64) {
	detail, err := s.loadPublicFestDetail(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		log.Printf("internal error: %v", err)
		http.Error(w, dopestrings.Default.Server.Error.Internal(), http.StatusInternalServerError)
		return
	}
	pages.RenderDoc(w, s.eng.AssetETags, hostpages.PublicFestDoc(hostpages.PublicFestDetail{
		Ref:         detail.Ref(),
		Title:       detail.Title,
		Dates:       detail.Dates,
		Description: detail.Description,
		Games:       detail.Games,
	}))
}

func (s *server) loadPublicFestSummaries(ctx context.Context) ([]publicFestSummary, error) {
	if s.eng.DB == nil {
		return nil, nil
	}
	rows, err := s.eng.DB.QueryContext(ctx, `
select id, coalesce(slug, ''), title, coalesce(start_date, ''), coalesce(end_date, '')
from fests
where is_public = 1
order by case when start_date is null or start_date = '' then 1 else 0 end,
         start_date,
         id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []publicFestSummary
	for rows.Next() {
		var s publicFestSummary
		if err := rows.Scan(&s.ID, &s.Slug, &s.Title, &s.StartDate, &s.EndDate); err != nil {
			return nil, err
		}
		s.Dates = util.HumanizeFestDates(s.StartDate, s.EndDate, time.Now().Year())
		out = append(out, s)
	}
	return out, rows.Err()
}

func (s *server) loadPublicFestDetail(ctx context.Context, id int64) (publicFestDetail, error) {
	if s.eng.DB == nil {
		return publicFestDetail{}, sql.ErrNoRows
	}
	var (
		slug        string
		title       string
		description string
		startDate   sql.NullString
		endDate     sql.NullString
		isPublic    int
	)
	if err := s.eng.DB.QueryRowContext(ctx, `
select coalesce(slug, ''), title, description, start_date, end_date, is_public
from fests where id = ?`, id).Scan(&slug, &title, &description, &startDate, &endDate, &isPublic); err != nil {
		return publicFestDetail{}, err
	}
	if isPublic != 1 {
		return publicFestDetail{}, sql.ErrNoRows
	}
	gameRows, err := hostpages.LoadFestGames(ctx, s.eng.DB, id)
	if err != nil {
		return publicFestDetail{}, err
	}
	festRef := slug
	if festRef == "" {
		festRef = fmt.Sprintf("%d", id)
	}
	publicGames := make([]hostpages.PublicFestGame, len(gameRows))
	for i, g := range gameRows {
		publicGames[i] = hostpages.PublicFestGame{
			ID:    g.ID,
			Slug:  g.Slug,
			Code:  g.Code,
			Title: g.Title,
			Type:  games.Label(g.Type),
			URL:   fmt.Sprintf("/fest/%s/game/%s/", festRef, g.Ref()),
		}
	}
	detail := publicFestDetail{
		ID:          id,
		Slug:        slug,
		Title:       title,
		Dates:       util.FormatFestDates(startDate.String, endDate.String),
		Description: markdown.Render(description),
		Games:       publicGames,
	}
	return detail, nil
}
