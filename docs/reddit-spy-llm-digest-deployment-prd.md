# Reddit-Spy LLM Digests + k3d-AI Deployment + Gitea Migration — PRD

## Document Info

| Field              | Value                                                       |
| ------------------ | ----------------------------------------------------------- |
| **Author**         | Pedro                                                       |
| **Date**           | 2026-04-16                                                  |
| **Version**        | 1.0                                                         |
| **Status**         | Draft                                                       |
| **Stakeholders**   | Pedro (sole owner — product + engineering + ops)            |
| **Target Release** | v2.1.0                                                      |
| **Related**        | Plan: `~/.claude/plans/i-would-like-to-resilient-pnueli.md` |

---

## Executive Summary

Reddit-Spy posts one Discord embed per rule match, producing a noisy feed of
near-duplicate weekly-release threads and raw Reddit selftext. This PRD
bundles three changes to fix that: (1) have an in-cluster vLLM rewrite each
match into a narrative summary and roll all matches from the same subreddit
on a given Phoenix-local day into **one Discord message that is edited in
place**; (2) deploy reddit-spy to the `k3d-ai` cluster via a new Helm chart +
ArgoCD Application; (3) move the repo, CI, and container registry from GitHub
to self-hosted Gitea (`gitea.cmtriley.com`).

---

## Problem Statement

### The Problem

Reddit-Spy is not usable as a daily-consumption feed. Three specific pains:

1. **Noise.** Every rule match creates a new Discord embed. Chatty subs
   (e.g. r/Metalcore, r/poppunkers) produce multiple near-duplicate posts
   per day — the channel becomes scroll-fatigue, not signal.
2. **Low-value bodies.** The `Summary` field is a raw truncation of Reddit
   selftext (giant lists of singles, stripped markdown). It reads like a
   data dump, not a digest.
3. **Ops friction.** Reddit-Spy runs outside the k3d AI cluster where the
   LLM lives, and the repo sits on GitHub while every other piece of Pedro's
   infrastructure has moved to self-hosted Gitea.

**Key insight:** the feed's value is not "did a post match?" — it's "what
happened in my subreddits today?" That framing demands one-per-subreddit-
per-day narrative, not one-per-match raw dump.

### Who is Affected

| Persona                | Description                                        | Impact                                                                                |
| ---------------------- | -------------------------------------------------- | ------------------------------------------------------------------------------------- |
| Pedro (Discord reader) | Daily reader of his private Discord channel        | Scroll-fatigue; actual signal drowned in redundant embeds                             |
| Pedro (operator)       | Runs reddit-spy as a side project alongside k3d-ai | Manual deploys; secrets outside sealed-secrets flow; split infra (GitHub + self-host) |

### Current State

- Reddit-Spy runs somewhere outside the AI cluster (laptop/VPS) against an
  external Postgres.
- Repo + CI on GitHub; image pushed to Docker Hub (`merileyjr/reddit-spy`).
- No LLM integration anywhere in the codebase; `Summary` = raw selftext
  truncated to 1024 chars (`internal/discord/discord.go:109`).
- Every match = new Discord embed (`internal/discord/discord.go:100`).
- No Helm chart, no k8s manifests, no ArgoCD Application.

### Impact of Not Solving

| Impact Type | Description                                                      | Quantification                                |
| ----------- | ---------------------------------------------------------------- | --------------------------------------------- |
| User Impact | Feed is ignored during high-volume days; signal lost             | ≥3 redundant embeds/day from active subs      |
| Ops Impact  | Reddit-Spy skipped during infra moves; drift from cluster-native | Parallel deploy stack (Docker Hub vs Gitea)   |
| Tech Debt   | No schema bootstrap, no k8s deployment, GitHub remnant           | Manual psql + manual image pull on every move |

---

## Proposed Solution

### Overview

1. Add an `internal/llm` package that calls the in-cluster vLLM via the
   OpenAI-compatible API; have it produce `{title, summary}` for each match.
2. Add a `rolling_posts` table keyed by `(channel, subreddit, phoenix_day)`.
   On the first match of the day, send a Discord embed and remember its
   message ID. On subsequent matches, re-prompt the LLM with the prior
   narrative + the new post and **edit the original Discord message**.
3. Author a Helm chart at `charts/reddit-spy` in `k3d-deployments`,
   mirroring `charts/vllm` and `charts/conversation-intelligence` (direct
   chart; SealedSecrets; no Service/Ingress; single replica with `Recreate`).
4. Add an ArgoCD Application at
   `clusters/k3d-ai/applications/reddit-spy.yaml` so syncs flow through the
   existing root-app.
5. Push the repo to `https://gitea.cmtriley.com/mriley/reddit-spy.git`; port
   GitHub Actions → Gitea Actions; switch the Makefile/Dockerfile image ref
   to `gitea.cmtriley.com/mriley/reddit-spy`.

Full technical detail lives in the plan file — this PRD captures the "what
and why" contract.

### User Stories

#### US-1: Rolling daily digest per subreddit

**As** Pedro (the Discord reader)
**I want** all matches from one subreddit on a given Phoenix day to roll into
a single Discord message that updates throughout the day
**So that** my channel shows one evolving story per sub per day, not a stack
of redundant embeds.

