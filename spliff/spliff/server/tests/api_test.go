package tests

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"pecheny.me/dopecore/authcred"

	spliffserver "spliff/spliff/server"
)

// The HTTP half of Spliff, driven the way a browser drives it: a session
// cookie, a same-origin header, and JSON both ways. The arithmetic is the
// domain packages' own tests; what is checked here is that the server reads
// what it was sent, refuses what it must, and writes History every time.

// fixtureRates is round enough to check by hand: one USD buys two GEL and half
// a euro.
var fixtureRates = map[string]string{
	"USD": "1", "EUR": "0.5", "GEL": "2.5", "JPY": "150", "KWD": "0.25",
}

type world struct {
	ts    *spliffserver.TestServer
	alice *spliffserver.Client
	bob   *spliffserver.Client
	carol *spliffserver.Client
	group int64
}

func newWorld(t *testing.T) *world {
	t.Helper()
	ts := spliffserver.NewTestServer(t, t.TempDir())
	ts.SeedRates(spliffserver.Today(), fixtureRates)
	hash, err := authcred.HashPassword("hunter2hunter2")
	if err != nil {
		t.Fatal(err)
	}
	w := &world{ts: ts}
	w.alice = ts.As(ts.AddUser("alice", hash))
	w.bob = ts.As(ts.AddUser("bob", hash))
	w.carol = ts.As(ts.AddUser("carol", hash))
	var created struct {
		ID int64 `json:"id"`
	}
	w.alice.JSON(http.MethodPost, "/api/groups", map[string]any{
		"name": "Tbilisi", "base_currency": "EUR",
	}, &created)
	w.group = created.ID
	return w
}

// invite mints a link as the Owner and walks somebody through it, which is the
// only way into a Group.
func (w *world) invite(t *testing.T, joiner *spliffserver.Client, approval bool) {
	t.Helper()
	var link struct {
		Code string `json:"code"`
	}
	w.alice.JSON(http.MethodPost, w.path("/invites"), map[string]any{
		"label": "the trip", "max_uses": 0, "ttl_hours": 0, "requires_approval": approval,
	}, &link)
	var result struct {
		GroupID int64  `json:"group_id"`
		State   string `json:"state"`
	}
	joiner.JSON(http.MethodPost, "/api/invites/code/"+link.Code+"/join", nil, &result)
	if result.GroupID != w.group {
		t.Fatalf("joined group %d, want %d", result.GroupID, w.group)
	}
	if approval {
		if result.State != "pending" {
			t.Fatalf("join state = %q, want pending", result.State)
		}
		w.alice.JSON(http.MethodPost,
			w.path("/join-requests/"+strconv.FormatInt(joiner.UserID, 10)),
			map[string]any{"decision": "approve"}, nil)
		return
	}
	if result.State != "member" {
		t.Fatalf("join state = %q, want member", result.State)
	}
}

func (w *world) path(suffix string) string {
	return "/api/groups/" + strconv.FormatInt(w.group, 10) + suffix
}

type memberView struct {
	UserID       int64  `json:"user_id"`
	Name         string `json:"name"`
	IsOwner      bool   `json:"is_owner"`
	BalanceMinor int64  `json:"balance_minor"`
	Balance      string `json:"balance"`
}

type transferView struct {
	FromName string `json:"from_name"`
	ToName   string `json:"to_name"`
	Minor    int64  `json:"minor"`
	Amount   string `json:"amount"`
}

type txView struct {
	ID             int64  `json:"id"`
	Description    string `json:"description"`
	Currency       string `json:"currency"`
	TotalMinor     int64  `json:"total_minor"`
	UnclaimedMinor int64  `json:"unclaimed_minor"`
	RateDate       string `json:"rate_date"`
	InBase         string `json:"in_base"`
	Deleted        bool   `json:"deleted"`
	Photos         []struct {
		ID  int64  `json:"id"`
		URL string `json:"url"`
	} `json:"photos"`
}

type groupView struct {
	Name      string         `json:"name"`
	Base      string         `json:"base_currency"`
	IsOwner   bool           `json:"is_owner"`
	Members   []memberView   `json:"members"`
	Transfers []transferView `json:"transfers"`
	Live      []txView       `json:"live"`
	Deleted   []txView       `json:"deleted"`
	History   []struct {
		Kind  string `json:"kind"`
		Actor string `json:"actor"`
	} `json:"history"`
}

