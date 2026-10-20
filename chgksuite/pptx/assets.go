package pptx

import _ "embed"

// The template and the config chgksuite ships, embedded: the .docx export
// carries its template the same way, and for the same reason — the output is
// compared against chgksuite's, so it has to start from the same file.

//go:embed assets/template.pptx
var defaultTemplate []byte

//go:embed assets/pptx_config.toml
var defaultConfigTOML string
