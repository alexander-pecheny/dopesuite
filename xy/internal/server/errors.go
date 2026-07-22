package server

import (
	"errors"
	"log"
	"net/http"
)

// appError is a handler error carrying an HTTP status and a user-facing message.
type appError struct {
	status int
	msg    string
}

func (e *appError) Error() string { return e.msg }

func errBadRequest(msg string) error { return &appError{status: http.StatusBadRequest, msg: msg} }
func errForbidden(msg string) error  { return &appError{status: http.StatusForbidden, msg: msg} }
func errNotFound(msg string) error   { return &appError{status: http.StatusNotFound, msg: msg} }
func errTooLarge(msg string) error {
	return &appError{status: http.StatusRequestEntityTooLarge, msg: msg}
}

// handleErr writes an error response if err != nil and reports whether it did.
// appErrors map to their status + message; anything else is a logged 500.
func handleErr(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	var ae *appError
	if errors.As(err, &ae) {
		httpError(w, ae.status, ae.msg)
		return true
	}
	log.Printf("internal error: %v", err)
	httpError(w, http.StatusInternalServerError, "ошибка сервера")
	return true
}
