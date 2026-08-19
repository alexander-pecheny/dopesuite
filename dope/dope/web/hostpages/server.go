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

import (
	"database/sql"
	"errors"
	"net/http"
	"sync"

	"dope/dope/domain/view"
	"dope/dope/web/pages"
	"dope/dope/web/route"
	dopeui "dope/dope/web/ui"
)

// Server binds the host-UI page handlers to a pages.Host. Construct with New
// once per host: it builds its route table (routes.go) on first use.
type Server struct {
	h     pages.Host
	table *route.Table
	once  sync.Once
}

// New returns a host-page Server over the given Host.
func New(h pages.Host) *Server { return &Server{h: h} }

// pages returns a sibling pages.Server bound to the same Host, used by the host
// router to delegate into the page handlers that stayed in package pages
// (fest numbers, audit, journal).
func (s *Server) pages() *pages.Server { return pages.New(s.h) }

// festPage renders a page over a fest the one way: the header loaded once, a
// missing fest a 404 and any other failure a 500, then build — whose own
// sql.ErrNoRows reads as 404 too. The form is parsed first, so a re-render
// after a failed POST reads the submitted values and a GET reads its query.
func (s *Server) festPage(w http.ResponseWriter, r *http.Request, festID int64, build func(fest view.HostFest) (*dopeui.Doc, error)) {
	_ = r.ParseForm()
	fest, err := s.h.LoadHostFestHeader(r.Context(), festID)
	var doc *dopeui.Doc
	if err == nil {
		doc, err = build(fest)
	}
	switch {
	case errors.Is(err, sql.ErrNoRows):
		http.NotFound(w, r)
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	default:
		pages.RenderDoc(w, s.h.Engine().AssetETags, doc)
	}
}
