package hostpages

import (
	"context"
	"database/sql"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"dope/dope/domain/entrants"
	"dope/dope/domain/roster"
	"dope/dope/domain/view"
	"dope/dope/platform/util"
	"dope/dope/storage/festwrite"
	"dope/dope/web/pages"
	"dope/dope/web/route"
	dopeui "dope/dope/web/ui"
	dopestrings "dope/i18nstrings"

	corei18n "pecheny.me/dopecore/i18nstrings"
	"pecheny.me/dopecore/idstr"
)

// The fest's troikas (CONTEXT.md, Assembled team): Participants assembled out of
// fest players for Troika, not drawn from the rating roster. The page lists
// them with their people, the team each counts for and its division, adds many at
// once from pasted lines, and edits one in a dialog — which is how a
// substitution between bouts is made. A Troika Game that takes a division follows
// the page: every save re-seats it while nothing is entered in it.

type hostTroikasData struct {
	Fest    view.HostFest
	Troikas []roster.Assembled
	Players []roster.FestPlayerChoice
	Teams   []roster.FestTeamChoice
	// Games are the Troika Games that take their troikas from a division.
	Games  []entrants.DivisionGame
	Lines  string
	Error  string
	Notice string
}

// troikaPlayerFields is how many name fields the edit dialog offers: the most
// a troika may declare.
const troikaPlayerFields = roster.AssembledMax

// The division select's two values that are not a Flag.
const (
	divisionFollowValue = "@team"
	divisionNoneValue   = "@none"
)

