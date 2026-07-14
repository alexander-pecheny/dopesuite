package handout

import (
	"os"
	"path/filepath"
	"sync"
)

// typst is a separate process that reads its source and images from a real
// filesystem — it has no virtual-FS mode — so, unlike every other part of xy,
// handout rendering must materialise the user's plaintext somewhere. The scratch
// dir is wiped when the render returns, but a crash in between would leave it
// behind, so put it somewhere RAM-backed: nothing then survives a reboot, and it
// never reaches persistent storage in the first place.
//
// XY_SCRATCH_DIR overrides; otherwise /dev/shm (tmpfs on Linux) when it's usable,
// else the OS default ($TMPDIR, /tmp) — which is often tmpfs too, but only by
// accident of how the host is mounted.
var scratchRoot = sync.OnceValue(func() string {
	if dir := os.Getenv("XY_SCRATCH_DIR"); dir != "" {
		return dir
	}
	const shm = "/dev/shm"
	probe, err := os.MkdirTemp(shm, "xy-probe-*")
	if err != nil {
		return "" // fall back to the OS default
	}
	os.RemoveAll(probe)
	return shm
})

// scratchTemp creates a scratch dir under the RAM-backed root, readable only by
// the server's own user — it holds decrypted questions and handouts.
func scratchTemp(pattern string) (string, error) {
	dir, err := os.MkdirTemp(scratchRoot(), pattern)
	if err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

// writeScratch writes one plaintext file into a scratch dir, owner-only.
func writeScratch(dir, name string, data []byte) error {
	return os.WriteFile(filepath.Join(dir, name), data, 0o600)
}
