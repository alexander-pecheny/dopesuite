package tests

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	spliffserver "spliff/spliff/server"
)

// A Phantom is a Member with a name and no account (spliff/CONTEXT.md). Every
// test here is about the one promise that makes it worth having: it is a Member
// in every way that touches money, and the day the person behind it joins,
// nothing about the Group's arithmetic changes.

// phantom mints one as the Owner and answers its member row.
func (w *world) phantom(t *testing.T, name string) int64 {
	t.Helper()
	var created struct {
		ID int64 `json:"id"`
	}
	w.alice.JSON(http.MethodPost, w.path("/phantoms"), map[string]any{"name": name}, &created)
	return created.ID
}

// mintInvite is the Owner's link, handed back as its code, for the tests that
// need to walk somebody through it themselves.
func (w *world) mintInvite(t *testing.T, approval bool) string {
	t.Helper()
	var link struct {
		Code string `json:"code"`
	}
	w.alice.JSON(http.MethodPost, w.path("/invites"), map[string]any{
		"label": "the trip", "max_uses": 0, "ttl_hours": 0, "requires_approval": approval,
	}, &link)
	return link.Code
}

func TestOwnerAddsAPhantomAndItIsAMember(t *testing.T) {
	w := newWorld(t)
	id := w.phantom(t, "Nino")
	if id == 0 {
		t.Fatal("no member row came back")
	}
	view := w.read(t, w.alice)
	var found *memberView
	for i := range view.Members {
		if view.Members[i].Name == "Nino" {
			found = &view.Members[i]
		}
	}
	if found == nil {
		t.Fatalf("Nino is not among the members: %+v", view.Members)
	}
	if !found.IsPhantom || found.UserID != 0 {
		t.Errorf("Nino = %+v, want a phantom with no account", *found)
	}
	if found.IsOwner {
		t.Error("a phantom owns nothing")
	}

	// Only the Owner may seat one, and a name is required.
	w.invite(t, w.bob, false)
	if resp := w.bob.Do(http.MethodPost, w.path("/phantoms"),
		map[string]any{"name": "Gio"}); resp.Code != http.StatusForbidden {
		t.Errorf("bob adding a phantom = %d, want 403", resp.Code)
	}
	if resp := w.alice.Do(http.MethodPost, w.path("/phantoms"),
		map[string]any{"name": "   "}); resp.Code != http.StatusBadRequest {
		t.Errorf("a nameless phantom = %d, want 400", resp.Code)
	}
}

// A Phantom pays and holds Shares like anybody, and the Debt graph names it.
func TestAPhantomPaysAndIsOwed(t *testing.T) {
	w := newWorld(t)
	w.invite(t, w.bob, false)
	nino := w.phantom(t, "Nino")

	// Nino paid 30.00 EUR for the three of them, evenly.
	w.addBill(t, w.alice, map[string]any{
		"description": "Khinkali", "day": spliffserver.Today(), "currency": "EUR",
		"total_minor": 3000,
		"payments":    []map[string]any{entry(nino, 3000)},
		"shares": []map[string]any{
			entry(w.mem(t, "alice"), 1000), entry(w.mem(t, "bob"), 1000), entry(nino, 1000),
		},
	})
	view := w.read(t, w.alice)
	if got := balanceOf(t, view, "Nino"); got != 2000 {
		t.Errorf("Nino = %d, want 2000 (paid 30, owes 10)", got)
	}
	for _, name := range []string{"alice", "bob"} {
		if got := balanceOf(t, view, name); got != -1000 {
			t.Errorf("%s = %d, want -1000", name, got)
		}
	}
	// And the Debt graph says so by name.
	pays := map[string]string{}
	for _, tr := range view.Transfers {
		pays[tr.FromName] = tr.ToName
	}
	for _, name := range []string{"alice", "bob"} {
		if pays[name] != "Nino" {
			t.Errorf("%s pays %q, want Nino", name, pays[name])
		}
	}
}

