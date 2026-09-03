package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	xystrings "xy/i18nstrings"
)

type timelineEventDTO struct {
	ID         int64   `json:"id"`
	Type       string  `json:"type"`
	AuthorID   *int64  `json:"author_user_id"`
	CreatedAt  string  `json:"created_at"`
	EditedAt   *string `json:"edited_at,omitempty"`
	IsExcerpt  bool    `json:"is_excerpt"`
	ReplyToID  *int64  `json:"reply_to_id,omitempty"`
	ReplyCount int     `json:"reply_count"`
	// Deleted marks a tombstone: a comment whose text is gone but which is still
	// rendered because live replies hang off it. PayloadEnc is empty for these.
	Deleted    bool   `json:"deleted,omitempty"`
	PayloadEnc string `json:"payload_enc"`
	// SessionID tags a comment with the Test Session it came out of ("on this
	// test the team stumbled over the wording"). CardID is null on a note about
	// the session itself, which is what a comment on the old test card was.
	SessionID *int64 `json:"session_id,omitempty"`
	CardID    *int64 `json:"card_id,omitempty"`
}

// boardCommentDTO is one comment as the prewarm indexes it: which card it hangs off,
// its ciphertext, and the id a search hit deep-links to. Nothing else — an
// author or a date would only be shown by a timeline, and the timeline asks per card.
type boardCommentDTO struct {
	ID         int64  `json:"id"`
	CardID     int64  `json:"card_id"`
	PayloadEnc string `json:"payload_enc"`
}

// handleGetBoardComments returns every live comment on a board's cards in one
// response, so the client's Search Index can cover comments without a request
// per card. Comments only: a desc_edit payload carries the whole before/after
// text of a question, which would make an index of every old wording.
func (s *server) handleGetBoardComments(w http.ResponseWriter, r *http.Request) {
	_, bid, _, ok := s.requireBoard(w, r, "id")
	if !ok {
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `
select e.id, e.card_id, e.payload_enc
from timeline_events e
join cards c on c.id = e.card_id
where c.board_id = ? and e.type = 'comment'
  and e.deleted_at is null and c.deleted_at is null
order by e.id`, bid)
	if handleErr(w, err) {
		return
	}
	defer rows.Close()
	out := []boardCommentDTO{}
	for rows.Next() {
		var e boardCommentDTO
		var payload []byte
		if err := rows.Scan(&e.ID, &e.CardID, &payload); handleErr(w, err) {
			return
		}
		e.PayloadEnc = b64(payload)
		out = append(out, e)
	}
	if err := rows.Err(); handleErr(w, err) {
		return
	}
	writeJSON(w, out)
}

func (s *server) handleGetTimeline(w http.ResponseWriter, r *http.Request) {
	_, cardID, _, ok := s.requireChildAccess(w, r, childCard)
	if !ok {
		return
	}
	// A deleted comment is normally gone from the timeline, but one that still
	// anchors live replies is returned as a tombstone (deleted = 1, empty
	// payload) so the thread beneath it stays reachable instead of orphaned.
	out, err := s.readTimeline(r.Context(), `
where e.card_id = ?
  and (e.deleted_at is null
       or exists (select 1 from timeline_events r
                    where r.reply_to_id = e.id and r.deleted_at is null))
order by e.id`, cardID)
	if handleErr(w, err) {
		return
	}
	writeJSON(w, out)
}

type addCommentRequest struct {
	PayloadEnc string `json:"payload_enc"`
	ReplyToID  *int64 `json:"reply_to_id"`
	SessionID  *int64 `json:"session_id"` // optional: the test this came out of
	// Mentions: board-member ids the comment's text names, resolved by the
	// client at compose time. Plaintext routing metadata (ADR-0009).
	Mentions []int64 `json:"mentions"`
}

