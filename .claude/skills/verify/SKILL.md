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
agent-browser open http://127.0.0.1:9681/login
agent-browser fill '#loginUsername' tester          # types real keys → input events fire
agent-browser eval 'usernameForm.requestSubmit()'
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

Both apps share the login UI: two JS steps — `#loginUsername` +
`usernameForm.requestSubmit()`, then `#passwordValue` +
`passwordForm.requestSubmit()`.

## xy

```bash
cd xy && go build -o $SP/xy-server ./cmd/xy-server
printf 'testpass123' | XY_DB=$SP/t.db $SP/xy-server adduser tester   # password on stdin
XY_DB=$SP/t.db PORT=9681 XY_WASM_CACHE=$SP/wasm-cache $SP/xy-server  # background task
```

Run it **from the repo root** for disk-mode assets (`assets from disk` in the
log) — edits to `web/assets/static/*` serve without rebuild. Run the binary
from elsewhere to test embed mode + `?v=` asset versioning.

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
DOPE_DB=$SP/fest.db PORT=9672 go run ./dope/cmd/dope-server   # background task
```

Log in with your local account, or mint an invite and register a fresh user:
`DOPE_DB=$SP/fest.db uv run python scripts/mint_invite.py` → paste at
`/register`. `scripts/fill_data.py` fills a fest's game with random answers
(see its docstring) for standings/propagation checks.
