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

// intentKind is what an incoming message asks the bot to do.
type intentKind int

const (
	intentIgnore   intentKind = iota // empty message
	intentLogin                      // /login or a bare /start: point at the site
	intentRegister                   // consume a code (pasted, or from a /start deep link)
)

type intent struct {
	kind intentKind
	code string // set when kind == intentRegister
}

// classify decides what a message means. A deep-link /start arrives as
// "/start <code>" (t.me/<bot>?start=<code>), and in a group as
// "/start@<bot> <code>" — the code MUST be pulled from the command argument, or
// the /start prefix keeps it out of the plain-code branch and it is silently
// dropped. Anything else that isn't a pasted code is a request for the site.
func classify(text string) intent {
	text = strings.TrimSpace(text)
	if text == "" {
		return intent{kind: intentIgnore}
	}
	if strings.HasPrefix(text, "/") {
		if commandName(text) == "/start" {
			if arg := commandArg(text); arg != "" {
				return intent{kind: intentRegister, code: strings.ToUpper(arg)}
			}
		}
		return intent{kind: intentLogin}
	}
	return intent{kind: intentRegister, code: strings.ToUpper(text)}
}

func handler(bridge *tgbot.Bridge) tgbot.Handler {
	return func(ctx context.Context, c *tgbot.Client, u tgbot.Update) {
		act := classify(u.Message.Text)
		if act.kind == intentIgnore {
			return
		}
		from := u.Message.From
		var reply string
		switch act.kind {
		case intentLogin:
			reply = call(ctx, bridge, "/api/telegram/login", map[string]any{
				"telegram_user_id": from.ID, "telegram_username": from.Username, "telegram_name": from.DisplayName(),
			})
		case intentRegister:
			reply = register(ctx, bridge, act.code, from)
		}
		if reply == "" {
			reply = fallbackMessage
		}
		c.Send(ctx, u.Message.Chat.ID, reply)
	}
}

// commandName returns the leading /command, lowercased and stripped of any
// @botname suffix (Telegram appends it in groups).
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

// commandArg returns the first whitespace-separated argument after the command
// word, or "" when there is none.
func commandArg(text string) string {
	parts := strings.Fields(text)
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
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
