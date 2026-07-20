---
name: verify
description: Drive the xy or dope UI in headless Chrome with rodney (CLI browser automation) to verify a change end-to-end — boot a throwaway server, log in, click through the flow, assert, screenshot. Use when verifying any frontend change or flow a user reaches through the browser, in either app.
---

# Verifying xy / dope in a real browser

`rodney` (installed at `/opt/homebrew/bin/rodney` on macOS, `~/go/bin/rodney`
on the Linux box — `go install github.com/simonw/rodney@latest`) drives a persistent headless
Chrome from the shell. `rodney start` once, then each command talks to the same
browser; `rodney stop` when done. `rodney --help` lists everything. Exit codes:
0 ok, 1 check failed (`exists`/`visible`/`assert`), 2 error/timeout.

```bash
rodney start
rodney open http://127.0.0.1:9681/login && rodney waitload
rodney input '#loginUsername' tester          # types real keys → input events fire
rodney js 'usernameForm.requestSubmit()'
rodney screenshot $SP/shot.png                # also: screenshot-el, html, text, count
rodney assert 'document.title' 'Мои доски · xy'
rodney stop
```

## rodney gotchas (each cost a debugging round)

- **Never `rodney submit`** — it's native `form.submit()`, which bypasses JS
  submit handlers. Both apps' forms are JS-driven: always
  `rodney js 'theForm.requestSubmit()'`.
- `rodney js` evaluates an **expression** — a bare `const` is a syntax error.
  Wrap statements in `(()=>{...})()`. Promises are awaited, so
  `(async()=>{...})()` works, including multi-step flows with internal sleeps.
- The profile persists in `~/.rodney/chrome-data` across start/stop — a second
  run starts logged in (`/login` redirects, the form ids are absent). For a
  clean slate: `rodney stop && rm -rf ~/.rodney/chrome-data`.
- `rodney wait` waits for *visible* and panics with a goroutine dump on
  timeout — that's just exit 2, not a rodney bug.
- Headless pages are unfocused: `.focus()` updates `document.activeElement`
  but fires no `focus`/`focusin`, so focus-tracking handlers silently see
  nothing. Dispatch the event yourself:
  `rodney js 'el.focus();el.dispatchEvent(new FocusEvent("focusin",{bubbles:true}))'`.

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
rodney js 'newBoardBtn.click()'
rodney input '#boardName' 'Тестовая доска'
rodney input '#boardPass' 'board-pass-16chars'
rodney js 'createForm.requestSubmit()'
rodney sleep 4        # scrypt KEK derivation is deliberately slow

# unlock after EVERY open — everything on a board is behind the overlay
rodney js '(()=>{const o=unlockOverlay;if(!o.hidden){unlockPass.value="board-pass-16chars";unlockForm.requestSubmit()}})()'

# add a list
rodney input '.klist-add .kadd-form input[type=text]' 'Тур 1'
rodney js 'document.querySelector(".klist-add .kadd-form").requestSubmit()'

# add a card: list ⋯ → «Добавить карточку» → switch to the raw-text tab first!
# cardSave reads the *active view*; in the default "fields" view setting
# #cardDesc does nothing → "Введите описание."
# Click the tabs by id (cardTabText / cardTabFields) — the visible labels are
# «Просмотр» / «Поля» / «Формат 4s», so a find-by-text on "Текст" matches the
# "+ Текст вопроса" field pill instead and you click the wrong thing.
rodney js '(async()=>{
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
rodney js 'document.dispatchEvent(new KeyboardEvent("keydown",{key:"Escape"}))'

# board ☰ menu is `.menu-trigger` (SVG hamburger — matching "☰" text fails)
rodney js 'document.querySelector(".menu-trigger").click()'
```

- Board data is encrypted — no SQL seeding; seed through the UI.
- Crypto + IndexedDB + sync are async: poll for the element/state you expect
  (`rodney wait` / `assert`), don't trust one sleep.
- Assert computed state via `rodney js` (`getComputedStyle(...)`,
  `localStorage.getItem(...)`), not just screenshots.
- Display prefs (list width / card height) live in `localStorage["xy.sizes"]`;
  clear between runs or you inherit the previous run's sizes.
- Setting `.value` on sliders/inputs fires nothing — prefer `rodney input`, or
  `dispatchEvent(new Event("input",{bubbles:true}))` after.

## dope

```bash
cd dope && cp fest.db $SP/fest.db     # real-ish local data; never run against the live DB
DOPE_DB=$SP/fest.db PORT=9672 go run ./dope/cmd/dope-server   # background task
```

Log in with your local account, or mint an invite and register a fresh user:
`DOPE_DB=$SP/fest.db uv run python scripts/mint_invite.py` → paste at
`/register`. `scripts/fill_data.py` fills a fest's game with random answers
(see its docstring) for standings/propagation checks.
