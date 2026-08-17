---
name: verify
description: Drive the xy or dope UI in headless Chrome with agent-browser (CLI browser automation) to verify a change end-to-end — boot a throwaway server, log in, click through the flow, assert, screenshot. Use when verifying any frontend change or flow a user reaches through the browser, in either app.
---

# Verifying xy / dope in a real browser

`agent-browser` drives a persistent headless Chrome from the shell via a native
daemon. The first `agent-browser open` auto-launches the browser; every later
command talks to the same one; `agent-browser close` when done. `--help` (or
`agent-browser skills get core --full`) lists everything. Exit code is 0 even on
a failed check — read the `✗`/`✓` line or the returned value, don't trust `$?`.

**This box needs `--no-sandbox`.** It's set once in
`~/.agent-browser/config.json` (`{"args":"--no-sandbox"}`) so every launch picks
it up. Without it Chrome dies with *"No usable sandbox … without writing
DevToolsActivePort"*. If you ever see that, the config is missing — restore it.

**Throwaway servers go on 978x — xy 9781, dope 9782.** This box is also xy's
production host, and the whole 967x–968x band is taken by long-running units:
9673 xy prod, 9683 xytest staging, 9674 design-review, 9675 tg-oidc (3000 is
forgejo). A test server there either refuses to bind or, worse, shadows a real
service. Never `pkill -f xy-server` to clean up — that pattern matches prod and
staging too; kill the PID you started, or run it as a background task and stop
that. If 978x is itself busy, another agent session is mid-verify: pick 9783+
rather than killing whatever holds it.

## The two workflows

- **Snapshot + refs** (agent-browser's native style): `agent-browser snapshot -i`
  prints interactive elements as `@e1`, `@e2` refs; act on them (`click @e3`,
  `fill @e4 "text"`). Refs go **stale on any page change** — re-snapshot first.
- **eval-driven** (what most of the flows below use): `agent-browser eval '…'`.
  Both apps are id-heavy and their crypto/sync is async, so poking known ids and
  asserting computed state is usually more reliable than snapshotting. `eval`
  accepts **bare statements** (`const x=…; x*2` is fine) — no IIFE wrapper
  needed. Promises are awaited, so an `(async()=>{…})()` with internal sleeps
  works for multi-step flows.

Command cheatsheet: `open <url>` · `fill <sel> <text>` (clears then types real
keys → input events fire) · `type <sel> <text>` (append) · `click <sel>` ·
`eval <js>` · `get text|html|value|attr|count <sel>` · `is visible|enabled <sel>`
· `wait <sel>` (until visible; `--state hidden` for gone; `--text`, `--url`,
`--load networkidle`, `--fn` variants) · `wait <ms>` (dumb sleep, last resort) ·
`screenshot [selector] [path]` · `close`. Default timeout 25s.

## Always test phone mode before releasing

xy is used on phones and desktop layouts silently overflow there (a header of
selects running off a 393px screen, e.g.). So for any UI change, verify at BOTH
sizes before you ship:

```bash
agent-browser set device "iPhone 16"   # 393x852 @3x, iPhone UA — persists across
                                        # every later command until reset
# … open, unlock, drive to the changed surface …
agent-browser eval 'JSON.stringify({vw:innerWidth, bodyOverflow:document.body.scrollWidth-innerWidth})'
# bodyOverflow must be 0. Also check the specific surface's scrollWidth vs innerWidth.
agent-browser screenshot $SP/phone.png   # captured at the emulated size
agent-browser set viewport 1280 800 1    # back to desktop; re-verify desktop too
```

- `set device <name>` sets Chrome's metrics + DPR + UA. Valid names (a bad one
  prints the list): `iPhone 15`, `iPhone 16`, `iPhone 16 Pro`, `iPhone 17`,
  `iPad`, `iPad Pro`, `Pixel 9`, `Galaxy S25`. It persists in the daemon and
  re-applies on every command; reset with `set viewport <w> <h> <scale>`.
- **`set device` does NOT enable touch** (`navigator.maxTouchPoints` stays 0, no
  `ontouchstart`). It's a layout/DPR/UA emulation, not a real touch device — fine
  for catching overflow, not for testing touch-only handlers.
- **Assert overflow numerically, don't trust the eye:** an element can overflow
  its container while the page looks fine because the container itself scrolls.
  Check `el.scrollWidth - el.clientWidth === 0` on the header/row you changed.
- For a single element, `screenshot '<css-selector>' out.png` clips to it
  (scroll it into view first with `scrollintoview`). Full-page `--full`.

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

