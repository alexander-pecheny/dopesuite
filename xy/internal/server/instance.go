package server

import (
	"errors"
	"os"
	"strings"
)

// publicURL is where this instance answers, and the only place any self-reference
// comes from — the bot's replies and the Trello-compat links a self-hosted
// instance hands to chgksuite. There is deliberately no default: guessing one
// sends someone else's users to whoever's URL was compiled in.
func publicURL() string { return trimPublicURL(os.Getenv("XY_PUBLIC_URL")) }

func trimPublicURL(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

// checkPublicURL fails a production start with no XY_PUBLIC_URL, where the cost
// of noticing later is users following dead or foreign links.
func checkPublicURL(prod bool, raw string) error {
	if prod && trimPublicURL(raw) == "" {
		return errors.New("XY_PUBLIC_URL is required (set it to this instance's base URL, e.g. https://xy.example.org, or set XY_ENV=development)")
	}
	return nil
}