// memberMentions keeps only ids that are actually on the board — a mention
// routes a notification, so it must not address strangers. Dropped rather than
// rejected: an offline-queued comment may arrive after the named member left,
// and a 400 would throw the comment itself away with the outbox op.
func memberMentions(ctx context.Context, q querier, bid int64, ids []int64) ([]int64, error) {
	var out []int64
	for _, id := range ids {
		var n int
		if err := q.QueryRowContext(ctx,
			`select count(*) from board_members where board_id = ? and user_id = ?`, bid, id).Scan(&n); err != nil {
			return nil, err
		}
		if n > 0 {
			out = append(out, id)
		}
	}
	return out, nil
}

func insertMentions(ctx context.Context, tx *sql.Tx, eventID int64, ids []int64) error {
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx,
			`insert or ignore into event_mentions(event_id, user_id) values(?, ?)`, eventID, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *server) handleAddComment(w http.ResponseWriter, r *http.Request) {
	uid, cardID, bid, ok := s.requireChildAccess(w, r, childCard)
	if !ok {
		return
	}
	var req addCommentRequest
	if !readJSON(w, r, &req) {
		return
	}
	if req.PayloadEnc == "" {
		httpError(w, http.StatusBadRequest, "payload_enc required")
		return
	}
	var replyTo *int64
	if req.ReplyToID != nil {
		root, err := threadRoot(r.Context(), s.db, *req.ReplyToID, cardID)
		if handleErr(w, err) {
			return
		}
		replyTo = &root
	}
	if req.SessionID != nil {
		if err := onBoard(r.Context(), s.db, childSession, *req.SessionID, bid); handleErr(w, err) {
			return
		}
	}
	mentions, err := memberMentions(r.Context(), s.db, bid, req.Mentions)
	if handleErr(w, err) {
		return
	}
	err = s.withWriteTx(r.Context(), "add-comment", func(ctx context.Context, tx *sql.Tx) error {
		payload, err := unb64(req.PayloadEnc)
		if err != nil {
			return errBadRequest("invalid payload_enc")
		}
		evID, err := insertEvent(ctx, tx, timelineEvent{BoardID: bid, CardID: cardID, SessionID: req.SessionID, Type: "comment", AuthorID: &uid, Payload: payload, ReplyToID: replyTo})
		if err != nil {
			return err
		}
		return insertMentions(ctx, tx, evID, mentions)
	})
	if handleErr(w, err) {
		return
	}
	s.notifyComment(bid, cardID, uid, mentions, replyTo)
	w.WriteHeader(http.StatusNoContent)
}

// threadRoot resolves the comment a reply should hang off. Threads are one level
// deep: replying to a reply attaches to that reply's root, so a thread is always
// a flat run under a single parent. The target must be a comment on the SAME
// card — otherwise a reply could be smuggled onto another board's discussion.
// A tombstoned parent is still a valid target; its thread outlives its text.
func threadRoot(ctx context.Context, q querier, id, cardID int64) (int64, error) {
	str := xystrings.Default
	var root sql.NullInt64
	var owner int64
	err := q.QueryRowContext(ctx, `
select card_id, coalesce(reply_to_id, id) from timeline_events where id = ? and type = 'comment'`, id).
		Scan(&owner, &root)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, errNotFound(str.Server.Comment.NotFound())
	}
	if err != nil {
		return 0, err
	}
	if owner != cardID {
		return 0, errBadRequest(str.Server.Comment.Foreign())
	}
	return root.Int64, nil
}

type patchCommentRequest struct {
	PayloadEnc *string `json:"payload_enc"`
	IsExcerpt  *bool   `json:"is_excerpt"`
	// The test this comment came out of; 0 clears it (the optBlob convention).
	SessionID *int64 `json:"session_id"`
	// Mentions re-resolved from the edited text; nil = leave as they were.
	// Only meaningful alongside PayloadEnc — mentions are the text's shadow.
	Mentions *[]int64 `json:"mentions"`
}