**Acceptance Criteria:**

```gherkin
Scenario: First match of the day creates a new message
  Given no rolling_posts row exists for (channel, subreddit, today_phoenix)
  When a Reddit post matches a rule for that channel+subreddit
  Then reddit-spy sends ONE new Discord embed
  And stores the Discord message_id in rolling_posts
  And the footer reads "1 matches • rules: #<id> • <today_phoenix>"

Scenario: Later match same day edits the existing message
  Given a rolling_posts row exists for (channel, subreddit, today_phoenix) with message_id M
  When a second Reddit post matches for the same channel+subreddit
  Then reddit-spy calls ChannelMessageEditComplex on message M
  And the row's included_post_ids grows to [post1, post2]
  And the footer reads "2 matches • rules: #<ids> • <today_phoenix>"
  And NO new Discord message is created

Scenario: Phoenix-midnight rollover starts a new message
  Given a rolling_posts row exists for yesterday_phoenix
  When a match arrives after 00:00 America/Phoenix
  Then reddit-spy sends a NEW Discord embed for today_phoenix
  And the old message is not edited

Scenario: Original message was deleted by a human
  Given rolling_posts has message_id M
  And the Discord message M was manually deleted
  When a new match arrives
  Then the edit returns 404
  And reddit-spy falls back to sending a new message
  And overwrites rolling_posts.discord_message_id

Scenario: Per-post dedupe still blocks double-processing
  Given notifications already has (post_id, channel_id, rule_id)
  When that same Reddit post is seen again
  Then reddit-spy does not edit or send
  And does not call the LLM
```

**Priority:** Must Have
**Estimated Size:** L

---

#### US-2: LLM-shaped narrative body

**As** Pedro (the Discord reader)
**I want** the embed summary to be an LLM-written narrative rather than raw
selftext
**So that** I can skim the digest instead of reading dumped lists.

**Acceptance Criteria:**

```gherkin
Scenario: Fresh summary
  Given a Fresh match
  When reddit-spy calls llm.Shape(Fresh, post, rule)
  Then it sends the fresh-mode system+user prompt to vLLM
  And writes the returned narrative (≤3800 chars) into the Summary field
  And uses the returned title as the embed title

Scenario: Update summary weaves the new post into the prior narrative
  Given a rolling_posts row with narrative_summary S1
  When llm.Shape(Update, prior=S1, newPost=P) is called
  Then the user prompt contains both S1 and P
  And the returned narrative mentions the new post
  And the returned narrative is bounded at 3800 chars

Scenario: vLLM is unreachable
  Given the LLM call errors or times out after LLM_TIMEOUT
  When reddit-spy handles the match
  Then it logs the error at WARN
  And falls back to raw selftext (current behaviour) so the match is not dropped
  And still writes rolling_posts + notifications rows
```

**Priority:** Must Have
**Estimated Size:** M

---

#### US-3: Deploy to the k3d-AI cluster via ArgoCD

**As** Pedro (the operator)
**I want** reddit-spy to run in the k3d-ai cluster under the same GitOps
conventions as conversation-intelligence / vllm / postgres-ai
**So that** deploys flow through ArgoCD and I stop managing a detached
instance.

**Acceptance Criteria:**

```gherkin
Scenario: Helm chart lints and renders
  When `helm lint charts/reddit-spy` runs in k3d-deployments
  Then exit code is 0
  And `helm template charts/reddit-spy | kubectl apply --dry-run=server -f -`
      succeeds against the k3d-ai API server

Scenario: ArgoCD picks up the new Application
  Given clusters/k3d-ai/applications/reddit-spy.yaml is committed
  When the root-app reconciles
  Then an Application named "reddit-spy" appears in ArgoCD
  And it syncs to namespace "ai"
  And the pod reaches Running within 5 minutes

Scenario: Pod connects to in-cluster dependencies
  Given the pod is Running
  When its logs are read
  Then they show a successful Postgres connection to postgres-ai.ai.svc.cluster.local:5432
  And a successful first LLM call against vllm.ai.svc.cluster.local:8000
  And the Reddit poller is emitting batches on its interval

Scenario: Secrets flow through SealedSecrets, not plaintext
  When the chart is inspected
  Then DISCORD_TOKEN, POSTGRES_PASSWORD, and LLM_API_KEY come from a Secret
  And the Secret is produced by a SealedSecret template
  And no plaintext credential is committed to either repo

Scenario: Schema auto-bootstraps on first run
  Given a fresh empty reddit_spy Postgres database
  When the pod starts
  Then it runs scripts/schema.sql idempotently (IF NOT EXISTS)
  And the poller starts without manual psql intervention
```

**Priority:** Must Have
**Estimated Size:** L

---

#### US-4: Move repo + CI + image registry to Gitea

**As** Pedro (the operator)
**I want** reddit-spy's source, CI, and container image to live on
`gitea.cmtriley.com`
**So that** my whole stack is self-hosted and the GitHub dependency is
retired.

**Acceptance Criteria:**

