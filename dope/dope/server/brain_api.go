package dopeserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"dope/dope/storage/festwrite"
	"dope/dope/storage/store"
)

// Брейн бой API: GET a бой's protocol and POST edits to it. A бой has no themes,
// so it does not use the EK /matches endpoints; it reads/writes match_questions
// and, on each edit, recomputes match_results and rebroadcasts the fest view so
// every group cross-table updates live.
//
// Routes (under /api/fest/{fid}/games/{gid}):
//   GET  bouts/{code}          — the бой protocol
//   POST bouts/{code}/update   — set a question's mark and/or answering player
//   POST bouts/{code}/finish   — mark the бой finished/reopened

type brainBoutEdit struct {
	Team     int    `json:"team"`     // slot index (0/1)
	Question int    `json:"question"` // question index
	Mark     string `json:"mark"`     // "right" | "wrong" | "" (set when present)
	SetMark  bool   `json:"setMark"`
	Player   int64  `json:"player"` // player_id (0 clears) when SetPlayer
	SetPlyr  bool   `json:"setPlayer"`
	Finished *bool  `json:"finished,omitempty"`
}

func (s *server) handleScopedBouts(w http.ResponseWriter, r *http.Request, scope festScope, sub []string) {
	if len(sub) == 0 || sub[0] == "" {
		http.NotFound(w, r)
		return
	}
	code := sub[0]
	action := ""
	if len(sub) > 1 {
		action = sub[1]
	}

	if action == "" {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !s.authorizeFestRead(w, r, scope.FestID) {
			return
		}
		bout, err := store.LoadBrainMatch(r.Context(), s.eng.DB, scope.FestID, code)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSONValue(w, bout)
		return
	}

	if action != "update" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, ok := s.requireFestTableEditor(w, r, scope.FestID); !ok {
		return
	}
	if !s.requireNumberedTeams(w, r, scope.FestID) {
		return
	}
	defer r.Body.Close()
	var edit brainBoutEdit
	if err := json.NewDecoder(r.Body).Decode(&edit); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	bout, revision, err := s.applyBrainBoutEdit(r.Context(), scope, code, edit)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.broadcastFestView(scope, revision)
	writeJSONValue(w, bout)
}

func (s *server) applyBrainBoutEdit(reqCtx context.Context, scope festScope, code string, edit brainBoutEdit) (store.BrainMatch, int64, error) {
	ctx, cancel := festwrite.AuditDetachedContext(reqCtx, scope.FestID)
	defer cancel()
	conn, err := s.eng.AcquireWriteConn(ctx, "brain-bout-update")
	if err != nil {
		return store.BrainMatch{}, 0, err
	}
	defer conn.Close()
	defer s.eng.LockWrite("brain-bout-update")()

	tx, err := s.eng.BeginWriteTxConn(ctx, conn)
	if err != nil {
		return store.BrainMatch{}, 0, err
	}
	defer tx.Rollback()

	var matchID int64
	if err := tx.QueryRowContext(ctx, `select id from matches where fest_id = ? and game_id = ? and code = ?`,
		scope.FestID, scope.GameID, code).Scan(&matchID); err != nil {
		return store.BrainMatch{}, 0, err
	}

	bout, err := store.LoadBrainMatch(ctx, tx, scope.FestID, code)
	if err != nil {
		return store.BrainMatch{}, 0, err
	}
	if edit.Finished == nil && bout.Finished {
		return store.BrainMatch{}, 0, errors.New("бой завершён")
	}
	if edit.Team < 0 || edit.Team >= len(bout.Teams) {
		return store.BrainMatch{}, 0, errors.New("bad team")
	}
	teamID := bout.Teams[edit.Team].TeamID

	if edit.Finished != nil {
		status := "active"
		if *edit.Finished {
			status = "finished"
		}
		if _, err := tx.ExecContext(ctx, `update matches set status = ? where id = ?`, status, matchID); err != nil {
			return store.BrainMatch{}, 0, err
		}
	} else {
		if teamID == 0 {
			return store.BrainMatch{}, 0, errors.New("slot is empty")
		}
		if edit.Question < 0 || edit.Question >= bout.QuestionCount {
			return store.BrainMatch{}, 0, errors.New("bad question")
		}
		if edit.SetMark {
			if err := store.SetBrainQuestionMarkTx(ctx, tx, matchID, teamID, edit.Question, edit.Mark); err != nil {
				return store.BrainMatch{}, 0, err
			}
		}
		if edit.SetPlyr {
			if err := store.SetBrainQuestionPlayerTx(ctx, tx, matchID, teamID, edit.Question, edit.Player); err != nil {
				return store.BrainMatch{}, 0, err
			}
		}
	}

	updated, err := store.LoadBrainMatch(ctx, tx, scope.FestID, code)
	if err != nil {
		return store.BrainMatch{}, 0, err
	}
	if err := store.RecalculateBrainMatchResultsTx(ctx, tx, updated); err != nil {
		return store.BrainMatch{}, 0, err
	}
	revision, err := festwrite.BumpFestRevisionTx(ctx, tx, scope.FestID, "brain:bout-update", "{}")
	if err != nil {
		return store.BrainMatch{}, 0, err
	}
	if err := tx.Commit(); err != nil {
		return store.BrainMatch{}, 0, err
	}
	updated.Revision = revision
	return updated, revision, nil
}
