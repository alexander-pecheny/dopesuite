package spliffserver

import (
	"errors"
	"os"
	"strings"
)

// publicURL is where this instance answers, and the only place a self-reference
// comes from — every link a telegram DM carries is built from it. There is
// deliberately no default: guessing one sends somebody else's Members to
// whoever's URL was compiled in.
func publicURL() string { return trimPublicURL(os.Getenv("SPLIFF_PUBLIC_URL")) }

func trimPublicURL(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

// checkPublicURL fails a production start with no SPLIFF_PUBLIC_URL, where the
// cost of noticing later is Members following dead or foreign links.
func checkPublicURL(prod bool, raw string) error {
	if prod && trimPublicURL(raw) == "" {
		return errors.New("SPLIFF_PUBLIC_URL is required (set it to this instance's base URL, e.g. https://spliff.example.org, or set SPLIFF_ENV=development)")
	}
	return nil
}