```gherkin
Scenario: Repo on Gitea
  Given the Gitea repo https://gitea.cmtriley.com/mriley/reddit-spy.git exists
  When `git push gitea master` is run
  Then master and all tags are mirrored to Gitea

Scenario: Gitea Actions builds and pushes the image
  Given .gitea/workflows/build.yaml exists
  When a push to master triggers the workflow
  Then it logs in using secrets GITEAUSERNAME + GITEATOKEN
  And builds the image as gitea.cmtriley.com/mriley/reddit-spy
  And tags it with both `latest` and `master-<sha>`
  And the workflow exits 0

Scenario: Gitea Actions runs lint + test + vuln-scan
  Given .gitea/workflows/ci.yml exists
  When a PR is opened
  Then golangci-lint, go test -race, and govulncheck all run and pass

Scenario: GitHub workflows are removed
  When the master branch is inspected
  Then .github/workflows/ci.yml is deleted
  And makefile and Dockerfile reference gitea.cmtriley.com/mriley/reddit-spy only

Scenario: Chart consumes the Gitea image
  Given the ArgoCD Application is synced
  When the pod's image is inspected
  Then it is `gitea.cmtriley.com/mriley/reddit-spy:<tag>`
  And `imagePullSecrets: [gitea-registry]` is set
```

**Priority:** Must Have
**Estimated Size:** M

---

#### US-5: Data migration from the current Postgres to postgres-ai

**As** Pedro (the operator)
**I want** existing rules, seen-posts, notifications, and channel mappings
preserved in the new `reddit_spy` database on `postgres-ai`
**So that** cutover is silent — no duplicate digests, no lost rules.

**Acceptance Criteria:**

```gherkin
Scenario: Schema is applied to reddit_spy on postgres-ai
  Given scripts/schema.sql exists
  When it is run against postgres-ai.ai db=reddit_spy
  Then all tables (discord_servers, discord_channels, subreddits, rules,
       posts, notifications, rolling_posts) exist with the expected unique
       constraints

Scenario: Data is restored
  Given a data-only pg_dump of the old database
  When it is `psql`'d into postgres-ai db=reddit_spy
  Then row counts match the source for each table

Scenario: Cutover produces no duplicate Discord post
  Given the old instance is stopped before the new pod starts
  And the `notifications` table was preserved
  When the new pod runs its first poll tick
  Then no previously-notified post produces a new Discord message
```

**Priority:** Must Have
**Estimated Size:** S

---

### User Journey

```
Reddit poster publishes thread
          │
          ▼
reddit-spy poller (30s tick)  ──▶  rule match
          │
          ▼
dedupe on (post, channel, rule)  ──▶  skip if seen
          │
          ▼
compute phoenix_day  ──▶  lookup rolling_posts
          │
   ┌──────┴──────┐
   ▼             ▼
  MISS          HIT
   │             │
llm.Shape     llm.Shape
 (Fresh)       (Update, prior)
   │             │
  SEND         EDIT
  embed        embed
   │             │
   └─────┬───────┘
         ▼
upsert rolling_posts + insert notifications
         │
         ▼
Pedro reads one coherent daily digest per sub
```

---

## Scope

### In Scope

| Item                                                   | Description                                                       | Priority    |
| ------------------------------------------------------ | ----------------------------------------------------------------- | ----------- |
| `internal/llm` package + OpenAI-compatible vLLM client | Fresh + Update prompts; fallback to raw on error                  | Must Have   |
| `rolling_posts` table + schema bootstrap               | Adds rolling-digest grouping keyed by (channel, sub, phoenix_day) | Must Have   |
| Rewrite of `SendMessage` for Fresh/Edit paths          | Keeps existing notifications dedupe; adds Discord edit path       | Must Have   |
| Phoenix-local day computation                          | `time.LoadLocation("America/Phoenix")` for rollover               | Must Have   |
| Helm chart at `charts/reddit-spy` in k3d-deployments   | Deployment + ConfigMap + SealedSecret; no Service                 | Must Have   |
| ArgoCD Application at `clusters/k3d-ai/applications/`  | Matches conversation-intelligence.yaml shape                      | Must Have   |
| Gitea repo + Gitea Actions CI + Gitea registry         | `.gitea/workflows/{ci.yml,build.yaml}`; Docker Hub removed        | Must Have   |
| Postgres data migration runbook + schema.sql           | `docs/migration-to-ai-cluster.md`; idempotent apply               | Must Have   |
| `LOG_LEVEL` env wired into go-kit/log                  | Replace hardcoded `AllowAll` in `internal/context/context.go:28`  | Should Have |

### Out of Scope

| Item                                               | Reason                                              | Future Consideration        |
| -------------------------------------------------- | --------------------------------------------------- | --------------------------- |
| Per-rule (not per-subreddit) grouping              | Pedro chose per-subreddit grouping                  | No                          |
| Multi-replica reddit-spy                           | Two replicas would post duplicate digests           | No (fundamentally unsafe)   |
| Ingress / externally reachable service             | reddit-spy is a client-only long-poller             | No                          |
| Upstream Reddit OAuth                              | Current public JSON endpoint is sufficient          | Yes if rate-limited         |
| Alternate LLM backends (Ollama, OpenAI, Anthropic) | vLLM already running in cluster; keep surface small | Yes — abstract client later |
| Overlay Helm chart pattern                         | Single-env deploy; direct chart is simpler          | Yes if a second env appears |
| Automatic GitHub repo deletion                     | Pedro archives manually once Gitea is green         | No — manual control desired |
| Summarization/translation of non-English posts     | Out of the digest-shaping MVP                       | Yes                         |

