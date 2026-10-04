package games

import (
	"fmt"
	"strconv"

	dopestrings "dope/i18nstrings"
)

// The schemes the creation form prefills, one per format that is described
// by a scheme (Definition.DefaultDSL). Each is the format at its smallest,
// written in the DSL so the host sees something editable.

// BrainDSL is a Brain at its plainest: one round-robin of everybody, so many
// questions a Match. The creation form offers it, and a clear moves a pre-DSL
// Brain onto it.
func BrainDSL(participants, questions int) string {
	if participants < 2 {
		participants = 4
	}
	if questions <= 0 {
		questions = 5
	}
	return fmt.Sprintf("[defaults]\nquestions: %d\n\n[scheme]\nkind: roundrobin\ngroup_size: %d\n", questions, participants)
}

// SIDefaultDSL is personal SI's shape at its smallest: one table, everyone at
// it, eight themes. A real tournament edits it into groups and a play-off.
func SIDefaultDSL(players int) string {
	if players < 3 {
		players = 3
	}
	return fmt.Sprintf("[scheme]\nkind: roundrobin\ngroup_size: %d\nmatch_size: 3\nthemes: 8\nbout.points: seats + 1 - place\nsorting: [points, total, plus]\n", players)
}

// TroikaDefaultDSL is Troika's regulations at their smallest: one group of
// everybody over six themes, ranked as the regulations rank — a rating score
// of 1 / 0.5 / 0 per Match plus game points over fifty, then head-to-head,
// taken, difference. A real tournament edits it into the group stages and the
// final.
func TroikaDefaultDSL(participants int) string {
	if participants < 2 {
		participants = 2
	}
	return fmt.Sprintf("[scheme]\nkind: roundrobin\ngroup_size: %d\nthemes: 6\nmetric: total\npoints: [1, 0.5, 0]\nstandings.rating: points + taken / 50\nsorting: [rating, h2h, taken, diff]\n", participants)
}

// HamsaDefaultDSL is the tournament's own shape at its smallest: a group stage
// of two Games four to a table, then a final of the four best. The scheme
// itself is in the Catalog — it is text a host reads and edits — and carries
// no `[init]` line naming the KSI qualifier, since a fest that has not played
// one yet would not compile it.
func HamsaDefaultDSL(participants int) string {
	if participants < 4 {
		participants = 4
	}
	participants -= participants % 4
	return dopestrings.Default.Host.Games.HamsaScheme(strconv.Itoa(participants))
}