func hostTroikasDoc(data hostTroikasData) *dopeui.Doc {
	s := dopestrings.Default
	ref := data.Fest.Ref()
	page := []dopeui.Item{
		dopeui.Title(s.Host.Troikas.Title(data.Fest.Title)), dopeui.PagePublic, dopeui.Classicscripts("dist/pageforms.js"),
		dopeui.Publictopbar(pages.Trail(pages.FestCrumbs(ref, data.Fest.Title), s.Host.Troikas.Crumb())),
	}
	if data.Error != "" {
		page = append(page, dopeui.Empty(dopeui.Text(data.Error)))
	}
	if data.Notice != "" {
		page = append(page, dopeui.Note(dopeui.Text(data.Notice)))
	}
	page = append(page, dopeui.Note(dopeui.Text(s.Host.Troikas.Hint())))
	// How troikas reach the Troika Games, and where each such Game stands,
	// read as one block.
	games := []dopeui.Item{dopeui.SpaceSM, dopeui.Hint(dopeui.Text(s.Host.Troikas.GamesHint()))}
	for _, game := range data.Games {
		games = append(games, dopeui.Hint(dopeui.Text(s.Host.Troikas.GameFollows(game.Title, divisionLabel(game.Division), strconv.Itoa(len(game.Troikas))))))
		switch {
		case game.Current:
		case game.Frozen:
			games = append(games, dopeui.Hint(dopeui.Text(s.Host.Troikas.GameFrozen(game.Title))))
		case game.Manual:
			games = append(games, dopeui.Hint(dopeui.Text(s.Host.Troikas.GameManual(game.Title))))
		case game.Problem != "":
			games = append(games, dopeui.Empty(dopeui.Text(s.Host.Troikas.GameProblem(game.Title, game.Problem))))
		case len(game.Troikas) == 0:
			games = append(games, dopeui.Empty(dopeui.Text(s.Host.Troikas.GameProblem(game.Title, s.Gamebuild.Division.NoTroikas(game.Division)))))
		}
	}
	page = append(page, dopeui.Col(games...))

	options := make([]dopeui.Item, 0, len(data.Players)+1)
	options = append(options, dopeui.ID("troikaPlayers"))
	for _, p := range data.Players {
		label := p.Name
		if p.Team != "" {
			label += " (" + p.Team + ")"
		}
		options = append(options, dopeui.Option(dopeui.Value(label)))
	}
	page = append(page, dopeui.Datalist(options...))

	if len(data.Troikas) > 0 {
		rows := []dopeui.Item{dopeui.Trow(
			dopeui.Hcell(dopeui.Text(s.Host.Troikas.ColApplied())),
			dopeui.Hcell(dopeui.Text(s.Host.Troikas.ColName())),
			dopeui.Hcell(dopeui.Text(s.Host.Troikas.ColPlayers())),
			dopeui.Hcell(dopeui.Text(s.Host.Troikas.ColTeam())),
			dopeui.Hcell(dopeui.Text(s.Host.Troikas.ColDivision())),
			dopeui.Hcell(),
		)}
		for _, t := range data.Troikas {
			applied := ""
			if t.Applied > 0 {
				applied = strconv.Itoa(t.Applied)
			}
			rows = append(rows, dopeui.Trow(
				dopeui.Cell(dopeui.Text(applied)),
				dopeui.Cell(dopeui.Text(t.Name)),
				dopeui.Cell(dopeui.Text(strings.Join(t.Players, ", "))),
				dopeui.Cell(dopeui.Text(t.HeadTeam)),
				dopeui.Cell(dopeui.Text(strings.Join(t.Flags, ", "))),
				dopeui.Cell(dopeui.Iconbtn(dopeui.IconPencil, dopeui.Label(s.Host.Troikas.EditLabel()), dopeui.Data("dialog-open", troikaDialogID(t.ID)))),
			))
		}
		page = append(page, dopeui.Table(append([]dopeui.Item{dopeui.Scroll()}, rows...)...))
		flags := festFlagChoices(data.Teams)
		for _, t := range data.Troikas {
			page = append(page, hostTroikaDialog(ref, t, data.Teams, flags))
		}
	} else {
		page = append(page, dopeui.Empty(dopeui.Text(s.Host.Troikas.Empty())))
	}

	page = append(page, dopeui.Section(
		dopeui.Subhead(dopeui.Text(s.Host.Troikas.AddSubhead())),
		dopeui.Form(dopeui.DirCol, dopeui.Method("post"), dopeui.Action("/host/fest/"+ref+"/troikas"), dopeui.Autocomplete("off"),
			dopeui.Hiddenfield(dopeui.Name("mode"), dopeui.Value("lines")),
			dopeui.Field(dopeui.Label(s.Host.Troikas.LinesLabel()),
				dopeui.Editor(dopeui.Name("lines"), dopeui.Rows("8"), dopeui.Placeholder(s.Host.Troikas.LinesPlaceholder()), dopeui.Text(data.Lines))),
			dopeui.Note(dopeui.Text(s.Host.Troikas.LinesHint())),
			dopeui.Row(dopeui.Button(dopeui.Submit(), dopeui.Text(s.Host.Troikas.AddSubmit()))),
		),
	))
	return &dopeui.Doc{Nodes: []dopeui.Node{dopeui.Page(page...)}}
}

// divisionLabel says a scheme's division in words: a Flag, or everyone without it.
func divisionLabel(division string) string {
	s := dopestrings.Default
	if flag, exclude := strings.CutPrefix(strings.TrimSpace(division), "-"); exclude {
		return s.Host.Troikas.DivisionNotCarrying(strings.TrimSpace(flag))
	}
	return s.Host.Troikas.DivisionCarrying(strings.TrimSpace(division))
}

// festFlagChoices is every Flag among the fest's teams, in the order first
// seen: the divisions a troika may be put in.
func festFlagChoices(teams []roster.FestTeamChoice) []string {
	var out []string
	seen := map[string]bool{}
	for _, team := range teams {
		for _, flag := range team.Flags {
			if !seen[flag] {
				seen[flag] = true
				out = append(out, flag)
			}
		}
	}
	return out
}

func troikaDialogID(id int64) string { return "troika-" + idstr.Format(id) }

