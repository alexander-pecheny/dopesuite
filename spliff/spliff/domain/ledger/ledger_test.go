package ledger

import (
	"testing"

	"spliff/spliff/domain/rates"
)

// eur is a Rate table round enough to check by hand: one USD buys two GEL and
// half a euro, so 1 EUR is 5 GEL and 150 JPY is 1 USD is 0.25 KWD.
func eur() rates.Table {
	return rates.Table{Day: "2026-09-10", Rates: map[string]string{
		"USD": "1", "EUR": "0.5", "GEL": "2.5", "JPY": "150", "KWD": "0.25",
	}}
}

func pay(member, minor int64) Entry   { return Entry{MemberID: member, Minor: minor} }
func share(member, minor int64) Entry { return Entry{MemberID: member, Minor: minor} }

func balancesOf(t *testing.T, base string, members []int64, txs ...Transaction) map[int64]int64 {
	t.Helper()
	bs, err := Balances(base, members, txs)
	if err != nil {
		t.Fatalf("Balances: %v", err)
	}
	out := map[int64]int64{}
	var sum int64
	for _, b := range bs {
		out[b.MemberID] = b.Minor
		sum += b.Minor
	}
	if sum != 0 {
		t.Fatalf("balances %v sum to %d, want 0", out, sum)
	}
	return out
}

func want(t *testing.T, got map[int64]int64, expected map[int64]int64) {
	t.Helper()
	if len(got) != len(expected) {
		t.Fatalf("balances = %v, want %v", got, expected)
	}
	for m, v := range expected {
		if got[m] != v {
			t.Fatalf("balances = %v, want %v", got, expected)
		}
	}
}

// The old rule — "A paid X, split evenly between everybody" — must fall out of
// the general one unchanged: one Payment, Shares covering the total, nothing
// Unclaimed.
func TestSinglePayerEvenSplit(t *testing.T) {
	tx := Transaction{
		ID: 1, Currency: "EUR", Total: 3000,
		Payments: []Entry{pay(1, 3000)},
		Shares:   []Entry{share(1, 1000), share(2, 1000), share(3, 1000)},
		Table:    eur(),
	}
	got := balancesOf(t, "EUR", []int64{1, 2, 3}, tx)
	want(t, got, map[int64]int64{1: 2000, 2: -1000, 3: -1000})
}

// Two payers and a third of the bill nobody claimed: the payers absorb the
// Unclaimed part pro rata to what they put in.
func TestTwoPayersWithUnclaimed(t *testing.T) {
	// 90.00 EUR: A paid 60, B paid 30. C claims 30.00, so 60.00 is Unclaimed
	// and splits 2:1 between A and B — 40.00 and 20.00.
	//   A: +6000 − 0 − 4000 = +2000
	//   B: +3000 − 0 − 2000 = +1000
	//   C:  0 − 3000 − 0    = −3000
	tx := Transaction{
		ID: 1, Currency: "EUR", Total: 9000,
		Payments: []Entry{pay(1, 6000), pay(2, 3000)},
		Shares:   []Entry{share(3, 3000)},
		Table:    eur(),
	}
	if u := tx.Unclaimed(); u != 6000 {
		t.Fatalf("Unclaimed = %d, want 6000", u)
	}
	got := balancesOf(t, "EUR", []int64{1, 2, 3}, tx)
	want(t, got, map[int64]int64{1: 2000, 2: 1000, 3: -3000})
}

// "I paid, claim your part": the payer sets the total and their own Share, the
// rest is Unclaimed and comes straight back to them — so before anybody claims,
// the payer's balance is exactly what the others have not taken yet.
func TestClaimYourPart(t *testing.T) {
	base := Transaction{
		ID: 1, Currency: "EUR", Total: 3000,
		Payments: []Entry{pay(1, 3000)},
		Shares:   []Entry{share(1, 1000)},
		Table:    eur(),
	}
	// Nothing claimed yet: A holds 20.00 of Unclaimed, so everybody is level.
	want(t, balancesOf(t, "EUR", []int64{1, 2, 3}, base), map[int64]int64{1: 0, 2: 0, 3: 0})

	// B claims 10.00: A is owed it, B owes it, C is still out of it.
	claimed := base
	claimed.Shares = []Entry{share(1, 1000), share(2, 1000)}
	want(t, balancesOf(t, "EUR", []int64{1, 2, 3}, claimed), map[int64]int64{1: 1000, 2: -1000, 3: 0})
}

