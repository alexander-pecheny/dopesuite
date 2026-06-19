// Package view holds shared presentation/view-model types that both the server
// and the HTTP handler packages need to name — kept in a leaf so neither side
// has to import the other.
package view

import "fmt"

// HostFest is the fest-header model shown across the host pages (dashboard,
// roster, numbers, games).
type HostFest struct {
	ID        int64
	Slug      string
	Title     string
	StartDate string
	EndDate   string
	Dates     string
	IsPublic  bool
}

// Ref returns the slug when set, else the numeric id — used when building host
// URLs so users see /host/fest/my-fest in preference to /host/fest/123.
func (h HostFest) Ref() string {
	if h.Slug != "" {
		return h.Slug
	}
	return fmt.Sprintf("%d", h.ID)
}