### Dependencies

| Dependency                                | Owner    | Status                                                | Risk |
| ----------------------------------------- | -------- | ----------------------------------------------------- | ---- |
| vLLM at `vllm.ai.svc.cluster.local:8000`  | Pedro    | Ready — already serving `Qwen/Qwen3-14B-AWQ`          | Low  |
| Postgres at `postgres-ai`                 | Pedro    | Ready — shared across AI apps                         | Low  |
| Gitea at `gitea.cmtriley.com`             | Pedro    | Ready — already hosting other repos + image registry  | Low  |
| Sealed-secrets controller in cluster      | Pedro    | Ready — `kubeseal` binary vendored in k3d-deployments | Low  |
| ArgoCD root-app                           | Pedro    | Ready — picks up new Application files automatically  | Low  |
| `github.com/sashabaranov/go-openai` (new) | Upstream | Stable; works with vLLM by overriding BaseURL         | Low  |
| `discordgo` edit permissions              | Discord  | Bot edits its own messages with SEND_MESSAGES only    | Low  |

### Assumptions

- vLLM's chat-completions endpoint stays at `/v1/chat/completions` and keeps
  accepting the OpenAI wire format.
- The Discord bot's identity stays constant (re-auth not required for edits).
- `postgres-ai` has capacity for one more small database.
- Pedro operates a single reddit-spy environment (no dev/staging split).
- Day rollover is Phoenix local time — no DST concern (AZ does not observe DST).

### Open Questions

- [ ] LLM tone knob: do we expose `LLM_TONE` (neutral / dry / snarky) as an
      env var in v1, or ship one opinionated voice? — Owner: Pedro
- [ ] Retention of `rolling_posts`: keep forever, or auto-prune rows older
      than N days? (Discord messages themselves persist; DB rows are tiny.)
      — Owner: Pedro
- [ ] Source Postgres location for the migration dump (laptop vs VPS)? —
      Owner: Pedro
- [ ] Do we keep GitHub as a passive mirror or archive it immediately after
      Gitea Actions goes green? — Owner: Pedro

---

## Success Metrics

### Key Results

| Metric                                              | Current State                       | Target                                         | Timeline           | Measurement Method                    |
| --------------------------------------------------- | ----------------------------------- | ---------------------------------------------- | ------------------ | ------------------------------------- |
| Discord messages per busy subreddit per day         | 3–5 (one per match)                 | 1 (rolling digest)                             | Day 1 post-deploy  | Manual spot-check of Discord channel  |
| Qualitative: "does the digest read as a narrative?" | No (raw selftext dump)              | Yes                                            | Day 1 post-deploy  | Pedro reads it                        |
| reddit-spy running inside k3d-ai                    | No — external instance              | Yes — ArgoCD-synced Deployment                 | Cutover day        | `kubectl -n ai get deploy reddit-spy` |
| Source repo on Gitea                                | GitHub only                         | Gitea primary; GitHub archived                 | Within 1 week      | `gh repo view` shows archived         |
| Container image on Gitea registry                   | Docker Hub (`merileyjr/reddit-spy`) | Gitea (`gitea.cmtriley.com/mriley/reddit-spy`) | First Gitea CI run | Running pod's image ref               |

### Leading Indicators (Early Signals)

| Indicator                                   | Target                        | Why It Matters                                |
| ------------------------------------------- | ----------------------------- | --------------------------------------------- |
| vLLM latency per Shape call                 | p95 < 8s                      | Longer = pod's per-match handling backs up    |
| LLM fallback rate                           | < 5% of matches               | Higher = vLLM flakiness swamps narrative gain |
| `rolling_posts` edit path taken / send path | > 50% edit on active sub days | Confirms grouping is doing its job            |
| Duplicate-message rate after cutover        | 0                             | Confirms `notifications` dedupe survived move |

### Guardrails (Don't Break These)

| Guardrail                     | Threshold                            | Action if Breached                                            |
| ----------------------------- | ------------------------------------ | ------------------------------------------------------------- |
| Match never silently dropped  | 100% of matches produce send or edit | LLM fallback; if that fails, log + retry                      |
| Embed description size        | ≤ 4096 chars (Discord hard limit)    | Shaper clips at 3800; embed-assembly truncates as belt+braces |
| No plaintext secret committed | 0 occurrences                        | Block merge; rotate credential                                |
| reddit-spy pod restart loop   | < 3 restarts per hour                | Rollback Application via ArgoCD                               |
| Postgres migration data-loss  | Row counts match source 1:1          | Hold cutover; re-dump                                         |

---

## Timeline & Milestones

