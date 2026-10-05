// Package idstr turns int64s into decimal strings and back. Most of them are
// the row ids every app uses, hence the name, but counts, offsets and byte
// sizes go through it too. It exists so call sites do not repeat strconv's base
// and bit size.
package idstr

import "strconv"

const (
	decimal = 10
	bits    = 64
)

// Format writes n in decimal.
func Format(n int64) string { return strconv.FormatInt(n, decimal) }

// Parse reads a decimal int64. Errors are strconv's.
func Parse(s string) (int64, error) { return strconv.ParseInt(s, decimal, bits) }

// Append appends n in decimal to dst.
func Append(dst []byte, n int64) []byte { return strconv.AppendInt(dst, n, decimal) }
