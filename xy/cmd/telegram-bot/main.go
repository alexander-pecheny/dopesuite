// Command telegram-bot is xy's login bot. It holds no database handle; it
// bridges Telegram users to the server through the shared-secret endpoints
// (/api/telegram/register, /api/telegram/login). The conversation itself is
// dopecore/tgbot.LoginHandler, shared with dope's bot; this file is xy's env
// and xy's words.
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

	"pecheny.me/dopecore/buildinfo"
	"pecheny.me/dopecore/tgbot"
	"pecheny.me/dopecore/tgbridge"
)

const defaultServerURL = "http://localhost:9673"

var texts = tgbot.Texts{
	Help: "Не понял. Пришли код регистрации с сайта или /login.",
	Down: "Сервер недоступен, попробуй позже.",
}

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

	// The server shares this host and asks here whether telegram login is worth
	// offering. Loopback, and a working default on both sides on purpose: a
	// REQUIRED new variable would mean a deploy that installs a healthy bot the
	// server then reports as unreachable.
	go tgbot.ServeLocal(ctx, healthAddr(), client, secret)

	log.Printf("xy telegram bot %s started", buildinfo.Version())
	if err := client.Run(ctx, tgbot.LoginHandler(bridge, texts)); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("bot: %v", err)
	}
}

// healthAddr is where the bot answers "am I working?". Shared with the server
// as a constant, overridable together when the default port is taken.
func healthAddr() string {
	if a := strings.TrimSpace(os.Getenv("XY_BOT_HEALTH_ADDR")); a != "" {
		return a
	}
	return tgbridge.DefaultHealthAddr
}
