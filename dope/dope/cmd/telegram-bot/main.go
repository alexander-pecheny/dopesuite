// Command telegram-bot is dope's login bot. It holds NO database handle: all
// login/registration writes go through the server's shared-secret Telegram
// bridge endpoints, so the server stays the sole writer of fest.db. The
// conversation itself is dopecore/tgbot.LoginHandler, shared with xy's bot;
// this file is dope's env and dope's words.
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
)

const (
	pollTimeout = 30 * time.Second

	defaultServerURL = "http://localhost:8090"

	loginURL = "https://dope.pecheny.me/login"
)

var texts = tgbot.Texts{
	Help: "Этот бот подтверждает вход на dope.pecheny.me.\n\n" +
		"Откройте " + loginURL + ", нажмите «Войти через телеграм» и пришлите мне код, который покажет сайт.",
	Down: "Произошла ошибка. Попробуй еще раз через минуту.",
}

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

	// Loopback "am I working?" for the host's monitoring; its own default port,
	// since a staging box may run xy's bot on tgbridge.DefaultHealthAddr.
	go tgbot.ServeHealth(ctx, getenvDefault("DOPE_BOT_HEALTH_ADDR", "127.0.0.1:9677"), client)

	log.Printf("telegram bot %s polling, server=%s", buildinfo.Version(), bridge.ServerURL())
	if err := client.Run(ctx, tgbot.LoginHandler(bridge, texts)); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("bot: %v", err)
	}
}

func getenvDefault(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
