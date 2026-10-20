package server

import (
	"encoding/base64"
	"errors"
	"net/http"
	"os"
	"path/filepath"

	"code.pecheny.me/pecheny/dopesuite/chgksuite/chgkimport"

	xystrings "xy/i18nstrings"
)

// A page in the desktop app's webview cannot open a native dialog, but the
// server it talks to runs in the app's own process and can. So the board's
// import asks here, and gets back the package and the pictures beside it that
// it names: the files a browser would have uploaded, had it seen the folder.

var errPickTooBig = errors.New("picked files too large")

// SetFilePicker installs the app's native file dialog. pick returns the chosen
// path, or "" when the dialog was cancelled. Call it before serving.
func (s *Server) SetFilePicker(pick func() (string, error)) { s.s.pickFile = pick }

func (s *server) handleImportPick(w http.ResponseWriter, r *http.Request) {
	if s.pickFile == nil {
		http.NotFound(w, r)
		return
	}
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	p, err := s.pickFile()
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if p == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	files, err := pickedFiles(p)
	if err != nil {
		httpError(w, http.StatusBadRequest, xystrings.Default.Server.Import.ReadFailed())
		return
	}
	writeJSON(w, struct {
		Files []importedImage `json:"files"`
	}{files})
}

// pickedFiles is the package at p and, for a .4s, the pictures it names from
// the folder it sits in, all under the upload's size cap.
func pickedFiles(p string) ([]importedImage, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	total := len(data)
	if total > maxImportUpload {
		return nil, errPickTooBig
	}
	files := []importedImage{{Name: filepath.Base(p), MIME: "application/octet-stream", Data: base64.StdEncoding.EncodeToString(data)}}
	if !chgkimport.Is4s(p) {
		return files, nil
	}
	images, err := chgkimport.ImagesBeside(filepath.Dir(p), string(data))
	if err != nil {
		return nil, err
	}
	for _, img := range images {
		if total += len(img.Data); total > maxImportUpload {
			return nil, errPickTooBig
		}
		files = append(files, importedImage{Name: img.Name, MIME: img.MIME, Data: base64.StdEncoding.EncodeToString(img.Data)})
	}
	return files, nil
}
