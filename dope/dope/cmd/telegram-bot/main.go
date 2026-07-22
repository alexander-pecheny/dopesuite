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

	loginURL = "https://dope.pecheny.me/login"
)

const startMessage = "Этот бот подтверждает вход на dope.pecheny.me.\n\n" +
	"Откройте " + loginURL + ", нажмите «Войти через телеграм» и пришлите мне код, который покажет сайт."

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

// intentKind is what an incoming message asks the bot to do.
type intentKind int

const (
	intentIgnore   intentKind = iota // empty message
	intentHelp                       // greet / show usage
	intentLogin                      // /login: point at the site
	intentRegister                   // consume a code (pasted, or from a /start deep link)
)

type intent struct {
	kind intentKind
	code string // set when kind == intentRegister
}

// classify decides what a message means. A deep-link /start arrives as
// "/start <code>" (t.me/<bot>?start=<code>) — the code MUST be pulled from its
// argument, since the /start prefix keeps it out of the plain-code branch. This
// is the bug the earlier bot had: /start<space><code> fell through to the help
// text and the code was dropped.
func classify(text string) intent {
	text = strings.TrimSpace(text)
	if text == "" {
		return intent{kind: intentIgnore}
	}
	if strings.HasPrefix(text, "/") {
		switch commandName(text) {
		case "/login":
			return intent{kind: intentLogin}
		case "/start":
			if arg := commandArg(text); arg != "" {
				return classifyCode(arg)
			}
			return intent{kind: intentHelp}
		default:
			return intent{kind: intentHelp}
		}
	}
	return classifyCode(text)
}

// classifyCode accepts a bare code (pasted or a /start argument), or falls back
// to help when it doesn't look like one — no server round-trip for obvious junk.
func classifyCode(raw string) intent {
	code := strings.ToUpper(strings.TrimSpace(raw))
	if !looksLikeCode(code) {
		return intent{kind: intentHelp}
	}
	return intent{kind: intentRegister, code: code}
}

func handler(bridge *tgbot.Bridge) tgbot.Handler {
	return func(ctx context.Context, c *tgbot.Client, u tgbot.Update) {
		act := classify(u.Message.Text)
		if act.kind == intentIgnore {
			return
		}
		chatID := u.Message.Chat.ID
		from := u.Message.From
		switch act.kind {
		case intentLogin:
			c.SendHTML(ctx, chatID, call(ctx, bridge, "/api/telegram/login", map[string]any{
				"telegram_user_id":  from.ID,
				"telegram_username": from.Username,
				"telegram_name":     from.DisplayName(),
			}))
		case intentRegister:
			c.Send(ctx, chatID, call(ctx, bridge, "/api/telegram/register", map[string]any{
				"code":              act.code,
				"telegram_user_id":  from.ID,
				"telegram_username": from.Username,
				"telegram_name":     from.DisplayName(),
			}))
		default:
			c.Send(ctx, chatID, startMessage)
		}
	}
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
