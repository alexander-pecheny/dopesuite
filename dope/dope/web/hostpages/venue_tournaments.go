package hostpages

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"dope/dope/domain/venues"
	"dope/dope/storage/buffdb"
	"dope/dope/web/pages"
	"dope/dope/web/route"
	ui "dope/dope/web/ui"
)

// TournamentCard is one playable tournament as the picker shows it: what buff
// knows about it, plus what rating.chgk.info says its editors expect of it.
type TournamentCard struct {
	ID         int64
	Name       string
	Type       string
	Editors    string
	Difficulty float64
	Teams      int
	Chosen     bool
}

// TournamentPicker is the same screen twice over: one card list, chosen from
// either to name the game's tournament or to offer the whole set to a poll.
type TournamentPicker struct {
	Venue  venues.Venue
	Slot   venues.Slot
	Base   string
	Poll   bool
	Cards  []TournamentCard
	Voting venues.Voting
	Tz     string
}

const (
	sortDifficultyUp   = "difficulty-asc"
	sortDifficultyDown = "difficulty-desc"
	sortTeams          = "teams-desc"

	typeAll   = "all"
	typeSync  = "sync"
	typeAsync = "async"
)

func tournamentTitle(p TournamentPicker) string {
	if p.Poll {
		return strs.Venues.Voting.CreateSubmit()
	}
	return strs.Venues.Tournaments.PickBtn()
}

func TournamentPickerDoc(p TournamentPicker) *ui.Doc {
	title := tournamentTitle(p)
	page := []ui.Item{ui.Title(title + " · " + p.Venue.Title), ui.PagePublicwide,
		ui.Classicscripts("dist/pageforms.js dist/tournament-picker.js")}
	page = append(page, ui.Publictopbar(ui.Crumbs(
		append(venueCrumbs(p.Venue),
			ui.Crumb(ui.Href(p.Base), ui.Text(slotTitle(p.Slot))), pages.Leaf(title))...)))
	if len(p.Cards) == 0 {
		return &ui.Doc{Nodes: []ui.Node{ui.Page(append(page,
			ui.Section(ui.Subhead(ui.Text(title)), ui.Empty(ui.Text(strs.Venues.Tournaments.Empty()))))...)}}
	}
	page = append(page, tournamentControls(), tournamentList(p))
	return &ui.Doc{Nodes: []ui.Node{ui.Page(page...)}}
}

// tournamentControls narrows and orders the list. Everything it does happens in
// the page: the whole week's tournaments are already here, and a Representative
// runs through several orderings before settling on one.
func tournamentControls() *ui.Element {
	return ui.Section(ui.Row(ui.SpaceMD, ui.AlignCenter, ui.Wrap(),
		ui.Field(ui.Label(strs.Venues.Tournaments.TypeLabel()),
			ui.Selectfield(ui.Compact(), ui.Data("tournament-filter", "kind"),
				ui.Option(ui.Value(typeSync), ui.Selected(), ui.Text(strs.Venues.Tournaments.TypeSync())),
				ui.Option(ui.Value(typeAsync), ui.Text(strs.Venues.Tournaments.TypeAsync())),
				ui.Option(ui.Value(typeAll), ui.Text(strs.Venues.Tournaments.TypeAll())),
			)),
		ui.Field(ui.Label(strs.Venues.Tournaments.DifficultyLabel()),
			ui.Row(ui.SpaceSM, ui.AlignCenter, ui.Wrap(),
				ui.Numfield(ui.Narrow(), ui.Min("0"), ui.Max("12"), ui.Step("0.5"),
					ui.Placeholder(strs.Venues.Tournaments.DifficultyFrom()), ui.Data("tournament-filter", "from")),
				ui.Numfield(ui.Narrow(), ui.Min("0"), ui.Max("12"), ui.Step("0.5"),
					ui.Placeholder(strs.Venues.Tournaments.DifficultyTo()), ui.Data("tournament-filter", "to")),
			)),
		ui.Field(ui.Label(strs.Venues.Tournaments.SortLabel()),
			ui.Selectfield(ui.Compact(), ui.Data("tournament-filter", "sort"),
				ui.Option(ui.Value(sortDifficultyUp), ui.Selected(), ui.Text(strs.Venues.Tournaments.SortDifficultyUp())),
				ui.Option(ui.Value(sortDifficultyDown), ui.Text(strs.Venues.Tournaments.SortDifficultyDown())),
				ui.Option(ui.Value(sortTeams), ui.Text(strs.Venues.Tournaments.SortTeams())),
			)),
		// One button for both ways: it says which way it would go, and the
		// picker rewrites it as the ticks change.
		ui.Button(ui.Ghost, ui.ID("tournamentsAll"), ui.Data("tournament-all", strs.Venues.Tournaments.SelectAll()),
			ui.Data("tournament-none", strs.Venues.Tournaments.DeselectAll()),
			ui.Text(strs.Venues.Tournaments.DeselectAll())),
	))
}

func tournamentList(p TournamentPicker) *ui.Element {
	cards := make([]ui.Item, 0, len(p.Cards))
	for _, c := range p.Cards {
		cards = append(cards, tournamentCard(p, c))
	}
	grid := ui.Cardgrid(append([]ui.Item{ui.ID("tournaments")}, cards...)...)
	if !p.Poll {
		return ui.Section(ui.Subhead(ui.Text(tournamentTitle(p))), grid)
	}
	// In poll mode the cards are the poll's candidates, so the grid and the
	// poll's own settings are one form and one save.
	form := []ui.Item{ui.DirCol, ui.Method("post"), ui.Action(p.Base + "/voting"), ui.Autocomplete("off"),
		ui.Subhead(ui.Text(tournamentTitle(p))), grid}
	form = append(form, votingSettings(p.Voting, p.Tz)...)
	form = append(form, ui.Row(ui.Button(ui.Submit(), ui.Text(strs.Venues.Voting.CreateSubmit()))))
	return ui.Section(ui.Form(form...))
}

