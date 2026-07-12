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
