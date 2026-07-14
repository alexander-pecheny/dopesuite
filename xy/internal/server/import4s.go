package server

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"

	"xy/internal/chgk/chgkimport"
)

// maxImportUpload bounds an uploaded package. A .docx of a full tournament with
// handout images runs to a few MB; 64 MB is generous and matches the export cap.
const maxImportUpload = 64 << 20

// importedImage is one image the package referenced, base64'd so the browser can
// encrypt it and upload it as a card attachment without a second round trip.
type importedImage struct {
	Name string `json:"name"`
	MIME string `json:"mime"`
	Data string `json:"data"` // base64
}

type importResponse struct {
	Name   string          `json:"name"`
	Source string          `json:"source"`
	Images []importedImage `json:"images"`
}

type textParseRequest struct {
	Text string `json:"text"`
}

type textParseResponse struct {
	Source string `json:"source"`
}

// handleImportText turns one card's plain text — a question pasted as prose,
// "Вопрос 1: … Ответ: … Автор: …" — into 4s source. It is handleImportParse's
// pipeline without the file: the same chgk text parser, run on text the client
// already holds in plaintext.
//
// Like the other parse endpoints, this handler necessarily sees plaintext, and
// like them it keeps none of it: the text is parsed in memory and the 4s handed
// straight back for the client to encrypt under the board key.
func (s *server) handleImportText(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	var req textParseRequest
	if !readJSON(w, r, &req) {
		return
	}
	source := chgkimport.ParseText(req.Text)
	if source == "" {
		httpError(w, http.StatusBadRequest, "в тексте не найдено вопросов")
		return
	}
	writeJSON(w, textParseResponse{Source: source})
}

// handleImportParse parses an uploaded question package (.4s, .zip or .docx) into
// 4s source plus its images. This is the read side of chgksuite's `parse` command,
// ported to Go (internal/chgk/chgkimport).
//
// Like the docx export and handout endpoints, this handler sees the package in
// plaintext — it has to, since only the server can parse a .docx. Nothing is
// persisted: the file is parsed in memory and the result is handed straight back
// for the client to encrypt under the board key. The server never learns the
// board's passphrase, and no plaintext touches the database or the disk.
func (s *server) handleImportParse(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxImportUpload)
	form, err := readMultipart(r, maxImportUpload)
	if err != nil {
		httpError(w, http.StatusBadRequest, "не удалось прочитать файл")
		return
	}
	files := form.Files("file")
	if len(files) == 0 {
		httpError(w, http.StatusBadRequest, "файл не выбран")
		return
	}

	res, err := chgkimport.Parse(files[0].Filename, files[0].Data)
	if err != nil {
		if errors.Is(err, chgkimport.ErrUnsupported) {
			httpError(w, http.StatusBadRequest, "поддерживаются только .4s, .zip и .docx")
			return
		}
		httpError(w, http.StatusBadRequest, "не удалось разобрать файл: "+err.Error())
		return
	}
	// Give the images names the (img …) directives can actually reference.
	res.SafeImageNames()

	if strings.TrimSpace(res.Source) == "" {
		httpError(w, http.StatusBadRequest, "в файле не найдено вопросов")
		return
	}

	out := importResponse{Name: res.Name, Source: res.Source, Images: []importedImage{}}
	for _, img := range res.Images {
		out.Images = append(out.Images, importedImage{
			Name: img.Name,
			MIME: img.MIME,
			Data: base64.StdEncoding.EncodeToString(img.Data),
		})
	}
	writeJSON(w, out)
}
