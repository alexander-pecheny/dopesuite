package server

import (
	"database/sql"
	"time"
)

// migrate brings the schema up to date. Each version is applied once, gated on a
// row in schema_versions, mirroring dope's migration runner. The whole M1 schema
// is version 1; later changes append new versioned blocks.
//
// Content columns suffixed `_enc` hold client-side encryption envelopes (opaque
// BLOBs, base64 over the wire). Everything else is plaintext structural metadata
// the server needs to order, sync, and authorize (the trust model — see AGENTS.md).
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
	if err := migrateV9(db); err != nil {
		return err
	}
	if err := migrateV10(db); err != nil {
		return err
	}
	if err := migrateV11(db); err != nil {
		return err
	}
	if err := migrateV12(db); err != nil {
		return err
	}
	if err := migrateV13(db); err != nil {
		return err
	}
	if err := migrateV14(db); err != nil {
		return err
	}
	if err := migrateV15(db); err != nil {
		return err
	}
	if err := migrateV16(db); err != nil {
		return err
	}
	if err := migrateV17(db); err != nil {
		return err
	}
	if err := migrateV18(db); err != nil {
		return err
	}
	if err := migrateV19(db); err != nil {
		return err
	}
	return nil
}

// migrateV19 stores a tour's Declaration: which Test Sessions its «Вопросы
// тестировали» line names. Board data rather than a per-reader preference —
// the preamble ships with the package, so two editors preparing it must see one
// answer. Undeclared, a tour falls back to the ЧГК custom (everyone who saw more
// than half its questions), which is why absence is meaningful and a row per
// named session is enough.
//
// A tour is a List or a whole List Group (exportScope), hence exactly one of the
// two FKs; both cascade, so a Declaration cannot outlive its tour. Linking or
// dissolving a group drops the Declarations involved: the claim was about a
// specific set of questions, and regrouping makes it a claim about a different
// one.
func migrateV19(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`select count(*) from schema_versions where version = 19`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := db.Exec(`
create table if not exists tour_testers(
  board_id integer not null references boards(id) on delete cascade,
  list_id integer references lists(id) on delete cascade,
  group_id integer references list_groups(id) on delete cascade,
  session_id integer references test_sessions(id) on delete cascade,
  check ((list_id is null) <> (group_id is null))
);
create unique index if not exists idx_tour_testers_list
  on tour_testers(list_id, session_id) where list_id is not null and session_id is not null;
create unique index if not exists idx_tour_testers_group
  on tour_testers(group_id, session_id) where group_id is not null and session_id is not null;
-- A NULL session_id marks «declared, and it names nobody». Without it an empty
-- Declaration is indistinguishable from never having declared, and the custom
-- silently re-ticks what the editor just cleared. NULLs compare distinct in
-- SQLite, so the marker needs uniqueness of its own.
create unique index if not exists idx_tour_testers_empty_list
  on tour_testers(list_id) where list_id is not null and session_id is null;
create unique index if not exists idx_tour_testers_empty_group
  on tour_testers(group_id) where group_id is not null and session_id is null;
create index if not exists idx_tour_testers_board on tour_testers(board_id);

insert or ignore into schema_versions(version, applied_at)
  values(19, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`)
	return err
}

