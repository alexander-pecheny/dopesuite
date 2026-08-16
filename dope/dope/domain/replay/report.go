package replay

import (
	"fmt"
	"sort"
	"strings"
)

// Discrepancies renders every override in a script as a page the tournament's
// author can review. It is generated rather than hand-kept for one reason: a
// hand-kept list is where an override goes to be forgotten, and an override
// nobody revisits is indistinguishable from a bug nobody fixed.
func Discrepancies(scripts ...Script) string {
	var out strings.Builder
	out.WriteString("# Расхождения с протоколами\n\n")
	out.WriteString("Здесь то, в чём dope не согласен с гугл-таблицами турнира и где решено верить dope.\n")
	out.WriteString("Файл собирается из `override` в стенограммах — правьте стенограмму, не эту страницу.\n")

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
		out.WriteString("| Бой | Что | У кого | Почему листу верить нельзя |\n|---|---|---|---|\n")
		overrides := append([]Override(nil), script.Overrides...)
		sort.SliceStable(overrides, func(a, b int) bool { return overrides[a].Line < overrides[b].Line })
		for _, over := range overrides {
			who := over.Participant
			if who == "" {
				who = "весь бой"
			}
			fmt.Fprintf(&out, "| `%s` | %s | %s | %s |\n", over.At, over.Field, who, over.Reason)
			total++
		}
	}
	if total == 0 {
		out.WriteString("\nПока ни одного: всё, что переиграно, сошлось с листами.\n")
	}
	return out.String()
}
