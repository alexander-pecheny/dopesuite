package server

// The Board Bundle's server half (ADR-0013): two whole-board ciphertext reads
// the export decrypts in the browser, and one board-level timeline import the
// re-encrypting importer writes through. Content stays ciphertext throughout —
// the plaintext half of the feature lives entirely in the client.

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	corei18n "pecheny.me/dopecore/i18nstrings"
	xystrings "xy/i18nstrings"
)

type bundleEventDTO struct {
	ID        int64  `json:"id"`
	CardID    *int64 `json:"card_id,omitempty"`
	SessionID *int64 `json:"session_id,omitempty"`
	Type      string `json:"type"`
	// AuthorUsername is advisory: the exporting instance's login name, matched
	// by name on import (users are not shared between instances).
	AuthorUsername *string `json:"author_username,omitempty"`
	CreatedAt      string  `json:"created_at"`
	EditedAt       *string `json:"edited_at,omitempty"`
	IsExcerpt      bool    `json:"is_excerpt"`
	ReplyToID      *int64  `json:"reply_to_id,omitempty"`
	PayloadEnc     string  `json:"payload_enc"`
}

// handleGetBoardTimeline returns every live timeline event on a board — all
// kinds, cards and sessions alike — for the Bundle export. Tombstoned events
// (and events under tombstoned cards/sessions) stay behind: a Bundle is the
// board's current content, not its trash can.
func (s *server) handleGetBoardTimeline(w http.ResponseWriter, r *http.Request) {
	_, bid, _, ok := s.requireBoard(w, r, "id")
	if !ok {
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `
select e.id, e.card_id, e.session_id, e.type,
       coalesce(nullif(u.username, ''), u.telegram_username),
       e.created_at, e.edited_at, e.is_excerpt, e.reply_to_id, e.payload_enc
from timeline_events e
left join users u on u.id = e.author_user_id
left join cards c on c.id = e.card_id
left join test_sessions ts on ts.id = e.session_id
where e.board_id = ? and e.deleted_at is null
  and (e.card_id is null or c.deleted_at is null)
  and (e.session_id is null or ts.deleted_at is null)
order by e.id`, bid)
	if handleErr(w, err) {
		return
	}
	defer rows.Close()
	out := []bundleEventDTO{}
	for rows.Next() {
		var e bundleEventDTO
		var cardID, sessionID, replyTo sql.NullInt64
		var author, edited sql.NullString
		var excerpt int
		var payload []byte
		if err := rows.Scan(&e.ID, &cardID, &sessionID, &e.Type, &author,
			&e.CreatedAt, &edited, &excerpt, &replyTo, &payload); handleErr(w, err) {
			return
		}
		if cardID.Valid {
			e.CardID = &cardID.Int64
		}
		if sessionID.Valid {
			e.SessionID = &sessionID.Int64
		}
		if author.Valid {
			e.AuthorUsername = &author.String
		}
		if edited.Valid {
			e.EditedAt = &edited.String
		}
		if replyTo.Valid {
			e.ReplyToID = &replyTo.Int64
		}
		e.IsExcerpt = excerpt != 0
		e.PayloadEnc = b64(payload)
		out = append(out, e)
	}
	if err := rows.Err(); handleErr(w, err) {
		return
	}
	writeJSON(w, out)
}

type boardAttachmentDTO struct {
	attachmentDTO
	CardID int64 `json:"card_id"`
}

// handleGetBoardAttachments lists every live attachment on a board in one
// response — the export's manifest, saving a request per card.
func (s *server) handleGetBoardAttachments(w http.ResponseWriter, r *http.Request) {
	_, bid, _, ok := s.requireBoard(w, r, "id")
	if !ok {
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `
select a.id, a.card_id, a.filename_enc, a.mime, a.size, a.lossless, a.is_excerpt, a.rev, a.created_at
from attachments a join cards c on c.id = a.card_id
where a.board_id = ? and a.deleted_at is null and c.deleted_at is null
order by a.id`, bid)
	if handleErr(w, err) {
		return
	}
	defer rows.Close()
	out := []boardAttachmentDTO{}
	for rows.Next() {
		var a boardAttachmentDTO
		var fn []byte
		var lossless, excerpt int
		if err := rows.Scan(&a.ID, &a.CardID, &fn, &a.Mime, &a.Size, &lossless, &excerpt, &a.Rev, &a.CreatedAt); handleErr(w, err) {
			return
		}
		a.FilenameEnc = b64(fn)
		a.Lossless = lossless != 0
		a.IsExcerpt = excerpt != 0
		out = append(out, a)
	}
	if err := rows.Err(); handleErr(w, err) {
		return
	}
	writeJSON(w, out)
}

// maxBundleEventsPerRequest keeps one import batch inside the 5s write-tx
// budget; the client chunks and chains batches through the returned id map.
const maxBundleEventsPerRequest = 500

var bundleEventTypes = map[string]bool{
	"comment": true, "desc_edit": true, "label_add": true, "label_remove": true,
	"attach_add": true, "attach_remove": true, "attach_replace": true, "reaction": true,
}

