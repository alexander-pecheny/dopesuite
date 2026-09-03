package server

// The unread rule, once. A Timeline event is in one of two buckets — the
// comment bucket (a comment, or a reaction to one) or the content bucket
// (a description edit, a label, an attachment, a reaction on the card) — and a
// reader keeps one watermark per bucket per card (card_reads). An event counts
// when it is live, on a live card, by someone else, and past the reader's
// watermark for its bucket; a comment is a Mention when it names the reader
// (event_mentions) or replies to one of theirs. The board list, the snapshot,
// the activity feed and the mark-all-read button compose these fragments; `e` is the
// timeline_events row, `cr` the reader's card_reads row.
const (
	sqlCommentBucket = `(e.type = 'comment' or (e.type = 'reaction' and e.reply_to_id is not null))`
	sqlUnreadComment = `(` + sqlCommentBucket + ` and e.id > coalesce(cr.comment_read_id,0))`
	sqlUnreadContent = `(not ` + sqlCommentBucket + ` and e.id > coalesce(cr.content_read_id,0))`
	sqlUnread        = `(` + sqlUnreadComment + ` or ` + sqlUnreadContent + `)`
	// sqlEventsOfOthers joins the card and the reader's watermarks and keeps the
	// events that can be unread at all; it takes the reader's user id once.
	sqlEventsOfOthers = `
from timeline_events e
join cards c on c.id = e.card_id and c.deleted_at is null
left join card_reads cr on cr.card_id = e.card_id and cr.user_id = ?
where e.deleted_at is null and e.author_user_id is not null and e.author_user_id <> ?`
)

// sqlMentionExplicit and sqlMentionReply are the two ways a comment mentions
// a reader, with the reader as a SQL expression (a parameter, or a column).
func sqlMentionExplicit(reader string) string {
	return `(e.type = 'comment' and exists(select 1 from event_mentions em where em.event_id = e.id and em.user_id = ` + reader + `))`
}

func sqlMentionReply(reader string) string {
	return `(e.type = 'comment' and exists(select 1 from timeline_events p where p.id = e.reply_to_id and p.author_user_id = ` + reader + `))`
}

func sqlMention(reader string) string {
	return `(` + sqlMentionExplicit(reader) + ` or ` + sqlMentionReply(reader) + `)`
}
