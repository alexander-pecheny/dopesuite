package dopeserver

import (
	"dope/dope/web/route"
	dopestrings "dope/i18nstrings"
	"log"
	"net/http"
	"strings"
	"time"
)

func writeJSONValue(w http.ResponseWriter, value any) {
	if err := route.JSON(w, value); err != nil {
		log.Printf("internal error: %v", err)
		http.Error(w, dopestrings.Default.Server.Error.Internal(), http.StatusInternalServerError)
	}
}

func splitPlayerName(fullName string) (string, string) {
	fullName = strings.TrimSpace(fullName)
	if fullName == "" {
		return "", ""
	}
	parts := strings.Fields(fullName)
	if len(parts) == 1 {
		return parts[0], ""
	}
	return parts[0], strings.Join(parts[1:], " ")
}

// inviteLifetime bounds a minted invite code.
const inviteLifetime = 7 * 24 * time.Hour