| Milestone                               | Target Date | Description                                                                 | Success Criteria                                           |
| --------------------------------------- | ----------- | --------------------------------------------------------------------------- | ---------------------------------------------------------- |
| PRD approved                            | 2026-04-16  | This document                                                               | Pedro signs off                                            |
| `internal/llm` + shaper tests green     | 2026-04-18  | Fresh/Update prompts, OpenAI client, unit tests                             | `go test ./internal/llm -race` passes                      |
| `rolling_posts` + `SendMessage` rewrite | 2026-04-20  | DB layer + Discord edit path + dedupe preserved                             | E2E: fake session test shows send/edit/next-day sequence   |
| Schema bootstrap + migration runbook    | 2026-04-21  | `scripts/schema.sql`, idempotent startup, `docs/migration-to-ai-cluster.md` | Dry-run restore into a scratch DB succeeds                 |
| Gitea repo + Gitea Actions green        | 2026-04-22  | Push to Gitea, build + CI workflows pass                                    | Image tag `master-<sha>` exists in Gitea registry          |
| Helm chart + ArgoCD Application         | 2026-04-23  | `charts/reddit-spy`, `clusters/k3d-ai/applications/reddit-spy.yaml`         | `helm lint` + server-side dry-run both clean               |
| Cutover (data migration + ArgoCD sync)  | 2026-04-24  | Stop old instance; run migration; sync Application                          | Pod Running; logs healthy; first match shaped by vLLM      |
| 7-day observation                       | 2026-05-01  | Watch rolling-digest behaviour across weekly-release threads                | No duplicate digests, no dropped matches, no restart loops |
| GitHub repo archived                    | 2026-05-02  | Archive `github.com/mriley/reddit-spy`                                      | GitHub shows archived state                                |

---

## Risks & Mitigations

| Risk                                                                   | Likelihood | Impact | Mitigation                                                                                              | Owner |
| ---------------------------------------------------------------------- | ---------- | ------ | ------------------------------------------------------------------------------------------------------- | ----- |
| vLLM latency/availability tanks digest quality                         | M          | M      | Hard timeout + fallback to raw selftext; surface LLM fallback rate in logs/metrics                      | Pedro |
| Discord API rate limits edits during burst days                        | L          | M      | `discordgo` handles 429 backoff natively; we respect it; only one edit per match per channel            | Pedro |
| Phoenix-day boundary miscomputed (off-by-one)                          | L          | M      | Unit test `dayLocal` calc across midnight; use `time.LoadLocation`, fail-fast if TZ data missing        | Pedro |
| Postgres migration drops rows (notifications lost → duplicate digests) | L          | H      | Row-count parity check post-restore; hold cutover until verified; keep old instance paused, not deleted | Pedro |
| Original Discord message deleted mid-day by Pedro                      | L          | L      | Edit-404 fallback creates fresh message and overwrites `rolling_posts.discord_message_id`               | Pedro |
| Two pods accidentally running (rolling update ignores `Recreate`)      | L          | H      | `strategy: Recreate` + `replicas: 1`; reviewer guardrail in chart review                                | Pedro |
| Gitea Actions runner missing Docker buildx                             | L          | M      | Mirror the working pattern from `build-stash-whisparr-sync.yaml`                                        | Pedro |
| Bot lacks permission to edit in a channel                              | L          | L      | Bot edits its own messages with just SEND_MESSAGES; if a future channel blocks it, surface error log    | Pedro |

---

## Appendix

### Related Documents

| Document                      | Description                                                           | Link                                                                                                |
| ----------------------------- | --------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------- |
| Implementation plan           | Full technical detail (files, phases, schema, prompts, cluster facts) | `~/.claude/plans/i-would-like-to-resilient-pnueli.md`                                               |
| k3d conventions reference     | ArgoCD Application pattern, SealedSecret pattern, vLLM service        | `/home/mriley/projects/k3d-deployments/clusters/k3d-ai/applications/conversation-intelligence.yaml` |
| Gitea Actions build reference | Canonical Gitea Actions workflow to mirror                            | `/home/mriley/projects/k3d-deployments/.gitea/workflows/build-stash-whisparr-sync.yaml`             |
| Current Discord post assembly | What we're replacing                                                  | `internal/discord/discord.go:100`                                                                   |

### Research / Data

| Source                                                          | Summary                                                                                              |
| --------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------- |
| Example Discord output shared by Pedro (2026-04-16)             | r/Metalcore weekly-release thread hit as one of 3 embeds in a day — concrete case for rolling digest |
| vLLM usage in `charts/conversation-intelligence.yaml`           | Confirms in-cluster model `Qwen/Qwen3-14B-AWQ` on service `vllm:8000` is production-ready            |
| Existing `postgres-ai` consumer pattern (glitchtip, conv-intel) | Confirms shared-Postgres + per-app DB + per-app sealed credentials is the house norm                 |

---

## Approval

| Role          | Name  | Status      | Date       |
| ------------- | ----- | ----------- | ---------- |
| Product + Eng | Pedro | [ ] Pending | 2026-04-16 |

---

## Document History

| Version | Date       | Author | Changes                                           |
| ------- | ---------- | ------ | ------------------------------------------------- |
| 1.0     | 2026-04-16 | Pedro  | Initial draft covering digests, deploy, and Gitea |
| 1.1     | 2026-04-16 | Pedro  | Added Implementation Plan + Progress sections     |

---

## Implementation Plan

> Added by the `prd-implementation-planning` skill on 2026-04-16. Grounds each
> task in the concrete files identified in
> `~/.claude/plans/i-would-like-to-resilient-pnueli.md`.

