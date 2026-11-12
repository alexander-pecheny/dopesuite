package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"testing"
)

// Every flag and input the CLI declares needs a Russian caption in
// shell/captions.json, or the classic form shows its bare English name.
func TestEveryFlagHasACaption(t *testing.T) {
	out, err := exec.Command("go", "run", "-C", "../chgksuite", "-tags", "nowasmtypst", "./cmd/chgksuite", "spec").Output()
	if err != nil {
		t.Fatalf("chgksuite spec: %v", err)
	}
	var specs []struct {
		Verb   string
		Inputs []struct{ Label string }
		Flags  []struct{ Name string }
	}
	if err := json.Unmarshal(out, &specs); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("shell/captions.json")
	if err != nil {
		t.Fatal(err)
	}
	var captions struct{ Inputs, Flags map[string]string }
	if err := json.Unmarshal(raw, &captions); err != nil {
		t.Fatal(err)
	}
	for _, s := range specs {
		for _, in := range s.Inputs {
			if captions.Inputs[in.Label] == "" {
				t.Errorf("%s: input %q has no caption", s.Verb, in.Label)
			}
		}
		for _, f := range s.Flags {
			if captions.Flags[f.Name] == "" {
				t.Errorf("%s --%s: no caption", s.Verb, f.Name)
			}
		}
	}
}
