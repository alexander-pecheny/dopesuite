package server

import (
	"net/http"
	"os"

	"pecheny.me/dopecore/sameorigin"
)

// trustedOriginHostsEnv lists the hosts besides xy's own whose pages may send
// writes: the mirrors, which proxy to xy with Host rewritten to xy.pecheny.me
// while the browser's Origin still names the mirror (xy.pecheny.kz, and
// xy.pecheny.ru through xy-relay.pecheny.me).
//
// Setting it, even to an empty value, is also what turns the check on for every
// write. Unset, only the admin POSTs are checked, as before, and the rest of xy
// relies on the SameSite=Lax cookie. The default stays off because a deploy
// never touches /etc/xy.env: a check that came on by itself would refuse every
// write from the mirrors until someone listed them there.
const trustedOriginHostsEnv = "XY_TRUSTED_ORIGIN_HOSTS"

// guardOrigin wraps the whole route table in dopecore's same-origin check when
// XY_TRUSTED_ORIGIN_HOSTS is set (see above), and returns h as it is otherwise.
func guardOrigin(h http.Handler) http.Handler {
	if _, on := os.LookupEnv(trustedOriginHostsEnv); !on {
		return h
	}
	return sameorigin.Middleware(func() string { return os.Getenv(trustedOriginHostsEnv) }, h)
}
