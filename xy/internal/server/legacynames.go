package server

import (
	"bytes"
	"encoding/json"
	"net/http"
)

// legacyNames maps the field names writes carried before schema v29 dropped
// the `_enc` suffix to the names they carry now. Browsers still hold queued
// offline writes with the old names in IndexedDB, and old xy-cli binaries send
// them. Delete this file once those clients are gone, which is safe after
// 2027-01-01: a browser rewrites its own outbox when it loads the new code.
var legacyNames = map[string]string{
	"description_enc":   "description",
	"handout_meta_enc":  "handout_meta",
	"alias_enc":         "alias",
	"seen_enc":          "seen",
	"title_enc":         "title",
	"name_enc":          "name",
	"color_enc":         "color",
	"payload_enc":       "payload",
	"filename_enc":      "filename",
	"meta_enc":          "meta",
	"names_enc":         "names",
	"event_payload_enc": "event_payload",
	"desc_event_enc":    "desc_event",
}

// renameLegacyFields rewrites every old field name in a JSON body, at any
// depth, to its new name. A body that also has the new name keeps that one. A
// body that does not parse is returned as it is, for the real decode to reject.
func renameLegacyFields(body []byte) []byte {
	if !bytes.Contains(body, []byte(`_enc"`)) {
		return body
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil {
		return body
	}
	renameLegacyIn(v)
	out, err := json.Marshal(v)
	if err != nil {
		return body
	}
	return out
}

func renameLegacyIn(v any) {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			renameLegacyIn(val)
			name, legacy := legacyNames[k]
			if !legacy {
				continue
			}
			if _, both := t[name]; !both {
				t[name] = val
			}
			delete(t, k)
		}
	case []any:
		for _, val := range t {
			renameLegacyIn(val)
		}
	}
}

// queryParam reads a query parameter by its new name, falling back to the old.
func queryParam(r *http.Request, name string) string {
	q := r.URL.Query()
	if v := q.Get(name); v != "" {
		return v
	}
	return q.Get(name + "_enc")
}
