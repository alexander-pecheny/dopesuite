package tgbot

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Telegram hands every update to exactly one poller. A second process on the
// same token does not fail loudly — it wins some of the updates and loses the
// rest, so a login code typed into the bot lands in whichever server asked
// first. The token is the only thing that identifies "the same bot": prod and
// staging differ by unit name, data dir and env file, and agree on this.
//
// So the claim is a lock named after the token, held for as long as the process
// polls. flock is check-and-hold in one syscall, which a scan of the process
// table is not — two units coming up together after a reboot both read an empty
// table and both start — and the kernel drops it on exit, crash or kill -9, so
// there is no stale state to reap.
//
// This bounds one host. The poller on another machine is Telegram's to report,
// and it does: ErrConflict.

// lockDir is where the claims live. /run/lock is the host's shared lock
// directory (1777, tmpfs, cleared on reboot) and it is NOT shadowed by
// systemd's PrivateTmp, so units that see different /tmp still contend here.
// A unit under ProtectSystem=strict needs ReadWritePaths=/run/lock.
var lockDir = "/run/lock"

// TokenHash names a bot token without printing it: the first 12 hex digits of
// its sha256. Short on purpose — it goes in a filename and a log line, and at
// 48 bits it identifies a token among the two this suite runs while saying
// nothing about the secret.
func TokenHash(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(sum[:])[:12]
}

// open takes an fd on the lock file, whoever created it. The two contenders
// worth catching may run as different users — xy's prod unit and a developer's
// shell are both on the box that hosts xy — and a lock only one of them can
// open is worse than none: it would let a dev process starve prod of its bot.
// So: widen the mode when we are the owner, settle for read-only when we are
// not. flock needs an fd, not write permission.
func open(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o666)
	if err == nil {
		_ = f.Chmod(0o666)
		return f, nil
	}
	if !errors.Is(err, os.ErrPermission) {
		return nil, err
	}
	return os.Open(path)
}

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