// Rounding an Unclaimed part must never leave a minor unit unaccounted for:
// three payers, one spare cent, and the balances still sum to zero.
func TestRoundingNeverLeaks(t *testing.T) {
	// 10.00 EUR, nothing claimed at all, three equal payers: each absorbs what
	// they paid, so everybody is level and the spare cent of the absorption is
	// cancelled by the Payment it came from.
	tx := Transaction{
		ID: 1, Currency: "EUR", Total: 1000,
		Payments: []Entry{pay(1, 334), pay(2, 333), pay(3, 333)},
		Table:    eur(),
	}
	want(t, balancesOf(t, "EUR", []int64{1, 2, 3}, tx), map[int64]int64{1: 0, 2: 0, 3: 0})

	// 10.00 EUR paid by three, one 0.01 Share by a fourth: the 9.99 Unclaimed
	// splits 334:333:333 of 10.00 — 333.666/332.667/332.667, and the two spare
	// cents go by largest remainder to B and C, so 333/333/333.
	//   A: +334 − 333 = +1 ; B: +333 − 333 = 0 ; C: +333 − 333 = 0 ; D: −1
	tx.Shares = []Entry{share(4, 1)}
	got := balancesOf(t, "EUR", []int64{1, 2, 3, 4}, tx)
	want(t, got, map[int64]int64{1: 1, 2: 0, 3: 0, 4: -1})
}

// A Settlement is not a kind of record: one Payment and one Share, the whole
// amount each. Adding one to a pair that owed must bring both to zero.
func TestSettlementZeroesAPair(t *testing.T) {
	bill := Transaction{
		ID: 1, Currency: "EUR", Total: 2000,
		Payments: []Entry{pay(1, 2000)},
		Shares:   []Entry{share(1, 1000), share(2, 1000)},
		Table:    eur(),
	}
	want(t, balancesOf(t, "EUR", []int64{1, 2}, bill), map[int64]int64{1: 1000, 2: -1000})

	settle := Transaction{
		ID: 2, Currency: "EUR", Total: 1000,
		Payments: []Entry{pay(2, 1000)},
		Shares:   []Entry{share(1, 1000)},
		Table:    eur(),
	}
	want(t, balancesOf(t, "EUR", []int64{1, 2}, bill, settle), map[int64]int64{1: 0, 2: 0})
	if tr := Transfers([]Balance{{1, 0}, {2, 0}}); len(tr) != 0 {
		t.Errorf("Transfers on a settled group = %v, want none", tr)
	}
}

// Changing the Base currency restates every balance and rewrites nothing: the
// same Transactions read in GEL are the EUR numbers at the day's rate.
func TestBaseCurrencyChangeIsAReRead(t *testing.T) {
	tx := Transaction{
		ID: 1, Currency: "EUR", Total: 3000,
		Payments: []Entry{pay(1, 3000)},
		Shares:   []Entry{share(1, 1000), share(2, 1000), share(3, 1000)},
		Table:    eur(),
	}
	members := []int64{1, 2, 3}
	want(t, balancesOf(t, "EUR", members, tx), map[int64]int64{1: 2000, 2: -1000, 3: -1000})
	// 1 EUR is 5 GEL (0.5 per USD against 2.5 per USD).
	want(t, balancesOf(t, "GEL", members, tx), map[int64]int64{1: 10000, 2: -5000, 3: -5000})
	// And in USD, at 2 USD to the euro.
	want(t, balancesOf(t, "USD", members, tx), map[int64]int64{1: 4000, 2: -2000, 3: -2000})
}

// A currency with no minor unit at all: 1000 JPY across three is 334/333/333,
// and reading it in JPY must not invent a decimal place.
func TestZeroExponentCurrency(t *testing.T) {
	tx := Transaction{
		ID: 1, Currency: "JPY", Total: 1000,
		Payments: []Entry{pay(1, 1000)},
		Shares:   []Entry{share(1, 334), share(2, 333), share(3, 333)},
		Table:    eur(),
	}
	want(t, balancesOf(t, "JPY", []int64{1, 2, 3}, tx), map[int64]int64{1: 666, 2: -333, 3: -333})
	// The same bill read in USD: 150 JPY to the dollar, so 666/150 = 4.44 USD.
	want(t, balancesOf(t, "USD", []int64{1, 2, 3}, tx), map[int64]int64{1: 444, 2: -222, 3: -222})
}

// A three-digit currency: KWD counts thousandths, and a 1.000 KWD bill split
// three ways is 334/333/333 fils.
func TestThreeDigitCurrency(t *testing.T) {
	tx := Transaction{
		ID: 1, Currency: "KWD", Total: 1000,
		Payments: []Entry{pay(1, 1000)},
		Shares:   []Entry{share(1, 334), share(2, 333), share(3, 333)},
		Table:    eur(),
	}
	want(t, balancesOf(t, "KWD", []int64{1, 2, 3}, tx), map[int64]int64{1: 666, 2: -333, 3: -333})
	// 0.25 KWD to the dollar, so 0.666 KWD is 2.664 USD.
	want(t, balancesOf(t, "USD", []int64{1, 2, 3}, tx), map[int64]int64{1: 266, 2: -133, 3: -133})
}

