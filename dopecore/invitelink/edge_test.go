package invitelink

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

var texts = Texts{
	Revoked: "revoked", Expired: "expired", Exhausted: "exhausted", Declined: "declined", Spent: "spent",
	Broken: "broken", NotFound: "not found", RequestNotFound: "no request", NoSeatsLeft: "no seats",
	LabelTooLong: "label", LimitsOutOfRange: "limits", DecisionInvalid: "decision",
}

func TestAnswer(t *testing.T) {
	cases := []struct {
		err    error
		status int
		msg    string
	}{
		{ErrNotFound, http.StatusNotFound, "not found"},
		{fmt.Errorf("wrapped: %w", ErrNotFound), http.StatusNotFound, "not found"},
		{ErrRequestNotFound, http.StatusBadRequest, "no request"},
		{ErrNoSeatsLeft, http.StatusBadRequest, "no seats"},
		{ErrLabelTooLong, http.StatusBadRequest, "label"},
		{ErrLimitsOutOfRange, http.StatusBadRequest, "limits"},
		{ErrDecisionInvalid, http.StatusBadRequest, "decision"},
		{&Refused{State: Revoked}, http.StatusBadRequest, "revoked"},
		{&Refused{State: Expired}, http.StatusBadRequest, "expired"},
		{&Refused{State: Exhausted}, http.StatusBadRequest, "exhausted"},
		{&Refused{State: Declined}, http.StatusBadRequest, "declined"},
		{&Refused{State: Spent}, http.StatusBadRequest, "spent"},
		{&Refused{State: State("new-one")}, http.StatusBadRequest, "broken"},
	}
	for _, c := range cases {
		a, ok := texts.Answer(c.err)
		if !ok || a.Status != c.status || a.Msg != c.msg {
			t.Errorf("%v: %+v %v, want %d %q", c.err, a, ok, c.status, c.msg)
		}
	}
	for _, err := range []error{nil, errors.New("disk on fire")} {
		if a, ok := texts.Answer(err); ok {
			t.Errorf("%v is not the package's, got %+v", err, a)
		}
	}
}

func TestMintRequestOptions(t *testing.T) {
	var req MintRequest
	if err := json.Unmarshal([]byte(`{"label":"trip","max_uses":3,"ttl_hours":24,"requires_approval":true}`), &req); err != nil {
		t.Fatal(err)
	}
	opt, err := req.Options()
	if err != nil || opt != (Options{Label: "trip", MaxUses: 3, TTLHours: 24, RequiresApproval: true}) {
		t.Fatalf("%+v %v", opt, err)
	}
	if _, err := (MintRequest{MaxUses: -1}).Options(); !errors.Is(err, ErrLimitsOutOfRange) {
		t.Errorf("negative uses: %v", err)
	}
	if _, err := (MintRequest{Label: strings.Repeat("я", MaxLabelRunes+1)}).Options(); !errors.Is(err, ErrLabelTooLong) {
		t.Errorf("long label: %v", err)
	}
}

func TestDecideRequestApprove(t *testing.T) {
	for decision, want := range map[string]bool{"approve": true, "decline": false} {
		got, err := DecideRequest{Decision: decision}.Approve()
		if err != nil || got != want {
			t.Errorf("%s: %v %v", decision, got, err)
		}
	}
	if _, err := (DecideRequest{Decision: "maybe"}).Approve(); !errors.Is(err, ErrDecisionInvalid) {
		t.Errorf("maybe: %v", err)
	}
}

// The view encodes the way both frontends read it: empty lists as [], no
// expiry left out, an uncapped link's limits as null.
func TestViewWire(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	lk := Link{ID: 7, Code: "abc", CreatedAt: "2026-10-01T00:00:00Z",
		Joined: []Person{{UserID: 2, Name: "ann", At: "2026-10-02T00:00:00Z"}}}
	got, err := json.Marshal(Views([]Link{lk}, now))
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"id":7,"code":"abc","label":"","created_at":"2026-10-01T00:00:00Z","max_uses":null,"used":0,"left":null,` +
		`"requires_approval":false,"state":"active","joined":[{"user_id":2,"name":"ann","at":"2026-10-02T00:00:00Z"}],"pending":[]}]`
	if string(got) != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if got, _ := json.Marshal(Views(nil, now)); string(got) != "[]" {
		t.Fatalf("no links: %s", got)
	}
}
