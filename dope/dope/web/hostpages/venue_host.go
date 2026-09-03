package hostpages

import (
	"strconv"
	"strings"
	"time"

	"dope/dope/domain/venues"
	"dope/dope/storage/festaccess"
	"dope/dope/web/pages"
	ui "dope/dope/web/ui"
)

type VenueDashSlot struct {
	ID         int64
	Date       string
	Tournament string
	Accepted   int
	Pending    int
	Href       string
}

type venueDashData struct {
	Venue     venues.Venue
	Slots     []VenueDashSlot
	Access    []festaccess.HostAccessMember
	CanManage bool
	CanDelete bool
	Tz        string
	Error     string
	Notice    string
}

func venueCrumbs(v venues.Venue) []ui.Item {
	return append(pages.HostCrumbs(), ui.Crumb(ui.Href(VenueBase(v)), ui.Text(v.Title)))
}

func venueDashDoc(data venueDashData) *ui.Doc {
	v := data.Venue
	page := []ui.Item{ui.Title(v.Title + " · площадка"), ui.PagePublic,
		ui.Classicscripts("dist/pageforms.js dist/roster-editor.js")}
	if v.IsPublic {
		page = append(page,
			ui.Data("jump-label", "Страница площадки"),
			ui.Data("jump-href", "/venue/"+v.Ref()),
			ui.Data("jump-title", "Открыть страницу площадки"),
			ui.Data("jump-icon", "eye"),
		)
	}
	page = append(page, ui.Publictopbar(pages.Trail(pages.HostCrumbs(), v.Title)))
	if data.Error != "" {
		page = append(page, ui.Empty(ui.Text(data.Error)))
	}
	if data.Notice != "" {
		page = append(page, ui.Hint(ui.Text(data.Notice)))
	}
	if data.CanManage {
		page = append(page, venueSettingsForm(v))
	}
	page = append(page, venueSlotsSection(data))
	if data.CanManage {
		page = append(page, hostDashAccessSection(hostFestDashData{Access: data.Access}, VenueBase(v)))
	}
	if data.CanDelete {
		page = append(page, ui.Section(
			ui.Subhead(ui.Text("Удаление")),
			ui.Form(ui.DirCol, ui.Method("post"), ui.Action(VenueBase(v)+"/delete"), ui.Autocomplete("off"),
				ui.Data("confirm", "Удалить площадку? Все слоты, заявки и результаты будут удалены."),
				ui.Row(ui.Button(ui.Danger, ui.Submit(), ui.Text("Удалить площадку"))),
			),
		))
	}
	return &ui.Doc{Nodes: []ui.Node{ui.Page(page...)}}
}

func venueSettingsForm(v venues.Venue) *ui.Element {
	ratingID := ""
	if v.RatingVenueID > 0 {
		ratingID = strconv.FormatInt(v.RatingVenueID, 10)
	}
	pub := []ui.Item{ui.Name("is_public"), ui.Value("1"), ui.Text("Публичная")}
	if v.IsPublic {
		pub = append(pub, ui.Checked())
	}
	return ui.Form(ui.DirCol, ui.Method("post"), ui.Action(VenueBase(v)), ui.Autocomplete("off"),
		ui.Field(ui.Label("Название"), ui.Textfield(ui.Name("title"), ui.Value(v.Title), ui.Required(), ui.Data("venue-name", ""))),
		ui.Field(ui.Label("Город"), ui.Textfield(ui.Name("city"), ui.Value(v.City), ui.Data("venue-city", ""))),
		ui.Field(ui.Label("Описание (markdown)"), ui.Editor(ui.Name("description"), ui.Rows("6"), ui.Text(v.Description))),
		slugField("Slug (URL вида /venue/{slug})", v.Slug),
		ratingVenueField(ratingID, false),
		ui.Checkbox(pub...),
		ui.Row(ui.Button(ui.Submit(), ui.Text("Сохранить"))),
	)
}

