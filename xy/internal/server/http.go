package server

import (
	"encoding/json"
	"net/http"
)

// writeJSON marshals v and writes it as application/json.
func writeJSON(w http.ResponseWriter, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
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

// readJSON decodes the request body into v, rejecting unknown fields. Returns
// false (after writing a 400) on failure.
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
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
