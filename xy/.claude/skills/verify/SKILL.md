---
name: verify
description: Drive the xy board UI in headless Chromium over CDP (no npm deps) to verify a change end-to-end — boot a throwaway server, log in, create/unlock a board, add lists and cards, screenshot. Use when verifying frontend changes to board.js/board.html/styles.css or any flow a user reaches through the browser.
---

# Verifying xy in a real browser

xy has no browser test harness and no npm dependencies. Verification means:
boot the server against a throwaway DB, drive Chromium over CDP with Node's
built-in `WebSocket`, and screenshot. `cdp.mjs` (next to this file) is the whole
driver — copy it to your scratchpad and import it.

## 1. Server on a throwaway DB

```bash
go build -o $SP/xy-server ./cmd/xy-server
printf 'testpass123' | XY_DB=$SP/t.db $SP/xy-server adduser tester   # password on stdin
XY_DB=$SP/t.db PORT=9681 $SP/xy-server        # run in background
```

Run it **from the repo root** to get disk-mode assets (`assets from disk` in the
log) — your edits to `web/assets/static/*` are then served without a rebuild.
Run the binary from elsewhere to test embed mode + `?v=` asset versioning instead.

## 2. Chromium

The binary is cached by Playwright (there is no `playwright` npm package):

```bash
~/.cache/ms-playwright/chromium_headless_shell-*/chrome-headless-shell-linux64/chrome-headless-shell \
  --remote-debugging-port=9333 --user-data-dir=$SP/chrome --no-sandbox --window-size=1400,900
```

Launch it as its **own** background task — backgrounding with `&` inside a task
that then exits kills it with the process group.

## 3. Drive it

`evalJS` runs in the page, so you drive the app through its real handlers.
The flows that took trial and error (all of these are non-obvious):

```js
import { connect } from "./cdp.mjs";
const p = await connect(9333);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// login: two steps — username form, then password form
await p.goto("http://127.0.0.1:9681/login");
await p.evalJS(`(()=>{loginUsername.value="tester";usernameForm.requestSubmit()})()`);
await sleep(800);
await p.evalJS(`(()=>{passwordValue.value="testpass123";passwordForm.requestSubmit()})()`);

// create a board (passphrase = client-side DK); lands on /board/{id}
await p.goto("http://127.0.0.1:9681/");
await p.evalJS(`(()=>{newBoardBtn.click();boardName.value="Тестовая доска";boardPass.value="pass";createForm.requestSubmit()})()`);
await sleep(3000);   // scrypt KEK derivation is deliberately slow

// unlock on every later page load (the DK cache is per-browser-profile)
await p.evalJS(`(()=>{const o=unlockOverlay;if(!o.hidden){unlockPass.value="pass";unlockForm.requestSubmit()}})()`);

// add a list
await p.evalJS(`(()=>{const f=document.querySelector(".klist-add .kadd-form");
  f.querySelector("input[type=text]").value="Тур 1";f.requestSubmit()})()`);

// add a card: list ⋯ menu → «Добавить карточку» → switch to the ТЕКСТ tab first!
// cardSave reads the *active view*; with the default "fields" view, setting
// #cardDesc does nothing and you get "Введите описание.".
await p.evalJS(`(async()=>{
  document.querySelector(".klist:not(.klist-add) .kadd").click();
  await new Promise(r=>setTimeout(r,300));
  [...document.querySelectorAll("button")].find(b=>b.textContent.includes("Добавить карточку")).click();
  await new Promise(r=>setTimeout(r,400));
  cardTabText.click();                       // ← the bit that bites
  await new Promise(r=>setTimeout(r,200));
  cardDesc.value = "Вопрос 1: …\\n\\nОтвет: …";
  cardSave.click();
})()`);
// then Escape to leave the card overlay: document.dispatchEvent(new KeyboardEvent("keydown",{key:"Escape"}))

// the ☰ menu button is `.menu-trigger` (an SVG hamburger — matching on "☰" text fails)
await p.evalJS(`document.querySelector(".menu-trigger").click()`);
await p.evalJS(`[...document.querySelectorAll("button,a")].find(b=>b.textContent.includes("Изменить размеры")).click()`);

await p.shot("/path/shot.png");
```

Sliders/inputs need `dispatchEvent(new Event("input",{bubbles:true}))` — setting
`.value` alone fires nothing.

## Gotchas

- Everything on a board is behind the unlock overlay; re-unlock after each
  `goto`. Board data is encrypted, so there is no way to seed it via SQL — seed
  through the UI.
- Waits are unavoidable (crypto + IndexedDB + sync are async). Poll for the
  element/state you expect rather than trusting one `sleep`.
- Assert on computed style / DOM state via `evalJS`, not just screenshots:
  `getComputedStyle(el).width`, `localStorage.getItem(...)`.
- Display prefs (list width / card height) live in `localStorage["xy.sizes"]`;
  clear it between runs or you inherit the previous run's sizes.
