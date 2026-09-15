# Spliff

Shared expenses for a circle of friends. People in a **Group** record who paid
for whom, in any currency, and the Group always knows who owes whom — Net
balances in the Group's Base currency and the fewest transfers that settle them.

- **Backend**: Go 1.26, SQLite (WAL, `modernc.org/sqlite`, pure Go, no cgo).
- **Frontend**: strict-TypeScript ES modules (root ADR-0001) + the DopeUIKit
  design system, embedded in the binary. Mobile-first: the audience enters bills
  at the table.
- **Language**: English only. There is no second catalog.

[`CONTEXT.md`](CONTEXT.md) is the glossary, [`docs/spec-v1.md`](docs/spec-v1.md)
the product shape, [`AGENTS.md`](AGENTS.md) the codebase map.

## Develop

```sh
just dev            # server (assets hot-read from disk); polls telegram if SPLIFF_BOT_TOKEN is set
just test           # go test + deno frontend tests
just check          # this module: fmt + vet + tidy-check + test
just pre-commit     # the whole repo, incl. class-check — run before a commit
```

Server listens on `$PORT` (default 9676); database at `$SPLIFF_DB` (default
`spliff.db`). Config via `.env` (copy from [`.env.example`](.env.example)).

Registration is telegram-only, so an instance with no bot needs a password
account made from the shell — the password comes in on stdin so it never
reaches the shell history:

```sh
printf '<password>' | SPLIFF_DB=… spliff-server adduser <username>
```

The same accounts can be made in bulk at `/admin/create_users`, by whoever holds
the username `$SPLIFF_ADMIN_USER` names.

## Money

A Transaction is stored as integer minor units in the currency it happened in,
plus its date; nothing converted is ever written
([`docs/adr/0001`](docs/adr/0001-amounts-keep-their-currency-and-convert-on-read.md)).
The server fetches one full rate table a day from
[open.er-api.com](https://open.er-api.com/v6/latest/USD) — USD base, ~160
currencies — and keeps every table it ever fetched. A Transaction converts with
the table nearest its own date, the earlier one on a tie, and a failed fetch
simply leaves the previous table in use.

So a Group's Base currency is a knob and not a commitment: changing it restates
every balance and rewrites nothing.

## Deployment & backups

**Photo bytes are files on disk, not rows in SQLite** (`dopecore/blobstore`:
random-ref, sharded, write-once). The database keeps the ref. So a backup has
**two halves, and a restore needs both** — restore `spliff.db` alone and every
Photo becomes a dangling ref:

| what | how | where |
| --- | --- | --- |
| `spliff.db` | litestream (continuous) | `r2:backups/spliff/spliff.db` |
| `blobs/` | `rclone sync --backup-dir`, hourly systemd timer | `r2:backups/spliff/blobs` (trash: `…/blobs-trash/<date>`) |

The two mechanisms are the ones xy uses, for the same reasons. litestream takes
timer-driven *full* snapshots, so a tree of photographs kept in the database
would be re-uploaded whole on every snapshot; `rclone sync` of an immutable
write-once tree uploads each blob exactly once. And `sync` with a `--backup-dir`
prefix MOVES a locally-deleted blob into a dated `blobs-trash/` prefix rather
than destroying it, so a mistaken delete has a recovery window on both halves.

Restore both:

```sh
litestream restore -o /var/lib/spliff/spliff.db r2:backups/spliff/spliff.db
set -a; . /etc/spliff-blobs-backup.env; set +a
RCLONE_CONFIG=/dev/null rclone copy r2:backups/spliff/blobs /var/lib/spliff/blobs
chown -R spliff:spliff /var/lib/spliff
```

The hourly half, as a pair of units:

```ini
# /etc/systemd/system/spliff-blobs-backup.service
[Unit]
Description=spliff blob backup to R2

[Service]
Type=oneshot
User=spliff
EnvironmentFile=/etc/spliff-blobs-backup.env
ExecStart=/usr/bin/rclone sync /var/lib/spliff/blobs r2:backups/spliff/blobs \
  --backup-dir r2:backups/spliff/blobs-trash/%%Y-%%m-%%d --fast-list --transfers 8
```

```ini
# /etc/systemd/system/spliff-blobs-backup.timer
[Unit]
Description=hourly spliff blob backup

[Timer]
OnCalendar=hourly
Persistent=true
RandomizedDelaySec=300

[Install]
WantedBy=timers.target
```

The service unit itself is [`deploy/spliff.service.example`](deploy/spliff.service.example).
Two of its `ReadWritePaths` are load-bearing under `ProtectSystem=strict`:
`/var/lib/spliff` for the database and the blobs, and `/run/lock` for the bot's
poll lock (root ADR-0005) — without the second, the instance quietly stops
offering telegram login.

### Staging — splifftest

The same binary on the same box, against a copy of prod's database, so a
release's startup migrations rehearse before prod sees them. `just
deploy-staging` (target `splifftest` in `deploy.py`).

| | prod | staging |
| --- | --- | --- |
| unit / port | `spliff.service`, 9676 | `splifftest.service`, 9686 |
| binary / env | `/opt/spliff`, `/etc/spliff.env` | `/opt/splifftest`, `/etc/splifftest.env` |
| data | `/var/lib/spliff` | `/var/lib/splifftest` |
| litestream | replicated | **no** — staging must never write to prod's replica |
| telegram bot | polls in the server | **none** — bootstrap with `spliff-server adduser` |

Refresh staging's data from prod with SQLite's online backup (consistent against
a live WAL) and hardlink the blobs, which costs no disk because the store is
write-once:

```sh
sudo systemctl stop splifftest
sudo rm -rf /var/lib/splifftest/spliff.db /var/lib/splifftest/blobs
sudo sqlite3 /var/lib/spliff/spliff.db ".backup /var/lib/splifftest/spliff.db"
sudo cp -al /var/lib/spliff/blobs /var/lib/splifftest/blobs
sudo chown -R spliff:spliff /var/lib/splifftest && sudo systemctl start splifftest
```

Never sweep `/tmp/systemd-private-*` during disk cleanup — those are the live
`PrivateTmp` directories of the running services, and deleting one makes that
unit's `systemctl reload` fail with `status=226/NAMESPACE` until it restarts.
