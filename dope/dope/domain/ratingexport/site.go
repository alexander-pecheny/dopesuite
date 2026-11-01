// Package ratingexport sends a fest's rosters and its OD's results to
// rating.chgk.info through the site's results entry API, as the user whose token it
// carries. On a synch tournament each venue's teams go in under its
// representative, which puts a new team in that venue's request.
package ratingexport

import (
	"dope/dope/storage/buffdb"
	"dope/dope/storage/ratingsite"
)

// Site is the rating site's API as the export uses it, authenticated by the
// user's token. Buff answers the teams' base rosters when it mirrors the
// season, so the site is asked only for the rest.
type Site struct {
	ratingsite.Client
	Buff *buffdb.Store
}
