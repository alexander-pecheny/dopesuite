package tgbot

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"time"
)

// A bot is a long-polling client: nothing can connect to it, so nothing can ask
// whether it works. ServeHealth gives it one loopback endpoint that answers
// that question, for a server sharing the host that wants to know whether
// telegram login is worth offering.
//
// It is opt-in — a library that opens a socket the caller never asked for is a
// surprise, and the two bots that link this package run on different hosts with
// different port maps.
//
// The answer is deliberately about POLLING, not about the process: a bot whose
// token was revoked, or whose network is blocked, is up and useless. `ok` is
// false until the first getUpdates returns, so a bot that has never reached
// Telegram never reads as healthy.
type Health struct {
	OK       bool   `json:"ok"`
	LastPoll string `json:"last_poll,omitempty"` // RFC3339, absent before the first
	StaleFor string `json:"stale_for,omitempty"` // how long since it, when not ok
}

// HealthOf reports the client's polling health. A poll is stale once more than
// twice the long-poll timeout has passed: one timeout is the normal quiet case,
// two means the loop is not coming back.
//
// Starting up counts as healthy. A long poll on a quiet bot takes the full
// timeout to return, so the first one lands up to a minute after boot, and
// calling that minute "unreachable" would report every bot restart — every
// deploy — as an outage on the login page. A bot that is actually broken does
// not sit silent: getUpdates fails within seconds (401 on a revoked token) and
// lastErr ends the grace immediately.
func HealthOf(c *Client, now time.Time) Health {
	started, last, failed := c.pollState()
	if last.IsZero() {
		warming := !started.IsZero() && failed.IsZero() && now.Sub(started) <= 2*c.PollTimeout()
		return Health{OK: warming}
	}
	age := now.Sub(last)
	h := Health{OK: age <= 2*c.PollTimeout(), LastPoll: last.UTC().Format(time.RFC3339)}
	if !h.OK {
		h.StaleFor = age.Round(time.Second).String()
	}
	return h
}

// ServeHealth runs the endpoint on addr until ctx is cancelled. addr should be
// loopback: this says whether the bot is working, which is nobody's business
// from outside the host.
func ServeHealth(ctx context.Context, addr string, c *Client) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		h := HealthOf(c, time.Now())
		w.Header().Set("Content-Type", "application/json")
		if !h.OK {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(h)
	})
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		// A bot that cannot serve its health endpoint is still a working bot:
		// say so and carry on, rather than taking telegram login down with it.
		log.Printf("health endpoint on %s: %v", addr, err)
		return
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		log.Printf("health endpoint: %v", err)
	}
}
