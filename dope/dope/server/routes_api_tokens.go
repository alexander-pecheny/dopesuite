package dopeserver

import (
	"errors"
	"net/http"

	"dope/dope/domain/core"
	"dope/dope/web/route"

	"code.pecheny.me/pecheny/dopesuite/dopecore/idstr"
)

// The caller's own API tokens (ADR-0021). A token may list and mint its
// siblings, as in xy: a leaked token is the whole account either way, and the
// answer to a leak is changing the password, which revokes them all.

func (s *server) apiTokensList(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	tokens, err := s.eng.ListAPITokens(r.Context(), sc.User.UserID)
	if err != nil {
		return err
	}
	return route.JSON(w, tokens)
}

func (s *server) apiTokensCreate(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	var req struct {
		Label string `json:"label"`
	}
	if err := route.DecodeJSON(r, &req); err != nil {
		return err
	}
	created, err := s.eng.CreateAPIToken(r.Context(), sc.User.UserID, req.Label)
	if err != nil {
		return err
	}
	return route.JSON(w, created)
}

func (s *server) apiTokensRevoke(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	id, err := idstr.Parse(r.PathValue("id"))
	if err != nil || id <= 0 {
		return route.BadRequest("bad token id")
	}
	if err := s.eng.RevokeAPIToken(r.Context(), sc.User.UserID, id); errors.Is(err, core.ErrNoAPIToken) {
		return route.NotFound
	} else if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// The caller's token for the rating site, as /profile shows it: when it was
// saved and how it ends, never the token itself.
func (s *server) apiRatingTokenShow(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	saved, ok, err := s.eng.LoadRatingToken(r.Context(), sc.User.UserID)
	if err != nil {
		return err
	}
	if !ok {
		return route.NotFound
	}
	return route.JSON(w, map[string]string{"saved_at": saved.SavedAt, "tail": saved.Tail()})
}

func (s *server) apiRatingTokenSave(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	var req struct {
		Token string `json:"token"`
	}
	if err := route.DecodeJSON(r, &req); err != nil {
		return err
	}
	if err := s.hostPageServer().SaveRatingToken(r.Context(), sc.User.UserID, req.Token); err != nil {
		return err
	}
	return s.apiRatingTokenShow(w, r, sc)
}

func (s *server) apiRatingTokenForget(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	if err := s.eng.ForgetRatingToken(r.Context(), sc.User.UserID); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
