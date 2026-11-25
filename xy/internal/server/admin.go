package server

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"math"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"code.pecheny.me/pecheny/dopesuite/dopecore/adminusers"
	"code.pecheny.me/pecheny/dopesuite/dopecore/authweb"
	"code.pecheny.me/pecheny/dopesuite/dopecore/idstr"
	"code.pecheny.me/pecheny/dopesuite/dopecore/sameorigin"
	"code.pecheny.me/pecheny/dopesuite/dopecore/session"
	"code.pecheny.me/pecheny/dopesuite/dopecore/sqlitex"
	kit "code.pecheny.me/pecheny/dopesuite/dopeuikit/kit"

	xystrings "xy/i18nstrings"
	"xy/internal/ui"
)

const adminUserEnv = "XY_ADMIN_USER"

func (s *server) requireAdmin(w http.ResponseWriter, r *http.Request) (session.User, bool) {
	return adminusers.RequireAdmin(w, r, adminUserEnv, func() (session.User, bool) {
		// Cookie only: /admin creates users, which no API token may do.
		return s.lookupCookieSession(w, r)
	})
}

// The length limits on a username, in bytes, and its shape: the shared rule
// (authweb.ValidUsername).
const (
	usernameMinLen = authweb.UsernameMinLen
	usernameMaxLen = authweb.UsernameMaxLen
)

func validNewUsername(name string) bool { return authweb.ValidUsername(name) }

// adminIndexDoc builds the /admin landing page: a link list of admin tools.
func adminIndexDoc() *ui.Doc {
	s := xystrings.Default
	return &ui.Doc{Nodes: []ui.Node{
		ui.Page(ui.Title(s.Admin.Page.Title()), ui.PageSheet,
			ui.Topbar(ui.Title(s.Admin.Page.Title())),
			ui.Section(
				ui.List(
					ui.Listrow(ui.Href("/admin/create_users"),
						ui.Listtitle(ui.Text(s.Admin.CreateUsers.Name())),
					),
					ui.Listrow(ui.Href("/admin/users"),
						ui.Listtitle(ui.Text(s.Admin.Users.Name())),
					),
				),
			),
		),
	}}
}

// adminCreateUsersDoc wraps the kit's create-users body in xy's admin chrome.
func adminCreateUsersDoc(data adminusers.CreateUsersData) *ui.Doc {
	s := xystrings.Default
	items := []ui.Item{
		ui.Title(s.Admin.CreateUsers.Title()), ui.PageSheet,
		ui.Topbar(ui.Title(s.Admin.CreateUsers.Name()),
			ui.Iconlink(ui.Href("/admin"), ui.Label(s.Admin.Page.Title()), ui.Text("↩️")),
		),
	}
	return &ui.Doc{Nodes: []ui.Node{ui.Page(append(items, kit.AdminCreateUsers(data)...)...)}}
}

type adminUserRow struct {
	ID         int64
	Username   string
	Telegram   string
	Used       int64
	Quota      int64
	Unlimited  bool
	LastSeenAt string // raw RFC3339 of the newest session's last_seen_at; empty when there is none
}

// sortAdminUsers reorders in place. Storage can't be an ORDER BY — it is summed
// per user after the query — so both keys are sorted here.
func sortAdminUsers(users []adminUserRow, s adminusers.Sort) {
	if s.Key == "" {
		return
	}
	slices.SortStableFunc(users, func(a, b adminUserRow) int {
		var c int
		if s.Key == "used" {
			c = cmp.Compare(a.Used, b.Used)
		} else {
			c = cmp.Compare(a.LastSeenAt, b.LastSeenAt) // RFC3339 sorts as text; never-seen ("") sorts first
		}
		if s.Desc {
			return -c
		}
		return c
	})
}

// storageCell reads "0.16 /" over an editable "25" field (quota_<id>, saved by
// the table's form); the MB unit is in the column header, since the page sheet
// is too narrow to repeat it per row. The admin's own uncapped account has no
// field and reads "0.16 / ∞".
func storageCell(u adminUserRow) ui.Item {
	if u.Unlimited {
		return ui.Cell(ui.Text(mbNum(u.Used) + " / ∞"))
	}
	return ui.Cell(ui.Col(ui.SpaceXS,
		ui.Line(ui.Text(mbNum(u.Used)+" /")),
		ui.Numfield(ui.Name(quotaField(u.ID)), ui.Value(mbNum(u.Quota)), ui.Narrow(), ui.Min("0"), ui.Step("any")),
	))
}

// quotaFieldPrefix names a row's quota input: quota_<user id>, in MB.
const quotaFieldPrefix = "quota_"

func quotaField(id int64) string { return quotaFieldPrefix + idstr.Format(id) }