func venueSlotsSection(data venueDashData) *ui.Element {
	ref := VenueBase(data.Venue)
	sect := []ui.Item{ui.Subhead(ui.Text("Слоты"))}
	if len(data.Slots) == 0 {
		sect = append(sect, ui.Empty(ui.Text("Слотов пока нет.")))
	} else {
		rows := make([]ui.Item, 0, len(data.Slots))
		for _, s := range data.Slots {
			title := s.Date
			if title == "" {
				title = "без даты"
			}
			row := []ui.Item{ui.Href(s.Href), ui.Listtitle(ui.Text(title))}
			sub := s.Tournament
			if sub == "" {
				sub = "турнир не выбран"
			}
			sub += " · принято " + strconv.Itoa(s.Accepted)
			if s.Pending > 0 {
				sub += ", ждут " + strconv.Itoa(s.Pending)
			}
			row = append(row, ui.Muted(ui.Text(sub)))
			rows = append(rows, ui.Listrow(row...))
		}
		sect = append(sect, ui.List(rows...))
	}
	if data.CanManage {
		sect = append(sect, ui.Details(
			ui.Summary(ui.Btn(), ui.Text("Новый слот")),
			ui.Form(ui.DirCol, ui.Method("post"), ui.Action(ref+"/slot/new"), ui.Autocomplete("off"),
				ui.Field(ui.Label("Дата и время"),
					ui.Datetimefield(ui.Name("starts_at"), ui.Placeholder("2026-09-04 19:00"), ui.Required(),
						ui.Data("datetime-tz", data.Tz))),
				ui.Row(ui.Button(ui.Submit(), ui.Text("Создать слот"))),
			),
		))
	}
	return ui.Section(sect...)
}

type SlotApplicationRow struct {
	App         venues.Application
	Flags       []string
	FlagSummary string
	Submitter   string
	SubmitterTg string
	Versions    []venues.Version
}

type slotPageData struct {
	Venue        venues.Venue
	Slot         venues.Slot
	Tournament   string
	GameStatus   string
	GameHref     string
	Applications []SlotApplicationRow
	Voting       VotingView
	Contested    []ContestedRow
	RegURL       string
	CanManage    bool
	Tz           string
	Error        string
	Notice       string
}

func slotTitle(slot venues.Slot) string {
	if slot.StartsAt == "" {
		return "Слот без даты"
	}
	return slot.StartsAt
}

// VenueBase is the Representative's tree for one Площадка; a Venue is a Fest
// of its own kind and has host pages of its own.
func VenueBase(v venues.Venue) string { return "/host/venue/" + v.Ref() }

func slotBase(v venues.Venue, slot venues.Slot) string {
	return VenueBase(v) + "/slot/" + strconv.FormatInt(slot.ID, 10)
}

func slotPageDoc(data slotPageData) *ui.Doc {
	page := []ui.Item{ui.Title(slotTitle(data.Slot) + " · " + data.Venue.Title), ui.PagePublic,
		ui.Classicscripts("dist/pageforms.js dist/roster-editor.js")}
	page = append(page, ui.Publictopbar(ui.Crumbs(
		append(venueCrumbs(data.Venue), pages.Leaf(slotTitle(data.Slot)))...)))
	if data.Error != "" {
		page = append(page, ui.Empty(ui.Text(data.Error)))
	}
	if data.Notice != "" {
		page = append(page, ui.Hint(ui.Text(data.Notice)))
	}
	page = append(page, slotHeaderSection(data), slotLinksSection(data), slotApplicationsSection(data),
		slotVotingSection(data), slotContestedSection(data), slotDownloadsSection(data))
	return &ui.Doc{Nodes: []ui.Node{ui.Page(page...)}}
}

