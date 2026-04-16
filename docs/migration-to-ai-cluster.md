# Migrating reddit-spy to the k3d-ai cluster

This runbook moves reddit-spy's Postgres data to the shared `postgres-ai`
instance in the `ai` namespace of the k3d-ai cluster, and cuts over from the
old standalone instance to the in-cluster ArgoCD-managed Deployment. It
covers PRD Tasks 24–28.

> **Reversibility.** Step 3 only reads from the source database. Step 4
> writes to the new `reddit_spy` database on `postgres-ai` but does nothing
> to the old instance. Step 7 (cutover) is the first destructive action; by
> that point row counts have been verified and the old instance is paused,
> not deleted.

## Prerequisites

- `kubectl` pointed at `k3d-ai` (verify: `kubectl config current-context`)
- Superuser DSN for `postgres-ai` (usually `postgres://postgres:<PW>@postgres-ai.ai.svc.cluster.local:5432/postgres`)
- Source DB DSN (wherever reddit-spy currently runs)
- `kubeseal` on `$PATH` (available at `./kubeseal` in the k3d-deployments repo)
- Both PRs merged on Gitea:
  - https://gitea.cmtriley.com/mriley/reddit-spy — app code
  - https://gitea.cmtriley.com/mriley/k3d-deployments — Helm chart + Application
- The Gitea Actions build workflow has produced at least one image tag at
  `gitea.cmtriley.com/mriley/reddit-spy:master-<sha>`

## 1. Port-forward postgres-ai so you can psql from the workstation

```bash
kubectl -n ai port-forward svc/postgres-ai 55432:5432 &
PF_PID=$!
trap "kill $PF_PID" EXIT
export ADMIN_URL='postgres://postgres:<SUPERUSER_PW>@127.0.0.1:55432/postgres'
```

## 2. Provision the reddit_spy role and database

Two equivalent options — use whichever matches your workflow.

### 2a. Let reddit-spy do it on first startup (recommended)

Seal `reddit-spy-admin-db-url` per `charts/reddit-spy/SECRETS.md` section 3
and uncomment the `postgres.adminURLExistingSecret*` lines in
`clusters/k3d-ai/applications/reddit-spy.yaml`. On first sync the pod runs
`dbstore.Bootstrap()` and creates the role + database idempotently. Remove
the admin SealedSecret and the `adminURLExistingSecret*` lines from the
Application after the first sync succeeds.

### 2b. Provision manually, then let the app run schema.sql only

```bash
psql "$ADMIN_URL" <<'SQL'
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'reddit_spy') THEN
        EXECUTE 'CREATE ROLE "reddit_spy" LOGIN PASSWORD ''<CHOOSE_A_PASSWORD>''';
    END IF;
END
$$;

SELECT 'CREATE DATABASE "reddit_spy" OWNER "reddit_spy"'
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = 'reddit_spy')
\gexec
SQL
```

Then seal `reddit-spy-db-credentials` with that password per `SECRETS.md`.

## 3. Capture row counts from the source

```bash
export SOURCE_URL='postgres://<OLD_USER>:<OLD_PW>@<OLD_HOST>/<OLD_DB>'
for t in discord_servers discord_channels subreddits rules posts notifications; do
  echo -n "$t "
  psql "$SOURCE_URL" -Atc "SELECT count(*) FROM $t"
done | tee /tmp/reddit-spy-src-counts.txt
```

Keep the file — you'll diff it against the destination in step 6.

## 4. Dump data from the source

```bash
pg_dump \
  --data-only --no-owner --no-privileges \
  --table=discord_servers \
  --table=discord_channels \
  --table=subreddits \
  --table=rules \
  --table=posts \
  --table=notifications \
  "$SOURCE_URL" > /tmp/reddit-spy-data.sql
```

`rolling_posts` is intentionally excluded — it doesn't exist on the source,
and the first match after cutover will populate it naturally.

## 5. Apply schema + restore into reddit_spy

The app's startup will also run `schema.sql`, so this step is only needed
if you want the DB ready before the pod boots (useful for sanity-checking
the restore in isolation).

```bash
export DEST_URL='postgres://reddit_spy:<DB_PASSWORD>@127.0.0.1:55432/reddit_spy'

# Schema (extracted from the embedded file for convenience)
psql "$DEST_URL" < /path/to/reddit-spy/internal/dbstore/sql/schema.sql

# Data
psql "$DEST_URL" < /tmp/reddit-spy-data.sql
```

## 6. Verify row counts match

```bash
for t in discord_servers discord_channels subreddits rules posts notifications; do
  echo -n "$t "
  psql "$DEST_URL" -Atc "SELECT count(*) FROM $t"
done | tee /tmp/reddit-spy-dst-counts.txt

diff /tmp/reddit-spy-src-counts.txt /tmp/reddit-spy-dst-counts.txt
```

Empty diff → good to proceed. Any difference → **stop**, investigate, do
not cut over.

## 7. Cutover

1. **Pause the old instance** (do not delete yet — keep it as a rollback).
   - systemd: `sudo systemctl stop reddit-spy`
   - docker: `docker stop reddit-spy`
   - k8s elsewhere: `kubectl scale deploy/reddit-spy --replicas=0`
2. **Sync the ArgoCD Application.** Either click _Sync_ in the ArgoCD UI or
   run `argocd app sync reddit-spy` on a host with the CLI.
3. **Watch the pod start.**

   ```bash
   kubectl -n ai get pods -l app.kubernetes.io/name=reddit-spy -w
   kubectl -n ai logs deploy/reddit-spy -f
   ```

   Expected log lines:

   ```
   msg="starting reddit-spy" version=master-<sha>
   msg="llm enabled" base_url=http://vllm.ai.svc.cluster.local:8000/v1 model=Qwen/Qwen3-14B-AWQ timeout=30s
   msg="starting poller" subreddit=<each one>
   ```

4. **Trigger a test match.** Add a temporary low-frequency rule against a
   currently-quiet subreddit, wait one poll tick (~30s), confirm one Discord
   embed appears. Watch across a second poll tick to confirm no duplicate.

## 8. Observation window (7 days)

- No duplicate digests in any channel during a high-volume sub's day.
- `rolling_posts` edit path taken on later matches (`kubectl -n ai logs` will
  not log these directly; check Discord UI for the "edited" marker).
- Pod restart count stays at 0.

If any of the above trips, roll back via Step 7.1 inverse: scale ArgoCD
Application to zero (or disable its `automated` sync), then restart the old
instance. The old database is still intact — the new one only contains
deltas collected since cutover.

## 9. Decommission the old instance (after 7 clean days)

1. Archive any final `.env` / config files from the old host.
2. Delete the old systemd unit / docker container / k8s Deployment.
3. Drop the old database (or leave it read-only as a cold archive).
4. Archive the GitHub repo `github.com/meriley/reddit-spy`.

## Rollback hatchets

- **ArgoCD sync broken?** Toggle `spec.syncPolicy.automated` off in the
  Application YAML and investigate manually.
- **schema.sql fails on startup?** Disable the ArgoCD Application, hand-apply
  a patched schema, re-enable.
- **vLLM offline?** The shaper falls back to raw selftext automatically;
  no migration action required. Investigate vLLM separately.
- **Data drift discovered post-cutover?** Re-run steps 4–6 against the
  post-cutover source (capture delta since cutover) and `UPSERT` manually.
