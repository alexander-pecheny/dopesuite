---
name: verify
description: Drive the xy or dope UI in headless Chrome using agent-browser, to check a change end to end: start a throwaway server, log in, click through the flow, assert, and take screenshots. Use this to verify any frontend change, or any flow a user reaches through the browser, in either app.
---

# Verifying xy / dope in a real browser

`agent-browser` drives a persistent headless Chrome from the shell, through a
native daemon. The first `agent-browser open` starts the browser automatically,
every command after that talks to the same one, and `agent-browser close` shuts
it down when you are finished. `--help`, or `agent-browser skills get core
--full`, lists everything it can do.

Note that the exit code is 0 even when a check fails. Read the `✗` or `✓` line,
or the value the command returned. Do not rely on `$?`.

**This machine needs `--no-sandbox`.** It is set once in
`~/.agent-browser/config.json`, as `{"args":"--no-sandbox"}`, so every launch
picks it up. Without it, Chrome dies with *"No usable sandbox … without writing
DevToolsActivePort"*. If you see that message, the config file is missing and you
should restore it.

**Put throwaway servers on 978x: 9781 for xy, 9782 for dope.** This machine is
also xy's production host, and everything in the 967x to 968x range is already
taken by a long-running service: 9673 is xy production, 9683 is the xytest
staging instance, 9674 is design-review and 9675 is tg-oidc. Port 3000 is
forgejo. A test server in that range will either fail to bind or, worse, shadow a
real service.

Never run `pkill -f xy-server` to clean up. That pattern also matches production
and staging. Kill the PID you started, or run the server as a background task and
stop that task. If something is already listening on 978x, another agent session
is in the middle of a verify run: use 9783 or higher rather than killing whatever
is there.

## The two workflows

- **Snapshots and refs**, which is agent-browser's own style. `agent-browser
  snapshot -i` prints the interactive elements as refs like `@e1` and `@e2`, and
  you then act on those: `click @e3`, `fill @e4 "text"`. A ref goes **stale as
  soon as the page changes**, so take a new snapshot first.
- **eval-driven**, which is what most of the flows below use:
  `agent-browser eval '…'`. Both apps use ids heavily, and their crypto and sync
  are asynchronous, so poking at known ids and asserting on computed state is
  usually more reliable than snapshotting. `eval` accepts **bare statements** —
  `const x=…; x*2` is fine — with no need to wrap them in an IIFE. Promises are
  awaited, so an `(async()=>{…})()` with sleeps inside it works for a multi-step
  flow.

Command cheatsheet: `open <url>` · `fill <sel> <text>` (clears then types real
keys → input events fire) · `type <sel> <text>` (append) · `click <sel>` ·
`eval <js>` · `get text|html|value|attr|count <sel>` · `is visible|enabled <sel>`
· `wait <sel>` (until visible; `--state hidden` for gone; `--text`, `--url`,
`--load networkidle`, `--fn` variants) · `wait <ms>` (dumb sleep, last resort) ·
`screenshot [selector] [path]` · `close`. Default timeout 25s.

## Always test phone mode before releasing

People use xy on phones, and a desktop layout overflows there without saying
anything about it — a header full of selects running off a 393px screen, for
example. So for any UI change, check BOTH sizes before you ship it:

```bash
agent-browser set device "iPhone 16"   # 393x852 @3x, iPhone UA — persists across
                                        # every later command until reset