func slotHeaderSection(data slotPageData) *ui.Element {
	base := slotBase(data.Venue, data.Slot)
	tournamentID := ""
	if data.Slot.RatingTournamentID > 0 {
		tournamentID = strconv.FormatInt(data.Slot.RatingTournamentID, 10)
	}
	sect := []ui.Item{
		ui.Subhead(ui.Text("Слот")),
		ui.Note(ui.Text(joinDots(slotTitle(data.Slot), data.Tournament, data.GameStatus))),
		ui.Row(ui.Button(ui.Ghost, ui.Small(), ui.Href(data.GameHref), ui.Text("Страница игры"))),
	}
	if !data.CanManage {
		return ui.Section(sect...)
	}
	closed := []ui.Item{ui.Name("reg_closed"), ui.Value("1"), ui.Text("Регистрация закрыта")}
	if data.Slot.RegClosed {
		closed = append(closed, ui.Checked())
	}
	sect = append(sect,
		ui.Form(ui.DirCol, ui.Method("post"), ui.Action(base), ui.Autocomplete("off"),
			ui.Field(ui.Label("Дата и время"), ui.Datetimefield(ui.Name("starts_at"), ui.Value(data.Slot.StartsAt),
				ui.Data("datetime-tz", data.Tz))),
			ui.Field(ui.Label("ID турнира на rating.chgk.info"),
				ui.Textfield(ui.Name("rating_tournament_id"), ui.Value(tournamentID), ui.Inputmode("numeric"),
					ui.Data("buff-tournament", data.Slot.StartsAt), ui.Autocomplete("off"))),
			ui.Field(ui.Label("Регистрация открывается"),
				ui.Datetimefield(ui.Name("reg_opens_at"), ui.Value(data.Slot.RegOpensAt), ui.Placeholder("сразу"),
					ui.Data("datetime-tz", data.Tz))),
			ui.Checkbox(closed...),
			ui.Row(ui.Button(ui.Submit(), ui.Text("Сохранить"))),
		),
		ui.Row(ui.Button(ui.Ghost, ui.Data("dialog-open", "cloneSlot"), ui.Text("Клонировать"))),
		ui.Dialog(ui.ID("cloneSlot"),
			ui.Form(ui.DirCol, ui.Method("post"), ui.Action(base+"/clone"), ui.Autocomplete("off"),
				ui.Subhead(ui.Text("Клонировать слот")),
				ui.Note(ui.Text("Копируются настройки игры и новая ссылка: без турнира, заявок и голосования.")),
				ui.Field(ui.Label("Дата и время нового слота"),
					ui.Datetimefield(ui.Name("starts_at"), ui.Value(venues.Shift(data.Slot.StartsAt, 7*24*time.Hour)),
						ui.Required(), ui.Data("datetime-tz", data.Tz))),
				ui.Row(
					ui.Button(ui.Submit(), ui.Text("Клонировать")),
					ui.Button(ui.Data("dialog-close", ""), ui.Text("Отмена")),
				),
			),
		),
	)
	return ui.Section(sect...)
}

func slotLinksSection(data slotPageData) *ui.Element {
	base := slotBase(data.Venue, data.Slot)
	sect := []ui.Item{ui.Subhead(ui.Text("Ссылка на регистрацию")),
		ui.Row(ui.SpaceSM, ui.AlignCenter, ui.Wrap(),
			ui.Textfield(ui.ID("regLink"), ui.Value(data.RegURL), ui.Readonly(), ui.Data("select-all", "")),
			ui.Button(ui.Ghost, ui.Small(), ui.Data("copy-target", "regLink"), ui.Text("Копировать")),
		),
	}
	if data.CanManage {
		sect = append(sect, ui.Form(ui.Method("post"), ui.Action(base+"/token"),
			ui.Data("confirm", "Сменить ссылку? Старая перестанет работать."),
			ui.Button(ui.Ghost, ui.Small(), ui.Submit(), ui.Text("Сменить ссылку")),
		))
	}
	return ui.Section(sect...)
}

func applicationActions(base string, row SlotApplicationRow) *ui.Element {
	actions := []ui.Item{ui.Method("post"), ui.Action(base + "/application/" + strconv.FormatInt(row.App.ID, 10) + "/status")}
	for _, a := range []struct{ status, label string }{
		{venues.StatusAccepted, "Принять"},
		{venues.StatusDeclined, "Отклонить"},
		{venues.StatusPending, "Вернуть в ожидание"},
	} {
		if a.status == row.App.Status {
			continue
		}
		actions = append(actions, ui.Button(ui.Ghost, ui.Small(), ui.Submit(),
			ui.Name("status"), ui.Value(a.status), ui.Text(a.label)))
	}
	return ui.Form(actions...)
}

