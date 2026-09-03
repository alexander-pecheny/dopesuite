package server

import (
	"context"
	"database/sql"
	"log"
	"strconv"
	"time"

	xystrings "xy/i18nstrings"
)

// The telegram nudge for a Mention: who + board + card link, nothing from
// inside the crypto boundary (the board name is the one deliberate plaintext).
// Best-effort by design — the red dot is the durable signal, this is a knock on
// the door. No queue, no retry: a failure is logged and dropped.

// notifyComment fans out the nudge for a freshly landed comment: the mentioned
// members, plus the parent's author when it is a reply (an implicit Mention).
// replyTo is the root comment id (int64) or nil, exactly as handleAddComment
// resolved it. Runs in a goroutine; does nothing when no bot is configured.
func (s *server) notifyComment(bid, cardID, authorID int64, mentions []int64, replyTo any) {
	if s.bot == nil {
		return
	}
	// All the lookups run inline (they are point reads on the same SQLite);
	// only the network call is fire-and-forget, so nothing here outlives the
	// request's owner of s.db.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	targets := map[int64]bool{}
	mentioned := map[int64]bool{}
	for _, id := range mentions {
		targets[id] = true
		mentioned[id] = true
	}
	if root, isReply := replyTo.(int64); isReply {
		var parent sql.NullInt64
		err := s.db.QueryRowContext(ctx,
			`select author_user_id from timeline_events where id = ?`, root).Scan(&parent)
		if err == nil && parent.Valid {
			targets[parent.Int64] = true
		}
	}
	delete(targets, authorID)
	if len(targets) == 0 {
		return
	}
	var author sql.NullString
	if err := s.db.QueryRowContext(ctx,
		`select coalesce(nullif(username,''), telegram_username) from users where id = ?`, authorID).Scan(&author); err != nil {
		log.Printf("notify: author lookup: %v", err)
		return
	}
	var boardName sql.NullString
	if err := s.db.QueryRowContext(ctx,
		`select name from boards where id = ?`, bid).Scan(&boardName); err != nil {
		log.Printf("notify: board lookup: %v", err)
		return
	}
	str := xystrings.Default
	where := str.Notify.Mention.BoardUnnamed()
	// A legacy board's name is still ciphertext; the nudge just points.
	if boardName.String != "" {
		where = str.Notify.Mention.BoardNamed(boardName.String)
	}
	link := publicURL() + "/board/" + strconv.FormatInt(bid, 10)
	if cardID != 0 {
		link += "?card=" + strconv.FormatInt(cardID, 10)
	}
	for id := range targets {
		var tgID sql.NullInt64
		if err := s.db.QueryRowContext(ctx,
			`select telegram_user_id from users where id = ?`, id).Scan(&tgID); err != nil || !tgID.Valid {
			continue // no telegram — the red dot alone will have to do
		}
		verb := str.Notify.Mention.VerbMentioned()
		if !mentioned[id] {
			verb = str.Notify.Mention.VerbReplied()
		}
		text := author.String + " " + verb + " " + where + ": " + link
		go func(tg int64) {
			sctx, scancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer scancel()
			s.notifyDM(sctx, tg, text)
		}(tgID.Int64)
	}
}

// notifyJoinRequest knocks on the owner's door when someone asks to join
// through a link that requires approval (ADR-0017). Nothing waits for it: the
// Members panel is the durable signal, this only saves the
// requester from waiting on an owner who has no reason to look.
func (s *server) notifyJoinRequest(bid, requesterID int64) {
	if s.bot == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var requester sql.NullString
	var ownerTg sql.NullInt64
	if err := s.db.QueryRowContext(ctx,
		`select coalesce(nullif(username,''), telegram_username) from users where id = ?`, requesterID).Scan(&requester); err != nil {
		log.Printf("notify: requester lookup: %v", err)
		return
	}
	if err := s.db.QueryRowContext(ctx, `
select u.telegram_user_id from boards b join users u on u.id = b.owner_user_id where b.id = ?`, bid).
		Scan(&ownerTg); err != nil {
		log.Printf("notify: board owner lookup: %v", err)
		return
	}
	if !ownerTg.Valid {
		return // no telegram — the pending count alone will have to do
	}
	// A legacy board's name is still ciphertext, and boardDisplayName is the one
	// place that knows it; the nudge then just points.
	boardName, err := boardDisplayName(ctx, s.db, bid)
	if err != nil {
		log.Printf("notify: board name: %v", err)
		return
	}
	str := xystrings.Default
	where := str.Notify.Join.BoardUnnamed()
	if boardName != "" {
		where = str.Notify.Join.BoardNamed(boardName)
	}
	text := str.Notify.Join.Text(requester.String, where,
		publicURL()+"/board/"+strconv.FormatInt(bid, 10))
	go func(tg int64) {
		sctx, scancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer scancel()
		s.notifyDM(sctx, tg, text)
	}(ownerTg.Int64)
}
