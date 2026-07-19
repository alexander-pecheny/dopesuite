package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Trello import proxy.
//
// xy's CSP is connect-src 'self' (the real defense for client-side crypto), and
// Trello's attachment download endpoint sends no CORS headers, so the browser
// cannot call Trello directly. This endpoint proxies read-only GETs to the
// Trello REST API on the client's behalf: the browser holds the per-user token
// (from Trello's implicit OAuth flow) and passes it per request; the server adds
// the shared app key and the OAuth header the download endpoint needs.
//
// Board content transiting here is NOT stored server-side — the importer
// encrypts every field client-side before uploading it into the new xy board,
// so xy's at-rest encryption is unchanged. This is a transient passthrough for a
// one-time migration.
//
// The app key is reused from chgksuite (the user's other project). Trello's
// implicit token flow needs only the key, not the OAuth secret.
const trelloAppKey = "1d4fe71dd193855686196e7768aa4b05"

const trelloAPIBase = "https://api.trello.com/1"

// maxTrelloProxyBytes bounds a proxied response so an attachment download can't
// exhaust memory. It sits just above the attachment cap (50 MiB) since the plain
// bytes fetched here become that ciphertext.
const maxTrelloProxyBytes = 55 << 20

var trelloProxyClient = &http.Client{Timeout: 90 * time.Second}

type trelloProxyRequest struct {
	Token  string            `json:"token"`
	Path   string            `json:"path"`
	Params map[string]string `json:"params"`
}

// handleTrelloProxy forwards a GET to https://api.trello.com/1{path}. It is a
// constrained proxy (fixed host, GET only, the caller's own read-scoped token),
// not an open one: the path must be relative and free of an absolute URL.
func (s *server) handleTrelloProxy(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	var req trelloProxyRequest
	if !readJSON(w, r, &req) {
		return
	}
	if req.Token == "" {
		httpError(w, http.StatusBadRequest, "token required")
		return
	}
	if !strings.HasPrefix(req.Path, "/") || strings.Contains(req.Path, "://") || strings.Contains(req.Path, "..") {
		httpError(w, http.StatusBadRequest, "bad path")
		return
	}

	q := url.Values{}
	for k, v := range req.Params {
		q.Set(k, v)
	}
	q.Set("key", trelloAppKey)
	q.Set("token", req.Token)
	target := trelloAPIBase + req.Path + "?" + q.Encode()

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	outReq, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		httpError(w, http.StatusBadRequest, "bad request")
		return
	}
	// The attachment download endpoint authenticates only via this header.
	outReq.Header.Set("Authorization",
		fmt.Sprintf(`OAuth oauth_consumer_key="%s", oauth_token="%s"`, trelloAppKey, req.Token))

	resp, err := trelloProxyClient.Do(outReq)
	if err != nil {
		httpError(w, http.StatusBadGateway, "trello request failed")
		return
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		w.Header().Set("Retry-After", ra) // let the client back off on 429
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, maxTrelloProxyBytes))
}
