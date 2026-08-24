# xy-cli holds a data key on disk

## Status

Accepted, 2026-08-24.

## Context

xy exists so that a server never sees a question. Every content column is
sealed under a per-board data key that only a passphrase-derived KEK unwraps,
and the browser keeps that unwrapped key in IndexedDB so a board opens without
retyping the passphrase.

`xy-cli` is the same client without a person in front of it: an agent reads and
writes cards, comments and labels between one command and the next. Deriving
the KEK costs ~0.3 s of scrypt at N=65536, and there is nobody to type a
passphrase for each of a hundred commands.

## Decision

`xy-cli unlock <board>` asks for the passphrase once, unwraps the data key, and
writes the raw key into `~/.config/xy-cli/state.json` (mode 0600, directory
0700) beside the API token. `xy-cli lock` forgets one; `lock --all` and
`logout` forget them all. `XY_CLI_STATE` moves the file.

The consequences chosen deliberately:

- **This is the browser's posture, on a filesystem.** IndexedDB stores the same
  raw key unencrypted; a file readable only by the account's own user is the
  same trust boundary, stated out loud instead of hidden in a browser profile.
- **The threat model is unchanged: the server.** xy defends questions from the
  host, not from the editor's own machine. Anyone who can read a 0600 file in
  the user's home directory can also read the browser profile next to it.
- **The passphrase is never stored** — only the key it unwraps, which is why a
  passphrase change (which re-wraps the same key) leaves held keys working, and
  why the file cannot be brute-forced back into a passphrase.
- **A key can be forgotten without touching the board**: `lock` deletes the
  local copy and nothing else, so an agent's access ends where the file ends.
- **No board creation, no passphrase changes.** The CLI never sets a board
  passphrase; boards are born and re-keyed in the browser, by a person who
  chose the words. So a passphrase reaches the CLI's stdout never, and its
  stdin once.

## Alternatives considered

**A daemon holding keys in RAM.** Nothing at rest, but the agent then depends
on a process the user must keep alive, and every crash means retyping every
passphrase.

**Re-deriving from a passphrase per command** (env var or prompt). ~0.3–1 s of
scrypt per command, and the passphrase itself — not merely one board's key —
would sit in the agent's environment.

**The OS keyring.** Session-scoped and no plaintext file, at the cost of a
dbus/libsecret dependency in a repo that is deliberately pure Go and has to run
headless.
