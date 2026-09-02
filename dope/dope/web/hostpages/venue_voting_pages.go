package hostpages

import (
	"strconv"
	"strings"

	"dope/dope/domain/venues"
	"dope/dope/web/pages"
	ui "dope/dope/web/ui"
)

type VotingView struct {
	Voting     venues.Voting
	URL        string
	Tally      []venues.TallyRow
	Ballots    []venues.Ballot
	Candidates []venues.Candidate // the offer when no poll exists yet
	KindLabel  string
}

var kindLabels = []struct{ kind, label string }{
	{venues.KindOne, "ровно один"},
	{venues.KindAny, "любые"},
	{venues.KindRanked, "три по порядку"},
}

func KindLabel(kind string) string {
	for _, k := range kindLabels {
		if k.kind == kind {
			return k.label
		}
	}
	return kind
}

func candidateLabel(c venues.Candidate) string {
	return joinDots(c.Name, c.Type)
}

func votingForm(base string, view VotingView) *ui.Element {
	v := view.Voting
	candidates := view.Candidates
	ticked := map[int64]bool{}
	if v.ID != 0 {
		candidates = v.Candidates
		for _, c := range v.Candidates {
			ticked[c.ID] = true
		}
	} else {
		for _, c := range candidates {
			ticked[c.ID] = true
		}
	}
	picks := make([]ui.Item, 0, len(candidates)+1)
	for _, c := range candidates {
		if v.Frozen {
			picks = append(picks, ui.Note(ui.Text(candidateLabel(c))))
			continue
		}
		items := []ui.Item{ui.Name("candidate"), ui.Value(strconv.FormatInt(c.ID, 10)), ui.Text(candidateLabel(c))}
		if ticked[c.ID] {
			items = append(items, ui.Checked())
		}
		picks = append(picks, ui.Checkbox(items...))
	}
	if len(picks) == 0 {
		picks = append(picks, ui.Empty(ui.Text("Буфф не знает турниров на эту дату — добавьте по id.")))
	}
	kinds := make([]ui.Item, 0, len(kindLabels))
	for _, k := range kindLabels {
		items := []ui.Item{ui.Name("kind"), ui.Value(k.kind), ui.Text(k.label)}
		if k.kind == v.Kind || (v.ID == 0 && k.kind == venues.KindOne) {
			items = append(items, ui.Checked())
		}
		kinds = append(kinds, ui.Radio(items...))
	}
	perTeam := []ui.Item{ui.Name("per_team"), ui.Value("1"), ui.Text("Голосуют команды, а не люди")}
	if v.PerTeam {
		perTeam = append(perTeam, ui.Checked())
	}
	// A cast ballot means one thing under one kind and another under the next,
	// so a frozen poll shows them rather than offering them, and posts them
	// back unchanged.
	if v.Frozen {
		kinds = []ui.Item{ui.Note(ui.Text(KindLabel(v.Kind))), ui.Hiddenfield(ui.Name("kind"), ui.Value(v.Kind))}
		perTeam = nil
	}
	submit := "Создать голосование"
	if v.ID != 0 {
		submit = "Сохранить голосование"
	}
	form := []ui.Item{ui.DirCol, ui.Method("post"), ui.Action(base + "/voting"), ui.Autocomplete("off")}
	form = append(form, ui.Fieldset(append([]ui.Item{ui.Subhead(ui.Text("Турниры"))}, picks...)...))
	if v.Frozen {
		form = append(form, ui.Hint(ui.Text("По голосованию уже голосовали: турниры, вид и режим больше не меняются.")))
	}
	if !v.Frozen {
		form = append(form, ui.Field(ui.Label("Добавить турнир по id"),
			ui.Textfield(ui.Name("extra_candidate"), ui.Inputmode("numeric"))))
	}
	form = append(form, ui.Pickgroup(append([]ui.Item{ui.Label("Сколько можно выбрать")}, kinds...)...))
	if perTeam != nil {
		form = append(form, ui.Checkbox(perTeam...))
	} else if v.PerTeam {
		form = append(form,
			ui.Note(ui.Text("Голосуют команды, а не люди")),
			ui.Hiddenfield(ui.Name("per_team"), ui.Value("1")))
	}
	form = append(form,
		ui.Field(ui.Label("Открывается"), ui.Textfield(ui.Name("opens_at"), ui.Value(v.OpensAt), ui.Placeholder("сразу"))),
		ui.Field(ui.Label("Закрывается"), ui.Textfield(ui.Name("closes_at"), ui.Value(v.ClosesAt), ui.Placeholder("2026-09-04 18:00"))),
		ui.Row(ui.Button(ui.Submit(), ui.Text(submit))),
	)
	return ui.Form(form...)
}

