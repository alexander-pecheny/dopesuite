package tgbot

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestCallReturnsResult: a method call hands back the result, not the envelope.
func TestCallReturnsResult(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":11}}`))
	}))
	defer srv.Close()

	c := New(Config{Token: "x", APIBase: srv.URL})
	res, err := c.Call(context.Background(), "sendMessage", map[string]any{"chat_id": "-100", "text": "привет"})
	if err != nil {
		t.Fatal(err)
	}
	var msg struct {
		MessageID int64 `json:"message_id"`
	}
	if err := json.Unmarshal(res, &msg); err != nil || msg.MessageID != 11 {
		t.Fatalf("result = %s (%v)", res, err)
	}
	if !strings.Contains(gotBody, `"text":"привет"`) {
		t.Errorf("body = %s", gotBody)
	}
}

// TestCallErrorIsTyped: ok:false is an APIError, so a caller can tell a refusal
// from a broken connection.
func TestCallErrorIsTyped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"ok":false,"description":"Bad Request: chat not found"}`))
	}))
	defer srv.Close()

	_, err := New(Config{Token: "x", APIBase: srv.URL}).Call(context.Background(), "sendPoll", map[string]any{})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || !strings.Contains(apiErr.Description, "chat not found") {
		t.Fatalf("want an APIError, got %v", err)
	}
}

// TestCallWaitsOutRateLimit: Telegram's retry_after is a wait, not a failure.
func TestCallWaitsOutRateLimit(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"ok":false,"description":"Too Many Requests","parameters":{"retry_after":0}}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":2}}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := New(Config{Token: "x", APIBase: srv.URL}).Call(ctx, "sendMessage", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Errorf("called %d times, want a retry", calls.Load())
	}
}

// TestCallMultipartSendsFiles: the fields go as form values and the files as
// attachments, which is how a payload refers to them as attach://<field>.
func TestCallMultipartSendsFiles(t *testing.T) {
	var fields map[string]string
	var fileBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		mr := multipart.NewReader(r.Body, params["boundary"])
		fields = map[string]string{}
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			data, _ := io.ReadAll(part)
			if part.FileName() != "" {
				fileBody = string(data)
				continue
			}
			fields[part.FormName()] = string(data)
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":3}}`))
	}))
	defer srv.Close()

	c := New(Config{Token: "x", APIBase: srv.URL})
	_, err := c.CallMultipart(context.Background(), "sendRichMessage",
		map[string]string{"chat_id": "-100", "rich_message": `{"html":"<p>x</p>"}`},
		[]FilePart{{Field: "fimg0", Filename: "img0.jpg", Data: []byte("JPEGBYTES")}})
	if err != nil {
		t.Fatal(err)
	}
	if fields["chat_id"] != "-100" || !strings.Contains(fields["rich_message"], "<p>x</p>") {
		t.Errorf("fields = %v", fields)
	}
	if fileBody != "JPEGBYTES" {
		t.Errorf("file body = %q", fileBody)
	}
}

// TestRunAllSeesSenderlessMessages: a channel post copied into a discussion
// group has no `from`, and it is exactly the update the telegram export waits
// for — Run's own filter would drop it.
func TestRunAllSeesSenderlessMessages(t *testing.T) {
	body := `{"ok":true,"result":[{"update_id":1,"message":{"message_id":5,"chat":{"id":-100,"type":"supergroup"},` +
		`"forward_from_chat":{"id":-200,"type":"channel"},"forward_from_message_id":9}}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/getUpdates") {
			_, _ = w.Write([]byte(body))
			body = `{"ok":true,"result":[]}`
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	seen := make(chan Update, 1)
	c := New(Config{Token: "x", APIBase: srv.URL, PollTimeout: time.Second})
	go func() {
		_ = c.RunAll(ctx, func(_ context.Context, _ *Client, u Update) {
			select {
			case seen <- u:
			default:
			}
		})
	}()
	select {
	case u := <-seen:
		if u.Message.ForwardFromChat == nil || u.Message.ForwardFromMessageID != 9 {
			t.Errorf("forward fields lost: %+v", u.Message)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("RunAll never dispatched the senderless message")
	}
}
