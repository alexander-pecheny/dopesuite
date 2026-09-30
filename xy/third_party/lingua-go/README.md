# lingua-go, trimmed

github.com/pemistahl/lingua-go v1.4.0 (Apache-2.0, see LICENSE) with the
language models of only the languages chgksuite reads: ru, uk, be, kk, en,
sr, az. The upstream module embeds all 75 languages (`//go:embed
language-models`, 124 MB); the linker cannot drop embedded files, so the
trim is done here and `go.mod` replaces the upstream module with this copy.
A detector built with any other language finds no model for it.

To update: copy the new upstream release over this directory, delete
`*_test.go` and every `language-models/<code>` but the seven above.
