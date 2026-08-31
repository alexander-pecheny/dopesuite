package tgbot

import "time"

// A bot is a long-polling client, so "is it working" cannot be asked of the
// process — only of the polling. Health is that answer, for a login page
// deciding whether telegram is worth offering.
//
// It is deliberately about POLLING, not about the process: a bot whose
// token was revoked, or whose network is blocked, is up and useless. `ok` is
// false until the first getUpdates returns, so a bot that has never reached
// Telegram never reads as healthy.
type Health struct {
	OK       bool   `json:"ok"`
	LastPoll string `json:"last_poll,omitempty"` // RFC3339, absent before the first
	StaleFor string `json:"stale_for,omitempty"` // how long since it, when not ok
	Conflict bool   `json:"conflict,omitempty"`  // another process is polling this token
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
	started, last, failed, conflict := c.pollState()
	// A conflict outranks everything below: the last poll may be seconds old and
	// still mean nothing, because half the updates went to the other poller.
	if !conflict.IsZero() && conflict.After(last) {
		return Health{Conflict: true, StaleFor: now.Sub(conflict).Round(time.Second).String()}
	}
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