### Skill Requirements

| Domain                  | Skills Required                               | Purpose                                                                     |
| ----------------------- | --------------------------------------------- | --------------------------------------------------------------------------- |
| Project setup           | `check-history`, `setup-go`                   | Context gathering + Go toolchain sanity on both repos                       |
| Go implementation       | `go-code-reviewer` (agent)                    | Review LLM wrapper, rolling-digest logic, Discord edit path, schema init    |
| Quality gates           | `quality-check`, `security-scan`, `run-tests` | Auto-invoked inside `safe-commit`; secrets scan critical for SealedSecrets  |
| Helm chart              | `helm-chart-writing`                          | Scaffold `charts/reddit-spy/`; lint; `kubectl apply --dry-run=server`       |
| ArgoCD / GitOps         | `helm-argocd-gitops`                          | Application manifest at `clusters/k3d-ai/applications/reddit-spy.yaml`      |
| Documentation           | `migration-guide-writer`                      | `docs/migration-to-ai-cluster.md` runbook (pg_dump → restore → cutover)     |
| Git ceremony            | `safe-commit`, `create-pr`, `manage-branch`   | Branch creation, commits, PRs on both reddit-spy and k3d-deployments        |
| CI (no dedicated skill) | N/A                                           | Gitea Actions workflow authoring — mirror existing k3d-deployments patterns |

### Implementation Tasks

| #   | Task                                                                                                      | User Story | Skill                       | Priority | Dependencies | Est. |
| --- | --------------------------------------------------------------------------------------------------------- | ---------- | --------------------------- | -------- | ------------ | ---- |
| 1   | Run `check-history` on reddit-spy + k3d-deployments; run `setup-go` in reddit-spy                         | All        | `check-history`, `setup-go` | P0       | None         | XS   |
| 2   | Add `github.com/sashabaranov/go-openai` dep; scaffold `internal/llm/client.go` (BaseURL-configurable)     | US-2       | `go-code-reviewer`          | P0       | 1            | S    |
| 3   | Implement `internal/llm/shaper.go` + `prompts.go` (Fresh + Update modes, 3800-char clip)                  | US-2       | `go-code-reviewer`          | P0       | 2            | M    |
| 4   | Write `internal/llm/shaper_test.go` — stubbed transport, Fresh vs Update prompt assertions                | US-2       | `run-tests`                 | P0       | 3            | M    |
| 5   | Author `scripts/schema.sql` — full DDL for all existing tables + new `rolling_posts` (`IF NOT EXISTS`)    | US-1, US-3 | N/A (SQL)                   | P0       | 1            | S    |
| 6   | Add `RollingPost` type + `GetRollingPost`/`UpsertRollingPost` in `internal/dbstore/store.go`              | US-1       | `go-code-reviewer`          | P0       | 5            | M    |
| 7   | Add schema-bootstrap call in `main.go` (runs `schema.sql` idempotently on startup)                        | US-3       | `go-code-reviewer`          | P0       | 5, 6         | S    |
| 8   | Wire `LLM_BASE_URL`, `LLM_MODEL`, `LLM_API_KEY`, `LLM_TIMEOUT`, `TZ`, `LOG_LEVEL` env in `main.go`        | US-2, US-3 | `go-code-reviewer`          | P0       | 3            | S    |
| 9   | Rewrite `internal/discord/discord.go:SendMessage` — Phoenix-day, Fresh/Edit branches, 404 fallback        | US-1, US-2 | `go-code-reviewer`          | P0       | 6, 8         | L    |
| 10  | Table-driven tests for `SendMessage`: first-match send, same-day edit, next-day send, 404-delete fallback | US-1       | `run-tests`                 | P0       | 9            | M    |
| 11  | Drop `HEALTHCHECK pgrep` line from `Dockerfile`                                                           | US-3       | N/A                         | P1       | None         | XS   |
| 12  | Update `makefile` + `Dockerfile` image ref → `gitea.cmtriley.com/mriley/reddit-spy`                       | US-4       | N/A                         | P0       | None         | XS   |
| 13  | Port `.github/workflows/ci.yml` → `.gitea/workflows/ci.yml` (golangci-lint, go test -race, govulncheck)   | US-4       | N/A                         | P0       | None         | S    |
| 14  | Author `.gitea/workflows/build.yaml` (buildx, login with GITEAUSERNAME/GITEATOKEN, push)                  | US-4       | N/A                         | P0       | 12           | S    |
| 15  | Delete `.github/workflows/` directory                                                                     | US-4       | N/A                         | P1       | 13, 14       | XS   |
| 16  | Run `go-code-reviewer` agent across `internal/{llm,discord,dbstore}` changes; address findings            | US-1, US-2 | `go-code-reviewer` (agent)  | P0       | 3, 6, 9, 10  | M    |
| 17  | Create Gitea repo `mriley/reddit-spy`; push master + tags; verify Gitea Actions CI + build green          | US-4       | `manage-branch`             | P0       | 13, 14, 15   | S    |
| 18  | `safe-commit` + `create-pr` for reddit-spy app changes (LLM + rolling digest + schema + Gitea migration)  | All        | `safe-commit`, `create-pr`  | P0       | 16, 17       | XS   |
| 19  | Scaffold `charts/reddit-spy/` in k3d-deployments — Chart.yaml, values.yaml, templates/ (follow vllm)      | US-3       | `helm-chart-writing`        | P0       | None         | M    |
| 20  | Generate 3 SealedSecrets with `kubeseal` (`reddit-spy-discord`, `-db-credentials`, `-llm`)                | US-3       | N/A (`kubeseal`)            | P0       | 19           | S    |
| 21  | Author `clusters/k3d-ai/applications/reddit-spy.yaml` ArgoCD Application (mirror conv-intel shape)        | US-3       | `helm-argocd-gitops`        | P0       | 19           | S    |
| 22  | `helm lint` + `helm template … \| kubectl apply --dry-run=server -f -` both clean                         | US-3       | `helm-chart-writing`        | P0       | 19, 20, 21   | XS   |
| 23  | `safe-commit` + `create-pr` for k3d-deployments changes (chart + Application)                             | US-3       | `safe-commit`, `create-pr`  | P0       | 22           | XS   |
| 24  | Create `reddit_spy` role + database on `postgres-ai` (one-off psql)                                       | US-5       | N/A (SQL)                   | P0       | 20           | S    |
| 25  | Author `docs/migration-to-ai-cluster.md` runbook (dump flags, psql invocation, row-count parity check)    | US-5       | `migration-guide-writer`    | P0       | None         | S    |
| 26  | Execute migration per runbook: apply schema, `pg_dump` old, restore into `reddit_spy`, verify row counts  | US-5       | N/A                         | P0       | 7, 24, 25    | M    |
| 27  | Cutover: stop old instance; ArgoCD sync; verify pod Running + LLM call + first Discord digest             | US-3, US-5 | N/A                         | P0       | 23, 26       | M    |
| 28  | 7-day observation: watch a weekly-release sub for correct rolling-edit, zero duplicates, no restart loops | All        | N/A                         | P1       | 27           | L    |
| 29  | Archive GitHub repo `github.com/mriley/reddit-spy`                                                        | US-4       | N/A                         | P2       | 28           | XS   |