# … open, unlock, drive to the changed surface …
agent-browser eval 'JSON.stringify({vw:innerWidth, bodyOverflow:document.body.scrollWidth-innerWidth})'
# bodyOverflow must be 0. Also check the specific surface's scrollWidth vs innerWidth.
agent-browser screenshot $SP/phone.png   # captured at the emulated size
agent-browser set viewport 1280 800 1    # back to desktop; re-verify desktop too
```

- **These are `set` subcommands, and the bare words give a misleading answer.**
  `agent-browser viewport 390 780` replies `Unknown command: viewport`, and
  `agent-browser device "iPhone 16"` replies `Valid options: list`, because
  there is a separate top-level `device list` for macOS iOS simulators that has
  nothing to do with emulation. Both replies read as "this build cannot resize",
  and both are wrong: the command is `agent-browser set <setting>` in every
  case. Check the *Browser Settings* block in `--help` before you conclude that
  something is not supported.
- `set device <name>` sets Chrome's metrics, its device pixel ratio and its user
  agent. The valid names are `iPhone 15`, `iPhone 16`, `iPhone 16 Pro`,
  `iPhone 17`, `iPad`, `iPad Pro`, `Pixel 9` and `Galaxy S25`; giving a bad one
  prints the list. The setting lives in the daemon and is reapplied on every
  later command. Reset it with `set viewport <w> <h> <scale>`.
- The other settings on the same prefix: `set media dark|light`
  (`prefers-color-scheme`, plus `reduced-motion`), `set offline on|off`,
  `set geo <lat> <lng>`, `set headers <json>`, `set credentials <user> <pass>`.
- **Never fake a breakpoint in order to test it.** Editing `@media
  (max-width: …)` in the stylesheet, rebuilding and then putting it back does
  exercise the rules, but it is slow and it leaves the tree dirty if anything
  interrupts you. `set viewport` makes the real query match, so
  `matchMedia("(max-width: 760px)").matches` becomes true. Assert on that
  instead.
- **`set device` does NOT turn on touch.** `navigator.maxTouchPoints` stays at
  0 and there is no `ontouchstart`. It emulates the layout, the device pixel
  ratio and the user agent, but not a real touch device. That is enough to catch
  overflow, and not enough to test a handler that only runs on touch.
- **Check overflow with numbers rather than by eye.** An element can overflow
  its container while the page still looks fine, because the container itself
  scrolls. Check that `el.scrollWidth - el.clientWidth === 0` on the header or
  row you changed.
- To capture a single element, use `screenshot '<css-selector>' out.png`, which
  clips to it; scroll it into view first with `scrollintoview`. For the whole
  page, add `--full`.

```bash
agent-browser open http://127.0.0.1:9781/login
agent-browser fill '#pwUsername' tester             # types real keys → input events fire
agent-browser fill '#pwPassword' testpass123        # see the login note below
agent-browser eval 'passwordForm.requestSubmit()'
agent-browser screenshot $SP/shot.png                # also: get html/text/count
agent-browser eval 'document.title'                  # → "Мои доски · xy" — assert in the shell
agent-browser close
```

## agent-browser gotchas

- **Never call the native `form.submit()`.** It bypasses the JavaScript submit
  handlers, and the forms in both apps are driven by JavaScript. Always use
  `agent-browser eval 'theForm.requestSubmit()'`.
- **Keep an inline `eval` simple.** A multi-line `eval '…'` containing nested
  quotes, backticks or object literals is easy for the shell to mangle into a
  `SyntaxError`. For anything that is not trivial, pipe it in instead: `echo
  '<js>' | agent-browser eval --stdin`, or use `eval -b <base64>`. A one-liner
  that returns `JSON.stringify` of a `.map(...)` is usually the right size.
- **Focus events only fire for real input.** `agent-browser focus`, `click` and
  `fill` all go through CDP input and do fire `focus` and `focusin`, so a handler
  that tracks focus will see them. A `.focus()` called inside `eval` on a
  headless page fires nothing at all. If you have to do it that way, dispatch the
  event yourself: `agent-browser eval 'el.focus();el.dispatchEvent(new
  FocusEvent("focusin",{bubbles:true}))'`.
- **Sessions do not persist by default.** Each launch of the daemon gets a
  fresh profile, so a second run never starts out already logged in, and there is
  nothing to clear between runs. Pass `--profile <dir>` or `--restore` only when
  you actually want the state to persist.
- `wait <sel>` waits until the element is *visible*. If it times out, that is
  simply a non-zero exit code and not a bug in the tool.
- It crashes when a page loads a **PDF into an iframe**, which the xy handouts
  preview does, and the whole browser dies. To check the geometry of that frame,
  use an `about:blank` iframe with the same class instead.

Both apps use the same login UI. Logging in with a password is behind a button,
and the fields are `#pwUsername` and `#pwPassword`. They are not
`#loginUsername` and `#passwordValue`, which do not exist:

```bash
agent-browser eval '[...document.querySelectorAll("button")].find(b=>b.textContent.trim()==="Войти по паролю").click()'
agent-browser fill '#pwUsername' tester
agent-browser fill '#pwPassword' testpass123
agent-browser eval 'passwordForm.requestSubmit()'
```

## xy

```bash
cd xy && go build -o $SP/xy-server ./cmd/xy-server
XY_DB=$SP/t.db PORT=9781 XY_WASM_CACHE=$SP/wasm-cache $SP/xy-server  # background task
printf 'testpass123' | XY_DB=$SP/t.db $SP/xy-server adduser tester   # password on stdin
```

Start the server **before** you run `adduser`. The maintenance subcommands
never create a database, so on a fresh `$SP` they exit with *"no database at … —
set XY_DB"*. Starting the server once creates the database and migrates it.

Run it with the working directory set to **`xy/`**, which is the module root
rather than the monorepo root. That gives you assets from disk, so an edit to
`web/assets/static/*` is served without rebuilding the binary.