// The zero-balance rule holds for a Phantom exactly as for a person: the Owner
// may not quietly delete one that is owed money.
func TestAPhantomCannotBeRemovedWhileItIsOwed(t *testing.T) {
	w := newWorld(t)
	w.invite(t, w.bob, false)
	nino := w.phantom(t, "Nino")
	w.addBill(t, w.alice, map[string]any{
		"description": "Wine", "day": spliffserver.Today(), "currency": "EUR",
		"total_minor": 1000,
		"payments":    []map[string]any{entry(nino, 1000)},
		"shares":      []map[string]any{entry(w.mem(t, "bob"), 1000)},
	})
	path := w.path("/members/" + strconv.FormatInt(nino, 10))
	resp := w.alice.Do(http.MethodDelete, path, nil)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("removing an owed phantom = %d, want 400", resp.Code)
	}
	if !strings.Contains(resp.Body.String(), "Nino") {
		t.Errorf("the refusal %q does not name Nino", strings.TrimSpace(resp.Body.String()))
	}

	// An empty one goes, through the same route anybody is removed by.
	gio := w.phantom(t, "Gio")
	w.alice.JSON(http.MethodDelete, w.path("/members/"+strconv.FormatInt(gio, 10)), nil, nil)
	for _, m := range w.read(t, w.alice).Members {
		if m.Name == "Gio" {
			t.Error("Gio is still a member")
		}
	}
}

