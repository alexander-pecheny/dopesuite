// Package store is qy's persistence: the schema as an ordered list of
// migrations (dopecore/schema). A new step takes the next number and goes at
// the end; `server/tests/testdata/schema.sql` pins what the list makes of an
// empty file.
package store

import (
	"database/sql"

	"code.pecheny.me/pecheny/dopesuite/dopecore/schema"
)

var Migrations = []schema.Migration{
	{Version: 1, Name: "the v1 schema", Up: schema.Exec(`
-- ===== accounts =====
-- The shape dopecore/tglogin and dopecore/authcred expect. rating_id is the
-- person's id on rating.chgk.info, which nominators and popular voters give.
create table if not exists users(
  id integer primary key,
  telegram_user_id integer unique,
  telegram_username text,
  telegram_name text,
  username text unique,
  password_hash text,
  password_salt text,
  rating_id integer,
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

-- Admins and jurors are named by their telegram handle, so the organisers can
-- add them before they have ever logged in.
create table if not exists admins(
  telegram_username text primary key collate nocase
);

-- ===== the contest =====
-- Every date is a calendar day in Moscow time, inclusive at both ends.
create table if not exists contests(
  year integer primary key,
  questions_from text not null,
  questions_to text not null,
  nomination_start text not null,
  nomination_end text not null,
  prelim_start text not null,
  prelim_end text not null,
  factcheck_start text not null,
  factcheck_end text not null,
  final_start text not null,
  final_end text not null,
  results_date text not null,
  main_slots integer not null default 5,
  reserve_slots integer not null default 3,
  portion_size integer not null default 36,
  portion_days integer not null default 3,
  expert_cutoff integer not null default 10,
  people_cutoff integer not null default 3,
  numbered_at text,
  shortlist_frozen_at text,
  results_published_at text
);

create table if not exists jurors(
  year integer not null references contests(year) on delete cascade,
  telegram_username text not null collate nocase,
  kind text not null check (kind in ('expert','people')),
  name text not null,
  primary key(year, telegram_username)
);

-- A question as nominated. orig is what the nominator gave, parsed, with its
-- credits; clean is the anonymised copy the jury reads. Both are JSON objects
-- of the question's fields (domain/question).
create table if not exists questions(
  id integer primary key,
  year integer not null references contests(year) on delete cascade,
  qy integer,
  status text not null default 'new' check (status in ('new','approved','rejected')),
  merged_into integer references questions(id) on delete set null,
  gq_id integer,
  link text not null default '',
  published integer not null default 0,
  source_text text not null,
  orig text not null default '{}',
  clean text not null default '{}',
  tournament_id integer,
  tournament_title text not null default '',
  tournament_start text not null default '',
  tournament_end text not null default '',
  factcheck text not null default '',
  extra_context text not null default '',
  author_declined integer not null default 0,
  shortlist_pin text check (shortlist_pin in ('add','remove')),
  ai_status text not null default 'pending' check (ai_status in ('pending','done','failed')),
  ai_error text not null default '',
  created_at text not null,
  updated_at text not null,
  unique(year, qy)
);
create unique index if not exists questions_gq on questions(year, gq_id) where gq_id is not null;

-- One nominator's slot: 1..main_slots are the main picks, the rest reserves.
create table if not exists nominations(
  id integer primary key,
  year integer not null references contests(year) on delete cascade,
  user_id integer not null references users(id) on delete cascade,
  slot integer not null,
  question_id integer not null references questions(id) on delete cascade,
  created_at text not null,
  unique(year, user_id, slot)
);

-- A proposed redaction of one field of a question's clean copy.
create table if not exists suggestions(
  id integer primary key,
  question_id integer not null references questions(id) on delete cascade,
  field text not null,
  quote text not null,
  replacement text not null,
  reason text not null default '',
  source text not null check (source in ('rule','ai')),
  status text not null default 'pending' check (status in ('pending','accepted','rejected')),
  created_at text not null
);
create index if not exists suggestions_question on suggestions(question_id, id);

-- ===== voting =====
-- A first-stage like. A juror may withdraw it during the factcheck, which
-- deletes the row like any other unlike.
create table if not exists likes(
  user_id integer not null references users(id) on delete cascade,
  question_id integer not null references questions(id) on delete cascade,
  created_at text not null,
  primary key(user_id, question_id)
);

create table if not exists shortlist(
  year integer not null references contests(year) on delete cascade,
  question_id integer not null references questions(id) on delete cascade,
  primary key(year, question_id)
);

-- An expert juror's 1–10 mark. A ballot counts only once every shortlisted
-- question has one.
create table if not exists scores(
  user_id integer not null references users(id) on delete cascade,
  question_id integer not null references questions(id) on delete cascade,
  score integer not null check (score between 1 and 10),
  updated_at text not null,
  primary key(user_id, question_id)
);

-- The popular vote: three different questions for 5, 4 and 3 points.
create table if not exists popular_votes(
  year integer not null references contests(year) on delete cascade,
  user_id integer not null references users(id) on delete cascade,
  first_id integer not null references questions(id) on delete cascade,
  second_id integer not null references questions(id) on delete cascade,
  third_id integer not null references questions(id) on delete cascade,
  updated_at text not null,
  primary key(year, user_id)
);

-- ===== pages =====
-- The CMS, after studchr's site_v2: a page per path, every save a revision,
-- and uploaded files served at their own path.
create table if not exists pages(
  path text primary key,
  title text not null,
  format text not null default 'markdown' check (format in ('markdown','html')),
  body text not null,
  updated_at text not null,
  updated_by integer references users(id)
);

create table if not exists page_revisions(
  id integer primary key,
  path text not null,
  title text not null,
  format text not null,
  body text not null,
  deleted integer not null default 0,
  created_at text not null,
  author_id integer references users(id)
);
create index if not exists page_revisions_path on page_revisions(path, id);

create table if not exists assets(
  path text primary key,
  content_type text not null,
  data blob not null,
  updated_at text not null,
  updated_by integer references users(id)
);
`)},
}

func Migrate(db *sql.DB) error { return schema.Apply(db, Migrations) }
