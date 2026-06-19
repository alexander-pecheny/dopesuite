package server

import "database/sql"

// migrate brings the schema up to date. Each version is applied once, gated on a
// row in schema_versions, mirroring dope's migration runner. The whole M1 schema
// is version 1; later changes append new versioned blocks.
//
// Content columns suffixed `_enc` hold client-side encryption envelopes (opaque
// BLOBs, base64 over the wire). Everything else is plaintext structural metadata
// the server needs to order, sync, and authorize (see PLAN §1 trust model).
func migrate(db *sql.DB) error {
	if _, err := db.Exec(`
create table if not exists schema_versions(
  version integer primary key,
  applied_at text not null
);

-- ===== auth (ported from dope) =====
create table if not exists users(
  id integer primary key,
  telegram_user_id integer unique,
  telegram_username text,
  username text unique,
  created_at text not null,
  updated_at text not null,
  password_hash text
);

create table if not exists invites(
  id integer primary key,
  code text not null unique,
  created_by integer references users(id),
  used_by integer references users(id),
  used_at text,
  created_at text not null,
  expires_at text not null
);

create table if not exists telegram_login_codes(
  id integer primary key,
  code text not null unique,
  kind text not null check (kind in ('register','login')),
  invite_id integer references invites(id),
  user_id integer references users(id),
  telegram_user_id integer,
  telegram_username text,
  created_at text not null,
  expires_at text not null,
  consumed_at text
);

create table if not exists sessions(
  id integer primary key,
  user_id integer not null references users(id) on delete cascade,
  token_hash text not null unique,
  created_at text not null,
  expires_at text not null,
  last_seen_at text not null
);

-- ===== encrypted boards =====
create table if not exists boards(
  id integer primary key,
  owner_user_id integer not null references users(id),
  name_enc blob not null,
  kdf_salt blob not null,
  kdf_params text not null,
  wrapped_key blob not null,
  verify_token blob not null,
  created_at text not null,
  updated_at text not null,
  deleted_at text
);

create table if not exists board_members(
  board_id integer not null references boards(id) on delete cascade,
  user_id integer not null references users(id) on delete cascade,
  role text not null check (role in ('owner','editor')),
  primary key (board_id, user_id)
);

create table if not exists lists(
  id integer primary key,
  board_id integer not null references boards(id) on delete cascade,
  type text not null check (type in ('normal','test')) default 'normal',
  title_enc blob not null,
  rank text not null,
  created_at text not null,
  updated_at text not null,
  deleted_at text
);

create table if not exists cards(
  id integer primary key,
  board_id integer not null references boards(id) on delete cascade,
  list_id integer not null references lists(id) on delete cascade,
  kind text not null check (kind in ('normal','test')) default 'normal',
  description_enc blob not null,
  rank text not null,
  created_at text not null,
  updated_at text not null,
  deleted_at text
);

create table if not exists labels(
  id integer primary key,
  board_id integer not null references boards(id) on delete cascade,
  name_enc blob not null,
  color_enc blob not null,
  kind text not null check (kind in ('normal','test_taken','test_missed')) default 'normal',
  created_at text not null,
  deleted_at text
);

create table if not exists card_labels(
  card_id integer not null references cards(id) on delete cascade,
  label_id integer not null references labels(id) on delete cascade,
  primary key (card_id, label_id)
);

create table if not exists timeline_events(
  id integer primary key,
  board_id integer not null references boards(id) on delete cascade,
  card_id integer not null references cards(id) on delete cascade,
  type text not null check (type in (
    'comment','desc_edit','label_add','label_remove',
    'attach_add','attach_remove','attach_replace')),
  author_user_id integer references users(id),
  created_at text not null,
  payload_enc blob not null
);

create table if not exists attachments(
  id integer primary key,
  board_id integer not null references boards(id) on delete cascade,
  card_id integer not null references cards(id) on delete cascade,
  filename_enc blob not null,
  mime text not null,
  size integer not null,
  lossless integer not null default 0,
  blob_ref text not null,
  created_at text not null,
  deleted_at text
);

create table if not exists board_player_map(
  board_id integer primary key references boards(id) on delete cascade,
  payload_enc blob not null,
  updated_at text not null
);

create index if not exists idx_lists_board on lists(board_id);
create index if not exists idx_cards_list on cards(list_id);
create index if not exists idx_cards_board on cards(board_id);
create index if not exists idx_labels_board on labels(board_id);
create index if not exists idx_timeline_card on timeline_events(card_id);
create index if not exists idx_attach_card on attachments(card_id);
create index if not exists idx_members_user on board_members(user_id);

insert or ignore into schema_versions(version, applied_at)
  values(1, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`); err != nil {
		return err
	}
	return nil
}
