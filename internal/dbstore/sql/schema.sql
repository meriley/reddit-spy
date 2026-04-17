-- App-scope schema bootstrap for reddit-spy.
--
-- Runs on every pod startup against the application database with the app
-- user's credentials. All statements are idempotent (IF NOT EXISTS) so this
-- is safe to re-run after every deploy.
--
-- The six original tables (discord_servers, discord_channels, subreddits,
-- rules, posts, notifications) mirror the source schema from the v2.0.x
-- standalone deploy, including the `created_at` audit column, so a
-- `pg_dump --data-only` from that source restores cleanly here.

CREATE TABLE IF NOT EXISTS discord_servers (
    id         SERIAL PRIMARY KEY,
    server_id  TEXT NOT NULL UNIQUE,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS discord_channels (
    id         SERIAL PRIMARY KEY,
    channel_id TEXT NOT NULL UNIQUE,
    server_id  INT  NOT NULL REFERENCES discord_servers(id) ON DELETE CASCADE,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS subreddits (
    id           SERIAL PRIMARY KEY,
    subreddit_id TEXT NOT NULL UNIQUE,
    created_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS rules (
    id           SERIAL PRIMARY KEY,
    target       TEXT NOT NULL,
    target_id    TEXT NOT NULL,
    exact        BOOLEAN NOT NULL DEFAULT FALSE,
    channel_id   INT  NOT NULL REFERENCES discord_channels(id) ON DELETE CASCADE,
    subreddit_id INT  NOT NULL REFERENCES subreddits(id)       ON DELETE CASCADE,
    mode         TEXT NOT NULL DEFAULT 'narrative',
    window_hours INT  NOT NULL DEFAULT 72,
    created_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
ALTER TABLE rules ADD COLUMN IF NOT EXISTS mode TEXT NOT NULL DEFAULT 'narrative';
ALTER TABLE rules ADD COLUMN IF NOT EXISTS window_hours INT NOT NULL DEFAULT 72;
CREATE INDEX IF NOT EXISTS rules_subreddit_id_idx ON rules(subreddit_id);
CREATE INDEX IF NOT EXISTS rules_channel_id_idx   ON rules(channel_id);

CREATE TABLE IF NOT EXISTS posts (
    id         SERIAL PRIMARY KEY,
    post_id    TEXT NOT NULL UNIQUE,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS notifications (
    id         SERIAL PRIMARY KEY,
    post_id    INT NOT NULL REFERENCES posts(id)            ON DELETE CASCADE,
    channel_id INT NOT NULL REFERENCES discord_channels(id) ON DELETE CASCADE,
    rule_id    INT NOT NULL REFERENCES rules(id)            ON DELETE CASCADE,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (post_id, channel_id, rule_id)
);

-- Rolling digest — one row per "active window" for (channel, subreddit).
-- window_start stamps when the digest opened; the row stays the active target
-- for new matches until now() - window_start exceeds the rule's window_hours,
-- at which point a subsequent match opens a fresh row. day_local is retained
-- for display in the footer + legacy log-lines only (no longer a unique key).
CREATE TABLE IF NOT EXISTS rolling_posts (
    id                  SERIAL PRIMARY KEY,
    channel_id          INT  NOT NULL REFERENCES discord_channels(id) ON DELETE CASCADE,
    subreddit_id        INT  NOT NULL REFERENCES subreddits(id)       ON DELETE CASCADE,
    day_local           DATE NOT NULL,
    window_start        TIMESTAMPTZ NOT NULL DEFAULT now(),
    mode                TEXT NOT NULL DEFAULT 'narrative',
    discord_message_id  TEXT NOT NULL DEFAULT '',            -- legacy; use discord_message_ids
    discord_message_ids TEXT[] NOT NULL DEFAULT '{}',        -- first + any spill messages
    narrative_title     TEXT NOT NULL DEFAULT '',
    narrative_summary   TEXT NOT NULL DEFAULT '',
    entries             JSONB NOT NULL DEFAULT '[]',         -- mode-specific structured payload
    included_post_ids   TEXT[] NOT NULL DEFAULT '{}',
    included_rule_ids   INT[]  NOT NULL DEFAULT '{}',
    latest_score        INT  NOT NULL DEFAULT 0,
    latest_comments     INT  NOT NULL DEFAULT 0,
    latest_url          TEXT NOT NULL DEFAULT '',
    latest_thumbnail    TEXT NOT NULL DEFAULT '',
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Columns added after v2.1 — idempotent for existing deploys.
ALTER TABLE rolling_posts ADD COLUMN IF NOT EXISTS mode                TEXT        NOT NULL DEFAULT 'narrative';
ALTER TABLE rolling_posts ADD COLUMN IF NOT EXISTS discord_message_ids TEXT[]      NOT NULL DEFAULT '{}';
ALTER TABLE rolling_posts ADD COLUMN IF NOT EXISTS entries             JSONB       NOT NULL DEFAULT '[]';
ALTER TABLE rolling_posts ADD COLUMN IF NOT EXISTS window_start        TIMESTAMPTZ NOT NULL DEFAULT now();
-- subreddit_ids: tracks every subreddit that has contributed to this digest.
-- Backfilled from the (legacy) scalar subreddit_id so existing open digests
-- keep their provenance. Lookup no longer keys on subreddit — see
-- rolling_posts_active_by_mode_idx — so one music-mode digest per channel
-- can collect matches from r/metalcore + r/poppunkers + … into one message.
ALTER TABLE rolling_posts ADD COLUMN IF NOT EXISTS subreddit_ids INT[] NOT NULL DEFAULT '{}';
UPDATE rolling_posts
SET    subreddit_ids = ARRAY[subreddit_id]
WHERE  subreddit_id IS NOT NULL
  AND  array_length(subreddit_ids, 1) IS NULL;
-- The legacy scalar subreddit_id was NOT NULL with a FK. Drop the NOT NULL
-- so new rows can omit it — we still populate it with the first contributing
-- sub for back-compat, but the digest key is (channel, mode, window_start).
ALTER TABLE rolling_posts ALTER COLUMN subreddit_id DROP NOT NULL;
-- Backfill window_start for pre-window rows to approximate their original
-- Phoenix-midnight start (day_local 00:00 MST ≈ 07:00 UTC). Only touches rows
-- whose window_start was just defaulted by the ALTER (i.e. within the last
-- 10 minutes) — a second run of schema.sql after startup is a no-op.
UPDATE rolling_posts
SET    window_start = day_local::timestamptz + INTERVAL '7 hours'
WHERE  window_start > now() - INTERVAL '10 minutes';
-- The day-based uniqueness no longer matches the new key shape — drop it so
-- a second same-day match can open a new window-bounded row if needed.
ALTER TABLE rolling_posts DROP CONSTRAINT IF EXISTS rolling_posts_channel_id_subreddit_id_day_local_key;
-- Backfill the new array from the legacy scalar if the array is empty.
UPDATE rolling_posts
SET    discord_message_ids = ARRAY[discord_message_id]
WHERE  discord_message_id <> ''
  AND  array_length(discord_message_ids, 1) IS NULL;
-- Drop the legacy 'discord_message_id <> ''' CHECK — the array is now the
-- authoritative message list; new code writes '' to the scalar column.
ALTER TABLE rolling_posts DROP CONSTRAINT IF EXISTS rolling_posts_discord_message_id_check;
-- Make the legacy scalar column optional. New code only writes to
-- discord_message_ids[]; existing deploys created the column as NOT NULL
-- with no default, which broke upserts from the new code and caused
-- double-posts on 2026-04-17 (the upsert errored, so InsertNotification
-- never ran, so the same reddit post was re-processed and re-sent).
ALTER TABLE rolling_posts ALTER COLUMN discord_message_id DROP NOT NULL;
ALTER TABLE rolling_posts ALTER COLUMN discord_message_id SET DEFAULT '';
CREATE INDEX IF NOT EXISTS rolling_posts_day_idx ON rolling_posts(day_local);
-- Optimises the "latest active digest for (channel, mode)" lookup path.
DROP INDEX IF EXISTS rolling_posts_active_idx;
CREATE INDEX IF NOT EXISTS rolling_posts_active_by_mode_idx
  ON rolling_posts (channel_id, mode, window_start DESC);

-- Last.fm listener-count + tags cache. artist_key is the normalized artist
-- name (case-folded, single-spaced, trimmed). Stale rows (> 30 days) get
-- overwritten lazily on the next lookup.
CREATE TABLE IF NOT EXISTS lastfm_cache (
    artist_key TEXT        PRIMARY KEY,
    listeners  INT         NOT NULL DEFAULT 0,
    tags       TEXT[]      NOT NULL DEFAULT '{}',
    fetched_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE lastfm_cache ADD COLUMN IF NOT EXISTS tags TEXT[] NOT NULL DEFAULT '{}';

-- Piped (YouTube-via-Piped) search cache. query_key is "filter|artist title"
-- so album searches and song searches are cached independently. youtube_url
-- is the full music.youtube.com URL (watch OR playlist) we hand to Discord.
-- Empty youtube_url is a legitimate cached outcome — the search returned no
-- match, so we stop re-querying Piped for it.
CREATE TABLE IF NOT EXISTS piped_cache (
    query_key   TEXT        PRIMARY KEY,
    video_id    TEXT        NOT NULL DEFAULT '',   -- legacy (songs-only); kept for one release
    youtube_url TEXT        NOT NULL DEFAULT '',
    fetched_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE piped_cache ADD COLUMN IF NOT EXISTS youtube_url TEXT NOT NULL DEFAULT '';

-- Qobuz album-page search cache. query_key is the normalized `artist title`
-- string. qobuz_url is the full qobuz.com/us-en/album URL; empty string is
-- a cacheable "no matching album" outcome.
CREATE TABLE IF NOT EXISTS qobuz_cache (
    query_key  TEXT        PRIMARY KEY,
    qobuz_url  TEXT        NOT NULL DEFAULT '',
    fetched_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
