package dopeserver

import (
	"errors"
	"net/http"
	"strconv"

	"dope/dope/domain/core"
	"dope/dope/web/route"
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
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
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
