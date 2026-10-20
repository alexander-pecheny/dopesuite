//go:build !windows

package handout

import "os/exec"

func hideConsole(*exec.Cmd) {}
