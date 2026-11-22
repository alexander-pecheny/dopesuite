// Package picture fetches a picture a handout links to, so that the site
// keeps its own copy: a link to gotquestions or to telegram's CDN may change
// or expire, and the jury should not depend on either.
package picture

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"time"
)

// Picture is a fetched picture: its kind and its bytes.
type Picture struct {
	ContentType string
	Data        []byte
}

type Source interface {
	Fetch(ctx context.Context, link string) (Picture, error)
}

// ErrNotPicture is a link that answers with something else: a page, or a
// picture too large to keep.
var ErrNotPicture = errors.New("the link is not a picture")

const timeout = 30 * time.Second

// Web fetches over HTTP and keeps a picture of one of the kinds and no
// larger than max.
type Web struct {
	HTTP  *http.Client
	Kinds map[string]string
	Max   int64
}

func NewWeb(kinds map[string]string, limit int64) Web {
	return Web{HTTP: &http.Client{Timeout: timeout}, Kinds: kinds, Max: limit}
}

func (w Web) Fetch(ctx context.Context, link string) (Picture, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return Picture{}, err
	}
	res, err := w.HTTP.Do(req)
	if err != nil {
		return Picture{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return Picture{}, fmt.Errorf("%s: %s", link, res.Status)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, w.Max+1))
	if err != nil {
		return Picture{}, err
	}
	kind, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if w.Kinds[kind] == "" {
		kind, _, _ = mime.ParseMediaType(http.DetectContentType(data))
	}
	if w.Kinds[kind] == "" || int64(len(data)) > w.Max || len(data) == 0 {
		return Picture{}, ErrNotPicture
	}
	return Picture{ContentType: kind, Data: data}, nil
}

// Fixed answers from a map, for tests; any other link is not a picture.
type Fixed map[string]Picture

func (f Fixed) Fetch(_ context.Context, link string) (Picture, error) {
	if p, ok := f[link]; ok {
		return p, nil
	}
	return Picture{}, ErrNotPicture
}
