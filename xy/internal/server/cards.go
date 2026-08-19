package server

import (
	"context"
	"database/sql"
	"net/http"
	"time"
)

type cardDTO struct {
	ID             int64   `json:"id"`
	ListID         int64   `json:"list_id"`
	Kind           string  `json:"kind"`
	DescEnc        string  `json:"description_enc"`
	Rank           string  `json:"rank"`
	HandoutMetaEnc *string `json:"handout_meta_enc,omitempty"` // nil = no saved handout settings
	AliasEnc       *string `json:"alias_enc,omitempty"`        // nil = no alias
	// CreatedAt anchors the лента: the client shows it as a «карточка создана»
	// line under the oldest event, so every later timestamp has something to be
	// read against. Deliberately NOT a timeline event — the column already
	// exists on every card ever made, where an event would only cover new ones.
	CreatedAt string `json:"created_at"`
}

// cardLabelDTO is one label ASSIGNMENT. SessionID nil = the author's own view of
// the question; set = what the testers thought at that sitting. The same label
// may appear twice on one card, once each way.
type cardLabelDTO struct {
	CardID    int64  `json:"card_id"`
	LabelID   int64  `json:"label_id"`
	SessionID *int64 `json:"session_id,omitempty"`
}

// tourTesterDTO is one session named by one tour's Declaration.
// tourTesterDTO is one session a tour's Declaration names — or, with a null
// SessionID, the marker that the tour declared and names nobody.
type tourTesterDTO struct {
	ListID    *int64 `json:"list_id,omitempty"`
	GroupID   *int64 `json:"group_id,omitempty"`
	SessionID *int64 `json:"session_id"`
}

// cardSessionDTO is one Playing: this question was played at that test.
type cardSessionDTO struct {
	CardID    int64 `json:"card_id"`
	SessionID int64 `json:"session_id"`
}

