package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

const (
	apiBase     = "https://api.telegram.org"
	pollTimeout = 30

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

// botConfig is the bot's connection to the dope server. The bot holds NO
// database handle: all login/registration writes go through the server's
// Telegram bridge endpoints (shared-secret gated) so the server stays the sole
// writer of fest.db.
type botConfig struct {
	serverURL string
	secret    string
}

type tgUpdate struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		MessageID int64 `json:"message_id"`
		From      *struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
		} `json:"from"`
		Chat *struct {
			ID int64 `json:"id"`
		} `json:"chat"`
		Text string `json:"text"`
	} `json:"message"`
}

type tgResponse struct {
	OK     bool       `json:"ok"`
	Result []tgUpdate `json:"result"`
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	token := strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN"))
	cfg := botConfig{
		serverURL: strings.TrimRight(getenvDefault("DOPE_SERVER_URL", defaultServerURL), "/"),
		secret:    os.Getenv("DOPE_BOT_SECRET"),
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if cfg.secret == "" {
		log.Printf("DOPE_BOT_SECRET is not set; the server bridge will reject all requests")
	}

	if token == "" {
		log.Printf("TELEGRAM_BOT_TOKEN is not set; running in stub mode (no updates will be processed)")
		<-ctx.Done()
		return
	}

	log.Printf("telegram bot polling, server=%s", cfg.serverURL)
	if err := runBot(ctx, cfg, token); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("bot: %v", err)
	}
}

func getenvDefault(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func runBot(ctx context.Context, cfg botConfig, token string) error {
	client := &http.Client{Timeout: (pollTimeout + 10) * time.Second}
	var offset int64

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		updates, err := getUpdates(ctx, client, token, offset, pollTimeout)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			log.Printf("getUpdates: %v", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(3 * time.Second):
			}
			continue
		}
		for _, u := range updates {
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
			}
			if u.Message == nil || u.Message.From == nil || u.Message.Chat == nil {
				continue
			}
			handleMessage(ctx, cfg, client, token, u)
		}
	}
}

func getUpdates(ctx context.Context, c *http.Client, token string, offset int64, timeoutSec int) ([]tgUpdate, error) {
	values := url.Values{}
	values.Set("timeout", fmt.Sprintf("%d", timeoutSec))
	if offset > 0 {
		values.Set("offset", fmt.Sprintf("%d", offset))
	}
	values.Set("allowed_updates", `["message"]`)
	endpoint := fmt.Sprintf("%s/bot%s/getUpdates?%s", apiBase, token, values.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}
	var parsed tgResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	if !parsed.OK {
		return nil, fmt.Errorf("not ok: %s", string(body))
	}
	return parsed.Result, nil
}

func sendMessage(ctx context.Context, c *http.Client, token string, chatID int64, text string) {
	sendMessageWithParseMode(ctx, c, token, chatID, text, "")
}

func sendHTMLMessage(ctx context.Context, c *http.Client, token string, chatID int64, text string) {
	sendMessageWithParseMode(ctx, c, token, chatID, text, "HTML")
}

func sendMessageWithParseMode(ctx context.Context, c *http.Client, token string, chatID int64, text string, parseMode string) {
	values := url.Values{}
	values.Set("chat_id", fmt.Sprintf("%d", chatID))
	values.Set("text", text)
	if parseMode != "" {
		values.Set("parse_mode", parseMode)
	}
	endpoint := fmt.Sprintf("%s/bot%s/sendMessage", apiBase, token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		log.Printf("sendMessage build: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.Do(req)
	if err != nil {
		log.Printf("sendMessage to %d: %v", chatID, err)
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		log.Printf("sendMessage to %d: status %d", chatID, resp.StatusCode)
	}
}

func handleMessage(ctx context.Context, cfg botConfig, c *http.Client, token string, u tgUpdate) {
	text := strings.TrimSpace(u.Message.Text)
	if text == "" {
		return
	}
	chatID := u.Message.Chat.ID
	from := u.Message.From

	if strings.HasPrefix(text, "/") {
		switch commandName(text) {
		case "/start", "/help":
			sendMessage(ctx, c, token, chatID, startMessage)
		case "/login":
			sendHTMLMessage(ctx, c, token, chatID, serverIssueLogin(ctx, c, cfg, from.ID, from.Username))
		default:
			sendMessage(ctx, c, token, chatID, startMessage)
		}
		return
	}

	// Plain text is treated as a register code. Triage locally so obvious
	// non-codes get the help text without a server round-trip.
	code := strings.ToUpper(text)
	if !looksLikeCode(code) {
		sendMessage(ctx, c, token, chatID, startMessage)
		return
	}
	sendMessage(ctx, c, token, chatID, serverConsumeRegister(ctx, c, cfg, code, from.ID, from.Username))
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

// serverConsumeRegister / serverIssueLogin call the dope server's Telegram
// bridge and return the user-facing reply text it produced.
func serverConsumeRegister(ctx context.Context, c *http.Client, cfg botConfig, code string, tgUserID int64, tgUsername string) string {
	return callBridge(ctx, c, cfg, "/api/telegram/register", map[string]any{
		"code":              code,
		"telegram_user_id":  tgUserID,
		"telegram_username": tgUsername,
	})
}

func serverIssueLogin(ctx context.Context, c *http.Client, cfg botConfig, tgUserID int64, tgUsername string) string {
	return callBridge(ctx, c, cfg, "/api/telegram/login", map[string]any{
		"telegram_user_id":  tgUserID,
		"telegram_username": tgUsername,
	})
}

func callBridge(ctx context.Context, c *http.Client, cfg botConfig, path string, payload any) string {
	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("bridge %s marshal: %v", path, err)
		return botErrorMessage
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.serverURL+path, bytes.NewReader(body))
	if err != nil {
		log.Printf("bridge %s build: %v", path, err)
		return botErrorMessage
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Bot-Secret", cfg.secret)
	resp, err := c.Do(req)
	if err != nil {
		log.Printf("bridge %s: %v", path, err)
		return botErrorMessage
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		log.Printf("bridge %s: status %d: %s", path, resp.StatusCode, strings.TrimSpace(string(data)))
		return botErrorMessage
	}
	var out struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(data, &out); err != nil || out.Message == "" {
		log.Printf("bridge %s: bad response: %v", path, err)
		return botErrorMessage
	}
	return out.Message
}
