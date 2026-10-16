# chgksuite v2: a desktop app over the Go port

## What already exists

The format work is done. `chgksuite_go_rewrite.md` lists every parser and
exporter of the Python tool as ported and checked against its output: every
import (docx, txt, db.chgk.info, СИ, Тройка, zip) and every export (docx, pdf,
pptx, telegram, markdown, redditmd, base, openquiz, lj, add_stats, handouts).
The only things left out on purpose are the pandoc and mammoth docx engines.
`chgksuite/cmd/chgksuite` runs all of it from the shell, and `chgksuite-gui` draws Fyne
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
  home page lists boards. A folder is a board, kept in one SQLite database in
  that folder. The panel loads xy's frontend from the app's binary, and xy's
  own server code, running in local mode on that database, answers its `/api`
  calls through the asset server. Wails' asset server takes an `http.Handler`,
  so this opens no port. There is no encryption, no login and no outbox.
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

### The database is the source of truth

Each folder holds `.chgksuite.sqlite`, unencrypted, with everything about the
board: lists and their order, cards, labels, comments, the timeline, read marks,
attachments and settings. Its schema is xy's. xy's server package runs in local
mode over it: one board per database, one local user, no login, no Telegram,
and the `*_enc` columns hold plaintext because the frontend uses its plaintext
key (below). Reusing the server means the Local tab gets every feature xy has
that does not need a second person, without a second implementation of the API.
xy exposes this as a public package (`xy/localserver`), because the app cannot
import `xy/internal/...`.

### The .4s files are kept in step

The `.4s` files exist so that the folder stays useful without the app: to send
to someone, to open in the Python chgksuite, to keep in git. After every write
that changes a list (a card edited, added, moved or deleted, a list renamed),
the server writes that list's `.4s` with `listexport`, atomically (temp file and
rename), with its pictures next to it as files that its `(img …)` lines name.
A renamed list renames its file; a deleted list's file is deleted.

Opening a folder that has `.4s` files but no database imports every one of them
as a list, the same way xy imports a `.4s` today, and creates the database.

The database remembers a hash of each file as it last wrote it. If a file has
changed since, someone edited it outside the app. On open, the app then asks
whether to re-import that file, which replaces the list, or to keep the
database's version, which rewrites the file. A new `.4s` that appears in the
folder is offered for import the same way. Nothing is overwritten silently.

### What folder mode leaves out

Members, invites, test sessions, tokens and Telegram login need a server or
more than one person. The local server reports a capability set, and the
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

- **Import** creates a new list in the open folder from a docx, txt,
  db.chgk.info file or zip, through `chgkimport`, and with it a new `.4s`.
- **Export** works on one list, or on several lists merged, in every format.
  The forms are generated from the same flag spec the CLI prints, as in
  `chgksuite-gui`, but they are drawn in the webview and call the exporters in
  process instead of running a subprocess. The last used values are saved as
  presets in the folder's database.
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
2. Local mode for xy's server, as plain Go with no UI: `xy/localserver` opens a
   folder's database (importing the `.4s` files on first open), runs without
   login, writes each changed list back to its `.4s`, and detects files edited
   outside the app. Tested with the same request shapes the board page sends.
3. The frontend side: the plaintext key, the capability flags, and the offline
   layer switched off.
4. Build the Wails shell on the fork: the tab strip, the Local tab with recent
   folders, and adding a server tab.
5. Add import and the spec-driven export forms in the webview.
6. Package for macOS (signed and notarised), Windows and Linux.
