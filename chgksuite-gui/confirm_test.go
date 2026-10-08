package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// telegram is the shape `chgksuite spec` gives compose telegram: a packet, a
// dry-run flag, and a confirm that checks for stats.
var telegram = commandSpec{
	Verb:   "compose telegram",
	Inputs: []input{{Kind: "file", Label: "packet", Ext: []string{".4s"}}},
	Flags: []flagSpec{
		{Name: "tgchannel", Default: ""},
		{Name: "dry_run", Default: "false", Bool: true},
	},
	Confirm: &confirmSpec{
		Check: []string{"compose", "has_stats"}, Field: "has_stats",
		SkipIf: []string{"dry_run"}, Flag: "allow_no_stats",
		Question: "В пакете нет статистики взятий. Всё равно опубликовать в телеграм?",
		Title:    "Нет статистики", Yes: "Опубликовать", No: "Отмена",
	},
}

// press is one press of Run on a form.
type press struct {
	spec    commandSpec
	checked bool  // what the check answers
	failed  error // the check's error, if it fails
	yes     bool  // what the person answers
	dryRun  bool
}

// run presses Run and returns the command line that ran, if any, and whether
// the question was asked.
func (p press) run(t *testing.T) (ran []string, asked bool) {
	t.Helper()
	test.NewApp()
	g := &gui{panes: map[string]*pane{}, log: widget.NewLabel("")}
	g.logBox = container.NewVScroll(g.log)
	g.cur = g.buildPane(p.spec)
	g.cur.rows[0].paths = []string{"/tmp/packet.4s"}
	g.cur.values["tgchannel"] = "@channel"
	if p.dryRun {
		g.cur.values["dry_run"] = "true"
	}
	g.check = func(string, *confirmSpec, string) (bool, error) { return p.checked, p.failed }
	g.ask = func(_ *confirmSpec, answer func(bool)) { asked = true; answer(p.yes) }
	g.confirmThenRun(g.cur.argv(), func(args []string) { ran = args })
	return ran, asked
}

func TestAPacketWithStatsRunsUnasked(t *testing.T) {
	ran, asked := press{spec: telegram, checked: true}.run(t)
	if asked || ran == nil || slices.Contains(ran, "--allow_no_stats") {
		t.Errorf("asked=%v ran=%q", asked, ran)
	}
}

func TestAPacketWithoutStatsIsAskedAbout(t *testing.T) {
	ran, asked := press{spec: telegram, yes: true}.run(t)
	want := []string{"compose", "telegram", "--allow_no_stats", "--tgchannel", "@channel", "/tmp/packet.4s"}
	if !asked || !slices.Equal(ran, want) {
		t.Errorf("asked=%v ran=%q, want %q", asked, ran, want)
	}
}

func TestSayingNoRunsNothing(t *testing.T) {
	if ran, asked := (press{spec: telegram}).run(t); !asked || ran != nil {
		t.Errorf("asked=%v ran=%q", asked, ran)
	}
}

// A dry run publishes nothing, so there is nothing to ask about.
func TestADryRunIsNotAskedAbout(t *testing.T) {
	if ran, asked := (press{spec: telegram, dryRun: true}).run(t); asked || ran == nil {
		t.Errorf("asked=%v ran=%q", asked, ran)
	}
}

// A check that fails asks rather than publishing unasked.
func TestAFailedCheckStillAsks(t *testing.T) {
	ran, asked := press{spec: telegram, checked: true, failed: errors.New("boom")}.run(t)
	if !asked || ran != nil {
		t.Errorf("asked=%v ran=%q", asked, ran)
	}
}

// A command whose spec has no confirm runs straight away.
func TestACommandWithoutAConfirmRuns(t *testing.T) {
	plain := telegram
	plain.Confirm = nil
	if ran, asked := (press{spec: plain}).run(t); asked || ran == nil {
		t.Errorf("asked=%v ran=%q", asked, ran)
	}
}

// The dialog opens and closes without taking the window with it.
func TestTheQuestionOpens(t *testing.T) {
	test.NewApp()
	g := &gui{win: test.NewWindow(nil), panes: map[string]*pane{}}
	g.askConfirm(telegram.Confirm, func(bool) {})
}

// The CLI's half of the seam: the check the spec names answers in the shape
// this side reads. Point $CHGKSUITE at the binary to run it.
func TestTheCheckReadsTheCLI(t *testing.T) {
	cli := os.Getenv("CHGKSUITE")
	if cli == "" {
		t.Skip("set CHGKSUITE to the chgksuite binary")
	}
	specs, err := loadSpec(cli)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(specs, func(s commandSpec) bool { return s.Confirm != nil })
	if i < 0 {
		t.Fatal("no command in the spec asks before running")
	}
	c := specs[i].Confirm
	dir := t.TempDir()
	for src, want := range map[string]bool{
		"? Вопрос?\n! Ответ\n/ Взятия: 3/10\n": true,
		"? Вопрос?\n! Ответ\n":                 false,
	} {
		path := filepath.Join(dir, "packet.4s")
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := runCheck(cli, c, path)
		if err != nil || got != want {
			t.Errorf("%q: %v, %v; want %v", src, got, err, want)
		}
	}
}
