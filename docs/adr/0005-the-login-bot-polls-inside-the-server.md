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

Two processes, two bridges, one host. What the boundary bought was the DB
discipline, and the server already had that: the writes the bot triggers are
the server's own, under the server's own transaction rules. Everything else
the boundary cost was real — two more units, two more env files, a shared
secret, a health probe, four deploy targets, and one failure mode (server up,
bot down or secret mismatch) that existed only because there were two.

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
- **One poller per token, enforced twice.** Telegram hands each update to
  exactly one poller, and a second one does not fail loudly — it wins some
  updates and loses the rest. On a host, `tgbot.AcquirePollLock` holds an
  `flock` named after the token's hash for as long as the process polls
  (check-and-hold in one syscall, dropped by the kernel on `kill -9`; a unit
  under `ProtectSystem=strict` needs `ReadWritePaths=/run/lock`). Across
  hosts, only Telegram knows, and it says so: a 409 is `tgbot.ErrConflict`,
  which backs the loop off hard and reports the bot unusable rather than
  retrying every three seconds in silence, as it used to.

## Consequences

Bot uptime is now server uptime: every deploy bounces it. Long polling is
offset-based and the conversation holds no state, so no update is lost — an
in-flight poll is dropped and reconnects. A panic while handling an update
would take the web server down, so the handler is wrapped in a `recover`.

If a server ever runs as more than one replica, in-process polling breaks and
the bot has to come back out. Nothing in either app is near that.
