---
status: accepted
date: 2026-08-31
---

# The login bot polls inside the server process

Each app shipped its Telegram login bot as a second systemd unit on the same
host as its server. The bot held no database handle — that was the point, after
a second long-lived writer on dope's live `fest.db` was implicated in the WAL
trouble behind the data-loss incident — so it reached the server's own write
paths over loopback HTTP behind a shared secret (`/api/telegram/register`,
`/api/telegram/login`). xy's server then reached back the other way, over a
second loopback endpoint, to send a DM and to ask whether the bot was still
polling.

That is two processes and two bridges on one host. What the boundary bought us
was the database discipline, and the server already had that anyway: the writes
the bot causes are the server's own writes, under the server's own transaction
rules. Everything the boundary cost us was real: two more systemd units, two
more env files, a shared secret, a health probe, four deploy targets, and one
failure mode that only existed because there were two processes — the server up
with the bot down, or with the secret mismatched.

## Decision

- **The bot polls in the server process.** `dopecore/tgbot.LoginHandler` takes
  a `Registrar` interface; each app implements it against the code its HTTP
  bridge handlers used to wrap. `tgbridge` keeps the SQL and the code shape;
  its wire protocol, its secret gate and both apps' `cmd/telegram-bot` are
  gone, as are `XY_BOT_SECRET`, `XY_BOT_HEALTH_ADDR`, `DOPE_BOT_SECRET` and
  `DOPE_BOT_HEALTH_ADDR`.
- **The token is the switch.** An instance with `XY_BOT_TOKEN` /
  `TELEGRAM_BOT_TOKEN` polls; an instance without one does not, and says
  telegram login is not on offer. Staging and dev checkouts carry no token.
- **One poller per token, enforced in two places.** Telegram gives each update
  to exactly one poller, and a second poller does not fail in any obvious way:
  it simply wins some updates and loses the others. Within one host,
  `tgbot.AcquirePollLock` holds an `flock` named after the hash of the token for
  as long as the process is polling. It checks and holds in a single syscall,
  and the kernel drops it even on `kill -9`. A unit running under
  `ProtectSystem=strict` needs `ReadWritePaths=/run/lock` for this. Across
  hosts, only Telegram knows, and it tells us: a 409 becomes
  `tgbot.ErrConflict`, which backs the loop off hard and reports that the bot is
  unusable, instead of retrying every three seconds in silence the way it used
  to.

## Consequences

The bot's uptime is now the server's uptime, so every deploy restarts it. Long
polling works from an offset and the conversation keeps no state, so no update
is lost: a poll that was in flight is dropped and then reconnects. A panic while
handling an update would bring the web server down with it, so the handler is
wrapped in a `recover`.

If a server is ever run as more than one replica, polling in-process stops
working and the bot has to be moved back out. Neither app is anywhere near
that.