func (w *world) read(t *testing.T, as *spliffserver.Client) groupView {
	t.Helper()
	var out groupView
	as.JSON(http.MethodGet, w.path(""), nil, &out)
	return out
}

func balanceOf(t *testing.T, view groupView, name string) int64 {
	t.Helper()
	for _, m := range view.Members {
		if m.Name == name {
			return m.BalanceMinor
		}
	}
	t.Fatalf("%s is not a member: %+v", name, view.Members)
	return 0
}

func entry(member int64, minor int64) map[string]any {
	return map[string]any{"member_id": member, "minor": minor}
}

func (w *world) addBill(t *testing.T, as *spliffserver.Client, body map[string]any) int64 {
	t.Helper()
	var created struct {
		ID int64 `json:"id"`
	}
	as.JSON(http.MethodPost, w.path("/transactions"), body, &created)
	return created.ID
}

// The whole flow the spec describes, end to end: a Group, a second Member
// through an Invite Link, a two-payer bill with something Unclaimed, a Claim by
// the other Member, the Debt graph, and a Settlement that zeroes the pair.
func TestTwoPayersUnclaimedClaimAndSettle(t *testing.T) {
	w := newWorld(t)
	w.invite(t, w.bob, false)

	// 90.00 EUR: Alice paid 60, Bob paid 30, and nobody has claimed anything.
	// The payers absorb the whole of it pro rata, so everybody is level.
	bill := w.addBill(t, w.alice, map[string]any{
		"description": "Dinner", "day": spliffserver.Today(), "currency": "EUR",
		"total_minor": 9000,
		"payments":    []map[string]any{entry(w.alice.UserID, 6000), entry(w.bob.UserID, 3000)},
		"shares":      []map[string]any{},
	})
	view := w.read(t, w.alice)
	if got := view.Live[0].UnclaimedMinor; got != 9000 {
		t.Fatalf("unclaimed = %d, want 9000", got)
	}
	for _, name := range []string{"alice", "bob"} {
		if got := balanceOf(t, view, name); got != 0 {
			t.Errorf("%s = %d before anybody claims, want 0", name, got)
		}
	}

	// Bob claims 30.00 of it. Alice is then owed her share of what is left:
	// the remaining 60.00 Unclaimed splits 2:1 with her paying 60 of the 90, so
	// she absorbs 40.00 and is owed 20.00; Bob absorbed 20.00 and claimed 30.00
	// against a 30.00 Payment, so he owes 20.00.
	w.bob.JSON(http.MethodPut, "/api/transactions/"+strconv.FormatInt(bill, 10), map[string]any{
		"description": "Dinner", "day": spliffserver.Today(), "currency": "EUR",
		"total_minor": 9000,
		"payments":    []map[string]any{entry(w.alice.UserID, 6000), entry(w.bob.UserID, 3000)},
		"shares":      []map[string]any{entry(w.bob.UserID, 3000)},
	}, nil)

	view = w.read(t, w.bob)
	if got := balanceOf(t, view, "alice"); got != 2000 {
		t.Errorf("alice = %d, want 2000", got)
	}
	if got := balanceOf(t, view, "bob"); got != -2000 {
		t.Errorf("bob = %d, want -2000", got)
	}
	if len(view.Transfers) != 1 {
		t.Fatalf("transfers = %+v, want one line", view.Transfers)
	}
	if tr := view.Transfers[0]; tr.FromName != "bob" || tr.ToName != "alice" || tr.Amount != "20.00" {
		t.Errorf("transfer = %+v, want bob pays alice 20.00", tr)
	}

	// Bob settles up: one Payment and one Share, the whole amount each.
	w.addBill(t, w.bob, map[string]any{
		"description": "Settling up", "day": spliffserver.Today(), "currency": "EUR",
		"total_minor": 2000,
		"payments":    []map[string]any{entry(w.bob.UserID, 2000)},
		"shares":      []map[string]any{entry(w.alice.UserID, 2000)},
	})
	view = w.read(t, w.alice)
	for _, name := range []string{"alice", "bob"} {
		if got := balanceOf(t, view, name); got != 0 {
			t.Errorf("%s = %d after the settlement, want 0", name, got)
		}
	}
	if len(view.Transfers) != 0 {
		t.Errorf("transfers = %+v, want none", view.Transfers)
	}
}