- **Never native `form.submit()`** — it bypasses JS submit handlers. Both apps'
  forms are JS-driven: always `agent-browser eval 'theForm.requestSubmit()'`.
- **Keep inline `eval` simple.** A multi-line `eval '…'` with nested quotes,
  backticks or object literals is easily mangled by the shell into a
  `SyntaxError`. For anything non-trivial, pipe it: `echo '<js>' | agent-browser
  eval --stdin` (or `eval -b <base64>`). One-liners returning a `JSON.stringify`
  of a `.map(...)` are the sweet spot.
- **Focus events only fire through real input.** `agent-browser
  focus/click/fill` use CDP input and fire `focus`/`focusin`, so focus-tracking
  handlers see them. But a `.focus()` done inside `eval` on a headless page fires
  nothing — if you must, dispatch it yourself:
  `agent-browser eval 'el.focus();el.dispatchEvent(new FocusEvent("focusin",{bubbles:true}))'`.
- **Sessions are ephemeral by default** — each daemon launch is a fresh profile,
  so no "logged-in second run" surprise and nothing to wipe between runs. Pass
  `--profile <dir>` / `--restore` only if you deliberately want persistence.
- `wait <sel>` waits for *visible*; on timeout it's just exit-nonzero, not a bug.
- Crashes when a page loads a **PDF into an iframe** (the xy handouts preview) —
  the browser dies. Test PDF-frame geometry with an `about:blank` iframe of the
  same class instead.

Both apps share the login UI. Password login is behind a button, and the fields
are `#pwUsername` / `#pwPassword` — not `#loginUsername` / `#passwordValue`,
which do not exist:

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

Start the server **before** `adduser`: maintenance subcommands never create a
database, so on a fresh `$SP` they exit with *"no database at … — set XY_DB"*.
Booting the server once creates and migrates it.

Run it with the working directory set to **`xy/`** (the module root, not the
monorepo root) for disk-mode assets — edits to `web/assets/static/*` then serve
without rebuilding the binary. **Check the first log line every time**: it says
`assets from disk` or `assets from embed`, and in embed mode you are testing the
assets baked in when the binary was built, so every `just build-web` since is
invisible. Backgrounding with `(cmd &)` inherits the calling shell's cwd — if
that was the monorepo root you silently get embed mode. Run from elsewhere
deliberately to test embed + `?v=` asset versioning.

Two caches sit in front of your edits, and clearing one is not enough:

- the **service worker** — `navigator.serviceWorker.getRegistrations()` →
  `unregister()`, then `caches.keys()` → `caches.delete()`;
- the browser's **HTTP cache** — disk mode serves `/static/dist/*.js` with no
  `?v=`, so the same URL that embed mode versioned is now unversioned and a
  stale copy is reused. `agent-browser close` and reopen: each launch is a fresh
  ephemeral profile, which is the only reliable way to drop it.

When the DOM does not match the source you just built, check the served bytes
before debugging the code: `curl -s localhost:9781/static/dist/board.js | grep -c
'<your new class>'`.

Flows that took trial and error:

```bash
# create a board; passphrase MUST be ≥16 chars — a short one just parks a
# message in #createMessage and never navigates, which reads like a hang
agent-browser eval 'newBoardBtn.click()'
agent-browser fill '#boardName' 'Тестовая доска'
agent-browser fill '#boardPass' 'board-pass-16chars'
agent-browser eval 'createForm.requestSubmit()'
agent-browser wait 4000        # scrypt KEK derivation is deliberately slow

# unlock after EVERY open — everything on a board is behind the overlay
agent-browser eval '(()=>{const o=unlockOverlay;if(!o.hidden){unlockPass.value="board-pass-16chars";unlockForm.requestSubmit()}})()'

# add a list
agent-browser fill '.klist-add .kadd-form input[type=text]' 'Тур 1'
agent-browser eval 'document.querySelector(".klist-add .kadd-form").requestSubmit()'

# add a card: list ⋯ → «Добавить карточку» → switch to the raw-text tab first!
# cardSave reads the *active view*; in the default "fields" view setting
# #cardDesc does nothing → "Введите описание."
# Click the tabs by id (cardTabText / cardTabFields) — the visible labels are
# «Просмотр» / «Поля» / «Формат 4s», so a find-by-text on "Текст" matches the
# "+ Текст вопроса" field pill instead and you click the wrong thing.
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
# leave the card overlay:
agent-browser eval 'document.dispatchEvent(new KeyboardEvent("keydown",{key:"Escape"}))'

# board ☰ menu is `.menu-trigger` (SVG hamburger — matching "☰" text fails)
agent-browser eval 'document.querySelector(".menu-trigger").click()'
```

