# Configuration Reference

This document describes every environment variable and Discord slash command
that reddit-spy exposes. Variables are loaded from `config/.env` at startup
via `godotenv`; any variable already set in the process environment takes
precedence.

Source evidence: `internal/context/context.go`, `internal/dbstore/store.go`,
`internal/dbstore/bootstrap.go`, `internal/discord/discord.go`,
`internal/llm/client.go`, `main.go`.

---

## Environment variables

### Database

| Variable             | Required | Default | Description                                                                                                                                                                                                         |
| -------------------- | -------- | ------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `POSTGRES_ADDRESS`   | Yes      | —       | `host:port` of the PostgreSQL server (e.g. `localhost:5432`). Not a full DSN.                                                                                                                                       |
| `POSTGRES_USER`      | Yes      | —       | PostgreSQL role name.                                                                                                                                                                                               |
| `POSTGRES_PASSWORD`  | Yes      | —       | Password for `POSTGRES_USER`.                                                                                                                                                                                       |
| `POSTGRES_DATABASE`  | Yes      | —       | Database name (e.g. `reddit_spy`).                                                                                                                                                                                  |
| `POSTGRES_ADMIN_URL` | No       | —       | Full DSN with superuser credentials (e.g. `postgres://postgres:pw@host:5432/postgres`). When set, the bot creates the role and database on startup, then applies the schema. Safe to omit once the database exists. |

The bot constructs its connection string as
`postgres://$POSTGRES_USER:$POSTGRES_PASSWORD@$POSTGRES_ADDRESS/$POSTGRES_DATABASE`.
The default query timeout per statement is 5 seconds.

### Discord

| Variable        | Required | Default | Description        |
| --------------- | -------- | ------- | ------------------ |
| `DISCORD_TOKEN` | Yes      | —       | Discord bot token. |

### Digest behavior

| Variable                      | Required | Default | Description                                                                                                                                                                            |
| ----------------------------- | -------- | ------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `DIGEST_DEFAULT_WINDOW_HOURS` | No       | `72`    | How many hours a rolling digest window stays open before a new match opens a fresh window. Per-rule `combine_hits_hours` overrides this. Fallback chain: rule value → this value → 72. |

### LLM (optional)

All four LLM variables are optional. When `LLM_BASE_URL` or `LLM_MODEL` is
unset, the LLM shaper is disabled. Narrative-mode digests fall back to raw
truncated selftext. Music-mode matches are silently skipped (logged at WARN).

| Variable       | Required | Default   | Description                                                                                                                                                |
| -------------- | -------- | --------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `LLM_BASE_URL` | No       | —         | Base URL of an OpenAI-compatible API, e.g. `http://vllm.ai.svc.cluster.local:8000/v1`.                                                                     |
| `LLM_MODEL`    | No       | —         | Model identifier, e.g. `Qwen/Qwen3-14B-AWQ`.                                                                                                               |
| `LLM_API_KEY`  | No       | `EMPTY`   | API key. Defaults to the literal string `EMPTY`, which is the vLLM convention for keyless access.                                                          |
| `LLM_TIMEOUT`  | No       | `30s`     | Per-call timeout as a Go duration string (e.g. `45s`, `2m`).                                                                                               |
| `LLM_TONE`     | No       | `neutral` | Narrative voice. Accepted values: `snarky` (dry, not mean-spirited), `playful` (warm, emoji allowed). Any other value produces neutral, informative prose. |

### Enrichment (optional)

| Variable         | Required | Default | Description                                                                                                                        |
| ---------------- | -------- | ------- | ---------------------------------------------------------------------------------------------------------------------------------- |
| `PIPED_BASE_URL` | No       | —       | Base URL of a Piped instance (e.g. `http://piped.ai.svc.cluster.local`). When unset, YouTube links are not added to music entries. |
| `QOBUZ_DISABLED` | No       | —       | Set to any non-empty value to disable Qobuz link lookup. When unset, Qobuz scraping is active.                                     |

Last.fm enrichment runs unconditionally when music mode is active; it uses
Last.fm's public web pages (no API key required).

### Logging

| Variable    | Required | Default | Description                                                                                                               |
| ----------- | -------- | ------- | ------------------------------------------------------------------------------------------------------------------------- |
| `LOG_LEVEL` | No       | `info`  | Minimum log level. Accepted values: `debug`, `info`, `warn` (or `warning`), `error`, `none` (or `off`). Case-insensitive. |

