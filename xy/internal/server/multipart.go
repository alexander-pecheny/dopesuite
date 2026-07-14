package server

import (
	"errors"
	"io"
	"net/http"
)

// The export / handout / staging / import endpoints all receive the same thing:
// the board's plaintext 4s source plus the images it references. http.Request's
// ParseMultipartForm keeps only its memory budget in RAM and spills everything
// past it into a temp file — so a package larger than the budget lands on disk as
// plaintext, in a file xy neither names nor deletes. Since the whole point of the
// app is that the server never persists plaintext, these handlers parse their
// bodies themselves and keep them entirely in memory.

// memFile is one uploaded file part.
type memFile struct {
	Filename string
	Data     []byte
}

// memForm is a multipart form held entirely in memory.
type memForm struct {
	values map[string]string
	files  map[string][]memFile
}

// Value returns the first value of a plain form field ("" if absent).
func (f *memForm) Value(key string) string { return f.values[key] }

// Files returns the uploaded parts sent under key.
func (f *memForm) Files(key string) []memFile { return f.files[key] }

// readMultipart parses a multipart body without ever touching the filesystem.
// maxTotal bounds the summed size of every part, so a large upload fails cleanly
// instead of being silently spilled to disk (or eating unbounded memory).
func readMultipart(r *http.Request, maxTotal int64) (*memForm, error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, errors.New("bad multipart form")
	}
	form := &memForm{values: map[string]string{}, files: map[string][]memFile{}}
	budget := maxTotal
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			return form, nil
		}
		if err != nil {
			return nil, errors.New("bad multipart form")
		}
		data, err := io.ReadAll(io.LimitReader(p, budget+1))
		name, filename := p.FormName(), p.FileName()
		p.Close()
		if err != nil {
			return nil, errors.New("bad multipart form")
		}
		if int64(len(data)) > budget {
			return nil, errors.New("upload too large")
		}
		budget -= int64(len(data))
		if filename != "" {
			form.files[name] = append(form.files[name], memFile{Filename: filename, Data: data})
		} else if _, dup := form.values[name]; !dup {
			form.values[name] = string(data)
		}
	}
}