type bundleImportedEvent struct {
	// SrcID / ReplyToSrcID rebuild threading exactly like the per-card import
	// (comments.go): fresh ids here, a src→new map resolves parents within the
	// batch. ReplyToID carries a parent already created by an EARLIER batch —
	// the new id the previous response's map handed back.
	SrcID        int64  `json:"src_id"`
	ReplyToSrcID *int64 `json:"reply_to_src_id"`
	ReplyToID    *int64 `json:"reply_to_id"`
	CardID       *int64 `json:"card_id"`
	SessionID    *int64 `json:"session_id"`
	Type         string `json:"type"`
	// AuthorUsername is matched against local logins; no match means no author
	// (the same advisory-authorship model as the per-card import).
	AuthorUsername string `json:"author_username"`
	CreatedAt      string `json:"created_at"`
	EditedAt       string `json:"edited_at"`
	IsExcerpt      bool   `json:"is_excerpt"`
	PayloadEnc     string `json:"payload_enc"`
}

type bundleImportRequest struct {
	Events []bundleImportedEvent `json:"events"`
}

// handleBundleImportEvents bulk-inserts timeline events of every kind onto a
// board's cards and sessions, preserving original timestamps, excerpt flags and
// (by username) authors. Returns the src→new id map so the next batch can
// thread replies across the chunk boundary.
func (s *server) handleBundleImportEvents(w http.ResponseWriter, r *http.Request) {
	_, bid, _, ok := s.requireBoard(w, r, "id")
	if !ok {
		return
	}
	var req bundleImportRequest
	if !readJSON(w, r, &req) {
		return
	}
	if len(req.Events) > maxBundleEventsPerRequest {
		httpError(w, http.StatusBadRequest, xystrings.Default.Server.Bundle.TooManyEvents())
		return
	}
	newID := make(map[int64]int64, len(req.Events))
	err := s.withWriteTx(r.Context(), "bundle-import-events", func(ctx context.Context, tx *sql.Tx) error {
		okCard := map[int64]bool{}
		okSession := map[int64]bool{}
		authorID := map[string]*int64{}
		checkRef := func(cache map[int64]bool, c child, id int64) error {
			if cache[id] {
				return nil
			}
			if err := onBoard(ctx, tx, c, id, bid); err != nil {
				return err
			}
			cache[id] = true
			return nil
		}
		for _, e := range req.Events {
			if !bundleEventTypes[e.Type] {
				return corei18n.User("bad event type")
			}
			if e.CardID == nil && e.SessionID == nil {
				return corei18n.User(xystrings.Default.Server.Bundle.EventWithoutTarget())
			}
			if e.CardID != nil {
				if err := checkRef(okCard, childCard, *e.CardID); err != nil {
					return err
				}
			}
			if e.SessionID != nil {
				if err := checkRef(okSession, childSession, *e.SessionID); err != nil {
					return err
				}
			}
			payload, err := unb64(e.PayloadEnc)
			if err != nil {
				return corei18n.User("invalid payload_enc")
			}
			created := e.CreatedAt
			if _, perr := time.Parse(time.RFC3339, created); perr != nil {
				created = rfc3339(time.Now())
			}
			author, cached := authorID[e.AuthorUsername]
			if !cached {
				author = nil
				if e.AuthorUsername != "" {
					var id int64
					err := tx.QueryRowContext(ctx, `
select id from users where coalesce(nullif(username, ''), telegram_username) = ?`, e.AuthorUsername).Scan(&id)
					if err == nil {
						author = &id
					} else if err != sql.ErrNoRows {
						return err
					}
				}
				authorID[e.AuthorUsername] = author
			}
			replyTo := e.ReplyToID
			if replyTo == nil && e.ReplyToSrcID != nil {
				if mapped, ok := newID[*e.ReplyToSrcID]; ok {
					replyTo = &mapped
				}
			}
			if replyTo != nil {
				var n int
				if err := tx.QueryRowContext(ctx,
					`select count(*) from timeline_events where id = ? and board_id = ?`, *replyTo, bid).Scan(&n); err != nil {
					return err
				}
				if n == 0 {
					replyTo = nil // an unresolvable parent drops the reply to top level
				}
			}
			// A reaction that lost its comment (tombstoned on the source) must not
			// be reparented: at top level it would read as a reaction on the card.
			if e.Type == "reaction" && replyTo == nil && (e.ReplyToSrcID != nil || e.ReplyToID != nil) {
				continue
			}
			cardID := int64(0)
			if e.CardID != nil {
				cardID = *e.CardID
			}
			id, err := insertEvent(ctx, tx, timelineEvent{
				BoardID: bid, CardID: cardID, SessionID: e.SessionID, Type: e.Type,
				AuthorID: author, CreatedAt: created, IsExcerpt: e.IsExcerpt,
				Payload: payload, ReplyToID: replyTo,
			})
			if err != nil {
				return err
			}
			if e.EditedAt != "" {
				if _, perr := time.Parse(time.RFC3339, e.EditedAt); perr == nil {
					if _, err := tx.ExecContext(ctx, `update timeline_events set edited_at = ? where id = ?`, e.EditedAt, id); err != nil {
						return err
					}
				}
			}
			if e.SrcID != 0 {
				newID[e.SrcID] = id
			}
		}
		return nil
	})
	if handleErr(w, err) {
		return
	}
	writeJSON(w, map[string]any{"ids": newID})
}
