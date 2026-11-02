package ratingsync

import "testing"

func TestDecide(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name             string
		fact             Fact
		dope, site, base string
		onSite, hasBase  bool
		want             Action
	}{
		{"a new team's empty roster places it", Roster, "[]", "[]", "", false, false, Send},
		{"a new team without зачёты sends none", Flags, "[]", "[]", "", false, false, Same},
		{"equal", Roster, "[1]", "[1]", "[2]", true, true, Same},
		{"typed on the site after the import", Roster, "[1]", "[1,2]", "[1]", true, true, Take},
		{"edited in dope after the import", Roster, "[1,3]", "[1]", "[1]", true, true, Send},
		{"edited on both sides", Roster, "[1,3]", "[1,2]", "[1]", true, true, Ask},
		{"only dope has answers", Answers, "0110", "", "", true, false, Send},
		{"only the site has a roster", Roster, "[]", "[4]", "", true, false, Take},
		{"both have answers and never agreed", Answers, "0110", "0111", "", true, false, Ask},
	}
	for _, c := range cases {
		if got := Decide(c.fact, c.dope, c.site, c.onSite, c.base, c.hasBase); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}
