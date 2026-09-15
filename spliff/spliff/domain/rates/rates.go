// Package rates is the Rate table and the one function that converts with it.
//
// A Rate table is every currency's rate against USD on one calendar day, as
// fetched that day. Tables exist only for days one was fetched, so a
// Transaction resolves its Rate date by nearest-day — its own date when a table
// exists for it, else the closest day that has one, the earlier one on a tie
// (spliff/docs/adr/0001).
//
// Every conversion in Spliff goes through Convert, including the ones a Pinned
// rate will one day take part in. v1 stores no Pin — there is no column and no
// form field — but the type, the argument and the chaining rule are here and
// tested, so shipping it is a migration and a form field rather than a redesign.
package rates

import (
	"errors"
	"math/big"
	"sort"
	"time"

	"spliff/spliff/domain/money"
)

// DayFormat is how a Rate date is written everywhere: a UTC calendar day.
const DayFormat = "2006-01-02"

// Table is one day's rates, each a decimal string of that currency per USD.
// They are strings because that is what the source sends and what the database
// keeps: a rate read back as a float is a rate that has already lost digits.
type Table struct {
	Day   string
	Rates map[string]string
}

// Pin is a rate a person wrote onto one Transaction because it is the one their
// bank really charged: Currency is what it converts INTO, and Rate is how many
// of those one unit of the Transaction's own currency buys.
//
// Not offered in v1; carried through Convert from the start so that adding it
// changes no arithmetic.
type Pin struct {
	Currency string
	Rate     string
}

var (
	// ErrNoRate says the day's table does not carry a currency the conversion
	// needs. It is what a Group whose base currency the source dropped gets.
	ErrNoRate = errors.New("rates: no rate for this currency on this day")
	// ErrBadRate says a stored rate is not a decimal number. A table that
	// cannot be read is a table that must not be guessed at.
	ErrBadRate = errors.New("rates: rate is not a number")
	// ErrNoTable says there is no Rate table at all — a fresh instance before
	// its first fetch, and nothing else.
	ErrNoTable = errors.New("rates: no rate table exists")
)

// Rate is one currency's rate on this day, as an exact rational.
func (t Table) Rate(currency string) (*big.Rat, error) {
	raw, ok := t.Rates[money.Normalise(currency)]
	if !ok {
		return nil, ErrNoRate
	}
	r, ok := new(big.Rat).SetString(raw)
	if !ok || r.Sign() <= 0 {
		return nil, ErrBadRate
	}
	return r, nil
}

// ResolveDay is the Rate date rule: the day itself when a table exists for it,
// else the nearest day that has one, the EARLIER one on a tie. days need not be
// sorted; it answers false only when there is no table at all.
//
// A tie goes to the earlier day because that is the one whose rate was already
// real when the expense happened: the later table is news the payer did not
// have.
func ResolveDay(days []string, day string) (string, bool) {
	if len(days) == 0 {
		return "", false
	}
	sorted := append([]string(nil), days...)
	sort.Strings(sorted)
	want, err := time.Parse(DayFormat, day)
	if err != nil {
		// An unparsable date is not a date; the newest table is the best guess
		// left and is what a Transaction with no date would get anyway.
		return sorted[len(sorted)-1], true
	}
	best, bestGap := "", time.Duration(0)
	for _, d := range sorted {
		parsed, err := time.Parse(DayFormat, d)
		if err != nil {
			continue
		}
		gap := parsed.Sub(want)
		if gap < 0 {
			gap = -gap
		}
		// sorted ascending, and `<` rather than `<=`, so the earlier day wins a tie.
		if best == "" || gap < bestGap {
			best, bestGap = d, gap
		}
	}
	if best == "" {
		return "", false
	}
	return best, true
}

// Convert turns an Amount into the `to` currency as of one Rate table, rounding
// half-even to `to`'s own minor units. The rule, in the order it is applied:
//
//   - a Pin naming `to` is used as-is, and the table is never opened;
//   - a Pin naming some other currency Y takes the amount to Y, and the day's
//     table takes it the rest of the way from Y;
//   - no Pin means the day's table alone: amount × (rate_to / rate_from).
//
// The intermediate stays an exact rational, so a chained conversion rounds once
// at the end rather than twice on the way.
func Convert(a money.Amount, to string, t Table, pin *Pin) (money.Amount, error) {
	value, err := ConvertExact(a, to, t, pin)
	if err != nil {
		return money.Amount{}, err
	}
	return money.Amount{Minor: RoundHalfEven(value), Currency: money.Normalise(to)}, nil
}

// ConvertExact is Convert without the rounding: the amount in `to`'s minor
// units as an exact rational. The ledger accumulates in it, so that a Group's
// balances can be rounded ONCE, together, and still sum to zero — rounding each
// Transaction on the way there is what makes a column of numbers not add up.
//
// It is linear in a.Minor, so converting one minor unit and multiplying is the
// same answer as converting the whole amount.
func ConvertExact(a money.Amount, to string, t Table, pin *Pin) (*big.Rat, error) {
	to = money.Normalise(to)
	from := money.Normalise(a.Currency)

	// Major units as an exact rational: minor / 10^exponent.
	value := new(big.Rat).SetFrac64(a.Minor, money.Scale(from))

	if pin != nil {
		rate, ok := new(big.Rat).SetString(pin.Rate)
		if !ok || rate.Sign() <= 0 {
			return nil, ErrBadRate
		}
		value.Mul(value, rate)
		from = money.Normalise(pin.Currency)
	}

	if from != to {
		rateFrom, err := t.Rate(from)
		if err != nil {
			return nil, err
		}
		rateTo, err := t.Rate(to)
		if err != nil {
			return nil, err
		}
		value.Mul(value, new(big.Rat).Quo(rateTo, rateFrom))
	}

	value.Mul(value, new(big.Rat).SetInt64(money.Scale(to)))
	return value, nil
}

// RoundHalfEven rounds an exact rational to the nearest integer, and to the
// even one when it sits exactly halfway. Half-up would bias every split in the
// payer's favour by a fraction of a minor unit per Transaction, which over a
// Group's life is a real number somebody notices.
func RoundHalfEven(r *big.Rat) int64 {
	num, den := r.Num(), r.Denom()
	q, rem := new(big.Int).QuoRem(num, den, new(big.Int))

	twice := new(big.Int).Abs(rem)
	twice.Lsh(twice, 1)
	switch twice.Cmp(den) {
	case -1:
		return q.Int64()
	case 0:
		if q.Bit(0) == 0 {
			return q.Int64()
		}
	}
	if num.Sign() < 0 {
		return q.Int64() - 1
	}
	return q.Int64() + 1
}
