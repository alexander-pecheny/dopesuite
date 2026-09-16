-- index idx_group_invites_group
CREATE INDEX idx_group_invites_group on group_invites(group_id);

-- index idx_transaction_history_tx
CREATE INDEX idx_transaction_history_tx on transaction_history(transaction_id, id);

-- index idx_transaction_photos_tx
CREATE INDEX idx_transaction_photos_tx on transaction_photos(transaction_id, id);

-- index idx_transactions_group
CREATE INDEX idx_transactions_group on transactions(group_id, id);

-- table group_invite_uses
CREATE TABLE group_invite_uses(
  id integer primary key,
  invite_id integer not null references group_invites(id) on delete cascade,
  user_id integer not null references users(id) on delete cascade,
  status text not null check (status in ('joined','pending','declined')),
  requested_at text not null,
  decided_at text,
  unique(invite_id, user_id)
);

-- table group_invites
CREATE TABLE group_invites(
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

-- table group_members
CREATE TABLE "group_members"(
  id integer primary key autoincrement,
  group_id integer not null references groups(id) on delete cascade,
  user_id integer references users(id) on delete cascade,
  display_name text,
  joined_at text not null,
  unique(group_id, user_id),
  check (user_id is not null or display_name is not null)
);

-- table groups
CREATE TABLE groups(
  id integer primary key,
  name text not null,
  base_currency text not null,
  owner_id integer not null references users(id),
  created_at text not null
);

-- table rate_fetches
CREATE TABLE rate_fetches(
  day text primary key,
  source text not null,
  fetched_at text not null
);

-- table rate_tables
CREATE TABLE rate_tables(
  day text not null,
  currency text not null,
  rate text not null,
  primary key (day, currency)
);

-- table schema_versions
CREATE TABLE schema_versions(
  version integer primary key,
  applied_at text not null
);

-- table sessions
CREATE TABLE sessions(
  id integer primary key,
  user_id integer not null references users(id) on delete cascade,
  token_hash text not null unique,
  created_at text not null,
  expires_at text not null,
  last_seen_at text not null
);

-- table telegram_login_codes
CREATE TABLE telegram_login_codes(
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

-- table transaction_history
CREATE TABLE transaction_history(
  id integer primary key,
  transaction_id integer not null references transactions(id) on delete cascade,
  actor_id integer references users(id),
  at text not null,
  kind text not null check (kind in ('created','edited','deleted','restored','photo_added','photo_removed')),
  before_json text,
  after_json text
);

-- table transaction_payments
CREATE TABLE "transaction_payments"(
  id integer primary key,
  transaction_id integer not null references transactions(id) on delete cascade,
  member_id integer not null,
  amount_minor integer not null,
  unique(transaction_id, member_id)
);

-- table transaction_photos
CREATE TABLE transaction_photos(
  id integer primary key,
  transaction_id integer not null references transactions(id) on delete cascade,
  blob_ref text not null,
  uploader_id integer not null references users(id),
  created_at text not null,
  width integer not null,
  height integer not null,
  bytes integer not null
);

-- table transaction_shares
CREATE TABLE "transaction_shares"(
  id integer primary key,
  transaction_id integer not null references transactions(id) on delete cascade,
  member_id integer not null,
  amount_minor integer not null,
  unique(transaction_id, member_id)
);

-- table transactions
CREATE TABLE transactions(
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

-- table users
CREATE TABLE users(
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