// A Transaction in another currency is shown in its own and restated in the
// Group's, at the Rate table its date resolves to.
func TestConversionUsesTheRateDate(t *testing.T) {
	w := newWorld(t)
	w.addBill(t, w.alice, map[string]any{
		"description": "Taxi", "day": spliffserver.Today(), "currency": "GEL",
		"total_minor": 5000,
		"payments":    []map[string]any{entry(w.alice.UserID, 5000)},
		"shares":      []map[string]any{},
	})
	view := w.read(t, w.alice)
	tx := view.Live[0]
	if tx.RateDate != spliffserver.Today() {
		t.Errorf("rate date = %q, want today's table", tx.RateDate)
	}
	// 50.00 GEL at 2.5 GEL to the dollar is 20 USD is 10.00 EUR.
	if tx.InBase != "10.00" {
		t.Errorf("in base = %q, want 10.00", tx.InBase)
	}
}

// Changing the Base currency restates every balance and rewrites nothing.
func TestBaseCurrencyChangeIsAReRead(t *testing.T) {
	w := newWorld(t)
	w.invite(t, w.bob, false)
	w.addBill(t, w.alice, map[string]any{
		"description": "Hotel", "day": spliffserver.Today(), "currency": "EUR",
		"total_minor": 2000,
		"payments":    []map[string]any{entry(w.alice.UserID, 2000)},
		"shares":      []map[string]any{entry(w.bob.UserID, 1000), entry(w.alice.UserID, 1000)},
	})
	if got := balanceOf(t, w.read(t, w.alice), "alice"); got != 1000 {
		t.Fatalf("alice in EUR = %d, want 1000", got)
	}
	w.alice.JSON(http.MethodPatch, w.path(""), map[string]any{"base_currency": "GEL"}, nil)
	view := w.read(t, w.alice)
	if view.Base != "GEL" {
		t.Fatalf("base = %q", view.Base)
	}
	// 1 EUR is 5 GEL, so 10.00 EUR is 50.00 GEL.
	if got := balanceOf(t, view, "alice"); got != 5000 {
		t.Errorf("alice in GEL = %d, want 5000", got)
	}
	if got := balanceOf(t, view, "bob"); got != -5000 {
		t.Errorf("bob in GEL = %d, want -5000", got)
	}
}

// Every write validates the same rules, and every refusal names the number that
// is wrong.
func TestWriteRefusals(t *testing.T) {
	w := newWorld(t)
	w.invite(t, w.bob, false)
	base := func(over map[string]any) map[string]any {
		body := map[string]any{
			"description": "Lunch", "day": spliffserver.Today(), "currency": "EUR",
			"total_minor": 1000,
			"payments":    []map[string]any{entry(w.alice.UserID, 1000)},
			"shares":      []map[string]any{},
		}
		for k, v := range over {
			body[k] = v
		}
		return body
	}
	cases := []struct {
		name string
		body map[string]any
		says string
	}{
		{"payments that do not add up", base(map[string]any{
			"payments": []map[string]any{entry(w.alice.UserID, 900)},
		}), "9.00"},
		{"shares past the total", base(map[string]any{
			"shares": []map[string]any{entry(w.alice.UserID, 700), entry(w.bob.UserID, 700)},
		}), "4.00"},
		{"two shares for one member", base(map[string]any{
			"shares": []map[string]any{entry(w.bob.UserID, 100), entry(w.bob.UserID, 100)},
		}), "twice"},
		{"somebody who is not a member", base(map[string]any{
			"shares": []map[string]any{entry(w.carol.UserID, 100)},
		}), "member"},
		{"no payer at all", base(map[string]any{
			"payments": []map[string]any{},
		}), "paid"},
		{"a total of nothing", base(map[string]any{
			"total_minor": 0, "payments": []map[string]any{},
		}), "more than nothing"},
		{"no description", base(map[string]any{"description": "   "}), "money was for"},
		{"a date that is not one", base(map[string]any{"day": "the 5th"}), "date"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := w.alice.Do(http.MethodPost, w.path("/transactions"), c.body)
			if resp.Code != http.StatusBadRequest {
				t.Fatalf("code = %d, want 400 (%s)", resp.Code, resp.Body.String())
			}
			if !strings.Contains(resp.Body.String(), c.says) {
				t.Errorf("refusal %q does not name %q", strings.TrimSpace(resp.Body.String()), c.says)
			}
		})
	}
}

