package qyserver

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"code.pecheny.me/pecheny/dopesuite/dopecore/tgbot"

	"qy/qy/platform/telegram"
)

// Telegram's web page does not show a post in the newer formatting, so a
// nominator forwards such a post to the bot. The bot keeps its text under the
// post's link, and the form's load finds it there before it asks the web page.

// botHandler is the bot's whole conversation: a forwarded channel post is
// kept, anything else is the login conversation.
func (s *server) botHandler() tgbot.Handler {
	login := tgbot.LoginHandler(botRegistrar{s}, botTexts())
	return func(ctx context.Context, c *tgbot.Client, u tgbot.Update) {
		if reply, ok := s.keepForward(ctx, u.Message); ok {
			c.Send(ctx, u.Message.Chat.ID, reply)
			return
		}
		login(ctx, c, u)
	}
}

// forwardedFrom is the channel and the post number a forwarded message came
// from; ok is false for anything that is not a channel post.
func forwardedFrom(m *tgbot.Message) (channel *tgbot.Chat, id int64, ok bool) {
	if o := m.ForwardOrigin; o != nil && o.Type == "channel" && o.Chat != nil {
		return o.Chat, o.MessageID, true
	}
	if m.ForwardFromChat != nil && m.ForwardFromChat.Type == "channel" {
		return m.ForwardFromChat, m.ForwardFromMessageID, true
	}
	return nil, 0, false
}

// keepForward stores a channel post forwarded in a private chat and says what
// to do next; ok is false when the message is not one.
func (s *server) keepForward(ctx context.Context, m *tgbot.Message) (string, bool) {
	if m == nil || m.Chat == nil || m.Chat.Type != "private" || m.From == nil {
		return "", false
	}
	channel, id, ok := forwardedFrom(m)
	if !ok {
		return "", false
	}
	if channel.Username == "" {
		return str.Bot.Forward.Private(), true
	}
	text := strings.TrimSpace(m.Text)
	if text == "" {
		text = strings.TrimSpace(m.Caption)
	}
	if text == "" {
		text = m.RichMessage.PlainText()
	}
	if text == "" {
		return str.Bot.Forward.Empty(), true
	}
	err := s.withWriteTx(ctx, "tg-forward", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
insert into telegram_posts(channel, message_id, text, forwarded_by, created_at) values(?, ?, ?, ?, ?)
on conflict(channel, message_id) do update set text = excluded.text, forwarded_by = excluded.forwarded_by, created_at = excluded.created_at`,
			channel.Username, id, text, m.From.ID, rfc3339(time.Now()))
		return err
	})
	if err != nil {
		return str.Bot.Texts.Down(), true
	}
	return str.Bot.Forward.Saved(telegram.Link(channel.Username, id)), true
}

// telegramPost is a post by its link: the copy forwarded to the bot, or else
// the post's web page.
func (s *server) telegramPost(ctx context.Context, link string) (telegram.Post, error) {
	if channel, id, ok := telegram.Ref(link); ok {
		var text string
		err := s.db.QueryRowContext(ctx, `select text from telegram_posts where channel = ? and message_id = ?`, channel, id).Scan(&text)
		if err == nil {
			return telegram.Post{Text: text}, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return telegram.Post{}, err
		}
	}
	return s.tg.Post(ctx, link)
}

// loadFailure is what the form says when a link did not load: for a telegram
// post, to forward it to the bot.
func loadFailure(link string) string {
	if telegram.IsPost(link) && botUsername() != "" {
		return str.Nominate.Slot.ForwardToBot("@" + botUsername())
	}
	return str.Nominate.Slot.LoadFailed()
}
