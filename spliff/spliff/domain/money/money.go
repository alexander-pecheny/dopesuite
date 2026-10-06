// Package money is the one way Spliff writes an amount down: an integer count
// of a currency's minor units plus its ISO 4217 code. There is no float
// anywhere near it — a tenth of a cent that a float invents is a cent somebody
// ends up owing, and the whole point of the Debt graph is that two people
// reading it see the same numbers.
//
// The currency table lives in code rather than the database because it is not
// data anybody edits: it is ISO 4217, it changes once a decade, and a rate
// table that arrives without it would leave every amount unreadable. It is in
// currencies.go, and it carries the English name beside the exponent — the
// picker needs to say what a code MEANS, and that is the same fact.
package money

import (
	"errors"
	"strconv"
	"strings"
)

const (
	decimalBase = 10
	int64Bits   = 64
	codeLen     = 3 // an ISO 4217 code is three letters
)

// Amount is a quantity of one currency, counted in that currency's minor
// units: 1234 USD minor units is $12.34, 1234 JPY minor units is ¥1234.
type Amount struct {
	Minor    int64
	Currency string
}

// ErrBadAmount is returned by Parse for anything that is not a number in the
// currency's own precision. It is a User Error at the HTTP edge: the app words
// it from its Catalog.
var ErrBadAmount = errors.New("money: not an amount in this currency")

// ErrUnknownCurrency names a code the exponent table does not carry — which
// also means the rate tables do not carry it, so nothing could be converted.
var ErrUnknownCurrency = errors.New("money: unknown currency")

// Exponent is how many minor digits a currency has: 0 for JPY, 3 for KWD and
// its five siblings, 2 for everything else — including a code the table does
// not carry, which is what a currency added to ISO 4217 after this table was
// written will be.
func Exponent(currency string) int {
	if c, ok := Lookup(currency); ok {
		return c.Exponent
	}
	return 2
}

// Scale is 10^Exponent — the number of minor units in one major unit.
func Scale(currency string) int64 {
	scale := int64(1)
	for range Exponent(currency) {
		scale *= decimalBase
	}
	return scale
}

// Normalise is the one spelling of a currency code: upper case, trimmed.
func Normalise(currency string) string {
	return strings.ToUpper(strings.TrimSpace(currency))
}

// ValidCode reports whether a code is shaped like ISO 4217 — three letters.
// Which codes actually exist is the rate table's business, not ours: it carries
// about 160 of them and the set changes without us.
func ValidCode(currency string) bool {
	c := Normalise(currency)
	if len(c) != codeLen {
		return false
	}
	for _, r := range c {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

// Parse reads what a person typed into an Amount of the named currency. It
// accepts an optional sign, digits, and at most Exponent(currency) decimal
// places after either separator people actually use — a JPY amount takes no
// decimal point at all, because there is no such thing as a tenth of a yen and
// silently rounding one away is how a bill stops adding up.
func Parse(s, currency string) (Amount, error) {
	if !ValidCode(currency) {
		return Amount{}, ErrUnknownCurrency
	}
	currency = Normalise(currency)
	raw, neg := clean(s)
	whole, frac, _ := strings.Cut(raw, ".")
	if whole == "" && frac == "" {
		return Amount{}, ErrBadAmount // nothing, or a lone "."
	}
	exp := Exponent(currency)
	if len(frac) > exp {
		return Amount{}, ErrBadAmount
	}
	if !digitsOnly(whole) || !digitsOnly(frac) {
		return Amount{}, ErrBadAmount
	}
	if whole == "" {
		whole = "0"
	}
	minor, err := strconv.ParseInt(whole+frac+strings.Repeat("0", exp-len(frac)), decimalBase, int64Bits)
	if err != nil {
		return Amount{}, ErrBadAmount
	}
	if neg {
		minor = -minor
	}
	return Amount{Minor: minor, Currency: currency}, nil
}

// clean drops the spaces people type between digit groups (any white space,
// the no-break space included), reads a comma as the decimal point, and splits
// off the sign.
func clean(s string) (raw string, neg bool) {
	raw = strings.Join(strings.Fields(s), "")
	raw = strings.ReplaceAll(raw, ",", ".")
	if raw == "" {
		return "", false
	}
	switch raw[0] {
	case '-':
		neg, raw = true, raw[1:]
	case '+':
		raw = raw[1:]
	}
	return raw, neg
}

func digitsOnly(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// String formats the amount's number alone, in the currency's own precision
// and with a plain "." separator: "12.34", "-0.05", "1234" for JPY. The code is
// not part of it because every place that shows an amount decides for itself
// whether the currency is already said elsewhere.
func (a Amount) String() string { return Format(a.Minor, a.Currency) }

// Format writes a count of minor units in the currency's own precision.
func Format(minor int64, currency string) string {
	exp := Exponent(currency)
	sign := ""
	if minor < 0 {
		sign = "-"
		minor = -minor
	}
	if exp == 0 {
		return sign + strconv.FormatInt(minor, decimalBase)
	}
	scale := Scale(currency)
	whole, frac := minor/scale, minor%scale
	fracDigits := strconv.FormatInt(frac, decimalBase)
	return sign + strconv.FormatInt(whole, decimalBase) + "." +
		strings.Repeat("0", exp-len(fracDigits)) + fracDigits
}

// WithCode is the amount as a person reads it on a line of its own: "12.34 EUR".
func (a Amount) WithCode() string { return a.String() + " " + a.Currency }

// IsZero is the test every refusal in the app asks — nobody leaves a Group, is
// kicked from one, or deletes one while an amount is not this.
func (a Amount) IsZero() bool { return a.Minor == 0 }

// Neg is the amount owed the other way round.
func (a Amount) Neg() Amount { return Amount{Minor: -a.Minor, Currency: a.Currency} }

// Abs drops the sign: a debt and a credit of the same size are the same size.
func (a Amount) Abs() Amount {
	if a.Minor < 0 {
		return a.Neg()
	}
	return a
}
