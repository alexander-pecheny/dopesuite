package hostpages

import (
	"html/template"
	"strconv"
	"strings"

	"dope/dope/domain/venues"
	"dope/dope/web/pages"
	ui "dope/dope/web/ui"
)

type VenueRow struct {
	Ref           string
	Title         string
	City          string
	NextSlot      string
	Registration  string
	Accepted      int
	RatingVenueID int64
}

type SlotRow struct {
	Date         string
	Tournament   string
	Registration string
	RegHref      string
	Accepted     int
}

type VenueDetail struct {
	Ref           string
	Title         string
	City          string
	RatingVenueID int64
	Description   template.HTML
	Upcoming      []SlotRow
	Past          []SlotRow
}

type RegPage struct {
	Token       string
	VenueTitle  string
	VenueRef    string
	City        string
	Date        string
	Tournament  string
	State       venues.RegState
	OpensAt     string
	LoggedIn    bool
	LoginHref   string
	Application *ApplicationView
	GameHref    string
	Error       string
	Notice      string
}

type ApplicationView struct {
	Status       string
	StatusLabel  string
	TeamName     string
	RatingTeamID int64
	Roster       []venues.RosterPlayer
	Flags        []string
	Number       int64
}

func joinDots(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, " · ")
}

func ratingVenueLink(id int64) ui.Item {
	return ui.Link(ui.Href("https://rating.chgk.info/venues/"+strconv.FormatInt(id, 10)), ui.Newtab(),
		ui.Text("рейтинг"))
}

func VenuesIndexDoc(rows []VenueRow) *ui.Doc {
	page := []ui.Item{ui.Title("Площадки"), ui.PagePublic, ui.Classicscripts("dist/pageforms.js")}
	page = append(page, jumpHostNav("/host", "Режим организатора", "Перейти в режим организатора")...)
	page = append(page, ui.Publictopbar(pages.Trail([]ui.Item{pages.HomeCrumb()}, "Площадки")))
	page = append(page, PublicTabs("/venues"))
	if len(rows) == 0 {
		page = append(page, ui.Empty(ui.Text("Публичных площадок пока нет.")))
		return &ui.Doc{Nodes: []ui.Node{ui.Page(page...)}}
	}
	page = append(page, ui.Field(ui.Label("Поиск"),
		ui.Textfield(ui.Data("filter-rows", "venues"), ui.Placeholder("город, название…"), ui.Autocomplete("off"))))

	table := []ui.Item{ui.ID("venues"), ui.Scroll(), ui.Trow(
		ui.Hcell(ui.Text("Площадка")), ui.Hcell(ui.Text("Город")), ui.Hcell(ui.Text("Ближайший слот")),
		ui.Hcell(ui.Text("Регистрация")), ui.Hcell(ui.Text("Команд")), ui.Hcell(ui.Text("Рейтинг")),
	)}
	for _, v := range rows {
		rating := ui.Cell(ui.Text(""))
		if v.RatingVenueID > 0 {
			rating = ui.Cell(ratingVenueLink(v.RatingVenueID))
		}
		table = append(table, ui.Trow(
			ui.Cell(ui.Link(ui.Href("/venue/"+v.Ref), ui.Text(v.Title))),
			ui.Cell(ui.Text(v.City)),
			ui.Cell(ui.Text(v.NextSlot)),
			ui.Cell(ui.Text(v.Registration)),
			ui.Cell(ui.Text(strconv.Itoa(v.Accepted))),
			rating,
		))
	}
	page = append(page, ui.Table(table...))
	return &ui.Doc{Nodes: []ui.Node{ui.Page(page...)}}
}

