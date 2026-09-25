package pages

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"dope/dope/platform/util"
	"dope/dope/storage/store"
	"dope/dope/web/route"
	ui "dope/dope/web/ui"
	dopestrings "dope/i18nstrings"

	"pecheny.me/dopecore/authcred"
	corei18n "pecheny.me/dopecore/i18nstrings"
	"pecheny.me/dopecore/session"
)

// A reset link works for this long, and once.
const passwordResetLifetime = 72 * time.Hour

const passwordResetTokenBytes = 20

type adminPasswordResetData struct {
	Username string
	Link     string // set once a link was made for Username
	Error    string
}

// adminPasswordResetDoc builds /admin/password_reset: the username form, and
// after a submit the link to send or the reason there is none.
func adminPasswordResetDoc(data adminPasswordResetData) *ui.Doc {
	s := dopestrings.Default
	page := []ui.Item{
		ui.Title(s.Admin.PasswordReset.Title()), ui.PagePublic, ui.Classicscripts("dist/pageforms.js"),
		ui.Publictopbar(Trail(AdminCrumbs(), s.Admin.PasswordReset.Name())),
	}
	if data.Link != "" {
		page = append(page, ui.Section(
			ui.Hint(ui.Text(s.Admin.PasswordReset.LinkLead(data.Username))),
			ui.Field(ui.Label(s.Admin.PasswordReset.LinkLabel()),
				ui.Editor(ui.Rows("2"), ui.Readonly(), ui.Data("select-all", ""), ui.Text(data.Link)),
			),
		))
	}
	if data.Error != "" {
		page = append(page, ui.Section(ui.Hint(ui.HintDanger, ui.Text(data.Error))))
	}
	page = append(page, ui.Section(
		ui.Hint(ui.Text(s.Admin.PasswordReset.Lead())),
		ui.Form(ui.DirCol, ui.SpaceMD, ui.Method("post"), ui.Action("/admin/password_reset"), ui.Autocomplete("off"),
			ui.Field(ui.Label(s.Admin.PasswordReset.UsernameLabel()),
				ui.Textfield(ui.Name("username"), ui.Value(data.Username), ui.Autocapitalize("off"), ui.Spellcheck("false"), ui.Required()),
			),
			ui.Row(ui.Button(ui.Submit(), ui.Text(s.Admin.PasswordReset.Submit()))),
		),
	))
	return &ui.Doc{Nodes: []ui.Node{ui.Page(page...)}}
}

// /admin/password_reset — GET shows the form (?username= fills it in); POST
// makes a one-time link for that user and shows it.
func (s *Server) HandleAdminPasswordReset(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/admin/password_reset" {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		if _, ok := s.requireAdmin(w, r); !ok {
			return
		}
		RenderDoc(w, s.h.Engine().AssetETags, adminPasswordResetDoc(adminPasswordResetData{
			Username: strings.TrimSpace(r.URL.Query().Get("username")),
		}))
	case http.MethodPost:
		admin, ok := s.requireAdmin(w, r)
		if !ok {
			return
		}
		if !route.SameOriginUnsafe(w, r) {
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		data := adminPasswordResetData{Username: strings.TrimSpace(r.PostForm.Get("username"))}
		token, username, err := s.createPasswordReset(r.Context(), data.Username, admin.UserID)
		if msg, ok := corei18n.AsUser(err); ok {
			data.Error = msg
		} else if err != nil {
			route.WriteError(w, r, err)
			return
		} else {
			data.Username = username
			data.Link = publicOrigin(r) + "/reset_password?token=" + url.QueryEscape(token)
		}
		RenderDoc(w, s.h.Engine().AssetETags, adminPasswordResetDoc(data))
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// createPasswordReset makes a link for the account named username (any case)
// and retires that account's older links. It returns the raw token and the
// account's username as stored. An account that cannot be reset is a
// corei18n.User error the page shows.
func (s *Server) createPasswordReset(ctx context.Context, username string, adminID int64) (token, stored string, err error) {
	tx, err := s.h.Engine().BeginWriteTx(ctx)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()
	userID, err := store.UserIDByName(ctx, tx, username)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", corei18n.User(dopestrings.Default.Admin.PasswordReset.NoSuchUser(username))
	}
	if err != nil {
		return "", "", err
	}
	var isSystem int
	if err = tx.QueryRowContext(ctx, `select username, is_system from users where id = ?`, userID).Scan(&stored, &isSystem); err != nil {
		return "", "", err
	}
	if isSystem == 1 {
		return "", "", corei18n.User(dopestrings.Default.Admin.PasswordReset.SystemUser())
	}
	if token, err = authcred.RandomBase32(passwordResetTokenBytes); err != nil {
		return "", "", err
	}
	now := time.Now().UTC()
	if _, err = tx.ExecContext(ctx, `delete from password_resets where user_id = ?`, userID); err != nil {
		return "", "", err
	}
	if _, err = tx.ExecContext(ctx, `
insert into password_resets(token_hash, user_id, created_by, created_at, expires_at)
values(?, ?, ?, ?, ?)`, authcred.HashSessionToken(token), userID, adminID,
		now.Format(time.RFC3339), now.Add(passwordResetLifetime).Format(time.RFC3339)); err != nil {
		return "", "", err
	}
	if err = tx.Commit(); err != nil {
		return "", "", err
	}
	return token, stored, nil
}

// publicOrigin is the scheme and host the request came in on; behind Caddy the
// scheme comes from X-Forwarded-Proto.
func publicOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

