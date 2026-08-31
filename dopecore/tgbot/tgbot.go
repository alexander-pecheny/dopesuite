// Package tgbot is the machinery shared by the dopesuite login bots: a Telegram
// long-poll client, the update dispatch loop, and the shared-secret HTTP bridge
// to the app server. A bot holds no database handle — every write goes through
// the server bridge, so the server stays the sole writer of its DB.
package tgbot

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
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultAPIBase = "https://api.telegram.org"

// ErrConflict is Telegram answering getUpdates with 409: another process is
// polling this same token. It is not a transient network hiccup — retrying it
// at the ordinary cadence just splits every incoming update between the two
// pollers at random, which is the failure that looks like nothing at all. Run
// backs off hard on it and HealthOf reports the bot unusable, so the login page
// stops offering a way in that only works half the time.
var ErrConflict = errors.New("another process is polling this token")

type Update struct {
	UpdateID int64    `json:"update_id"`
	Message  *Message `json:"message"`
}

type Message struct {
	MessageID int64  `json:"message_id"`
	From      *User  `json:"from"`
	Chat      *Chat  `json:"chat"`
	Text      string `json:"text"`
}

type User struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

// DisplayName is the user's public first/last name, "" when Telegram sent neither.
func (u *User) DisplayName() string {
	return strings.TrimSpace(u.FirstName + " " + u.LastName)
}

type Chat struct {
	ID int64 `json:"id"`
}

type Config struct {
	Token string
	// APIBase defaults to https://api.telegram.org.
	APIBase string
	// PollTimeout is the getUpdates long-poll timeout. Default 30s.
	PollTimeout time.Duration
	// HTTPTimeout defaults to PollTimeout + 10s.
	HTTPTimeout time.Duration
	// AllowedUpdates, when set, is passed to getUpdates.
	AllowedUpdates []string
	// RetryDelay is the backoff after a failed getUpdates. Default 3s.
	RetryDelay time.Duration
	// ConflictDelay is the backoff after a 409. Default 60s.
	ConflictDelay time.Duration
}

type Client struct {
	token          string
	apiBase        string
	pollTimeout    time.Duration
	allowedUpdates []string
	retryDelay     time.Duration
	conflictDelay  time.Duration
	http           *http.Client

	mu       sync.Mutex
	started  time.Time // when Run began polling
	lastPoll time.Time // last getUpdates that actually answered
	lastErr  time.Time // last getUpdates that failed
	conflict time.Time // last getUpdates that lost the token to another poller
}

// Handler processes one update whose Message, From and Chat are all non-nil.
type Handler func(ctx context.Context, c *Client, u Update)

func New(cfg Config) *Client {
	if cfg.APIBase == "" {
		cfg.APIBase = defaultAPIBase
	}
	if cfg.PollTimeout <= 0 {
		cfg.PollTimeout = 30 * time.Second
	}
	if cfg.HTTPTimeout <= 0 {
		cfg.HTTPTimeout = cfg.PollTimeout + 10*time.Second
	}
	if cfg.RetryDelay <= 0 {
		cfg.RetryDelay = 3 * time.Second
	}
	if cfg.ConflictDelay <= 0 {
		cfg.ConflictDelay = 60 * time.Second
	}
	return &Client{
		token:          cfg.Token,
		apiBase:        strings.TrimRight(cfg.APIBase, "/"),
		pollTimeout:    cfg.PollTimeout,
		allowedUpdates: cfg.AllowedUpdates,
		retryDelay:     cfg.RetryDelay,
		conflictDelay:  cfg.ConflictDelay,
		http:           &http.Client{Timeout: cfg.HTTPTimeout},
	}
}

// HTTP is the client's HTTP client, so a Bridge can share it.
func (c *Client) HTTP() *http.Client { return c.http }

func (c *Client) markPoll(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.lastErr = time.Now()
		if errors.Is(err, ErrConflict) {
			c.conflict = c.lastErr
		}
		return
	}
	c.lastPoll = time.Now()
	c.conflict = time.Time{}
}

func (c *Client) markStarted() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.started = time.Now()
}

