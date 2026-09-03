package i18nstrings

import (
	"errors"
	"log"
)

// UserError is a failure whose message was written for the person who caused
// it. The HTTP edge shows a UserError verbatim and maps every other error to
// one generic line, logging the detail.
type UserError struct{ Msg string }

func (e UserError) Error() string { return e.Msg }

// User wraps a catalog string as an error a person may read.
func User(msg string) error { return UserError{Msg: msg} }

// AsUser returns the message when err is (or wraps) a UserError.
func AsUser(err error) (string, bool) {
	var u UserError
	if errors.As(err, &u) {
		return u.Msg, true
	}
	return "", false
}

// Reveal is the one rule for what a failure may say to the person who hit it:
// a UserError's message verbatim (it was written for them), anything else one
// generic line over a log entry. Both apps' HTTP edges answer through it, and
// forUser is what tells a 400 from a 500 (root docs/adr/0006).
func Reveal(err error, generic string) (msg string, forUser bool) {
	if m, ok := AsUser(err); ok {
		return m, true
	}
	log.Printf("internal error: %v", err)
	return generic, false
}
