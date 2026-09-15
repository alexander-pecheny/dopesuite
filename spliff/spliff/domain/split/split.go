// Package split turns a way of typing Shares into the Shares themselves.
//
// Only amounts are ever stored (spliff/CONTEXT.md): an even split and a
// percentage split are form conveniences, and this is where the convenience
// becomes the amounts. Three people and a 10.00 bill is 3.34/3.33/3.33, and
// WHICH of the three gets the extra cent has to be the same on everybody's
// phone — so the leftover minor units are handed out by largest remainder, and
// an equal remainder is broken by a stated order: the payers first, in
// descending Payment, then everyone else in split order.
package split

import (
	"errors"
	"math/big"
	"sort"
)

// Share is one Member's computed part of a derived split.
type Share struct {
	MemberID int64
	Minor    int64
}

var (
	// ErrWeights says the weights do not line up with the Members.
	ErrWeights = errors.New("split: one weight per member is required")
	// ErrPercent says a percentage is negative, or they sum past 100 — which
	// would make the Shares exceed the total, and Shares never may.
	ErrPercent = errors.New("split: percentages must be non-negative and sum to at most 100")
)

// Even splits total equally across members. The odd minor units go one each in
// priority order (payers by descending Payment, then split order).
func Even(total int64, members []int64, payers []int64) []Share {
	n := len(members)
	if n == 0 {
		return nil
	}
	weights := make([]*big.Rat, n)
	for i := range weights {
		weights[i] = big.NewRat(1, int64(n))
	}
	shares, _ := Allocate(total, members, weights, payers)
	return shares
}

// ByPercent splits total by percentage. Percentages that sum to less than 100
// leave the rest Unclaimed, which is a normal state and not an error; summing
// past 100 is refused, because a Share may never exceed the total.
func ByPercent(total int64, members []int64, percents []*big.Rat, payers []int64) ([]Share, error) {
	if len(percents) != len(members) {
		return nil, ErrWeights
	}
	sum := new(big.Rat)
	weights := make([]*big.Rat, len(percents))
	for i, p := range percents {
		if p == nil || p.Sign() < 0 {
			return nil, ErrPercent
		}
		sum.Add(sum, p)
		weights[i] = new(big.Rat).Quo(p, big.NewRat(100, 1))
	}
	if sum.Cmp(big.NewRat(100, 1)) > 0 {
		return nil, ErrPercent
	}
	return Allocate(total, members, weights, payers)
}

// Allocate is the primitive underneath both: each Member's weight is the
// fraction of the total they answer for, and the exact fractions are rounded
// into whole minor units by largest remainder.
//
// The Shares come back in members order, so a caller can zip them with whatever
// it built the list from.
func Allocate(total int64, members []int64, weights []*big.Rat, payers []int64) ([]Share, error) {
	if len(weights) != len(members) {
		return nil, ErrWeights
	}
	n := len(members)
	out := make([]Share, n)
	if n == 0 {
		return out, nil
	}

	exact := make([]*big.Rat, n)
	sum := new(big.Rat)
	floors := int64(0)
	remainders := make([]*big.Rat, n)
	for i, w := range weights {
		if w == nil || w.Sign() < 0 {
			return nil, ErrWeights
		}
		exact[i] = new(big.Rat).Mul(new(big.Rat).SetInt64(total), w)
		sum.Add(sum, exact[i])
		whole := new(big.Int).Quo(exact[i].Num(), exact[i].Denom()) // truncates; totals are never negative
		out[i] = Share{MemberID: members[i], Minor: whole.Int64()}
		floors += out[i].Minor
		remainders[i] = new(big.Rat).Sub(exact[i], new(big.Rat).SetInt(whole))
	}

	// What the shares SHOULD add up to: the whole total when the weights cover
	// it, less when a percentage split leaves part of it Unclaimed.
	target := roundNearest(sum)
	leftover := target - floors

	order := rank(members, payers)
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		if c := remainders[idx[b]].Cmp(remainders[idx[a]]); c != 0 {
			return c < 0 // bigger remainder first
		}
		return order[idx[a]] < order[idx[b]]
	})
	for k := int64(0); k < leftover && k < int64(n); k++ {
		out[idx[k]].Minor++
	}
	return out, nil
}

// rank is the order leftover minor units are handed out in: the payers, in the
// order they were given (descending Payment), then everyone else in split
// order. It answers one number per member, so the sort can read it directly.
func rank(members, payers []int64) []int {
	place := make(map[int64]int, len(payers))
	for i, p := range payers {
		if _, seen := place[p]; !seen {
			place[p] = i
		}
	}
	out := make([]int, len(members))
	next := len(payers)
	for i, m := range members {
		if p, ok := place[m]; ok {
			out[i] = p
			continue
		}
		out[i] = next + i
	}
	return out
}

// roundNearest rounds a non-negative rational to the nearest integer, halves
// up. It is only ever asked about the SUM of the weighted shares, which is the
// whole total in every split that covers it — the rounding only bites on a
// percentage split that does not, where a half minor unit belongs to nobody.
func roundNearest(r *big.Rat) int64 {
	twice := new(big.Rat).Mul(r, big.NewRat(2, 1))
	twice.Add(twice, big.NewRat(1, 1))
	half := new(big.Int).Quo(twice.Num(), new(big.Int).Mul(twice.Denom(), big.NewInt(2)))
	return half.Int64()
}