// handlePatchComment edits a comment's text, flips its excerpt flag and/or
// retags the test it came out of. The fields carry different permissions:
// rewriting what someone said is the author's business alone, while marking a
// comment as an excerpt or naming the test behind it is curation any board
// member may do (the same trust level as adding one).
func (s *server) handlePatchComment(w http.ResponseWriter, r *http.Request) {
	uid, evID, bid, cardID, author, ok := s.requireComment(w, r)
	if !ok {
		return
	}
	var req patchCommentRequest
	if !readJSON(w, r, &req) {
		return
	}
	if req.PayloadEnc != nil && (author == nil || *author != uid) {
		httpError(w, http.StatusForbidden, xystrings.Default.Server.Comment.EditOwnerOnly())
		return
	}
	if req.Mentions != nil {
		filtered, err := memberMentions(r.Context(), s.db, bid, *req.Mentions)
		if handleErr(w, err) {
			return
		}
		req.Mentions = &filtered
	}
	if req.SessionID != nil && *req.SessionID != 0 {
		if err := onBoard(r.Context(), s.db, childSession, *req.SessionID, bid); handleErr(w, err) {
			return
		}
	}
	var addedMentions []int64
	err := s.withWriteTx(r.Context(), "patch-comment", func(ctx context.Context, tx *sql.Tx) error {
		if req.SessionID != nil {
			var sid *int64
			if *req.SessionID != 0 {
				sid = req.SessionID
			}
			if _, err := tx.ExecContext(ctx, `update timeline_events set session_id = ? where id = ?`, sid, evID); err != nil {
				return err
			}
		}
		if req.IsExcerpt != nil {
			flag := 0
			if *req.IsExcerpt {
				flag = 1
			}
			if _, err := tx.ExecContext(ctx, `update timeline_events set is_excerpt = ? where id = ?`, flag, evID); err != nil {
				return err
			}
		}
		if req.PayloadEnc != nil {
			payload, err := unb64(*req.PayloadEnc)
			if err != nil || len(payload) == 0 {
				return errBadRequest("invalid payload_enc")
			}
			if _, err := tx.ExecContext(ctx, `
update timeline_events set payload_enc = ?, edited_at = ? where id = ?`, payload, rfc3339(time.Now()), evID); err != nil {
				return err
			}
			if req.Mentions != nil {
				old := map[int64]bool{}
				mrows, err := tx.QueryContext(ctx, `select user_id from event_mentions where event_id = ?`, evID)
				if err != nil {
					return err
				}
				for mrows.Next() {
					var id int64
					if err := mrows.Scan(&id); err != nil {
						mrows.Close()
						return err
					}
					old[id] = true
				}
				if err := mrows.Err(); err != nil {
					mrows.Close()
					return err
				}
				mrows.Close()
				if _, err := tx.ExecContext(ctx, `delete from event_mentions where event_id = ?`, evID); err != nil {
					return err
				}
				if err := insertMentions(ctx, tx, evID, *req.Mentions); err != nil {
					return err
				}
				for _, id := range *req.Mentions {
					if !old[id] {
						addedMentions = append(addedMentions, id)
					}
				}
			}
		}
		return nil
	})
	if handleErr(w, err) {
		return
	}
	if len(addedMentions) > 0 {
		s.notifyComment(bid, cardID, uid, addedMentions, nil)
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteComment tombstones a comment (author only). The row survives so
// the id stays taken — read watermarks are ids, and reusing one would silently
// mark later comments read — and so a thread hanging off it stays anchored. The
// TEXT is scrubbed, not merely hidden: a tombstone with replies is still sent to
// clients, and delete has to mean the words are gone.
func (s *server) handleDeleteComment(w http.ResponseWriter, r *http.Request) {
	uid, evID, _, _, author, ok := s.requireComment(w, r)
	if !ok {
		return
	}
	if author == nil || *author != uid {
		httpError(w, http.StatusForbidden, xystrings.Default.Server.Comment.DeleteOwnerOnly())
		return
	}
	err := s.withWriteTx(r.Context(), "delete-comment", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
update timeline_events set deleted_at = ?, payload_enc = x'', is_excerpt = 0
where id = ? and deleted_at is null`, rfc3339(time.Now()), evID)
		return err
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// requireComment resolves {id} to a live comment event the caller may see,
// returning the caller's uid plus the event's board, card and author.
func (s *server) requireComment(w http.ResponseWriter, r *http.Request) (uid, evID, bid, cardID int64, author *int64, ok bool) {
	u, okU := s.requireUser(w, r)
	if !okU {
		return
	}
	id, okP := pathInt(w, r, "id")
	if !okP {
		return
	}
	var a sql.NullInt64
	err := s.db.QueryRowContext(r.Context(), `
select board_id, card_id, author_user_id from timeline_events
where id = ? and type = 'comment' and deleted_at is null`, id).Scan(&bid, &cardID, &a)
	if errors.Is(err, sql.ErrNoRows) {
		httpError(w, http.StatusNotFound, xystrings.Default.Server.Comment.NotFound())
		return
	}
	if handleErr(w, err) {
		return
	}
	if _, err := boardRole(r.Context(), s.db, bid, u.UserID); handleErr(w, err) {
		return
	}
	if a.Valid {
		author = &a.Int64
	}
	return u.UserID, id, bid, cardID, author, true
}

type importEventsRequest struct {
	Events []importedEvent `json:"events"`
}

type importedEvent struct {
	// SrcID / ReplyToSrcID are the SOURCE card's event ids, used only to rebuild
	// threading: the copy gets fresh ids, so a reply's parent is resolved through
	// a src→new map as the batch is inserted (oldest first, so a parent is always
	// already mapped by the time its reply arrives).
	SrcID        int64  `json:"src_id"`
	ReplyToSrcID *int64 `json:"reply_to_src_id"`
	Type         string `json:"type"` // "comment" (default) or "desc_edit"
	AuthorUserID *int64 `json:"author_user_id"`
	CreatedAt    string `json:"created_at"`
	IsExcerpt    bool   `json:"is_excerpt"`
	PayloadEnc   string `json:"payload_enc"`
}

// handleImportEvents bulk-inserts timeline events while preserving their
// original author + timestamp — used by the copy/move path so a duplicated card
// keeps its discussion intact instead of re-stamping every comment to the copier
// and "now", and by the Trello import for a card's comments and its description
// history. Authorship here is advisory display metadata (the same trust model
// as the rest of the board: any editor can already write arbitrary encrypted
// content); author_user_id, when present, must reference a real user (FK).
func (s *server) handleImportEvents(w http.ResponseWriter, r *http.Request) {
	_, cardID, bid, ok := s.requireChildAccess(w, r, childCard)
	if !ok {
		return
	}
	var req importEventsRequest
	if !readJSON(w, r, &req) {
		return
	}
	err := s.withWriteTx(r.Context(), "import-events", func(ctx context.Context, tx *sql.Tx) error {
		newID := make(map[int64]int64, len(req.Events))
		for _, c := range req.Events {
			typ := c.Type
			if typ == "" {
				typ = "comment"
			}
			if typ != "comment" && typ != "desc_edit" {
				return errBadRequest("bad event type")
			}
			payload, err := unb64(c.PayloadEnc)
			if err != nil {
				return errBadRequest("invalid payload_enc")
			}
			created := c.CreatedAt
			if _, perr := time.Parse(time.RFC3339, created); perr != nil {
				created = rfc3339(time.Now()) // fall back on a missing/garbled timestamp
			}
			author := c.AuthorUserID
			// An unresolvable parent (out-of-order or absent from the batch) drops
			// the reply to top level rather than failing the whole copy.
			var replyTo *int64
			if c.ReplyToSrcID != nil {
				if mapped, ok := newID[*c.ReplyToSrcID]; ok {
					replyTo = &mapped
				}
			}
			id, err := insertEvent(ctx, tx, timelineEvent{BoardID: bid, CardID: cardID, Type: typ, AuthorID: author, CreatedAt: created, IsExcerpt: c.IsExcerpt, Payload: payload, ReplyToID: replyTo})
			if err != nil {
				return err
			}
			if c.SrcID != 0 {
				newID[c.SrcID] = id
			}
		}
		return nil
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
