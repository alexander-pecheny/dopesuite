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
  kind text not null check (kind in ('normal','question','test','meta','heading','other')) default 'normal',
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
	if err := migrateV2(db); err != nil {
		return err
	}
	if err := migrateV3(db); err != nil {
		return err
	}
	if err := migrateV4(db); err != nil {
		return err
	}
	if err := migrateV5(db); err != nil {
		return err
	}
	if err := migrateV6(db); err != nil {
		return err
	}
	if err := migrateV7(db); err != nil {
		return err
	}
	if err := migrateV8(db); err != nil {
		return err
	}
	return nil
}

// migrateV8 adds board_members.last_visited_at: a per-user, per-board timestamp
// stamped when the user opens a board (POST /api/boards/{id}/visit). The board
// list orders by it (most-recently-visited first) so returning to an active
// board is one tap. NULL = never visited on record (sorts after visited boards).
func migrateV8(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`select count(*) from schema_versions where version = 8`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := db.Exec(`
alter table board_members add column last_visited_at text;
insert or ignore into schema_versions(version, applied_at)
  values(8, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`)
	return err
}

// migrateV7 adds card_reads: per-(user, card) read watermarks used for the blue
// "unread" dots. Two independent watermarks per card — content_read_id (the
// highest timeline_events.id read among desc_edit/label_*/attach_* events) and
// comment_read_id (same, for `comment` events) — let a user clear either bucket
// independently. An event is unread for a user iff its id exceeds the relevant
// watermark and it wasn't authored by that same user (own edits never count).
func migrateV7(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`select count(*) from schema_versions where version = 7`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := db.Exec(`
create table if not exists card_reads(
  user_id integer not null references users(id) on delete cascade,
  card_id integer not null references cards(id) on delete cascade,
  content_read_id integer not null default 0,
  comment_read_id integer not null default 0,
  updated_at text not null,
  primary key (user_id, card_id)
);
create index if not exists idx_card_reads_user on card_reads(user_id);
insert or ignore into schema_versions(version, applied_at)
  values(7, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`)
	return err
}

// migrateV6 adds list grouping ("list_of_lists"): a named, ordered run of
// consecutive lists that share a single question-numbering sequence and export
// as one document. `list_groups` holds the encrypted group name; `lists.group_id`
// is a nullable back-reference. Group membership + position are otherwise plain
// structural metadata (the client keeps a group's lists consecutive by rank).
func migrateV6(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`select count(*) from schema_versions where version = 6`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := db.Exec(`
create table if not exists list_groups(
  id integer primary key,
  board_id integer not null references boards(id) on delete cascade,
  name_enc blob not null,
  created_at text not null,
  updated_at text not null,
  deleted_at text
);
alter table lists add column group_id integer references list_groups(id);
create index if not exists idx_list_groups_board on list_groups(board_id);
create index if not exists idx_lists_group on lists(group_id);
insert or ignore into schema_versions(version, applied_at)
  values(6, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`)
	return err
}

// migrateV5 drops the unused board_player_map table. It backed the abandoned
// "integer player ids → name" model for test cards; testers are now stored as
// plaintext strings inside each test card's encrypted description, so the table
// never held data worth keeping.
func migrateV5(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`select count(*) from schema_versions where version = 5`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := db.Exec(`
drop table if exists board_player_map;
insert or ignore into schema_versions(version, applied_at)
  values(5, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`)
	return err
}

// migrateV4 adds cards.handout_meta_enc: an optional encrypted blob holding the
// per-question handout-generation settings (the .hndt block minus the live
// handout text — columns/rows/grouping/etc.), edited on the card or round-tripped
// through the "Генерация раздаток" modal. NULL means "no saved settings".
func migrateV4(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`select count(*) from schema_versions where version = 4`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := db.Exec(`
alter table cards add column handout_meta_enc blob;
insert or ignore into schema_versions(version, applied_at)
  values(4, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`)
	return err
}

// migrateV3 adds api_tokens: month-lived bearer tokens that authorize the
// Trello-compatible read/write API (chgksuite integration). Tokens are stored
// as a sha256 hash (like sessions); the raw value is shown to the user once.
func migrateV3(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`select count(*) from schema_versions where version = 3`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := db.Exec(`
create table if not exists api_tokens(
  id integer primary key,
  user_id integer not null references users(id) on delete cascade,
  token_hash text not null unique,
  label text,
  created_at text not null,
  expires_at text not null,
  revoked_at text,
  last_used_at text
);
create index if not exists idx_api_tokens_user on api_tokens(user_id);
insert or ignore into schema_versions(version, applied_at)
  values(3, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`)
	return err
}

// migrateV2 widens the cards.kind CHECK constraint to the chgksuite card kinds
// (question/meta/heading/other) introduced after M1. SQLite can't ALTER a CHECK
// in place, so we rebuild the table (the standard 12-step dance) with foreign
// keys disabled for the swap. Gated on schema_versions so it runs once.
func migrateV2(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`select count(*) from schema_versions where version = 2`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := db.Exec(`
pragma foreign_keys=off;
create table cards_new(
  id integer primary key,
  board_id integer not null references boards(id) on delete cascade,
  list_id integer not null references lists(id) on delete cascade,
  kind text not null check (kind in ('normal','question','test','meta','heading','other')) default 'normal',
  description_enc blob not null,
  rank text not null,
  created_at text not null,
  updated_at text not null,
  deleted_at text
);
insert into cards_new(id, board_id, list_id, kind, description_enc, rank, created_at, updated_at, deleted_at)
  select id, board_id, list_id, kind, description_enc, rank, created_at, updated_at, deleted_at from cards;
drop table cards;
alter table cards_new rename to cards;
create index if not exists idx_cards_list on cards(list_id);
create index if not exists idx_cards_board on cards(board_id);
pragma foreign_keys=on;
insert or ignore into schema_versions(version, applied_at)
  values(2, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`)
	return err
}
