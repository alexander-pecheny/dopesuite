// Package buildinfo reports which build of an app is running. Releases stamp
// the tag in with -ldflags; everything else falls back to the VCS revision Go
// records automatically, so an unstamped binary still identifies itself.
package buildinfo

import (
	"runtime/debug"
	"sync"
)

// stamped is set at link time: -ldflags "-X pecheny.me/dopecore/buildinfo.stamped=<tag>".
var stamped string

// Version is the human-readable build identity, e.g. "xy/2026.08.03" for a
// release or "dev-abcdef1-dirty" for a working-tree build.
var Version = sync.OnceValue(func() string {
	var revision string
	var modified bool
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				revision = s.Value
			case "vcs.modified":
				modified = s.Value == "true"
			}
		}
	}
	return describe(stamped, revision, modified)
})

func describe(stamped, revision string, modified bool) string {
	if stamped != "" {
		return stamped
	}
	if revision == "" {
		return "dev"
	}
	out := "dev-" + revision[:min(7, len(revision))]
	if modified {
		out += "-dirty"
	}
	return out
}