### Implementation Notes

- **Two repos, two PRs.** Tasks 1–18 land in `reddit-spy`. Tasks 19–23 land in
  `k3d-deployments`. The ArgoCD Application's `spec.source.targetRevision`
  won't deploy until the k3d-deployments PR is merged — sequence the merges
  before Task 24.
- **Secrets never plaintext.** Task 20 uses the `kubeseal` binary vendored at
  `/home/mriley/projects/k3d-deployments/kubeseal`. `safe-commit`'s
  `security-scan` gate must pass — SealedSecrets are safe to commit, raw
  `Secret` manifests are not.
- **No `safe-commit` skipping on Dockerfile / Makefile edits (Tasks 11, 12)** —
  those ship in the same PR as Tasks 13, 14, 15 to keep CI green.
- **Task 17 unblocks Gitea Actions image availability.** The ArgoCD
  Application (Task 21) pins an image tag from Gitea's registry, so Task 17
  must produce at least one image before Task 27 cutover.
- **Task 26 (row-count parity) is the gate for Task 27.** Hold cutover until
  row counts match source-to-destination for all migrated tables.
- **Task 28 intentionally non-blocking.** Use the observation window to
  decide on the four PRD open questions (LLM tone knob, `rolling_posts`
  retention, source DB location confirmation, GitHub archive timing).

---

## Implementation Progress