func slotTable(title string, rows []SlotRow) *ui.Element {
	table := []ui.Item{ui.Scroll(), ui.Trow(
		ui.Hcell(ui.Text("Когда")), ui.Hcell(ui.Text("Турнир")),
		ui.Hcell(ui.Text("Регистрация")), ui.Hcell(ui.Text("Команд")),
	)}
	for _, s := range rows {
		reg := ui.Cell(ui.Text(s.Registration))
		if s.RegHref != "" {
			reg = ui.Cell(ui.Link(ui.Href(s.RegHref), ui.Text(s.Registration)))
		}
		table = append(table, ui.Trow(
			ui.Cell(ui.Text(s.Date)), ui.Cell(ui.Text(s.Tournament)), reg,
			ui.Cell(ui.Text(strconv.Itoa(s.Accepted))),
		))
	}
	return ui.Section(ui.Subhead(ui.Text(title)), ui.Table(table...))
}

func VenueDoc(d VenueDetail) *ui.Doc {
	page := []ui.Item{ui.Title(d.Title), ui.PagePublic}
	page = append(page, jumpHostNav("/host/venue/"+d.Ref, "Режим организатора", "Открыть в режиме организатора")...)
	page = append(page, ui.Publictopbar(ui.Crumbs(
		pages.HomeCrumb(), ui.Crumb(ui.Href("/venues"), ui.Text("Площадки")), pages.Leaf(d.Title))))
	head := []ui.Item{ui.SpaceSM, ui.AlignCenter, ui.Wrap()}
	if d.City != "" {
		head = append(head, ui.Muted(ui.Text(d.City)))
	}
	if d.RatingVenueID > 0 {
		head = append(head, ratingVenueLink(d.RatingVenueID))
	}
	if len(head) > 3 {
		page = append(page, ui.Row(head...))
	}
	if d.Description != "" {
		page = append(page, ui.Richtext(ui.Raw(string(d.Description))))
	}
	if len(d.Upcoming) == 0 && len(d.Past) == 0 {
		page = append(page, ui.Empty(ui.Text("Слотов пока нет.")))
	}
	if len(d.Upcoming) > 0 {
		page = append(page, slotTable("Ближайшие слоты", d.Upcoming))
	}
	if len(d.Past) > 0 {
		page = append(page, slotTable("Прошедшие слоты", d.Past))
	}
	return &ui.Doc{Nodes: []ui.Node{ui.Page(page...)}}
}

// rosterFlagsTable shows a Состав as it was saved, flags and all: they are
// derived on save, so the form never offers them.
func rosterFlagsTable(players []venues.RosterPlayer, flags []string) *ui.Element {
	table := []ui.Item{ui.Scroll(), ui.Trow(ui.Hcell(ui.Text("Игрок")), ui.Hcell(ui.Text("ID")), ui.Hcell(ui.Text("Флаг")))}
	for i, p := range players {
		flag := ""
		if i < len(flags) {
			flag = flags[i]
		}
		id := ""
		if p.PlayerID > 0 {
			id = strconv.FormatInt(p.PlayerID, 10)
		}
		table = append(table, ui.Trow(ui.Cell(ui.Text(p.FullName())), ui.Cell(ui.Text(id)), ui.Cell(ui.Text(flag))))
	}
	return ui.Table(table...)
}

func rosterEditor(players []venues.RosterPlayer) *ui.Element {
	return ui.Col(ui.SpaceSM, ui.Data("roster-editor", ""),
		ui.Hiddenfield(ui.Data("roster-json", ""), ui.Name("roster_json"), ui.Value(venues.MarshalRoster(players))),
	)
}