- Board data is encrypted — no SQL seeding; seed through the UI.
- Crypto + IndexedDB + sync are async: poll for the element/state you expect
  (`wait` / re-`eval` the assertion), don't trust one sleep.
- Assert computed state via `eval` (`getComputedStyle(...)`,
  `localStorage.getItem(...)`), not just screenshots.
- Display prefs (list width / card height) live in `localStorage["xy.sizes"]`;
  clear between runs or you inherit the previous run's sizes.
- Setting `.value` on sliders/inputs from `eval` fires nothing — prefer
  `agent-browser fill`, or `dispatchEvent(new Event("input",{bubbles:true}))`.

## dope

```bash
cd dope && cp fest.db $SP/fest.db     # real-ish local data; never run against the live DB
DOPE_DB=$SP/fest.db PORT=9782 go run ./dope/cmd/dope-server   # background task
```

Log in with your local account, or mint an invite and register a fresh user:
`DOPE_DB=$SP/fest.db uv run python scripts/mint_invite.py` → paste at
`/register`. `scripts/fill_data.py` fills a fest's game with random answers
(see its docstring) for standings/propagation checks.

### The hand-over matrix

A change to a table skin, the Сетка or a game page is not verified until it
has been looked at in every cell of this matrix, and the report to the user
names the cells that were looked at:

| | phone (393 px, DPR 3) | desktop (1280 × 800) |
|---|---|---|
| light | screenshot | screenshot |
| dark | screenshot | screenshot |

…for every game type the change touches — ЭК, Личная СИ, ТПШ (both on the
ЭК page), КИнСБФ, ОД, КСИ — as spectator (`/fest/…`) and, where it edits, as
host (`/host/fest/…`).

**Run it, do not hand-drive it: `cd dope && just matrix`.** That is
`scripts/matrix.py run`: it checks HEAD out into `.tmp/verify/head-wt`,
builds it and the working tree, serves both on a copy of
`.tmp/verify/fest.db` (a dopetest snapshot — see the dopetest memory note;
HEAD on 9783, the tree on 9782), shoots 22 pages × the four cells on each
through eight workers on one Chrome, pixel-diffs the pairs and prints a
table; about 2.5 minutes, of which the two builds are most. Two runs of the
same tree are 88/88 identical, so a differing pair is a finding. The first
page is `/gallery` (dev mode only): every shared table and the Сетка from
fixtures on one page — a table-skin change is judged there first, four
shots instead of 84. `scripts/matrix.py shoot --label X --host URL` shoots
any host (dopetest, prod) and `diff A B` re-diffs; `--pages file` takes a
`name|/path` list for another fest.

What the tool knows that a hand-driven run learns the hard way:

- The baseline is HEAD built locally, never dopetest: dopetest lags the
  branch, and every unrelated commit shows up as a diff.
- Eight workers on one Chrome (`agent-browser connect` to a hub's CDP URL)
  cost tabs, not browsers; more workers than cores buys only CDP timeouts —
  rendering is software GL at DPR 3. Leaked Chromes from old sessions swap
  the box (7 GB RSS was seen); the tool kills the hub by pid at the end.
- Every page opens in a fresh tab. Over plain HTTP/1.1 (a local server) the
  previous page's SSE stream outlives an in-place navigation and starves the
  next one of Chrome's six connections per host — `Page.navigate` times out
  on the same three transitions every run. dopetest is h2 and never shows it.
- No `screenshot --full`: it resizes the viewport under CDP and the Сетка
  re-lays out on resize, so captures never settle. The tool sets the viewport
  to the page height, waits for the page to be quiet again, and takes a plain
  capture; a shot counts only when two captures 300 ms apart agree.
- `content-visibility: auto` boxes can capture blank; the tool forces
  `visible` for the shot. Readiness is fonts loaded + a content node + no DOM
  mutation for 400 ms + two painted frames — not a per-page selector.
- The header (viewer count, tab strip scroll) is cropped off: it changes
  between two shots of one page and is never the subject.

Dark by hand is `localStorage.setItem("dope-theme","dark")` before `open`
(the kit's menu.ts reads it at boot), or the ☰ → Оформление segment. The
three Сетка bugs of 16 Aug 2026 (columns stretched, group rows drifting off
the бой rows, two font sizes) were all phone-only and all invisible on the
desktop the change was checked on.
