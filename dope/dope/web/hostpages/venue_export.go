package hostpages

import (
	"context"
	"net/http"
	"strconv"

	"github.com/xuri/excelize/v2"

	"dope/dope/domain/games"
	"dope/dope/domain/venues"
	"dope/dope/export/gameexport"
	"dope/dope/export/xlsxexport"
	"dope/dope/storage/store"
	"dope/dope/web/route"
)

func (s *Server) handleSlotToursExport(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	festID := sc.FestID
	_, slot, err := s.slotOf(r, festID)
	if err != nil {
		return err
	}
	doc, err := store.LoadGameDoc(r.Context(), s.h.Engine().DB, festID, slot.GameID)
	if err != nil {
		return err
	}
	ratingByNumber, _, err := s.acceptedByNumber(r.Context(), slot)
	if err != nil {
		return err
	}
	state := string(store.WithContestedFor(r.Context(), s.h.Engine().DB, slot.GameID, []byte(doc.State)))
	f := excelize.NewFile()
	defer f.Close()
	if err := xlsxexport.BuildODSheet(f, doc.SchemeJSON, state, ratingByNumber); err != nil {
		return err
	}
	return writeWorkbook(w, f, slotFileStem(slot)+"-tours")
}

func (s *Server) handleSlotPlayersExport(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	festID := sc.FestID
	venue, slot, err := s.slotOf(r, festID)
	if err != nil {
		return err
	}
	doc, err := store.LoadGameDoc(r.Context(), s.h.Engine().DB, festID, slot.GameID)
	if err != nil {
		return err
	}
	results, err := games.ComputeODResults(doc.SchemeJSON, doc.State)
	if err != nil {
		return err
	}
	placeByNumber := map[int64]string{}
	for _, team := range results.Teams {
		placeByNumber[team.Number] = team.Place
	}
	_, apps, err := s.acceptedByNumber(r.Context(), slot)
	if err != nil {
		return err
	}
	registryCity, err := venues.RegistryCities(r.Context(), s.h.Engine().DB, festID)
	if err != nil {
		return err
	}
	rows := make([]xlsxexport.ODPlayerRow, 0, len(apps)*6)
	for _, app := range apps {
		city := registryCity[app.RatingTeamID]
		if city == "" {
			city = venue.City
		}
		flags := s.rosterFlags(r.Context(), app, slot)
		for i, p := range app.Roster {
			flag := ""
			if i < len(flags) {
				flag = flags[i]
			}
			rows = append(rows, xlsxexport.ODPlayerRow{
				Place: placeByNumber[app.Number], TeamID: app.RatingTeamID, Name: app.TeamName,
				City: city, Flag: flag, PlayerID: p.PlayerID,
				Surname: p.Surname, FirstName: p.Name, Patronymic: p.Patronymic,
			})
		}
	}
	f := excelize.NewFile()
	defer f.Close()
	if err := xlsxexport.BuildODPlayersSheet(f, rows); err != nil {
		return err
	}
	return writeWorkbook(w, f, slotFileStem(slot)+"-players")
}

func (s *Server) acceptedByNumber(ctx context.Context, slot venues.Slot) (map[int64]int64, []venues.Application, error) {
	apps, err := venues.SlotApplications(ctx, s.h.Engine().DB, slot.ID)
	if err != nil {
		return nil, nil, err
	}
	ratingByNumber := map[int64]int64{}
	accepted := make([]venues.Application, 0, len(apps))
	for _, app := range apps {
		if app.Status != venues.StatusAccepted || app.Number <= 0 {
			continue
		}
		if app.RatingTeamID > 0 {
			ratingByNumber[app.Number] = app.RatingTeamID
		}
		accepted = append(accepted, app)
	}
	return ratingByNumber, accepted, nil
}

func slotFileStem(slot venues.Slot) string {
	if slot.StartsAt != "" {
		return "slot-" + slot.StartsAt[:10]
	}
	return "slot-" + strconv.FormatInt(slot.ID, 10)
}

func writeWorkbook(w http.ResponseWriter, f *excelize.File, stem string) error {
	if f.SheetCount > 1 {
		_ = f.DeleteSheet("Sheet1")
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", gameexport.ContentDispositionAttachment(stem+".xlsx"))
	_ = f.Write(w)
	return nil
}
