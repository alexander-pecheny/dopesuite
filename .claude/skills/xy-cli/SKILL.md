---
name: xy-cli
description: Read and write xy boards from the shell with xy-cli — cards (4s), comments, labels, search, export. Use when a task involves the content of an xy board: reading questions, editing them, leaving comments, triaging with labels, or exporting a tour.
---

# Working on an xy board from the shell

`xy-cli` is an ordinary xy client: it authenticates with an API token and
decrypts board content locally with a key the user unlocked once. Everything it
prints is plaintext that came out of ciphertext on this machine — the server
never saw it.

Build/install: `cd xy && just cli` → `~/.local/bin/xy-cli`.

## Before you can do anything

Both steps are the **user's** — you cannot do them, so ask:

1. `xy-cli login --url https://xy.pecheny.me` — the token is minted in the
   browser at `/profile/tokens` and pasted in (or passed as `XY_TOKEN`).
2. `xy-cli unlock <board>` — the board passphrase, typed once. The key is then
   held in `~/.config/xy-cli/state.json` (0600) until `xy-cli lock`.

`xy-cli boards` shows which boards have a key (🔓). Without one, every content
command fails and says so — do not try to work around it.

## The shape of every command

- **Name the board every time**: `--board <id|имя>`. There is no current board.
- **Human text by default, `--json` when you need exactness** (ids to feed the
  next command).
- **Card content is raw 4s**: `card get` prints it verbatim, `card set` and
  `card add` read it from stdin.

```
xy-cli board show --board 12                    # lists and cards, with ids
xy-cli card get 412 --board 12                  # the question's 4s, verbatim
xy-cli card get 412 --board 12 --json           # + list_id, kind, alias, hash
xy-cli search «Гоголь» --board 12               # folded search over cards + comments
xy-cli search '\d{4} год' --board 12 --regex
xy-cli source --board 12 --list 7               # the whole tour as one 4s document
xy-cli export --board 12 --list 7 --format docx,pdf --out /tmp
```

Writes:

```
xy-cli card set 412 --board 12 --expect a1b2c3d4e5f6 < новый.4s
printf '? Вопрос\n! Ответ\n' | xy-cli card add --board 12 --list 7 --after 411
xy-cli card mv 412 --board 12 --list 8 --before 500
xy-cli comment add 412 --board 12 --text '@pecheny зачёт бы пошире'
xy-cli label assign 412 --board 12 --label готово
xy-cli attachment add 412 --board 12 картинка.png
```

## Rules that matter

- **Always pass `--expect`** when rewriting a card: take the hash from
  `card get` (stderr line, or the `hash` field with `--json`) and pass it back.
  Without it you will silently overwrite an edit a human made in between.
- **A `card set` that changes the 4s writes a `desc_edit` entry** to the лента
  automatically — your edits are as reviewable as a human's. Don't try to
  suppress it. Re-writing identical text changes nothing and records nothing.
- **Never invent a 4s marker.** The format is chgksuite's and parity is
  byte-for-byte: `?` question, `!` answer, `=` зачёт, `!=` незачёт, `/`
  комментарий, `^` источник, `@` автор, `##` тур heading. A card may also carry
  versions — separator lines `(hidden-comment xy-version: имя)` — and `source`
  folds them back into one numbered question, as an export does.
- **`@логин` in a comment is a Mention** only if the login is on the board;
  xy-cli resolves it against the roster and the named member gets a
  notification. Use it when you want a human to actually see something.
- **Deletes are 14-day tombstones**, not instant destruction — but still ask
  before deleting anything you did not create.
- Comments and cards are **Russian-language content**. Write in Russian on a
  board.

## What xy-cli deliberately does not do

Create or delete boards, change a board passphrase, manage members, or touch
Test Sessions and Playings. Those stay in the browser. Read markers and the 🔔
feed are not implemented either: reading a card with the CLI never clears a
human's unread dot.
