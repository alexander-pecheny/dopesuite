package hostpages

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"strings"

	"dope/dope/domain/roster"
	"dope/dope/domain/view"
	"dope/dope/web/pages"
	"dope/dope/web/route"
	dopeui "dope/dope/web/ui"
	dopestrings "dope/i18nstrings"

	corei18n "pecheny.me/dopecore/i18nstrings"
)

// The fest's troikas (CONTEXT.md, Assembled team): Participants assembled out of
// fest players for Troika, not drawn from the rating roster. The page lists
// them with their people and the team each is credited to, adds many at once
// from pasted lines, and edits one in a dialog — which is how a substitution
// between bouts is made.

type hostTroikasData struct {
	Fest    view.HostFest
	Troikas []roster.Assembled
	Players []roster.FestPlayerChoice
	Lines   string
	Error   string
	Notice  string
}

// troikaPlayerFields is how many name fields the edit dialog offers: the most
// a troika may declare.
const troikaPlayerFields = roster.AssembledMax

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
			dopeui.Hcell(dopeui.Text(s.Host.Troikas.ColName())),
			dopeui.Hcell(dopeui.Text(s.Host.Troikas.ColPlayers())),
			dopeui.Hcell(dopeui.Text(s.Host.Troikas.ColTeam())),
			dopeui.Hcell(),
		)}
		for _, t := range data.Troikas {
			rows = append(rows, dopeui.Trow(
				dopeui.Cell(dopeui.Text(t.Name)),
				dopeui.Cell(dopeui.Text(strings.Join(t.Players, ", "))),
				dopeui.Cell(dopeui.Text(t.Team)),
				dopeui.Cell(dopeui.Iconbtn(dopeui.IconPencil, dopeui.Label(s.Host.Troikas.EditLabel()), dopeui.Data("dialog-open", troikaDialogID(t.ID)))),
			))
		}
		page = append(page, dopeui.Table(append([]dopeui.Item{dopeui.Scroll()}, rows...)...))
		for _, t := range data.Troikas {
			page = append(page, hostTroikaDialog(ref, t))
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

func troikaDialogID(id int64) string { return "troika-" + strconv.FormatInt(id, 10) }

// hostTroikaDialog edits one troika: its name and up to four people, each a
// field that suggests the fest's players. Delete is there only while no Game
// seats it.
func hostTroikaDialog(ref string, t roster.Assembled) *dopeui.Element {
	s := dopestrings.Default
	fields := []dopeui.Item{
		dopeui.Subhead(dopeui.Text(t.Name)),
		dopeui.Hiddenfield(dopeui.Name("mode"), dopeui.Value("edit")),
		dopeui.Hiddenfield(dopeui.Name("id"), dopeui.Value(strconv.FormatInt(t.ID, 10))),
		dopeui.Field(dopeui.Label(s.Host.Troikas.ColName()),
			dopeui.Textfield(dopeui.Name("name"), dopeui.Value(t.Name), dopeui.Required())),
	}
	for i := 0; i < troikaPlayerFields; i++ {
		value := ""
		if i < len(t.Players) {
			value = t.Players[i]
		}
		fields = append(fields, dopeui.Field(dopeui.Label(s.Host.Troikas.PlayerN(strconv.Itoa(i+1))),
			dopeui.Textfield(dopeui.Name("player"), dopeui.Value(value), dopeui.InputList("troikaPlayers"))))
	}
	buttons := []dopeui.Item{dopeui.Button(dopeui.Submit(), dopeui.Text(s.Host.Roster.SaveSubmit()))}
	if !t.Seated {
		buttons = append(buttons, dopeui.Button(dopeui.Danger, dopeui.Submit(), dopeui.Name("delete"), dopeui.Value("1"), dopeui.Formnovalidate(),
			dopeui.Data("confirm", s.Host.Troikas.DeleteConfirm(t.Name)), dopeui.Text(s.Host.Roster.DeleteBtn())))
	}
	buttons = append(buttons, dopeui.Button(dopeui.Data("dialog-close", ""), dopeui.Text(s.Host.Roster.CancelBtn())))
	fields = append(fields, dopeui.Row(buttons...))
	return dopeui.Dialog(dopeui.ID(troikaDialogID(t.ID)),
		dopeui.Form(append([]dopeui.Item{dopeui.DirCol, dopeui.Method("post"), dopeui.Action("/host/fest/" + ref + "/troikas"), dopeui.Autocomplete("off")}, fields...)...),
	)
}

func (s *Server) renderHostFestTroikas(w http.ResponseWriter, r *http.Request, festID int64) {
	s.renderHostFestTroikasWith(w, r, festID, hostTroikasData{})
}

func (s *Server) renderHostFestTroikasWith(w http.ResponseWriter, r *http.Request, festID int64, data hostTroikasData) {
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
		data.Fest, data.Troikas, data.Players = fest, troikas, players
		return hostTroikasDoc(data), nil
	})
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
	write := func(label string, fn func(ctx context.Context, tx *sql.Tx) error) error {
		err := s.h.Engine().WithWriteTx(r.Context(), festID, label, fn)
		if err == nil {
			s.h.Engine().InvalidateFestViewCache(festID)
		}
		return err
	}
	switch r.Form.Get("mode") {
	case "lines":
		lines := r.Form.Get("lines")
		inputs, err := roster.ParseAssembledLines(lines)
		if err != nil {
			fail(err, lines)
			return
		}
		if err := write("troikas-add", func(ctx context.Context, tx *sql.Tx) error {
			for _, in := range inputs {
				if _, err := roster.SaveAssembledTx(ctx, tx, festID, 0, in); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			fail(err, lines)
			return
		}
		s.renderHostFestTroikasWith(w, r, festID, hostTroikasData{Notice: str.Host.Troikas.AddedNotice(len(inputs))})
	case "edit":
		id, err := strconv.ParseInt(r.Form.Get("id"), 10, 64)
		if err != nil || id <= 0 {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		if r.Form.Get("delete") != "" {
			if err := write("troika-delete", func(ctx context.Context, tx *sql.Tx) error {
				return roster.DeleteAssembledTx(ctx, tx, festID, id)
			}); err != nil {
				fail(err, "")
				return
			}
			s.renderHostFestTroikasWith(w, r, festID, hostTroikasData{Notice: str.Host.Troikas.DeletedNotice()})
			return
		}
		in := roster.AssembledInput{Name: r.Form.Get("name"), Players: r.Form["player"]}
		if err := write("troika-edit", func(ctx context.Context, tx *sql.Tx) error {
			_, err := roster.SaveAssembledTx(ctx, tx, festID, id, in)
			return err
		}); err != nil {
			fail(err, "")
			return
		}
		s.renderHostFestTroikasWith(w, r, festID, hostTroikasData{Notice: str.Host.Troikas.SavedNotice()})
	default:
		http.Error(w, "bad mode", http.StatusBadRequest)
	}
}
