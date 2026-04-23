# Getting Started

This tutorial takes you from a fresh checkout to receiving your first Discord
digest. By the end you will have:

- A running local reddit-spy process connected to a PostgreSQL database
- A Discord channel with one active rule monitoring a subreddit
- One digest embed delivered to that channel

Estimated time: 20–30 minutes.

---

## Prerequisites

- Go 1.22 or later (`go version`)
- PostgreSQL 14 or later running locally (the bot will create its schema
  automatically)
- A Discord application with a bot token and the bot invited to a server with
  `Send Messages` and `Use Application Commands` permissions
- The subreddit you want to monitor must exist (the bot validates this on rule
  creation)

---

## Step 1: Create the database user and database

Connect to your PostgreSQL instance as a superuser and run:

```sql
CREATE ROLE reddit_spy LOGIN PASSWORD 'choose_a_password';
CREATE DATABASE reddit_spy OWNER reddit_spy;
```

The bot applies the schema (10 tables) on startup using `IF NOT EXISTS`
statements, so no manual schema file is needed.

Alternatively, set `POSTGRES_ADMIN_URL` to a superuser DSN (see
`docs/configuration.md`) and the bot will create the role and database itself.

---

## Step 2: Create `config/.env`

Create the directory and file:

```bash
mkdir -p config
```

Minimum required content:

```
DISCORD_TOKEN=your_discord_bot_token_here
POSTGRES_ADDRESS=localhost:5432
POSTGRES_USER=reddit_spy
POSTGRES_PASSWORD=choose_a_password
POSTGRES_DATABASE=reddit_spy
```

`POSTGRES_ADDRESS` is `host:port`, not a full DSN. The bot constructs its
connection string from the four `POSTGRES_*` variables.

Save the file. All other variables have defaults and can be added later.

---

## Step 3: Run the bot

```bash
go run ./main.go
```

On first startup you should see log lines similar to:

```
level=info msg="schema bootstrap complete"
level=info msg="discord session opened"
level=info msg="commands registered" count=8
```

If `POSTGRES_ADDRESS` is unreachable or credentials are wrong, the bot exits
immediately with a descriptive error.

Leave the process running. Open a new terminal for the next steps.

---

## Step 4: Add a rule in Discord

In any channel where the bot is present, type `/add_subreddit_listener`.

Fill in the options:

| Option      | Example value   | Notes                     |
| ----------- | --------------- | ------------------------- |
| `subreddit` | `programming`   | Without the `r/` prefix   |
| `match_on`  | `title`         | Match against post titles |
| `value`     | `rust`          | The string to look for    |
| `exact`     | `false`         | Substring match           |
| `mode`      | _(leave blank)_ | Defaults to `narrative`   |

The bot will validate that `r/programming` exists (live HTTP request). On
success it replies with the new rule ID.

Requires the **Manage Channels** permission in that channel.

---

## Step 5: Wait for a match

reddit-spy polls every 30 seconds. When a post on `r/programming` has "rust"
in its title, the bot posts an embed to the channel.

The embed contains:

- A title (the post title, or LLM-generated if `LLM_BASE_URL` is configured)
- A summary of the post body
- Footer showing match count, rule ID, and the digest date (Phoenix local time)

If more posts match on the same calendar day (America/Phoenix), the embed is
edited in place rather than creating a new message. Run `/list_rules` to see
your active rules at any time.

---

## Step 6: Verify with `/preview_digest`

You can test the pipeline against any Reddit post without waiting for a poll
cycle or modifying the database:

```
/preview_digest url:https://www.reddit.com/r/programming/comments/xyz123/post_title/
```

The bot fetches the post, runs it through the shaper (if LLM is configured),
builds the embed, and replies ephemerally. Nothing is written to the database.

---

## Next steps

### Add LLM-shaped summaries

Add to `config/.env`:

```
LLM_BASE_URL=http://your-vllm-host:8000/v1
LLM_MODEL=Qwen/Qwen3-14B-AWQ
```

Restart the bot. Narrative digests will now be shaped as prose rather than
truncated selftext. See `docs/configuration.md` for tone and timeout options.

### Monitor music release threads

Change `mode` to `music` when adding a rule on a weekly-release subreddit
(e.g. `r/listentothis`). Music mode requires the LLM to be configured; without
it, matching posts are logged at WARN and skipped.

To add YouTube links, set `PIPED_BASE_URL` to a Piped instance. Qobuz links
are looked up automatically unless `QOBUZ_DISABLED` is set.

### Production deployment

See `docs/migration-to-ai-cluster.md` for the runbook that moves data to
`postgres-ai` and cuts over to the ArgoCD-managed Deployment in the k3d-ai
cluster.
