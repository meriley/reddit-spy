# reddit-spy

A Discord bot that monitors Reddit subreddits for matching posts and delivers
rolling daily digests to Discord channels.

## What it does

reddit-spy polls one or more subreddits every 30 seconds using Reddit's
unauthenticated JSON API. When a post matches a configured rule, the bot sends
(or edits) a Discord embed containing a summary of that post. All matches from
the same subreddit on the same calendar day (Phoenix local time) roll into a
single Discord message that is edited in place rather than producing a new
message for each match.

Two digest modes are available:

- **narrative** — post body rendered as prose, optionally shaped by an LLM
- **music** — LLM extracts a structured list of `{artist, title, kind}` entries
  from weekly-release threads; entries are enriched with listener counts
  (Last.fm), YouTube links (Piped), and Qobuz links before display

The LLM integration, Last.fm, Piped, and Qobuz are all optional. The bot
operates without them; music mode requires the LLM to be configured.

## Requirements

- Go 1.22+
- PostgreSQL 14+ (a single database)
- A Discord application with the `SEND_MESSAGES` and `applications.commands`
  bot permissions in the target guild

## Deployment

reddit-spy is deployed to the k3d-ai cluster via ArgoCD. The Helm chart and
ArgoCD Application manifest live in the
[k3d-deployments](https://gitea.cmtriley.com/mriley/k3d-deployments) repo.
See `docs/migration-to-ai-cluster.md` in this repo for the cutover runbook.

## Local development

Copy `config/.env.example` to `config/.env` (or create one — see
`docs/configuration.md` for all variables), then:

```bash
go run ./main.go
```

The bot runs its full startup sequence: schema bootstrap, Discord connection,
and subreddit pollers in goroutines.

## Documentation

| Document                          | Type        | Contents                                               |
| --------------------------------- | ----------- | ------------------------------------------------------ |
| `docs/getting-started.md`         | Tutorial    | Install, configure, and receive your first digest      |
| `docs/configuration.md`           | Reference   | All environment variables and Discord slash commands   |
| `docs/architecture.md`            | Explanation | Rolling-digest design, LLM integration, music pipeline |
| `docs/migration-to-ai-cluster.md` | How-To      | Migrate data to postgres-ai and cut over to ArgoCD     |

## License

See `LICENSE`.