// tournamentCard carries everything about one tournament and everything a
// Representative does with it. The tick is what keeps a card among the real
// options — unticking sinks it below the rest, and in poll mode drops it from
// the ballot.
func tournamentCard(p TournamentPicker, c TournamentCard) *ui.Element {
	id := strconv.FormatInt(c.ID, 10)
	card := []ui.Item{
		ui.Data("tournament", id),
		ui.Data("difficulty", strconv.FormatFloat(c.Difficulty, 'f', -1, 64)),
		ui.Data("teams", strconv.Itoa(c.Teams)),
		ui.Data("kind", cardKind(c.Type)),
		ui.Link(ui.Href("https://rating.chgk.info/tournament/"+id), ui.Newtab(), ui.Text(c.Name)),
	}
	if c.Editors != "" {
		card = append(card, ui.Muted(ui.Text(c.Editors)))
	}
	facts := []ui.Item{ui.SpaceMD, ui.AlignCenter, ui.Wrap()}
	if c.Difficulty > 0 {
		facts = append(facts, ui.Fact(ui.IconFlaskConical, ui.Title(strs.Venues.Tournaments.DifficultyLabel()),
			ui.Text(strconv.FormatFloat(c.Difficulty, 'f', -1, 64))))
	}
	if c.Teams > 0 {
		facts = append(facts, ui.Fact(ui.IconUsers, ui.Title(strs.Venues.Tournaments.TeamsTitle()),
			ui.Text("~"+strconv.Itoa(c.Teams))))
	}
	if c.Chosen {
		facts = append(facts, ui.Fact(ui.IconCheck, ui.Text(strs.Venues.Tournaments.Chosen())))
	}
	card = append(card, ui.Muted(ui.Text(c.Type)), ui.Row(facts...), ui.Spacer())
	// The tick and the button are the card's own foot, so a card is read and
	// acted on in one place rather than across a row.
	foot := []ui.Item{ui.SpaceSM, ui.AlignCenter, ui.Wrap(),
		ui.Checkbox(ui.Name("candidate"), ui.Value(id), ui.Checked(),
			ui.Data("tournament-keep", ""), ui.Aria("label", strs.Venues.Tournaments.KeepAria()))}
	if !p.Poll {
		foot = append(foot, ui.Form(ui.Method("post"), ui.Action(p.Base+"/tournament"),
			ui.Hiddenfield(ui.Name("rating_tournament_id"), ui.Value(id)),
			ui.Button(ui.Small(), ui.Submit(), ui.Text(strs.Venues.Tournaments.PickSubmit()))))
	}
	return ui.Section(append(card, ui.Row(foot...))...)
}

// The type dropdown tells the two apart because a Venue that plays sync
// tournaments does not want the async ones in the way.
func cardKind(kind string) string {
	if buffdb.IsAsync(kind) {
		return typeAsync
	}
	return typeSync
}

// renderTournamentPicker builds the card list. What buff mirrors is here in the
// database; the forecast and the requests are one call per tournament to
// rating.chgk.info, so a tournament it will not answer for still gets a card.
func (s *Server) renderTournamentPicker(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	venue, slot, err := s.slotOf(r, sc)
	if err != nil {
		return err
	}
	p := TournamentPicker{
		Venue: venue, Slot: slot, Base: slotBase(venue, slot),
		Poll: r.URL.Query().Get("mode") == "poll",
		Tz:   s.userTimezone(r.Context(), sc.User.UserID),
	}
	if voting, err := venues.SlotVoting(r.Context(), s.h.Engine().DB, slot.ID); err == nil {
		p.Voting = voting
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	candidates := s.playableCandidates(r.Context(), slot)
	ids := make([]int64, 0, len(candidates))
	for _, c := range candidates {
		ids = append(ids, c.ID)
	}
	details := s.h.Engine().RatingTournaments().Details(r.Context(), ids)
	for _, c := range candidates {
		d := details[c.ID]
		p.Cards = append(p.Cards, TournamentCard{
			ID: c.ID, Name: c.Name, Type: c.Type, Editors: strings.Join(d.Editors, ", "),
			Difficulty: d.Difficulty, Teams: d.Teams, Chosen: c.ID == slot.RatingTournamentID,
		})
	}
	pages.RenderDoc(w, s.h.Engine().AssetETags, TournamentPickerDoc(p))
	return nil
}

// handleSlotTournament is a card's own button: it names the game's tournament
// and nothing else, so the form beside it keeps whatever it holds.
func (s *Server) handleSlotTournament(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	festID := sc.FestID
	_, slot, err := s.slotOf(r, sc)
	if err != nil {
		return err
	}
	if err := r.ParseForm(); err != nil {
		return route.BadRequest("bad form")
	}
	tournamentID := formInt64(r.Form, "rating_tournament_id")
	err = s.h.Engine().WithWriteTx(r.Context(), festID, "slot-tournament", func(ctx context.Context, tx *sql.Tx) error {
		if err := venues.UpdateSlotTx(ctx, tx, slot.ID, slot.StartsAt, tournamentID); err != nil {
			return err
		}
		if tournamentID <= 0 || tournamentID == slot.RatingTournamentID {
			return nil
		}
		return venues.RetourTx(ctx, tx, festID, slot.GameID, s.tourComposition(ctx, tournamentID, ""))
	})
	if err != nil {
		return s.renderSlotPage(w, r, sc, err.Error(), "")
	}
	s.h.Engine().InvalidateFestViewCache(festID)
	return s.redirectToSlot(w, r, festID, slot.ID)
}
