package qyserver

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	corei18n "code.pecheny.me/pecheny/dopesuite/dopecore/i18nstrings"

	"qy/qy/web/route"

	qystrings "qy/i18nstrings"
)

// maxJSONBody caps a JSON request body.
const maxJSONBody = 1 << 20

// maxFormBody caps a posted form; a nomination carries eight questions.
const maxFormBody = 1 << 20

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

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxJSONBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return corei18n.User(qystrings.Default.Server.Error.BadRequest())
	}
	return nil
}

func parseForm(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBody)
	if err := r.ParseForm(); err != nil {
		return corei18n.User(qystrings.Default.Server.Error.BadRequest())
	}
	return nil
}

// errorOf is the one place a failure becomes a status and words: a
// route.Status as its own, a UserError as a 400 carrying its own, anything
// else as one generic line over a log entry (root docs/adr/0006).
func errorOf(err error) (int, string) {
	if st, ok := route.AsStatus(err); ok {
		return st.Code, st.Msg
	}
	msg, forUser := corei18n.Reveal(err, qystrings.Default.Server.Error.Internal())
	if forUser {
		return http.StatusBadRequest, msg
	}
	log.Printf("qy: %v", err)
	return http.StatusInternalServerError, msg
}

func writeAPIError(w http.ResponseWriter, _ *http.Request, err error) {
	if err == nil {
		return
	}
	code, msg := errorOf(err)
	http.Error(w, msg, code)
}

// logDropped records something that failed after the response was decided.
func logDropped(what string, err error) {
	if err != nil && !errors.Is(err, http.ErrBodyNotAllowed) {
		log.Printf("qy: %s: %v", what, err)
	}
}

func userError(msg string) error { return corei18n.User(msg) }
