package server

import (
	"errors"
	"strings"
)

// Server-side fractional-indexing, ported from web/assets/static/rank.js (which
// is itself a port of rocicorp/fractional-indexing, MIT). The web client owns
// ranking for interactive reorders; the only server need is appending a card at
// the bottom of a list during a Trello-compatible upload, so we implement just
// keyAfter (== keyBetween(a, null)) and its dependencies. Keys are base-62
// strings that sort lexicographically.

const rankDigits = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

func rankIndexOf(c byte) int { return strings.IndexByte(rankDigits, c) }

func rankIntegerLength(head byte) (int, error) {
	switch {
	case head >= 'a' && head <= 'z':
		return int(head-'a') + 2, nil
	case head >= 'A' && head <= 'Z':
		return int('Z'-head) + 2, nil
	}
	return 0, errors.New("invalid order key head: " + string(head))
}

func rankIntegerPart(key string) (string, error) {
	if key == "" {
		return "", errors.New("empty order key")
	}
	n, err := rankIntegerLength(key[0])
	if err != nil {
		return "", err
	}
	if n > len(key) {
		return "", errors.New("invalid order key: " + key)
	}
	return key[:n], nil
}

// midpointOpen is midpoint(a, null): the shortest string strictly greater than
// the fractional part a, with the right end open.
func midpointOpen(a string) string {
	digitA := 0
	if a != "" {
		digitA = rankIndexOf(a[0])
	}
	digitB := len(rankDigits)
	if digitB-digitA > 1 {
		mid := (digitA + digitB + 1) / 2 // == Math.round(0.5*(digitA+digitB))
		return string(rankDigits[mid])
	}
	rest := ""
	if len(a) > 0 {
		rest = a[1:]
	}
	return string(rankDigits[digitA]) + midpointOpen(rest)
}

// incrementInteger returns the next integer part, or ok=false when there is no
// larger one (head 'z' fully carried — caller then extends the fractional part).
func incrementInteger(x string) (string, bool) {
	head := x[0]
	digs := []byte(x[1:])
	carry := true
	for i := len(digs) - 1; carry && i >= 0; i-- {
		d := rankIndexOf(digs[i]) + 1
		if d == len(rankDigits) {
			digs[i] = '0'
		} else {
			digs[i] = rankDigits[d]
			carry = false
		}
	}
	if carry {
		switch head {
		case 'Z':
			return "a0", true
		case 'z':
			return "", false
		}
		h := head + 1
		if h > 'a' {
			digs = append(digs, '0')
		} else if len(digs) > 0 {
			digs = digs[1:]
		}
		return string(h) + string(digs), true
	}
	return string(head) + string(digs), true
}

// rankAfter returns a key strictly greater than prev (keyBetween(prev, null)).
// An empty prev means "no existing key" and yields the first key, "a0".
func rankAfter(prev string) (string, error) {
	if prev == "" {
		return "a" + string(rankDigits[0]), nil
	}
	ia, err := rankIntegerPart(prev)
	if err != nil {
		return "", err
	}
	fa := prev[len(ia):]
	if i, ok := incrementInteger(ia); ok {
		return i, nil
	}
	return ia + midpointOpen(fa), nil
}
