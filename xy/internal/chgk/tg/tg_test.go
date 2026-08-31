package tg

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"testing"
	"time"

	"pecheny.me/dopecore/tgbot"
)

// TestPollConfigReadsChgksuites reads the file chgksuite ships, so the reader
// keeps up with the shape it is written in.
func TestPollConfigReadsChgksuites(t *testing.T) {
	cfg, err := DefaultPollConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != "comment" {
		t.Errorf("mode = %q", cfg.Mode)
	}
	if cfg.Question == nil || cfg.Tour == nil || cfg.Packet == nil {
		t.Fatal("a poll table is missing")
	}
	if cfg.Question.Text != "Ваша оценка вопроса {NUMBER}" {
		t.Errorf("question text = %q", cfg.Question.Text)
	}
	if len(cfg.Question.Variants) != 6 || cfg.Question.Variants[0] != "🔥" {
		t.Errorf("variants = %v", cfg.Question.Variants)
	}
	if cfg.Question.IsAnonymous || !cfg.Question.AllowsRevotingSet || cfg.Question.AllowsRevoting {
		t.Errorf("flags: anonymous=%v revoting=%v/%v", cfg.Question.IsAnonymous,
			cfg.Question.AllowsRevoting, cfg.Question.AllowsRevotingSet)
	}
}

func TestPollConfigRejectsNonsense(t *testing.T) {
	for _, src := range []string{"[unknown]\n", "text = \"x\"\n", "[tour_poll]\nnope = 1\n"} {
		if _, err := ParsePollConfig(src); err == nil {
			t.Errorf("accepted %q", src)
		}
	}
}

func TestParseTargetRef(t *testing.T) {
	cases := []struct {
		ref  string
		id   int64
		name string
	}{
		{"-1001234567890", 1234567890, ""},
		{"1234567890", 1234567890, ""},
		{"@channel", 0, "channel"},
		{"https://t.me/c/1234567890/12", 1234567890, ""},
		{"https://t.me/channel", 0, "channel"},
	}
	for _, c := range cases {
		id, name, err := parseTargetRef(c.ref)
		if err != nil {
			t.Errorf("%s: %v", c.ref, err)
			continue
		}
		if id != c.id || name != c.name {
			t.Errorf("%s → (%d, %q), want (%d, %q)", c.ref, id, name, c.id, c.name)
		}
	}
	if _, _, err := parseTargetRef("  "); err == nil {
		t.Error("empty reference accepted")
	}
}

// TestBotWaitsBothWays: a waiter finds what arrived before it asked, and what
// arrives after. The discussion copy of a channel post can land either side of
// the wait, and chgksuite's five-minute lookback is why.
func TestBotWaitsBothWays(t *testing.T) {
	b := &Bot{}
	ctx := context.Background()
	early := tgbot.Update{Message: &tgbot.Message{
		MessageID: 7, Chat: &tgbot.Chat{ID: -100200, Type: "supergroup"},
		ForwardFromChat: &tgbot.Chat{ID: -100100, Type: "channel"}, ForwardFromMessageID: 42,
	}}
	b.offer(early)
	got, err := b.WaitForDiscussionCopy(ctx, -100100, -100200, 42, time.Second)
	if err != nil || got != 7 {
		t.Fatalf("history: got %d, %v", got, err)
	}

	go func() {
		time.Sleep(20 * time.Millisecond)
		b.offer(tgbot.Update{Message: &tgbot.Message{
			MessageID: 9, Chat: &tgbot.Chat{ID: 5, Type: "private"}, Text: "код abc123",
		}})
	}()
	chat, err := b.WaitForCode(ctx, "abc123", 2*time.Second)
	if err != nil || chat != 5 {
		t.Fatalf("live: got %d, %v", chat, err)
	}
}

func TestBotWaitTimesOut(t *testing.T) {
	b := &Bot{}
	if _, err := b.WaitForCode(context.Background(), "nope", 10*time.Millisecond); err == nil {
		t.Fatal("waited forever")
	}
	if len(b.waiters) != 0 {
		t.Errorf("a timed-out waiter was left behind: %d", len(b.waiters))
	}
}

// TestPrepareImageRules checks the two shapes Telegram refuses: a picture taller
// than the message shows, and a sliver.
func TestPrepareImageRules(t *testing.T) {
	tall := prepared(t, 400, 1200, 200)
	if tall.Dy() != 200 || tall.Dx() != 66 {
		t.Errorf("tall image → %dx%d, want 66x200", tall.Dx(), tall.Dy())
	}
	sliver := prepared(t, 1000, 20, 0)
	ratio := float64(sliver.Dx()) / float64(sliver.Dy())
	if ratio >= 20 {
		t.Errorf("sliver stayed %dx%d (ratio %.1f)", sliver.Dx(), sliver.Dy(), ratio)
	}
}

func prepared(t *testing.T, w, h, maxHeight int) image.Rectangle {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	out, err := prepareImage(buf.Bytes(), maxHeight)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	return image.Rect(0, 0, cfg.Width, cfg.Height)
}

// TestEmbeddedPollConfigMatchesChgksuites: the default is a copy of the file
// chgksuite ships, and the transcript oracle was recorded with that file.
func TestEmbeddedPollConfigMatchesChgksuites(t *testing.T) {
	raw, err := os.ReadFile(pollConfigPath())
	if err != nil {
		t.Skipf("no chgksuite checkout: %v", err)
	}
	if string(raw) != defaultPollConfig {
		t.Error("assets/poll_config.toml has drifted from chgksuite's")
	}
}