// pollState is what a health check needs, read under one lock.
func (c *Client) pollState() (started, lastPoll, lastErr, conflict time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.started, c.lastPoll, c.lastErr, c.conflict
}

// LastPoll is when getUpdates last answered — zero before the first one returns.
// A bot whose process is up but whose polling is wedged (revoked token, blocked
// network) is useless to a login page in exactly the way a dead one is, and this
// is what tells them apart. See ServeHealth.
func (c *Client) LastPoll() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastPoll
}

// PollTimeout is the long-poll timeout, so a health check can decide how stale
// LastPoll has to be before it counts as stale.
func (c *Client) PollTimeout() time.Duration { return c.pollTimeout }

// Run long-polls until ctx is cancelled, dispatching each message update to h.
// It returns ctx.Err() on shutdown.
func (c *Client) Run(ctx context.Context, h Handler) error {
	c.markStarted()
	var offset int64
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		updates, err := c.GetUpdates(ctx, offset)
		c.markPoll(err)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			delay := c.retryDelay
			if errors.Is(err, ErrConflict) {
				delay = c.conflictDelay
				log.Printf("getUpdates: CONFLICT — another process is polling this bot token; "+
					"updates are being split between us at random. Find it and stop it. (%v)", err)
			} else {
				log.Printf("getUpdates: %v", err)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
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
			h(ctx, c, u)
		}
	}
}

func (c *Client) GetUpdates(ctx context.Context, offset int64) ([]Update, error) {
	values := url.Values{}
	values.Set("timeout", strconv.Itoa(int(c.pollTimeout/time.Second)))
	if offset > 0 {
		values.Set("offset", strconv.FormatInt(offset, 10))
	}
	if len(c.allowedUpdates) > 0 {
		allowed, err := json.Marshal(c.allowedUpdates)
		if err != nil {
			return nil, err
		}
		values.Set("allowed_updates", string(allowed))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.method("getUpdates")+"?"+values.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusConflict {
		return nil, fmt.Errorf("%w: %s", ErrConflict, strings.TrimSpace(string(body)))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}
	var parsed struct {
		OK     bool     `json:"ok"`
		Result []Update `json:"result"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	if !parsed.OK {
		return nil, fmt.Errorf("not ok: %s", string(body))
	}
	return parsed.Result, nil
}

// Send posts a plain-text message. Failures are logged, not returned: there is
// nothing a bot can do about a failed reply but carry on polling.
func (c *Client) Send(ctx context.Context, chatID int64, text string) {
	c.send(ctx, chatID, text, "")
}

// SendHTML posts a message with parse_mode=HTML.
func (c *Client) SendHTML(ctx context.Context, chatID int64, text string) {
	c.send(ctx, chatID, text, "HTML")
}

func (c *Client) send(ctx context.Context, chatID int64, text, parseMode string) {
	values := url.Values{}
	values.Set("chat_id", strconv.FormatInt(chatID, 10))
	values.Set("text", text)
	if parseMode != "" {
		values.Set("parse_mode", parseMode)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.method("sendMessage"), strings.NewReader(values.Encode()))
	if err != nil {
		log.Printf("sendMessage build: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
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

func (c *Client) method(name string) string {
	return c.apiBase + "/bot" + c.token + "/" + name
}

// Bridge is the bot's shared-secret connection to its app server.
type Bridge struct {
	serverURL string
	secret    string
	http      *http.Client
}

func NewBridge(serverURL, secret string, hc *http.Client) *Bridge {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Bridge{serverURL: strings.TrimRight(serverURL, "/"), secret: secret, http: hc}
}

func (b *Bridge) ServerURL() string { return b.serverURL }

// Call POSTs payload to a shared-secret endpoint and returns the server's
// user-facing "message" field. Anything other than a 200 with parseable JSON is
// an error; the caller decides what to say to the user.
func (b *Bridge) Call(ctx context.Context, path string, payload any) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("bridge %s marshal: %w", path, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.serverURL+path, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("bridge %s build: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Bot-Secret", b.secret)
	resp, err := b.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("bridge %s: %w", path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("bridge %s: status %d: %s", path, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var out struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("bridge %s: bad response: %w", path, err)
	}
	return out.Message, nil
}