type resetPasswordData struct {
	Token    string
	Username string // empty when the link does not work
	Error    string
}

// resetPasswordDoc builds /reset_password: the new-password form for a working
// link, or a note saying the link does not work.
func resetPasswordDoc(data resetPasswordData) *ui.Doc {
	s := dopestrings.Default
	page := []ui.Item{ui.Title(s.Auth.Reset.Title()), ui.PagePublic,
		ui.Publictopbar(Trail([]ui.Item{HomeCrumb()}, s.Auth.Reset.Title())),
	}
	if data.Username == "" {
		page = append(page, ui.Empty(ui.Text(s.Auth.Reset.LinkInvalid())))
		return &ui.Doc{Nodes: []ui.Node{ui.Page(page...)}}
	}
	section := []ui.Item{ui.Hint(ui.Text(s.Auth.Reset.Lead(data.Username)))}
	if data.Error != "" {
		section = append(section, ui.Hint(ui.HintDanger, ui.Text(data.Error)))
	}
	section = append(section, ui.Form(ui.DirCol, ui.Method("post"), ui.Action("/reset_password"),
		ui.Hiddenfield(ui.Name("token"), ui.Value(data.Token)),
		ui.Password(ui.Name("new_password"), ui.Placeholder(s.Auth.Reset.NewPlaceholder()),
			ui.Autocomplete("new-password"), ui.Minlength(strconv.Itoa(authcred.PasswordMinLen)), ui.Required()),
		ui.Password(ui.Name("confirm_password"), ui.Placeholder(s.Auth.Reset.ConfirmPlaceholder()),
			ui.Autocomplete("new-password"), ui.Required()),
		ui.Button(ui.Submit(), ui.Text(s.Auth.Reset.Submit())),
	))
	page = append(page, ui.Section(section...))
	return &ui.Doc{Nodes: []ui.Node{ui.Page(page...)}}
}

// /reset_password — GET shows the form for a working link; POST sets the new
// password, burns the link, ends the account's other sessions and logs in.
func (s *Server) HandleResetPassword(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/reset_password" {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		token := r.URL.Query().Get("token")
		_, username, err := lookupPasswordReset(r.Context(), s.h.Engine().DB, token, time.Now().UTC())
		if err != nil {
			route.WriteError(w, r, err)
			return
		}
		RenderDoc(w, s.h.Engine().AssetETags, resetPasswordDoc(resetPasswordData{Token: token, Username: username}))
	case http.MethodPost:
		if !route.SameOriginUnsafe(w, r) {
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		s.handleResetPasswordSubmit(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleResetPasswordSubmit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	token := r.PostForm.Get("token")
	password := r.PostForm.Get("new_password")
	now := time.Now().UTC()

	tx, err := s.h.Engine().BeginWriteTx(ctx)
	if err != nil {
		route.WriteError(w, r, err)
		return
	}
	defer tx.Rollback()
	userID, username, err := lookupPasswordReset(ctx, tx, token, now)
	if err != nil {
		route.WriteError(w, r, err)
		return
	}
	data := resetPasswordData{Token: token, Username: username}
	switch {
	case username == "":
	case len(password) < authcred.PasswordMinLen:
		data.Error = dopestrings.Default.Auth.Password.TooShort(strconv.Itoa(authcred.PasswordMinLen))
	case len(password) > authcred.PasswordMaxLen:
		data.Error = dopestrings.Default.Auth.Password.TooLong(strconv.Itoa(authcred.PasswordMaxLen))
	case password != r.PostForm.Get("confirm_password"):
		data.Error = dopestrings.Default.Auth.Reset.Mismatch()
	}
	if username == "" || data.Error != "" {
		RenderDoc(w, s.h.Engine().AssetETags, resetPasswordDoc(data))
		return
	}

	hashed, err := authcred.HashPassword(password)
	if err != nil {
		route.WriteError(w, r, err)
		return
	}
	var sessionToken string
	err = func() error {
		if _, err := tx.ExecContext(ctx, `
update users set password_hash = ?, password_salt = null, updated_at = ? where id = ?`,
			hashed, util.UtcNow(), userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
update password_resets set used_at = ? where token_hash = ?`, now.Format(time.RFC3339), authcred.HashSessionToken(token)); err != nil {
			return err
		}
		// Whoever knew the old password is logged out everywhere.
		if _, err := tx.ExecContext(ctx, `delete from sessions where user_id = ?`, userID); err != nil {
			return err
		}
		var err error
		sessionToken, err = authcred.CreateSession(ctx, tx, userID, now)
		return err
	}()
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		route.WriteError(w, r, err)
		return
	}
	session.SetCookie(w, sessionToken)
	http.Redirect(w, r, "/host", http.StatusSeeOther)
}

// lookupPasswordReset resolves a token to its account. A token that is
// unknown, used or expired gives an empty username and no error.
func lookupPasswordReset(ctx context.Context, q store.RowQueryer, token string, now time.Time) (int64, string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return 0, "", nil
	}
	var (
		userID   int64
		username sql.NullString
	)
	err := q.QueryRowContext(ctx, `
select u.id, u.username
from password_resets p join users u on u.id = p.user_id
where p.token_hash = ? and p.used_at is null and p.expires_at > ? and u.is_system = 0`,
		authcred.HashSessionToken(token), now.Format(time.RFC3339)).Scan(&userID, &username)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !username.Valid) {
		return 0, "", nil
	}
	if err != nil {
		return 0, "", err
	}
	return userID, username.String, nil
}
