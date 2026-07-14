// Package authcred mints and verifies the credentials both apps' auth flows are
// built on: session tokens, invite codes, telegram auth/login codes, and
// password hashes.
package authcred

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"math/big"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

const (
	SessionTokenBytes    = 32
	InviteCodeBytes      = 12
	TelegramAuthBytes    = 12
	TelegramLoginCodeLen = 8

	PasswordMinLen = 8
	PasswordMaxLen = 72 // bcrypt truncates beyond this

	// TelegramLoginCodeAlphabet is the human-typed alphabet: uppercase letters
	// and digits, so a code can be read aloud and retyped without ambiguity.
	TelegramLoginCodeAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
)

var base32enc = base32.StdEncoding.WithPadding(base32.NoPadding)

// RandomBase32 returns n random bytes as unpadded uppercase base32.
func RandomBase32(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base32enc.EncodeToString(buf), nil
}

func NewSessionToken() (string, error) {
	buf := make([]byte, SessionTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func NewInviteCode() (string, error)       { return RandomBase32(InviteCodeBytes) }
func NewTelegramAuthCode() (string, error) { return RandomBase32(TelegramAuthBytes) }

func NewTelegramLoginCode() (string, error) {
	buf := make([]byte, TelegramLoginCodeLen)
	max := big.NewInt(int64(len(TelegramLoginCodeAlphabet)))
	for i := range buf {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		buf[i] = TelegramLoginCodeAlphabet[n.Int64()]
	}
	return string(buf), nil
}

// HashSessionToken hashes a raw session token into the value stored in
// sessions.token_hash.
func HashSessionToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// HashPassword returns a bcrypt hash. The bcrypt format embeds its own salt, so
// callers pass none in.
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// VerifyPassword checks a candidate against a bcrypt hash.
func VerifyPassword(storedHash, password string) bool {
	if storedHash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(password)) == nil
}

// LegacySHA256Password is the pre-bcrypt scheme: sha256(salt + ":" + password).
func LegacySHA256Password(password, salt string) string {
	sum := sha256.Sum256([]byte(salt + ":" + password))
	return hex.EncodeToString(sum[:])
}

// VerifyPasswordUpgrading checks a candidate against a stored hash that may be
// either bcrypt or the legacy SHA256 scheme. On a legacy match it returns a
// fresh bcrypt hash in upgraded, which the caller should persist so the next
// login no longer relies on the weaker scheme. Apps with no legacy rows should
// call VerifyPassword instead.
func VerifyPasswordUpgrading(storedHash, storedSalt, password string) (ok bool, upgraded string, err error) {
	if storedHash == "" {
		return false, "", nil
	}
	if strings.HasPrefix(storedHash, "$2a$") || strings.HasPrefix(storedHash, "$2b$") || strings.HasPrefix(storedHash, "$2y$") {
		if cerr := bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(password)); cerr != nil {
			if errors.Is(cerr, bcrypt.ErrMismatchedHashAndPassword) {
				return false, "", nil
			}
			return false, "", cerr
		}
		return true, "", nil
	}
	expected := LegacySHA256Password(password, storedSalt)
	if subtle.ConstantTimeCompare([]byte(storedHash), []byte(expected)) != 1 {
		return false, "", nil
	}
	fresh, herr := HashPassword(password)
	if herr != nil {
		return true, "", nil
	}
	return true, fresh, nil
}
