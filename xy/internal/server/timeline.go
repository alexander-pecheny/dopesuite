package server

import (
	"context"
	"database/sql"
	"time"
)

// The Timeline (CONTEXT.md): comments, description edits and the metadata
// trail, on a Card or on a Test Session, with Reactions riding along. One
// writer knows every kind's columns; one scanner reads a row into the DTO the
// лента renders. The unread rule over the same rows is unread.go's.

// timelineEvent is one entry as written. CardID 0 is a note on the Session
// itself; a nil AuthorID is an imported entry with no author on record; an
// empty CreatedAt means now.
type timelineEvent struct {
	BoardID   int64
	CardID    int64
	SessionID *int64
	Type      string
	AuthorID  *int64
	CreatedAt string
	IsExcerpt bool
	Payload   []byte // the ciphertext envelope
	ReplyToID *int64
}

func insertEvent(ctx context.Context, tx *sql.Tx, ev timelineEvent) (int64, error) {
	var cardID, author, replyTo, sessionID any
	if ev.CardID != 0 {
		cardID = ev.CardID
	}
	if ev.AuthorID != nil {
		author = *ev.AuthorID
	}
	if ev.ReplyToID != nil {
		replyTo = *ev.ReplyToID
	}
	if ev.SessionID != nil {
		sessionID = *ev.SessionID
	}
	if ev.CreatedAt == "" {
		ev.CreatedAt = rfc3339(time.Now())
	}
	res, err := tx.ExecContext(ctx, `
insert into timeline_events(board_id, card_id, session_id, type, author_user_id, created_at, is_excerpt, payload_enc, reply_to_id)
values(?, ?, ?, ?, ?, ?, ?, ?, ?)`, ev.BoardID, cardID, sessionID, ev.Type, author, ev.CreatedAt, boolInt(ev.IsExcerpt), ev.Payload, replyTo)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// appendEvent is insertEvent for the metadata trail: a base64 payload envelope,
// an author, now.
func appendEvent(ctx context.Context, tx *sql.Tx, boardID, cardID int64, typ string, authorID int64, payloadB64 string) error {
	payload, err := unb64(payloadB64)
	if err != nil {
		return errBadRequest("invalid payload_enc")
	}
	_, err = insertEvent(ctx, tx, timelineEvent{BoardID: boardID, CardID: cardID, Type: typ, AuthorID: &authorID, Payload: payload})
	return err
}

// timelineColumns is what scanTimelineEvent reads, from `timeline_events e`.
const timelineColumns = `
select e.id, e.type, e.author_user_id, e.created_at, e.edited_at, e.is_excerpt,
       e.reply_to_id, e.deleted_at is not null,
       (select count(*) from timeline_events r
          where r.reply_to_id = e.id and r.deleted_at is null),
       e.payload_enc, e.session_id, e.card_id
from timeline_events e`

func scanTimelineEvent(rows *sql.Rows) (timelineEventDTO, error) {
	var e timelineEventDTO
	var author, replyTo, sessionID, cardRef sql.NullInt64
	var edited sql.NullString
	var excerpt, deleted int
	var payload []byte
	if err := rows.Scan(&e.ID, &e.Type, &author, &e.CreatedAt, &edited, &excerpt,
		&replyTo, &deleted, &e.ReplyCount, &payload, &sessionID, &cardRef); err != nil {
		return e, err
	}
	if sessionID.Valid {
		e.SessionID = &sessionID.Int64
	}
	if cardRef.Valid {
		e.CardID = &cardRef.Int64
	}
	if author.Valid {
		e.AuthorID = &author.Int64
	}
	if edited.Valid {
		e.EditedAt = &edited.String
	}
	if replyTo.Valid {
		e.ReplyToID = &replyTo.Int64
	}
	e.IsExcerpt = excerpt != 0
	e.Deleted = deleted != 0
	e.PayloadEnc = b64(payload)
	return e, nil
}

// readTimeline runs a query over timelineColumns into the лента's rows.
func (s *server) readTimeline(ctx context.Context, where string, args ...any) ([]timelineEventDTO, error) {
	rows, err := s.db.QueryContext(ctx, timelineColumns+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []timelineEventDTO{}
	for rows.Next() {
		e, err := scanTimelineEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
