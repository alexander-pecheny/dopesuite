package qyserver

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"qy/qy/domain/contest"
	"qy/qy/storage/store"
	"qy/qy/web/route"
	"qy/qy/web/ui"
)

// The organisers' pages: the contests, the people who run them, and the
// settings and jury of one year.

func adminTrail(steps ...crumb) []crumb {
	return append([]crumb{{"/admin", str.Admin.Page.Title()}}, steps...)
}

func contestHref(year int) string { return "/admin/contest/" + strconv.Itoa(year) }

func (s *server) handleAdmin(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	ctx := r.Context()
	contests, err := store.Contests(ctx, s.db)
	if err != nil {
		return err
	}
	var rows []ui.Item
	for _, c := range contests {
		rows = append(rows, ui.Listrow(ui.Href(contestHref(c.Year)),
			ui.Listtitle(text(str.Admin.Contests.Row(strconv.Itoa(c.Year)))),
			ui.Muted(text(stageName(c.StageAt(s.now()))))))
	}
	contestSection := []ui.Item{subhead(str.Admin.Contests.Title())}
	if len(rows) > 0 {
		contestSection = append(contestSection, ui.List(rows...))
	}
	contestSection = append(contestSection, ui.Form(ui.Method("post"), ui.Action("/admin/contests"),
		ui.Button(ui.Submit(), ui.IconPlus, text(str.Admin.Contests.Create(strconv.Itoa(s.nextContest(contests).Year))))))

	admins, err := s.adminHandles(ctx)
	if err != nil {
		return err
	}
	var adminRows []ui.Item
	for _, h := range admins {
		adminRows = append(adminRows, ui.Listrow(ui.Row(ui.SpaceSM, ui.JustifyBetween, ui.AlignCenter,
			ui.Listtitle(text("@"+h)),
			ui.Form(ui.Method("post"), ui.Action("/admin/admins/remove"),
				ui.Hiddenfield(ui.Name("telegram"), ui.Value(h)),
				ui.Button(ui.Submit(), ui.Ghost, ui.Small(), text(str.Common.Remove()))))))
	}
	adminSection := []ui.Item{subhead(str.Admin.Admins.Title()), hint(str.Admin.Admins.Hint())}
	if len(adminRows) > 0 {
		adminSection = append(adminSection, ui.List(adminRows...))
	}
	adminSection = append(adminSection, ui.Form(ui.Method("post"), ui.Action("/admin/admins"), ui.DirRow, ui.SpaceSM,
		ui.Textfield(ui.Name("telegram"), ui.Placeholder(str.Admin.Admins.Placeholder()), ui.Required(), ui.Grow()),
		ui.Button(ui.Submit(), text(str.Common.Add()))))

	return s.ok(w, r, view{
		title: str.Admin.Page.Title(),
		trail: []crumb{{"", str.Admin.Page.Title()}},
		body: []ui.Item{
			ui.Section(contestSection...),
			ui.Section(subhead(str.Admin.Site.Title()), ui.List(
				ui.Listrow(ui.Href("/admin/pages"), ui.Listtitle(text(str.Admin.Site.Pages()))),
				ui.Listrow(ui.Href("/admin/files"), ui.Listtitle(text(str.Admin.Site.Files()))),
				ui.Listrow(ui.Href("/admin/create_users"), ui.Listtitle(text(str.Admin.CreateUsers.Title()))),
			)),
			ui.Section(adminSection...),
		},
	})
}

func stageName(st contest.Stage) string {
	return [...]string{
		contest.Before:     str.Contest.Stage.Before(),
		contest.Nomination: str.Contest.Stage.Nomination(),
		contest.Review:     str.Contest.Stage.Review(),
		contest.Prelim:     str.Contest.Stage.Prelim(),
		contest.Factcheck:  str.Contest.Stage.Factcheck(),
		contest.Final:      str.Contest.Stage.Final(),
		contest.Counting:   str.Contest.Stage.Counting(),
		contest.Done:       str.Contest.Stage.Done(),
	}[st]
}

// nextContest is the contest the create button would make: the latest one a
// year on, or the first after the scripts.
func (s *server) nextContest(existing []contest.Contest) contest.Contest {
	if len(existing) == 0 {
		return contest.Last2025.Next()
	}
	return existing[0].Next()
}

func (s *server) handleCreateContest(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	var year int
	err := s.withWriteTx(r.Context(), "create-contest", func(ctx context.Context, tx *sql.Tx) error {
		existing, err := store.Contests(ctx, tx)
		if err != nil {
			return err
		}
		next := s.nextContest(existing)
		year = next.Year
		if err := store.SaveContest(ctx, tx, next); err != nil {
			return err
		}
		return seedYearPages(ctx, tx, year, s.stamp())
	})
	if err != nil {
		return err
	}
	return seeOther(w, r, contestHref(year))
}

