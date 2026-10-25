-- Skimdit schema v1. Times are Unix seconds (UTC).

CREATE TABLE drafts (
  id          INTEGER PRIMARY KEY,
  kind        TEXT NOT NULL CHECK (kind IN ('post', 'page')),
  -- source is the published file this draft edits ('' for a new post).
  source      TEXT NOT NULL DEFAULT '',
  fields      TEXT NOT NULL DEFAULT '{}',  -- frontmatter as JSON
  body        TEXT NOT NULL DEFAULT '',
  publish_at  INTEGER,                     -- scheduled publish time, or NULL
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);

CREATE TABLE draft_revisions (
  id         INTEGER PRIMARY KEY,
  draft_id   INTEGER NOT NULL REFERENCES drafts(id) ON DELETE CASCADE,
  fields     TEXT NOT NULL,
  body       TEXT NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE INDEX draft_revisions_draft ON draft_revisions(draft_id, created_at);

CREATE TABLE jobs (
  id         INTEGER PRIMARY KEY,
  kind       TEXT NOT NULL,
  -- key makes a job idempotent: the same key never runs twice.
  key        TEXT NOT NULL UNIQUE,
  payload    TEXT NOT NULL DEFAULT '{}',
  run_at     INTEGER NOT NULL,
  attempts   INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  result     TEXT NOT NULL DEFAULT '',
  locked_until INTEGER NOT NULL DEFAULT 0,
  done_at    INTEGER,
  failed_at  INTEGER,
  created_at INTEGER NOT NULL
);
CREATE INDEX jobs_due ON jobs(done_at, failed_at, run_at);

CREATE TABLE credentials (
  id            INTEGER PRIMARY KEY CHECK (id = 1),
  password_hash TEXT NOT NULL DEFAULT '',
  totp_secret   TEXT NOT NULL DEFAULT '',
  user_handle   BLOB NOT NULL,
  updated_at    INTEGER NOT NULL
);

CREATE TABLE passkeys (
  id          BLOB PRIMARY KEY,
  name        TEXT NOT NULL,
  data        TEXT NOT NULL,   -- webauthn.Credential as JSON
  created_at  INTEGER NOT NULL,
  last_used   INTEGER
);

CREATE TABLE sessions (
  token_hash TEXT PRIMARY KEY,
  csrf       TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  ip         TEXT NOT NULL DEFAULT '',
  user_agent TEXT NOT NULL DEFAULT ''
);

CREATE TABLE login_attempts (
  id         INTEGER PRIMARY KEY,
  ip         TEXT NOT NULL,
  ok         INTEGER NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE INDEX login_attempts_ip ON login_attempts(ip, created_at);

CREATE TABLE comments (
  id           INTEGER PRIMARY KEY,
  post         TEXT NOT NULL,          -- post slug
  parent_id    INTEGER REFERENCES comments(id) ON DELETE SET NULL,
  author       TEXT NOT NULL,
  email        TEXT NOT NULL DEFAULT '',
  url          TEXT NOT NULL DEFAULT '',
  body         TEXT NOT NULL,          -- Markdown as written
  html         TEXT NOT NULL,          -- sanitized HTML
  status       TEXT NOT NULL CHECK (status IN ('pending', 'approved', 'spam', 'deleted')),
  notify       INTEGER NOT NULL DEFAULT 0,
  notify_token TEXT NOT NULL DEFAULT '',
  ip_hash      TEXT NOT NULL DEFAULT '',
  created_at   INTEGER NOT NULL
);
CREATE INDEX comments_post ON comments(post, status, created_at);

CREATE TABLE webmentions (
  id          INTEGER PRIMARY KEY,
  source      TEXT NOT NULL,
  target      TEXT NOT NULL,
  post        TEXT NOT NULL DEFAULT '',
  type        TEXT NOT NULL DEFAULT 'mention',  -- reply, like, repost, mention
  author      TEXT NOT NULL DEFAULT '',
  author_url  TEXT NOT NULL DEFAULT '',
  author_photo TEXT NOT NULL DEFAULT '',
  html        TEXT NOT NULL DEFAULT '',
  status      TEXT NOT NULL CHECK (status IN ('pending', 'verifying', 'approved', 'spam', 'invalid', 'deleted')),
  published   INTEGER,
  verified_at INTEGER,
  created_at  INTEGER NOT NULL,
  UNIQUE (source, target)
);

CREATE TABLE subscribers (
  id           INTEGER PRIMARY KEY,
  email        TEXT NOT NULL UNIQUE COLLATE NOCASE,
  status       TEXT NOT NULL CHECK (status IN ('pending', 'active', 'unsubscribed', 'bounced', 'complained')),
  token        TEXT NOT NULL,          -- confirm and unsubscribe links
  consent_at   INTEGER,
  consent_source TEXT NOT NULL DEFAULT '',
  consent_ip_hash TEXT NOT NULL DEFAULT '',
  bounces      INTEGER NOT NULL DEFAULT 0,
  created_at   INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL
);

CREATE TABLE newsletter_sends (
  id         INTEGER PRIMARY KEY,
  post       TEXT NOT NULL,
  subscriber_id INTEGER NOT NULL REFERENCES subscribers(id) ON DELETE CASCADE,
  sent_at    INTEGER,
  error      TEXT NOT NULL DEFAULT '',
  UNIQUE (post, subscriber_id)
);

CREATE TABLE stats_daily (
  day      TEXT NOT NULL,   -- YYYY-MM-DD (UTC)
  path     TEXT NOT NULL,
  referrer TEXT NOT NULL DEFAULT '',  -- referring domain only
  kind     TEXT NOT NULL DEFAULT 'page' CHECK (kind IN ('page', 'feed')),
  views    INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (day, path, referrer, kind)
);

CREATE TABLE ap_keys (
  id          INTEGER PRIMARY KEY CHECK (id = 1),
  private_pem TEXT NOT NULL,
  public_pem  TEXT NOT NULL,
  created_at  INTEGER NOT NULL
);

CREATE TABLE ap_followers (
  actor      TEXT PRIMARY KEY,
  inbox      TEXT NOT NULL,
  shared_inbox TEXT NOT NULL DEFAULT '',
  follow_id  TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL
);

CREATE TABLE ap_activities (
  id         TEXT PRIMARY KEY,
  direction  TEXT NOT NULL CHECK (direction IN ('in', 'out')),
  type       TEXT NOT NULL,
  actor      TEXT NOT NULL,
  object     TEXT NOT NULL DEFAULT '',
  data       TEXT NOT NULL,
  created_at INTEGER NOT NULL
);

CREATE TABLE crossposts (
  post     TEXT NOT NULL,
  network  TEXT NOT NULL,
  url      TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (post, network)
);
