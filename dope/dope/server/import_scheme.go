package dopeserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"dope/dope/domain/gamebuild"
	"dope/dope/storage/festwrite"
	"dope/dope/storage/store"
)

func (s *server) handleImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	festID, err := store.ResolveFestID(r.Context(), s.eng.DB, strings.TrimSpace(r.URL.Query().Get("fest_id")))
	if err != nil || festID <= 0 {
		http.Error(w, "missing fest_id", http.StatusBadRequest)
		return
	}
	if _, ok := s.requireFestAdmin(w, r, festID); !ok {
		return
	}
	defer r.Body.Close()

	var scheme store.FestScheme
	if err := json.NewDecoder(r.Body).Decode(&scheme); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}

	if err := s.importSchemeIntoFest(r.Context(), festID, scheme); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	gameID, err := defaultGameID(r.Context(), s.eng.DB, festID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	view, err := s.loadFestViewSnapshot(festID, gameID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data, _ := json.Marshal(view)
	s.eng.BroadcastState(festID, "fest", view.Revision, data)
	writeJSON(w, data)
}

// importSchemeIntoFest wipes the fest's existing games (and dependent rows)
// and materialises a single new game from the pasted scheme — the ADR-0006
// escape hatch. The fest row itself stays intact.
func (s *server) importSchemeIntoFest(ctx context.Context, festID int64, scheme store.FestScheme) error {
	if s.eng.DB == nil {
		return errors.New("sqlite is not enabled")
	}
	s.eng.Mu.Lock()
	defer s.eng.Mu.Unlock()

	tx, err := s.eng.BeginWriteTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Skip per-row audit churn for the wipe-and-rebuild; the import is recorded as
	// one 'import' event below and acts as a revert boundary. See festwrite.SuppressAuditTx.
	if err := festwrite.SuppressAuditTx(ctx, tx); err != nil {
		return err
	}
	if err := clearFestImportData(ctx, tx, festID); err != nil {
		return err
	}
	if _, err := gamebuild.Materialise(ctx, tx, festID, scheme); err != nil {
		return err
	}
	schemaJSON, err := json.Marshal(scheme)
	if err != nil {
		return err
	}
	if _, err := festwrite.BumpFestRevisionTx(ctx, tx, festID, "import", string(schemaJSON)); err != nil {
		return err
	}
	return tx.Commit()
}

// clearFestImportData drops all per-fest rows that an import would
// recreate (games, stages, matches, venues, teams, players, journal). The
// fest row and its organizers stay.
func clearFestImportData(ctx context.Context, tx *sql.Tx, festID int64) error {
	statements := []string{
		`delete from journal where fest_id = ?`,
		`delete from games where fest_id = ?`,
		`delete from participant_players where participant_id in (select id from participants where fest_id = ?)`,
		`delete from participants where fest_id = ?`,
		`delete from players where fest_id = ?`,
		`delete from venues where fest_id = ?`,
	}
	for _, sqlText := range statements {
		if _, err := tx.ExecContext(ctx, sqlText, festID); err != nil {
			return err
		}
	}
	return nil
}