**Check the first line of the log every time.** It says either `assets from
disk` or `assets from embed`. In embed mode you are testing the assets that were
baked in when the binary was built, so every `just build-web` you have run since
is invisible. Note that backgrounding with `(cmd &)` inherits the calling
shell's working directory, so if that was the monorepo root you will silently get
embed mode. Run it from somewhere else on purpose when you want to test embed
mode and the `?v=` asset versioning.

There are two caches between you and your edits, and clearing one of them is
not enough.

- The **service worker**. Clear it with
  `navigator.serviceWorker.getRegistrations()` and `unregister()`, then
  `caches.keys()` and `caches.delete()`.
- The browser's **HTTP cache**. In disk mode, `/static/dist/*.js` is served with
  no `?v=` on it, so a URL that embed mode would have versioned is now
  unversioned, and the browser reuses its stale copy. Run `agent-browser close`
  and open it again: each launch gets a fresh temporary profile, and that is the
  only reliable way to clear it.

When the DOM does not match the source you just built, check what is actually
being served before you start debugging the code: `curl -s localhost:9781/static/dist/board.js | grep -c
'<your new class>'`.

Flows that took trial and error:

```bash
# Create a board. The passphrase MUST be at least 16 characters. A shorter one
# only puts a message in #createMessage and never navigates, which looks like
# the page has hung.
agent-browser eval 'newBoardBtn.click()'
agent-browser fill '#boardName' 'Тестовая доска'
agent-browser fill '#boardPass' 'board-pass-16chars'
agent-browser eval 'createForm.requestSubmit()'
agent-browser wait 4000        # deriving the scrypt KEK is deliberately slow

# Unlock after EVERY open. Everything on a board is behind the unlock overlay.
agent-browser eval '(()=>{const o=unlockOverlay;if(!o.hidden){unlockPass.value="board-pass-16chars";unlockForm.requestSubmit()}})()'

# add a list
agent-browser fill '.klist-add .kadd-form input[type=text]' 'Тур 1'
agent-browser eval 'document.querySelector(".klist-add .kadd-form").requestSubmit()'

# Add a card: open the list's ⋯ menu, choose «Добавить карточку», and then
# switch to the raw-text tab BEFORE you do anything else. cardSave reads
# whichever view is active, so in the default "fields" view, setting #cardDesc
# does nothing and you get "Введите описание."
# Click the tabs by their ids, cardTabText and cardTabFields. The visible labels
# are «Просмотр», «Поля» and «Формат 4s», so searching for the text "Текст"
# finds the "+ Текст вопроса" field pill instead and clicks the wrong thing.
agent-browser eval '(async()=>{
  document.querySelector(".klist:not(.klist-add) .kadd").click();
  await new Promise(r=>setTimeout(r,300));
  [...document.querySelectorAll("button")].find(b=>b.textContent.includes("Добавить карточку")).click();
  await new Promise(r=>setTimeout(r,400));
  cardTabText.click();
  await new Promise(r=>setTimeout(r,200));
  cardDesc.value = "Вопрос 1: …\n\nОтвет: …";
  cardSave.click();
})()'
# Close the card overlay:
agent-browser eval 'document.dispatchEvent(new KeyboardEvent("keydown",{key:"Escape"}))'

# The board's ☰ menu is `.menu-trigger`. It is an SVG hamburger, so searching
# for the text "☰" does not find it.
agent-browser eval 'document.querySelector(".menu-trigger").click()'
```

- Board data is encrypted, so you cannot seed it with SQL. Seed it through the
  UI.
- The crypto, IndexedDB and sync are all asynchronous. Poll for the element or
  the state you are expecting, using `wait` or by re-running the assertion in
  `eval`. Do not rely on a single sleep.
- Assert on computed state through `eval`, using things like
  `getComputedStyle(...)` and `localStorage.getItem(...)`, rather than only on
  screenshots.
- The display preferences — list width and card height — are stored in
  `localStorage["xy.sizes"]`. Clear them between runs, or you will inherit the
  sizes from the previous one.
- Setting `.value` on a slider or an input from inside `eval` fires no events.
  Use `agent-browser fill`, or dispatch the event yourself with
  `dispatchEvent(new Event("input",{bubbles:true}))`.

## dope

```bash
cd dope && cp fest.db $SP/fest.db     # realistic local data. Never run against the live DB.
DOPE_DB=$SP/fest.db PORT=9782 go run ./dope/cmd/dope-server   # background task
```

Log in with your local account, or mint an invite and register a new user:
run `DOPE_DB=$SP/fest.db uv run python scripts/mint_invite.py` and paste the
code at `/register`. `scripts/fill_data.py` fills a fest's game with random
answers, which is useful for checking standings and propagation; see its
docstring.

### The hand-over matrix

