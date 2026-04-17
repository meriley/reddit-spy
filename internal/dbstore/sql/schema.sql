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
    created_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
ALTER TABLE rules ADD COLUMN IF NOT EXISTS mode TEXT NOT NULL DEFAULT 'narrative';
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

-- Rolling daily digest — one row per (channel, subreddit, day_local). The
-- day is computed in America/Phoenix at match-handling time; all matches from
-- the same subreddit on the same Phoenix day update the same Discord message.
CREATE TABLE IF NOT EXISTS rolling_posts (
    id                  SERIAL PRIMARY KEY,
    channel_id          INT  NOT NULL REFERENCES discord_channels(id) ON DELETE CASCADE,
    subreddit_id        INT  NOT NULL REFERENCES subreddits(id)       ON DELETE CASCADE,
    day_local           DATE NOT NULL,
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
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (channel_id, subreddit_id, day_local)
);
-- Columns added after v2.1 — idempotent for existing deploys.
ALTER TABLE rolling_posts ADD COLUMN IF NOT EXISTS mode                TEXT   NOT NULL DEFAULT 'narrative';
ALTER TABLE rolling_posts ADD COLUMN IF NOT EXISTS discord_message_ids TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE rolling_posts ADD COLUMN IF NOT EXISTS entries             JSONB  NOT NULL DEFAULT '[]';
-- Backfill the new array from the legacy scalar if the array is empty.
UPDATE rolling_posts
SET    discord_message_ids = ARRAY[discord_message_id]
WHERE  discord_message_id <> ''
  AND  array_length(discord_message_ids, 1) IS NULL;
-- Drop the legacy 'discord_message_id <> ''' CHECK — the array is now the
-- authoritative message list; new code writes '' to the scalar column.
ALTER TABLE rolling_posts DROP CONSTRAINT IF EXISTS rolling_posts_discord_message_id_check;
CREATE INDEX IF NOT EXISTS rolling_posts_day_idx ON rolling_posts(day_local);

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
