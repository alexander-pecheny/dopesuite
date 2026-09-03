package dopeserver

import (
	"context"
	"log"
	"os"
	"runtime/debug"
	"strings"
	"time"

	dopestrings "dope/i18nstrings"
	"pecheny.me/dopecore/buildinfo"
	"pecheny.me/dopecore/tgbot"
)

// dope's login bot runs here, in the server process. It used to be its own
// systemd unit calling the server's /api/telegram/* endpoints over loopback
// behind a shared secret — a hop that existed only because the bot was not
// allowed to touch fest.db, which the server it was calling already owned.
//
// TELEGRAM_BOT_TOKEN is the switch: an instance with one polls, an instance
// without one does not. Staging has none. tgbot.AcquirePollLock is what stops a
// second instance on this host from claiming the same token anyway.

var botTexts = tgbot.Texts{
	Help: dopestrings.Default.Server.Bot.Help(),
	Down: dopestrings.Default.Server.Bot.Down(),
}

// startBot begins polling if this instance holds both the token and the host's
// claim on it. It never fails the boot: a fest runs fine with no way in by
// telegram, and a login that silently goes to another process is worse.
func (s *server) startBot(ctx context.Context) {
	token := strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN"))
	if token == "" {
		return
	}
	release, err := tgbot.AcquirePollLock(token)
	if err != nil {
		log.Printf("telegram bot: not polling: %v", err)
		return
	}
	client := tgbot.New(tgbot.Config{
		Token:          token,
		PollTimeout:    30 * time.Second,
		AllowedUpdates: []string{"message"},
	})
	log.Printf("telegram bot %s polling (token %s)", buildinfo.Version(), tgbot.TokenHash(token))
	go func() {
		defer release()
		_ = client.Run(ctx, recovering(tgbot.LoginHandler(botRegistrar{s}, botTexts)))
	}()
}

// recovering keeps one malformed message from taking the fest down with it —
// the risk the bot did not carry while it was its own process.
func recovering(h tgbot.Handler) tgbot.Handler {
	return func(ctx context.Context, c *tgbot.Client, u tgbot.Update) {
		defer func() {
			if p := recover(); p != nil {
				log.Printf("telegram bot: panic on update %d: %v\n%s", u.UpdateID, p, debug.Stack())
			}
		}()
		h(ctx, c, u)
	}
}

// botRegistrar answers the conversation from inside the server, through the
// same two calls the loopback handlers wrapped — dope's reply text, dope's
// global write mutex.
type botRegistrar struct{ s *server }

func (b botRegistrar) Register(ctx context.Context, code string, from tgbot.From) (string, error) {
	return b.s.tgBridge().TelegramConsumeRegister(ctx,
		strings.ToUpper(strings.TrimSpace(code)), from.UserID, from.Username, from.Name), nil
}

func (b botRegistrar) Login(ctx context.Context, from tgbot.From) (string, error) {
	return b.s.tgBridge().TelegramIssueLogin(ctx, from.UserID, from.Username), nil
}
