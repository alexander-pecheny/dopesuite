package dopeserver

import (
	"dope/dope/web/route"
	"net/http"
	"strings"
	"time"
)

func writeJSONValue(w http.ResponseWriter, value any) {
	if err := route.JSON(w, value); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
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
