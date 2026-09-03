package replay

import (
	"fmt"
	"sort"
	"strings"

	dopestrings "dope/i18nstrings"
)

// Discrepancies renders every override in a script as a page the tournament's
// author can review. It is generated rather than hand-kept for one reason: a
// hand-kept list is where an override goes to be forgotten, and an override
// nobody revisits is indistinguishable from a bug nobody fixed.
func Discrepancies(scripts ...Script) string {
	s := dopestrings.Default
	var out strings.Builder
	out.WriteString("# " + s.Replay.Report.Title() + "\n\n")
	out.WriteString(s.Replay.Report.Intro() + "\n")
	out.WriteString(s.Replay.Report.CollectedLead() + "`override`" + s.Replay.Report.CollectedMid() + "\n")

	total := 0
	for _, script := range scripts {
		if len(script.Overrides) == 0 {
			continue
		}
		title := script.Title
		if title == "" {
			title = script.Game
		}
		fmt.Fprintf(&out, "\n## %s\n\n", title)
		out.WriteString("| " + s.Replay.Report.ColBout() + " | " + s.Replay.Report.ColWhat() + " | " + s.Replay.Report.ColWho() + " | " + s.Replay.Report.ColWhy() + " |\n|---|---|---|---|\n")
		overrides := append([]Override(nil), script.Overrides...)
		sort.SliceStable(overrides, func(a, b int) bool { return overrides[a].Line < overrides[b].Line })
		for _, over := range overrides {
			who := over.Participant
			if who == "" {
				who = s.Replay.Report.WhoAll()
			}
			fmt.Fprintf(&out, "| `%s` | %s | %s | %s |\n", over.At, over.Field, who, over.Reason)
			total++
		}
	}
	if total == 0 {
		out.WriteString("\n" + s.Replay.Report.NoneYet() + "\n")
	}
	return out.String()
}