// applicationForm is the Заявка as its submitter edits it. The Состав is asked
// for only once the Заявка is accepted — before that a Слот has more applicants
// than seats, and naming six players is work for a team that has one.
func applicationForm(action string, app *ApplicationView, submit string, roster bool) *ui.Element {
	teamName, ratingID := "", ""
	var players []venues.RosterPlayer
	if app != nil {
		teamName = app.TeamName
		if app.RatingTeamID > 0 {
			ratingID = strconv.FormatInt(app.RatingTeamID, 10)
		}
		players = app.Roster
	}
	form := []ui.Item{ui.DirCol, ui.Method("post"), ui.Action(action), ui.Autocomplete("off"),
		ui.Field(ui.Label("Название команды"), ui.Textfield(ui.Name("team_name"), ui.Value(teamName), ui.Required())),
		// The id names the team: roster-editor.js writes what buff answers into
		// the box above rather than printing it under this one.
		ui.Field(ui.Label("ID команды на rating.chgk.info (0 — разовая команда)"),
			ui.Textfield(ui.Name("rating_team_id"), ui.Value(ratingID),
				ui.Inputmode("numeric"), ui.Data("buff-team", ""), ui.Autocomplete("off"))),
	}
	if roster {
		form = append(form, ui.Field(ui.Label("Состав"), rosterEditor(players)))
	}
	form = append(form, ui.Row(ui.Button(ui.Submit(), ui.Text(submit))))
	return ui.Form(form...)
}

var statusLabels = map[string]string{
	venues.StatusPending:  "на рассмотрении",
	venues.StatusAccepted: "принята",
	venues.StatusDeclined: "отклонена",
}

func StatusLabel(status string) string {
	if label, ok := statusLabels[status]; ok {
		return label
	}
	return status
}

func RegDoc(p RegPage) *ui.Doc {
	page := []ui.Item{ui.Title("Регистрация · " + p.VenueTitle), ui.PagePublic,
		ui.Classicscripts("dist/pageforms.js dist/roster-editor.js")}
	page = append(page, ui.Publictopbar(ui.Crumbs(
		pages.HomeCrumb(), ui.Crumb(ui.Href("/venues"), ui.Text("Площадки")),
		ui.Crumb(ui.Href("/venue/"+p.VenueRef), ui.Text(p.VenueTitle)), pages.Leaf("Регистрация"))))

	page = append(page, ui.Section(
		ui.Subhead(ui.Text(p.VenueTitle)),
		ui.Note(ui.Text(joinDots(p.Date, p.City, p.Tournament))),
	))

	if p.Error != "" {
		page = append(page, ui.Empty(ui.Text(p.Error)))
	}
	if p.Notice != "" {
		page = append(page, ui.Hint(ui.Text(p.Notice)))
	}

	switch {
	case p.State == venues.RegScheduled:
		page = append(page, ui.Empty(ui.Text("Регистрация откроется "+p.OpensAt+".")))
	case !p.LoggedIn:
		page = append(page, ui.Section(
			ui.Hint(ui.Text("Чтобы подать заявку, войдите через Telegram.")),
			ui.Row(ui.Button(ui.Primary, ui.Href(p.LoginHref), ui.Text("Войти"))),
		))
	default:
		page = append(page, applicationSection(p))
	}
	return &ui.Doc{Nodes: []ui.Node{ui.Page(page...)}}
}

func applicationSection(p RegPage) *ui.Element {
	action := "/reg/" + p.Token
	sect := []ui.Item{ui.Subhead(ui.Text("Заявка"))}
	if p.Application != nil {
		number := ""
		if p.Application.Number > 0 {
			number = "номер команды " + strconv.FormatInt(p.Application.Number, 10)
		}
		sect = append(sect, ui.Note(ui.Text(joinDots("Статус: "+p.Application.StatusLabel, number))))
		if p.Application.Status == venues.StatusAccepted && p.GameHref != "" {
			sect = append(sect, ui.Row(ui.Button(ui.Primary, ui.Href(p.GameHref), ui.Text("Страница игры"))))
		}
		if len(p.Application.Roster) > 0 {
			sect = append(sect, rosterFlagsTable(p.Application.Roster, p.Application.Flags))
		}
	}
	if p.State == venues.RegClosed && p.Application == nil {
		sect = append(sect, ui.Empty(ui.Text("Регистрация закрыта.")))
		return ui.Section(sect...)
	}
	submit := "Подать заявку"
	accepted := false
	if p.Application != nil {
		submit = "Сохранить заявку"
		accepted = p.Application.Status == venues.StatusAccepted
	}
	sect = append(sect, applicationForm(action, p.Application, submit, accepted))
	return ui.Section(sect...)
}
