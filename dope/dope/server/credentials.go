package dopeserver

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net/http"
	"strings"
	"time"
)

func writeJSONValue(w http.ResponseWriter, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, data)
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

// Auth helpers. Codes must be unique enough not to collide between concurrent
// users; we get that from crypto/rand.

const (
	inviteCodeBytes      = 12
	telegramAuthBytes    = 12
	telegramLoginCodeLen = 8
	sessionTokenBytes    = 32
	inviteLifetime       = 7 * 24 * time.Hour
)

const telegramLoginCodeAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func randomBase32(bytesLen int) (string, error) {
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return strings.ToUpper(strings.TrimRight(base32.StdEncoding.EncodeToString(buf), "=")), nil
}

func newInviteCode() (string, error) {
	return randomBase32(inviteCodeBytes)
}

func newTelegramAuthCode() (string, error) {
	return randomBase32(telegramAuthBytes)
}

func newTelegramLoginCode() (string, error) {
	buf := make([]byte, telegramLoginCodeLen)
	max := big.NewInt(int64(len(telegramLoginCodeAlphabet)))
	for i := range buf {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		buf[i] = telegramLoginCodeAlphabet[n.Int64()]
	}
	return string(buf), nil
}

func newSessionToken() (string, error) {
	buf := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
