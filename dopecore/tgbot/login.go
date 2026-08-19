package tgbot

import (
	"context"
	"log"
	"strings"

	"pecheny.me/dopecore/tgbridge"
)

// The login conversation both bots hold. A pasted code, or the code a /start
// deep link carries, registers through the server; /login, a bare /start and
// any other command ask the server for its "go to the site" reply; text that
// cannot be a code gets the app's help without a round trip. Env names and
// copy stay in each bot's main; the server side is tgbridge.

// IntentKind is what an incoming message asks the bot to do.
type IntentKind int

const (
	IntentIgnore   IntentKind = iota // empty message
	IntentLogin                      // a command without a code: point at the site
	IntentRegister                   // consume a code
	IntentHelp                       // text that is not a code: explain, no server round-trip
)

type Intent struct {
	Kind IntentKind
	Code string // set when Kind == IntentRegister
}

// Classify decides what a message means. A deep-link /start arrives as
// "/start <code>" (t.me/<bot>?start=<code>), and in a group as
// "/start@<bot> <code>" — the code MUST be pulled from the command argument, or
// the /start prefix keeps it out of the plain-code branch and it is silently
// dropped.
func Classify(text string) Intent {
	text = strings.TrimSpace(text)
	if text == "" {
		return Intent{Kind: IntentIgnore}
	}
	if strings.HasPrefix(text, "/") {
		if commandName(text) == "/start" {
			if arg := commandArg(text); arg != "" {
				return classifyCode(arg)
			}
		}
		return Intent{Kind: IntentLogin}
	}
	return classifyCode(text)
}

// classifyCode takes a pasted code or a /start argument; anything that cannot
// be a code gets the help text instead of a server call.
func classifyCode(raw string) Intent {
	code := strings.ToUpper(strings.TrimSpace(raw))
	if !LooksLikeCode(code) {
		return Intent{Kind: IntentHelp}
	}
	return Intent{Kind: IntentRegister, Code: code}
}

// LooksLikeCode is the shape of a login code: base32 (A–Z, 2–7), 4–64 chars.
func LooksLikeCode(s string) bool {
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

func commandArg(text string) string {
	parts := strings.Fields(text)
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}

// Texts is what the app itself says in the conversation; every other reply
// comes from its server.
type Texts struct {
	Help string // a message that is neither a code nor a command
	Down string // the server could not be reached, or answered nothing
}

// LoginHandler is the bot's Handler for the conversation over bridge.
func LoginHandler(bridge *Bridge, texts Texts) Handler {
	return func(ctx context.Context, c *Client, u Update) {
		act := Classify(u.Message.Text)
		if act.Kind == IntentIgnore {
			return
		}
		from := u.Message.From
		reply := texts.Help
		switch act.Kind {
		case IntentLogin:
			reply = call(ctx, bridge, "/api/telegram/login", tgbridge.LoginRequest{
				TelegramUserID: from.ID, TelegramUsername: from.Username, TelegramName: from.DisplayName(),
			}, texts.Down)
		case IntentRegister:
			reply = call(ctx, bridge, "/api/telegram/register", tgbridge.RegisterRequest{
				Code: act.Code, TelegramUserID: from.ID, TelegramUsername: from.Username, TelegramName: from.DisplayName(),
			}, texts.Down)
		}
		c.Send(ctx, u.Message.Chat.ID, reply)
	}
}

func call(ctx context.Context, bridge *Bridge, path string, payload any, down string) string {
	msg, err := bridge.Call(ctx, path, payload)
	if err != nil {
		log.Print(err)
		return down
	}
	if msg == "" {
		log.Printf("bridge %s: empty message", path)
		return down
	}
	return msg
}
