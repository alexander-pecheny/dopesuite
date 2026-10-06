// Package split turns a way of typing Shares into the Shares themselves.
//
// Only amounts are ever stored (spliff/CONTEXT.md). An even split is a form
// convenience, and this is where the convenience becomes the amounts. Three
// people and a 10.00 bill is 3.34/3.33/3.33, and WHICH of the three gets the
// extra cent has to be the same on everybody's phone. So the leftover minor
// units are handed out by largest remainder, and an equal remainder is broken
// by a stated order: the payers first, in descending Payment, then everyone
// else in split order.
//
// The editor computes the even split in the browser (web/ts/txform.ts), and
// this package is the reference it is checked against: testdata/even_cases.json
// is read by this package's tests and by web/jstest/split-parity.test.js. The
// server does not recompute a submitted split. It stores the amounts it is
// sent, because an even split is only one way of typing them.
package split

import (
	"errors"
	"math/big"
	"slices"
	"sort"
)

// Share is one Member's computed part of a derived split.
type Share struct {
	MemberID int64
	Minor    int64
}

// ErrWeights says the weights do not line up with the Members.
var ErrWeights = errors.New("split: one weight per member is required")

// PayerOrder is the order spare minor units go to the payers in: by
// descending Payment, equal Payments by join order. payments are the rows as
// typed, so one Member's two rows count as one Payment, and a row of nothing
// is not a Payment at all. joined is the Group's Members in join order.
func PayerOrder(payments []Share, joined []int64) []int64 {
	paid := map[int64]int64{}
	order := []int64{}
	for _, p := range payments {
		if p.Minor <= 0 {
			continue
		}
		if _, seen := paid[p.MemberID]; !seen {
			order = append(order, p.MemberID)
		}
		paid[p.MemberID] += p.Minor
	}
	sort.SliceStable(order, func(a, b int) bool {
		if paid[order[a]] != paid[order[b]] {
			return paid[order[a]] > paid[order[b]]
		}
		return slices.Index(joined, order[a]) < slices.Index(joined, order[b])
	})
	return order
}

// Even splits total equally across members, in their order. The odd minor
// units go one each in priority order: payers (as PayerOrder lists them),
// then split order.
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

// Allocate is the primitive under Even and under the ledger's spreading of
// Unclaimed across the payers: each Member's weight is the
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
	// it, less when they leave part of it Unclaimed.
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
// whole total in every split that covers it. The rounding only bites on
// weights that do not cover it, where a half minor unit belongs to nobody.
func roundNearest(r *big.Rat) int64 {
	twice := new(big.Rat).Mul(r, big.NewRat(2, 1))
	twice.Add(twice, big.NewRat(1, 1))
	half := new(big.Int).Quo(twice.Num(), new(big.Int).Mul(twice.Denom(), big.NewInt(2)))
	return half.Int64()
}
