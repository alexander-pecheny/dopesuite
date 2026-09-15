package spliffserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	corei18n "pecheny.me/dopecore/i18nstrings"

	"spliff/spliff/web/route"

	spliffstrings "spliff/i18nstrings"
)

// writeJSON marshals v and writes it as application/json. API payloads are
// per-session and read-your-writes sensitive — the Group page must never show
// a balance from before the bill that was just entered — so nothing may cache
// them.
func writeJSON(w http.ResponseWriter, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
	return nil
}

// readJSON decodes the request body into v, rejecting unknown fields.
func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return corei18n.User(spliffstrings.Default.Server.Error.BadRequest())
	}
	return nil
}

// writeError is the one place a failure becomes a response: a route.Status as
// its own status and words, a UserError as a 400 carrying its own, anything
// else as one generic line over a log entry (root docs/adr/0006).
func writeError(w http.ResponseWriter, _ *http.Request, err error) {
	if err == nil {
		return
	}
	if st, ok := route.AsStatus(err); ok {
		http.Error(w, st.Msg, st.Code)
		return
	}
	msg, forUser := corei18n.Reveal(err, spliffstrings.Default.Server.Error.Internal())
	if forUser {
		http.Error(w, msg, http.StatusBadRequest)
		return
	}
	http.Error(w, msg, http.StatusInternalServerError)
}

// newReader is bytes.NewReader under a name the callers that hand bytes to a
// streaming API can read at a glance.
func newReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }

// logDropped records something that failed after the response was already
// decided — a blob that outlived its row, a DM nobody could be told about.
func logDropped(what string, err error) {
	if err != nil && !errors.Is(err, http.ErrBodyNotAllowed) {
		log.Printf("spliff: %s: %v", what, err)
	}
}
