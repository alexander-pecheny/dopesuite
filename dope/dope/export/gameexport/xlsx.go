package gameexport

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/xuri/excelize/v2"

	"dope/dope/export/xlsxexport"
	"dope/dope/storage/store"
)

// XLSX export of a game's tables for archival. OD games export in the
// rating.chgk.info "tournament-tours" layout (one sheet, per-tour blocks of
// 0/1 cells). KSI and EK export "as they look" — one sheet per in-app view tab,
// pure values, no formulas. Answer cells, which are color-only on screen, are
// rendered as the signed point value the color stands for (+10 / -10 / blank).

// HandleScopedGameExport serves GET /api/fest/{fid}/games/{gid}/export.xlsx.
// Gated by read access — anyone who can view the fest can download the archive.
func HandleScopedGameExport(s Host, w http.ResponseWriter, r *http.Request, festID, gameID int64) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.AuthorizeFestRead(w, r, festID) {
		return
	}

	var gameType, schemeJSON, stateJSON string
	var gameSlug, festSlug sql.NullString
	err := s.DB().QueryRowContext(r.Context(), `
select g.game_type, g.slug, coalesce(g.scheme_json, ''), coalesce(g.state_json, ''), f.slug
from games g join fests f on f.id = g.fest_id
where g.fest_id = ? and g.id = ?`, festID, gameID).
		Scan(&gameType, &gameSlug, &schemeJSON, &stateJSON, &festSlug)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	f := excelize.NewFile()
	defer f.Close()

	switch gameType {
	case "od":
		var ratingByNumber map[int64]int64
		ratingByNumber, err = loadTeamRatingIDsByNumber(r.Context(), s.DB(), festID)
		if err == nil {
			err = xlsxexport.BuildODSheet(f, schemeJSON, stateJSON, ratingByNumber)
		}
	case "ksi", "si":
		err = xlsxexport.BuildKSISheets(f, stateJSON)
	case "ek":
		var stages []store.StageMatches
		if stages, err = s.LoadAllStageMatchViews(r.Context(), festID, gameID); err == nil {
			err = xlsxexport.BuildEKSheets(f, stages)
		}
	default:
		http.Error(w, "export not supported for this game type", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// excelize seeds a default "Sheet1"; drop it if our builders added their own.
	if f.SheetCount > 1 {
		_ = f.DeleteSheet("Sheet1")
	}

	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	stem := ExportFileStem(festSlug.String, festID, gameSlug.String, gameID)
	w.Header().Set("Content-Disposition", ContentDispositionAttachment(stem+".xlsx"))
	if err := f.Write(w); err != nil {
		// Headers may already be flushed; nothing useful to send to the client.
		return
	}
}

// ExportFileStem returns "festPart-gamePart" for use as a download filename
// stem. If slug is non-empty it is used verbatim; otherwise "fest"/"game" is
// concatenated with the numeric ID as a stable fallback.
func ExportFileStem(festSlug string, festID int64, gameSlug string, gameID int64) string {
	festPart := festSlug
	if festPart == "" {
		festPart = fmt.Sprintf("fest%d", festID)
	}
	gamePart := gameSlug
	if gamePart == "" {
		gamePart = fmt.Sprintf("game%d", gameID)
	}
	return festPart + "-" + gamePart
}

// ContentDispositionAttachment builds an RFC 6266 header carrying both an ASCII
// fallback and a UTF-8 (filename*) form, so Cyrillic titles survive the trip.
func ContentDispositionAttachment(name string) string {
	ascii := strings.Map(func(r rune) rune {
		if r < 32 || r > 126 {
			return '_'
		}
		return r
	}, name)
	return fmt.Sprintf("attachment; filename=%q; filename*=UTF-8''%s", ascii, url.PathEscape(name))
}

// --- OD: rating.chgk.info "tournament-tours" layout ---------------------------

// loadTeamRatingIDsByNumber returns a map from team number to rating.chgk.info ID
// for all non-deleted, numbered teams in the given fest. Teams without a rating_id
// are omitted (caller falls back to the internal number).
func loadTeamRatingIDsByNumber(ctx context.Context, db *sql.DB, festID int64) (map[int64]int64, error) {
	rows, err := db.QueryContext(ctx,
		`select number, rating_id from fest_teams where fest_id = ? and deleted = 0 and number is not null and rating_id is not null`,
		festID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := make(map[int64]int64)
	for rows.Next() {
		var num, rid int64
		if err := rows.Scan(&num, &rid); err != nil {
			return nil, err
		}
		m[num] = rid
	}
	return m, rows.Err()
}
