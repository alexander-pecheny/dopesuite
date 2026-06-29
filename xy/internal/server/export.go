package server

import (
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"xy/internal/chgk/docx"
	"xy/internal/chgk/fsource"
)

// Export turns a list of (already-decrypted, client-supplied) chgksuite "4s"
// source text plus its referenced image files into a .docx via the external
// `chgksuite` tool. This is the one place where plaintext board content reaches
// the server: the client decrypts the descriptions + attachments, posts them,
// the server composes the docx, returns it, and deletes the scratch files
// immediately (PLAN risk note — brief, tolerated plaintext exposure).
//
// The compose command is configurable via XY_CHGKSUITE_CMD (space-separated,
// default "chgksuite"); the server appends `compose docx source.4s` and runs it
// in a fresh temp dir whose only other contents are the uploaded images.

const exportTimeout = 60 * time.Second

// maxExportRequest bounds the whole multipart export upload (4s source + images)
// so a single request can't exhaust memory/temp disk during parsing.
const maxExportRequest = 64 << 20

// chgksuiteCommand returns the configured compose command tokens.
func chgksuiteCommand() []string {
	raw := strings.TrimSpace(os.Getenv("XY_CHGKSUITE_CMD"))
	if raw == "" {
		return []string{"chgksuite"}
	}
	return strings.Fields(raw)
}

// safeImageName reduces a client-supplied filename to a path-free base name,
// rejecting empties and traversal attempts. Images in the 4s source are
// referenced by base name, so this is what chgksuite will look for.
func safeImageName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
		return ""
	}
	return name
}

// headerSafeName strips characters that could break out of the quoted-string
// value of a Content-Disposition header (quotes, backslashes, control bytes).
// safeImageName has already removed path separators.
func headerSafeName(name string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '"' || r == '\\' {
			return -1
		}
		return r
	}, name)
}

// reImgRef detects an (img …) directive — docx with images still goes through
// chgksuite (the Go docx exporter doesn't embed images yet).
var reImgRef = regexp.MustCompile(`\(img\b`)

func (s *server) handleExportDocx(w http.ResponseWriter, r *http.Request) {
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
		outName = "export"
	}
	outName = strings.TrimSuffix(outName, ".docx")

	// Fast path: no images → render the .docx in-process (Go, no Python).
	if !reImgRef.MatchString(source) {
		b, err := docx.Export(fsource.Parse(source, "chgk"))
		if err != nil {
			httpError(w, http.StatusInternalServerError, "docx export failed: "+err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
		w.Header().Set("Content-Disposition", "attachment; filename=\""+outName+".docx\"")
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		_, _ = w.Write(b)
		return
	}

	dir, err := os.MkdirTemp("", "xy-export-*")
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

	srcPath := filepath.Join(dir, "source.4s")
	if err := os.WriteFile(srcPath, []byte(source), 0o600); err != nil {
		handleErr(w, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), exportTimeout)
	defer cancel()
	argv := append(chgksuiteCommand(), "compose", "docx", "--ignore_missing_images", "source.4s")
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
		// exec error before producing output (e.g. binary missing) → 500 with detail.
		httpError(w, http.StatusInternalServerError, "chgksuite failed: "+msg)
		return
	}

	docx := findDocx(dir)
	if docx == "" {
		httpError(w, http.StatusInternalServerError, "chgksuite produced no .docx")
		return
	}
	f, err := os.Open(docx)
	if err != nil {
		handleErr(w, err)
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+outName+".docx\"")
	w.Header().Set("Cache-Control", "private, no-store")
	if st, err := f.Stat(); err == nil {
		w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
	}
	_, _ = io.Copy(w, f)
}

// saveUpload streams a multipart file part's bytes to dst.
func saveUpload(fh *multipart.FileHeader, dst string) error {
	src, err := fh.Open()
	if err != nil {
		return err
	}
	defer src.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, src)
	return err
}

// findDocx returns the first .docx in dir (other than nothing), preferring one
// not named source.4s. chgksuite writes the output alongside the source.
func findDocx(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".docx") {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}
