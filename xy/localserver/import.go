package localserver

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"code.pecheny.me/pecheny/dopesuite/chgksuite/chgkimport"
	"code.pecheny.me/pecheny/dopesuite/chgksuite/listexport"

	"xy/internal/rank"
)

// blockGap separates cards in 4s source, as the browser's import splits it.
var blockGap = regexp.MustCompile(`\n[ \t]*\n`)

// Import makes a new board out of the package at path and returns its id. A
// file becomes a board with one list. A folder becomes a board with a list per
// .4s file in it, in name order, and their pictures are read from beside them.
//
// It writes through the server's own handlers, as the browser's import does,
// so a local board made here is no different from one made there.
func (s *Server) Import(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	var parts []*chgkimport.Result
	if info.IsDir() {
		if parts, err = openFolder(path); err != nil {
			return 0, err
		}
	} else {
		res, err := chgkimport.Open(path)
		if err != nil {
			return 0, err
		}
		parts = []*chgkimport.Result{res}
	}
	name := parts[0].Name
	if info.IsDir() {
		name = filepath.Base(path)
	}
	board, err := s.post("/api/boards", map[string]string{
		"name": name, "kdf_params": `{"kdf":"none"}`, "kdf_salt": "", "wrapped_key": "", "verify_token": "",
	})
	if err != nil {
		return 0, err
	}
	listRank := ""
	for _, part := range parts {
		if listRank, err = rank.After(listRank); err != nil {
			return 0, err
		}
		list, err := s.post(fmt.Sprintf("/api/boards/%d/lists", board), map[string]string{
			"title": plain(part.Name), "rank": listRank, "type": "normal",
		})
		if err != nil {
			return 0, err
		}
		if err := s.addCards(list, part); err != nil {
			return 0, err
		}
	}
	return board, nil
}

func openFolder(dir string) ([]*chgkimport.Result, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.4s"))
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%s has no .4s files", dir)
	}
	slices.Sort(files)
	parts := make([]*chgkimport.Result, 0, len(files))
	for _, f := range files {
		res, err := chgkimport.Open(f)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(f), err)
		}
		parts = append(parts, res)
	}
	return parts, nil
}

// addCards makes a card of every block of part's source, and attaches to each
// the pictures it names.
func (s *Server) addCards(list int64, part *chgkimport.Result) error {
	images := map[string]chgkimport.Image{}
	for _, im := range part.Images {
		images[im.Name] = im
	}
	cardRank := ""
	for _, block := range blockGap.Split(part.Source, -1) {
		desc := strings.TrimSpace(block)
		if desc == "" {
			continue
		}
		var err error
		if cardRank, err = rank.After(cardRank); err != nil {
			return err
		}
		card, err := s.post(fmt.Sprintf("/api/lists/%d/cards", list), map[string]string{
			"description": plain(desc), "rank": cardRank, "kind": listexport.ImportKind(desc),
		})
		if err != nil {
			return err
		}
		for _, ref := range listexport.ImageRefs(desc) {
			if im, ok := images[ref]; ok {
				if err := s.attach(card, im); err != nil {
					return fmt.Errorf("%s: %w", ref, err)
				}
			}
		}
	}
	return nil
}

func (s *Server) attach(card int64, im chgkimport.Image) error {
	meta, err := json.Marshal(map[string]any{
		"filename": plain(im.Name), "mime": im.MIME, "lossless": true,
		"event_payload": plain(fmt.Sprintf(`{"file":%q}`, im.Name)),
	})
	if err != nil {
		return err
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if err := mw.WriteField("meta", string(meta)); err != nil {
		return err
	}
	blob, err := mw.CreateFormFile("blob", "blob")
	if err != nil {
		return err
	}
	if _, err := blob.Write(im.Data); err != nil {
		return err
	}
	if err := mw.Close(); err != nil {
		return err
	}
	_, err = s.serve(fmt.Sprintf("/api/cards/%d/attachments", card), mw.FormDataContentType(), &body)
	return err
}

// post sends a JSON body to one of the handlers and returns the id it made.
func (s *Server) post(path string, body any) (int64, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	return s.serve(path, "application/json", bytes.NewReader(data))
}

func (s *Server) serve(path, contentType string, body io.Reader) (int64, error) {
	req := httptest.NewRequest(http.MethodPost, path, body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	s.s.Handler().ServeHTTP(rec, req)
	if rec.Code >= http.StatusMultipleChoices {
		return 0, fmt.Errorf("%s: %d %s", path, rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		return 0, fmt.Errorf("%s: %w", path, err)
	}
	return created.ID, nil
}

// plain is a content field of a local board on the wire: no encryption, so
// just the text in base64.
func plain(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
