package pages

import (
	"context"
	"database/sql"
	"strconv"

	dopeui "dope/dope/web/ui"
	dopestrings "dope/i18nstrings"
)

// The header breadcrumb trail, shared by every server-rendered page. It mirrors
// the URL: each crumb is a real navigable prefix, labelled with the title of the
// page it links to, and the last one — the page you are on — is plain text.
//
//	/host/fest/12/roster  →  🏠 / My fests / City Cup / Teams
//	/fest/12              →  🏠 / City Cup
//
// dope has two parallel trees: /host/... is where a fest is edited and /fest/...
// is where it is watched. Both start at 🏠 (the public index); the host tree just
// earns a My-fests crumb on the way, exactly as its URL does.
//
// Each helper returns the shared prefix; pages append their own leaf crumb, so
// no page restates its ancestry by hand.

// HomeCrumb is the 🏠 every trail starts with.
func HomeCrumb() dopeui.Item {
	return dopeui.Crumb(dopeui.Home(), dopeui.Href("/"), dopeui.IconHouse, dopeui.Label(dopestrings.Default.Pages.Crumbs.Home()))
}

// HostCrumbs is the editing tree's root: 🏠 / My fests.
func HostCrumbs() []dopeui.Item {
	return []dopeui.Item{HomeCrumb(), dopeui.Crumb(dopeui.Href("/host"), dopeui.Text(dopestrings.Default.Pages.Crumbs.Host()))}
}

// FestCrumbs is a fest's dashboard within the editing tree, the prefix every
// per-fest page (teams, players, games, imports, numbers, audit) hangs off.
func FestCrumbs(ref, title string) []dopeui.Item {
	return append(HostCrumbs(), dopeui.Crumb(dopeui.Href("/host/fest/"+ref), dopeui.Text(title)))
}

// AdminCrumbs is the admin tree's root: 🏠 / Admin.
func AdminCrumbs() []dopeui.Item {
	return []dopeui.Item{HomeCrumb(), dopeui.Crumb(dopeui.Href("/admin"), dopeui.Text(dopestrings.Default.Pages.Crumbs.Admin()))}
}

// Leaf closes a trail with the current page, which is text rather than a link.
func Leaf(title string) dopeui.Item {
	return dopeui.Crumb(dopeui.Text(title))
}

// Trail assembles a topbar's crumbs from a prefix plus this page's own title.
func Trail(prefix []dopeui.Item, title string) dopeui.Item {
	return dopeui.Crumbs(append(append([]dopeui.Item{}, prefix...), Leaf(title))...)
}

// FestTitle looks a fest's name up for the crumb that links to it, on the two
// pages (audit, journal) reached deep enough that their handlers never loaded
// the fest itself. One indexed lookup; an unnamed or missing fest falls back to
// a label rather than an empty crumb.
func FestTitle(ctx context.Context, db *sql.DB, festID int64) string {
	var title string
	if err := db.QueryRowContext(ctx, `select coalesce(title, '') from fests where id = ?`, festID).Scan(&title); err != nil || title == "" {
		return dopestrings.Default.Pages.Crumbs.FestFallback(strconv.FormatInt(festID, 10))
	}
	return title
}
