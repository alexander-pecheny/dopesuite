package handout

import (
	"os/exec"
	"syscall"
)

// createNoWindow keeps typst from flashing a console over the desktop app.
const createNoWindow = 0x08000000

func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
