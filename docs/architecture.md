# Architecture

This document explains the design decisions behind reddit-spy: the rolling
digest model, Phoenix-timezone bucketing, LLM integration, the music pipeline,
and how the system handles partial failures.

Source evidence: `internal/discord/discord.go`, `internal/discord/digest_music.go`,
`internal/discord/digest_music_enrich.go`, `internal/discord/digest_music_enrich_parallel.go`,
`internal/discord/digest_music_piped.go`, `internal/discord/digest_music_qobuz.go`,
`internal/evaluator/evaluate.go`, `internal/llm/shaper.go`,
`internal/llm/shaper_music.go`, `internal/dbstore/store.go`,
`internal/dbstore/bootstrap.go`, `internal/dbstore/sql/schema.sql`,
`internal/redditJSON/poller.go`, `redditDiscordBot/bot.go`.

---

## Data flow

```
Reddit JSON API (https://www.reddit.com/r/<sub>/.json)
    │  poll every 30 s, 5 s HTTP timeout
    ▼
redditDiscordBot.Bot  ──▶  evaluator.Evaluate()
    │  matches all rules for the post's subreddit
    │  fan-out capped at 4 concurrent DB inserts per post
    │  dedupes by (post_id, channel_id, rule_id) via notifications table
    ▼
discord.Client.SendMessage()
    │  reads active rolling_posts row for (channel, mode, window)
    │
    ├─ mode == "narrative" ──▶ LLM ShapeFresh / ShapeUpdate
    │                          (falls back to raw selftext on error or no LLM)
    │
    └─ mode == "music"    ──▶ LLM ShapeMusic  ──▶ enrichment
                               (skips match if no LLM configured)
    │
    ▼
Discord send (new embed) or edit (existing embed)
    │
    ▼
upsert rolling_posts + insert notifications
```

---

## Rolling digest

### Why one message per (channel, mode, day) rather than one per post

Each Discord channel is a stream that a single reader scans once. Posting a
separate embed for every matching post on a busy subreddit would fill the
channel with nearly-identical cards, most differing only in the new post. The
rolling model collapses all same-day matches into one message that is edited in
place as new matches arrive.

The grouping key is `(channel_id, mode, window_start + window_hours)`. All
rules with the same mode in a given channel share one digest regardless of
which subreddit they target. This means a channel with five music-mode rules
across different subreddits produces one growing music digest per window, not
five parallel ones.

### Edit-or-send path

`SendMessage` calls `GetActiveRollingPost` to find an open window row. If
none exists, it calls `ChannelMessageSendComplex` and stores the new message
ID. If one exists, it calls `ChannelMessageEditComplex` on the stored message
ID. A 404 from Discord (message manually deleted by a human) causes the bot to
fall back to sending a fresh message and overwriting the stored message ID.

The `notifications` table has a `UNIQUE (post_id, channel_id, rule_id)`
constraint. `SendMessage` checks this before any Discord call; a match that has
already produced a notification is silently skipped without touching Discord or
the LLM.

### Phoenix timezone

Day boundaries are computed in `America/Phoenix` (UTC-7, no DST). This
timezone was chosen because it never observes daylight saving time, eliminating
the hour-ambiguity problem at spring-forward and fall-back boundaries. A
`time.LoadLocation` failure at startup is fatal.

The `day_local` column in `rolling_posts` stores the Phoenix midnight as a UTC
timestamp (time part always zero). `GetActiveRollingPost` compares this value
against the current Phoenix day to decide whether the existing row is still
within the active window.

---

## LLM integration

### Shaper

The `llm.Shaper` struct wraps `github.com/sashabaranov/go-openai` with the
`BaseURL` field overridden to point at an OpenAI-compatible endpoint (vLLM in
production). The shaper is constructed only when both `LLM_BASE_URL` and
`LLM_MODEL` are set; the `discord.Client` holds a nil shaper otherwise.

Two narrative methods and one music method:

- `ShapeFresh` — first match: produce `{title, summary}` from the post body
- `ShapeUpdate` — later match: weave a new post into an existing narrative
- `ShapeMusic` — extract `[{artist, title, kind}]` from a release thread body

All three use `ResponseFormat: JSONObject` and strip `<think>...</think>`
blocks emitted by Qwen3 when the `/no_think` directive is ineffective.

### Narrative fallback

`freshNarrative` and `updateNarrative` catch every LLM error and fall back
gracefully:

- `freshNarrative` with no shaper or on error: uses the raw post title and
  truncated selftext (capped at 6000 runes for the prompt, 3800 chars for the
  digest body)
- `updateNarrative` on error: keeps the prior narrative unchanged

This means a narrative-mode match is never silently dropped. The match is
always written to `notifications` and `rolling_posts` regardless of LLM
availability.

