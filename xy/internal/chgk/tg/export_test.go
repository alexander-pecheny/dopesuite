package tg

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"xy/internal/chgk/fsource"
)

// call is one line of the transcript: what chgksuite's exporter would have sent.
// scripts/gen_tg_oracle.py writes them by running the real exporter with every
// call to Telegram stubbed out.
type call struct {
	Call      string         `json:"call"`
	Chat      string         `json:"chat,omitempty"`
	ReplyTo   *int64         `json:"reply_to,omitempty"`
	HTML      string         `json:"html,omitempty"`
	Media     []string       `json:"media,omitempty"`
	Text      string         `json:"text,omitempty"`
	Photo     *bool          `json:"photo,omitempty"`
	MessageID int64          `json:"message_id,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
}

// recorder is a Poster that writes the same transcript instead of posting.
type recorder struct {
	calls []call
	msgID int64
}

func (r *recorder) next() int64 { r.msgID++; return r.msgID }

func (r *recorder) PostRich(_ context.Context, chatID, html string, media []Media, replyTo int64) (int64, error) {
	ids := []string{}
	for _, m := range media {
		ids = append(ids, m.ID)
	}
	r.calls = append(r.calls, call{Call: "post", Chat: chatID, ReplyTo: replyPtr(replyTo), HTML: html, Media: ids})
	return r.next(), nil
}

func (r *recorder) PostText(_ context.Context, chatID, text string, replyTo int64) (int64, error) {
	no := false
	r.calls = append(r.calls, call{Call: "post", Chat: chatID, ReplyTo: replyPtr(replyTo), Text: text, Photo: &no})
	return r.next(), nil
}

func (r *recorder) DiscussionMessage(_ context.Context, _ string, messageID int64) (int64, error) {
	r.calls = append(r.calls, call{Call: "discussion_of", MessageID: messageID})
	return r.next(), nil
}

func (r *recorder) Call(_ context.Context, method string, data map[string]any) error {
	r.calls = append(r.calls, call{Call: method, Data: data})
	return nil
}

func replyPtr(id int64) *int64 {
	if id == 0 {
		return nil
	}
	return &id
}

// TestTranscriptParity replays every fixture and switch through the export and
// compares what it would send, call for call, against chgksuite's own.
func TestTranscriptParity(t *testing.T) {
	raw, err := os.ReadFile("testdata/transcript.json")
	if err != nil {
		t.Fatal(err)
	}
	var runs []struct {
		Fixture string `json:"fixture"`
		Variant string `json:"variant"`
		Calls   []call `json:"calls"`
	}
	if err := json.Unmarshal(raw, &runs); err != nil {
		t.Fatal(err)
	}
	polls, err := DefaultPollConfig()
	if err != nil {
		t.Fatal(err)
	}

	for _, run := range runs {
		t.Run(run.Fixture+"/"+run.Variant, func(t *testing.T) {
			src, err := os.ReadFile(filepath.Join("testdata", run.Fixture+".4s"))
			if err != nil {
				t.Fatal(err)
			}
			images, err := loadImages(filepath.Join("testdata"))
			if err != nil {
				t.Fatal(err)
			}
			opts := Options{}
			var cfg *PollConfig
			switch run.Variant {
			case "nospoilers":
				opts.NoSpoilers = true
			case "polls":
				cfg = polls
			case "skip":
				opts.SkipUntil = 2
			case "asterisks":
				opts.DisableAsterisks = true
			case "english":
				opts.Language = "en"
			}
			rec := &recorder{msgID: 1000, calls: []call{}}
			target := Target{ChannelID: "-1001111111111", ChatID: "-1002222222222"}
			req := Request{
				Doc: fsource.Parse(string(src), "chgk"), Images: images,
				Options: opts, Target: target, Polls: cfg,
			}
			if err := Export(context.Background(), rec, req); err != nil {
				t.Fatalf("export: %v", err)
			}
			want, _ := json.MarshalIndent(run.Calls, "", " ")
			got, _ := json.MarshalIndent(rec.calls, "", " ")
			if string(want) != string(got) {
				t.Errorf("transcript mismatch\n--- chgksuite ---\n%s\n--- go ---\n%s", want, got)
			}
		})
	}
}

// pollConfigPath is chgksuite's own copy of the poll config the "polls" runs of
// the transcript were recorded with; assets/poll_config.toml must still match it.
func pollConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "chgksuite", "chgksuite", "chgksuite", "resources", "poll_config.toml")
}

// loadImages reads the fixture pictures, keyed the way (img …) names them.
func loadImages(dir string) (map[string][]byte, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	images := map[string][]byte{}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".png") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		images[e.Name()] = data
	}
	return images, nil
}
