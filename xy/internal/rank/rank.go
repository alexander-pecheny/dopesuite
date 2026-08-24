// Package rank is fractional indexing: base-62 keys that sort
// lexicographically, so moving one card rewrites one row. A faithful port of
// the public-domain `fractional-indexing` algorithm (rocicorp/fractional-indexing,
// MIT), the same one web/ts/rank.ts runs in the browser — the two must agree,
// or a card lands in a different place depending on who moved it.
//
// An open end is the empty string here, where the TS uses null.
package rank

import (
	"errors"
	"strings"
)

const digits = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
const zero = '0'

func indexOf(c byte) int { return strings.IndexByte(digits, c) }

// midpoint is the shortest string strictly between the fractional parts a and b
// (b empty = open right end).
func midpoint(a, b string) (string, error) {
	if b != "" && a >= b {
		return "", errors.New("rank: " + a + " >= " + b)
	}
	if strings.HasSuffix(a, string(zero)) || strings.HasSuffix(b, string(zero)) {
		return "", errors.New("rank: trailing zero")
	}
	if b != "" {
		n := 0
		for n < len(b) {
			ca := byte(zero)
			if n < len(a) {
				ca = a[n]
			}
			if ca != b[n] {
				break
			}
			n++
		}
		if n > 0 {
			restA := ""
			if n < len(a) {
				restA = a[n:]
			}
			rest, err := midpoint(restA, b[n:])
			if err != nil {
				return "", err
			}
			return b[:n] + rest, nil
		}
	}
	digitA := 0
	if a != "" {
		digitA = indexOf(a[0])
	}
	digitB := len(digits)
	if b != "" {
		digitB = indexOf(b[0])
	}
	if digitB-digitA > 1 {
		return string(digits[(digitA+digitB+1)/2]), nil // == Math.round(0.5*(a+b))
	}
	if len(b) > 1 {
		return b[:1], nil
	}
	rest := ""
	if len(a) > 0 {
		rest = a[1:]
	}
	tail, err := midpoint(rest, "")
	if err != nil {
		return "", err
	}
	return string(digits[digitA]) + tail, nil
}

func integerLength(head byte) (int, error) {
	switch {
	case head >= 'a' && head <= 'z':
		return int(head-'a') + 2, nil
	case head >= 'A' && head <= 'Z':
		return int('Z'-head) + 2, nil
	}
	return 0, errors.New("rank: invalid order key head: " + string(head))
}

func integerPart(key string) (string, error) {
	if key == "" {
		return "", errors.New("rank: empty order key")
	}
	n, err := integerLength(key[0])
	if err != nil {
		return "", err
	}
	if n > len(key) {
		return "", errors.New("rank: invalid order key: " + key)
	}
	return key[:n], nil
}

func validate(key string) error {
	if key == "A"+strings.Repeat(string(zero), 26) {
		return errors.New("rank: invalid order key: " + key)
	}
	i, err := integerPart(key)
	if err != nil {
		return err
	}
	if strings.HasSuffix(key[len(i):], string(zero)) {
		return errors.New("rank: invalid order key: " + key)
	}
	return nil
}

// incrementInteger returns the next integer part; ok=false when there is none
// ('z' fully carried), which the caller answers by extending the fraction.
func incrementInteger(x string) (string, bool, error) {
	if err := validateInteger(x); err != nil {
		return "", false, err
	}
	head, digs := x[0], []byte(x[1:])
	carry := true
	for i := len(digs) - 1; carry && i >= 0; i-- {
		if d := indexOf(digs[i]) + 1; d == len(digits) {
			digs[i] = zero
		} else {
			digs[i] = digits[d]
			carry = false
		}
	}
	if carry {
		switch head {
		case 'Z':
			return "a" + string(zero), true, nil
		case 'z':
			return "", false, nil
		}
		h := head + 1
		if h > 'a' {
			digs = append(digs, zero)
		} else if len(digs) > 0 {
			digs = digs[1:]
		}
		return string(h) + string(digs), true, nil
	}
	return string(head) + string(digs), true, nil
}

// decrementInteger is incrementInteger's mirror; ok=false at 'A', the low end.
func decrementInteger(x string) (string, bool, error) {
	if err := validateInteger(x); err != nil {
		return "", false, err
	}
	head, digs := x[0], []byte(x[1:])
	last := digits[len(digits)-1]
	borrow := true
	for i := len(digs) - 1; borrow && i >= 0; i-- {
		if d := indexOf(digs[i]) - 1; d == -1 {
			digs[i] = last
		} else {
			digs[i] = digits[d]
			borrow = false
		}
	}
	if borrow {
		switch head {
		case 'a':
			return "Z" + string(last), true, nil
		case 'A':
			return "", false, nil
		}
		h := head - 1
		if h < 'Z' {
			digs = append(digs, last)
		} else if len(digs) > 0 {
			digs = digs[1:]
		}
		return string(h) + string(digs), true, nil
	}
	return string(head) + string(digs), true, nil
}

func validateInteger(x string) error {
	if x == "" {
		return errors.New("rank: empty integer part")
	}
	n, err := integerLength(x[0])
	if err != nil {
		return err
	}
	if len(x) != n {
		return errors.New("rank: invalid integer part: " + x)
	}
	return nil
}

// Between returns a key strictly between a and b. Either may be "" for an open
// end, so Between("", "") is the first key of an empty list.
func Between(a, b string) (string, error) {
	if a != "" {
		if err := validate(a); err != nil {
			return "", err
		}
	}
	if b != "" {
		if err := validate(b); err != nil {
			return "", err
		}
	}
	if a != "" && b != "" && a >= b {
		return "", errors.New("rank: " + a + " >= " + b)
	}
	switch {
	case a == "" && b == "":
		return "a" + string(zero), nil
	case a == "":
		ib, err := integerPart(b)
		if err != nil {
			return "", err
		}
		fb := b[len(ib):]
		if ib == "A"+strings.Repeat(string(zero), 26) {
			mid, err := midpoint("", fb)
			if err != nil {
				return "", err
			}
			return ib + mid, nil
		}
		if ib < b {
			return ib, nil
		}
		res, ok, err := decrementInteger(ib)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", errors.New("rank: cannot decrement any more")
		}
		return res, nil
	case b == "":
		ia, err := integerPart(a)
		if err != nil {
			return "", err
		}
		fa := a[len(ia):]
		i, ok, err := incrementInteger(ia)
		if err != nil {
			return "", err
		}
		if ok {
			return i, nil
		}
		mid, err := midpoint(fa, "")
		if err != nil {
			return "", err
		}
		return ia + mid, nil
	}
	ia, err := integerPart(a)
	if err != nil {
		return "", err
	}
	ib, err := integerPart(b)
	if err != nil {
		return "", err
	}
	fa, fb := a[len(ia):], b[len(ib):]
	if ia == ib {
		mid, err := midpoint(fa, fb)
		if err != nil {
			return "", err
		}
		return ia + mid, nil
	}
	i, ok, err := incrementInteger(ia)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errors.New("rank: cannot increment any more")
	}
	if i < b {
		return i, nil
	}
	mid, err := midpoint(fa, "")
	if err != nil {
		return "", err
	}
	return ia + mid, nil
}

// After appends: a key strictly greater than prev, or the first key when prev
// is "". What the Trello-compatible upload ranks a new card by.
func After(prev string) (string, error) { return Between(prev, "") }
