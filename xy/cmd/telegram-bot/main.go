// Command telegram-bot is xy's login bot. It holds no database handle; it
// bridges Telegram users to the server through the shared-secret endpoints
// (/api/telegram/register, /api/telegram/login), mirroring dope's bot design.
//
// Config (env):
//
//	XY_BOT_TOKEN   Telegram Bot API token
//	XY_BOT_SECRET  shared secret, must match the server's XY_BOT_SECRET
//	XY_SERVER_URL  base URL of the xy server (default http://localhost:9673)
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func main() {
	token := os.Getenv("XY_BOT_TOKEN")
	secret := os.Getenv("XY_BOT_SECRET")
	server := os.Getenv("XY_SERVER_URL")
	if server == "" {
		server = "http://localhost:9673"
	}
	if token == "" || secret == "" {
		log.Fatal("XY_BOT_TOKEN and XY_BOT_SECRET are required")
	}
	bot := &bot{token: token, secret: secret, server: strings.TrimRight(server, "/"), client: &http.Client{Timeout: 70 * time.Second}}
	log.Println("xy telegram bot started")
	bot.run()
}

type bot struct {
	token, secret, server string
	client                *http.Client
	offset                int64
}

type tgUpdate struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		Chat struct {
			ID int64 `json:"id"`
		} `json:"chat"`
		From struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
		} `json:"from"`
		Text string `json:"text"`
	} `json:"message"`
}

func (b *bot) run() {
	for {
		updates, err := b.getUpdates()
		if err != nil {
			log.Printf("getUpdates: %v", err)
			time.Sleep(3 * time.Second)
			continue
		}
		for _, u := range updates {
			b.offset = u.UpdateID + 1
			if u.Message == nil {
				continue
			}
			b.handle(u)
		}
	}
}

func (b *bot) getUpdates() ([]tgUpdate, error) {
	u := b.api("getUpdates") + "?timeout=60&offset=" + url.QueryEscape(itoa(b.offset))
	resp, err := b.client.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		OK     bool       `json:"ok"`
		Result []tgUpdate `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Result, nil
}

func (b *bot) handle(u tgUpdate) {
	text := strings.TrimSpace(u.Message.Text)
	from := u.Message.From
	var reply string
	switch {
	case text == "/login" || text == "/start":
		reply = b.bridge("/api/telegram/login", map[string]any{
			"telegram_user_id": from.ID, "telegram_username": from.Username,
		})
	case strings.HasPrefix(text, "/start "):
		code := strings.TrimSpace(strings.TrimPrefix(text, "/start "))
		reply = b.bridge("/api/telegram/register", map[string]any{
			"code": code, "telegram_user_id": from.ID, "telegram_username": from.Username,
		})
	default:
		// A bare token is treated as a registration code.
		reply = b.bridge("/api/telegram/register", map[string]any{
			"code": text, "telegram_user_id": from.ID, "telegram_username": from.Username,
		})
	}
	if reply == "" {
		reply = "Не понял. Пришли код-приглашение или /login."
	}
	b.send(u.Message.Chat.ID, reply)
}

// bridge POSTs to a shared-secret server endpoint and returns the message field.
func (b *bot) bridge(path string, body map[string]any) string {
	buf, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, b.server+path, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Bot-Secret", b.secret)
	resp, err := b.client.Do(req)
	if err != nil {
		log.Printf("bridge %s: %v", path, err)
		return "Сервер недоступен, попробуй позже."
	}
	defer resp.Body.Close()
	var out struct {
		Message string `json:"message"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out.Message
}

func (b *bot) send(chatID int64, text string) {
	body, _ := json.Marshal(map[string]any{"chat_id": chatID, "text": text})
	resp, err := b.client.Post(b.api("sendMessage"), "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("send: %v", err)
		return
	}
	resp.Body.Close()
}

func (b *bot) api(method string) string {
	return "https://api.telegram.org/bot" + b.token + "/" + method
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
