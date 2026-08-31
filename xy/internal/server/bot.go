package server

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
)

// xy's login bot runs here, in the server process. It used to be its own
// systemd unit that reached these same code paths over loopback HTTP behind a
// shared secret, and answered a second loopback endpoint about its own health —
// a bridge in each direction, to cross a boundary that bought nothing.
//
// XY_BOT_TOKEN is the switch: an instance that has one polls, an instance that
// does not, doesn't. That is how staging and a dev checkout stay out of prod's
// updates; tgbot.AcquirePollLock is what happens when someone gets it wrong.

var botTexts = tgbot.Texts{
	Help: "Не понял. Пришли код регистрации с сайта или /login.",
	Down: "Сервер недоступен, попробуй позже.",
}

// startBot begins polling if this instance holds both the token and the host's
// claim on it. It never fails the boot: a server with no bot serves everything
// else, and the login page says telegram is not on offer.
func (s *server) startBot(ctx context.Context) {
	token := strings.TrimSpace(os.Getenv("XY_BOT_TOKEN"))
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
		_ = s.bot.Run(ctx, recovering(tgbot.LoginHandler(botRegistrar{s}, botTexts)))
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

// botRegistrar answers the conversation from inside the server, under xy's
// write-transaction discipline — the same writes the loopback bridge carried.
type botRegistrar struct{ s *server }

func (b botRegistrar) Register(ctx context.Context, code string, from tgbot.From) (string, error) {
	now := time.Now()
	msg := "Код не найден или истёк. Начни вход на сайте заново."
	err := b.s.withWriteTx(ctx, "tg-register", func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, tgbridge.ConsumeRegisterSQL,
			from.UserID, nullStr(from.Username), nullStr(from.Name),
			rfc3339(now), strings.TrimSpace(code), rfc3339(now))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			msg = "Готово! Вернись на сайт — вход подтверждён."
		}
		return nil
	})
	return msg, err
}

// Login answers a bare /start or /login — including a deep-link /start whose
// payload the client dropped (a known Telegram behavior for users who already
// started the bot). The code the site shows is the only thing that binds this
// chat to the browser, so point them back at it.
func (botRegistrar) Login(context.Context, tgbot.From) (string, error) {
	return "Пришлите код со страницы входа. Если его нет — откройте " +
		publicURL() + "/login и нажмите «Войти через телеграм».", nil
}

// botPolling reports whether this instance's bot is actually working — not
// whether the process is up, which was never the question. In-process that is
// a timestamp instead of the 300ms loopback probe behind a 5s cache it used to
// take to ask.
func (s *server) botPolling() bool {
	return s.bot != nil && tgbot.HealthOf(s.bot, time.Now()).OK
}

// notifyDM knocks on a user's telegram door. Best-effort by contract; does
// nothing on an instance that runs no bot.
func (s *server) notifyDM(ctx context.Context, tgUserID int64, text string) {
	if s.bot == nil {
		return
	}
	s.bot.Send(ctx, tgUserID, text)
}
