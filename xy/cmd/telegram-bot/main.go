// Command telegram-bot is xy's login bot. It holds no database handle; it
// bridges Telegram users to the server through the shared-secret endpoints
// (/api/telegram/register, /api/telegram/login) using dopecore/tgbot, the
// poll/bridge/send machinery it shares with dope's bot.
//
// Config (env):
//
//	XY_BOT_TOKEN   Telegram Bot API token
//	XY_BOT_SECRET  shared secret, must match the server's XY_BOT_SECRET
//	XY_SERVER_URL  base URL of the xy server (default http://localhost:9673)
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
	defaultServerURL = "http://localhost:9673"

	fallbackMessage   = "Не понял. Пришли код регистрации с сайта или /login."
	serverDownMessage = "Сервер недоступен, попробуй позже."
)

func main() {
	token := os.Getenv("XY_BOT_TOKEN")
	secret := os.Getenv("XY_BOT_SECRET")
	server := os.Getenv("XY_SERVER_URL")
	if server == "" {
		server = defaultServerURL
	}
	if token == "" || secret == "" {
		log.Fatal("XY_BOT_TOKEN and XY_BOT_SECRET are required")
	}

	client := tgbot.New(tgbot.Config{Token: token, PollTimeout: 60 * time.Second, HTTPTimeout: 70 * time.Second})
	bridge := tgbot.NewBridge(server, secret, client.HTTP())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Println("xy telegram bot started")
	if err := client.Run(ctx, handler(bridge)); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("bot: %v", err)
	}
}

func handler(bridge *tgbot.Bridge) tgbot.Handler {
	return func(ctx context.Context, c *tgbot.Client, u tgbot.Update) {
		text := strings.TrimSpace(u.Message.Text)
		from := u.Message.From

		var reply string
		switch {
		case text == "/login" || text == "/start":
			reply = call(ctx, bridge, "/api/telegram/login", map[string]any{
				"telegram_user_id": from.ID, "telegram_username": from.Username, "telegram_name": from.DisplayName(),
			})
		case strings.HasPrefix(text, "/start "):
			code := strings.TrimSpace(strings.TrimPrefix(text, "/start "))
			reply = register(ctx, bridge, code, from)
		default:
			// A bare token is treated as a registration code.
			reply = register(ctx, bridge, text, from)
		}
		if reply == "" {
			reply = fallbackMessage
		}
		c.Send(ctx, u.Message.Chat.ID, reply)
	}
}

func register(ctx context.Context, bridge *tgbot.Bridge, code string, from *tgbot.User) string {
	return call(ctx, bridge, "/api/telegram/register", map[string]any{
		"code": code, "telegram_user_id": from.ID, "telegram_username": from.Username, "telegram_name": from.DisplayName(),
	})
}

func call(ctx context.Context, bridge *tgbot.Bridge, path string, payload map[string]any) string {
	msg, err := bridge.Call(ctx, path, payload)
	if err != nil {
		log.Print(err)
		return serverDownMessage
	}
	return msg
}
