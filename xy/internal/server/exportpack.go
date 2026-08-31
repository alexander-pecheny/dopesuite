package server

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"strings"

	"xy/internal/chgk/docx"
	"xy/internal/chgk/fsource"
	"xy/internal/chgk/handout"
	"xy/internal/chgk/typstdoc"
)

// The pack export: one request, several formats, one download. The export modal
// ticks formats; each is rendered here from the same 4s source and images, and
// the result comes back as the bare file when only one was asked for or as a zip
// when there are more. Nothing is written to disk — same as the single-format
// exports this composes.

// handoutsDir is the zip folder split-fit's per-question PDFs land in, so they
// don't crowd the root alongside the .4s and its images.
const handoutsDir = "раздатки/"

// packFile is one rendered output on its way into the response.
type packFile struct {
	name string
	data []byte
}

// packFormats is the set of formats one pack request asks for.
type packFormats struct {
	fourS, docx, pdf, pdfMobile, handouts bool
}

func parsePackFormats(v string) packFormats {
	var f packFormats
	for _, name := range strings.Split(v, ",") {
		switch strings.TrimSpace(name) {
		case "4s":
			f.fourS = true
		case "docx":
			f.docx = true
		case "pdf":
			f.pdf = true
		case "pdf_mobile":
			f.pdfMobile = true
		case "handouts":
			f.handouts = true
		}
	}
	return f
}

func (f packFormats) empty() bool {
	return !f.fourS && !f.docx && !f.pdf && !f.pdfMobile && !f.handouts
}

// needsTypst reports whether any selected format goes through the typst pool,
// so a pack of just .4s + .docx never pays for (or fails on) a typesetter.
func (f packFormats) needsTypst() bool { return f.pdf || f.pdfMobile || f.handouts }

func (s *server) handleExportPack(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	req, form, ok := s.readExportForm(w, r, "")
	if !ok {
		return
	}
	formats := parsePackFormats(form.Value("formats"))
	if formats.empty() {
		httpError(w, http.StatusBadRequest, "no formats selected")
		return
	}

	var ts handout.Typesetter
	if formats.needsTypst() {
		var err error
		if ts, err = s.typesetter(); err != nil {
			httpError(w, http.StatusInternalServerError, "typst unavailable: "+err.Error())
			return
		}
	}

	// One timeout for the whole pack: split-fit binary-searches every block and
	// dominates whenever it is selected.
	timeout := exportPDFTimeout
	if formats.handouts {
		timeout = splitFitTimeout
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	files, err := s.renderPack(ctx, req, formats, normalizeNewlines(form.Value("hndt")), ts)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			httpError(w, http.StatusGatewayTimeout, "export timed out")
			return
		}
		httpError(w, http.StatusInternalServerError, "export failed: "+err.Error())
		return
	}
	if len(files) == 0 {
		httpError(w, http.StatusBadRequest, "nothing to export")
		return
	}
	if len(files) == 1 {
		serveDownload(w, files[0].data, files[0].name, contentTypeFor(files[0].name))
		return
	}
	zipped, err := zipPack(files)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "zip failed: "+err.Error())
		return
	}
	serveDownload(w, zipped, req.name+".zip", "application/zip")
}

// renderPack renders every selected format. The parsed 4s is shared: all three
// document exports read the same structure.
func (s *server) renderPack(ctx context.Context, req exportRequest, formats packFormats, hndt string, ts handout.Typesetter) ([]packFile, error) {
	var files []packFile
	var parsed fsource.Doc
	var parsedOK bool
	structure := func() fsource.Doc {
		if !parsedOK {
			parsed, parsedOK = fsource.Parse(req.source, "chgk"), true
		}
		return parsed
	}

	if formats.fourS {
		files = append(files, packFile{req.name + ".4s", []byte(req.source)})
		// The .4s references its images by base name and cannot embed them, so
		// they ride alongside; docx/pdf embed their own copies.
		for name, data := range req.images {
			files = append(files, packFile{name, data})
		}
	}
	if formats.docx {
		b, err := docx.Export(structure(), req.images, docx.Options{})
		if err != nil {
			return nil, err
		}
		files = append(files, packFile{req.name + ".docx", b})
	}
	for _, v := range []struct {
		want   bool
		device typstdoc.Device
		suffix string
	}{
		{formats.pdf, typstdoc.Desktop, ".pdf"},
		{formats.pdfMobile, typstdoc.Mobile, "_mobile.pdf"},
	} {
		if !v.want {
			continue
		}
		b, err := typstdoc.Export(ctx, structure(), req.images, ts, typstdoc.Options{Device: v.device})
		if err != nil {
			return nil, err
		}
		files = append(files, packFile{req.name + v.suffix, b})
	}
	if formats.handouts && strings.TrimSpace(hndt) != "" {
		zipped, err := handout.SplitFit(ctx, hndt, req.images, handout.DefaultArgs(), ts)
		if err != nil {
			return nil, err
		}
		inner, err := unzipInto(zipped, handoutsDir)
		if err != nil {
			return nil, err
		}
		files = append(files, inner...)
	}
	return files, nil
}

// unzipInto reads split-fit's zip back out so its PDFs join the pack under one
// prefix, rather than arriving as a zip nested inside a zip.
func unzipInto(zipped []byte, prefix string) ([]packFile, error) {
	zr, err := zip.NewReader(bytes.NewReader(zipped), int64(len(zipped)))
	if err != nil {
		return nil, err
	}
	var out []packFile
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		var buf bytes.Buffer
		_, err = buf.ReadFrom(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		out = append(out, packFile{prefix + safeImageName(f.Name), buf.Bytes()})
	}
	return out, nil
}

func zipPack(files []packFile) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		wr, err := zw.Create(f.name)
		if err != nil {
			return nil, err
		}
		if _, err := wr.Write(f.data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func contentTypeFor(name string) string {
	switch {
	case strings.HasSuffix(name, ".docx"):
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case strings.HasSuffix(name, ".pdf"):
		return "application/pdf"
	case strings.HasSuffix(name, ".zip"):
		return "application/zip"
	}
	return "text/plain; charset=utf-8"
}
