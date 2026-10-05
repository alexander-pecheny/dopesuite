// Package idstr turns the int64 row ids every app uses into decimal strings and
// back. It exists so call sites do not repeat strconv's base and bit size.
package idstr

import "strconv"

const (
	decimal = 10
	bits    = 64
)

// Format writes id in decimal.
func Format(id int64) string { return strconv.FormatInt(id, decimal) }

// Parse reads a decimal int64. Errors are strconv's.
func Parse(s string) (int64, error) { return strconv.ParseInt(s, decimal, bits) }

// Append appends id in decimal to dst.
func Append(dst []byte, id int64) []byte { return strconv.AppendInt(dst, id, decimal) }
