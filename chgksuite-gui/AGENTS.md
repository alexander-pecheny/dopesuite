# chgksuite-gui — agent notes

A Fyne window over the `chgksuite` CLI in `../xy/cmd/chgksuite`, for people who
would rather not type flags. Nothing about the commands is written here: the CLI
prints its own flags as JSON (`chgksuite spec`), and this program draws a form
from that. Add a flag to a command and it appears in the window; nothing to edit
on this side.

## Why it is a module of its own, and a subprocess

- Fyne needs cgo and pulls in a GPU stack. `xy` is a server, and its `go.mod`
  has no business carrying that, so this is a separate module and stays out of
  the root `justfile`'s fan-out.
- The CLI runs as a subprocess rather than linked in. Output streams into the
  log pane a line at a time, `Stop` kills it, and a command that panics or calls
  `os.Exit` cannot take the window down.
- The GUI finds the binary at `$CHGKSUITE`, then beside its own executable
  (which is where `just app` puts it), then on the `PATH`.

## The seam

`spec.go` on each side has to agree. `spec_test.go` here reads a real
`chgksuite spec` and builds a form for every command it names — `just test`
builds the CLI first and points the test at it, so the two cannot drift apart
unnoticed.

Widgets follow what a flag says it is: a bool is a checkbox, a flag with
choices is a radio row (a `Select` past four of them), a flag naming a file or
a folder gets a picker, anything with "password" or "token" in its name is
masked in the entry and in the command-line preview. The CLI's `spec.go`
decides which is which; this side only draws.

## Building

`just build` builds both binaries into `build/`. `just app` wraps them in a
double-clickable `build/chgksuite.app`. The app is unsigned, so shipping it to
anyone else needs `codesign` and notarisation first.
