package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"xy/internal/chgk/fsource"
	"xy/internal/chgk/tg"

	corei18n "pecheny.me/dopecore/i18nstrings"
	xystrings "xy/i18nstrings"
)

// The telegram export: the one export that publishes instead of downloading.
// It is unlike every other because it is a conversation — a channel named by
// @username cannot be looked up by a bot, so the person driving it has to show
// the bot the channel and the group from the inside, and the bot has to be
// polling while they do. That takes minutes, so the response is a stream of
// NDJSON lines rather than one answer: notes for the reader, and a last line
// carrying the resolved ids so the browser can remember them and skip the
// conversation next time.
//
// The bot token passes through this server, which is otherwise the one thing it
// never sees. Posting to Telegram is Go code and the page's CSP forbids reaching
// api.telegram.org itself, so there is no version of this that keeps the token
// (or the questions) on the device. Nothing is stored: the token lives for the
// length of the request.

// tgExportTimeout bounds the whole conversation. Each step of the resolution
// waits five minutes for a human, and a package of forty questions is posted one
// message at a time under Telegram's own rate limit.
const tgExportTimeout = 30 * time.Minute

// tgKeepalive is how often a line goes out while nothing is happening, so the
// proxies between here and the browser see a live connection rather than a
// stalled one.
const tgKeepalive = 20 * time.Second

// tgLine is one line of the stream: a note to show, the failure that ended it,
// or the last line with the ids the browser should remember.
type tgLine struct {
	Note    string `json:"note,omitempty"`
	Error   string `json:"error,omitempty"`
	Done    bool   `json:"done,omitempty"`
	Channel string `json:"channel,omitempty"`
	Chat    string `json:"chat,omitempty"`
}

// tgStream serializes the writes, since the keepalive ticker and the export
// itself both write lines.
type tgStream struct {
	mu  sync.Mutex
	enc *json.Encoder
	w   http.ResponseWriter
}

func (s *tgStream) send(line tgLine) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.enc.Encode(line); err != nil {
		return
	}
	if f, ok := s.w.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *server) handleExportTelegram(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	req, form, ok := s.readExportForm(w, r, "")
	if !ok {
		return
	}
	token := strings.TrimSpace(form.Value("token"))
	channel := strings.TrimSpace(form.Value("channel"))
	chat := strings.TrimSpace(form.Value("chat"))
	if token == "" || channel == "" || chat == "" {
		httpError(w, http.StatusBadRequest, xystrings.Default.Tg.Export.MissingFields())
		return
	}

	// Everything past here is reported inside the stream: once the header is
	// out, a status code is no longer available to say what went wrong.
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	stream := &tgStream{enc: json.NewEncoder(w), w: w}
	stream.send(tgLine{Note: xystrings.Default.Tg.Export.Connecting()})

	ctx, cancel := context.WithTimeout(r.Context(), tgExportTimeout)
	defer cancel()
	go func() {
		t := time.NewTicker(tgKeepalive)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				stream.send(tgLine{})
			}
		}
	}()

	target, err := s.postToTelegram(ctx, stream, req, token, channel, chat)
	if err != nil {
		msg, _ := corei18n.Reveal(err, xystrings.Default.Tg.Export.Failed())
		stream.send(tgLine{Error: msg})
		return
	}
	stream.send(tgLine{Done: true, Channel: target.ChannelID, Chat: target.ChatID})
}

// postToTelegram runs the whole conversation: poll, resolve the two targets
// (which is also where the bot's admin rights are checked), post the package.
func (s *server) postToTelegram(ctx context.Context, stream *tgStream, req exportRequest, token, channel, chat string) (tg.Target, error) {
	bot := tg.NewBotAt(token, s.tgAPIBase)
	stop, err := bot.Start(ctx)
	if err != nil {
		return tg.Target{}, err
	}
	defer stop()

	say := func(format string, args ...any) { stream.send(tgLine{Note: fmt.Sprintf(format, args...)}) }
	target, err := tg.ResolveTarget(ctx, bot, channel, chat, say)
	if err != nil {
		return target, err
	}
	poster, err := tg.NewPoster(bot, target)
	if err != nil {
		return target, err
	}
	stream.send(tgLine{Note: xystrings.Default.Tg.Export.Posting(target.ChannelID, target.ChatID)})
	return target, tg.Export(ctx, poster, tg.Request{
		Doc:    fsource.Parse(req.source, "chgk"),
		Images: req.images,
		Target: target,
	})
}
