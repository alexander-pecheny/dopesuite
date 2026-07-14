// Command telegram-bot is dope's login bot. It holds NO database handle: all
// login/registration writes go through the server's shared-secret Telegram
// bridge endpoints, so the server stays the sole writer of fest.db. The
// poll/bridge/send machinery is dopecore/tgbot, shared with xy's bot.
package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"pecheny.me/dopecore/tgbot"
)

const (
	pollTimeout = 30 * time.Second

	defaultServerURL = "http://localhost:8090"

	registerURL = "https://dope.pecheny.me/register"
	loginURL    = "https://dope.pecheny.me/login"
)

const startMessage = "Этот бот выдает одноразовые коды для входа на dope.pecheny.me.\n\n" +
	"• Зарегистрироваться по инвайту: " + registerURL + "\n" +
	"• Войти в существующий аккаунт: " + loginURL + "\n\n" +
	"После регистрации на сайте пришли мне код, который он покажет. Чтобы войти — введи логин на сайте; если выберешь код, я пришлю его сюда."

// botErrorMessage is shown when the bot can't reach the server bridge (network
// error / non-200). The bridge itself returns its own user-facing messages.
const botErrorMessage = "Произошла ошибка. Попробуй еще раз через минуту."

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	token := strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN"))
	serverURL := strings.TrimRight(getenvDefault("DOPE_SERVER_URL", defaultServerURL), "/")
	secret := os.Getenv("DOPE_BOT_SECRET")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if secret == "" {
		log.Printf("DOPE_BOT_SECRET is not set; the server bridge will reject all requests")
	}

	if token == "" {
		log.Printf("TELEGRAM_BOT_TOKEN is not set; running in stub mode (no updates will be processed)")
		<-ctx.Done()
		return
	}

	client := tgbot.New(tgbot.Config{
		Token:          token,
		PollTimeout:    pollTimeout,
		AllowedUpdates: []string{"message"},
	})
	bridge := tgbot.NewBridge(serverURL, secret, client.HTTP())

	log.Printf("telegram bot polling, server=%s", bridge.ServerURL())
	if err := client.Run(ctx, handler(bridge)); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("bot: %v", err)
	}
}

func getenvDefault(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func handler(bridge *tgbot.Bridge) tgbot.Handler {
	return func(ctx context.Context, c *tgbot.Client, u tgbot.Update) {
		text := strings.TrimSpace(u.Message.Text)
		if text == "" {
			return
		}
		chatID := u.Message.Chat.ID
		from := u.Message.From

		if strings.HasPrefix(text, "/") {
			switch commandName(text) {
			case "/login":
				c.SendHTML(ctx, chatID, call(ctx, bridge, "/api/telegram/login", map[string]any{
					"telegram_user_id":  from.ID,
					"telegram_username": from.Username,
				}))
			default:
				c.Send(ctx, chatID, startMessage)
			}
			return
		}

		// Plain text is treated as a register code. Triage locally so obvious
		// non-codes get the help text without a server round-trip.
		code := strings.ToUpper(text)
		if !looksLikeCode(code) {
			c.Send(ctx, chatID, startMessage)
			return
		}
		c.Send(ctx, chatID, call(ctx, bridge, "/api/telegram/register", map[string]any{
			"code":              code,
			"telegram_user_id":  from.ID,
			"telegram_username": from.Username,
		}))
	}
}

func call(ctx context.Context, bridge *tgbot.Bridge, path string, payload map[string]any) string {
	msg, err := bridge.Call(ctx, path, payload)
	if err != nil || msg == "" {
		if err != nil {
			log.Print(err)
		} else {
			log.Printf("bridge %s: empty message", path)
		}
		return botErrorMessage
	}
	return msg
}

func commandName(text string) string {
	parts := strings.Fields(text)
	if len(parts) == 0 {
		return ""
	}
	cmd := parts[0]
	if i := strings.Index(cmd, "@"); i >= 0 {
		cmd = cmd[:i]
	}
	return strings.ToLower(cmd)
}

func looksLikeCode(s string) bool {
	if len(s) < 4 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !(r >= 'A' && r <= 'Z') && !(r >= '2' && r <= '7') {
			return false
		}
	}
	return true
}
