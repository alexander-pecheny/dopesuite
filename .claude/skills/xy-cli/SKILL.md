---
name: xy-cli
description: Read and write xy boards from the shell with xy-cli: cards in 4s form, comments, labels, search and export. Use this when a task involves the content of an xy board, such as reading questions, editing them, leaving comments, triaging with labels, or exporting a tour.
---

# Working on an xy board from the shell

`xy-cli` is an ordinary xy client. It authenticates with an API token, and it
decrypts board content on this machine, using a key the user unlocked once.
Everything it prints is plaintext that was decrypted here, and the server never
saw any of it.

Build/install: `cd xy && just cli` → `~/.local/bin/xy-cli`.

## Before you can do anything

Both of these steps are the **user's** to do. You cannot do them yourself, so
ask:

1. `xy-cli login --url https://xy.pecheny.me` — the token is minted in the
   browser at `/profile/tokens` and pasted in (or passed as `XY_TOKEN`).
2. `xy-cli unlock <board>`, which asks for the board passphrase once. The key is
   then kept in `~/.config/xy-cli/state.json`, with mode 0600, until somebody
   runs `xy-cli lock`.

`xy-cli boards` shows which boards currently have a key, marked 🔓. Without a
key, every command that touches content fails and says so. Do not try to work
around that.

## The shape of every command

- **Name the board every time**, with `--board <id|имя>`. There is no such
  thing as a current board.
- **Output is human-readable text by default.** Add `--json` when you need exact
  values, such as ids to pass to the next command.
- **Card content is raw 4s.** `card get` prints it exactly as stored, and
  `card set` and `card add` read it from stdin.

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

- **Always pass `--expect`** when you rewrite a card. Take the hash that
  `card get` prints, either on its stderr line or in the `hash` field with
  `--json`, and pass it back. Without it, you will silently overwrite any edit a
  person made in the meantime.
- **A `card set` that changes the 4s automatically writes a `desc_edit` entry**
  to the лента, so your edits can be reviewed just like a person's. Do not try to
  suppress that. Writing back identical text changes nothing and records
  nothing.
- **Never invent a 4s marker.** The format belongs to chgksuite and we match it
  byte for byte. The markers are `?` for the question, `!` for the answer, `=`
  for зачёт, `!=` for незачёт, `/` for комментарий, `^` for источник, `@` for
  автор and `##` for a тур heading. A card may also hold several versions,
  separated by lines reading `(hidden-comment xy-version: имя)`. `source` folds
  those back into a single numbered question, the same way an export does.
- **`@логин` in a comment is a Mention** only when that login belongs to
  somebody on the board. xy-cli resolves it against the roster, and the member it
  names gets a notification. Use it when you want a person to actually see
  something.
- **A delete creates a tombstone that lasts 14 days**, rather than destroying
  anything immediately. Even so, ask before you delete anything you did not
  create yourself.
- Cards and comments are **in Russian**. Write in Russian when you write on a
  board.

## What xy-cli deliberately does not do

It cannot create or delete boards, change a board passphrase, manage members, or
touch Test Sessions and Playings. All of those stay in the browser. Read markers
and the 🔔 feed are not implemented either, which means that reading a card with
the CLI never clears somebody's unread dot.