---

## Discord slash commands

### Rule management

#### `/add_subreddit_listener`

Creates a new rule in the current channel. Requires **Manage Channels**
permission.

| Option               | Type    | Required | Constraints                              | Description                                                                                             |
| -------------------- | ------- | -------- | ---------------------------------------- | ------------------------------------------------------------------------------------------------------- |
| `subreddit`          | string  | Yes      | 1–21 chars, `[a-zA-Z0-9_]+`              | Subreddit name without the `r/` prefix. The bot validates the subreddit exists via a live HTTP request. |
| `match_on`           | string  | Yes      | `author` or `title`                      | Which post field to match against.                                                                      |
| `value`              | string  | Yes      | —                                        | The value to match.                                                                                     |
| `exact`              | boolean | Yes      | —                                        | `true` for case-insensitive equality; `false` for case-insensitive substring match.                     |
| `mode`               | string  | No       | `narrative`, `music`, `summary`, `media` | Digest mode. Defaults to `narrative`.                                                                   |
| `combine_hits_hours` | integer | No       | 1–720                                    | Override the rolling window duration for this rule. See `DIGEST_DEFAULT_WINDOW_HOURS`.                  |

#### `/list_rules`

Lists all rules for the current channel. No permission requirement. Shows up
to 25 rules with inline Delete buttons.

#### `/delete_rule`

Deletes a rule by ID. Requires **Manage Channels** permission. The rule must
belong to the current server.

| Option    | Type    | Required | Description            |
| --------- | ------- | -------- | ---------------------- |
| `rule_id` | integer | Yes      | ID from `/list_rules`. |

#### `/edit_rule`

Edits one or more fields of an existing rule. Requires **Manage Channels**
permission. Omitting an option leaves that field unchanged.

| Option               | Type    | Required | Description                   |
| -------------------- | ------- | -------- | ----------------------------- |
| `rule_id`            | integer | Yes      | ID from `/list_rules`.        |
| `value`              | string  | No       | New match target string.      |
| `exact`              | boolean | No       | New exact-match flag.         |
| `digest_mode`        | string  | No       | New digest mode.              |
| `combine_hits_hours` | integer | No       | New window duration in hours. |

### Diagnostic commands

#### `/preview_digest`

Dry-runs the full digest pipeline (LLM shaping + enrichment) against a real
Reddit post and responds ephemerally. Nothing is written to the database and no
message is sent to the channel. Requires no special permissions.

Music mode preview requires the LLM to be configured; it returns an error if
the shaper is absent.

| Option   | Type    | Required | Description                                                                                                                                           |
| -------- | ------- | -------- | ----------------------------------------------------------------------------------------------------------------------------------------------------- |
| `url`    | string  | Yes      | Reddit post URL (`https://www.reddit.com/r/.../comments/xyz123/...`), short URL (`https://redd.it/xyz123`), or bare post ID (5–10 base36 characters). |
| `public` | boolean | No       | Post the preview visibly to the channel instead of ephemerally. Default: ephemeral.                                                                   |

#### `/ping`

Returns Discord gateway heartbeat latency. No permission requirement.

#### `/status`

Returns bot uptime, active poller count, and gateway latency. No permission
requirement.

#### `/help`

Returns an embed listing all registered commands. No permission requirement.

---

## Digest modes

| Mode      | Value       | LLM required                           | Description                                                      |
| --------- | ----------- | -------------------------------------- | ---------------------------------------------------------------- |
| Narrative | `narrative` | No (falls back to raw selftext)        | Prose summary of each matched post, updated as more posts match. |
| Music     | `music`     | Yes (match silently skipped if absent) | Structured release list extracted from weekly-release threads.   |
| Summary   | `summary`   | —                                      | Reserved.                                                        |
| Media     | `media`     | —                                      | Reserved.                                                        |

---

## Rolling window behavior

The rolling window groups matches by `(channel, mode, window_start + window_hours)`.
This means all music-mode rules in a channel share one music digest, and all
narrative-mode rules share one narrative digest, regardless of which subreddit
each rule monitors.

Window precedence for a given rule match:

1. `combine_hits_hours` on the matched rule (if set)
2. `DIGEST_DEFAULT_WINDOW_HOURS` (if set)
3. Hard fallback: 72 hours

Source: `internal/discord/discord.go` (`effectiveWindowHours`),
`internal/dbstore/mode.go`.