func applicationVersions(base string, row SlotApplicationRow) *ui.Element {
	appID := strconv.FormatInt(row.App.ID, 10)
	items := []ui.Item{ui.Summary(ui.Text("Версии и правка"))}
	table := []ui.Item{ui.Trow(ui.Hcell(ui.Text("№")), ui.Hcell(ui.Text("Когда")), ui.Hcell(ui.Text("Кто")),
		ui.Hcell(ui.Text("Команда")), ui.Hcell(ui.Text("Состав")), ui.Hcell(ui.Text("")))}
	for _, v := range row.Versions {
		names := make([]string, 0, len(v.Roster))
		for _, p := range v.Roster {
			names = append(names, p.FullName())
		}
		table = append(table, ui.Trow(
			ui.Cell(ui.Text(strconv.FormatInt(v.Seq, 10))),
			ui.Cell(ui.Text(venues.HumanTime(v.CreatedAt))),
			ui.Cell(ui.Text(v.Author)),
			ui.Cell(ui.Text(v.TeamName)),
			ui.Cell(ui.Text(strings.Join(names, ", "))),
			ui.Cell(ui.Form(ui.Method("post"), ui.Action(base+"/application/"+appID+"/revert"),
				ui.Button(ui.Ghost, ui.Small(), ui.Submit(), ui.Name("seq"), ui.Value(strconv.FormatInt(v.Seq, 10)),
					ui.Text("Вернуть эту версию")))),
		))
	}
	return ui.Details(append(items, ui.Table(append([]ui.Item{ui.Scroll()}, table...)...))...)
}

func slotApplicationsSection(data slotPageData) *ui.Element {
	base := slotBase(data.Venue, data.Slot)
	sect := []ui.Item{ui.Subhead(ui.Text("Заявки"))}
	if len(data.Applications) == 0 {
		return ui.Section(append(sect, ui.Empty(ui.Text("Заявок пока нет.")))...)
	}
	table := []ui.Item{ui.Scroll(), ui.Trow(
		ui.Hcell(ui.Text("№")), ui.Hcell(ui.Text("Команда")), ui.Hcell(ui.Text("Рейтинг")),
		ui.Hcell(ui.Text("Кто подал")), ui.Hcell(ui.Text("Подана")), ui.Hcell(ui.Text("Изменена")),
		ui.Hcell(ui.Text("Состав")), ui.Hcell(ui.Text("Флаги")), ui.Hcell(ui.Text("Статус")),
	)}
	for _, row := range data.Applications {
		number := ""
		if row.App.Number > 0 {
			number = strconv.FormatInt(row.App.Number, 10)
		}
		rating := ui.Cell(ui.Text(""))
		if row.App.RatingTeamID > 0 {
			id := strconv.FormatInt(row.App.RatingTeamID, 10)
			rating = ui.Cell(ui.Link(ui.Href("https://rating.chgk.info/teams/"+id), ui.Newtab(), ui.Text(id)))
		}
		submitter := ui.Cell(ui.Text(row.Submitter))
		if row.SubmitterTg != "" {
			submitter = ui.Cell(ui.Link(ui.Href(row.SubmitterTg), ui.Newtab(), ui.Text(row.Submitter)))
		}
		table = append(table, ui.Trow(
			ui.Cell(ui.Text(number)),
			ui.Cell(ui.Text(row.App.TeamName)),
			rating,
			submitter,
			ui.Cell(ui.Text(venues.HumanTime(row.App.CreatedAt))),
			ui.Cell(ui.Text(venues.HumanTime(row.App.UpdatedAt))),
			ui.Cell(ui.Text(strconv.Itoa(len(row.App.Roster)))),
			ui.Cell(ui.Text(row.FlagSummary)),
			ui.Cell(ui.Text(StatusLabel(row.App.Status))),
		))
	}
	sect = append(sect, ui.Table(table...))
	if data.CanManage {
		for _, row := range data.Applications {
			sect = append(sect, ui.Details(
				ui.Summary(ui.Text(row.App.TeamName+" · "+StatusLabel(row.App.Status))),
				rosterFlagsTable(row.App.Roster, row.Flags),
				applicationActions(base, row),
				applicationVersions(base, row),
				applicationForm(base+"/application/"+strconv.FormatInt(row.App.ID, 10)+"/edit",
					&ApplicationView{TeamName: row.App.TeamName, RatingTeamID: row.App.RatingTeamID, Roster: row.App.Roster},
					"Сохранить как новую версию", true),
			))
		}
	}
	return ui.Section(sect...)
}

