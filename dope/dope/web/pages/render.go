package pages

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	dopeui "dope/dope/web/ui"
)

// RenderDoc renders a typed-builder page document, cache-busts its /static asset
// URLs against etags (the engine's AssetETags), and writes it as text/html. It is
// the single write path shared by every builder-based page in pages/hostpages —
// the analogue of the compiled-page pipeline for dynamic pages. Callers set any
// additional response headers (e.g. Cache-Control) before calling; RenderDoc only
// sets Content-Type. On a render error it writes a 500 and returns.
func RenderDoc(w http.ResponseWriter, etags map[string]string, doc *dopeui.Doc) {
	rendered, err := dopeui.Render(doc)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	body := versionAssetRefs(etags, rendered)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(body)
}

// assetRefRe matches a local /static .js/.css reference in an HTML attribute,
// skipping URLs that already carry a query string. Mirrors serve_html.go.
var assetRefRe = regexp.MustCompile(`(src|href)="(/static/[^"?]+\.(?:js|css))"`)

// versionAssetRefs appends "?v=<content-hash>" to every local /static .js/.css URL
// whose asset has a known content-hash ETag, busting the browser cache on deploy.
// Mirrors dopeserver.versionAssetRefs so builder pages match the compiled pages.
func versionAssetRefs(etags map[string]string, body []byte) []byte {
	if len(etags) == 0 {
		return body
	}
	return assetRefRe.ReplaceAllFunc(body, func(m []byte) []byte {
		sub := assetRefRe.FindSubmatch(m)
		path := string(sub[2])
		tag := strings.Trim(etags[path], `"`)
		if tag == "" {
			return m
		}
		return []byte(fmt.Sprintf(`%s="%s?v=%s"`, sub[1], path, tag))
	})
}
