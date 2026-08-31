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
	"mime/multipart"
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
	// ForwardFromChat and ForwardFromMessageID say where a forwarded message
	// came from. A channel post copied into the channel's discussion group
	// carries them, which is how a poster finds the message to reply to.
	ForwardFromChat      *Chat `json:"forward_from_chat"`
	ForwardFromMessageID int64 `json:"forward_from_message_id"`
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
	// Type is "private", "group", "supergroup" or "channel".
	Type string `json:"type"`
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

// Token is the bot token this client polls with, for the caller that has to
// claim the host's poll lock for it.
func (c *Client) Token() string { return c.token }

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
// is what tells them apart. See HealthOf.
func (c *Client) LastPoll() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastPoll
}

// PollTimeout is the long-poll timeout, so a health check can decide how stale
// LastPoll has to be before it counts as stale.
func (c *Client) PollTimeout() time.Duration { return c.pollTimeout }

// Run long-polls until ctx is cancelled, dispatching to h each message update
// that carries a sender and a chat — what a conversation with a person is. It
// returns ctx.Err() on shutdown.
func (c *Client) Run(ctx context.Context, h Handler) error {
	return c.RunAll(ctx, func(ctx context.Context, c *Client, u Update) {
		if u.Message.From == nil || u.Message.Chat == nil {
			return
		}
		h(ctx, c, u)
	})
}

// RunAll is Run without that filter: every update carrying a message, sender or
// not. A channel post copied into a discussion group is nobody's message, and
// it is the one the telegram export waits for.
func (c *Client) RunAll(ctx context.Context, h Handler) error {
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
			if u.Message == nil {
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

// APIError is Telegram answering a method call with ok:false.
type APIError struct {
	Method      string
	Description string
	// RateLimited is Telegram asking for a pause; RetryAfter is how long it
	// wants, which may be zero.
	RateLimited bool
	RetryAfter  int
}

func (e *APIError) Error() string { return e.Method + ": " + e.Description }

// apiResponse is the envelope every Bot API method answers with.
type apiResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	Description string          `json:"description"`
	Parameters  *struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

// Call invokes a Bot API method with a JSON body and returns its result. A
// rate-limited call waits the retry_after Telegram asks for and goes again;
// everything else comes back as an APIError.
func (c *Client) Call(ctx context.Context, method string, payload any) (json.RawMessage, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.method(method), bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		res, err := c.do(ctx, method, req)
		wait, limited := retryAfter(err)
		if !limited {
			return res, err
		}
		if err := sleep(ctx, wait); err != nil {
			return nil, err
		}
	}
}

// FilePart is one file uploaded with a multipart method call.
type FilePart struct {
	Field, Filename string
	Data            []byte
}

// CallMultipart invokes a Bot API method that carries files: the fields are
// sent as form values (a structured one must already be JSON), the files as
// attachments the payload can refer to as attach://<field>.
func (c *Client) CallMultipart(ctx context.Context, method string, fields map[string]string, files []FilePart) (json.RawMessage, error) {
	for {
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		for k, v := range fields {
			if err := w.WriteField(k, v); err != nil {
				return nil, err
			}
		}
		for _, f := range files {
			part, err := w.CreateFormFile(f.Field, f.Filename)
			if err != nil {
				return nil, err
			}
			if _, err := part.Write(f.Data); err != nil {
				return nil, err
			}
		}
		if err := w.Close(); err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.method(method), &buf)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", w.FormDataContentType())
		res, err := c.do(ctx, method, req)
		wait, limited := retryAfter(err)
		if !limited {
			return res, err
		}
		if err := sleep(ctx, wait); err != nil {
			return nil, err
		}
	}
}

func (c *Client) do(ctx context.Context, method string, req *http.Request) (json.RawMessage, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var parsed apiResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("%s: status %d: %s", method, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if !parsed.OK {
		apiErr := &APIError{Method: method, Description: parsed.Description}
		if parsed.Parameters != nil {
			apiErr.RateLimited, apiErr.RetryAfter = true, parsed.Parameters.RetryAfter
		}
		return nil, apiErr
	}
	return parsed.Result, nil
}

// retryAfter says whether Telegram rate-limited this call, and how long to wait
// before going again — its own number plus a second, as chgksuite waits.
func retryAfter(err error) (time.Duration, bool) {
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.RateLimited {
		return time.Duration(apiErr.RetryAfter+1) * time.Second, true
	}
	return 0, false
}

func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}
