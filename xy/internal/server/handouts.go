package server

import (
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"xy/internal/chgk/handout"
)

// Handout PDF generation. The client builds a chgksuite ".hndt" handouts file
// (porting `chgksuite handouts 4s2hndt` in chgk.js) from the list's questions,
// lets the user edit it, then posts it here with any referenced image files.
// The server renders it to a PDF in-process via the Go handout package (which
// emits the same Typst document chgksuite would and compiles it with the typst
// binary) — no Python, ~60× faster than shelling out to chgksuite. Plaintext only
// lives in the render's scratch dir, wiped before this returns (PLAN risk note).

const handoutTimeout = 180 * time.Second

// typstCommand returns the typst binary path (XY_TYPST_CMD, default "typst").
func typstCommand() string {
	if raw := strings.TrimSpace(os.Getenv("XY_TYPST_CMD")); raw != "" {
		return raw
	}
	return "typst"
}

func (s *server) handleHandoutsPDF(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxExportRequest)
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		httpError(w, http.StatusBadRequest, "bad multipart form")
		return
	}
	source := r.FormValue("source")
	if strings.TrimSpace(source) == "" {
		httpError(w, http.StatusBadRequest, "empty source")
		return
	}
	outName := headerSafeName(safeImageName(r.FormValue("filename")))
	if outName == "" {
		outName = "handouts"
	}
	outName = strings.TrimSuffix(outName, ".pdf")

	// Referenced images (multipart "img" parts), keyed by base name.
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

	ctx, cancel := context.WithTimeout(r.Context(), handoutTimeout)
	defer cancel()
	pdf, err := handout.Render(ctx, source, images, handout.DefaultArgs(), typstCommand())
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			httpError(w, http.StatusGatewayTimeout, "typst timed out")
			return
		}
		httpError(w, http.StatusInternalServerError, "handout render failed: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+outName+".pdf\"")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(pdf)))
	_, _ = w.Write(pdf)
}

// readUpload reads a multipart file part fully into memory.
func readUpload(fh *multipart.FileHeader) ([]byte, error) {
	src, err := fh.Open()
	if err != nil {
		return nil, err
	}
	defer src.Close()
	return io.ReadAll(src)
}
