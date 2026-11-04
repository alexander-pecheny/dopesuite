package ratingsync

// Action is what the export does with a fact of a team.
type Action string

const (
	// Same: dope and the site agree, and nothing goes.
	Same Action = "same"
	// Send: dope's version goes to the site.
	Send Action = "send"
	// Take: the site's version stands, and dope takes it where it can.
	Take Action = "take"
	// Ask: both sides changed since they last agreed, and a person picks.
	Ask Action = "ask"
)

// empty is a fact's value when a side has nothing: no players, no Flags,
// no answers.
func empty(f Fact) string {
	if f == Answers {
		return ""
	}
	return "[]"
}

// Decide compares dope's value of a fact with the site's and with the base,
// the last value both held (ADR-0036). A team the tournament lacks gets its
// roster even when empty, since the roster is what puts it in its venue's
// request.
func Decide(f Fact, dope, site string, onSite bool, base string, hasBase bool) Action {
	switch {
	case !onSite && f != Roster && dope == empty(f):
		return Same
	case !onSite:
		return Send
	case dope == site:
		return Same
	case !hasBase && site == empty(f):
		return Send
	case !hasBase && dope == empty(f):
		return Take
	case !hasBase:
		return Ask
	case dope == base:
		return Take
	case site == base:
		return Send
	default:
		return Ask
	}
}
