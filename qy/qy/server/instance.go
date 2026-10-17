package qyserver

import (
	"errors"
	"os"
	"strings"
)

// publicURL is where this instance answers, and the only place a self-reference
// comes from — every link a telegram DM carries is built from it. There is
// deliberately no default: guessing one sends somebody else's Members to
// whoever's URL was compiled in.
func publicURL() string { return trimPublicURL(os.Getenv("QY_PUBLIC_URL")) }

func trimPublicURL(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

// checkPublicURL fails a production start with no QY_PUBLIC_URL, where the
// cost of noticing later is Members following dead or foreign links.
func checkPublicURL(prod bool, raw string) error {
	if prod && trimPublicURL(raw) == "" {
		return errors.New("QY_PUBLIC_URL is required (set it to this instance's base URL, e.g. https://qy.pecheny.me, or set QY_ENV=development)")
	}
	return nil
}
