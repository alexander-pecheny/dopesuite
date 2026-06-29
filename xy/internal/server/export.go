package server

import (
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"xy/internal/chgk/docx"
	"xy/internal/chgk/fsource"
)

// Export turns the client-supplied chgksuite "4s" source (the list's decrypted
// card descriptions concatenated in board order) plus its referenced images into
// a .docx — entirely in-process via the Go chgk/docx package (no Python). This is
// one of the two places plaintext briefly reaches the server (PLAN risk note);
// nothing is written to disk — the docx is built in memory and streamed.

// maxExportRequest bounds the whole multipart export upload (4s source + images)
// so a single request can't exhaust memory during parsing.
const maxExportRequest = 64 << 20

// normalizeNewlines collapses CRLF/CR to LF. Browsers normalize textarea/text
// field values to CRLF in multipart/form-data, which would otherwise leave a
// stray \r on every line — breaking the 4s parser and the .hndt "---" block
// separators (server parsers split on \n).
func normalizeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

// safeImageName reduces a client-supplied filename to a path-free base name,
// rejecting empties and traversal attempts. Images in the 4s source are
// referenced by base name, so this is what the (img …) directives look for.
func safeImageName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
		return ""
	}
	return name
}

// headerSafeName strips characters that could break out of the quoted-string
// value of a Content-Disposition header (quotes, backslashes, control bytes).
func headerSafeName(name string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '"' || r == '\\' {
			return -1
		}
		return r
	}, name)
}

func (s *server) handleExportDocx(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxExportRequest)
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		httpError(w, http.StatusBadRequest, "bad multipart form")
		return
	}
	source := normalizeNewlines(r.FormValue("source"))
	if strings.TrimSpace(source) == "" {
		httpError(w, http.StatusBadRequest, "empty source")
		return
	}
	outName := headerSafeName(safeImageName(r.FormValue("filename")))
	if outName == "" {
		outName = "export"
	}
	outName = strings.TrimSuffix(outName, ".docx")

	// Referenced images (multipart "img" parts), keyed by base name; the docx
	// exporter re-encodes them to PNG and embeds them.
	images := map[string][]byte{}
	if r.MultipartForm != nil {
		for _, fh := range r.MultipartForm.File["img"] {
			base := safeImageName(fh.Filename)
			if base == "" {
				continue
			}
			data, err := readUpload(fh)
			if err != nil {
				handleErr(w, err)
				return
			}
			images[base] = data
		}
	}

	b, err := docx.Export(fsource.Parse(source, "chgk"), images)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "docx export failed: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+outName+".docx\"")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	_, _ = w.Write(b)
}
