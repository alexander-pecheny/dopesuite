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

- **Local.** An xy server that runs inside the app, on one SQLite database in
  the app's data directory. The panel loads xy's frontend from the app's
  binary, and xy's own server code, in local mode, answers its `/api` calls
  through the asset server. Wails' asset server takes an `http.Handler`, so
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

## Local mode in detail

### A local xy server

The Local tab is xy: the same schema, the same server package, the same
frontend. xy exposes its server as a public package (`xy/localserver`), because
the app cannot import `xy/internal/...`. In local mode it runs with one local
user and no login, no Telegram bot and no invites, and its boards are not
encrypted. Every feature of xy that does not need a second person works
unchanged: lists, cards, labels, comments, the timeline, attachments, exports.

Local boards have nothing to do with folders while they are open. A folder of
`.4s` files is imported into a new board, one list per file, and a board is
exported into a folder, one `.4s` per list with its pictures next to it. Both
are explicit actions. Keeping files in sync automatically was considered and
dropped, because two copies of the truth bring conflicts that nobody wants to
resolve.

### Column names say what they hold

xy's content columns are called `description_enc`, `title_enc` and so on,
because on a server board they hold AES-GCM ciphertext. On a local board the
same columns would hold plaintext, and a name that says "encrypted" would then
be false. The suffix is dropped everywhere in xy, in the schema, the API and
the code: a content column holds an Envelope, and whether that Envelope is
encrypted is a property of the board (its keymeta), not of the column.

### What local mode leaves out

Members, invites, tokens and Telegram login need a server or more than one
person. Test sessions stay, because the owner records them alone. The local server reports a capability set, and the
frontend hides whatever it does not support.

### Frontend changes

These are small, because the seams already exist.

- `crypto.ts` gains a plaintext key, so that `encField` and `decField` pass the
  string through. A board whose keymeta says `none` gets that key, and the
  unlock overlay never shows.
- The offline layer (`store.ts`, `sync.ts`) is switched off in local mode,
  because every write lands on the local disk anyway.
- The pages read the capability set and hide what is unsupported.

## Imports and exports in the UI

- **Import** creates a list from a docx, txt, db.chgk.info file, zip or `.4s`
  through `chgkimport`, as xy's import does, and a whole folder of `.4s` files
  becomes a board.
- **Export** works on one list, or on several lists merged, in every format.
  The forms are generated from the same flag spec the CLI prints, as in
  `chgksuite-gui`, but they are drawn in the webview and call the exporters in
  process instead of running a subprocess. The last used values are saved as
  presets in the local database. A board can also be exported to a folder of
  `.4s` files.
- The app ships the typst release beside its executable and leaves the wasm
  out (`-tags nowasmtypst`). Its boards are plaintext on the user's own disk,
  so the wasm's reason to exist does not apply, and the binary is faster.
  html2img still needs a Chromium, as the CLI does.

Once this covers what the Fyne window does, `chgksuite-gui` is retired.

## Automatic backups of server boards

People rarely back up by hand, so the app does it for every server it has a tab
for. A backup is the boards' ciphertext and keymeta exactly as the server holds
them, plus the attachment blobs. Making one needs no passphrase, and the file on
disk is as safe as the server's copy.

- **xy side.** `GET /api/backup` streams a zip of every board the user belongs
  to: keymeta, snapshot, timeline and attachments, all ciphertext. Tokens gain
  a `read` scope that reaches only GET routes and lives a year, listed and
  revocable at `/profile/tokens` like the others (ADR-0015 says a token is the
  whole user and lives a month, which is too much power and too short a life
  for this). A "connect the app" flow hands the token over: the app opens
  `/link?code=…` in the server's tab, the user presses Allow, and the app
  collects the token by polling with that code, the way the Telegram login
  handshake works.
- **App side.** Adding a server offers backups. The app checks on start and
  every hour, and runs a backup in the background when the last good one is
  older than three days. It keeps the last ten per server in
  `Documents/chgksuite backups/<server>/`; all three are settings. A failure
  shows in the tab strip, success stays quiet.
- **Restore.** "Open backup…" asks for a board's passphrase, decrypts it with
  `xyenvelope` into a Bundle (`xy.board.v1`), and imports it into the Local
  tab. The Bundle can also be saved and imported into any xy server, so a
  backup is useful even if the original server is gone.

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
2. Drop the `_enc` suffix from xy's columns, API fields and code, with a
   migration and a transition period in which the server still accepts the old
   field names.
3. Local mode for xy's server as plain Go with no UI: `xy/localserver`, one
   local user, no login, unencrypted boards, the capability set; and on the
   frontend the plaintext key, the capability flags and the offline layer
   switched off. A `cmd/xy-local` harness serves it on a port, so it can be
   checked in a browser before the app exists.
4. Build the Wails shell on the fork: the tab strip, the Local tab on the local
   server, and adding a server tab.
5. Add import and the spec-driven export forms in the webview, with native
   file dialogs; import a folder of `.4s` files as a board and export a board
   to a folder.
6. Automatic backups: on xy, the backup endpoint, the `read` token scope and
   the connect-the-app flow; in the app, the schedule, retention, and restoring
   a backup into the Local tab.
7. Package for macOS (signed and notarised), Windows and Linux.