// Deleting is soft: the Transaction leaves the ledger, stays in History, and
// any Member restores it with its Shares intact.
func TestSoftDeleteAndRestore(t *testing.T) {
	w := newWorld(t)
	w.invite(t, w.bob, false)
	id := w.addBill(t, w.alice, map[string]any{
		"description": "Museum", "day": spliffserver.Today(), "currency": "EUR",
		"total_minor": 1000,
		"payments":    []map[string]any{entry(w.alice.UserID, 1000)},
		"shares":      []map[string]any{entry(w.bob.UserID, 1000)},
	})
	if got := balanceOf(t, w.read(t, w.alice), "alice"); got != 1000 {
		t.Fatalf("alice = %d before the delete, want 1000", got)
	}

	path := "/api/transactions/" + strconv.FormatInt(id, 10)
	w.bob.JSON(http.MethodDelete, path, nil, nil)
	view := w.read(t, w.alice)
	if len(view.Live) != 0 {
		t.Errorf("a deleted transaction is still in the ledger: %+v", view.Live)
	}
	if len(view.Deleted) != 1 || !view.Deleted[0].Deleted {
		t.Errorf("deleted feed = %+v", view.Deleted)
	}
	if got := balanceOf(t, view, "alice"); got != 0 {
		t.Errorf("alice = %d after the delete, want 0", got)
	}

	w.alice.JSON(http.MethodPost, path+"/restore", nil, nil)
	view = w.read(t, w.alice)
	if len(view.Live) != 1 {
		t.Fatalf("live feed = %+v after the restore", view.Live)
	}
	if got := balanceOf(t, view, "alice"); got != 1000 {
		t.Errorf("alice = %d after the restore, want 1000", got)
	}

	// Four acts, four History entries, each naming who did it.
	var detail struct {
		History []struct {
			Kind  string `json:"kind"`
			Actor string `json:"actor"`
		} `json:"history"`
	}
	w.alice.JSON(http.MethodGet, path, nil, &detail)
	kinds := []string{}
	for _, h := range detail.History {
		kinds = append(kinds, h.Kind+":"+h.Actor)
	}
	want := []string{"created:alice", "deleted:bob", "restored:alice"}
	if strings.Join(kinds, " ") != strings.Join(want, " ") {
		t.Errorf("history = %v, want %v", kinds, want)
	}
}

// Nobody leaves owing, and the refusal names the amount.
func TestLeavingIsRefusedWhileTheBalanceIsNotZero(t *testing.T) {
	w := newWorld(t)
	w.invite(t, w.bob, false)
	id := w.addBill(t, w.alice, map[string]any{
		"description": "Wine", "day": spliffserver.Today(), "currency": "EUR",
		"total_minor": 1000,
		"payments":    []map[string]any{entry(w.alice.UserID, 1000)},
		"shares":      []map[string]any{entry(w.bob.UserID, 1000)},
	})

	resp := w.bob.Do(http.MethodDelete, w.path("/members/me"), nil)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("leaving = %d, want 400", resp.Code)
	}
	if !strings.Contains(resp.Body.String(), "10.00 EUR") {
		t.Errorf("refusal %q does not name the amount", strings.TrimSpace(resp.Body.String()))
	}
	// Nor may the Owner kick him.
	if resp := w.alice.Do(http.MethodDelete,
		w.path("/members/"+strconv.FormatInt(w.bob.UserID, 10)), nil); resp.Code != http.StatusBadRequest {
		t.Errorf("kicking = %d, want 400", resp.Code)
	}
	// Nor delete the Group.
	if resp := w.alice.Do(http.MethodDelete, w.path(""), nil); resp.Code != http.StatusBadRequest {
		t.Errorf("deleting the group = %d, want 400", resp.Code)
	}

	// Once the bill is gone he is level — but he is still named on nothing, so
	// he may go.
	w.alice.JSON(http.MethodDelete, "/api/transactions/"+strconv.FormatInt(id, 10), nil, nil)
	w.bob.JSON(http.MethodDelete, w.path("/members/me"), nil, nil)
	if got := len(w.read(t, w.alice).Members); got != 1 {
		t.Errorf("members = %d after bob left, want 1", got)
	}
}