// adminUsersDoc builds the /admin/users page: every account with its telegram
// handle, storage against quota, and last activity. Login and handle share one
// column — two lines of the same fact, and three columns fit a phone where five
// did not.
// The table sits in one POST form back to the same URL, so saving keeps the sort.
func adminUsersDoc(users []adminUserRow, s adminusers.Sort, action string) *ui.Doc {
	str := xystrings.Default
	var body ui.Item
	if len(users) > 0 {
		rows := []ui.Item{ui.Scroll(), ui.ID("adminUsers"), ui.Trow(
			ui.Hcell(ui.Text(str.Admin.Users.ColUser())),
			kit.SortHeader("last", str.Admin.Users.ColActivity(), s),
			kit.SortHeader("used", str.Admin.Users.ColStorage(), s),
		)}
		for _, u := range users {
			who := ui.Cell(ui.Text(u.Username))
			if u.Telegram != "" {
				who = ui.Cell(ui.Col(ui.SpaceNone, ui.Line(ui.Text(u.Username)), ui.Muted(ui.Text(u.Telegram))))
			}
			rows = append(rows, ui.Trow(
				who,
				ui.Cell(ui.Text(kit.AdminTime(u.LastSeenAt))),
				storageCell(u),
			))
		}
		body = ui.Section(ui.Form(ui.DirCol, ui.SpaceMD, ui.Method("post"), ui.Action(action), ui.Autocomplete("off"),
			ui.Table(rows...),
			ui.Row(ui.Button(ui.Submit(), ui.Text(str.Admin.Users.Save()))),
		))
	} else {
		body = ui.Section(ui.Hint(ui.Text(str.Admin.Users.Empty())))
	}
	return &ui.Doc{Nodes: []ui.Node{
		ui.Page(ui.Title(str.Admin.Users.Title()), ui.PageSheet,
			ui.Topbar(ui.Title(str.Admin.Users.Name()),
				ui.Iconlink(ui.Href("/admin"), ui.Label(str.Admin.Page.Title()), ui.Text("↩️")),
			),
			body,
		),
	}}
}

// HandleAdminUsers serves /admin/users — the account list, ordered by ?sort/?dir.
// Activity is the newest session's last_seen_at, not its created_at: sessions
// slide, so someone who visits daily on the same cookie never logs in again.
// POST saves the edited quotas and redirects back to the same view.
func (s *server) HandleAdminUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if r.Method == http.MethodPost {
		if !sameorigin.Allowed(r, os.Getenv(trustedOriginHostsEnv)) {
			http.Error(w, "bad origin", http.StatusForbidden)
			return
		}
		s.handleAdminQuotasSubmit(w, r)
		return
	}
	users, err := s.loadAdminUsers(r.Context())
	if err != nil {
		handleErr(w, err)
		return
	}
	order := adminusers.ParseSort(r.URL.Query(), "used", "last")
	sortAdminUsers(users, order)
	s.renderAdminPage(w, adminUsersDoc(users, order, r.URL.RequestURI()))
}

// maxQuotaMB is the largest quota whose byte count still fits an int64.
const maxQuotaMB = math.MaxInt64 / bytesPerMiB

// parseQuotaMB reads one quota field, in MB ("2.5" or "2,5"), as bytes. NaN
// fails both comparisons and ±Inf fails one, so neither gets through.
func parseQuotaMB(v string) (int64, bool) {
	mb, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(v), ",", "."), floatBits)
	if err != nil || !(mb >= 0 && mb <= maxQuotaMB) {
		return 0, false
	}
	return int64(math.Round(mb * bytesPerMiB)), true
}

// parseQuotas reads the quota_<id> fields into bytes per user id. One bad
// value fails the whole save.
func parseQuotas(form map[string][]string) (map[int64]int64, error) {
	out := map[int64]int64{}
	for key, vals := range form {
		idStr, ok := strings.CutPrefix(key, quotaFieldPrefix)
		if !ok || len(vals) == 0 {
			continue
		}
		id, err := idstr.Parse(idStr)
		if err != nil {
			return nil, errors.New("bad quota field " + key)
		}
		q, ok := parseQuotaMB(vals[0])
		if !ok {
			return nil, errors.New("bad quota for user " + idStr)
		}
		out[id] = q
	}
	return out, nil
}

// handleAdminQuotasSubmit saves the edited quotas, then sends the admin back to
// the same view.
func (s *server) handleAdminQuotasSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	quotas, err := parseQuotas(r.PostForm)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	err = s.withWriteTx(r.Context(), "admin-quotas", func(ctx context.Context, tx *sql.Tx) error {
		return saveQuotas(ctx, tx, quotas, time.Now())
	})
	if err != nil {
		handleErr(w, err)
		return
	}
	http.Redirect(w, r, r.URL.RequestURI(), http.StatusSeeOther)
}

