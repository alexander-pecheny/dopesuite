package main

import (
	"os"
	"testing"

	"fyne.io/fyne/v2/test"
)

// The seam between the two modules: what `chgksuite spec` prints has to be
// what this program reads, and every command it names has to draw a form.
// Point $CHGKSUITE at the binary to run it.
func TestEveryCommandInTheSpecDrawsAForm(t *testing.T) {
	cli := os.Getenv("CHGKSUITE")
	if cli == "" {
		t.Skip("set CHGKSUITE to the chgksuite binary")
	}
	specs, err := loadSpec(cli)
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) == 0 {
		t.Fatal("no commands")
	}
	test.NewApp()
	g := &gui{panes: map[string]*pane{}, cli: cli}
	for _, spec := range specs {
		p := g.buildPane(spec)
		if spec.What == "" {
			t.Errorf("%s: no description", spec.Verb)
		}
		if len(p.rows) != len(spec.Inputs) {
			t.Errorf("%s: %d input rows for %d inputs", spec.Verb, len(p.rows), len(spec.Inputs))
		}
		if got := previewLine(cli, p.argv()); got == "" {
			t.Errorf("%s: no command line", spec.Verb)
		}
	}
}