// hostTroikaDialog edits one troika: its name, up to four people, each a field
// that suggests the fest's players, the team it counts for and its division.
// Delete is there only while no Game seats it.
func hostTroikaDialog(ref string, t roster.Assembled, teams []roster.FestTeamChoice, flags []string) *dopeui.Element {
	s := dopestrings.Default
	fields := []dopeui.Item{
		dopeui.Subhead(dopeui.Text(t.Name)),
		dopeui.Hiddenfield(dopeui.Name("mode"), dopeui.Value("edit")),
		dopeui.Hiddenfield(dopeui.Name("id"), dopeui.Value(idstr.Format(t.ID))),
		dopeui.Field(dopeui.Label(s.Host.Troikas.ColName()),
			dopeui.Textfield(dopeui.Name("name"), dopeui.Value(t.Name), dopeui.Required())),
	}
	applied := ""
	if t.Applied > 0 {
		applied = strconv.Itoa(t.Applied)
	}
	fields = append(fields,
		dopeui.Field(dopeui.Label(s.Host.Troikas.AppliedLabel()),
			dopeui.Textfield(dopeui.Name("applied"), dopeui.Value(applied), dopeui.Inputmode("numeric"))),
		dopeui.Hint(dopeui.Text(s.Host.Troikas.AppliedHint())))
	for i := 0; i < troikaPlayerFields; i++ {
		value := ""
		if i < len(t.Players) {
			value = t.Players[i]
		}
		fields = append(fields, dopeui.Field(dopeui.Label(s.Host.Troikas.PlayerN(strconv.Itoa(i+1))),
			dopeui.Textfield(dopeui.Name("player"), dopeui.Value(value), dopeui.InputList("troikaPlayers"))))
	}

	option := func(value, text string, selected bool) dopeui.Item {
		items := []dopeui.Item{dopeui.Value(value), dopeui.Text(text)}
		if selected {
			items = append(items, dopeui.Selected())
		}
		return dopeui.Option(items...)
	}
	teamOptions := []dopeui.Item{dopeui.Name("head_team"), option("0", s.Host.Troikas.TeamNone(), t.HeadTeamID == 0)}
	for _, team := range teams {
		teamOptions = append(teamOptions, option(idstr.Format(team.ID), team.Name, team.ID == t.HeadTeamID))
	}
	fields = append(fields,
		dopeui.Field(dopeui.Label(s.Host.Troikas.ColTeam()), dopeui.Selectfield(teamOptions...)),
		dopeui.Hint(dopeui.Text(s.Host.Troikas.TeamHint())))

	chosen := ""
	if t.Division != nil {
		chosen = *t.Division
	}
	divisionOptions := []dopeui.Item{dopeui.Name("division"),
		option(divisionFollowValue, s.Host.Troikas.DivisionFollow(), t.FollowsTeam()),
		option(divisionNoneValue, s.Host.Troikas.DivisionNone(), !t.FollowsTeam() && chosen == ""),
	}
	for _, flag := range flags {
		divisionOptions = append(divisionOptions, option(flag, flag, !t.FollowsTeam() && chosen == flag))
	}
	if !t.FollowsTeam() && chosen != "" && !slices.Contains(flags, chosen) {
		// A division no fest team carries any more stays offered, so saving the
		// dialog does not quietly drop it.
		divisionOptions = append(divisionOptions, option(chosen, chosen, true))
	}
	fields = append(fields, dopeui.Field(dopeui.Label(s.Host.Troikas.ColDivision()), dopeui.Selectfield(divisionOptions...)))

	buttons := []dopeui.Item{dopeui.Button(dopeui.Submit(), dopeui.Text(s.Host.Roster.SaveSubmit()))}
	if !t.Seated {
		buttons = append(buttons, dopeui.Button(dopeui.Danger, dopeui.Submit(), dopeui.Name("delete"), dopeui.Value("1"), dopeui.Formnovalidate(),
			dopeui.Data("confirm", s.Host.Troikas.DeleteConfirm(t.Name)), dopeui.Text(s.Host.Roster.DeleteBtn())))
	}
	buttons = append(buttons, dopeui.Button(dopeui.Data("dialog-close", ""), dopeui.Text(s.Host.Roster.CancelBtn())))
	fields = append(fields, dopeui.Row(append([]dopeui.Item{dopeui.SpaceSM, dopeui.Wrap()}, buttons...)...))
	return dopeui.Dialog(dopeui.ID(troikaDialogID(t.ID)),
		dopeui.Form(append([]dopeui.Item{dopeui.DirCol, dopeui.Method("post"), dopeui.Action("/host/fest/" + ref + "/troikas"), dopeui.Autocomplete("off")}, fields...)...),
	)
}