func scanCards(ctx context.Context, q querier, boardID int64) ([]cardDTO, error) {
	rows, err := q.QueryContext(ctx, `
select id, list_id, kind, description_enc, rank, handout_meta_enc, alias_enc, created_at
from cards where board_id = ? and deleted_at is null order by rank`, boardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []cardDTO{}
	for rows.Next() {
		var c cardDTO
		var descEnc, metaEnc, aliasEnc []byte
		if err := rows.Scan(&c.ID, &c.ListID, &c.Kind, &descEnc, &c.Rank, &metaEnc, &aliasEnc, &c.CreatedAt); err != nil {
			return nil, err
		}
		c.DescEnc = b64(descEnc)
		if metaEnc != nil {
			s := b64(metaEnc)
			c.HandoutMetaEnc = &s
		}
		if aliasEnc != nil {
			s := b64(aliasEnc)
			c.AliasEnc = &s
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

type createCardRequest struct {
	DescEnc        string  `json:"description_enc"`
	Rank           string  `json:"rank"`
	Kind           string  `json:"kind"`
	HandoutMetaEnc *string `json:"handout_meta_enc"` // optional handout-gen settings
	AliasEnc       *string `json:"alias_enc"`        // optional short label shown instead of the card's text
}

// validCardKind allowlists the card kinds the client may set (mirrors the
// cards.kind CHECK constraint).
func validCardKind(kind string) bool {
	switch kind {
	case "normal", "question", "test", "meta", "heading", "other":
		return true
	}
	return false
}

func (s *server) handleCreateCard(w http.ResponseWriter, r *http.Request) {
	_, listID, bid, ok := s.requireChildAccess(w, r, childList)
	if !ok {
		return
	}
	var req createCardRequest
	if !readJSON(w, r, &req) {
		return
	}
	descEnc, err := unb64(req.DescEnc)
	if err != nil || req.Rank == "" {
		httpError(w, http.StatusBadRequest, "invalid card fields")
		return
	}
	kind := req.Kind
	if kind == "" {
		kind = "normal"
	}
	if !validCardKind(kind) {
		httpError(w, http.StatusBadRequest, "bad card kind")
		return
	}
	metaEnc, err := optBlob(req.HandoutMetaEnc)
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid handout_meta_enc")
		return
	}
	aliasEnc, err := optBlob(req.AliasEnc)
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid alias_enc")
		return
	}
	now := time.Now()
	var id int64
	err = s.withWriteTx(r.Context(), "create-card", func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
insert into cards(board_id, list_id, kind, description_enc, rank, handout_meta_enc, alias_enc, created_at, updated_at) values(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			bid, listID, kind, descEnc, req.Rank, metaEnc, aliasEnc, rfc3339(now), rfc3339(now))
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	if handleErr(w, err) {
		return
	}
	writeJSON(w, map[string]any{"id": id})
}

type patchCardRequest struct {
	DescEnc        *string `json:"description_enc"`
	Rank           *string `json:"rank"`
	ListID         *int64  `json:"list_id"`
	Kind           *string `json:"kind"`             // optional kind change (feature: change card type after creation)
	DescEventEnc   *string `json:"desc_event_enc"`   // optional desc_edit timeline payload
	HandoutMetaEnc *string `json:"handout_meta_enc"` // optional handout-gen settings; "" clears (sets NULL)
	AliasEnc       *string `json:"alias_enc"`        // optional short card label; "" clears (sets NULL)
}

// optBlob maps an optional base64 field to nullable blob bytes: nil pointer or
// empty string → NULL; otherwise the decoded envelope.
func optBlob(b64v *string) ([]byte, error) {
	if b64v == nil || *b64v == "" {
		return nil, nil
	}
	return unb64(*b64v)
}

func (s *server) handlePatchCard(w http.ResponseWriter, r *http.Request) {
	uid, cardID, bid, ok := s.requireChildAccess(w, r, childCard)
	if !ok {
		return
	}
	var req patchCardRequest
	if !readJSON(w, r, &req) {
		return
	}
	err := s.withWriteTx(r.Context(), "patch-card", func(ctx context.Context, tx *sql.Tx) error {
		var p patch
		if req.DescEnc != nil {
			descEnc, err := unb64(*req.DescEnc)
			if err != nil {
				return errBadRequest("invalid description_enc")
			}
			p.set("description_enc", descEnc)
			if req.DescEventEnc != nil {
				if err := appendEvent(ctx, tx, bid, cardID, "desc_edit", uid, *req.DescEventEnc); err != nil {
					return err
				}
			}
		}
		if req.HandoutMetaEnc != nil {
			metaEnc, err := optBlob(req.HandoutMetaEnc)
			if err != nil {
				return errBadRequest("invalid handout_meta_enc")
			}
			p.set("handout_meta_enc", metaEnc)
		}
		if req.AliasEnc != nil {
			aliasEnc, err := optBlob(req.AliasEnc)
			if err != nil {
				return errBadRequest("invalid alias_enc")
			}
			p.set("alias_enc", aliasEnc)
		}
		if req.Rank != nil {
			p.set("rank", *req.Rank)
		}
		if req.Kind != nil {
			if !validCardKind(*req.Kind) {
				return errBadRequest("bad card kind")
			}
			p.set("kind", *req.Kind)
		}
		if req.ListID != nil {
			if err := onBoard(ctx, tx, childList, *req.ListID, bid); err != nil {
				return err
			}
			p.set("list_id", *req.ListID)
		}
		return p.apply(ctx, tx, "cards", cardID)
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleDeleteCard(w http.ResponseWriter, r *http.Request) {
	_, cardID, _, ok := s.requireChildAccess(w, r, childCard)
	if !ok {
		return
	}
	err := s.withWriteTx(r.Context(), "delete-card", func(ctx context.Context, tx *sql.Tx) error {
		return tombstone(ctx, tx, "cards", "id = ?", cardID)
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// labelAssignment is one (label, optional playing) pair — see cardLabelDTO.
type labelAssignment struct {
	LabelID   int64  `json:"label_id"`
	SessionID *int64 `json:"session_id"`
}

type setCardLabelsRequest struct {
	Labels []labelAssignment `json:"labels"`
	Events []eventInput      `json:"events"` // optional label_add/label_remove timeline events
}

type eventInput struct {
	Type       string `json:"type"`
	PayloadEnc string `json:"payload_enc"`
}

func (s *server) handleSetCardLabels(w http.ResponseWriter, r *http.Request) {
	uid, cardID, bid, ok := s.requireChildAccess(w, r, childCard)
	if !ok {
		return
	}
	var req setCardLabelsRequest
	if !readJSON(w, r, &req) {
		return
	}
	err := s.withWriteTx(r.Context(), "set-card-labels", func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `delete from card_labels where card_id = ?`, cardID); err != nil {
			return err
		}
		for _, a := range req.Labels {
			if err := onBoard(ctx, tx, childLabel, a.LabelID, bid); err != nil {
				return err
			}
			// A scoped assignment must name a Playing that exists: «взяли», but at
			// what, is not a state the model has.
			if a.SessionID != nil {
				var played int
				if err := tx.QueryRowContext(ctx,
					`select count(*) from card_sessions where card_id = ? and session_id = ?`, cardID, *a.SessionID).Scan(&played); err != nil {
					return err
				}
				if played == 0 {
					return errBadRequest("вопрос не отмечен этим тестом")
				}
			}
			if _, err := tx.ExecContext(ctx,
				`insert or ignore into card_labels(card_id, label_id, session_id) values(?, ?, ?)`,
				cardID, a.LabelID, a.SessionID); err != nil {
				return err
			}
		}
		for _, ev := range req.Events {
			if ev.Type != "label_add" && ev.Type != "label_remove" {
				return errBadRequest("bad label event type")
			}
			if err := appendEvent(ctx, tx, bid, cardID, ev.Type, uid, ev.PayloadEnc); err != nil {
				return err
			}
		}
		return nil
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSetCardSessions replaces a card's Playings: which tests this question was
// played at. Dropping one takes the labels scoped to it — a label scoped to a
// playing that no longer exists cannot be read (ADR-0004) — which the FK does,
// so the client confirms the count first.
func (s *server) handleSetCardSessions(w http.ResponseWriter, r *http.Request) {
	_, cardID, bid, ok := s.requireChildAccess(w, r, childCard)
	if !ok {
		return
	}
	var req struct {
		SessionIDs []int64 `json:"session_ids"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	err := s.withWriteTx(r.Context(), "set-card-sessions", func(ctx context.Context, tx *sql.Tx) error {
		keep := map[int64]bool{}
		for _, sid := range req.SessionIDs {
			if err := onBoard(ctx, tx, childSession, sid, bid); err != nil {
				return err
			}
			keep[sid] = true
		}
		rows, err := tx.QueryContext(ctx, `select session_id from card_sessions where card_id = ?`, cardID)
		if err != nil {
			return err
		}
		var have []int64
		for rows.Next() {
			var sid int64
			if err := rows.Scan(&sid); err != nil {
				rows.Close()
				return err
			}
			have = append(have, sid)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, sid := range have {
			if keep[sid] {
				continue
			}
			// Explicit, not by FK: the cascade fires when the SESSION dies, and this
			// is the card walking away from a session that still exists.
			if _, err := tx.ExecContext(ctx,
				`delete from card_labels where card_id = ? and session_id = ?`, cardID, sid); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx,
				`delete from card_sessions where card_id = ? and session_id = ?`, cardID, sid); err != nil {
				return err
			}
		}
		for sid := range keep {
			if _, err := tx.ExecContext(ctx,
				`insert or ignore into card_sessions(card_id, session_id) values(?, ?)`, cardID, sid); err != nil {
				return err
			}
		}
		return nil
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSetTourTesters replaces one tour's Declaration. The tour is a list or a
// whole group (exportScope), so exactly one of the two ids is given.
func (s *server) handleSetTourTesters(w http.ResponseWriter, r *http.Request) {
	_, bid, _, ok := s.requireBoard(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		ListID     *int64  `json:"list_id"`
		GroupID    *int64  `json:"group_id"`
		SessionIDs []int64 `json:"session_ids"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if (req.ListID == nil) == (req.GroupID == nil) {
		httpError(w, http.StatusBadRequest, "нужен ровно один из list_id / group_id")
		return
	}
	err := s.withWriteTx(r.Context(), "set-tour-testers", func(ctx context.Context, tx *sql.Tx) error {
		scope, tour, id := "list_id", childList, req.ListID
		if req.GroupID != nil {
			scope, tour, id = "group_id", childGroup, req.GroupID
		}
		if err := onBoard(ctx, tx, tour, *id, bid); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `delete from tour_testers where `+scope+` = ?`, *id); err != nil {
			return err
		}
		// A tour that names nobody still declares: one marker row, so the custom
		// does not re-tick what the editor just cleared.
		if len(req.SessionIDs) == 0 {
			_, err := tx.ExecContext(ctx,
				`insert into tour_testers(board_id, list_id, group_id, session_id) values(?, ?, ?, null)`,
				bid, req.ListID, req.GroupID)
			return err
		}
		for _, sid := range req.SessionIDs {
			if err := onBoard(ctx, tx, childSession, sid, bid); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx,
				`insert or ignore into tour_testers(board_id, list_id, group_id, session_id) values(?, ?, ?, ?)`,
				bid, req.ListID, req.GroupID, sid); err != nil {
				return err
			}
		}
		return nil
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
