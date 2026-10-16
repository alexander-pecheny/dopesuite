# chgksuite v2: a desktop app over the Go port

## What already exists

The format work is done. `chgksuite_go_rewrite.md` lists every parser and
exporter of the Python tool as ported and checked against its output: every
import (docx, txt, db.chgk.info, СИ, Тройка, zip) and every export (docx, pdf,
pptx, telegram, markdown, redditmd, base, openquiz, lj, add_stats, handouts).
The only things left out on purpose are the pandoc and mammoth docx engines.
`xy/cmd/chgksuite` runs all of it from the shell, and `chgksuite-gui` draws Fyne
forms for it from `chgksuite spec`.

So v2 is mostly an application problem, not a format problem. It needs a window,
a local storage backend, and a way to reach the exporters from the UI.

## The shape

One Wails app whose window has tabs. The first tab is **Local**, and each xy
server the user has added gets a tab of its own.

Each tab is a native webview of its own: a Wails `WebviewPanel` filling the
window below the tab strip, which the main webview draws. Switching tabs shows
one panel and hides the others.

- **Local.** The user opens folders here, and the tab lists them the way xy's
  home page lists boards. A folder is a board, each `.4s` file
  in it is a list, and each question is a card. The panel loads xy's frontend
  from the app's binary, and a Go handler on the asset server answers its
  `/api` calls from the files. Wails' asset server takes an `http.Handler`, so
  this opens no port. There is no encryption, no login and no outbox.
- **A server tab.** The panel loads the xy server's own URL. The frontend is
  the server's, so it always matches the API, and the login cookie is an
  ordinary first-party cookie. Two tabs of one server share the login.

Panels come from our Wails fork (see below). They share one persistent profile
with the main window, and they cannot call the Wails runtime: the tab strip,
which can, is the only page that drives them. An outside page can still load an
asset URL as a top-level page or an iframe, though it can't read the result, so
the Local handler must never change anything on a GET.

### The Wails fork

Upstream Wails has no way to put several webviews in one window. PR #4880 adds
`WebviewPanel` and has been open since January 2026. We build against
`code.pecheny.me/pecheny/wails`, branch `webview-panel`: the PR merged with
current master, plus fixes from a review of all three platforms. Among them,
panels are kept out of the runtime, Windows panels share the window's WebView2
profile, cookies persist on Linux, and macOS panels get popups, file pickers and
crash recovery. CI on the GitHub copy (`alexander-pecheny/wails`) builds and
smoke-tests it on Windows, macOS and Linux, including a two-run test that a
login survives a restart. When the PR is merged upstream, we switch back.

## Folder mode in detail

### Files are the source of truth

A card's description in xy is already raw 4s, and a list exports as the
concatenation of its cards (`internal/listexport`). That makes the mapping
direct. Reading a file splits it into cards at question boundaries, and writing
puts the cards back together.

The hard requirement is that the app never rewrites text the user did not
touch. Each card keeps the exact bytes of its span in the file. Saving replaces
only the spans of edited cards and moves spans when cards are reordered. A test
over the whole fixture corpus checks that split followed by join gives back the
file byte for byte.

Writes are atomic (temp file and rename) and happen on every change, the way xy
saves. The app watches the folder. When a file changes on disk, the app reloads
that list. If the user also has unsaved edits in a card of that list, the app
shows a conflict instead of choosing a winner.

### Where the meta goes

- **Per card**: one hidden comment at the end of the card's first field holds
  the card's id and its meta as JSON, for example
  `(hidden-comment xy {"id":"k3f9","labels":["Проверить"]})`. Parentheses in
  values are written as `\u0028` and `\u0029`, so the comment always ends at
  the first `)`. Every exporter already drops hidden comments, so the output
  does not change. The id is what ties a card to its comments, so it survives
  edits made outside the app and reordering.
- **Per folder**: `chgksuite.toml` holds the order of the lists, list groups,
  the label definitions (name and colour), board settings, and the export
  presets (the flag values last used for each format). A `.4s` file that the
  meta file does not mention is added at the end of the board.
- **Comments, the timeline and read marks** live in `.chgksuite.sqlite` in the
  same folder, keyed by card id, so they travel with the folder.
- **Pictures** stay files in the folder, referenced by `(img …)` relative to
  the `.4s`, which is what chgksuite already reads. An attachment added in the
  UI is copied into the folder.

### What folder mode leaves out

Members, invites, test sessions, tokens and Telegram login need a server or
more than one person. The local handler reports a capability set, and the
frontend hides whatever it does not support.

### Frontend changes

These are small, because the seams already exist.

- `crypto.ts` gains a plaintext key, so that `encField` and `decField` pass the
  string through. A board whose keymeta says `none` gets that key, and the
  unlock overlay never shows.
- The offline layer (`store.ts`, `sync.ts`) is switched off in folder mode,
  because every write lands on the local disk anyway.
- The pages read the capability set and hide what is unsupported.

## Imports and exports in the UI

- **Import** creates a new `.4s` in the open folder from a docx, txt, db.chgk.info
  file or zip, through `chgkimport`.
- **Export** works on one list, or on several lists merged, in every format.
  The forms are generated from the same flag spec the CLI prints, as in
  `chgksuite-gui`, but they are drawn in the webview and call the exporters in
  process instead of running a subprocess. The last used values are saved as
  presets in `chgksuite.toml`.
- typst is embedded as wasm (`-tags wasmtypst`), so the app needs nothing
  installed next to it. html2img still needs a Chromium, as the CLI does.

Once this covers what the Fyne window does, `chgksuite-gui` is retired.

## Module layout

Go does not let another module import `xy/internal/...`, and the app must not
put Wails and cgo into xy's `go.mod`. So the chgk packages, `listexport`,
`cardkind`, the CLI and the xy envelope crypto that `board` needs move into a
new module, `code.pecheny.me/pecheny/dopesuite/chgksuite`, with a string catalog
of its own. xy imports it through a `replace`. dopecore, which chgksuite
imports, becomes `code.pecheny.me/pecheny/dopesuite/dopecore`, so that `go get`
can resolve both. The forge answers `?go-get=1` only for the repo root, so a
Caddy rule answers it for every path under `/pecheny/dopesuite/`. Releases are
tagged `chgksuite/vX.Y.Z` and `dopecore/vX.Y.Z`. The app is a third module,
`chgksuite-app/`, kept out of the cross-module recipes like `chgksuite-gui`.

## Steps

1. Rename dopecore's module path, move the chgk code into the chgksuite module
   with its own catalog, and make `go get` work. This step only moves code.
2. Build the folder store as plain Go with no UI: open a folder, split and join
   with the byte-identity ratchet, the per-card hidden comment, the meta file
   and the watcher.
3. Write the local API handler over the store, covering the requests the board
   page sends, and add the plaintext key and the capability flags to the
   frontend.
4. Build the Wails shell on the fork: the tab strip, the Local tab with recent
   folders, and adding a server tab.
5. Add import and the spec-driven export forms in the webview.
6. Package for macOS (signed and notarised), Windows and Linux.
