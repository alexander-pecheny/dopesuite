package qyserver

import (
	"context"
	"database/sql"
	"net/http"

	"code.pecheny.me/pecheny/dopesuite/dopecore/authcred"
	"code.pecheny.me/pecheny/dopesuite/dopecore/session"

	"qy/qy/web/route"
	"qy/qy/web/ui"
)

// The account page the kit's menu links to: who you are logged in as, your
// rating id, and the way out.

func (s *server) handleProfilePage(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	rating, err := s.ratingID(r.Context(), sc.User.UserID)
	if err != nil {
		return err
	}
	who := sc.User.Username.String
	if sc.User.Telegram.Valid && sc.User.Telegram.String != "" {
		who = "@" + sc.User.Telegram.String
	}
	return s.ok(w, r, view{
		title: str.Profile.Title(),
		trail: []crumb{{"", str.Profile.Title()}},
		body: []ui.Item{
			ui.Section(append([]ui.Item{hint(str.Profile.LoggedInAs(who))}, noticeOf(r)...)...),
			ui.Section(ui.Form(ui.Method("post"), ui.Action("/profile"), ui.DirCol, ui.SpaceSM,
				ui.Field(ui.Label(str.Common.RatingId()), ui.Textfield(ui.Name("rating_id"), ui.Value(rating), ui.Inputmode("numeric"), ui.Required())),
				hint(str.Profile.RatingHint()),
				ui.Row(ui.Button(ui.Submit(), ui.Primary, text(str.Common.Save()))))),
			ui.Section(ui.Form(ui.Method("post"), ui.Action("/logout"),
				ui.Button(ui.Submit(), ui.Ghost, ui.IconLogIn, text(str.Profile.Logout())))),
		},
	})
}

func (s *server) handleProfileSave(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	if err := parseForm(w, r); err != nil {
		return err
	}
	rating, err := parseRatingID(r.FormValue("rating_id"))
	if err != nil {
		return err
	}
	err = s.withWriteTx(r.Context(), "profile", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `update users set rating_id = ?, updated_at = ? where id = ?`, rating, s.stamp(), sc.User.UserID)
		return err
	})
	if err != nil {
		return err
	}
	return seeOther(w, r, "/profile?done="+doneSaved)
}

func (s *server) handleLogoutForm(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	logDropped("logout", s.withWriteTx(r.Context(), "logout", func(ctx context.Context, tx *sql.Tx) error {
		return authcred.DeleteSession(ctx, tx, sc.User.SessionID)
	}))
	session.ClearCookie(w)
	return seeOther(w, r, "/")
}
