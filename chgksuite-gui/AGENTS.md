# chgksuite-gui — agent notes

This is a Fyne window over the `chgksuite` CLI in `../xy/cmd/chgksuite`, for
people who would rather not type flags. Nothing about the commands is written
here. The CLI prints its own flags as JSON when you run `chgksuite spec`, and
this program draws a form from that output. If you add a flag to a command, it
appears in the window, and you don't have to edit anything on this side.

## Why it is a module of its own, and a subprocess

- Fyne needs cgo and brings a GPU stack with it. `xy` is a server and its
  `go.mod` should not have to carry any of that, so this is a separate module,
  and the root `justfile` leaves it out of its cross-module recipes.
- The CLI is run as a subprocess instead of being linked in. Its output is
  streamed into the log pane one line at a time, `Stop` kills it, and a command
  that panics or calls `os.Exit` cannot bring the window down with it.
- The GUI finds the binary at `$CHGKSUITE`, then beside its own executable
  (which is where `just app` puts it), then on the `PATH`.

## The seam

The `spec.go` on each side has to agree with the other. `spec_test.go` in this
module reads the output of a real `chgksuite spec` and builds a form for every
command in it. `just test` builds the CLI first and points the test at it, so the
two sides cannot drift apart without someone noticing.

The widget is chosen from what the flag says it is. A bool becomes a checkbox.
A flag with a fixed set of choices becomes a row of radio buttons, or a `Select`
if there are more than four. A flag that names a file or a folder gets a picker.
Anything whose name contains "password" or "token" is masked, both in the input
and in the preview of the command line. The CLI's `spec.go` decides which is
which, and this side only draws them.

## Building

`just build` builds both binaries into `build/`. `just app` wraps them into a
double-clickable `build/chgksuite.app`. That app is unsigned, so before you can
give it to anyone else it needs to be signed with `codesign` and notarised.
