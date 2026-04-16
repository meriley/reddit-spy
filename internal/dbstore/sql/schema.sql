-- App-scope schema bootstrap for reddit-spy.
--
-- Runs on every pod startup against the application database with the app
-- user's credentials. All statements are idempotent (IF NOT EXISTS) so this
-- is safe to re-run after every deploy.

CREATE TABLE IF NOT EXISTS discord_servers (
    id        SERIAL PRIMARY KEY,
    server_id TEXT NOT NULL UNIQUE
);

CREATE TABLE IF NOT EXISTS discord_channels (
    id         SERIAL PRIMARY KEY,
    channel_id TEXT NOT NULL UNIQUE,
    server_id  INT  NOT NULL REFERENCES discord_servers(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS subreddits (
    id           SERIAL PRIMARY KEY,
    subreddit_id TEXT NOT NULL UNIQUE
);

CREATE TABLE IF NOT EXISTS rules (
    id           SERIAL PRIMARY KEY,
    target       TEXT NOT NULL,
    target_id    TEXT NOT NULL,
    exact        BOOLEAN NOT NULL DEFAULT FALSE,
    channel_id   INT  NOT NULL REFERENCES discord_channels(id) ON DELETE CASCADE,
    subreddit_id INT  NOT NULL REFERENCES subreddits(id)       ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS rules_subreddit_id_idx ON rules(subreddit_id);
CREATE INDEX IF NOT EXISTS rules_channel_id_idx   ON rules(channel_id);

CREATE TABLE IF NOT EXISTS posts (
    id      SERIAL PRIMARY KEY,
    post_id TEXT NOT NULL UNIQUE
);

CREATE TABLE IF NOT EXISTS notifications (
    id         SERIAL PRIMARY KEY,
    post_id    INT NOT NULL REFERENCES posts(id)            ON DELETE CASCADE,
    channel_id INT NOT NULL REFERENCES discord_channels(id) ON DELETE CASCADE,
    rule_id    INT NOT NULL REFERENCES rules(id)            ON DELETE CASCADE,
    UNIQUE (post_id, channel_id, rule_id)
);

-- Rolling daily digest — one row per (channel, subreddit, day_local). The
-- day is computed in America/Phoenix at match-handling time; all matches from
-- the same subreddit on the same Phoenix day update the same Discord message.
CREATE TABLE IF NOT EXISTS rolling_posts (
    id                 SERIAL PRIMARY KEY,
    channel_id         INT  NOT NULL REFERENCES discord_channels(id) ON DELETE CASCADE,
    subreddit_id       INT  NOT NULL REFERENCES subreddits(id)       ON DELETE CASCADE,
    day_local          DATE NOT NULL,
    discord_message_id TEXT NOT NULL,
    narrative_title    TEXT NOT NULL,
    narrative_summary  TEXT NOT NULL,
    included_post_ids  TEXT[] NOT NULL DEFAULT '{}',
    included_rule_ids  INT[]  NOT NULL DEFAULT '{}',
    latest_score       INT  NOT NULL DEFAULT 0,
    latest_comments    INT  NOT NULL DEFAULT 0,
    latest_url         TEXT NOT NULL DEFAULT '',
    latest_thumbnail   TEXT NOT NULL DEFAULT '',
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (channel_id, subreddit_id, day_local)
);
CREATE INDEX IF NOT EXISTS rolling_posts_day_idx ON rolling_posts(day_local);
