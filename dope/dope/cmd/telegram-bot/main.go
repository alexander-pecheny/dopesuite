package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
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

	_ "modernc.org/sqlite"
)

const (
	defaultDBFile = "tournament.db"
	apiBase       = "https://api.telegram.org"
	pollTimeout   = 30
	codeLifetime  = time.Minute
	codeBytes     = 12

	registerURL = "https://dope.pecheny.me/register"
	loginURL    = "https://dope.pecheny.me/login"
)

const startMessage = "Этот бот выдает одноразовые коды для входа на dope.pecheny.me.\n\n" +
	"• Зарегистрироваться по инвайту: " + registerURL + "\n" +
	"• Войти в существующий аккаунт: " + loginURL + "\n\n" +
	"После регистрации на сайте пришли мне код, который он покажет. Чтобы войти — отправь /login и я пришлю код для ввода на сайте."

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

	dbPath := os.Getenv("DOPE_DB")
	if dbPath == "" {
		dbPath = defaultDBFile
	}
	token := strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN"))

	db, err := openDB(dbPath)
	if err != nil {
		log.Fatalf("open db %s: %v", dbPath, err)
	}
	defer db.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if token == "" {
		log.Printf("TELEGRAM_BOT_TOKEN is not set; running in stub mode (no updates will be processed)")
		<-ctx.Done()
		return
	}

	log.Printf("telegram bot polling, db=%s", dbPath)
	if err := runBot(ctx, db, token); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("bot: %v", err)
	}
}

func openDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys = ON; PRAGMA journal_mode = WAL;`); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func runBot(ctx context.Context, db *sql.DB, token string) error {
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
			handleMessage(ctx, db, client, token, u)
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
	values := url.Values{}
	values.Set("chat_id", fmt.Sprintf("%d", chatID))
	values.Set("text", text)
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

func handleMessage(ctx context.Context, db *sql.DB, c *http.Client, token string, u tgUpdate) {
	text := strings.TrimSpace(u.Message.Text)
	if text == "" {
		return
	}
	chatID := u.Message.Chat.ID
	from := u.Message.From

	if strings.HasPrefix(text, "/") {
		cmd := commandName(text)
		switch cmd {
		case "/start", "/help":
			sendMessage(ctx, c, token, chatID, startMessage)
		case "/login":
			sendMessage(ctx, c, token, chatID, issueLoginCode(ctx, db, from.ID, from.Username))
		default:
			sendMessage(ctx, c, token, chatID, startMessage)
		}
		return
	}

	sendMessage(ctx, c, token, chatID, consumeRegisterCode(ctx, db, strings.ToUpper(text), from.ID, from.Username))
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

func consumeRegisterCode(ctx context.Context, db *sql.DB, code string, tgUserID int64, tgUsername string) string {
	if !looksLikeCode(code) {
		return startMessage
	}
	now := utcNow()
	res, err := db.ExecContext(ctx, `
update telegram_login_codes
set telegram_user_id = ?, telegram_username = ?, consumed_at = ?
where code = ?
  and kind = 'register'
  and consumed_at is null
  and expires_at > ?`, tgUserID, tgUsername, now, code, now)
	if err != nil {
		log.Printf("register consume %s: %v", code, err)
		return "Произошла ошибка. Попробуй еще раз через минуту."
	}
	n, _ := res.RowsAffected()
	if n == 1 {
		return "Готово! Вернись на сайт — там уже видна твоя регистрация."
	}
	return registerFailureReason(ctx, db, code)
}

func registerFailureReason(ctx context.Context, db *sql.DB, code string) string {
	var kind string
	var consumedAt sql.NullString
	err := db.QueryRowContext(ctx, `
select kind, consumed_at from telegram_login_codes where code = ?`, code).Scan(&kind, &consumedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "Такого кода нет. Проверь, что скопировал его без пробелов и не дольше минуты прошло."
	}
	if err != nil {
		log.Printf("lookup code: %v", err)
		return "Произошла ошибка. Попробуй еще раз через минуту."
	}
	if consumedAt.Valid {
		return "Этот код уже использован. Запроси новый на сайте."
	}
	if kind != "register" {
		return "Этот код не для регистрации. Открой " + registerURL + " и начни заново."
	}
	return "Срок действия кода истек. Запроси новый на " + registerURL + "."
}

func issueLoginCode(ctx context.Context, db *sql.DB, tgUserID int64, tgUsername string) string {
	var userID int64
	err := db.QueryRowContext(ctx, `select id from users where telegram_user_id = ? and is_system = 0`, tgUserID).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return "Сначала зарегистрируйся по инвайту: " + registerURL
	}
	if err != nil {
		log.Printf("lookup user: %v", err)
		return "Произошла ошибка. Попробуй еще раз через минуту."
	}

	now := time.Now().UTC()
	expires := now.Add(codeLifetime).Format(time.RFC3339)
	createdAt := now.Format(time.RFC3339)

	for attempt := 0; attempt < 3; attempt++ {
		code, err := newCode()
		if err != nil {
			log.Printf("generate code: %v", err)
			return "Произошла ошибка. Попробуй еще раз через минуту."
		}
		_, err = db.ExecContext(ctx, `
insert into telegram_login_codes(code, kind, user_id, telegram_user_id, telegram_username, created_at, expires_at)
values(?, 'login', ?, ?, ?, ?, ?)`, code, userID, tgUserID, tgUsername, createdAt, expires)
		if err == nil {
			return "Твой код для входа: " + code + "\nВведи его на " + loginURL + " в течение минуты."
		}
		if !strings.Contains(strings.ToLower(err.Error()), "unique") {
			log.Printf("issue login code: %v", err)
			return "Произошла ошибка. Попробуй еще раз через минуту."
		}
	}
	return "Не получилось выдать код, попробуй еще раз."
}

func newCode() (string, error) {
	buf := make([]byte, codeBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return strings.ToUpper(strings.TrimRight(base32.StdEncoding.EncodeToString(buf), "=")), nil
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

func utcNow() string {
	return time.Now().UTC().Format(time.RFC3339)
}
