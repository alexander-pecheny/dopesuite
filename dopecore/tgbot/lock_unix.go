//go:build !windows

package tgbot

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// AcquirePollLock claims this host's right to poll token. The returned release
// drops it. An error means someone else already holds it and this process must
// not poll; the message names the holder it found.
func AcquirePollLock(token string) (release func(), err error) {
	path := filepath.Join(lockDir, "dopesuite-bot-"+TokenHash(token)+".lock")
	f, err := open(path)
	if err != nil {
		return nil, fmt.Errorf("poll lock %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		held, _ := os.ReadFile(path)
		f.Close()
		return nil, fmt.Errorf("poll lock %s is held by %s: %w", path, strings.TrimSpace(string(held)), err)
	}
	// Who holds it, for whoever is reading the log at 3am. Best-effort: the lock
	// is the fd, not the contents, and we may only have it open for reading.
	if err := f.Truncate(0); err == nil {
		_, _ = fmt.Fprintf(f, "pid %d %s\n", os.Getpid(), strings.Join(os.Args, " "))
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		if err := f.Close(); err != nil {
			log.Printf("poll lock %s: %v", path, err)
		}
	}, nil
}
