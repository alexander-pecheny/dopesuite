package server

import (
	"errors"
	"net/http"

	corei18n "pecheny.me/dopecore/i18nstrings"

	xystrings "xy/i18nstrings"
)

// appError is a handler error carrying a status other than 400 and a message
// written for the person who caused it. A 400 needs no type of its own: that is
// what corei18n.User is, and handleErr answers both.
type appError struct {
	status int
	msg    string
}

func (e *appError) Error() string { return e.msg }

func errForbidden(msg string) error { return &appError{status: http.StatusForbidden, msg: msg} }
func errNotFound(msg string) error  { return &appError{status: http.StatusNotFound, msg: msg} }
func errTooLarge(msg string) error {
	return &appError{status: http.StatusRequestEntityTooLarge, msg: msg}
}

// handleErr writes an error response if err != nil and reports whether it did.
// appErrors map to their status + message, a UserError to a 400 carrying its
// own, anything else to a logged 500 (root docs/adr/0006).
func handleErr(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	var ae *appError
	if errors.As(err, &ae) {
		httpError(w, ae.status, ae.msg)
		return true
	}
	msg, forUser := corei18n.Reveal(err, xystrings.Default.Server.Internal())
	if forUser {
		httpError(w, http.StatusBadRequest, msg)
		return true
	}
	httpError(w, http.StatusInternalServerError, msg)
	return true
}

// handleUser writes a 400 for a domain error a person may read: a UserError
// verbatim, anything else as one generic line (its real text goes to the log).
// The edge shows only messages written for the person who caused them
// (root docs/adr/0006).
func handleUser(w http.ResponseWriter, err error) {
	msg, _ := corei18n.Reveal(err, xystrings.Default.Server.Error.BadRequest())
	httpError(w, http.StatusBadRequest, msg)
}
