# chgksuite-app — agent notes

This is chgksuite v2, the desktop app described in
`../docs/chgksuite-v2-plan.md`. It is one Wails window with a tab strip. Each
tab is a native `WebviewPanel` under the strip: the first tab is the Local xy
(`xy/localserver`), and each server the user has added gets a tab of its own.

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
- «Открыть папку/файл» is in the strip, not in xy's page, because a panel
  cannot reach the runtime and so cannot open a native dialog. `Shell.Open`
  hands the path to `localserver.Import` and sends the Local panel to the
  new board.
- The strip borrows xy's stylesheet from the Local xy, and copies only the
  theme boot of `menu.js`, whose ☰ menu has no place here. It shares the
  Local tab's storage, so it follows the theme chosen there.
- On macOS the strip is the title bar. `lights_darwin.go` centres the
  traffic lights in it after every resize, since AppKit puts them back each
  time; its `stripHeight` must match the strip's height in
  `shell/index.html`.
- Like `chgksuite-gui`, this module needs cgo and stays out of the root
  justfile's cross-module recipes. `just build` first builds xy's web dist and
  checks that `../chgksuite/typstwasm/typst.wasm` exists.

```
just run    # build and open the window
just app    # build/chgksuite.app, unsigned
just check  # fmt + vet + test
```
