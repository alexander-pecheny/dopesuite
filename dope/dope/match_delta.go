package dopeserver

import "dope/dope/realtime"

// matchDeltaOps lives in the realtime leaf (package dope/realtime); this thin
// wrapper keeps the match-handler call sites terse. See realtime.MatchDeltaOps.
func matchDeltaOps(oldJSON, newJSON []byte) ([]byte, bool) {
	return realtime.MatchDeltaOps(oldJSON, newJSON)
}
