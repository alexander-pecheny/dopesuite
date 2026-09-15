// Package store is Spliff's persistence: the schema as an ordered list of
// migrations, and the queries the server reads and writes it with. Everything
// a person typed is kept exactly as they typed it — a Transaction's amount
// stays in the currency it happened in, and nothing converted is ever written
// (spliff/docs/adr/0001).
package store

import (
	"database/sql"

	"pecheny.me/dopecore/schema"
)

// Migrations is the schema as a list (dopecore/schema): each step runs once and
// is recorded in schema_versions; a new step takes the next number and goes at
// the end, never in the middle. `server/tests/testdata/schema.sql` pins what
// the list makes of an empty file.
var Migrations = []schema.Migration{
	{Version: 1, Name: "the v1 schema", Up: schema.Exec(`
-- ===== accounts =====
-- The shape dopecore/tglogin and dopecore/authcred expect, plus the password
-- columns the admin bulk-create writes. password_salt is the legacy sha256
-- scheme's; Spliff has no legacy rows and never sets it, but
-- VerifyPasswordUpgrading reads it and one nullable column is cheaper than a
-- second code path.
create table if not exists users(
  id integer primary key,
  telegram_user_id integer unique,
  telegram_username text,
  telegram_name text,
  username text unique,
  password_hash text,
  password_salt text,
  created_at text not null,
  updated_at text not null
);

create table if not exists sessions(
  id integer primary key,
  user_id integer not null references users(id) on delete cascade,
  token_hash text not null unique,
  created_at text not null,
  expires_at text not null,
  last_seen_at text not null
);

create table if not exists telegram_login_codes(
  id integer primary key,
  code text not null unique,
  kind text not null check (kind in ('register','login')),
  user_id integer references users(id),
  telegram_user_id integer,
  telegram_username text,
  telegram_name text,
  created_at text not null,
  expires_at text not null,
  consumed_at text
);

-- ===== groups =====
-- base_currency is a knob and not a commitment: nothing is stored converted,
-- so changing it restates every balance and rewrites nothing.
create table if not exists groups(
  id integer primary key,
  name text not null,
  base_currency text not null,
  owner_id integer not null references users(id),
  created_at text not null
);

-- group_members carries its own id because join order is the tie-break the
-- Debt graph is sorted by, and two people admitted in the same second must
-- still have an order everybody agrees on: joined_at, then id.
create table if not exists group_members(
  id integer primary key,
  group_id integer not null references groups(id) on delete cascade,
  user_id integer not null references users(id) on delete cascade,
  joined_at text not null,
  unique(group_id, user_id)
);

-- ===== invite links (dopecore/invitelink) =====
-- The columns are the package's; only the names are ours. The code is plaintext
-- because the owner's list hands the link back days after minting, which a
-- hash cannot do, and the code buys Membership and nothing else.
create table if not exists group_invites(
  id integer primary key,
  group_id integer not null references groups(id) on delete cascade,
  code text not null unique,
  label text,
  created_by integer not null references users(id),
  created_at text not null,
  expires_at text,
  max_uses integer,
  requires_approval integer not null default 0,
  revoked_at text
);
create index if not exists idx_group_invites_group on group_invites(group_id);

create table if not exists group_invite_uses(
  id integer primary key,
  invite_id integer not null references group_invites(id) on delete cascade,
  user_id integer not null references users(id) on delete cascade,
  status text not null check (status in ('joined','pending','declined')),
  requested_at text not null,
  decided_at text,
  unique(invite_id, user_id)
);

-- ===== transactions =====
-- day is the EXPENSE date, which is what the Rate date is resolved from; it is
-- not created_at and a backdated Transaction is ordinary. There is no pinned
-- rate column in v1 (spliff/docs/adr/0001): adding one is a migration.
-- deleted_at is a tombstone — a deleted Transaction leaves the ledger, stays in
-- History and any Member may restore it, shares and photos intact.
create table if not exists transactions(
  id integer primary key,
  group_id integer not null references groups(id) on delete cascade,
  description text not null,
  day text not null,
  currency text not null,
  total_minor integer not null,
  created_by integer not null references users(id),
  created_at text not null,
  updated_at text not null,
  deleted_at text
);
create index if not exists idx_transactions_group on transactions(group_id, id);

-- One row per Member per Transaction on both sides. The Shares rule is the
-- spec's ("at most one Share per Member"); Payments carry the same constraint
-- because an edit replaces a person's Payment rather than adding a second one,
-- and because the largest-remainder ordering sorts payers by THEIR Payment.
create table if not exists transaction_payments(
  id integer primary key,
  transaction_id integer not null references transactions(id) on delete cascade,
  member_id integer not null references users(id),
  amount_minor integer not null,
  unique(transaction_id, member_id)
);

create table if not exists transaction_shares(
  id integer primary key,
  transaction_id integer not null references transactions(id) on delete cascade,
  member_id integer not null references users(id),
  amount_minor integer not null,
  unique(transaction_id, member_id)
);

-- The bytes live in the blob store (dopecore/blobstore); the row is the ref and
-- what the page needs to lay the picture out before it loads.
create table if not exists transaction_photos(
  id integer primary key,
  transaction_id integer not null references transactions(id) on delete cascade,
  blob_ref text not null,
  uploader_id integer not null references users(id),
  created_at text not null,
  width integer not null,
  height integer not null,
  bytes integer not null
);
create index if not exists idx_transaction_photos_tx on transaction_photos(transaction_id, id);

-- History is what makes full trust auditable: every change to a Transaction,
-- who made it and when, with the before and after of everything that can
-- change as JSON. It is append-only and nothing reaps it.
create table if not exists transaction_history(
  id integer primary key,
  transaction_id integer not null references transactions(id) on delete cascade,
  actor_id integer references users(id),
  at text not null,
  kind text not null check (kind in ('created','edited','deleted','restored','photo_added','photo_removed')),
  before_json text,
  after_json text
);
create index if not exists idx_transaction_history_tx on transaction_history(transaction_id, id);

-- ===== rate tables =====
-- One row per currency per calendar day (UTC), the rate a decimal STRING as the
-- source sent it: a rate read back as a float is a rate that has lost digits.
create table if not exists rate_tables(
  day text not null,
  currency text not null,
  rate text not null,
  primary key (day, currency)
);

-- One row per day that was fetched, so "we have today's table" is a point read
-- and not a count over 160 rows.
create table if not exists rate_fetches(
  day text primary key,
  source text not null,
  fetched_at text not null
);
`)},
}

// Migrate applies the list. sqlitex.Open calls it on the single pinned
// connection, before the pool widens.
func Migrate(db *sql.DB) error { return schema.Apply(db, Migrations) }
