package server

import (
	"net/http"
	"strings"

	"xy/internal/chgk/typoedit"
)

type typoRequest struct {
	Text string `json:"text"`
}

type typoResponse struct {
	Text string `json:"text"`
}

// handleTypo runs one card's 4s through the typography pass behind the editor's
// «типограф» button (chgk/typoedit: quotes, dashes, non-breaking spaces and
// hyphens). The pass is chgksuite's, ported to Go, which is why it can't run in
// the browser.
//
// Like the export, handout and import endpoints, this one necessarily sees
// plaintext, and like them it keeps none of it: the text is typographed in memory
// and handed straight back for the client to encrypt under the board key.
func (s *server) handleTypo(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	var req typoRequest
	if !readJSON(w, r, &req) {
		return
	}
	// Browsers send textarea content with CRLF; the pass is line-oriented.
	text := strings.ReplaceAll(req.Text, "\r\n", "\n")
	writeJSON(w, typoResponse{Text: typoedit.Pass(text)})
}
