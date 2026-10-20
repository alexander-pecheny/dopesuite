-- index page_revisions_path
CREATE INDEX page_revisions_path on page_revisions(path, id);

-- index questions_gq
CREATE UNIQUE INDEX questions_gq on questions(year, gq_id) where gq_id is not null;

-- index suggestions_question
CREATE INDEX suggestions_question on suggestions(question_id, id);

-- table admins
CREATE TABLE admins(
  telegram_username text primary key collate nocase
);

-- table assets
CREATE TABLE assets(
  path text primary key,
  content_type text not null,
  data blob not null,
  updated_at text not null,
  updated_by integer references users(id)
);

-- table contests
CREATE TABLE contests(
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

-- table jurors
CREATE TABLE jurors(
  year integer not null references contests(year) on delete cascade,
  telegram_username text not null collate nocase,
  kind text not null check (kind in ('expert','people')),
  name text not null,
  primary key(year, telegram_username)
);

-- table likes
CREATE TABLE likes(
  user_id integer not null references users(id) on delete cascade,
  question_id integer not null references questions(id) on delete cascade,
  created_at text not null,
  primary key(user_id, question_id)
);

-- table nominations
CREATE TABLE nominations(
  id integer primary key,
  year integer not null references contests(year) on delete cascade,
  user_id integer not null references users(id) on delete cascade,
  slot integer not null,
  question_id integer not null references questions(id) on delete cascade,
  created_at text not null,
  unique(year, user_id, slot)
);

-- table page_revisions
CREATE TABLE page_revisions(
  id integer primary key,
  path text not null,
  title text not null,
  format text not null,
  body text not null,
  deleted integer not null default 0,
  created_at text not null,
  author_id integer references users(id)
);

-- table pages
CREATE TABLE pages(
  path text primary key,
  title text not null,
  format text not null default 'markdown' check (format in ('markdown','html')),
  body text not null,
  updated_at text not null,
  updated_by integer references users(id)
);

-- table popular_votes
CREATE TABLE popular_votes(
  year integer not null references contests(year) on delete cascade,
  user_id integer not null references users(id) on delete cascade,
  first_id integer not null references questions(id) on delete cascade,
  second_id integer not null references questions(id) on delete cascade,
  third_id integer not null references questions(id) on delete cascade,
  updated_at text not null,
  primary key(year, user_id)
);

-- table questions
CREATE TABLE questions(
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

-- table schema_versions
CREATE TABLE schema_versions(
  version integer primary key,
  applied_at text not null
);

-- table scores
CREATE TABLE scores(
  user_id integer not null references users(id) on delete cascade,
  question_id integer not null references questions(id) on delete cascade,
  score integer not null check (score between 1 and 10),
  updated_at text not null,
  primary key(user_id, question_id)
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

-- table shortlist
CREATE TABLE shortlist(
  year integer not null references contests(year) on delete cascade,
  question_id integer not null references questions(id) on delete cascade,
  primary key(year, question_id)
);

-- table suggestions
CREATE TABLE suggestions(
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

-- table users
CREATE TABLE users(
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

