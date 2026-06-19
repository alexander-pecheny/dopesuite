// Package hostpages holds the server-rendered host-UI page handlers (the host
// landing/dashboard, game settings, roster, and fest-game helpers). It is the
// host-only slice carved out of package pages: handlers reach the service core
// only through pages.Host (the same interface package pages defines), so this
// presentation layer never imports the server package. The server constructs a
// *hostpages.Server by wrapping itself (hostpages.New(s)) and dispatches into
// it; hostpages never dispatches back, so there is no import cycle.
//
// hostpages imports pages (for the shared Host interface and the few sibling
// page renders the host router delegates to — numbers/audit/journal); pages
// must never import hostpages.
package hostpages

import "dope/dope/web/pages"

// Server binds the host-UI page handlers to a pages.Host. Construct with New.
type Server struct {
	h pages.Host
}

// New returns a host-page Server over the given Host.
func New(h pages.Host) *Server { return &Server{h: h} }

// pages returns a sibling pages.Server bound to the same Host, used by the host
// router to delegate into the page handlers that stayed in package pages
// (fest numbers, audit, journal).
func (s *Server) pages() *pages.Server { return pages.New(s.h) }