// migrateV18 turns a test session from a Card in a test List into its own
// board-level entity, and rebuilds how a label reaches a question.
//
// A label is no longer bound to a session (ADR-0004): a label ASSIGNMENT carries
// an optional session, so «взяли» is one ordinary board label composed onto a
// Playing rather than one label per sitting. Being played at a test is its own
// fact — card_sessions — because that, not a label, is what «Видели» reads.
//
// Nothing here decrypts. A test card's description_enc holds the session's JSON
// under the board key, so the ciphertext moves verbatim into
// test_sessions.meta_enc and the client folds the old {datetime,title,testers}
// shape forward on first read (sessions.ts#parseSession). An old test label keeps
// the exact name it already has and becomes a plain board label — 1↔1, no
// regeneration, so no key is needed for that either.
//
// A test card carrying attachments cannot dissolve (the bytes have nowhere to
// go), so it stays as an ordinary card in its now-ordinary list. One with only
// comments leaves nothing behind: the comments move onto the session, which is
// what they always were.
func migrateV18(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`select count(*) from schema_versions where version = 18`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}

	// timeline_events is AUTOINCREMENT for the card_reads watermark (migrateV17);
	// dropping the table would drop its sqlite_sequence row and let ids be reused.
	var oldSeq sql.NullInt64
	if err := db.QueryRow(`select seq from sqlite_sequence where name = 'timeline_events'`).Scan(&oldSeq); err != nil && err != sql.ErrNoRows {
		return err
	}

	if _, err := db.Exec(`
pragma foreign_keys = off;

create table if not exists test_sessions(
  id integer primary key autoincrement,
  board_id integer not null references boards(id) on delete cascade,
  meta_enc blob not null,
  created_at text not null,
  deleted_at text
);

-- A Playing: this question was played at that test. Its own fact, not a side
-- effect of labelling — «Видели» reads this, and a scoped label hangs off it.
create table if not exists card_sessions(
  card_id integer not null references cards(id) on delete cascade,
  session_id integer not null references test_sessions(id) on delete cascade,
  primary key (card_id, session_id)
);

-- Which labels were the auto-created «взяли»/«не взяли» ones, captured while
-- labels.kind still exists: the data half needs it to tell a session's own
-- labels from a hand-made label that happens to sit on a test card, and the
-- rebuild below is about to drop the column.
create table _v18_test_labels as select id from labels where kind like 'test%';

create table labels_v18(
  id integer primary key,
  board_id integer not null references boards(id) on delete cascade,
  name_enc blob not null,
  color_enc blob not null,
  created_at text not null,
  deleted_at text
);
insert into labels_v18(id, board_id, name_enc, color_enc, created_at, deleted_at)
  select id, board_id, name_enc, color_enc, created_at, deleted_at from labels;

-- An assignment carries an optional Playing: null = the author's own view, set =
-- what the testers thought at that sitting. The same label may therefore appear
-- on one card twice, which is the point.
create table card_labels_v18(
  card_id integer not null references cards(id) on delete cascade,
  label_id integer not null references labels(id) on delete cascade,
  session_id integer references test_sessions(id) on delete cascade,
  primary key (card_id, label_id, session_id)
);
insert into card_labels_v18(card_id, label_id, session_id)
  select card_id, label_id, null from card_labels;

drop table card_labels;
drop table labels;
alter table labels_v18 rename to labels;
alter table card_labels_v18 rename to card_labels;

-- SQLite compares NULLs as DISTINCT, so the primary key above does not make the
-- unscoped assignment unique: (1,5,null) inserts twice without complaint. This
-- closes that hole exactly where it opens.
create unique index if not exists idx_card_labels_unscoped
  on card_labels(card_id, label_id) where session_id is null;

-- timeline: a comment may hang off a card, off a session, or off both.
create table timeline_events_v18(
  id integer primary key autoincrement,
  board_id integer not null references boards(id) on delete cascade,
  card_id integer references cards(id) on delete cascade,
  session_id integer references test_sessions(id) on delete cascade,
  type text not null check (type in (
    'comment','desc_edit','label_add','label_remove',
    'attach_add','attach_remove','attach_replace')),
  author_user_id integer references users(id),
  created_at text not null,
  payload_enc blob not null,
  deleted_at text,
  edited_at text,
  is_excerpt integer not null default 0,
  reply_to_id integer references timeline_events(id),
  check (card_id is not null or session_id is not null)
);
insert into timeline_events_v18(id, board_id, card_id, session_id, type, author_user_id,
    created_at, payload_enc, deleted_at, edited_at, is_excerpt, reply_to_id)
  select id, board_id, card_id, null, type, author_user_id,
    created_at, payload_enc, deleted_at, edited_at, is_excerpt, reply_to_id
  from timeline_events;
drop table timeline_events;
alter table timeline_events_v18 rename to timeline_events;

create index if not exists idx_sessions_board on test_sessions(board_id);
create index if not exists idx_labels_board on labels(board_id);
create index if not exists idx_card_sessions_session on card_sessions(session_id);
create index if not exists idx_card_labels_session on card_labels(session_id);
create index if not exists idx_timeline_card on timeline_events(card_id);
create index if not exists idx_timeline_session on timeline_events(session_id);
create index if not exists idx_timeline_reply on timeline_events(reply_to_id);

alter table users add column timezone text;
alter table users add column announce_cities text;
alter table users add column session_title_mode text;
alter table users add column onboarded_at text;
pragma foreign_keys = on;
`); err != nil {
		return err
	}

	if oldSeq.Valid {
		if _, err := db.Exec(
			`update sqlite_sequence set seq = ? where name = 'timeline_events' and seq < ?`,
			oldSeq.Int64, oldSeq.Int64); err != nil {
			return err
		}
	}

	if err := migrateV18Sessions(db); err != nil {
		return err
	}

	_, err := db.Exec(`
insert or ignore into schema_versions(version, applied_at)
  values(18, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`)
	return err
}