### Music mode: no fallback

Music mode has no selftext fallback. When `c.shaper == nil`,
`handleMusicMatch` logs a warning and returns `nil` — the match is not written
to `notifications` or `rolling_posts`. This is intentional: a music digest
without structured entries has no value. Configure the LLM before using music
mode.

---

## Music pipeline

### Extraction

`ShapeMusic` sends the post body to the LLM with a system prompt that instructs
structured JSON output only. The response is normalized:

- Bare JSON arrays are accepted in addition to the `{"entries":[...]}` wrapper
- Truncated responses (token budget hit mid-stream) are recovered by finding
  the last complete `}` in the output and patching in `]}` or `]`
- Token budget is estimated dynamically: `nonEmptyLines × 25 × 1.5 + 100`,
  clamped between 512 and 6000 tokens

### Deduplication

Each entry is keyed by `artist|title|kind` (all lowercased, whitespace
trimmed, `(feat. ...)` stripped from the title for comparison only). Entries
whose key already appears in `KnownEntries` (the existing digest) are dropped
before the new entries are returned. The `notifications` table UNIQUE
constraint provides a second deduplication layer at the post level.

### Enrichment

After extraction, new entries are enriched in parallel via three independent
goroutines (source: `digest_music_enrich_parallel.go`):

| Enricher        | Data added                            | Cache TTL | Concurrency per call | Total budget |
| --------------- | ------------------------------------- | --------- | -------------------- | ------------ |
| Last.fm scraper | `listeners` (monthly), `tags` (genre) | 30 days   | 2                    | 45 s         |
| Piped client    | `youtube_url` (music.youtube.com)     | 30 days   | 2                    | 60 s         |
| Qobuz scraper   | `qobuz_url` (qobuz.com/us-en/album)   | 30 days   | 2                    | 60 s         |

All three enrichers are cache-first (tables `lastfm_cache`, `piped_cache`,
`qobuz_cache`). Cache misses write back on success. All three fail soft: an
enricher error leaves the affected fields empty; the digest is rendered with
whatever data was obtained.

Last.fm runs unconditionally (no configuration required). Piped runs only when
`PIPED_BASE_URL` is set. Qobuz runs unless `QOBUZ_DISABLED` is set to a
non-empty value.

### Rendering

The music digest renders as two Discord artifacts:

1. **Parent card** — posted to the channel. Shows a compact at-a-glance view
   with up to 5 entries per bucket (genre/kind), sorted by Last.fm listener
   count descending (entries with zero listeners sort last). Embed description
   capped at 3900 runes.
2. **Thread embeds** — posted as replies inside a thread attached to the card.
   Show the full per-release list with artist, linked title (YouTube), optional
   Qobuz link, and genre tags. Thread auto-archives after 7 days.

On same-window updates, the parent card is edited in place and the thread
messages are updated by index.

---

## Database schema

Ten tables (all created idempotently on startup):

| Table              | Purpose                                                                    |
| ------------------ | -------------------------------------------------------------------------- |
| `discord_servers`  | Guild identity                                                             |
| `discord_channels` | Channel identity + external ID                                             |
| `subreddits`       | Subreddit identity + external ID                                           |
| `rules`            | Match rules: target field, value, exact flag, mode, window_hours           |
| `posts`            | Seen post IDs (external Reddit ID → internal integer)                      |
| `notifications`    | UNIQUE (post_id, channel_id, rule_id) — primary dedupe guard               |
| `rolling_posts`    | One row per active window: message IDs, narrative, music entries, metadata |
| `lastfm_cache`     | Artist → listeners + tags, 30-day TTL                                      |
| `piped_cache`      | Query → YouTube URL, 30-day TTL                                            |
| `qobuz_cache`      | Artist + title → Qobuz URL, 30-day TTL                                     |

The `rules` table defaults: `mode = 'narrative'`, `window_hours = 72`.

---

## Graceful degradation summary

| Component              | Failure behavior                                               |
| ---------------------- | -------------------------------------------------------------- |
| LLM (narrative mode)   | Falls back to raw selftext; match is still stored              |
| LLM (music mode)       | Match is silently skipped and logged at WARN; no DB write      |
| Last.fm enricher       | Entry rendered without listener count or genre tags            |
| Piped enricher         | Entry rendered without YouTube link                            |
| Qobuz enricher         | Entry rendered without Qobuz link                              |
| Discord 404 on edit    | Falls back to sending a new message; updates stored message ID |
| Reddit poll HTTP error | Poll cycle skipped; next tick retries                          |

The only hard failure path is the database: a DB error during `InsertPost` or
`UpsertRollingPost` propagates as an error and the match is not acknowledged,
allowing a retry on the next poll.
