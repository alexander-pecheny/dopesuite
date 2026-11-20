package tgbot

import (
	"net/http"
	"time"
)

const (
	redactedToken = "<token>"

	// A long poll keeps one HTTP/2 stream open for most of a minute with
	// nothing on the wire, so a connection that died under it (a peer that
	// stopped answering, a dropped flow) looks exactly like a quiet one. The
	// client used to keep sending every later poll down that dead socket, and
	// the bot stayed "unreachable" until the process was restarted. Pinging a
	// silent connection, and giving up on one whose writes stall, closes it
	// instead.
	h2SendPingTimeout  = 15 * time.Second
	h2PingTimeout      = 10 * time.Second
	h2WriteByteTimeout = 10 * time.Second
)

func newTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.HTTP2 = &http.HTTP2Config{
		SendPingTimeout:  h2SendPingTimeout,
		PingTimeout:      h2PingTimeout,
		WriteByteTimeout: h2WriteByteTimeout,
	}
	return t
}
