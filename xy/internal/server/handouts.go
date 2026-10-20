package server

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"code.pecheny.me/pecheny/dopesuite/chgksuite/handout"
	corei18n "code.pecheny.me/pecheny/dopesuite/dopecore/i18nstrings"

	xystrings "xy/i18nstrings"
)

// Handout PDF generation. The client builds a chgksuite ".hndt" handouts file
// (porting `chgksuite handouts 4s2hndt` in chgk.js) from the list's questions,
// lets the user edit it, then posts it here with any referenced image files and
// the tour's page setup. The server renders it in-process via the Go handout
// package (which emits the same Typst document chgksuite would) — no Python. On
// a server typst runs in-process as a wasm module with its filesystem served
// from memory (chgksuite/typstwasm), so the decrypted questions and their
// images never touch a disk at all.

const handoutTimeout = 180 * time.Second

// maxPackTeams bounds the print run, which is pages per team times teams.
const maxPackTeams = 9999

// handoutRequest is what every handout render is sent: the .hndt, the name to
// hand the file back under, the pictures and the page setup.
type handoutRequest struct {
	source string
	name   string
	images map[string][]byte
	args   handout.Args
}

func (s *server) readHandoutRequest(w http.ResponseWriter, r *http.Request, ext string) (handoutRequest, *memForm, bool) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return handoutRequest{}, nil, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxExportRequest)
	form, err := readMultipart(r, maxExportRequest)
	if err != nil {
		handleUser(w, err)
		return handoutRequest{}, nil, false
	}
	req := handoutRequest{source: normalizeNewlines(form.Value("source")), images: map[string][]byte{}}
	if strings.TrimSpace(req.source) == "" {
		httpError(w, http.StatusBadRequest, "empty source")
		return req, nil, false
	}
	if req.args, err = pageArgs(form); err != nil {
		handleUser(w, err)
		return req, nil, false
	}
	req.name = headerSafeName(safeImageName(form.Value("filename")))
	if req.name == "" {
		req.name = "handouts"
	}
	req.name = strings.TrimSuffix(req.name, ext)
	// Referenced images: any inline "img" parts, plus the user's staged session
	// (the common case — the client uploaded them once when the modal opened).
	for name, data := range s.stagedImages(form.Value("session"), u.UserID) {
		req.images[name] = data
	}
	for _, f := range form.Files("img") {
		if base := safeImageName(f.Filename); base != "" {
			req.images[base] = f.Data
		}
	}
	return req, form, true
}

// handoutTypesetter is the shared typesetter, or a 500 when there is none.
func (s *server) handoutTypesetter(w http.ResponseWriter) (handout.Typesetter, bool) {
	ts, err := s.typesetter()
	if err != nil {
		log.Printf("typst unavailable: %v", err)
		httpError(w, http.StatusInternalServerError, xystrings.Default.Server.Internal())
		return nil, false
	}
	return ts, true
}

// renderFailed answers a render that did not finish.
func renderFailed(w http.ResponseWriter, ctx context.Context, what string, err error) {
	if ctx.Err() == context.DeadlineExceeded {
		httpError(w, http.StatusGatewayTimeout, what+" timed out")
		return
	}
	if _, isUser := corei18n.AsUser(err); isUser {
		handleUser(w, err)
		return
	}
	log.Printf("%s failed: %v", what, err)
	httpError(w, http.StatusInternalServerError, xystrings.Default.Server.Internal())
}

func (s *server) handleHandoutsPDF(w http.ResponseWriter, r *http.Request) {
	req, form, ok := s.readHandoutRequest(w, r, ".pdf")
	if !ok {
		return
	}
	ts, ok := s.handoutTypesetter(w)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), handoutTimeout)
	defer cancel()
	if svgTS, ok := ts.(handout.SVGTypesetter); ok && form.Value("format") == "svg" {
		pages, err := handout.RenderSVG(ctx, req.source, req.images, req.args, svgTS)
		if err != nil {
			renderFailed(w, ctx, "handout svg render", err)
			return
		}
		out := make([]string, len(pages))
		for i, p := range pages {
			out[i] = string(p)
		}
		w.Header().Set("Cache-Control", "private, no-store")
		writeJSON(w, map[string][]string{"pages": out})
		return
	}
	pdf, err := handout.Render(ctx, req.source, req.images, req.args, ts)
	if err != nil {
		renderFailed(w, ctx, "handout render", err)
		return
	}
	serveHandout(w, pdf, req.name+".pdf", "application/pdf")
}

func serveHandout(w http.ResponseWriter, data []byte, name, contentType string) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", contentDisposition(name))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	_, _ = w.Write(data)
}

// ── split-fit (Go: per-block typst fit → per-question + all-questions PDFs) ──
// Ports chgksuite's `handouts split_fit` (handout.SplitFit): each handout block
// is paged to the densest 1-page layout (binary search via typst's own
// pagination), rendered, compressed (pdfcpu) and zipped. No Python.

const splitFitTimeout = 300 * time.Second

func (s *server) handleHandoutsSplitFit(w http.ResponseWriter, r *http.Request) {
	req, _, ok := s.readHandoutRequest(w, r, ".zip")
	if !ok {
		return
	}
	ts, ok := s.handoutTypesetter(w)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), splitFitTimeout)
	defer cancel()
	zipped, err := handout.SplitFit(ctx, req.source, req.images, req.args, ts)
	if err != nil {
		renderFailed(w, ctx, "split_fit", err)
		return
	}
	serveHandout(w, zipped, req.name+".zip", "application/zip")
}

// handleHandoutsPack is chgksuite's split_fit followed by pack: the print run
// for a field of teams, one PDF for the colour printer and one for the other,
// zipped when there are both.
func (s *server) handleHandoutsPack(w http.ResponseWriter, r *http.Request) {
	req, form, ok := s.readHandoutRequest(w, r, ".zip")
	if !ok {
		return
	}
	teams, err := strconv.Atoi(strings.TrimSpace(form.Value("teams")))
	if err != nil || teams < 1 || teams > maxPackTeams {
		handleUser(w, corei18n.User(xystrings.Default.Server.Handouts.BadTeams()))
		return
	}
	ts, ok := s.handoutTypesetter(w)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), splitFitTimeout)
	defer cancel()
	files, err := packFiles(ctx, req, ts, teams)
	if err != nil {
		renderFailed(w, ctx, "pack", err)
		return
	}
	if len(files) == 1 {
		serveHandout(w, files[0].data, files[0].name, "application/pdf")
		return
	}
	zipped, err := zipPack(files)
	if err != nil {
		renderFailed(w, ctx, "pack", err)
		return
	}
	serveHandout(w, zipped, req.name+"_print.zip", "application/zip")
}

// packFiles is the print run as files named the way chgksuite's pack names them.
func packFiles(ctx context.Context, req handoutRequest, ts handout.Typesetter, teams int) ([]packFile, error) {
	colour, bw, err := handout.Pack(ctx, req.source, req.images, req.args, ts, teams)
	if err != nil {
		return nil, err
	}
	var files []packFile
	if colour != nil {
		files = append(files, packFile{req.name + "_color.pdf", colour})
	}
	if bw != nil {
		files = append(files, packFile{req.name + "_bw.pdf", bw})
	}
	return files, nil
}
