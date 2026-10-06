package invitelink

import (
	"errors"
	"net/http"
	"time"
)

// The HTTP edge both apps share: the bodies their frontends send, the JSON an
// owner's list is drawn from, and what each of the package's answers becomes
// on the wire. This is the shape tgbot.LoginHandler settled on: the app passes
// its words in Texts and keeps its routes, its Scope and its peek and join
// responses, which name the scope in the app's own terms.

// ErrDecisionInvalid is a decision that is neither approve nor decline.
var ErrDecisionInvalid = errors.New("decision must be approve or decline")

// Texts is every sentence the edge may say, from the app's catalog.
type Texts struct {
	// Why a dead link refuses the person holding it, one per State.
	Revoked, Expired, Exhausted, Declined, Spent string
	// Broken answers a refusal in a State the app has no sentence for.
	Broken string

	NotFound         string // ErrNotFound
	RequestNotFound  string // ErrRequestNotFound
	NoSeatsLeft      string // ErrNoSeatsLeft
	LabelTooLong     string // ErrLabelTooLong
	LimitsOutOfRange string // ErrLimitsOutOfRange
	DecisionInvalid  string // ErrDecisionInvalid
}

// Answer is the status and the sentence an app's edge replies with.
type Answer struct {
	Status int
	Msg    string
}

// Answer maps one of the package's errors to its reply: a link that does not
// exist is a 404, everything else a 400 the person can act on. ok is false for
// an error that is not the package's, which the app handles as it handles any
// other.
func (t Texts) Answer(err error) (a Answer, ok bool) {
	var refused *Refused
	switch {
	case err == nil:
		return Answer{}, false
	case errors.Is(err, ErrNotFound):
		return Answer{http.StatusNotFound, t.NotFound}, true
	case errors.As(err, &refused):
		return Answer{http.StatusBadRequest, t.Refusal(refused.State)}, true
	}
	for _, c := range []struct {
		err error
		msg string
	}{
		{ErrRequestNotFound, t.RequestNotFound},
		{ErrNoSeatsLeft, t.NoSeatsLeft},
		{ErrLabelTooLong, t.LabelTooLong},
		{ErrLimitsOutOfRange, t.LimitsOutOfRange},
		{ErrDecisionInvalid, t.DecisionInvalid},
	} {
		if errors.Is(err, c.err) {
			return Answer{http.StatusBadRequest, c.msg}, true
		}
	}
	return Answer{}, false
}

// Refusal words a dead link for the person holding it.
func (t Texts) Refusal(s State) string {
	switch s {
	case Revoked:
		return t.Revoked
	case Expired:
		return t.Expired
	case Exhausted:
		return t.Exhausted
	case Declined:
		return t.Declined
	case Spent:
		return t.Spent
	}
	return t.Broken
}

// MintRequest is the body that mints a link.
type MintRequest struct {
	Label            string `json:"label"`
	MaxUses          int64  `json:"max_uses"`  // 0 = unlimited
	TTLHours         int64  `json:"ttl_hours"` // 0 = no expiry
	RequiresApproval bool   `json:"requires_approval"`
}

// Options is the request as Mint reads it, already validated, so a bad request
// is refused before a write transaction is opened for it.
func (r MintRequest) Options() (Options, error) {
	opt := Options{Label: r.Label, MaxUses: r.MaxUses, TTLHours: r.TTLHours, RequiresApproval: r.RequiresApproval}
	return opt, opt.Validate()
}

// DecideRequest is the body that approves or declines a Join Request.
type DecideRequest struct {
	Decision string `json:"decision"` // approve | decline
}

// Approve reads the decision, ErrDecisionInvalid for anything but the two.
func (r DecideRequest) Approve() (bool, error) {
	switch r.Decision {
	case "approve":
		return true, nil
	case "decline":
		return false, nil
	}
	return false, ErrDecisionInvalid
}

// PersonView is one line of the who-came-in list on the wire.
type PersonView struct {
	UserID int64  `json:"user_id"`
	Name   string `json:"name"`
	At     string `json:"at"`
}

// View is one link as the owner's list draws it. It carries the code and not
// a URL: the page builds the URL from its own origin, because a mirror
// (xy.pecheny.ru) reaches the server with Host rewritten, and an owner on the
// mirror should hand out a link to the mirror.
type View struct {
	ID               int64        `json:"id"`
	Code             string       `json:"code"`
	Label            string       `json:"label"`
	CreatedAt        string       `json:"created_at"`
	ExpiresAt        string       `json:"expires_at,omitempty"`
	MaxUses          *int64       `json:"max_uses"`
	Used             int64        `json:"used"`
	Left             *int64       `json:"left"`
	RequiresApproval bool         `json:"requires_approval"`
	State            string       `json:"state"`
	Joined           []PersonView `json:"joined"`
	Pending          []PersonView `json:"pending"`
}

// View is the link on the wire, its state as of now.
func (lk Link) View(now time.Time) View {
	return View{
		ID: lk.ID, Code: lk.Code, Label: lk.Label, CreatedAt: lk.CreatedAt,
		ExpiresAt: lk.ExpiresAt, MaxUses: lk.MaxUses, Used: lk.Used, Left: lk.Left(),
		RequiresApproval: lk.RequiresApproval, State: string(lk.State(now)),
		Joined: personViews(lk.Joined), Pending: personViews(lk.Waiting),
	}
}

// Views is View over a whole list, never nil, so it encodes as [].
func Views(links []Link, now time.Time) []View {
	out := make([]View, 0, len(links))
	for _, lk := range links {
		out = append(out, lk.View(now))
	}
	return out
}

func personViews(in []Person) []PersonView {
	out := make([]PersonView, 0, len(in))
	for _, p := range in {
		out = append(out, PersonView(p))
	}
	return out
}