// troikaPlacement reads the dialog's team and division selects.
func troikaPlacement(form map[string][]string) (*roster.AssembledPlacement, error) {
	get := func(key string) string {
		if v := form[key]; len(v) > 0 {
			return v[0]
		}
		return ""
	}
	teamID, err := idstr.Parse(strings.TrimSpace(get("head_team")))
	if err != nil || teamID < 0 {
		return nil, route.BadRequest("bad head team")
	}
	placement := &roster.AssembledPlacement{HeadTeamID: teamID}
	switch division := get("division"); division {
	case divisionFollowValue, "":
	case divisionNoneValue:
		none := ""
		placement.Division = &none
	default:
		placement.Division = &division
	}
	return placement, nil
}

func (s *Server) renderHostFestTroikas(w http.ResponseWriter, r *http.Request, festID int64) {
	s.renderHostFestTroikasWith(w, r, festID, hostTroikasData{})
}

// renderHostFestTroikasWith draws the page; problems are what the save that
// led here reported per Game, which a plain view cannot know.
func (s *Server) renderHostFestTroikasWith(w http.ResponseWriter, r *http.Request, festID int64, data hostTroikasData, problems ...entrants.DivisionGame) {
	s.festPage(w, r, festID, func(fest view.HostFest) (*dopeui.Doc, error) {
		db := s.h.Engine().DB
		troikas, err := roster.LoadAssembled(r.Context(), db, festID)
		if err != nil {
			return nil, err
		}
		players, err := roster.LoadFestPlayerChoices(r.Context(), db, festID)
		if err != nil {
			return nil, err
		}
		teams, err := roster.LoadFestTeamChoices(r.Context(), db, festID)
		if err != nil {
			return nil, err
		}
		divisionGames, err := entrants.LoadDivisionGames(r.Context(), db, festID)
		if err != nil {
			return nil, err
		}
		for i := range divisionGames {
			for _, reported := range problems {
				if reported.GameID == divisionGames[i].GameID {
					divisionGames[i].Problem = reported.Problem
				}
			}
		}
		data.Fest, data.Troikas, data.Players, data.Teams, data.Games = fest, troikas, players, teams, divisionGames
		return hostTroikasDoc(data), nil
	})
}

// AddTroikas adds troikas from pasted lines, the troikas page's grammar, and
// says how many it added. Like every troika write it re-seats the Troika Games
// that take a division and tells the open Troika pages; it returns those Games
// so the page can say which of them could not follow.
func (s *Server) AddTroikas(ctx context.Context, festID int64, lines string) (int, []entrants.DivisionGame, error) {
	inputs, err := roster.ParseAssembledLines(lines)
	if err != nil {
		return 0, nil, err
	}
	synced, err := s.troikaWrite(ctx, festID, "add", 0, func(ctx context.Context, tx *sql.Tx) error {
		for _, in := range inputs {
			if _, err := roster.SaveAssembledTx(ctx, tx, festID, 0, in); err != nil {
				return err
			}
		}
		return nil
	})
	return len(inputs), synced, err
}

// SaveTroika renames one troika and sets its players, and its head team and
// division when in.Placement says so.
func (s *Server) SaveTroika(ctx context.Context, festID, id int64, in roster.AssembledInput) ([]entrants.DivisionGame, error) {
	return s.troikaWrite(ctx, festID, "edit", 0, func(ctx context.Context, tx *sql.Tx) error {
		_, err := roster.SaveAssembledTx(ctx, tx, festID, id, in)
		return err
	})
}

// DeleteTroika deletes a troika no game seats. The Games that take a division
// let go of it first, so a troika only they seat can be deleted.
func (s *Server) DeleteTroika(ctx context.Context, festID, id int64) ([]entrants.DivisionGame, error) {
	return s.troikaWrite(ctx, festID, "delete", id, func(ctx context.Context, tx *sql.Tx) error {
		_, err := entrants.DeleteTroikaTx(ctx, tx, festID, id)
		return err
	})
}

