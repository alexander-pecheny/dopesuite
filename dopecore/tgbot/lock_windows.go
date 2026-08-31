//go:build windows

package tgbot

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// AcquirePollLock claims this host's right to poll token. The returned release
// drops it. An error means someone else already holds it and this process must
// not poll; the message names the holder it found.
//
// Windows has no flock; LockFileEx with LOCKFILE_FAIL_IMMEDIATELY is the same
// bargain — check and hold in one call, released by the kernel when the handle
// closes, however the process ended.
func AcquirePollLock(token string) (release func(), err error) {
	path := filepath.Join(lockDir, "dopesuite-bot-"+TokenHash(token)+".lock")
	f, err := open(path)
	if err != nil {
		return nil, fmt.Errorf("poll lock %s: %w", path, err)
	}
	var overlapped windows.Overlapped
	err = windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, &overlapped)
	if err != nil {
		held, _ := os.ReadFile(path)
		f.Close()
		return nil, fmt.Errorf("poll lock %s is held by %s: %w", path, strings.TrimSpace(string(held)), err)
	}
	if err := f.Truncate(0); err == nil {
		_, _ = fmt.Fprintf(f, "pid %d %s\n", os.Getpid(), strings.Join(os.Args, " "))
	}
	return func() {
		var o windows.Overlapped
		_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &o)
		_ = f.Close()
	}, nil
}