func votingTally(rows []venues.TallyRow) *ui.Element {
	table := []ui.Item{ui.Scroll(), ui.Trow(ui.Hcell(ui.Text("Турнир")), ui.Hcell(ui.Text("Голоса")))}
	for _, r := range rows {
		table = append(table, ui.Trow(
			ui.Cell(ui.Text(candidateLabel(r.Candidate))),
			ui.Cell(ui.Text(strconv.Itoa(r.Score))),
		))
	}
	return ui.Table(table...)
}

func votingBallots(base string, view VotingView) *ui.Element {
	names := map[int64]string{}
	for _, c := range view.Voting.Candidates {
		names[c.ID] = c.Name
	}
	table := []ui.Item{ui.Scroll(), ui.Trow(
		ui.Hcell(ui.Text("Кто")), ui.Hcell(ui.Text("Команда")), ui.Hcell(ui.Text("Выбор")),
		ui.Hcell(ui.Text("Когда")), ui.Hcell(ui.Text("")),
	)}
	for _, b := range view.Ballots {
		choice := make([]string, 0, len(b.Choice))
		for _, id := range b.Choice {
			choice = append(choice, names[id])
		}
		label, discard := "Отклонить", "1"
		if b.Discarded {
			label, discard = "Вернуть", "0"
		}
		voter := b.Voter
		if voter == "" {
			voter = "—"
		} else {
			voter = "@" + voter
		}
		table = append(table, ui.Trow(
			ui.Cell(ui.Text(voter)),
			ui.Cell(ui.Text(b.TeamName)),
			ui.Cell(ui.Text(joinList(choice))),
			ui.Cell(ui.Text(venues.HumanTime(b.CreatedAt))),
			ui.Cell(ui.Form(ui.Method("post"), ui.Action(base+"/voting/ballot/"+strconv.FormatInt(b.ID, 10)),
				ui.Button(ui.Ghost, ui.Small(), ui.Submit(), ui.Name("discarded"), ui.Value(discard), ui.Text(label)))),
		))
	}
	return ui.Table(table...)
}

func joinList(parts []string) string { return strings.Join(parts, ", ") }

func slotVotingSection(data slotPageData) *ui.Element {
	base := slotBase(data.Venue, data.Slot)
	view := data.Voting
	sect := []ui.Item{ui.Subhead(ui.Text("Голосование"))}
	if !data.CanManage {
		if view.Voting.ID == 0 {
			return ui.Section(append(sect, ui.Empty(ui.Text("Голосования нет.")))...)
		}
		return ui.Section(append(sect, votingTally(view.Tally))...)
	}
	if view.Voting.ID != 0 {
		sect = append(sect,
			ui.Note(ui.Text(joinDots("Вид: "+KindLabel(view.Voting.Kind), votingWindowLabel(view.Voting)))),
			ui.Row(ui.SpaceSM, ui.AlignCenter, ui.Wrap(),
				ui.Textfield(ui.ID("voteLink"), ui.Value(view.URL), ui.Readonly(), ui.Data("select-all", "")),
				ui.Button(ui.Ghost, ui.Small(), ui.Data("copy-target", "voteLink"), ui.Text("Копировать")),
			),
			votingTally(view.Tally),
		)
		if len(view.Ballots) > 0 {
			sect = append(sect, votingBallots(base, view))
		}
		sect = append(sect, ui.Details(ui.Summary(ui.Btn(), ui.Text("Настройки голосования")), votingForm(base, view)))
		return ui.Section(sect...)
	}
	sect = append(sect, ui.Details(ui.Summary(ui.Btn(), ui.Text("Создать голосование")), votingForm(base, view)))
	return ui.Section(sect...)
}

func votingWindowLabel(v venues.Voting) string {
	switch {
	case v.OpensAt != "" && v.ClosesAt != "":
		return v.OpensAt + " — " + v.ClosesAt
	case v.ClosesAt != "":
		return "до " + v.ClosesAt
	case v.OpensAt != "":
		return "с " + v.OpensAt
	}
	return "без срока"
}

type VotePage struct {
	Voting     venues.Voting
	VenueTitle string
	VenueRef   string
	SlotDate   string
	State      venues.RegState
	LoggedIn   bool
	LoginHref  string
	Ballot     *venues.Ballot
	Tally      []venues.TallyRow
	Error      string
}