// troikaWrite runs one troika write, then re-seats the Troika Games that take
// a division (exclude leaves out a troika being deleted) and tells the open
// Troika pages, whose seat rosters it may have changed.
func (s *Server) troikaWrite(ctx context.Context, festID int64, label string, exclude int64, fn func(ctx context.Context, tx *sql.Tx) error) ([]entrants.DivisionGame, error) {
	var synced []entrants.DivisionGame
	var revision int64
	err := s.h.Engine().WithWriteTx(ctx, festID, "troikas-"+label, func(ctx context.Context, tx *sql.Tx) error {
		if err := fn(ctx, tx); err != nil {
			return err
		}
		var err error
		if synced, err = entrants.FollowDivisionsTx(ctx, tx, festID, exclude); err != nil {
			return err
		}
		revision, err = festwrite.BumpFestRevisionTx(ctx, tx, festID, "troikas:"+label, util.MustJSON(map[string]any{"label": label}))
		return err
	})
	if err != nil {
		return nil, err
	}
	s.h.Engine().InvalidateFestViewCache(festID)
	s.broadcastTroikaGames(ctx, festID, revision)
	return synced, nil
}

// handleHostSaveTroikas takes the page's three forms: pasted lines, one
// troika's edit, and its delete.
func (s *Server) handleHostSaveTroikas(w http.ResponseWriter, r *http.Request, festID int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	str := dopestrings.Default
	fail := func(err error, lines string) {
		message, ok := corei18n.AsUser(err)
		if !ok {
			route.WriteError(w, r, err)
			return
		}
		s.renderHostFestTroikasWith(w, r, festID, hostTroikasData{Error: message, Lines: lines})
	}
	switch r.Form.Get("mode") {
	case "lines":
		lines := r.Form.Get("lines")
		added, synced, err := s.AddTroikas(r.Context(), festID, lines)
		if err != nil {
			fail(err, lines)
			return
		}
		s.renderHostFestTroikasWith(w, r, festID, hostTroikasData{Notice: str.Host.Troikas.AddedNotice(added)}, synced...)
	case "edit":
		id, err := idstr.Parse(r.Form.Get("id"))
		if err != nil || id <= 0 {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		if r.Form.Get("delete") != "" {
			synced, err := s.DeleteTroika(r.Context(), festID, id)
			if err != nil {
				fail(err, "")
				return
			}
			s.renderHostFestTroikasWith(w, r, festID, hostTroikasData{Notice: str.Host.Troikas.DeletedNotice()}, synced...)
			return
		}
		placement, err := troikaPlacement(r.Form)
		if err != nil {
			route.WriteError(w, r, err)
			return
		}
		in := roster.AssembledInput{Name: r.Form.Get("name"), Players: r.Form["player"], Placement: placement}
		if raw := strings.TrimSpace(r.Form.Get("applied")); raw != "" {
			applied, err := strconv.Atoi(raw)
			if err != nil || applied < 1 {
				fail(corei18n.User(str.Host.Troikas.AppliedInvalid()), "")
				return
			}
			in.Applied = applied
		}
		synced, err := s.SaveTroika(r.Context(), festID, id, in)
		if err != nil {
			fail(err, "")
			return
		}
		s.renderHostFestTroikasWith(w, r, festID, hostTroikasData{Notice: str.Host.Troikas.SavedNotice()}, synced...)
	default:
		http.Error(w, "bad mode", http.StatusBadRequest)
	}
}

// broadcastTroikaGames tells every open Troika page of the fest to fetch its
// bouts again: a troika's people, or a Game's entrants, may have changed.
func (s *Server) broadcastTroikaGames(ctx context.Context, festID, revision int64) {
	ids, err := entrants.TroikaGameIDs(ctx, s.h.Engine().DB, festID)
	if err != nil {
		return
	}
	for _, gameID := range ids {
		s.h.BroadcastFestView(festID, gameID, revision)
	}
}