// A Member who is level but still named on a live Transaction stays: the Debt
// graph must never point at somebody who is not there.
func TestALevelMemberStillNamedCannotLeave(t *testing.T) {
	w := newWorld(t)
	w.invite(t, w.bob, false)
	w.addBill(t, w.alice, map[string]any{
		"description": "Split down the middle", "day": spliffserver.Today(), "currency": "EUR",
		"total_minor": 2000,
		"payments":    []map[string]any{entry(w.alice.UserID, 1000), entry(w.bob.UserID, 1000)},
		"shares":      []map[string]any{entry(w.alice.UserID, 1000), entry(w.bob.UserID, 1000)},
	})
	if got := balanceOf(t, w.read(t, w.alice), "bob"); got != 0 {
		t.Fatalf("bob = %d, want 0", got)
	}
	resp := w.bob.Do(http.MethodDelete, w.path("/members/me"), nil)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("leaving = %d, want 400", resp.Code)
	}
	if !strings.Contains(resp.Body.String(), "live transaction") {
		t.Errorf("refusal %q does not say why", strings.TrimSpace(resp.Body.String()))
	}
}

// The Owner cannot leave without handing the Group on.
func TestTheOwnerHandsOverBeforeLeaving(t *testing.T) {
	w := newWorld(t)
	w.invite(t, w.bob, false)
	if resp := w.alice.Do(http.MethodDelete, w.path("/members/me"), nil); resp.Code != http.StatusBadRequest {
		t.Fatalf("the owner left: %d", resp.Code)
	}
	w.alice.JSON(http.MethodPost, w.path("/owner"), map[string]any{"user_id": w.bob.UserID}, nil)
	view := w.read(t, w.bob)
	if !view.IsOwner {
		t.Error("bob did not become the owner")
	}
	w.alice.JSON(http.MethodDelete, w.path("/members/me"), nil, nil)
}

// Minting, revoking and deciding belong to the Owner alone; joining belongs to
// anybody holding the link.
func TestInviteLinksAreTheOwnersAlone(t *testing.T) {
	w := newWorld(t)
	w.invite(t, w.bob, false)
	if resp := w.bob.Do(http.MethodGet, w.path("/invites"), nil); resp.Code != http.StatusForbidden {
		t.Errorf("a plain member listed the links: %d", resp.Code)
	}
	if resp := w.bob.Do(http.MethodPost, w.path("/invites"), map[string]any{}); resp.Code != http.StatusForbidden {
		t.Errorf("a plain member minted a link: %d", resp.Code)
	}
	// A stranger sees a 404, not a 403: the id is a guessable integer.
	if resp := w.carol.Do(http.MethodGet, w.path(""), nil); resp.Code != http.StatusNotFound {
		t.Errorf("a stranger read the group: %d", resp.Code)
	}
}

// A Join Request waits for the Owner, and an approval seats the person.
func TestJoinRequestNeedsApproval(t *testing.T) {
	w := newWorld(t)
	w.invite(t, w.bob, true)
	names := []string{}
	for _, m := range w.read(t, w.alice).Members {
		names = append(names, m.Name)
	}
	if strings.Join(names, ",") != "alice,bob" {
		t.Errorf("members = %v, want alice and bob", names)
	}
}

// An anonymous visitor at an Invite Link learns the Group's name and nothing
// else, and the page they land on is served to them rather than redirected.
func TestAnonymousSeesTheGroupName(t *testing.T) {
	w := newWorld(t)
	var link struct {
		Code string `json:"code"`
	}
	w.alice.JSON(http.MethodPost, w.path("/invites"), map[string]any{}, &link)

	anon := w.ts.Anonymous()
	var peek struct {
		GroupName string `json:"group_name"`
		State     string `json:"state"`
	}
	anon.JSON(http.MethodGet, "/api/invites/code/"+link.Code+"/public", nil, &peek)
	if peek.GroupName != "Tbilisi" || peek.State != "active" {
		t.Errorf("peek = %+v", peek)
	}
	if resp := anon.Do(http.MethodGet, "/join/"+link.Code, nil); resp.Code != http.StatusOK {
		t.Errorf("the join page answered %d to a logged-out visitor", resp.Code)
	}
	// Whereas a page of the Group itself sends them to /login carrying where
	// they were going.
	resp := anon.Do(http.MethodGet, "/group/"+strconv.FormatInt(w.group, 10), nil)
	if resp.Code != http.StatusSeeOther {
		t.Fatalf("the group page answered %d, want 303", resp.Code)
	}
	if got := resp.Header().Get("Location"); !strings.Contains(got, "next=") {
		t.Errorf("Location = %q, want a next=", got)
	}
}

