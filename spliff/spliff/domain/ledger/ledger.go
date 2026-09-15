// Package ledger is the arithmetic a Group is read with: every Member's Net
// balance in the Base currency, and the Debt graph those balances resolve into.
//
// Nothing here is stored. Both are pure functions of the live Transactions, so
// changing a Group's Base currency is a re-read and not a rewrite
// (spliff/docs/adr/0001), and two people looking at the same Group see the same
// numbers because the two places the arithmetic could go either way — which
// payer absorbs a spare minor unit, and which pair the graph picks next — are
// both decided by a stated order.
package ledger

import (
	"errors"
	"math/big"
	"sort"

	"spliff/spliff/domain/money"
	"spliff/spliff/domain/rates"
	"spliff/spliff/domain/split"
)

// Entry is one Member's Payment or Share on a Transaction, in the
// Transaction's own currency.
type Entry struct {
	MemberID int64
	Minor    int64
}

// Transaction is what the ledger reads of one live Transaction. Table is the
// Rate table of its Rate date — the caller resolved that with
// rates.ResolveDay, because only it knows which days have tables.
//
// Pin is the Pinned rate, which v1 never sets: the arithmetic carries it so
// that shipping it is a migration and a form field.
type Transaction struct {
	ID       int64
	Currency string
	Total    int64
	Payments []Entry
	Shares   []Entry
	Table    rates.Table
	Pin      *rates.Pin
}

// Unclaimed is the part of the total no Share accounts for. It belongs to
// nobody and is owed by nobody: the payers absorb it.
func (t Transaction) Unclaimed() int64 {
	claimed := int64(0)
	for _, s := range t.Shares {
		claimed += s.Minor
	}
	if left := t.Total - claimed; left > 0 {
		return left
	}
	return 0
}

// Balance is one Member's Net balance in the Base currency: positive means the
// Group owes them, negative means they owe the Group.
type Balance struct {
	MemberID int64
	Minor    int64
}

// Transfer is one line of the Debt graph: From hands To this much, in the Base
// currency.
type Transfer struct {
	From  int64
	To    int64
	Minor int64
}

// Balances is every Member's Net balance, in members order — which is join
// order, and therefore the tie-break everything else in this package uses.
//
// Each Transaction contributes Payments − Shares − absorbed Unclaimed, in its
// own currency, converted at its Rate date. The conversion is kept exact all
// the way and the whole column is rounded ONCE, at the end, by largest
// remainder — which is what makes the balances sum to exactly zero rather than
// to a stray minor unit somebody has to explain.
func Balances(base string, members []int64, txs []Transaction) ([]Balance, error) {
	base = money.Normalise(base)
	index := make(map[int64]int, len(members))
	for i, m := range members {
		index[m] = i
	}
	exact := make([]*big.Rat, len(members))
	for i := range exact {
		exact[i] = new(big.Rat)
	}

	for _, tx := range txs {
		// One minor unit of the Transaction's currency, in Base minor units.
		// Conversion is linear, so this factor carries the whole Transaction.
		factor, err := rates.ConvertExact(
			money.Amount{Minor: 1, Currency: tx.Currency}, base, tx.Table, tx.Pin)
		if err != nil {
			return nil, err
		}
		for member, net := range tx.nets() {
			i, ok := index[member]
			if !ok {
				// Every Payment and Share names a current Member (the Group
				// refuses to let a non-zero Member go), so this cannot happen —
				// and if it ever did, dropping the amount would silently move it
				// onto everybody else. Refuse instead.
				return nil, ErrStranger
			}
			exact[i].Add(exact[i], new(big.Rat).Mul(new(big.Rat).SetInt64(net), factor))
		}
	}

	out := roundColumn(members, exact)
	// The assertion the whole design rests on: a Group's Net balances sum to
	// zero. If they ever did not, the Debt graph below would invent or destroy
	// money, so this is a refusal and not a log line.
	sum := int64(0)
	for _, b := range out {
		sum += b.Minor
	}
	if sum != 0 {
		return nil, ErrNotZeroSum
	}
	return out, nil
}

var (
	// ErrStranger says a Payment or a Share names somebody who is not a Member
	// of the Group. Nothing in the app may produce one: a Member with a
	// non-zero balance cannot leave and cannot be kicked, so it is the
	// assertion that those rules held.
	ErrStranger = errors.New("ledger: a payment or share names somebody who is not a member of the group")
	// ErrNotZeroSum says the balances did not sum to zero — which means a
	// Transaction's Payments did not sum to its total, or its Shares overdrew
	// it. Both are refused on every write.
	ErrNotZeroSum = errors.New("ledger: balances do not sum to zero")
)

