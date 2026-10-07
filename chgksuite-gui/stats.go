package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"slices"

	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// Before a packet goes to Telegram the window asks the CLI whether it carries
// stats, and when it does not, asks the person whether to publish it anyway.
// It asks every time, whatever stop_if_no_stats says: that setting is the
// command line's, and a packet posted without its stats is hard to take back.

const (
	telegramVerb = "compose telegram"
	allowNoStats = "--allow_no_stats"
	// telegramVerbWords is how many words of argv the verb takes.
	telegramVerbWords = 2
)

// telegramPacket is the packet a run is about to publish, or "" when the run
// publishes nothing: another command, a dry run, or no packet chosen yet.
func telegramPacket(p *pane, args []string) string {
	if p.spec.Verb != telegramVerb || slices.Contains(args, "--dry_run") {
		return ""
	}
	for _, row := range p.rows {
		if row.spec.Kind == "file" && len(row.paths) == 1 {
			return row.paths[0]
		}
	}
	return ""
}

// hasStats runs `chgksuite compose has_stats` on the packet.
func hasStats(cli, packet string) (bool, error) {
	out, err := exec.Command(cli, "compose", "has_stats", packet).Output()
	if err != nil {
		return false, fmt.Errorf("compose has_stats: %w", err)
	}
	var answer struct {
		HasStats bool `json:"has_stats"`
	}
	if err := json.Unmarshal(out, &answer); err != nil {
		return false, fmt.Errorf("compose has_stats: %w", err)
	}
	return answer.HasStats, nil
}

// withAllowNoStats is the command line once the person has agreed to publish
// without stats. The flag goes straight after the verb, ahead of the packet,
// which is where the flag package stops reading flags.
func withAllowNoStats(args []string) []string {
	if slices.Contains(args, allowNoStats) {
		return args
	}
	return slices.Insert(slices.Clone(args), telegramVerbWords, allowNoStats)
}

// gateStats runs args, first asking about a packet without stats.
func (g *gui) gateStats(args []string, run func([]string)) {
	packet := telegramPacket(g.cur, args)
	if packet == "" {
		run(args)
		return
	}
	check := g.statsOf
	if check == nil {
		check = hasStats
	}
	ok, err := check(g.cli, packet)
	if err != nil {
		dialog.ShowError(err, g.win)
		return
	}
	if ok {
		run(args)
		return
	}
	ask := g.askNoStats
	if ask == nil {
		ask = g.confirmNoStats
	}
	ask(func(yes bool) {
		if yes {
			run(withAllowNoStats(args))
		}
	})
}

// confirmNoStats is the question itself. The safe answer is the one that
// stands out, so Enter does not publish.
func (g *gui) confirmNoStats(answer func(bool)) {
	d := dialog.NewConfirm("No stats in this packet",
		"This pack has no stats. Publish to Telegram anyway?", answer, g.win)
	d.SetConfirmText("Publish anyway")
	d.SetDismissText("Cancel")
	d.SetConfirmImportance(widget.DangerImportance)
	d.Show()
}