// A Photo is re-encoded, never stored as sent, and served to Members only.
func TestPhotoIsReencodedAndMembersOnly(t *testing.T) {
	w := newWorld(t)
	id := w.addBill(t, w.alice, map[string]any{
		"description": "Receipt", "day": spliffserver.Today(), "currency": "EUR",
		"total_minor": 1000,
		"payments":    []map[string]any{entry(w.alice.UserID, 1000)},
		"shares":      []map[string]any{},
	})
	path := "/api/transactions/" + strconv.FormatInt(id, 10) + "/photos"

	if resp := w.alice.Upload(path, "photo", "notes.txt",
		[]byte("this is a text file, not a receipt")); resp.Code != http.StatusBadRequest {
		t.Errorf("a text file was accepted as a photo: %d", resp.Code)
	}

	resp := w.alice.Upload(path, "photo", "receipt.png", tinyPNG())
	if resp.Code != http.StatusOK {
		t.Fatalf("upload = %d: %s", resp.Code, resp.Body.String())
	}
	view := w.read(t, w.alice)
	if len(view.Live[0].Photos) != 1 {
		t.Fatalf("photos = %+v", view.Live[0].Photos)
	}
	url := view.Live[0].Photos[0].URL

	got := w.alice.Do(http.MethodGet, url, nil)
	if got.Code != http.StatusOK {
		t.Fatalf("serving = %d", got.Code)
	}
	if ct := got.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("content type = %q, want image/jpeg — a stored Photo is always our own JPEG", ct)
	}
	if got.Body.Len() == 0 {
		t.Error("the photo came back empty")
	}
	// Somebody who is not in the Group gets a 404, not a 403: the id is a
	// guessable integer.
	if outsider := w.carol.Do(http.MethodGet, url, nil); outsider.Code != http.StatusNotFound {
		t.Errorf("a stranger read the photo: %d", outsider.Code)
	}
}

// A 1×1 PNG, as the smallest thing that is really a picture.
func tinyPNG() []byte {
	return []byte{
		0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a,
		0, 0, 0, 0x0d, 'I', 'H', 'D', 'R',
		0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0,
		0x1f, 0x15, 0xc4, 0x89,
		0, 0, 0, 0x0a, 'I', 'D', 'A', 'T',
		0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00, 0x05, 0x00, 0x01,
		0x0d, 0x0a, 0x2d, 0xb4,
		0, 0, 0, 0, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82,
	}
}

// The pages a browser asks for all compile and answer.
func TestPagesServe(t *testing.T) {
	w := newWorld(t)
	for _, path := range []string{
		"/",
		"/login",
		"/login?next=%2Fjoin%2FABC",
		"/group/" + strconv.FormatInt(w.group, 10),
		"/group/" + strconv.FormatInt(w.group, 10) + "/new",
	} {
		resp := w.alice.Do(http.MethodGet, path, nil)
		if resp.Code != http.StatusOK {
			t.Errorf("GET %s = %d", path, resp.Code)
			continue
		}
		if ct := resp.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("GET %s content type = %q", path, ct)
		}
		if got := resp.Header().Get("Content-Security-Policy"); got == "" {
			t.Errorf("GET %s carries no CSP", path)
		}
		// Spliff is English-only, and the page says so — which is also what
		// picks the English Catalog for the shared login and menu scripts.
		if !strings.Contains(resp.Body.String(), `lang="en"`) {
			t.Errorf("GET %s does not declare lang=en", path)
		}
	}
}

// The login page honours a `next` that names a page of ours, and nothing else.
func TestLoginNextIsSameSiteOnly(t *testing.T) {
	w := newWorld(t)
	anon := w.ts.Anonymous()
	body := anon.Do(http.MethodGet, "/login?next=%2Fjoin%2FABC", nil).Body.String()
	if !strings.Contains(body, `data-login-redirect="/join/ABC"`) {
		t.Error("the login page did not carry the destination")
	}
	body = anon.Do(http.MethodGet, "/login?next=https%3A%2F%2Fevil.test%2F", nil).Body.String()
	if strings.Contains(body, "evil.test") {
		t.Error("the login page carried a foreign destination")
	}
}
