package hostpages

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"

	"dope/dope/domain/core"
	"dope/dope/domain/games"
	"dope/dope/storage/store"
	"dope/dope/web/route"
	ui "dope/dope/web/ui"
)

// The Слот page's Спорные list: what the ОД page recorded from Ввод, with the
// «Принят на площадке» toggle and the delete a host reaches for when an answer
// was simply wrong.

// ContestedRow is one спорный as the Слот page shows it.
type ContestedRow struct {
	store.ContestedAnswer
	Tour     int
	InTour   int
	TeamName string
}

// contestedRows numbers each спорный by tour and by its place in that tour,
// and names the team its Number seats.
func contestedRows(list []store.ContestedAnswer, schemeJSON, stateJSON string) []ContestedRow {
	tours := games.ParseTourComp(schemeJSON)
	var state games.ODState
	_ = json.Unmarshal([]byte(stateJSON), &state)
	names := map[int64]string{}
	for _, t := range state.Teams {
		names[t.Number] = t.Name
	}
	out := make([]ContestedRow, 0, len(list))
	for _, c := range list {
		row := ContestedRow{ContestedAnswer: c, Tour: 1, InTour: c.Question + 1, TeamName: names[c.Number]}
		left := c.Question
		for i, size := range tours {
			if left < size {
				row.Tour, row.InTour = i+1, left+1
				break
			}
			left -= size
		}
		out = append(out, row)
	}
	return out
}

func slotContestedSection(data slotPageData) *ui.Element {
	base := slotBase(data.Venue, data.Slot)
	sect := []ui.Item{ui.Subhead(ui.Text("Спорные"))}
	if len(data.Contested) == 0 {
		return ui.Section(append(sect, ui.Empty(ui.Text("Спорных нет.")))...)
	}
	table := []ui.Item{ui.Scroll(), ui.Trow(
		ui.Hcell(ui.Text("Тур")), ui.Hcell(ui.Text("Вопрос")), ui.Hcell(ui.Text("Команда")),
		ui.Hcell(ui.Text("Ответ")), ui.Hcell(ui.Text("Принят")), ui.Hcell(ui.Text("")),
	)}
	for _, row := range data.Contested {
		question := strconv.Itoa(row.Question)
		number := strconv.FormatInt(row.Number, 10)
		fields := []ui.Item{
			ui.Hiddenfield(ui.Name("question"), ui.Value(question)),
			ui.Hiddenfield(ui.Name("number"), ui.Value(number)),
		}
		toggle, accepted := "Принять на площадке", "1"
		if row.AcceptedHere {
			toggle, accepted = "Снять принятие", "0"
		}
		actions := ui.Cell(ui.Text(""))
		state := ui.Cell(ui.Text(contestedStateLabel(row.AcceptedHere)))
		if data.CanManage {
			state = ui.Cell(ui.Form(append([]ui.Item{ui.Method("post"), ui.Action(base + "/contested/accept")},
				append(fields, ui.Button(ui.Ghost, ui.Small(), ui.Submit(), ui.Name("accepted"), ui.Value(accepted),
					ui.Text(toggle)))...)...))
			actions = ui.Cell(ui.Form(append([]ui.Item{ui.Method("post"), ui.Action(base + "/contested/delete"),
				ui.Data("confirm", "Удалить спорный?")},
				append(fields, ui.Button(ui.Danger, ui.Small(), ui.Submit(), ui.Text("Удалить")))...)...))
		}
		table = append(table, ui.Trow(
			ui.Cell(ui.Text(strconv.Itoa(row.Tour))),
			ui.Cell(ui.Text(strconv.Itoa(row.InTour))),
			ui.Cell(ui.Text(joinDots(number, row.TeamName))),
			ui.Cell(ui.Text(row.Answer)),
			state,
			actions,
		))
	}
	return ui.Section(append(sect, ui.Table(table...))...)
}

func contestedStateLabel(accepted bool) string {
	if accepted {
		return "принят на площадке"
	}
	return "ждёт жюри"
}

func (s *Server) loadContestedRows(ctx context.Context, festID, gameID int64) ([]ContestedRow, error) {
	list, err := store.LoadContested(ctx, s.h.Engine().DB, gameID)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, nil
	}
	doc, err := store.LoadGameDoc(ctx, s.h.Engine().DB, festID, gameID)
	if err != nil {
		return nil, err
	}
	return contestedRows(list, doc.SchemeJSON, doc.State), nil
}

// contestedWrite runs one host action on a спорный and rebroadcasts the Слот's
// document so every open page sees it.
func (s *Server) contestedWrite(w http.ResponseWriter, r *http.Request, festID int64,
	apply func(ctx context.Context, tx *sql.Tx, question int, number int64) error) error {
	_, slot, err := s.slotOf(r, festID)
	if err != nil {
		return err
	}
	if err := r.ParseForm(); err != nil {
		return route.BadRequest("bad form")
	}
	question := int(formInt64(r.Form, "question"))
	number := formInt64(r.Form, "number")
	if number <= 0 {
		return route.BadRequest("нужен номер команды")
	}
	if err := s.h.Engine().WithWriteTx(r.Context(), festID, "contested", func(ctx context.Context, tx *sql.Tx) error {
		return apply(ctx, tx, question, number)
	}); err != nil {
		return s.renderSlotPage(w, r, festID, err.Error(), "")
	}
	if doc, err := store.LoadGameDoc(r.Context(), s.h.Engine().DB, festID, slot.GameID); err == nil {
		var revision int64
		_ = s.h.Engine().DB.QueryRowContext(r.Context(), `select revision from games where id = ?`, slot.GameID).Scan(&revision)
		s.h.Engine().BroadcastState(festID, core.GameStateScope(slot.GameID), revision, []byte(doc.State))
	}
	return s.redirectToSlot(w, r, festID, slot.ID)
}

func (s *Server) handleContestedAccept(w http.ResponseWriter, r *http.Request, festID int64) error {
	_, slot, err := s.slotOf(r, festID)
	if err != nil {
		return err
	}
	accepted := r.FormValue("accepted") == "1"
	return s.contestedWrite(w, r, festID, func(ctx context.Context, tx *sql.Tx, question int, number int64) error {
		return store.SetContestedAcceptedTx(ctx, tx, festID, slot.GameID, question, number, accepted)
	})
}

func (s *Server) handleContestedDelete(w http.ResponseWriter, r *http.Request, festID int64) error {
	_, slot, err := s.slotOf(r, festID)
	if err != nil {
		return err
	}
	return s.contestedWrite(w, r, festID, func(ctx context.Context, tx *sql.Tx, question int, number int64) error {
		return store.DeleteContestedTx(ctx, tx, festID, slot.GameID, question, number)
	})
}
