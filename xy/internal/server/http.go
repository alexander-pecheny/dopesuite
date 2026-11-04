package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// maxJSONBody caps a JSON request body.
const maxJSONBody = 1 << 20

// writeJSON marshals v and writes it as application/json.
func writeJSON(w http.ResponseWriter, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		handleErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// API payloads are dynamic, per-session, and read-your-writes sensitive (e.g.
	// the board snapshot must never lag a just-saved card edit). Without an explicit
	// directive a browser may heuristically cache these GETs and replay a stale
	// snapshot, so forbid all caching.
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

// etagHashBytes is how much of the body's SHA-256 goes into an ETag. Twelve
// bytes is far more than enough to tell two versions of one URL apart.
const etagHashBytes = 12

// writeJSONRevalidated writes v like writeJSON, but lets the browser keep the
// body and ask again with If-None-Match. The answer is still computed on every
// request, so it never lags a write. Only the transfer is saved: when nothing
// changed the server replies 304 with no body, which matters on a slow
// connection, where a 100 KB board snapshot can take seconds. Use it for big
// GETs that are re-read often and change rarely between reads.
func writeJSONRevalidated(w http.ResponseWriter, r *http.Request, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		handleErr(w, err)
		return
	}
	sum := sha256.Sum256(data)
	tag := hex.EncodeToString(sum[:etagHashBytes])
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	// private: no shared cache may keep it. no-cache: the browser must ask the
	// server every time before it uses what it kept.
	h.Set("Cache-Control", "private, no-cache")
	h.Set("ETag", `W/"`+tag+`"`)
	if etagMatches(r.Header.Get("If-None-Match"), tag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = w.Write(data)
}

// etagMatches reports whether an If-None-Match header names tag. The match is
// weak, as RFC 9110 asks of If-None-Match. A suffix after the hash is
// ignored, because a reverse proxy that compresses the body may add one
// (Caddy's encode appends "-gzip" or "-zstd").
func etagMatches(header, tag string) bool {
	for _, t := range strings.Split(header, ",") {
		t = strings.TrimPrefix(strings.TrimSpace(t), "W/")
		t = strings.Trim(t, `"`)
		if t == "*" || strings.HasPrefix(t, tag) {
			return true
		}
	}
	return false
}

// readJSON decodes the request body into v, rejecting unknown fields and
// accepting the field names from before v29 (renameLegacyFields). Returns
// false (after writing a 400) on failure.
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxJSONBody))
	if err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(renameLegacyFields(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return false
	}
	return true
}

// httpError writes a plain-text error with the given status.
func httpError(w http.ResponseWriter, status int, msg string) {
	http.Error(w, msg, status)
}
