package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"fyne.io/fyne/v2/test"
)

var telegram = commandSpec{
	Verb:   "compose telegram",
	Inputs: []input{{Kind: "file", Label: "packet", Ext: []string{".4s"}}},
	Flags: []flagSpec{
		{Name: "tgchannel", Default: ""},
		{Name: "dry_run", Default: "false", Bool: true},
	},
}

// statsRun is one press of Run on the telegram form, with the CLI's answer
// and the person's answer given, and the command line that ran, if any.
func statsRun(t *testing.T, has, yes bool, dryRun bool) (ran []string, asked bool) {
	t.Helper()
	test.NewApp()
	g := &gui{panes: map[string]*pane{}}
	g.cur = g.buildPane(telegram)
	g.cur.rows[0].paths = []string{"/tmp/packet.4s"}
	g.cur.values["tgchannel"] = "@channel"
	if dryRun {
		g.cur.values["dry_run"] = "true"
	}
	g.statsOf = func(string, string) (bool, error) { return has, nil }
	g.askNoStats = func(answer func(bool)) { asked = true; answer(yes) }
	g.gateStats(g.cur.argv(), func(args []string) { ran = args })
	return ran, asked
}

func TestAPacketWithStatsRunsUnasked(t *testing.T) {
	ran, asked := statsRun(t, true, false, false)
	if asked || slices.Contains(ran, allowNoStats) || ran == nil {
		t.Errorf("asked=%v ran=%q", asked, ran)
	}
}

func TestAPacketWithoutStatsIsAskedAbout(t *testing.T) {
	ran, asked := statsRun(t, false, true, false)
	want := []string{"compose", "telegram", "--allow_no_stats", "--tgchannel", "@channel", "/tmp/packet.4s"}
	if !asked || !slices.Equal(ran, want) {
		t.Errorf("asked=%v ran=%q, want %q", asked, ran, want)
	}
}

func TestSayingNoRunsNothing(t *testing.T) {
	if ran, asked := statsRun(t, false, false, false); !asked || ran != nil {
		t.Errorf("asked=%v ran=%q", asked, ran)
	}
}

// A dry run publishes nothing, so there is nothing to ask about.
func TestADryRunIsNotAskedAbout(t *testing.T) {
	if ran, asked := statsRun(t, false, false, true); asked || ran == nil {
		t.Errorf("asked=%v ran=%q", asked, ran)
	}
}

// The dialog opens and closes without taking the window with it.
func TestTheQuestionOpens(t *testing.T) {
	test.NewApp()
	g := &gui{win: test.NewWindow(nil), panes: map[string]*pane{}}
	g.confirmNoStats(func(bool) {})
}

// The CLI's half of the seam: has_stats answers in the shape this side reads.
// Point $CHGKSUITE at the binary to run it.
func TestHasStatsReadsTheCLI(t *testing.T) {
	cli := os.Getenv("CHGKSUITE")
	if cli == "" {
		t.Skip("set CHGKSUITE to the chgksuite binary")
	}
	dir := t.TempDir()
	for src, want := range map[string]bool{
		"? Вопрос?\n! Ответ\n/ Взятия: 3/10\n": true,
		"? Вопрос?\n! Ответ\n":                 false,
	} {
		path := filepath.Join(dir, "packet.4s")
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := hasStats(cli, path)
		if err != nil || got != want {
			t.Errorf("%q: has_stats=%v, %v; want %v", src, got, err, want)
		}
	}
}
