package server

import (
	"context"
	"net/http"
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
// binary) — no Python. typst runs in-process as a wasm module with its filesystem
// served from memory (internal/chgk/typstwasm), so the decrypted questions and
// their images never touch a disk at all.

const handoutTimeout = 180 * time.Second

func (s *server) handleHandoutsPDF(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxExportRequest)
	form, err := readMultipart(r, maxExportRequest)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	source := normalizeNewlines(form.Value("source"))
	if strings.TrimSpace(source) == "" {
		httpError(w, http.StatusBadRequest, "empty source")
		return
	}
	outName := headerSafeName(safeImageName(form.Value("filename")))
	if outName == "" {
		outName = "handouts"
	}
	outName = strings.TrimSuffix(outName, ".pdf")

	// Referenced images: any inline "img" parts, plus the user's staged session
	// (the common case — the client uploaded them once when the modal opened).
	images := map[string][]byte{}
	for name, data := range s.stagedImages(form.Value("session"), u.UserID) {
		images[name] = data
	}
	for _, f := range form.Files("img") {
		if base := safeImageName(f.Filename); base != "" {
			images[base] = f.Data
		}
	}

	ts, err := s.typesetter()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "typst unavailable: "+err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), handoutTimeout)
	defer cancel()
	pdf, err := handout.Render(ctx, source, images, handout.DefaultArgs(), ts)
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

// ── split-fit (Go: per-block typst fit → per-question + all-questions PDFs) ──
// Ports chgksuite's `handouts split_fit` (handout.SplitFit): each handout block
// is paged to the densest 1-page layout (binary search via typst's own
// pagination), rendered, compressed (pdfcpu) and zipped. No Python.

const splitFitTimeout = 300 * time.Second

func (s *server) handleHandoutsSplitFit(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxExportRequest)
	form, err := readMultipart(r, maxExportRequest)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	source := normalizeNewlines(form.Value("source"))
	if strings.TrimSpace(source) == "" {
		httpError(w, http.StatusBadRequest, "empty source")
		return
	}
	outName := headerSafeName(safeImageName(form.Value("filename")))
	if outName == "" {
		outName = "handouts"
	}
	outName = strings.TrimSuffix(outName, ".zip")

	// Referenced images: staged session images + any inline "img" parts.
	images := map[string][]byte{}
	for name, data := range s.stagedImages(form.Value("session"), u.UserID) {
		images[name] = data
	}
	for _, f := range form.Files("img") {
		if base := safeImageName(f.Filename); base != "" {
			images[base] = f.Data
		}
	}

	ts, err := s.typesetter()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "typst unavailable: "+err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), splitFitTimeout)
	defer cancel()
	zipped, err := handout.SplitFit(ctx, source, images, handout.DefaultArgs(), ts)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			httpError(w, http.StatusGatewayTimeout, "split_fit timed out")
			return
		}
		httpError(w, http.StatusInternalServerError, "split_fit failed: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+outName+".zip\"")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(zipped)))
	_, _ = w.Write(zipped)
}
