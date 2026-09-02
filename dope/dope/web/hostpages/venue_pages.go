package hostpages

import (
	"html/template"
	"strconv"
	"strings"

	"dope/dope/domain/venues"
	"dope/dope/web/pages"
	ui "dope/dope/web/ui"
)

// The public Площадка pages: the /venues index, a Venue's own page and the
// registration link a Representative posts. They are builder pages like the
// two fest ones beside them, and share their vocabulary.

// VenueRow is one Venue on the index.
type VenueRow struct {
	Ref           string
	Title         string
	City          string
	NextSlot      string
	Registration  string
	Accepted      int
	RatingVenueID int64
}

// SlotRow is one Слот on a Venue's page.
type SlotRow struct {
	Date         string
	Tournament   string
	Registration string
	RegHref      string
	Accepted     int
}

// VenueDetail is a Venue's own page.
type VenueDetail struct {
	Ref           string
	Title         string
	City          string
	RatingVenueID int64
	Description   template.HTML
	Upcoming      []SlotRow
	Past          []SlotRow
}

// RegPage is what a registration link shows: always the Слот, then whichever
// of the three states it is in.
type RegPage struct {
	Token        string
	VenueTitle   string
	VenueRef     string
	City         string
	Date         string
	Tournament   string
	State        venues.RegState
	OpensAt      string
	LoggedIn     bool
	LoginHref    string
	Application  *ApplicationView
	SuggestLimit int
	Error        string
	Notice       string
}

// ApplicationView is a Заявка as a page shows it.
type ApplicationView struct {
	Status       string
	StatusLabel  string
	TeamName     string
	RatingTeamID int64
	BuffTeamName string
	Roster       []venues.RosterPlayer
	Flags        []string
	Number       int64
}

// joinDots is the one line a page's subtitle is: what is known, dotted.
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

// VenuesIndexDoc builds /venues: one table, one filter box over it.
func VenuesIndexDoc(rows []VenueRow) *ui.Doc {
	page := []ui.Item{ui.Title("Площадки"), ui.PagePublic, ui.Classicscripts("dist/pageforms.js")}
	page = append(page, ui.Publictopbar(pages.Trail([]ui.Item{pages.HomeCrumb()}, "Площадки")))
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

// VenueDoc builds /venue/{slug}: what the площадка is, and its Слоты.
func VenueDoc(d VenueDetail) *ui.Doc {
	page := []ui.Item{ui.Title(d.Title), ui.PagePublic}
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

// rosterEditor is the Состав editor's mount: the current roster travels in a
// hidden field, and roster-editor.js draws the rows over it.
func rosterEditor(players []venues.RosterPlayer) *ui.Element {
	return ui.Col(ui.SpaceSM, ui.Data("roster-editor", ""),
		ui.Hiddenfield(ui.Data("roster-json", ""), ui.Name("roster_json"), ui.Value(venues.MarshalRoster(players))),
	)
}

// applicationForm is the заявка form, the same one the Representative edits a
// заявка with.
func applicationForm(action string, app *ApplicationView, submit string) *ui.Element {
	teamName, ratingID := "", ""
	var players []venues.RosterPlayer
	if app != nil {
		teamName = app.TeamName
		if app.RatingTeamID > 0 {
			ratingID = strconv.FormatInt(app.RatingTeamID, 10)
		}
		players = app.Roster
	}
	ratingField := []ui.Item{ui.Label("ID команды на rating.chgk.info (0 — разовая команда)")}
	ratingField = append(ratingField, ui.Textfield(ui.Name("rating_team_id"), ui.Value(ratingID),
		ui.Inputmode("numeric"), ui.Data("buff-team", ""), ui.Autocomplete("off")))
	if app != nil && app.BuffTeamName != "" {
		ratingField = append(ratingField, ui.Hint(ui.Text(app.BuffTeamName)))
	}
	return ui.Form(ui.DirCol, ui.Method("post"), ui.Action(action), ui.Autocomplete("off"),
		ui.Field(ui.Label("Название команды"), ui.Textfield(ui.Name("team_name"), ui.Value(teamName), ui.Required())),
		ui.Field(ratingField...),
		ui.Field(ui.Label("Состав"), rosterEditor(players)),
		ui.Row(ui.Button(ui.Submit(), ui.Text(submit))),
	)
}

var statusLabels = map[string]string{
	venues.StatusPending:  "на рассмотрении",
	venues.StatusAccepted: "принята",
	venues.StatusDeclined: "отклонена",
}

// StatusLabel is a Заявка's status in the host's words.
func StatusLabel(status string) string {
	if label, ok := statusLabels[status]; ok {
		return label
	}
	return status
}

// RegDoc builds /reg/{token}.
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
		if len(p.Application.Roster) > 0 {
			sect = append(sect, rosterFlagsTable(p.Application.Roster, p.Application.Flags))
		}
	}
	if p.State == venues.RegClosed && p.Application == nil {
		sect = append(sect, ui.Empty(ui.Text("Регистрация закрыта.")))
		return ui.Section(sect...)
	}
	submit := "Подать заявку"
	if p.Application != nil {
		submit = "Сохранить заявку"
	}
	sect = append(sect, applicationForm(action, p.Application, submit))
	return ui.Section(sect...)
}