func slotDownloadsSection(data slotPageData) *ui.Element {
	base := slotBase(data.Venue, data.Slot)
	return ui.Section(
		ui.Subhead(ui.Text("Выгрузки")),
		ui.Row(ui.SpaceSM, ui.Wrap(),
			ui.Button(ui.Ghost, ui.Href(base+"/export/tours.xlsx"), ui.Download(), ui.Text("Туры (xlsx)")),
			ui.Button(ui.Ghost, ui.Href(base+"/export/players.xlsx"), ui.Download(), ui.Text("Игроки (xlsx)")),
		),
	)
}

// SlugPattern and SlugTitle are what util.ValidateSlug takes, said to the
// browser so a bad slug is refused before the form is posted.
const (
	// The hyphen is escaped: a bare one inside a class is a syntax error under
	// the v flag a browser compiles `pattern` with, and a pattern it cannot
	// compile is one it silently ignores.
	SlugPattern = `[a-z0-9\-]*[a-z\-][a-z0-9\-]*`
	SlugTitle   = "Только латиница в нижнем регистре, цифры и дефис; не одни цифры"
)

func slugField(label, value string) *ui.Element {
	return ui.Field(ui.Label(label),
		ui.Textfield(ui.Name("slug"), ui.Value(value), ui.Pattern(SlugPattern),
			ui.Title(SlugTitle), ui.Placeholder("my-venue")),
		ui.Hint(ui.Text(SlugTitle)))
}

func venueCreateForm(data hostLandingData) *ui.Element {
	items := []ui.Item{ui.Summary(ui.Btn(), ui.Text("Создать площадку"))}
	if data.Open == "venue" {
		items = append([]ui.Item{ui.Open()}, items...)
		if data.Error != "" {
			items = append(items, ui.Hint(ui.HintDanger, ui.Text(data.Error)))
		}
	}
	return ui.Details(append(items,
		ui.Form(ui.DirCol, ui.Method("post"), ui.Action("/host/venue"), ui.Autocomplete("off"),
			ratingVenueField(data.kept("rating_venue_id"), true),
			slugField("Slug (URL вида /venue/{slug})", data.kept("slug")),
			ui.Field(ui.Label("Описание (markdown)"), ui.Editor(ui.Name("description"), ui.Rows("4"), ui.Text(data.kept("description")))),
			checkboxKept("is_public", "Публичная", data.checked("is_public")),
			ui.Row(ui.Button(ui.Submit(), ui.Text("Создать"))),
		))...)
}

// ratingVenueField is the venue on rating.chgk.info a Площадка stands for: a
// suggest over their catalogue by id, name or town. A Площадка is one of
// theirs, so the create form asks for nothing else — the name and the town come
// with the pick.
func ratingVenueField(value string, required bool) *ui.Element {
	field := []ui.Item{ui.Name("rating_venue_id"), ui.Value(value),
		ui.Placeholder("город, название или id"), ui.Data("rating-venue", "")}
	if required {
		field = append(field, ui.Required())
	}
	return ui.Field(ui.Label("Площадка на rating.chgk.info"), ui.Textfield(field...))
}
