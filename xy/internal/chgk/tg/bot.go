package tg

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"pecheny.me/dopecore/tgbot"
)

// The export runs its own bot, in this process, for the two things a bot token
// alone cannot do: recognising the person driving the export (they send it a
// code), and seeing a channel post arrive in the discussion group so the replies
// have something to hang off. chgksuite runs a second Python process for this
// and passes the updates through a sqlite file; xy and dope have run their login
// bots in-process for a while, and this is the same shape — one long-poll loop,
// and waiters reading what it hears.

// historyLimit is how many recent updates a waiter can still match. The copy of
// a channel post can reach the discussion group before anything asks for it, so
// the arrivals are kept, briefly, rather than only awaited.
const historyLimit = 200

// historyWindow mirrors chgksuite's "last five minutes" query: older updates are
// somebody else's conversation.
const historyWindow = 5 * time.Minute

// Bot is the export's poller and the waiters over it.
type Bot struct {
	client *tgbot.Client

	mu      sync.Mutex
	seen    []seenUpdate
	waiters []*waiter
}

type seenUpdate struct {
	at time.Time
	u  tgbot.Update
}

type waiter struct {
	match func(tgbot.Update) bool
	found chan tgbot.Update
}

// NewBot prepares the poller for a token. Start it before waiting on anything.
func NewBot(token string) *Bot {
	return &Bot{client: tgbot.New(tgbot.Config{
		Token:          token,
		PollTimeout:    30 * time.Second,
		AllowedUpdates: []string{"message", "channel_post"},
	})}
}

// Client is the underlying Bot API client, for the calls the poster makes.
func (b *Bot) Client() *tgbot.Client { return b.client }

// Start claims this host's right to poll the token and begins polling. The
// returned stop releases both.
func (b *Bot) Start(ctx context.Context) (stop func(), err error) {
	release, err := tgbot.AcquirePollLock(b.client.Token())
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer release()
		_ = b.client.RunAll(ctx, func(_ context.Context, _ *tgbot.Client, u tgbot.Update) {
			b.offer(u)
		})
	}()
	return func() {
		cancel()
		<-done
	}, nil
}

// offer hands an update to whoever is waiting for it, and remembers it for
// whoever asks next.
func (b *Bot) offer(u tgbot.Update) {
	b.mu.Lock()
	defer b.mu.Unlock()
	kept := b.waiters[:0]
	for _, w := range b.waiters {
		if w.match(u) {
			w.found <- u
			continue
		}
		kept = append(kept, w)
	}
	b.waiters = kept
	b.seen = append(b.seen, seenUpdate{at: time.Now(), u: u})
	if len(b.seen) > historyLimit {
		b.seen = b.seen[len(b.seen)-historyLimit:]
	}
}

// ErrTimeout is a wait that ran out.
var ErrTimeout = errors.New("timed out waiting for a telegram message")

// Wait returns the first update matching pred, from what has already arrived or
// from what arrives within timeout.
func (b *Bot) Wait(ctx context.Context, timeout time.Duration, pred func(tgbot.Update) bool) (tgbot.Update, error) {
	w := &waiter{match: pred, found: make(chan tgbot.Update, 1)}
	b.mu.Lock()
	cutoff := time.Now().Add(-historyWindow)
	for _, s := range b.seen {
		if s.at.After(cutoff) && pred(s.u) {
			b.mu.Unlock()
			return s.u, nil
		}
	}
	b.waiters = append(b.waiters, w)
	b.mu.Unlock()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case u := <-w.found:
		return u, nil
	case <-timer.C:
		b.drop(w)
		return tgbot.Update{}, ErrTimeout
	case <-ctx.Done():
		b.drop(w)
		return tgbot.Update{}, ctx.Err()
	}
}

func (b *Bot) drop(w *waiter) {
	b.mu.Lock()
	defer b.mu.Unlock()
	kept := b.waiters[:0]
	for _, x := range b.waiters {
		if x != w {
			kept = append(kept, x)
		}
	}
	b.waiters = kept
}

// WaitForCode waits for someone to send the code to the bot in a private chat,
// and returns that chat: the person driving this export, and where the bot can
// talk back to them.
func (b *Bot) WaitForCode(ctx context.Context, code string, timeout time.Duration) (int64, error) {
	u, err := b.Wait(ctx, timeout, func(u tgbot.Update) bool {
		return u.Message.Chat != nil && u.Message.Chat.Type == "private" &&
			strings.Contains(u.Message.Text, code)
	})
	if err != nil {
		return 0, err
	}
	return u.Message.Chat.ID, nil
}

// WaitForForwardedChannel waits for a message forwarded from a channel and
// returns that channel's id.
func (b *Bot) WaitForForwardedChannel(ctx context.Context, timeout time.Duration) (int64, error) {
	u, err := b.Wait(ctx, timeout, func(u tgbot.Update) bool {
		return u.Message.ForwardFromChat != nil && u.Message.ForwardFromChat.Type == "channel"
	})
	if err != nil {
		return 0, err
	}
	return u.Message.ForwardFromChat.ID, nil
}

// WaitForChatMessage waits for the code to be written in a group and returns
// that group's id — the only way a bot learns the id of a group nobody has
// named to it.
func (b *Bot) WaitForChatMessage(ctx context.Context, code string, timeout time.Duration) (int64, error) {
	u, err := b.Wait(ctx, timeout, func(u tgbot.Update) bool {
		return u.Message.Chat != nil && u.Message.Chat.Type != "private" &&
			strings.Contains(u.Message.Text, code)
	})
	if err != nil {
		return 0, err
	}
	return u.Message.Chat.ID, nil
}

// WaitForDiscussionCopy waits for Telegram to copy a channel post into the
// linked discussion group, and returns the copy's id there.
func (b *Bot) WaitForDiscussionCopy(ctx context.Context, channelID int64, chatID int64, messageID int64, timeout time.Duration) (int64, error) {
	u, err := b.Wait(ctx, timeout, func(u tgbot.Update) bool {
		return u.Message.Chat != nil && u.Message.Chat.ID == chatID &&
			u.Message.ForwardFromChat != nil &&
			u.Message.ForwardFromChat.ID == channelID &&
			u.Message.ForwardFromMessageID == messageID
	})
	if err != nil {
		return 0, fmt.Errorf("channel post %d never reached the discussion group: %w", messageID, err)
	}
	return u.Message.MessageID, nil
}