// Each Transaction converts at ITS OWN Rate date, so two bills on two days do
// not share a rate.
func TestPerTransactionRateDate(t *testing.T) {
	cheap := rates.Table{Day: "2026-09-01", Rates: map[string]string{"USD": "1", "EUR": "0.5"}}
	dear := rates.Table{Day: "2026-09-10", Rates: map[string]string{"USD": "1", "EUR": "1"}}
	first := Transaction{
		ID: 1, Currency: "EUR", Total: 1000,
		Payments: []Entry{pay(1, 1000)},
		Shares:   []Entry{share(2, 1000)},
		Table:    cheap,
	}
	second := first
	second.ID, second.Table = 2, dear
	// 10.00 EUR at 2 USD each, plus 10.00 EUR at 1 USD each = 30.00 USD.
	want(t, balancesOf(t, "USD", []int64{1, 2}, first, second), map[int64]int64{1: 3000, 2: -3000})
}

func TestBalancesRefusesAStranger(t *testing.T) {
	tx := Transaction{
		ID: 1, Currency: "EUR", Total: 1000,
		Payments: []Entry{pay(9, 1000)},
		Shares:   []Entry{share(1, 1000)},
		Table:    eur(),
	}
	if _, err := Balances("EUR", []int64{1, 2}, []Transaction{tx}); err != ErrStranger {
		t.Errorf("Balances with a stranger = %v, want ErrStranger", err)
	}
}

func TestTransfers(t *testing.T) {
	cases := []struct {
		name     string
		balances []Balance
		want     []Transfer
	}{
		{
			name:     "one debtor one creditor",
			balances: []Balance{{1, 1000}, {2, -1000}},
			want:     []Transfer{{From: 2, To: 1, Minor: 1000}},
		},
		{
			// A is owed 30, B owes 20, C owes 10: largest against largest, then
			// what is left.
			name:     "largest against largest",
			balances: []Balance{{1, 3000}, {2, -2000}, {3, -1000}},
			want: []Transfer{
				{From: 2, To: 1, Minor: 2000},
				{From: 3, To: 1, Minor: 1000},
			},
		},
		{
			// Four people, two a side: the biggest debtor pays the biggest
			// creditor first, and the pairing is re-picked each round — so D's
			// 15.00 goes to B, who is now the larger creditor, before the 5.00
			// left over comes back to A. Three lines for four people.
			name:     "two a side",
			balances: []Balance{{1, 3000}, {2, 1000}, {3, -2500}, {4, -1500}},
			want: []Transfer{
				{From: 3, To: 1, Minor: 2500},
				{From: 4, To: 2, Minor: 1000},
				{From: 4, To: 1, Minor: 500},
			},
		},
		{
			// Equal amounts on both sides: the earlier joiner goes first, both
			// sides — which is what makes the list the same on every phone.
			name:     "ties go by join order",
			balances: []Balance{{1, 1000}, {2, 1000}, {3, -1000}, {4, -1000}},
			want: []Transfer{
				{From: 3, To: 1, Minor: 1000},
				{From: 4, To: 2, Minor: 1000},
			},
		},
		{
			name:     "a settled group owes nothing",
			balances: []Balance{{1, 0}, {2, 0}},
			want:     []Transfer{},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Transfers(c.balances)
			if len(got) != len(c.want) {
				t.Fatalf("Transfers = %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("Transfers = %v, want %v", got, c.want)
				}
			}
			if n := len(c.balances); len(got) > n-1 && n > 0 {
				t.Errorf("Transfers gave %d lines for %d members, want at most %d", len(got), n, n-1)
			}
		})
	}
}

// Whatever the balances, the transfers must move exactly what each person is
// owed or owes — no more, no less.
func TestTransfersSettleEverybody(t *testing.T) {
	balances := []Balance{{1, 4321}, {2, -1234}, {3, 567}, {4, -3654}}
	net := map[int64]int64{}
	for _, b := range balances {
		net[b.MemberID] = b.Minor
	}
	for _, tr := range Transfers(balances) {
		if tr.Minor <= 0 {
			t.Fatalf("transfer of %d", tr.Minor)
		}
		net[tr.From] += tr.Minor
		net[tr.To] -= tr.Minor
	}
	for member, left := range net {
		if left != 0 {
			t.Errorf("member %d left holding %d after the transfers", member, left)
		}
	}
}