func VoteDoc(p VotePage) *ui.Doc {
	page := []ui.Item{ui.Title("Голосование · " + p.VenueTitle), ui.PagePublic, ui.Classicscripts("dist/pageforms.js")}
	page = append(page, ui.Publictopbar(ui.Crumbs(
		pages.HomeCrumb(), ui.Crumb(ui.Href("/venues"), ui.Text("Площадки")),
		ui.Crumb(ui.Href("/venue/"+p.VenueRef), ui.Text(p.VenueTitle)), pages.Leaf("Голосование"))))
	page = append(page, ui.Section(
		ui.Subhead(ui.Text(p.VenueTitle)),
		ui.Note(ui.Text(joinDots(p.SlotDate, votingWindowLabel(p.Voting)))),
	))
	if p.Error != "" {
		page = append(page, ui.Empty(ui.Text(p.Error)))
	}
	switch {
	case p.State == venues.RegScheduled:
		page = append(page, ui.Empty(ui.Text("Голосование откроется "+p.Voting.OpensAt+".")))
	case p.State == venues.RegClosed:
		page = append(page, ui.Section(ui.Subhead(ui.Text("Итог")), votingTally(p.Tally)))
	case !p.LoggedIn:
		page = append(page, ui.Section(
			ui.Hint(ui.Text("Чтобы проголосовать, войдите через Telegram.")),
			ui.Row(ui.Button(ui.Primary, ui.Href(p.LoginHref), ui.Text("Войти"))),
		))
	default:
		page = append(page, ballotSection(p))
	}
	return &ui.Doc{Nodes: []ui.Node{ui.Page(page...)}}
}

func ballotSection(p VotePage) *ui.Element {
	v := p.Voting
	chosen := map[int64]bool{}
	var order []int64
	if p.Ballot != nil {
		order = p.Ballot.Choice
		for _, id := range order {
			chosen[id] = true
		}
	}
	form := []ui.Item{ui.DirCol, ui.Method("post"), ui.Action("/vote/" + v.Token), ui.Autocomplete("off")}
	if v.PerTeam {
		teamName := ""
		if p.Ballot != nil {
			teamName = p.Ballot.TeamName
		}
		form = append(form, ui.Field(ui.Label("Команда"),
			ui.Textfield(ui.Name("team_name"), ui.Value(teamName), ui.Required())))
	}
	switch v.Kind {
	case venues.KindRanked:
		for place := 0; place < venues.RankedDepth; place++ {
			options := []ui.Item{ui.Name("choice"), ui.Option(ui.Value(""), ui.Text("—"))}
			for _, c := range v.Candidates {
				item := []ui.Item{ui.Value(strconv.FormatInt(c.ID, 10)), ui.Text(candidateLabel(c))}
				if place < len(order) && order[place] == c.ID {
					item = append(item, ui.Selected())
				}
				options = append(options, ui.Option(item...))
			}
			form = append(form, ui.Field(ui.Label(rankLabels[place]), ui.Selectfield(options...)))
		}
	case venues.KindAny:
		picks := make([]ui.Item, 0, len(v.Candidates)+1)
		picks = append(picks, ui.Subhead(ui.Text("Во что готовы играть")))
		for _, c := range v.Candidates {
			items := []ui.Item{ui.Name("choice"), ui.Value(strconv.FormatInt(c.ID, 10)), ui.Text(candidateLabel(c))}
			if chosen[c.ID] {
				items = append(items, ui.Checked())
			}
			picks = append(picks, ui.Checkbox(items...))
		}
		form = append(form, ui.Fieldset(picks...))
	default:
		picks := make([]ui.Item, 0, len(v.Candidates)+1)
		picks = append(picks, ui.Label("Во что играем"))
		for _, c := range v.Candidates {
			items := []ui.Item{ui.Name("choice"), ui.Value(strconv.FormatInt(c.ID, 10)), ui.Text(candidateLabel(c))}
			if chosen[c.ID] {
				items = append(items, ui.Checked())
			}
			picks = append(picks, ui.Radio(items...))
		}
		form = append(form, ui.Pickgroup(picks...))
	}
	submit := "Проголосовать"
	if p.Ballot != nil {
		submit = "Изменить голос"
	}
	form = append(form, ui.Row(ui.Button(ui.Submit(), ui.Text(submit))))

	sect := []ui.Item{ui.Subhead(ui.Text("Ваш голос"))}
	if p.Ballot != nil && p.Ballot.Discarded {
		sect = append(sect, ui.Hint(ui.HintDanger, ui.Text("Ваш голос отклонён представителем площадки.")))
	}
	return ui.Section(append(sect, ui.Form(form...))...)
}

var rankLabels = [venues.RankedDepth]string{"Первое место", "Второе место", "Третье место"}