// migrateV18Sessions is migrateV18's data half: one session per test card, its
// comments and its labels' Playings rebound, then the test lists demoted.
//
// It runs AFTER the DDL above, which has already flattened labels — so the
// old kind/session_id/mark are read here from the pre-migration tables via the
// temporary copies the DDL left behind.
func migrateV18Sessions(db *sql.DB) error {
	now := rfc3339(time.Now().UTC())
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	rows, err := tx.Query(`select id, board_id, description_enc from cards where kind = 'test' and deleted_at is null`)
	if err != nil {
		return err
	}
	type testCard struct {
		id, boardID int64
		desc        []byte
	}
	var cards []testCard
	for rows.Next() {
		var c testCard
		if err := rows.Scan(&c.id, &c.boardID, &c.desc); err != nil {
			rows.Close()
			return err
		}
		cards = append(cards, c)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, c := range cards {
		res, err := tx.Exec(
			`insert into test_sessions(board_id, meta_enc, created_at) values(?, ?, ?)`,
			c.boardID, c.desc, now)
		if err != nil {
			return err
		}
		sid, err := res.LastInsertId()
		if err != nil {
			return err
		}
		// The auto-created «взяли»/«не взяли» pair was assigned to the test card
		// itself, so card_labels already records which labels are this session's.
		// Every question carrying one of them was PLAYED at this session — that is
		// the Playing the new model needs, and the label assignment stays unscoped
		// because its name already says which test it was («… · взяли»).
		if _, err := tx.Exec(`
insert or ignore into card_sessions(card_id, session_id)
select distinct cl.card_id, ?
from card_labels cl
join cards c on c.id = cl.card_id and c.deleted_at is null and c.kind <> 'test'
where cl.label_id in (
    select label_id from card_labels
    where card_id = ? and label_id in (select id from _v18_test_labels))`, sid, c.id); err != nil {
			return err
		}
		// A comment on a test card is a note about the session with no question
		// attached — exactly the shape session-only comments now have.
		if _, err := tx.Exec(
			`update timeline_events set session_id = ?, card_id = null where card_id = ? and type = 'comment'`,
			sid, c.id); err != nil {
			return err
		}
		// The derived events (desc_edit, label_*) logged edits to a card that is
		// about to stop existing; they have no session-level meaning.
		if _, err := tx.Exec(
			`update timeline_events set deleted_at = ? where card_id = ? and type <> 'comment' and deleted_at is null`,
			now, c.id); err != nil {
			return err
		}

		var attached int
		if err := tx.QueryRow(
			`select count(*) from attachments where card_id = ? and deleted_at is null`, c.id).Scan(&attached); err != nil {
			return err
		}
		// The session's own labels never belong on the card either way: the card is
		// about to dissolve, or it survives only to hold an attachment. Leaving
		// them on the survivor was the difference between the two paths.
		if _, err := tx.Exec(`delete from card_labels where card_id = ?`, c.id); err != nil {
			return err
		}
		if attached > 0 {
			if _, err := tx.Exec(`update cards set kind = 'normal', updated_at = ? where id = ?`, now, c.id); err != nil {
				return err
			}
			continue
		}
		if _, err := tx.Exec(`update cards set deleted_at = ? where id = ?`, now, c.id); err != nil {
			return err
		}
	}

	// Test lists are gone as a concept. One with surviving cards becomes an
	// ordinary list, keeping its (encrypted, unreadable here) title; an emptied
	// one is tombstoned.
	if _, err := tx.Exec(`
update lists set deleted_at = ?, type = 'normal'
where type = 'test' and deleted_at is null
  and not exists (select 1 from cards where cards.list_id = lists.id and cards.deleted_at is null)`, now); err != nil {
		return err
	}
	if _, err := tx.Exec(`update lists set type = 'normal' where type = 'test'`); err != nil {
		return err
	}
	if _, err := tx.Exec(`drop table if exists _v18_test_labels`); err != nil {
		return err
	}

	return tx.Commit()
}

// migrateV17 rebuilds timeline_events with AUTOINCREMENT. The reaper (ADR-0002)
// hard-deletes rows, and a plain integer primary key lets SQLite reuse a freed
// max id — but card_reads watermarks compare `id > comment_read_id`, so a new
// comment landing on a recycled id would arrive silently marked read.
// AUTOINCREMENT (sqlite_sequence) makes ids strictly increasing forever.
func migrateV17(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`select count(*) from schema_versions where version = 17`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := db.Exec(`
pragma foreign_keys = off;
create table timeline_events_v17(
  id integer primary key autoincrement,
  board_id integer not null references boards(id) on delete cascade,
  card_id integer not null references cards(id) on delete cascade,
  type text not null check (type in (
    'comment','desc_edit','label_add','label_remove',
    'attach_add','attach_remove','attach_replace')),
  author_user_id integer references users(id),
  created_at text not null,
  payload_enc blob not null,
  deleted_at text,
  edited_at text,
  is_excerpt integer not null default 0,
  reply_to_id integer references timeline_events(id)
);
insert into timeline_events_v17(id, board_id, card_id, type, author_user_id,
    created_at, payload_enc, deleted_at, edited_at, is_excerpt, reply_to_id)
  select id, board_id, card_id, type, author_user_id,
    created_at, payload_enc, deleted_at, edited_at, is_excerpt, reply_to_id
  from timeline_events;
drop table timeline_events;
alter table timeline_events_v17 rename to timeline_events;
create index if not exists idx_timeline_card on timeline_events(card_id);
create index if not exists idx_timeline_reply on timeline_events(reply_to_id);
pragma foreign_keys = on;
insert or ignore into schema_versions(version, applied_at)
  values(17, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`)
	return err
}

// migrateV16 backs open Telegram registration and per-user storage quotas.
//
// users.quota_bytes caps the encrypted content + attachment bytes across a
// user's own boards (default 25 MiB; raise per user with a single UPDATE, and
// the admin account is exempt in code). users.telegram_name keeps the public
// first/last name so the operator can reach a registrant through the bot.
//
// telegram_login_codes.telegram_name rides along from the bot so the new user
// row can keep it. desired_username is kept nullable and unused: the login
// handshake collects the username at claim time (in the request body), not up
// front — the column remains only because this migration already shipped.
func migrateV16(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`select count(*) from schema_versions where version = 16`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := db.Exec(`
alter table users add column quota_bytes integer not null default 26214400;
alter table users add column telegram_name text;
alter table telegram_login_codes add column telegram_name text;
alter table telegram_login_codes add column desired_username text;
insert or ignore into schema_versions(version, applied_at)
  values(16, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`)
	return err
}

// migrateV15 adds timeline_events.reply_to_id: a comment answering another
// comment on the same card. Threads are deliberately ONE level deep — replying
// to a reply attaches to its root (handleAddComment resolves it), so a thread is
// always a flat run under one parent and the modal that shows it needs no
// indentation, no recursion, and no ordering rules beyond "oldest first".
//
// It also scrubs payload_enc from already-tombstoned comments. Deleting a
// comment now clears its text rather than merely hiding it, because a deleted
// parent that still has replies is RENDERED (as «комментарий удалён») to keep
// the discussion reachable — and a tombstone that still carried its ciphertext
// would hand the client text the author asked to destroy.
func migrateV15(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`select count(*) from schema_versions where version = 15`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := db.Exec(`
alter table timeline_events add column reply_to_id integer references timeline_events(id);
create index if not exists idx_timeline_reply on timeline_events(reply_to_id);
update timeline_events set payload_enc = x'', is_excerpt = 0 where deleted_at is not null;
insert or ignore into schema_versions(version, applied_at)
  values(15, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`)
	return err
}

// migrateV14 makes comments editable/deletable and lets a comment or an
// attachment be flagged as a «выписка» — an excerpt from a source, kept beside
// the question so it can be checked at a glance during a quick edit.
//
// timeline_events was append-only until now: deleted_at tombstones a comment
// (filtered out of the timeline, the activity feed and the unread rollup) and
// edited_at marks a rewritten payload. Only comments are ever edited or
// deleted — the derived events (desc_edit, label_*, attach_*) stay a log.
//
// attachments.rev counts replacements, so a client can tell that an id it has
// already downloaded now holds different bytes (see attachmentDTO.Rev).
//
// is_excerpt is a PLAINTEXT column on both tables, so the server learns which
// items are excerpts (one bit each) without learning what they say. That is the
// same accepted metadata leak as attachments.mime/size; it buys a countable,
// filterable flag instead of forcing the client to decrypt every comment and
// filename just to render «Выписок: N».
func migrateV14(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`select count(*) from schema_versions where version = 14`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := db.Exec(`
alter table timeline_events add column deleted_at text;
alter table timeline_events add column edited_at text;
alter table timeline_events add column is_excerpt integer not null default 0;
alter table attachments add column is_excerpt integer not null default 0;
alter table attachments add column rev integer not null default 0;
insert or ignore into schema_versions(version, applied_at)
  values(14, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`)
	return err
}

// migrateV13 adds users.card_title: which field a card's list preview derives
// its title from — "" / "question" (the question text, the historic behaviour)
// or "answer". A per-reader display preference, not question content, so it
// lives plaintext like users.sizes. An alias (cards.alias_enc, migrateV12) wins
// over both.
func migrateV13(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`select count(*) from schema_versions where version = 13`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := db.Exec(`
alter table users add column card_title text;
insert or ignore into schema_versions(version, applied_at)
  values(13, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`)
	return err
}

// migrateV12 adds cards.alias_enc: an optional encrypted short label for a
// question — 1–3 keywords that identify it in a list faster than the opening
// words of its text. Deliberately NOT a 4s marker: the alias is xy card
// metadata, and the 4s markers mirror chgksuite's table byte-for-byte (see
// internal/chgk/fsource), so a marker of our own would either break import /
// export parity or leak the alias into exported documents. NULL means no alias.
func migrateV12(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`select count(*) from schema_versions where version = 12`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := db.Exec(`
alter table cards add column alias_enc blob;
insert or ignore into schema_versions(version, applied_at)
  values(12, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`)
	return err
}

// migrateV11 adds users.default_author: the author name pre-filled into new
// question cards (the Текст-tab stub's "@" line and the Поля editor's Автор
// field). A display default, not question content, so it lives plaintext like
// users.sizes. NULL/empty = no default.
func migrateV11(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`select count(*) from schema_versions where version = 11`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := db.Exec(`
alter table users add column default_author text;
insert or ignore into schema_versions(version, applied_at)
  values(11, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`)
	return err
}

// migrateV10 un-encrypts board NAMES. Names were the one piece of user-entered
// data whose encryption cost more UX than it bought (you couldn't see a board's
// name without its data key — the board list showed 🔒 placeholders); everything
// else stays encrypted. Two columns: `name` (plaintext, NULL until migrated) and
// per-board `schema_version` (1 = name still in name_enc; 2 = name is plaintext).
// The server can't decrypt existing name_enc, so legacy rows start at version 1 and
// are backfilled lazily by DK-holding clients (POST /api/boards/{id}/migrate-name);
// new boards are created at version 2. Invariant: schema_version = 2 ⟺ name IS NOT
// NULL. name_enc is kept (dead once migrated) until a later retirement migration
// drops it once no version-1 boards remain.
func migrateV10(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`select count(*) from schema_versions where version = 10`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := db.Exec(`
alter table boards add column name text;
alter table boards add column schema_version integer not null default 1;
insert or ignore into schema_versions(version, applied_at)
  values(10, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`)
	return err
}

// migrateV9 adds users.sizes: a small plaintext JSON blob of the user's board
// display layout ({boardW,listW,cardLines}), shared across all of that user's
// boards and devices. These are display numbers only — no question content — so,
// like ranks and ordering, they are stored in the clear rather than encrypted.
// NULL = never set (the client falls back to its defaults). Replaces the old
// localStorage["xy.sizes"], which lived per browser and so neither followed the
// user across devices nor was scoped to an account on a shared browser.
func migrateV9(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`select count(*) from schema_versions where version = 9`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := db.Exec(`
alter table users add column sizes text;
insert or ignore into schema_versions(version, applied_at)
  values(9, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`)
	return err
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
