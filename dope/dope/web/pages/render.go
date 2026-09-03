package pages

import (
	"net/http"

	dopeui "dope/dope/web/ui"
	dopestrings "dope/i18nstrings"

	corei18n "pecheny.me/dopecore/i18nstrings"
	"pecheny.me/dopecore/webassets"
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
		msg, _ := corei18n.Reveal(err, dopestrings.Default.Server.Error.Internal())
		http.Error(w, msg, http.StatusInternalServerError)
		return
	}
	body := versionAssetRefs(etags, rendered)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(body)
}

func versionAssetRefs(etags map[string]string, body []byte) []byte {
	return webassets.VersionRefs(etags, body)
}