// The whole point: a real person joins claiming the Phantom, everything it held
// becomes theirs, and no balance in the Group moves.
func TestClaimingAPhantomOnJoinMovesNoBalance(t *testing.T) {
	w := newWorld(t)
	nino := w.phantom(t, "Nino")
	bill := w.addBill(t, w.alice, map[string]any{
		"description": "Khinkali", "day": spliffserver.Today(), "currency": "EUR",
		"total_minor": 3000,
		"payments":    []map[string]any{entry(nino, 3000)},
		"shares":      []map[string]any{entry(w.mem(t, "alice"), 1000), entry(nino, 2000)},
	})
	before := w.read(t, w.alice)
	beforeBalances := map[string]int64{}
	for _, m := range before.Members {
		beforeBalances[m.Name] = m.BalanceMinor
	}
	if beforeBalances["Nino"] != 1000 || beforeBalances["alice"] != -1000 {
		t.Fatalf("balances before the claim: %+v", beforeBalances)
	}

	code := w.mintInvite(t, false)
	// The peek offers the Phantoms by name, which is what the join page shows.
	var peek struct {
		Phantoms []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"phantoms"`
	}
	w.bob.JSON(http.MethodGet, "/api/invites/code/"+code, nil, &peek)
	if len(peek.Phantoms) != 1 || peek.Phantoms[0].ID != nino || peek.Phantoms[0].Name != "Nino" {
		t.Fatalf("the peek offers %+v, want Nino", peek.Phantoms)
	}

	w.bob.JSON(http.MethodPost, "/api/invites/code/"+code+"/join",
		map[string]any{"claim": nino}, nil)

	after := w.read(t, w.alice)
	// One member row fewer than a plain join would have made: bob took Nino's.
	if len(after.Members) != 2 {
		t.Fatalf("members after the claim = %d, want 2: %+v", len(after.Members), after.Members)
	}
	var bobRow *memberView
	for i := range after.Members {
		if after.Members[i].Name == "bob" {
			bobRow = &after.Members[i]
		}
	}
	if bobRow == nil {
		t.Fatalf("bob is not a member: %+v", after.Members)
	}
	if bobRow.ID != nino {
		t.Errorf("bob sits on row %d, want Nino's %d", bobRow.ID, nino)
	}
	if bobRow.IsPhantom || bobRow.UserID != w.bob.UserID {
		t.Errorf("bob = %+v, want a real account on that row", *bobRow)
	}
	// The arithmetic is untouched: what was Nino's is bob's, to the minor unit.
	if got := balanceOf(t, after, "bob"); got != beforeBalances["Nino"] {
		t.Errorf("bob = %d after claiming, want Nino's %d", got, beforeBalances["Nino"])
	}
	if got := balanceOf(t, after, "alice"); got != beforeBalances["alice"] {
		t.Errorf("alice = %d after the claim, want %d", got, beforeBalances["alice"])
	}

	// The Payments, the Shares and the History came with it — they name the
	// member row, which is why nothing had to be rewritten.
	var view struct {
		Transaction struct {
			Payments []struct {
				MemberID int64  `json:"member_id"`
				Name     string `json:"name"`
				Minor    int64  `json:"minor"`
			} `json:"payments"`
			Shares []struct {
				MemberID int64  `json:"member_id"`
				Name     string `json:"name"`
				Minor    int64  `json:"minor"`
			} `json:"shares"`
		} `json:"transaction"`
		History []struct {
			Kind string `json:"kind"`
		} `json:"history"`
	}
	w.bob.JSON(http.MethodGet, "/api/transactions/"+strconv.FormatInt(bill, 10), nil, &view)
	if len(view.Transaction.Payments) != 1 ||
		view.Transaction.Payments[0].MemberID != nino ||
		view.Transaction.Payments[0].Name != "bob" ||
		view.Transaction.Payments[0].Minor != 3000 {
		t.Errorf("the payment is %+v, want bob on Nino's row for 3000", view.Transaction.Payments)
	}
	var bobShare int64
	for _, sh := range view.Transaction.Shares {
		if sh.MemberID == nino {
			bobShare = sh.Minor
		}
	}
	if bobShare != 2000 {
		t.Errorf("bob's share = %d, want Nino's 2000", bobShare)
	}
	if len(view.History) == 0 {
		t.Error("the history did not survive the claim")
	}
}

// Every way "I am <phantom>" can be wrong.
func TestClaimingAPhantomIsRefusedWhenItCannotBeMeant(t *testing.T) {
	w := newWorld(t)
	nino := w.phantom(t, "Nino")

	// Somebody already in the Group has nobody to become.
	w.invite(t, w.bob, false)
	code := w.mintInvite(t, false)
	resp := w.bob.Do(http.MethodPost, "/api/invites/code/"+code+"/join",
		map[string]any{"claim": nino})
	if resp.Code != http.StatusBadRequest {
		t.Errorf("a member claiming = %d, want 400", resp.Code)
	}

	// A member row that is a real person is not a Phantom.
	resp = w.carol.Do(http.MethodPost, "/api/invites/code/"+code+"/join",
		map[string]any{"claim": w.mem(t, "alice")})
	if resp.Code != http.StatusBadRequest {
		t.Errorf("claiming a person = %d, want 400", resp.Code)
	}
	// Nor is a row from no Group at all.
	resp = w.carol.Do(http.MethodPost, "/api/invites/code/"+code+"/join",
		map[string]any{"claim": nino + 1000})
	if resp.Code != http.StatusBadRequest {
		t.Errorf("claiming nothing = %d, want 400", resp.Code)
	}
	// And a refused claim seats nobody.
	if got := len(w.read(t, w.alice).Members); got != 3 {
		t.Fatalf("members = %d after three refusals, want 3", got)
	}

	// A link that waits for the owner's approval cannot carry a claim: the
	// decision comes later and there is nowhere to keep it.
	held := w.mintInvite(t, true)
	resp = w.carol.Do(http.MethodPost, "/api/invites/code/"+held+"/join",
		map[string]any{"claim": nino})
	if resp.Code != http.StatusBadRequest {
		t.Errorf("claiming through an approval link = %d, want 400", resp.Code)
	}

	// Carol may still join as herself, and Nino stays a Phantom.
	w.carol.JSON(http.MethodPost, "/api/invites/code/"+code+"/join",
		map[string]any{"claim": 0}, nil)
	for _, m := range w.read(t, w.alice).Members {
		if m.Name == "Nino" && !m.IsPhantom {
			t.Error("Nino stopped being a phantom")
		}
	}
}

// Two people cannot both become Nino.
func TestAPhantomIsClaimedOnce(t *testing.T) {
	w := newWorld(t)
	nino := w.phantom(t, "Nino")
	code := w.mintInvite(t, false)
	w.bob.JSON(http.MethodPost, "/api/invites/code/"+code+"/join",
		map[string]any{"claim": nino}, nil)
	resp := w.carol.Do(http.MethodPost, "/api/invites/code/"+code+"/join",
		map[string]any{"claim": nino})
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("the second claim = %d, want 400", resp.Code)
	}
	if got := len(w.read(t, w.alice).Members); got != 2 {
		t.Errorf("members = %d, want 2 — the refused claim seated nobody", got)
	}
}
