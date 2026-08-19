package server

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"time"
)

// ---- read markers (card_reads) + activity feed ----
//
// Read-tracking is online-only best-effort (like loadMembers on the frontend):
// it never goes through the sync outbox. See migrateV7 for the schema and the
// unread computation shared with the board snapshot (boards.go).

type markReadRequest struct {
	ContentReadID *int64 `json:"content_read_id"`
	CommentReadID *int64 `json:"comment_read_id"`
}

// handleMarkRead advances (never rewinds — see the max() upsert) the caller's
// read watermarks for a card. Sending 0 for a bucket leaves it untouched.
func (s *server) handleMarkRead(w http.ResponseWriter, r *http.Request) {
	uid, cardID, _, ok := s.requireChildAccess(w, r, childCard)
	if !ok {
		return
	}
	var req markReadRequest
	if !readJSON(w, r, &req) {
		return
	}
	var contentID, commentID int64
	if req.ContentReadID != nil {
		contentID = *req.ContentReadID
	}
	if req.CommentReadID != nil {
		commentID = *req.CommentReadID
	}
	err := s.withWriteTx(r.Context(), "mark-read", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
insert into card_reads(user_id, card_id, content_read_id, comment_read_id, updated_at)
values(?, ?, ?, ?, ?)
on conflict(user_id, card_id) do update set
  content_read_id = max(content_read_id, excluded.content_read_id),
  comment_read_id = max(comment_read_id, excluded.comment_read_id),
  updated_at = excluded.updated_at`, uid, cardID, contentID, commentID, rfc3339(time.Now()))
		return err
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// activityEventDTO is one row of the board activity feed: another member's
// timeline event, flagged unread/read against the caller's watermarks.
type activityEventDTO struct {
	ID         int64  `json:"id"`
	CardID     int64  `json:"card_id"`
	Type       string `json:"type"`
	AuthorID   int64  `json:"author_user_id"`
	CreatedAt  string `json:"created_at"`
	PayloadEnc string `json:"payload_enc"`
	Unread     bool   `json:"unread"`
	// Mention: this row names the caller — the 🔔 panel paints it red while
	// Unread holds. MentionReply: it does so by replying to their comment
	// (the two are told apart so the row's wording can be honest: a reply to
	// a THIRD person that @-mentions you is not «ответ вам»).
	Mention      bool   `json:"mention"`
	MentionReply bool   `json:"mention_reply"`
	ReplyToID    *int64 `json:"reply_to_id"`
}

// handleBoardActivity returns the board's other-authored events, newest first,
// for the 🔔 bell panel. `?limit=` defaults to 50, capped at 200.
func (s *server) handleBoardActivity(w http.ResponseWriter, r *http.Request) {
	uid, bid, _, ok := s.requireBoard(w, r, "id")
	if !ok {
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := s.db.QueryContext(r.Context(), `
select e.id, e.card_id, e.type, e.author_user_id, e.created_at, e.payload_enc, e.reply_to_id,
  case when `+sqlUnread+` then 1 else 0 end as unread,
  case when `+sqlMentionExplicit("?")+` then 1 else 0 end as mention_explicit,
  case when `+sqlMentionReply("?")+` then 1 else 0 end as mention_reply
`+sqlEventsOfOthers+` and e.board_id = ?
order by e.id desc
limit ?`, uid, uid, uid, uid, bid, limit)
	if handleErr(w, err) {
		return
	}
	defer rows.Close()
	out := []activityEventDTO{}
	for rows.Next() {
		var e activityEventDTO
		var payload []byte
		var replyTo sql.NullInt64
		var unread, explicit, reply int
		if err := rows.Scan(&e.ID, &e.CardID, &e.Type, &e.AuthorID, &e.CreatedAt, &payload, &replyTo, &unread, &explicit, &reply); handleErr(w, err) {
			return
		}
		e.PayloadEnc = b64(payload)
		e.Unread = unread == 1
		e.Mention = explicit == 1 || reply == 1
		e.MentionReply = reply == 1 && explicit == 0
		if replyTo.Valid {
			e.ReplyToID = &replyTo.Int64
		}
		out = append(out, e)
	}
	if err := rows.Err(); handleErr(w, err) {
		return
	}
	writeJSON(w, out)
}

// handleBoardReadAll upserts every board card's watermarks to their current
// per-bucket max (i.e. marks the whole board read), for the panel's "Прочитать
// всё" button.
func (s *server) handleBoardReadAll(w http.ResponseWriter, r *http.Request) {
	uid, bid, _, ok := s.requireBoard(w, r, "id")
	if !ok {
		return
	}
	err := s.withWriteTx(r.Context(), "read-all", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
insert into card_reads(user_id, card_id, content_read_id, comment_read_id, updated_at)
select ?, c.id,
  coalesce(max(case when not `+sqlCommentBucket+` then e.id end), 0),
  coalesce(max(case when `+sqlCommentBucket+` then e.id end), 0),
  ?
from cards c left join timeline_events e on e.card_id = c.id
where c.board_id = ? and c.deleted_at is null
group by c.id
on conflict(user_id, card_id) do update set
  content_read_id = max(content_read_id, excluded.content_read_id),
  comment_read_id = max(comment_read_id, excluded.comment_read_id),
  updated_at = excluded.updated_at`, uid, rfc3339(time.Now()), bid)
		return err
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
