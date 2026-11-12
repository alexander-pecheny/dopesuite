# chgksuite-app — agent notes

This is chgksuite v2, the desktop app described in
`../docs/chgksuite-v2-plan.md`. It is one Wails window with a tab strip. The first
tab, «Классический UI», is drawn by the strip's own page; each other tab is a
native `WebviewPanel` under the strip: the Local xy (`xy/localserver`), then
one per server the user has added.

- The Wails fork lives at `code.pecheny.me/pecheny/wails`, branch
  `webview-panel`, and `go.mod` replaces upstream with it at a pseudo-version.
  To move to a newer commit of the fork, run
  `go mod edit -replace=github.com/wailsapp/wails/v3=code.pecheny.me/pecheny/wails/v3@<commit>`
  and then `go mod tidy`.
- One asset handler serves both pages. `/shell/` is the tab strip and
  `/wails/` is the Wails runtime; every other path goes to the Local xy, which
  opens no port. The fork refuses a panel's request for `/wails/`, and it
  refuses any request from a page that the asset server did not serve, so a
  server tab cannot reach the Local xy.
- The strip names tabs by id and never hands a URL to a panel. `Shell` in
  `shell.go` owns the panels and the saved server list
  (`servers.json` in the user's config directory, next to `local/`).
- The board's «Импорт из файла…» is in xy's page, so it cannot open a
  dialog either. Instead it asks the Local xy (`POST /api/import/pick`), and
  the app installs `Shell.pickFile` with `local.SetFilePicker`. The server
  sends back the picked file and, for a `.4s`, the pictures it names from its
  folder, so the page imports them as if they had been uploaded together.
  `xy-local -pick <file>` stands in for the dialog in a browser.
- The strip borrows xy's stylesheet from the Local xy, and copies only the
  theme boot of `menu.js`, whose ☰ menu has no place here. It shares the
  Local tab's storage, so it follows the theme chosen there.
- On macOS the strip is the title bar. `lights_darwin.go` centres the
  traffic lights in it after every resize, since AppKit puts them back each
  time; its `stripHeight` must match the strip's height in
  `shell/index.html`.
- The classic tab (`classic.go`, `shell/classic.js`) is the old chgksuite
  window: a form over the CLI, drawn from `chgksuite-cli spec`, so a flag added
  to the CLI appears there with nothing edited here. The captions are in
  `chgksuite/i18nstrings/ru/cliform.toml`. The CLI ships beside the app as
  `chgksuite-cli` (plain `chgksuite` is the app itself) and runs as a
  subprocess, so Stop kills it, it can ask for input, and agents can run it on
  their own. `$CHGKSUITE_CLI` points the tab at another build. Like the
  Python window, it shares `~/.chgksuite` with chgksuite: its dialogs open at
  `lastdir`, and the CLI keeps the bot tokens in `telegram.toml` and the
  channels it resolved in `resolve.db`, which `spec` offers as suggestions.
- This module needs cgo and stays out of the root
  justfile's cross-module recipes. `just build` first builds xy's web dist.
- It renders with the typst release (`typst_version` in the justfile, its
  sums in `typst.sha256`), which `just app` and `just exe` put beside the
  executable. The justfile builds with `-tags nowasmtypst`, so the 30 MB wasm
  that xy's server renders with is left out. A checkout without a bundled
  typst uses the one on PATH or in `~/.pecheny_utils`. The fonts are the
  Noto Sans that chgksuite embeds, plus whatever the computer has.
- The app updates itself (`update.go`, on Wails' updater with the tag prefix,
  CalVer and multi-file installs our fork added). When it opens it looks at
  the GitHub releases tagged `chgksuite-app/`, at most once a day
  (`update.json` beside `servers.json`), and a newer one becomes a button in
  the strip that downloads it, checks it against `SHA256SUMS` and restarts
  into it. The version is stamped from `CHGKSUITE_VERSION`; a "dev" build
  never updates. A second release on one day is tagged `<date>.2`.
- A tag `chgksuite-app/<date>` makes `.github/workflows/chgksuite-app.yml`
  publish a GitHub pre-release. The icon is `icon.png`, from the Python
  chgksuite. The Mac app uses `icon-mac.png`, the same picture on Apple's
  icon grid (an 824 px rounded square on a 1024 px canvas), because macOS 26
  puts any other shape inside a grey rounded square.

```
just run    # build and open the window
just app    # build/<arch>/chgksuite.app, unsigned (macOS only)
just exe    # build/windows/, from any OS
just check  # fmt + vet + test
```
