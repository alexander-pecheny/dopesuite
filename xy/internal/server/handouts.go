package server

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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
	source := normalizeNewlines(r.FormValue("source"))
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

// ── split-fit (chgksuite handouts split_fit → zip of per-question PDFs) ──
// split_fit pages each handout block to fit and renders a fitted PDF per block
// plus an all-questions PDF. It's a large pagination algorithm we haven't ported,
// so this one action still shells out to chgksuite (XY_CHGKSUITE_CMD); the venv
// stays installed for it. The produced PDFs are zipped and streamed.

const splitFitTimeout = 300 * time.Second

// chgksuiteCommand returns the configured chgksuite command tokens
// (XY_CHGKSUITE_CMD, default "chgksuite").
func chgksuiteCommand() []string {
	raw := strings.TrimSpace(os.Getenv("XY_CHGKSUITE_CMD"))
	if raw == "" {
		return []string{"chgksuite"}
	}
	return strings.Fields(raw)
}

func (s *server) handleHandoutsSplitFit(w http.ResponseWriter, r *http.Request) {
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
		outName = "handouts"
	}
	outName = strings.TrimSuffix(outName, ".zip")

	dir, err := os.MkdirTemp("", "xy-splitfit-*")
	if err != nil {
		handleErr(w, err)
		return
	}
	defer os.RemoveAll(dir) // brief plaintext exposure — wiped on return

	// Referenced images by base name (split_fit needs them alongside the source).
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
			if err := os.WriteFile(filepath.Join(dir, base), data, 0o600); err != nil {
				handleErr(w, err)
				return
			}
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "source.hndt"), []byte(source), 0o600); err != nil {
		handleErr(w, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), splitFitTimeout)
	defer cancel()
	argv := append(chgksuiteCommand(), "handouts", "split_fit", "source.hndt", "--output_dir", ".")
	// split_fit finds typst via XY_TYPST_CMD's dir / the chgksuite utils dir; the
	// service env already provides it.
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	if combined, runErr := cmd.CombinedOutput(); runErr != nil {
		if ctx.Err() == context.DeadlineExceeded {
			httpError(w, http.StatusGatewayTimeout, "split_fit timed out")
			return
		}
		msg := strings.TrimSpace(string(combined))
		if msg == "" {
			msg = runErr.Error()
		}
		httpError(w, http.StatusInternalServerError, "split_fit failed: "+msg)
		return
	}

	zipped, n, err := zipPDFs(dir)
	if err != nil {
		handleErr(w, err)
		return
	}
	if n == 0 {
		httpError(w, http.StatusInternalServerError, "split_fit produced no PDFs")
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+outName+".zip\"")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(zipped)))
	_, _ = w.Write(zipped)
}

// zipPDFs zips every *.pdf in dir (by base name) and returns the bytes + count.
func zipPDFs(dir string) ([]byte, int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0, err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	n := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".pdf") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, 0, err
		}
		fw, err := zw.Create(e.Name())
		if err != nil {
			return nil, 0, err
		}
		if _, err := fw.Write(data); err != nil {
			return nil, 0, err
		}
		n++
	}
	if err := zw.Close(); err != nil {
		return nil, 0, err
	}
	return buf.Bytes(), n, nil
}