// nets is what one Transaction does to each Member's balance, in the
// Transaction's own currency: what they paid, less what they answer for, less
// their share of what nobody claimed. They sum to exactly zero.
func (t Transaction) nets() map[int64]int64 {
	out := make(map[int64]int64, len(t.Payments)+len(t.Shares))
	for _, p := range t.Payments {
		out[p.MemberID] += p.Minor
	}
	for _, s := range t.Shares {
		out[s.MemberID] -= s.Minor
	}
	for member, absorbed := range t.absorbed() {
		out[member] -= absorbed
	}
	return out
}

// absorbed hands the Unclaimed part to the payers, pro rata to their Payments,
// by largest remainder — with the spare minor units going to the biggest
// Payment first, which is the same rule a derived split uses.
func (t Transaction) absorbed() map[int64]int64 {
	unclaimed := t.Unclaimed()
	if unclaimed == 0 || len(t.Payments) == 0 {
		return nil
	}
	paid := map[int64]int64{}
	order := []int64{}
	totalPaid := int64(0)
	for _, p := range t.Payments {
		if _, seen := paid[p.MemberID]; !seen {
			order = append(order, p.MemberID)
		}
		paid[p.MemberID] += p.Minor
		totalPaid += p.Minor
	}
	if totalPaid <= 0 {
		return nil
	}
	// Payers by descending Payment; a tie keeps the order they were entered in,
	// which is the Transaction's own and therefore the same for everybody.
	sort.SliceStable(order, func(a, b int) bool { return paid[order[a]] > paid[order[b]] })

	weights := make([]*big.Rat, len(order))
	for i, m := range order {
		weights[i] = new(big.Rat).SetFrac64(paid[m], totalPaid)
	}
	shares, err := split.Allocate(unclaimed, order, weights, order)
	if err != nil {
		return nil
	}
	out := make(map[int64]int64, len(shares))
	for _, s := range shares {
		out[s.MemberID] = s.Minor
	}
	return out
}

// roundColumn turns a column of exact balances that sums to zero into a column
// of whole minor units that still sums to zero: floor everything, then hand the
// shortfall out one minor unit at a time to the largest fractional parts, ties
// by join order.
func roundColumn(members []int64, exact []*big.Rat) []Balance {
	out := make([]Balance, len(members))
	fracs := make([]*big.Rat, len(members))
	floors := int64(0)
	for i, v := range exact {
		f := floorRat(v)
		out[i] = Balance{MemberID: members[i], Minor: f}
		fracs[i] = new(big.Rat).Sub(v, new(big.Rat).SetInt64(f))
		floors += f
	}
	short := -floors // the exact column sums to zero, so this is what flooring took
	idx := make([]int, len(members))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return fracs[idx[b]].Cmp(fracs[idx[a]]) < 0 })
	for k := int64(0); k < short && k < int64(len(idx)); k++ {
		out[idx[k]].Minor++
	}
	return out
}

// floorRat rounds towards negative infinity, which is what makes the fractional
// parts non-negative and the shortfall a count of whole minor units.
func floorRat(r *big.Rat) int64 {
	q, rem := new(big.Int).QuoRem(r.Num(), r.Denom(), new(big.Int))
	if rem.Sign() < 0 {
		q.Sub(q, big.NewInt(1))
	}
	return q.Int64()
}

// Transfers resolves the balances into the transfer list the Group page shows.
//
// Greedy: the biggest creditor and the biggest debtor are paired, the smaller
// of the two amounts moves, and whoever that settled drops out — so every step
// finishes at least one person and the list is never longer than n−1 lines.
// Ties are broken by join order, so the list is the same on everybody's phone.
//
// balances must be in join order; Balances answers them that way.
func Transfers(balances []Balance) []Transfer {
	type side struct {
		member int64
		amount int64
		place  int
	}
	var creditors, debtors []side
	for i, b := range balances {
		switch {
		case b.Minor > 0:
			creditors = append(creditors, side{b.MemberID, b.Minor, i})
		case b.Minor < 0:
			debtors = append(debtors, side{b.MemberID, -b.Minor, i})
		}
	}
	// largest picks the biggest remaining amount, the earliest joiner on a tie.
	largest := func(s []side) int {
		best := -1
		for i := range s {
			if s[i].amount == 0 {
				continue
			}
			if best < 0 || s[i].amount > s[best].amount ||
				(s[i].amount == s[best].amount && s[i].place < s[best].place) {
				best = i
			}
		}
		return best
	}

	out := []Transfer{}
	for {
		c, d := largest(creditors), largest(debtors)
		if c < 0 || d < 0 {
			return out
		}
		amount := creditors[c].amount
		if debtors[d].amount < amount {
			amount = debtors[d].amount
		}
		out = append(out, Transfer{From: debtors[d].member, To: creditors[c].member, Minor: amount})
		creditors[c].amount -= amount
		debtors[d].amount -= amount
	}
}