// saveQuotas writes every changed quota. A value that shows as the same MB
// figure (two decimals, as mbNum rounds) is left alone, so saving an untouched
// row never rewrites a quota set in bytes that the page rounds. Unknown ids
// match no row.
func saveQuotas(ctx context.Context, tx *sql.Tx, quotas map[int64]int64, now time.Time) error {
	for id, q := range quotas {
		if _, err := tx.ExecContext(ctx, `
update users set quota_bytes = ?, updated_at = ?
where id = ? and round(quota_bytes * 1.0 / ?, 2) <> round(? * 1.0 / ?, 2)`,
			q, rfc3339(now), id, bytesPerMiB, q, bytesPerMiB); err != nil {
			return err
		}
	}
	return nil
}

// loadAdminUsers reads every account, then prices each one's storage. The
// per-user sum runs after the row scan, not inside it, so the two queries never
// share a connection.
func (s *server) loadAdminUsers(ctx context.Context) ([]adminUserRow, error) {
	rows, err := s.db.QueryContext(ctx, `
select u.id, u.username, coalesce(u.telegram_username, ''), u.quota_bytes,
       coalesce((select max(s.last_seen_at) from sessions s where s.user_id = u.id), '')
from users u
order by u.created_at desc, u.id desc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []adminUserRow
	for rows.Next() {
		var (
			u        adminUserRow
			username sql.NullString
			lastSeen string
		)
		if err := rows.Scan(&u.ID, &username, &u.Telegram, &u.Quota, &lastSeen); err != nil {
			return nil, err
		}
		u.Username = username.String
		u.Unlimited = quotaExempt(username)
		u.LastSeenAt = lastSeen
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range out {
		used, err := storageBytes(ctx, s.db, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Used = used
	}
	return out, nil
}

// HandleAdminLanding serves /admin — a landing page linking to the admin tools.
func (s *server) HandleAdminLanding(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	s.renderAdminPage(w, adminIndexDoc())
}

// HandleAdminCreateUsers serves /admin/create_users: GET shows the form, POST
// bulk-creates username+password accounts with random one-time passwords and
// renders them once.
func (s *server) HandleAdminCreateUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		s.renderAdminPage(w, adminCreateUsersDoc(adminusers.CreateUsersData{}))
	case http.MethodPost:
		if !sameorigin.Allowed(r, os.Getenv(trustedOriginHostsEnv)) {
			http.Error(w, "bad origin", http.StatusForbidden)
			return
		}
		s.handleAdminCreateUsersSubmit(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// renderAdminPage renders an admin page doc with asset-ref versioning + the
// app's strict CSP (the admin pages only load same-origin styles.css/menu.js).
func (s *server) renderAdminPage(w http.ResponseWriter, doc *ui.Doc) {
	rendered, err := ui.Render(doc)
	if err != nil {
		handleErr(w, err)
		return
	}
	body := s.assets.VersionRefs(rendered)
	w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(body)
}

// adminUserStore is xy's half of the bulk create: xy's users schema, run inside
// the caller's write transaction.
type adminUserStore struct {
	tx  *sql.Tx
	now time.Time
}

func (st adminUserStore) UserExists(ctx context.Context, username string) (bool, error) {
	var id int64
	err := st.tx.QueryRowContext(ctx, `select id from users where username = ?`, username).Scan(&id)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	default:
		return false, err
	}
}

func (st adminUserStore) InsertUser(ctx context.Context, username, passwordHash string) error {
	_, err := st.tx.ExecContext(ctx, `
insert into users(username, password_hash, created_at, updated_at) values(?, ?, ?, ?)`,
		username, passwordHash, rfc3339(st.now), rfc3339(st.now))
	if sqlitex.IsUniqueViolation(err) {
		return adminusers.ErrUserExists
	}
	return err
}

func (s *server) handleAdminCreateUsersSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	usernames := adminusers.ParseUsernameLines(r.PostForm.Get("usernames"))

	now := time.Now()
	var data adminusers.CreateUsersData
	err := s.withWriteTx(r.Context(), "admin-create-users", func(ctx context.Context, tx *sql.Tx) error {
		var err error
		data, err = adminusers.Creator{
			Store:         adminUserStore{tx: tx, now: now},
			Validate:      validNewUsername,
			InvalidReason: xystrings.Default.Admin.CreateUsers.InvalidUsername(),
			Policy:        adminusers.AbortOnRowError,
		}.Create(ctx, usernames)
		return err
	})
	if err != nil {
		handleErr(w, err)
		return
	}
	s.renderAdminPage(w, adminCreateUsersDoc(data))
}