| #   | Task                                                                 | Status        | Started    | Completed  | Commit / PR                                                                               |
| --- | -------------------------------------------------------------------- | ------------- | ---------- | ---------- | ----------------------------------------------------------------------------------------- |
| 1   | check-history + setup-go bootstrap                                   | Done          | 2026-04-16 | 2026-04-16 | baseline clean (lint/build/test green)                                                    |
| 2   | Add go-openai dep + `internal/llm/client.go`                         | Done          | 2026-04-16 | 2026-04-16 | `88ea59f`                                                                                 |
| 3   | `internal/llm/shaper.go` + `prompts.go`                              | Done          | 2026-04-16 | 2026-04-16 | `88ea59f`                                                                                 |
| 4   | `internal/llm/shaper_test.go`                                        | Done          | 2026-04-16 | 2026-04-16 | `88ea59f` + `471bde2` (malformed-json cases)                                              |
| 5   | `internal/dbstore/sql/schema.sql`                                    | Done          | 2026-04-16 | 2026-04-16 | `88ea59f` (+ `471bde2` CHECK constraint)                                                  |
| 6   | `RollingPost` + Get/Upsert in `internal/dbstore/store.go`            | Done          | 2026-04-16 | 2026-04-16 | `88ea59f`                                                                                 |
| 7   | Schema bootstrap on startup in `main.go`                             | Done          | 2026-04-16 | 2026-04-16 | `88ea59f`                                                                                 |
| 8   | Wire `LLM_*` / `TZ` / `LOG_LEVEL` env in `main.go`                   | Done          | 2026-04-16 | 2026-04-16 | `88ea59f`                                                                                 |
| 9   | Rewrite `SendMessage` — Fresh/Edit branches + 404 fallback           | Done          | 2026-04-16 | 2026-04-16 | `88ea59f` + `471bde2` (403 no longer swallowed)                                           |
| 10  | `SendMessage` table-driven tests                                     | Done          | 2026-04-16 | 2026-04-16 | `88ea59f` + `471bde2` (Phoenix-day edge case)                                             |
| 11  | Drop `HEALTHCHECK pgrep` from `Dockerfile`                           | Done          | 2026-04-16 | 2026-04-16 | `c38a5bb`                                                                                 |
| 12  | Update `makefile`/`Dockerfile` registry → Gitea                      | Done          | 2026-04-16 | 2026-04-16 | `c38a5bb`                                                                                 |
| 13  | Port CI to `.gitea/workflows/ci.yml`                                 | Done          | 2026-04-16 | 2026-04-16 | `c38a5bb`                                                                                 |
| 14  | Author `.gitea/workflows/build.yaml`                                 | Done          | 2026-04-16 | 2026-04-16 | `c38a5bb`                                                                                 |
| 15  | Delete `.github/workflows/`                                          | Done          | 2026-04-16 | 2026-04-16 | `c38a5bb`                                                                                 |
| 16  | `go-code-reviewer` agent sweep                                       | Done          | 2026-04-16 | 2026-04-16 | `471bde2` addresses all blocking + important findings                                     |
| 17  | Create Gitea repo, push, verify CI green                             | Done          | 2026-04-16 | 2026-04-16 | `gitea.cmtriley.com/mriley/reddit-spy`; CI runs 1–3 all success                           |
| 18  | `safe-commit` + `create-pr` (reddit-spy)                             | Done          | 2026-04-16 | 2026-04-16 | https://gitea.cmtriley.com/mriley/reddit-spy/pulls/1                                      |
| 19  | Scaffold `charts/reddit-spy/` in k3d-deployments                     | Done          | 2026-04-16 | 2026-04-16 | `ba465525` (k3d-deployments)                                                              |
| 20  | Generate SealedSecrets (discord, db, llm)                            | Partial       | 2026-04-16 | -          | LLM sealed (`ba465525`); discord + db sealing runbooked in `charts/reddit-spy/SECRETS.md` |
| 21  | ArgoCD Application at `clusters/k3d-ai/applications/reddit-spy.yaml` | Done          | 2026-04-16 | 2026-04-16 | `ba465525` (k3d-deployments)                                                              |
| 22  | `helm lint` + server-side dry-run                                    | Done          | 2026-04-16 | 2026-04-16 | 4/4 resources valid against live k3d-ai                                                   |
| 23  | `safe-commit` + `create-pr` (k3d-deployments)                        | Done          | 2026-04-16 | 2026-04-16 | https://gitea.cmtriley.com/mriley/k3d-deployments/pulls/212                               |
| 24  | Create `reddit_spy` DB + role on `postgres-ai`                       | Done (folded) | 2026-04-16 | 2026-04-16 | Absorbed into `dbstore.Bootstrap` via `POSTGRES_ADMIN_URL`                                |
| 25  | Author `docs/migration-to-ai-cluster.md` runbook                     | Done          | 2026-04-16 | 2026-04-16 | committed alongside PRD v1.2 in this branch                                               |
| 26  | Execute migration (pg_dump → restore → row-count parity)             | Blocked       | -          | -          | Needs source DB DSN from operator                                                         |
| 27  | Cutover (stop old, ArgoCD sync, verify pod + LLM + first digest)     | Blocked       | -          | -          | Gated on #20 (discord + db sealing), #26, PR merges                                       |
| 28  | 7-day observation                                                    | Pending       | -          | -          | Starts after #27                                                                          |
| 29  | Archive GitHub repo                                                  | Pending       | -          | -          | Manual after #28 clean                                                                    |

### Progress Summary

- **Total Tasks:** 29
- **Completed:** 25 (86%)
- **Partial:** 1 (#20 — 1 of 3 SealedSecrets produced)
- **Blocked:** 2 (#26, #27 — operator actions)
- **Pending:** 1 (#28 starts after cutover, #29 after observation)
- **Last Updated:** 2026-04-16

### Operator handoff (what Pedro does next)

1. Seal `reddit-spy-discord` + `reddit-spy-db-credentials` per
   `charts/reddit-spy/SECRETS.md` in the k3d-deployments repo.
2. Review + merge PR [#1 on reddit-spy](https://gitea.cmtriley.com/mriley/reddit-spy/pulls/1)
   to trigger the Gitea Actions image build.
3. Once the image tag appears in the Gitea registry, review + merge
   [PR #212 on k3d-deployments](https://gitea.cmtriley.com/mriley/k3d-deployments/pulls/212)
   so ArgoCD picks up the new Application.
4. Follow `docs/migration-to-ai-cluster.md` to run the pg_dump, apply the
   schema, restore, diff row counts, then cut over.
5. Observe for a Phoenix week; archive the GitHub repo once green.

**Commit convention:** tag commits with `[PRD Task N]` so progress can be
auto-tracked. Example: `feat(llm): add vLLM shaper with Fresh/Update modes [PRD Task 3]`.
