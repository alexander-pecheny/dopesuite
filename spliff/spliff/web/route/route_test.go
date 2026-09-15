package route

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"pecheny.me/dopecore/session"
)

// The access matrix. Every level × every kind of caller, once, so that the one
// place a role is decided is also the one place it is checked.

const (
	ownerID   = int64(1)
	memberID  = int64(2)
	strangerN = int64(3)
)

func testDeps(user int64) Deps {
	return Deps{
		Session: func(_ http.ResponseWriter, _ *http.Request) (session.User, bool) {
			if user == 0 {
				return session.User{}, false
			}
			return session.User{UserID: user}, true
		},
		Membership: func(_ context.Context, groupID, userID int64) (bool, bool, error) {
			if groupID != 7 {
				return false, false, nil // no such Group
			}
			switch userID {
			case ownerID:
				return true, true, nil
			case memberID:
				return true, false, nil
			}
			return false, false, nil
		},
		GroupOfTransaction: func(_ context.Context, txID int64) (int64, error) {
			if txID == 42 {
				return 7, nil
			}
			return 0, nil
		},
		WriteError: func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		},
	}
}

func serve(t *testing.T, user int64, access Access, method, target string) (int, Scope) {
	t.Helper()
	mux := http.NewServeMux()
	var got Scope
	table := New(mux, testDeps(user), DenyAPI)
	table.Handle(method+" /group/{group}", access, func(w http.ResponseWriter, _ *http.Request, sc Scope) error {
		got = sc
		w.WriteHeader(http.StatusOK)
		return nil
	})
	table.Handle(method+" /transaction/{tx}", access, func(w http.ResponseWriter, _ *http.Request, sc Scope) error {
		got = sc
		w.WriteHeader(http.StatusOK)
		return nil
	})
	table.Handle(method+" /plain", access, func(w http.ResponseWriter, _ *http.Request, sc Scope) error {
		got = sc
		w.WriteHeader(http.StatusOK)
		return nil
	})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(method, target, nil))
	return w.Code, got
}

func TestAccessMatrix(t *testing.T) {
	cases := []struct {
		name   string
		user   int64
		access Access
		target string
		want   int
	}{
		{"public admits a stranger", 0, Public, "/plain", http.StatusOK},
		{"logged-in refuses nobody", 0, LoggedIn, "/plain", http.StatusUnauthorized},
		{"logged-in admits anybody with a session", strangerN, LoggedIn, "/plain", http.StatusOK},

		{"member admits a member", memberID, Member, "/group/7", http.StatusOK},
		{"member admits the owner", ownerID, Member, "/group/7", http.StatusOK},
		{"member refuses a stranger as a 404", strangerN, Member, "/group/7", http.StatusNotFound},
		{"member refuses nobody with a 401", 0, Member, "/group/7", http.StatusUnauthorized},

		{"owner admits the owner", ownerID, Owner, "/group/7", http.StatusOK},
		{"owner refuses a plain member", memberID, Owner, "/group/7", http.StatusForbidden},
		{"owner refuses a stranger as a 404", strangerN, Owner, "/group/7", http.StatusNotFound},

		// A Group that does not exist and one you are not in answer the same:
		// the id is a guessable integer, and telling them apart would say which
		// Groups exist.
		{"a missing group is a 404", memberID, Member, "/group/9", http.StatusNotFound},
		{"a non-numeric group is a 404", memberID, Member, "/group/abc", http.StatusNotFound},

		{"a transaction resolves its group", memberID, Member, "/transaction/42", http.StatusOK},
		{"a missing transaction is a 404", memberID, Member, "/transaction/43", http.StatusNotFound},
		{"a stranger cannot reach a transaction", strangerN, Member, "/transaction/42", http.StatusNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, _ := serve(t, c.user, c.access, http.MethodGet, c.target)
			if code != c.want {
				t.Errorf("GET %s as %d = %d, want %d", c.target, c.user, code, c.want)
			}
		})
	}
}

func TestScopeIsResolvedBeforeTheHandler(t *testing.T) {
	_, sc := serve(t, ownerID, Member, http.MethodGet, "/group/7")
	if sc.GroupID != 7 || !sc.IsOwner || sc.User.UserID != ownerID {
		t.Errorf("scope = %+v, want group 7 owned by %d", sc, ownerID)
	}
	_, sc = serve(t, memberID, Member, http.MethodGet, "/transaction/42")
	if sc.TxID != 42 || sc.GroupID != 7 || sc.IsOwner {
		t.Errorf("scope = %+v, want transaction 42 in group 7, not owned", sc)
	}
}

func TestSameOriginGuardsWrites(t *testing.T) {
	mux := http.NewServeMux()
	table := New(mux, testDeps(memberID), DenyAPI)
	table.Handle("POST /group/{group}", Member, func(w http.ResponseWriter, _ *http.Request, _ Scope) error {
		w.WriteHeader(http.StatusOK)
		return nil
	})
	for _, c := range []struct {
		origin string
		want   int
	}{
		{"", http.StatusOK},
		{"http://example.test", http.StatusOK},
		{"http://evil.test", http.StatusForbidden},
	} {
		r := httptest.NewRequest(http.MethodPost, "http://example.test/group/7", nil)
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != c.want {
			t.Errorf("Origin %q = %d, want %d", c.origin, w.Code, c.want)
		}
	}
}

// A read is never cross-origin-checked: a GET carries no side effect, and a
// browser sends an Origin on a plain navigation too.
func TestSameOriginIgnoresReads(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://example.test/group/7", nil)
	r.Header.Set("Origin", "http://evil.test")
	if !SameOriginUnsafe(r) {
		t.Error("a GET was refused for its Origin")
	}
}

// A logged-out visitor who followed an Invite Link must land back on it after
// logging in — which is the whole reason the page table has its own refusal.
func TestDenyPageCarriesWhereTheyWereGoing(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/join/ABC123", nil)
	DenyPage(w, r, NoSession)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("code = %d, want 303", w.Code)
	}
	if got := w.Header().Get("Location"); !strings.Contains(got, "next=%2Fjoin%2FABC123") {
		t.Errorf("Location = %q, want it to carry /join/ABC123", got)
	}
}

func TestStatusErrorsCarryTheirCode(t *testing.T) {
	for _, c := range []struct {
		err  error
		want int
	}{
		{NotFound("gone"), http.StatusNotFound},
		{Forbidden("no"), http.StatusForbidden},
		{Conflict("busy"), http.StatusConflict},
	} {
		st, ok := AsStatus(c.err)
		if !ok || st.Code != c.want {
			t.Errorf("AsStatus(%v) = %+v, %v; want code %d", c.err, st, ok, c.want)
		}
	}
	if _, ok := AsStatus(context.Canceled); ok {
		t.Error("a plain error reported a status")
	}
}
