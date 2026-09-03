package i18nstrings

import "errors"

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