func (s *server) adminHandles(ctx context.Context) ([]string, error) {
	return store.Collect(ctx, s.db, store.Scalar[string], `select telegram_username from admins order by telegram_username`)
}

func (s *server) handleAddAdmin(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	h := normalHandle(r.FormValue("telegram"))
	if h == "" {
		return userError(str.Admin.Admins.Empty())
	}
	err := s.withWriteTx(r.Context(), "add-admin", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `insert or ignore into admins(telegram_username) values(?)`, h)
		return err
	})
	if err != nil {
		return err
	}
	return seeOther(w, r, "/admin")
}

func (s *server) handleRemoveAdmin(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	h := normalHandle(r.FormValue("telegram"))
	if sc.User.Telegram.Valid && strings.EqualFold(sc.User.Telegram.String, h) {
		return userError(str.Admin.Admins.NotYourself())
	}
	err := s.withWriteTx(r.Context(), "remove-admin", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `delete from admins where telegram_username = ?`, h)
		return err
	})
	if err != nil {
		return err
	}
	return seeOther(w, r, "/admin")
}

// pathContest is the contest named by {year} in the URL.
func (s *server) pathContest(r *http.Request) (contest.Contest, error) {
	year, err := strconv.Atoi(r.PathValue("year"))
	if err != nil {
		return contest.Contest{}, route.NotFound(str.Contest.None())
	}
	c, err := store.Contest(r.Context(), s.db, year)
	if errors.Is(err, store.ErrNoContest) {
		return c, route.NotFound(str.Contest.None())
	}
	return c, err
}

type dateField struct {
	label string
	value *string
}

// dateFields pairs each date of the settings form with its label.
func dateFields(d *contest.Dates) []dateField {
	labels := []string{
		str.Admin.Dates.QuestionsFrom(), str.Admin.Dates.QuestionsTo(),
		str.Admin.Dates.NominationStart(), str.Admin.Dates.NominationEnd(),
		str.Admin.Dates.PrelimStart(), str.Admin.Dates.PrelimEnd(),
		str.Admin.Dates.FactcheckStart(), str.Admin.Dates.FactcheckEnd(),
		str.Admin.Dates.FinalStart(), str.Admin.Dates.FinalEnd(), str.Admin.Dates.Results(),
	}
	out := make([]dateField, len(labels))
	for i, p := range d.Ptrs() {
		out[i] = dateField{labels[i], p}
	}
	return out
}

type numberField struct {
	name, label string
	value       *int
}

// numberFields are the settings that are counts.
func numberFields(c *contest.Contest) []numberField {
	return []numberField{
		{"main_slots", str.Admin.Numbers.MainSlots(), &c.MainSlots},
		{"reserve_slots", str.Admin.Numbers.ReserveSlots(), &c.ReserveSlots},
		{"portion_size", str.Admin.Numbers.PortionSize(), &c.PortionSize},
		{"portion_days", str.Admin.Numbers.PortionDays(), &c.PortionDays},
		{"expert_cutoff", str.Admin.Numbers.ExpertCutoff(), &c.ExpertCutoff},
		{"people_cutoff", str.Admin.Numbers.PeopleCutoff(), &c.PeopleCutoff},
	}
}