A change to a table skin, to the Сетка or to a game page is not verified until
somebody has looked at it in every cell of this matrix, and the report to the
user has to say which cells were looked at:

| | phone (393 px) | desktop (1280 × 800) |
|---|---|---|
| light | screenshot | screenshot |
| dark | screenshot | screenshot |

Do that for every game type the change touches: ЭК, Личная СИ, Тройка, брейн,
ОД, КСИ both plain and with stickers, and Мультиигры. Look at each as a
spectator, at `/fest/…`, and, where the change affects editing, as a host at
`/host/fest/…`.

**Run the tool rather than doing this by hand: `cd dope && just matrix`.** That
runs `scripts/matrix.py run`, which builds the working tree, seeds a fixture
database with `dope-server seed-fixture`, serves it on port 9782, takes
screenshots of 27 pages in each of the four cells using several workers on one
Chrome, and compares each screenshot against the golden image committed in
`dope/scripts/matrix-goldens`. It takes about a minute with `--split 2`, most of
which is the build. It is cheap enough to run on every commit, rather than only
before a merge.

- **It needs nothing outside this checkout.** The fest it photographs is built
  by `dope/dope/domain/fixture` in about a second, and contains one game of
  every format, with every document filled in from an arithmetic pattern. There
  is no snapshot to download and no staging database to keep up to date, and a
  schema change needs neither.
- **When a change to the UI is intended, run `just matrix --bless`.** That
  adopts the new screenshots as the goldens, and they belong in the same commit
  as the change, because that image diff is what a reviewer looks at.
- The first page it shoots is `/gallery`, which only works in dev mode. It puts
  every shared table and the Сетка on one page, built from fixtures, so a change
  to a table skin can be judged from those four screenshots instead of all 108.
- `scripts/matrix.py shoot --label X --host URL` photographs any host, such as
  dopetest or production, and `diff A B` compares two sets again. The name
  `goldens` refers to the committed set. `--pages file` takes a list of
  `name|/path` lines, for photographing a different fest.

Some things the tool already knows, which you would otherwise learn the hard
way:

- **The fixture pins every game's `random_seed`.** When a Block has to separate
  entrants who are tied on every metric, it draws a lot from that column, and
  the column is a random blob written by a trigger when the game is created. So
  two runs of the seeder sent different teams through, and no golden image ever
  matched itself. The seed is pinned before anything is played. Pinning it
  afterwards would only re-rank the standings, by which point the bracket has
  already advanced the wrong people.
- **The goldens are taken at DPR 1 and are one viewport tall.** At DPR 3 and
  full page height, one page costs about a megabyte across the four cells, which
  is too much to commit on every UI change; this way it is about fifty
  kilobytes. What that gives up is fine rendering detail, so if your change is
  about rendering rather than layout, use `shoot` against a deployed host and
  compare by hand.
- **A page counts as unchanged if fewer than 64 pixels differ.** The Сетка on a
  phone antialiases a handful of pixels differently between two runs of the same
  tree, because its layout is measured in JavaScript and the last fraction of a
  pixel depends on when that ran. A change of 1px in padding moves hundreds or
  thousands of pixels, so this floor costs nothing.
- Several workers on one Chrome cost you tabs rather than whole browsers. Using
  more workers than the machine has cores only buys CDP timeouts. Chromes left
  behind by earlier sessions will swap the machine — 7 GB of RSS has been seen —
  so the tool kills them by pid when it finishes.
- Every page is opened in a new tab. Over plain HTTP/1.1, which is what a local
  server speaks, the previous page's SSE stream outlives an in-place navigation
  and starves the next one of Chrome's six connections per host. dopetest speaks
  h2 and never shows this.
- **Every agent-browser call is a separate process.** At ten of them per
  screenshot, the CLI round trips cost more than the rendering does. That is why
  the settle step is a single `eval` that does four things at once: it turns
  `content-visibility` off, because an `auto` box can be captured blank; it
  resets every scroll offset; it waits for two painted frames; and it reports
  where the page starts. Before that step, "ready" means the fonts have loaded,
  a content node exists, and the DOM has not mutated for 400 ms. It is not a
  per-page selector.
- The header is cropped off every screenshot. It contains the viewer count and
  the scroll position of the tab strip, both of which change between two shots
  of the same page, and neither of which is ever what you are looking at.

To switch to the dark theme by hand, run
`localStorage.setItem("dope-theme","dark")` before `open`, since the kit's
menu.ts reads that when it boots. You can also use the Оформление segment in the
☰ menu.

It is worth remembering the three Сетка bugs of 16 August 2026: the columns were
stretched, the group rows had drifted off the бой rows, and two different font
sizes were in use. All three only happened on a phone, and all three were
invisible on the desktop where the change had been checked.
