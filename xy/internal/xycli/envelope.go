// Package xycli is the guts of xy-cli: the Envelope, the API client, the
// decrypted board model and the commands over them. cmd/xy-cli is a thin main.
package xycli

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	corei18n "pecheny.me/dopecore/i18nstrings"
	xystrings "xy/i18nstrings"

	"golang.org/x/crypto/scrypt"
	"golang.org/x/text/unicode/norm"
)

// The Envelope, as web/ts/crypto.ts owns it: magic "xy1" | alg | nonce(12) |
// ciphertext+tag, base64 over JSON. A second implementation of a wire format is
// a parity risk, so envelope_test.go opens what the browser sealed and the
// browser opens what this sealed (jstest/envelope_parity.test.js).
const (
	magic     = "xy1"
	algAESGCM = 1
	nonceLen  = 12
	headerLen = len(magic) + 1 + nonceLen

	verifyPlaintext = "xy-verify-v1"
)

// KDFParams are the scrypt parameters a board stores with its wrapped key.
type KDFParams struct {
	KDF   string `json:"kdf,omitempty"`
	N     int    `json:"N"`
	R     int    `json:"r"`
	P     int    `json:"p"`
	DKLen int    `json:"dkLen,omitempty"`
}

// Keymeta is everything needed to unwrap a board's data key given the passphrase.
type Keymeta struct {
	KDFSalt     string `json:"kdf_salt"`
	KDFParams   string `json:"kdf_params"` // JSON-encoded KDFParams
	WrappedKey  string `json:"wrapped_key"`
	VerifyToken string `json:"verify_token"`
}

// DataKey is a board's live data key.
type DataKey []byte

func b64enc(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func b64dec(s string) ([]byte, error) { return base64.StdEncoding.DecodeString(s) }

// seal wraps plaintext in an Envelope under key.
func seal(key, plaintext []byte) ([]byte, error) {
	gcm, err := gcmFor(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := make([]byte, 0, headerLen+len(plaintext)+gcm.Overhead())
	out = append(out, magic...)
	out = append(out, algAESGCM)
	out = append(out, nonce...)
	return gcm.Seal(out, nonce, plaintext, nil), nil
}

// open unwraps an Envelope, rejecting a foreign or damaged one.
func open(key, envelope []byte) ([]byte, error) {
	if len(envelope) < headerLen {
		return nil, errors.New("envelope too short")
	}
	if string(envelope[:len(magic)]) != magic {
		return nil, errors.New("bad envelope magic")
	}
	if envelope[len(magic)] != algAESGCM {
		return nil, errors.New("unknown envelope alg")
	}
	gcm, err := gcmFor(key)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, envelope[len(magic)+1:headerLen], envelope[headerLen:], nil)
}

func gcmFor(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// deriveKEK is the passphrase half: scrypt over the NFKC-normalised passphrase,
// with the board's own stored parameters (they may be raised for new boards
// without touching old ones).
func deriveKEK(passphrase string, salt []byte, p KDFParams) ([]byte, error) {
	dkLen := p.DKLen
	if dkLen == 0 {
		dkLen = 32
	}
	return scrypt.Key([]byte(norm.NFKC.String(passphrase)), salt, p.N, p.R, p.P, dkLen)
}

// Unlock derives the KEK, unwraps the data key and checks the verify token.
// A wrong passphrase fails here and nowhere else.
func Unlock(passphrase string, km Keymeta) (DataKey, error) {
	var p KDFParams
	if err := json.Unmarshal([]byte(km.KDFParams), &p); err != nil {
		return nil, fmt.Errorf("kdf_params: %w", err)
	}
	salt, err := b64dec(km.KDFSalt)
	if err != nil {
		return nil, fmt.Errorf("kdf_salt: %w", err)
	}
	kek, err := deriveKEK(passphrase, salt, p)
	if err != nil {
		return nil, err
	}
	wrapped, err := b64dec(km.WrappedKey)
	if err != nil {
		return nil, fmt.Errorf("wrapped_key: %w", err)
	}
	dk, err := open(kek, wrapped)
	if err != nil {
		return nil, ErrWrongPassphrase
	}
	verify, err := b64dec(km.VerifyToken)
	if err != nil {
		return nil, fmt.Errorf("verify_token: %w", err)
	}
	plain, err := open(dk, verify)
	if err != nil || string(plain) != verifyPlaintext {
		return nil, ErrWrongPassphrase
	}
	return dk, nil
}

// ErrWrongPassphrase is what a failed unwrap or a failed verify both say: the
// difference is never the user's business.
var ErrWrongPassphrase = corei18n.User(xystrings.Default.Cli.Unlock.WrongPassphrase())

// EncField seals a string into the base64 Envelope the API carries.
func (dk DataKey) EncField(s string) (string, error) {
	env, err := seal(dk, []byte(s))
	if err != nil {
		return "", err
	}
	return b64enc(env), nil
}

// DecField opens a base64 Envelope back into its string.
func (dk DataKey) DecField(b64 string) (string, error) {
	env, err := b64dec(b64)
	if err != nil {
		return "", err
	}
	plain, err := open(dk, env)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// EncBytes / DecBytes are the attachment path: raw Envelope bytes, no base64.
func (dk DataKey) EncBytes(b []byte) ([]byte, error) { return seal(dk, b) }

func (dk DataKey) DecBytes(b []byte) ([]byte, error) { return open(dk, b) }