func (s *server) handleContestPage(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	c, err := s.pathContest(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	y := strconv.Itoa(c.Year)
	base := contestHref(c.Year)

	settings := []ui.Item{ui.Method("post"), ui.Action(base + "/settings"), ui.DirCol, ui.SpaceSM}
	for i, f := range dateFields(&c.Dates) {
		settings = append(settings, ui.Field(ui.Label(f.label),
			ui.Textfield(ui.Name("date"+strconv.Itoa(i)), ui.Value(*f.value), ui.Placeholder(str.Admin.Dates.Placeholder()), ui.Required())))
	}
	for _, f := range numberFields(&c) {
		settings = append(settings, ui.Field(ui.Label(f.label),
			ui.Textfield(ui.Name(f.name), ui.Value(strconv.Itoa(*f.value)), ui.Inputmode("numeric"), ui.Required())))
	}
	settings = append(settings, ui.Row(ui.Button(ui.Primary, ui.Submit(), text(str.Common.Save()))))

	jury, err := s.juryTable(ctx, c)
	if err != nil {
		return err
	}
	numbering, err := s.numberingSection(ctx, c)
	if err != nil {
		return err
	}
	return s.ok(w, r, view{
		title: str.Admin.Contests.Row(y),
		trail: adminTrail(crumb{"", str.Admin.Contests.Row(y)}),
		body: []ui.Item{
			ui.Section(append([]ui.Item{
				hint(stageName(c.StageAt(s.now()))),
				ui.List(
					ui.Listrow(ui.Href(base+"/questions"), ui.Listtitle(text(str.Admin.Contest.Questions()))),
					ui.Listrow(ui.Href(base+"/stage1"), ui.Listtitle(text(str.Admin.Contest.Stage1()))),
					ui.Listrow(ui.Href(base+"/final"), ui.Listtitle(text(str.Admin.Contest.Final()))),
				),
			}, noticeOf(r)...)...),
			ui.Section(append([]ui.Item{subhead(str.Admin.Numbering.Title())}, numbering...)...),
			ui.Section(append([]ui.Item{subhead(str.Admin.Jury.Title()), hint(str.Admin.Jury.Hint())}, jury...)...),
			ui.Section(subhead(str.Admin.Contest.Settings()), hint(str.Admin.Contest.SettingsHint()), ui.Form(settings...)),
		},
	})
}

func (s *server) handleContestSettings(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	if err := parseForm(w, r); err != nil {
		return err
	}
	c, err := s.pathContest(r)
	if err != nil {
		return err
	}
	for i, f := range dateFields(&c.Dates) {
		v := strings.TrimSpace(r.FormValue("date" + strconv.Itoa(i)))
		if _, ok := contest.Day(v); !ok {
			return userError(str.Admin.Dates.Invalid(f.label))
		}
		*f.value = v
	}
	for _, f := range numberFields(&c) {
		n, err := strconv.Atoi(strings.TrimSpace(r.FormValue(f.name)))
		if err != nil || n < 1 {
			return userError(str.Admin.Numbers.Invalid(f.label))
		}
		*f.value = n
	}
	err = s.withWriteTx(r.Context(), "contest-settings", func(ctx context.Context, tx *sql.Tx) error {
		return store.SaveContest(ctx, tx, c)
	})
	if err != nil {
		return err
	}
	return seeOther(w, r, contestHref(c.Year)+"?done="+doneSaved)
}

func jurorKindName(kind string) string {
	if kind == store.People {
		return str.Admin.Jury.People()
	}
	return str.Admin.Jury.Expert()
}

func (s *server) juryTable(ctx context.Context, c contest.Contest) ([]ui.Item, error) {
	jurors, err := store.Jurors(ctx, s.db, c.Year)
	if err != nil {
		return nil, err
	}
	base := contestHref(c.Year)
	var out []ui.Item
	if len(jurors) > 0 {
		rows := []ui.Item{ui.Scroll(), ui.Trow(ui.Hcell(text(str.Admin.Jury.Name())), ui.Hcell(text(str.Admin.Jury.Telegram())), ui.Hcell(text(str.Admin.Jury.Kind())), ui.Hcell())}
		for _, j := range jurors {
			rows = append(rows, ui.Trow(ui.Cell(text(j.Name)), ui.Cell(text("@"+j.Telegram)), ui.Cell(text(jurorKindName(j.Kind))),
				ui.Cell(ui.Form(ui.Method("post"), ui.Action(base+"/jurors/remove"),
					ui.Hiddenfield(ui.Name("telegram"), ui.Value(j.Telegram)),
					ui.Button(ui.Submit(), ui.Ghost, ui.Small(), text(str.Common.Remove()))))))
		}
		out = append(out, ui.Table(rows...))
	}
	out = append(out, ui.Form(ui.Method("post"), ui.Action(base+"/jurors"), ui.DirRow, ui.SpaceSM,
		ui.Textfield(ui.Name("name"), ui.Placeholder(str.Admin.Jury.Name()), ui.Required(), ui.Grow()),
		ui.Textfield(ui.Name("telegram"), ui.Placeholder(str.Admin.Admins.Placeholder()), ui.Required(), ui.Grow()),
		ui.Selectfield(ui.Name("kind"),
			ui.Option(ui.Value(store.Expert), text(str.Admin.Jury.Expert())),
			ui.Option(ui.Value(store.People), text(str.Admin.Jury.People()))),
		ui.Button(ui.Submit(), text(str.Common.Add()))))
	return out, nil
}

func (s *server) handleAddJuror(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	c, err := s.pathContest(r)
	if err != nil {
		return err
	}
	h, name, kind := normalHandle(r.FormValue("telegram")), strings.TrimSpace(r.FormValue("name")), r.FormValue("kind")
	if h == "" || name == "" || (kind != store.Expert && kind != store.People) {
		return userError(str.Admin.Jury.Incomplete())
	}
	err = s.withWriteTx(r.Context(), "add-juror", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
insert into jurors(year, telegram_username, kind, name) values(?, ?, ?, ?)
on conflict(year, telegram_username) do update set kind = excluded.kind, name = excluded.name`, c.Year, h, kind, name)
		return err
	})
	if err != nil {
		return err
	}
	return seeOther(w, r, contestHref(c.Year))
}

func (s *server) handleRemoveJuror(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	c, err := s.pathContest(r)
	if err != nil {
		return err
	}
	err = s.withWriteTx(r.Context(), "remove-juror", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `delete from jurors where year = ? and telegram_username = ?`, c.Year, normalHandle(r.FormValue("telegram")))
		return err
	})
	if err != nil {
		return err
	}
	return seeOther(w, r, contestHref(c.Year))
}
