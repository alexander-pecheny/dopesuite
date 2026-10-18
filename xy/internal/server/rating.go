package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	corei18n "code.pecheny.me/pecheny/dopesuite/dopecore/i18nstrings"
	xystrings "xy/i18nstrings"
)

// The rating site relay (ADR-0021). The CSP keeps the page off
// api.rating.chgk.info, so an appeals list asks the server to make the call.
// The token arrives in the body, lives for the length of the request, and
// nothing the site answers is stored here: the page encrypts it into cards.

const ratingAPI = "https://api.rating.chgk.info"

const (
	ratingTimeout = 30 * time.Second
	// maxRatingBody caps what the relay reads back. A synchronous tournament
	// with a few hundred teams has a few thousand controversials.
	maxRatingBody = 16 << 20
)

var ratingClient = &http.Client{Timeout: ratingTimeout}

func (s *server) ratingBase() string {
	if s.ratingAPIBase != "" {
		return s.ratingAPIBase
	}
	return ratingAPI
}

// ratingCall makes one request to the site and returns its status and body.
func (s *server) ratingCall(ctx context.Context, method, path, token string, body any) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.ratingBase()+path, rd)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token = strings.TrimSpace(token); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := ratingClient.Do(req)
	if err != nil {
		return 0, nil, corei18n.User(xystrings.Default.Server.Rating.Unreachable())
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxRatingBody))
	return resp.StatusCode, data, err
}

// ratingFailure turns the site's refusal into a line the jury can act on. The
// site words its own refusals (422 carries them in violations), so those are
// passed on as they are.
func ratingFailure(status int, body []byte) error {
	var e struct {
		Detail     string `json:"detail"`
		Violations []struct {
			Message string `json:"message"`
		} `json:"violations"`
	}
	_ = json.Unmarshal(body, &e)
	var said []string
	for _, v := range e.Violations {
		said = append(said, v.Message)
	}
	if len(said) == 0 && e.Detail != "" {
		said = append(said, e.Detail)
	}
	var msg string
	switch status {
	case http.StatusUnauthorized:
		msg = xystrings.Default.Server.Rating.Unauthorized()
	case http.StatusForbidden:
		msg = xystrings.Default.Server.Rating.Forbidden()
	case http.StatusNotFound:
		msg = xystrings.Default.Server.Rating.NotFound()
	case http.StatusConflict:
		msg = xystrings.Default.Server.Rating.Busy()
	default:
		msg = xystrings.Default.Server.Rating.Failed(fmt.Sprint(status))
	}
	if len(said) > 0 {
		msg += ": " + strings.Join(said, "; ")
	}
	return corei18n.User(msg)
}

type ratingAppealsRequest struct {
	TournamentID int64  `json:"tournament_id"`
	Token        string `json:"token"`
}

type ratingAppealsResponse struct {
	Appeals        json.RawMessage `json:"appeals"`
	Controversials json.RawMessage `json:"controversials"`
	// ControversialsError is why the controversials did not come: the appeals
	// are still worth showing without them.
	ControversialsError string `json:"controversials_error,omitempty"`
}

// handleRatingAppeals fetches a tournament's appeals and its controversials.
func (s *server) handleRatingAppeals(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	var req ratingAppealsRequest
	if !readJSON(w, r, &req) {
		return
	}
	if req.TournamentID <= 0 {
		httpError(w, http.StatusBadRequest, xystrings.Default.Server.Rating.NoTournament())
		return
	}
	base := fmt.Sprintf("/tournaments/%d/", req.TournamentID)
	status, appeals, err := s.ratingCall(r.Context(), http.MethodGet, base+"appeals.json", req.Token, nil)
	if err == nil && status != http.StatusOK {
		err = ratingFailure(status, appeals)
	}
	if handleErr(w, err) {
		return
	}
	resp := ratingAppealsResponse{Appeals: appeals, Controversials: json.RawMessage("[]")}
	status, controversials, err := s.ratingCall(r.Context(), http.MethodGet, base+"controversials.json", req.Token, nil)
	switch {
	case err != nil:
		resp.ControversialsError, _ = corei18n.Reveal(err, xystrings.Default.Server.Internal())
	case status != http.StatusOK:
		resp.ControversialsError, _ = corei18n.Reveal(ratingFailure(status, controversials), "")
	default:
		resp.Controversials = controversials
	}
	writeJSON(w, resp)
}

type ratingVerdictRequest struct {
	AppealID int64  `json:"appeal_id"`
	Status   string `json:"status"`
	Comment  string `json:"comment"`
	Token    string `json:"token"`
}

// handleRatingVerdict posts one appeal's verdict and answers with the appeal as
// the site now has it.
func (s *server) handleRatingVerdict(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	var req ratingVerdictRequest
	if !readJSON(w, r, &req) {
		return
	}
	if req.AppealID <= 0 || (req.Status != "A" && req.Status != "D") {
		httpError(w, http.StatusBadRequest, "invalid verdict")
		return
	}
	if strings.TrimSpace(req.Token) == "" {
		httpError(w, http.StatusBadRequest, xystrings.Default.Server.Rating.NoToken())
		return
	}
	body := map[string]string{"status": req.Status, "comment": req.Comment}
	path := fmt.Sprintf("/tournament_synch_appeals/%d/verdict", req.AppealID)
	status, answer, err := s.ratingCall(r.Context(), http.MethodPost, path, req.Token, body)
	if err == nil && (status < http.StatusOK || status >= http.StatusMultipleChoices) {
		err = ratingFailure(status, answer)
	}
	if handleErr(w, err) {
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(answer)
}
