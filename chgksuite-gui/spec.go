package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// What the command line says about itself. `chgksuite spec` prints this, so a
// flag added to the CLI turns up in the window with nothing edited here.

type flagSpec struct {
	Name     string   `json:"name"`
	Usage    string   `json:"usage"`
	Default  string   `json:"default"`
	Bool     bool     `json:"bool"`
	Advanced bool     `json:"advanced"`
	Choices  []string `json:"choices"`
	Path     string   `json:"path"` // "file" or "folder" for the flags that name one
}

type input struct {
	Kind     string   `json:"kind"` // file, files, folder, text or choice
	Label    string   `json:"label"`
	Ext      []string `json:"ext"`
	Choices  []string `json:"choices"`
	Optional bool     `json:"optional"`
}

type commandSpec struct {
	Verb   string     `json:"verb"`
	What   string     `json:"what"`
	Inputs []input    `json:"inputs"`
	Flags  []flagSpec `json:"flags"`
}

// findCLI looks for the chgksuite binary where it is likely to be: named
// outright, beside this program (which is where a .app bundle puts it), or on
// the PATH.
func findCLI() (string, error) {
	if p := os.Getenv("CHGKSUITE"); p != "" {
		return p, nil
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for _, p := range []string{
			filepath.Join(dir, "chgksuite"),
			filepath.Join(dir, "..", "Resources", "chgksuite"),
		} {
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p, nil
			}
		}
	}
	p, err := exec.LookPath("chgksuite")
	if err != nil {
		return "", fmt.Errorf("no chgksuite binary beside this one or on the PATH; set $CHGKSUITE to it")
	}
	return p, nil
}

func loadSpec(cli string) ([]commandSpec, error) {
	out, err := exec.Command(cli, "spec").Output()
	if err != nil {
		return nil, fmt.Errorf("%s spec: %w", cli, err)
	}
	var specs []commandSpec
	if err := json.Unmarshal(out, &specs); err != nil {
		return nil, fmt.Errorf("%s spec: %w", cli, err)
	}
	return specs, nil
}
