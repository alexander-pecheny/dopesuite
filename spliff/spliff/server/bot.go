package spliffserver

import (
	"context"
	"database/sql"
	"log"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"pecheny.me/dopecore/buildinfo"
	"pecheny.me/dopecore/tgbot"
	"pecheny.me/dopecore/tgbridge"

	spliffstrings "spliff/i18nstrings"
)

// Spliff's login bot polls in the server process (root ADR-0005).
// SPLIFF_BOT_TOKEN is the switch: an instance that holds one polls, an instance
// that does not, does not — and says telegram login is not on offer. That is
// how staging and a dev checkout stay out of prod's updates;
// tgbot.AcquirePollLock is what happens when somebody gets it wrong.

func botTexts() tgbot.Texts {
	return tgbot.Texts{
		Help: spliffstrings.Default.Bot.Texts.Help(),
		Down: spliffstrings.Default.Bot.Texts.Down(),
	}
}

// startBot begins polling if this instance holds both the token and the host's
// claim on it. It never fails the boot: a server with no bot serves everything
// else.
func (s *server) startBot(ctx context.Context) {
	token := strings.TrimSpace(os.Getenv("SPLIFF_BOT_TOKEN"))
	if token == "" {
		return
	}
	release, err := tgbot.AcquirePollLock(token)
	if err != nil {
		log.Printf("telegram bot: not polling: %v", err)
		return
	}
	s.bot = tgbot.New(tgbot.Config{
		Token:          token,
		PollTimeout:    60 * time.Second,
		HTTPTimeout:    70 * time.Second,
		AllowedUpdates: []string{"message"},
	})
	log.Printf("telegram bot %s polling (token %s)", buildinfo.Version(), tgbot.TokenHash(token))
	go func() {
		defer release()
		_ = s.bot.Run(ctx, recovering(tgbot.LoginHandler(botRegistrar{s}, botTexts())))
	}()
}

// recovering keeps one malformed message from taking the web server down with
// it — the risk the bot did not carry while it was its own process.
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

// botRegistrar answers the login conversation from inside the server, under
// Spliff's own write-transaction discipline.
type botRegistrar struct{ s *server }

func (b botRegistrar) Register(ctx context.Context, code string, from tgbot.From) (string, error) {
	now := time.Now()
	msg := spliffstrings.Default.Bot.Register.Expired()
	err := b.s.withWriteTx(ctx, "tg-register", func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, tgbridge.ConsumeRegisterSQL,
			from.UserID, nullStr(from.Username), nullStr(from.Name),
			rfc3339(now), strings.TrimSpace(code), rfc3339(now))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			msg = spliffstrings.Default.Bot.Register.Done()
		}
		return nil
	})
	return msg, err
}

// Login answers a bare /start or /login — including a deep-link /start whose
// payload the client dropped. The code the site shows is the only thing that
// binds this chat to the browser, so point them back at it.
func (botRegistrar) Login(context.Context, tgbot.From) (string, error) {
	return spliffstrings.Default.Bot.Login.Hint(publicURL()), nil
}

func nullStr(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

// botPolling reports whether this instance's bot is actually working — not
// whether the process is up, which was never the question.
func (s *server) botPolling() bool {
	return s.bot != nil && tgbot.HealthOf(s.bot, time.Now()).OK
}

// notifyDM knocks on a person's telegram door. Best-effort by contract: it does
// nothing on an instance that runs no bot, and nothing waits for it.
func (s *server) notifyDM(ctx context.Context, tgUserID int64, text string) {
	if s.bot == nil {
		return
	}
	s.bot.Send(ctx, tgUserID, text)
}
