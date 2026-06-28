package server

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Handout PDF generation. The client builds a chgksuite ".hndt" handouts file
// (porting `chgksuite handouts 4s2hndt` in chgk.js) from the list's questions,
// lets the user edit it, then posts it here with any referenced image files.
// The server renders it to a PDF via `chgksuite handouts hndt2pdf` (which shells
// out to tectonic) and streams the result, wiping the scratch dir immediately.
// Symmetric with handleExportDocx — this is the other place plaintext briefly
// reaches the server (PLAN risk note), so the temp dir is removed on return.

// hndt2pdf can pull/compile fonts via tectonic on first use, so it gets a longer
// budget than docx export.
const handoutTimeout = 180 * time.Second

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

	dir, err := os.MkdirTemp("", "xy-handouts-*")
	if err != nil {
		handleErr(w, err)
		return
	}
	defer os.RemoveAll(dir) // brief plaintext exposure — wiped on return

	// Write referenced images (multipart "img" parts) by their base name.
	if r.MultipartForm != nil {
		for _, fh := range r.MultipartForm.File["img"] {
			base := safeImageName(fh.Filename)
			if base == "" {
				continue
			}
			if err := saveUpload(fh, filepath.Join(dir, base)); err != nil {
				handleErr(w, err)
				return
			}
		}
	}

	srcPath := filepath.Join(dir, "source.hndt")
	if err := os.WriteFile(srcPath, []byte(source), 0o600); err != nil {
		handleErr(w, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), handoutTimeout)
	defer cancel()
	argv := append(chgksuiteCommand(), "handouts", "hndt2pdf", "source.hndt")
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	combined, runErr := cmd.CombinedOutput()
	if runErr != nil {
		if ctx.Err() == context.DeadlineExceeded {
			httpError(w, http.StatusGatewayTimeout, "chgksuite timed out")
			return
		}
		msg := strings.TrimSpace(string(combined))
		if msg == "" {
			msg = runErr.Error()
		}
		httpError(w, http.StatusInternalServerError, "chgksuite failed: "+msg)
		return
	}

	pdf := findPDF(dir)
	if pdf == "" {
		httpError(w, http.StatusInternalServerError, "chgksuite produced no .pdf")
		return
	}
	f, err := os.Open(pdf)
	if err != nil {
		handleErr(w, err)
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+outName+".pdf\"")
	w.Header().Set("Cache-Control", "private, no-store")
	if st, err := f.Stat(); err == nil {
		w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
	}
	_, _ = io.Copy(w, f)
}

// findPDF returns the first .pdf in dir (chgksuite writes the output alongside
// the source).
func findPDF(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".pdf") {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}
